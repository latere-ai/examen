// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// Gradings keeps the check runs and judgments Examen runs of finished
// trials.
func (s *Store) Gradings() store.Gradings { return gradings{s} }

type gradings struct{ s *Store }

const gradingColumns = `id, experiment, cell, trial, grader, kind, position, state, attempts, record`

func scanGrading(row pgx.Row) (objects.Grading, error) {
	var g objects.Grading
	var record []byte
	if err := row.Scan(&g.ID, &g.Experiment, &g.Cell, &g.Trial, &g.Grader, &g.Kind, &g.Position, &g.State, &g.Attempts, &record); err != nil {
		return g, err
	}
	if len(record) > 0 {
		g.Record = json.RawMessage(record)
	}
	return g, nil
}

func collectGradings(rows pgx.Rows, what string) ([]objects.Grading, error) {
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (objects.Grading, error) { return scanGrading(row) })
	if err != nil {
		return nil, fmt.Errorf("postgres: reading %s: %w", what, err)
	}
	return out, nil
}

// gradingID is a grading's key: its trial and its place among the trial's
// gradings, which no other grading of the trial holds.
func gradingID(trial string, position int) string { return trial + "." + strconv.Itoa(position) }

// grade makes a finished trial's gradings pending, due at once.
func (c *change) grade(ctx context.Context, t objects.Trial, list []objects.NewGrading) error {
	for _, g := range list {
		if _, err := c.tx.Exec(ctx, `INSERT INTO gradings (id, owner, experiment, cell, trial, grader, kind, position, state, due_at, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'pending', $9, $9, $9) ON CONFLICT (trial, grader) DO NOTHING`,
			gradingID(t.ID, g.Position), t.Owner, t.Experiment, t.Cell, t.ID, g.Grader, g.Kind, g.Position, c.now); err != nil {
			return fmt.Errorf("postgres: making a trial's gradings: %w", err)
		}
	}
	return nil
}

// endGradings ends the pending gradings of a cell that parked or was
// canceled, not_run with the reason, since the trials they would grade are
// no longer read toward the cell.
func (c *change) endGradings(ctx context.Context, cell, reason string) error {
	record, err := marshal(map[string]string{"state": objects.CheckNotRun, "reason": reason})
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(ctx, `UPDATE gradings SET state = 'not_run', record = jsonb_build_object('grader', grader) || $2::jsonb,
			held_until = NULL, ended_at = $3, updated_at = $3
		WHERE cell = $1 AND state = 'pending'`, cell, record, c.now); err != nil {
		return fmt.Errorf("postgres: ending a cell's gradings: %w", err)
	}
	return nil
}

// graded reads, for a cell's view, the trials with a grading not ended,
// those with a check run not ended, and the first check run of each trial
// that ended in a state other than ok.
func (c *change) graded(ctx context.Context, view *execute.Cell) error {
	rows, err := c.tx.Query(ctx, `SELECT trial, grader, kind, state FROM gradings WHERE cell = $1 ORDER BY trial, position`, view.ID)
	if err != nil {
		return fmt.Errorf("postgres: reading a cell's gradings: %w", err)
	}
	defer rows.Close()
	view.Ungraded, view.Checking, view.CheckFailed = map[string]bool{}, map[string]bool{}, map[string]string{}
	for rows.Next() {
		var trial, grader, kind, state string
		if err := rows.Scan(&trial, &grader, &kind, &state); err != nil {
			return fmt.Errorf("postgres: reading a cell's grading: %w", err)
		}
		switch {
		case state == objects.GradingPending:
			view.Ungraded[trial] = true
			if kind == objects.GraderCheck {
				view.Checking[trial] = true
			}
		case kind == objects.GraderCheck && state != objects.CheckOK && view.CheckFailed[trial] == "":
			view.CheckFailed[trial] = grader + ": " + state
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("postgres: reading a cell's gradings: %w", err)
	}
	return nil
}

// Due answers pending gradings a run may start on.
func (g gradings) Due(ctx context.Context, limit int) ([]objects.Grading, error) {
	rows, err := g.s.pool.Query(ctx, `SELECT `+prefixed("g.", gradingColumns)+` FROM gradings g JOIN experiments e ON e.id = g.experiment
		WHERE g.state = 'pending' AND g.due_at <= coalesce($1::timestamptz, now()) AND (g.held_until IS NULL OR g.held_until <= coalesce($1::timestamptz, now()))
			AND e.state NOT IN ('complete', 'canceled')
			AND (g.kind = 'check' OR NOT EXISTS (SELECT 1 FROM gradings c WHERE c.trial = g.trial AND c.kind = 'check' AND c.state = 'pending'))
		ORDER BY g.due_at, g.trial, g.position LIMIT $2`, g.s.at(), limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the gradings due: %w", err)
	}
	return collectGradings(rows, "the gradings due")
}

// prefixed qualifies each of a list of columns with a table's alias.
func prefixed(alias, columns string) string {
	cols := strings.Split(columns, ",")
	for i, c := range cols {
		cols[i] = alias + strings.TrimSpace(c)
	}
	return strings.Join(cols, ", ")
}

// Hold takes the named gradings for a run for the duration.
func (g gradings) Hold(ctx context.Context, ids []string, hold time.Duration) ([]string, error) {
	rows, err := g.s.pool.Query(ctx, `UPDATE gradings SET held_until = coalesce($2::timestamptz, now()) + $3::interval, updated_at = coalesce($2::timestamptz, now())
		WHERE id = ANY($1) AND state = 'pending' AND (held_until IS NULL OR held_until <= coalesce($2::timestamptz, now())) RETURNING id`, ids, g.s.at(), interval(hold))
	if err != nil {
		return nil, fmt.Errorf("postgres: holding gradings: %w", err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("postgres: holding gradings: %w", err)
	}
	return out, nil
}

// Wait leaves a held grading pending, due again after the duration.
func (g gradings) Wait(ctx context.Context, id string, after time.Duration, attempts int) error {
	if _, err := g.s.pool.Exec(ctx, `UPDATE gradings SET held_until = NULL, due_at = coalesce($4::timestamptz, now()) + $2::interval, attempts = $3,
			updated_at = coalesce($4::timestamptz, now())
		WHERE id = $1 AND state = 'pending'`, id, interval(after), attempts, g.s.at()); err != nil {
		return fmt.Errorf("postgres: putting off a grading: %w", err)
	}
	return nil
}

// End ends a pending grading and advances its trial's cell.
func (g gradings) End(ctx context.Context, end store.GradingEnd) error {
	var eid string
	err := g.s.pool.QueryRow(ctx, `SELECT experiment FROM gradings WHERE id = $1`, end.ID).Scan(&eid)
	if err := one(err, "a grading"); err != nil {
		return err
	}
	x := experiments(g)
	return x.begin(ctx, eid, func(c *change) error {
		gr, err := scanGrading(c.tx.QueryRow(ctx, `SELECT `+gradingColumns+` FROM gradings WHERE id = $1 FOR UPDATE`, end.ID))
		if err := one(err, "a grading"); err != nil {
			return err
		}
		if gr.State != objects.GradingPending {
			return nil
		}
		t, err := scanTrial(c.tx.QueryRow(ctx, `SELECT `+trialColumns+` FROM trials WHERE id = $1`, gr.Trial))
		if err := one(err, "a graded trial"); err != nil {
			return err
		}
		var generation *int
		if len(end.Values) > 0 {
			n, err := c.grading(ctx, t, gr.Grader, end.Values)
			if err != nil {
				return err
			}
			generation = &n
		}
		record, err := recordWith(end.Record, generation)
		if err != nil {
			return err
		}
		if _, err := c.tx.Exec(ctx, `UPDATE gradings SET state = $2, record = $3, generation = $4, held_until = NULL, ended_at = $5, updated_at = $5
			WHERE id = $1`, gr.ID, end.State, record, generation, c.now); err != nil {
			return fmt.Errorf("postgres: ending a grading: %w", err)
		}
		if len(end.Values) > 0 {
			if err := c.dirty(ctx); err != nil {
				return err
			}
		}
		cell, trials, err := c.cell(ctx, gr.Cell)
		if err != nil {
			return err
		}
		step := execute.Step{State: end.Park, Reason: end.ParkReason, CancelOpen: end.Park != ""}
		if end.Park == "" || objects.Parked(cell.State) || cell.State == objects.StateComplete {
			view := c.view(cell, trials)
			if err := c.graded(ctx, &view); err != nil {
				return err
			}
			step = execute.Graded(view)
		} else {
			for _, r := range cell.Rollouts {
				if r.State != objects.RolloutEnded {
					step.Ended = append(step.Ended, r.Key)
				}
			}
		}
		if err := c.apply(ctx, cell, step); err != nil {
			return err
		}
		return c.settle(ctx)
	})
}

// recordWith is a grading's record as stored, with the generation its run
// appended when it appended one.
func recordWith(record any, generation *int) (string, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("postgres: rendering a grading's record: %w", err)
	}
	if generation == nil {
		return string(raw), nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", fmt.Errorf("postgres: rendering a grading's record: %w", err)
	}
	m["generation"] = *generation
	return marshal(m)
}

// interval is a duration as Postgres reads an interval.
func interval(d time.Duration) string {
	return strconv.FormatInt(d.Microseconds(), 10) + " microseconds"
}

// OfTrials reads every grading of the trials.
func (g gradings) OfTrials(ctx context.Context, trials []string) ([]objects.Grading, error) {
	if len(trials) == 0 {
		return nil, nil
	}
	rows, err := g.s.pool.Query(ctx, `SELECT `+gradingColumns+` FROM gradings WHERE trial = ANY($1) ORDER BY trial, position`, trials)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading trials' gradings: %w", err)
	}
	return collectGradings(rows, "trials' gradings")
}

// HoldKey records a judge's Key.
func (g gradings) HoldKey(ctx context.Context, experiment, grader, keyID string) error {
	if _, err := g.s.pool.Exec(ctx, `INSERT INTO judge_keys (experiment, key_id, grader, created_at) VALUES ($1, $2, $3, coalesce($4::timestamptz, now()))
		ON CONFLICT (experiment, key_id) DO NOTHING`, experiment, keyID, grader, g.s.at()); err != nil {
		return fmt.Errorf("postgres: recording a judge's key: %w", err)
	}
	return nil
}

// UnreleasedKeys answers the judge Keys of ended experiments not yet
// released.
func (g gradings) UnreleasedKeys(ctx context.Context, limit int) ([]store.KeyHold, error) {
	rows, err := g.s.pool.Query(ctx, `SELECT k.experiment, e.owner, k.key_id FROM judge_keys k JOIN experiments e ON e.id = k.experiment
		WHERE k.released_at IS NULL AND e.state IN ('complete', 'canceled') ORDER BY k.created_at, k.key_id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the judge keys to release: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.KeyHold, error) {
		var h store.KeyHold
		err := row.Scan(&h.Experiment, &h.Owner, &h.KeyID)
		return h, err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the judge keys to release: %w", err)
	}
	return out, nil
}

// ReleasedKey records that a judge Key was released.
func (g gradings) ReleasedKey(ctx context.Context, experiment, keyID string) error {
	if _, err := g.s.pool.Exec(ctx, `UPDATE judge_keys SET released_at = coalesce($3::timestamptz, now()) WHERE experiment = $1 AND key_id = $2 AND released_at IS NULL`,
		experiment, keyID, g.s.at()); err != nil {
		return fmt.Errorf("postgres: recording a released judge key: %w", err)
	}
	return nil
}
