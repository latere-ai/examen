// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// The trials of an executor that starts its runs at a core itself, the
// `topos` executor (specs/005-execution.md): examend claims a pending
// trial within the experiment's parallelism, records the run the core
// started for it, ends it with what the core reports, and stops a run
// whose trial ended canceled while it went on.

func (x experiments) Driven(ctx context.Context, executor string, limit int) ([]string, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT experiment FROM (
			SELECT DISTINCT t.experiment FROM trials t JOIN cells c ON c.id = t.cell, jsonb_array_elements(c.rollouts) r
			WHERE t.state <> 'done' AND r->>'key' = t.rollout AND r->>'executor' = $1 AND r->>'state' = 'submitted'
			UNION
			SELECT DISTINCT experiment FROM trials WHERE stop_pending
		) d ORDER BY experiment LIMIT $2`, executor, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the experiments an executor drives: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the experiments an executor drives: %w", err)
	}
	return ids, nil
}

func (x experiments) Claim(ctx context.Context, eid string, cells []string) ([]objects.Trial, error) {
	var out []objects.Trial
	err := x.begin(ctx, eid, func(c *change) error {
		if c.e.Finished() || c.e.Mode != objects.ModeExecute || len(cells) == 0 {
			return nil
		}
		take, err := c.leasable(ctx, store.LeaseRequest{Experiment: eid, Cells: cells})
		if err != nil || len(take) == 0 {
			return err
		}
		rows, err := c.tx.Query(ctx, `UPDATE trials SET state = 'running', updated_at = $2
			WHERE id = ANY($1) AND state = 'pending' RETURNING `+trialColumns, take, c.now)
		if err != nil {
			return fmt.Errorf("postgres: claiming trials: %w", err)
		}
		claimed, err := collectTrials(rows)
		if err != nil {
			return err
		}
		order := map[string]int{}
		for i, tid := range take {
			order[tid] = i
		}
		slices.SortFunc(claimed, func(a, b objects.Trial) int { return order[a.ID] - order[b.ID] })
		for _, t := range claimed {
			if err := c.trialState(ctx, t); err != nil {
				return err
			}
		}
		out = claimed
		return nil
	})
	return out, err
}

func (x experiments) Started(ctx context.Context, tid, ref string, at time.Time) (bool, error) {
	t, err := x.Trial(ctx, tid)
	if err != nil {
		return false, err
	}
	stop := false
	err = x.begin(ctx, t.Experiment, func(c *change) error {
		var state, current string
		if err := c.tx.QueryRow(ctx, `SELECT state, executor_ref FROM trials WHERE id = $1 FOR UPDATE`, tid).Scan(&state, &current); err != nil {
			return fmt.Errorf("postgres: reading a trial to record its run: %w", err)
		}
		switch {
		case current != "" && current != ref:
			return fmt.Errorf("postgres: trial %s already names the run %s, not %s", tid, current, ref)
		case state == objects.StateDone:
			// The trial ended before the core answered, so the run it
			// started is stopped; the ref is kept to name the run stopped.
			stop = true
			_, err := c.tx.Exec(ctx, `UPDATE trials SET executor_ref = $2, stop_pending = true, updated_at = $3 WHERE id = $1`, tid, ref, c.now)
			return err
		case current == ref:
			return nil
		}
		_, err := c.tx.Exec(ctx, `UPDATE trials SET executor_ref = $2, started_at = $3, updated_at = $4 WHERE id = $1`, tid, ref, at.UTC(), c.now)
		return err
	})
	return stop, err
}

func (x experiments) End(ctx context.Context, tid string, o store.Outcome, stop bool) (objects.Trial, error) {
	t, err := x.Trial(ctx, tid)
	if err != nil {
		return t, err
	}
	var out objects.Trial
	err = x.begin(ctx, t.Experiment, func(c *change) error {
		t, err := scanTrial(c.tx.QueryRow(ctx, `SELECT `+trialColumns+` FROM trials WHERE id = $1 FOR UPDATE`, tid))
		if err != nil {
			return fmt.Errorf("postgres: reading a trial to end: %w", err)
		}
		if t.State == objects.StateDone {
			out = t
			return nil
		}
		if out, err = c.finish(ctx, t, o, "", ""); err != nil {
			return err
		}
		if stop {
			_, err = c.tx.Exec(ctx, `UPDATE trials SET stop_pending = true WHERE id = $1`, tid)
		}
		return err
	})
	return out, err
}

func (x experiments) Stopping(ctx context.Context, eid string) ([]objects.Trial, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT `+trialColumns+` FROM trials WHERE experiment = $1 AND stop_pending ORDER BY id`, eid)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the runs to stop: %w", err)
	}
	return collectTrials(rows)
}

func (x experiments) Stopped(ctx context.Context, tid string) error {
	if _, err := x.s.pool.Exec(ctx, `UPDATE trials SET stop_pending = false WHERE id = $1`, tid); err != nil {
		return fmt.Errorf("postgres: recording a stopped run: %w", err)
	}
	return nil
}
