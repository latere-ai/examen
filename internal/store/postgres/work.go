// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// change is one transaction that changes an experiment: the experiment
// read under its row lock, the transaction's time, and the journal its
// events go to.
type change struct {
	x   experiments
	tx  pgx.Tx
	e   objects.Experiment
	now time.Time
	j   *journal
}

// begin runs fn in a transaction that holds an experiment's row lock, and
// stores the log's next sequence when fn returns without an error.
func (x experiments) begin(ctx context.Context, eid string, fn func(*change) error) error {
	return pgx.BeginFunc(ctx, x.s.pool, func(tx pgx.Tx) error {
		now, err := x.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		var seq int64
		row := tx.QueryRow(ctx, `SELECT `+experimentColumns+`, next_seq FROM experiments WHERE id = $1 FOR UPDATE`, eid)
		e, err := scanLocked(row, &seq)
		if err := one(err, "an experiment"); err != nil {
			return err
		}
		c := &change{x: x, tx: tx, e: e, now: now, j: &journal{tx: tx, exp: e.ID, owner: e.Owner, seq: seq, now: now}}
		if err := fn(c); err != nil {
			return err
		}
		return c.j.close(ctx)
	})
}

func scanLocked(row pgx.Row, seq *int64) (objects.Experiment, error) {
	var e objects.Experiment
	var metadata, declared, independence []byte
	if err := row.Scan(&e.ID, &e.Owner, &e.Suite, &e.SuiteID, &e.SuiteName, &e.Candidates, &e.CandidateIDs, &e.Trials, &e.Budget.Name,
		&e.Parallelism, &e.Reference.Baseline, &e.Reference.Candidate, &e.Mode, &e.Scheduling, &declared, &independence, &e.State, &e.CreatedBy, &metadata, &e.IntakeClosed,
		&e.CreatedAt, &e.UpdatedAt, seq); err != nil {
		return objects.Experiment{}, err
	}
	if err := unmarshal(metadata, &e.Metadata); err != nil {
		return objects.Experiment{}, err
	}
	return e, declaredOf(declared, independence, &e)
}

// declaredOf reads what an experiment declared: its comparisons, an empty
// list when it declared none, and its test of independence, nil when it
// declared none.
func declaredOf(comparisons, independence []byte, e *objects.Experiment) error {
	if err := unmarshal(comparisons, &e.Comparisons); err != nil {
		return err
	}
	if e.Comparisons == nil {
		e.Comparisons = []objects.DeclaredComparison{}
	}
	return unmarshal(independence, &e.Independence)
}

// experimentState moves the experiment to a state and journals it, with
// the sink event of an end.
func (c *change) experimentState(ctx context.Context, state string) error {
	if c.e.State == state {
		return nil
	}
	if _, err := c.tx.Exec(ctx, `UPDATE experiments SET state = $2, updated_at = $3 WHERE id = $1`, c.e.ID, state, c.now); err != nil {
		return fmt.Errorf("postgres: changing an experiment's state: %w", err)
	}
	c.e.State = state
	p, err := progress(ctx, c.tx, c.e.ID)
	if err != nil {
		return err
	}
	if err := c.j.add(ctx, objects.EventExperiment, map[string]any{"id": c.e.ID, "state": state, "progress": p}); err != nil {
		return err
	}
	end := map[string]any{"id": c.e.ID, "state": state, "budget": c.e.Budget.Name, "created_by": c.e.CreatedBy}
	switch state {
	case objects.StateComplete:
		return c.j.add(ctx, objects.EventExperimentCompleted, end)
	case objects.StateCanceled:
		return c.j.add(ctx, objects.EventExperimentCanceled, end)
	}
	return nil
}

// cell reads one cell of the experiment under its row lock, with every
// trial it holds.
func (c *change) cell(ctx context.Context, cid string) (objects.Cell, []objects.Trial, error) {
	cell, err := scanCell(c.tx.QueryRow(ctx, `SELECT `+cellColumns+` FROM cells WHERE id = $1 AND experiment = $2 FOR UPDATE`, cid, c.e.ID))
	if err := one(err, "a cell"); err != nil {
		return objects.Cell{}, nil, err
	}
	rows, err := c.tx.Query(ctx, `SELECT `+trialColumns+` FROM trials WHERE cell = $1 ORDER BY id`, cid)
	if err != nil {
		return objects.Cell{}, nil, fmt.Errorf("postgres: reading a cell's trials: %w", err)
	}
	trials, err := collectTrials(rows)
	return cell, trials, err
}

// cellState moves a cell to a state with its parked reason, and journals
// it, with cell.parked for a parking state. A cell that completes passes
// through grading.
func (c *change) cellState(ctx context.Context, cell *objects.Cell, state, reason string) error {
	if cell.State == state {
		return nil
	}
	steps := []string{state}
	if state == objects.StateComplete && cell.State != objects.StateGrading {
		steps = []string{objects.StateGrading, objects.StateComplete}
	}
	for _, s := range steps {
		cell.State, cell.ParkedReason = s, ""
		if objects.Parked(s) {
			cell.ParkedReason = reason
		}
		if err := c.j.add(ctx, objects.EventCell, cellEvent(*cell)); err != nil {
			return err
		}
		if objects.Parked(s) {
			if err := c.j.add(ctx, objects.EventCellParked, cellEvent(*cell)); err != nil {
				return err
			}
		}
	}
	return nil
}

// saveCell writes a cell's rollouts, state and parked reason.
func (c *change) saveCell(ctx context.Context, cell objects.Cell) error {
	rollouts, err := marshal(cell.Rollouts)
	if err != nil {
		return err
	}
	if _, err := c.tx.Exec(ctx, `UPDATE cells SET rollouts = $2, state = $3, parked_reason = $4, updated_at = $5 WHERE id = $1`,
		cell.ID, rollouts, cell.State, cell.ParkedReason, c.now); err != nil {
		return fmt.Errorf("postgres: writing a cell: %w", err)
	}
	return nil
}

// trialState writes a trial's state and journals it.
func (c *change) trialState(ctx context.Context, t objects.Trial) error {
	return c.j.add(ctx, objects.EventTrial, trialEvent(t))
}

// endOpen ends a cell's trials not yet done as canceled, with their
// leases.
func (c *change) endOpen(ctx context.Context, cid, code, detail string) error {
	failure, err := marshal(objects.Failure{Code: code, Detail: detail})
	if err != nil {
		return err
	}
	rows, err := c.tx.Query(ctx, `UPDATE trials SET state = 'done', outcome = 'canceled', failure = $2, ended_at = $3, lease_expires_at = NULL, updated_at = $3
		WHERE cell = $1 AND state <> 'done' RETURNING `+trialColumns, cid, failure, c.now)
	if err != nil {
		return fmt.Errorf("postgres: canceling a cell's trials: %w", err)
	}
	ended, err := collectTrials(rows)
	if err != nil {
		return err
	}
	for _, t := range ended {
		if err := c.trialState(ctx, t); err != nil {
			return err
		}
	}
	return nil
}

// apply applies a decision to a cell: the trials it retries, the rollouts
// that ended, its open trials canceled, the rollouts it queues with their
// trials, and its new state.
func (c *change) apply(ctx context.Context, cell objects.Cell, s execute.Step) error {
	if len(s.Retried) > 0 {
		if _, err := c.tx.Exec(ctx, `UPDATE trials SET retried = true, updated_at = $2 WHERE id = ANY($1)`, s.Retried, c.now); err != nil {
			return fmt.Errorf("postgres: marking retried trials: %w", err)
		}
	}
	for i, r := range cell.Rollouts {
		if slices.Contains(s.Ended, r.Key) {
			cell.Rollouts[i].State = objects.RolloutEnded
		}
	}
	if s.CancelOpen {
		if err := c.endOpen(ctx, cell.ID, "cell_parked", "the cell parked "+s.State+" before the trial ended: "+s.Reason); err != nil {
			return err
		}
	}
	for _, q := range s.Queue {
		cell.Rollouts = append(cell.Rollouts, q.Rollout)
		trials := make([]store.RolloutTrial, len(q.Trials))
		for i, t := range q.Trials {
			trials[i] = store.RolloutTrial{Index: t.Index, RetryOf: t.RetryOf, Auto: t.Auto}
		}
		if err := insertTrials(ctx, c.tx, c.j, cell, q.Rollout.Key, trials, c.now); err != nil {
			return err
		}
	}
	if s.State != "" {
		if err := c.cellState(ctx, &cell, s.State, s.Reason); err != nil {
			return err
		}
	}
	return c.saveCell(ctx, cell)
}

// settle completes the experiment when every cell is complete or parked,
// and, in mode import, its intake is closed; its automatic comparisons are
// then final.
func (c *change) settle(ctx context.Context) error {
	if c.e.Finished() || (c.e.Mode == objects.ModeImport && !c.e.IntakeClosed) {
		return nil
	}
	var open int
	if err := c.tx.QueryRow(ctx, `SELECT count(*) FROM cells WHERE experiment = $1 AND state NOT IN ('complete', 'blocked', 'over_budget', 'canceled')`, c.e.ID).Scan(&open); err != nil {
		return fmt.Errorf("postgres: counting open cells: %w", err)
	}
	if open > 0 {
		return nil
	}
	if err := c.experimentState(ctx, objects.StateGrading); err != nil {
		return err
	}
	if err := c.experimentState(ctx, objects.StateComplete); err != nil {
		return err
	}
	if _, err := c.tx.Exec(ctx, `UPDATE comparisons SET final = true, dirty = true, updated_at = $2 WHERE experiment = $1 AND `+keptComparisons+` AND NOT final`, c.e.ID, c.now); err != nil {
		return fmt.Errorf("postgres: finalizing comparisons: %w", err)
	}
	return c.markDeclared(ctx)
}

// keptComparisons selects the comparisons Examen keeps of an experiment
// without being asked, which a trial reaching done marks and the
// experiment's end finalizes: the automatic ones and the declared ones.
const keptComparisons = `(automatic OR declared IS NOT NULL)`

// dirty marks the experiment's comparisons that are not final, whose
// inputs a trial reaching done changed, and its test of independence.
func (c *change) dirty(ctx context.Context) error {
	if _, err := c.tx.Exec(ctx, `UPDATE comparisons SET dirty = true, updated_at = $2 WHERE experiment = $1 AND `+keptComparisons+` AND NOT final`, c.e.ID, c.now); err != nil {
		return fmt.Errorf("postgres: marking comparisons: %w", err)
	}
	return c.markDeclared(ctx)
}

// markDeclared marks the declared analysis of an experiment that declares
// a test of independence for recomputation: the test reads every cell,
// whatever comparisons the experiment keeps.
func (c *change) markDeclared(ctx context.Context) error {
	if c.e.Independence == nil {
		return nil
	}
	if _, err := c.tx.Exec(ctx, `UPDATE experiments SET declared_dirty = true WHERE id = $1`, c.e.ID); err != nil {
		return fmt.Errorf("postgres: marking a declared analysis: %w", err)
	}
	return nil
}

// view is what a decision reads of a cell.
func (c *change) view(cell objects.Cell, trials []objects.Trial) execute.Cell {
	return execute.Cell{ID: cell.ID, State: cell.State, N: c.e.Trials, Rollouts: cell.Rollouts, Trials: trials}
}

// measure writes the gradings a finished trial gets at done: generation 1
// of builtin, the built-in decline judge's when the suite lists declined,
// and the next generation of the values the harness submitted itself; and
// journals each grading.
func (c *change) measure(ctx context.Context, t objects.Trial, o store.Outcome) error {
	if !t.Finished() {
		return nil
	}
	var builtin []store.Measured
	values := objects.BuiltinValues(t)
	for _, m := range objects.Builtins() {
		if v, ok := values[objects.BuiltinDigest(m.Name)]; ok {
			builtin = append(builtin, store.Measured{Metric: objects.BuiltinDigest(m.Name), Value: v})
		}
	}
	if err := c.grading(ctx, t, objects.GraderBuiltin, builtin); err != nil {
		return err
	}
	if o.Declined {
		if err := c.grading(ctx, t, objects.GraderDecline, []store.Measured{{Metric: objects.BuiltinDigest(objects.MetricDeclined), Value: objects.DeclineValue(t)}}); err != nil {
			return err
		}
	}
	if len(o.SelfReported) > 0 {
		return c.grading(ctx, t, objects.Self(o.Harness), o.SelfReported)
	}
	return nil
}

// grading appends one generation of a grader on a trial, its measurements
// in one statement each, and journals it.
func (c *change) grading(ctx context.Context, t objects.Trial, grader string, values []store.Measured) error {
	var generation int
	if err := c.tx.QueryRow(ctx, `SELECT coalesce(max(generation), 0) + 1 FROM measurements WHERE trial = $1 AND grader = $2 AND annotator = ''`, t.ID, grader).Scan(&generation); err != nil {
		return fmt.Errorf("postgres: numbering a grading: %w", err)
	}
	for _, v := range values {
		var b *bool
		var n *float64
		var s *string
		switch x := v.Value.(type) {
		case bool:
			b = &x
		case float64:
			n = &x
		case string:
			s = &x
		default:
			return fmt.Errorf("postgres: a value of type %T", v.Value)
		}
		if _, err := c.tx.Exec(ctx, `INSERT INTO measurements (id, owner, experiment, cell, trial, metric, grader, self_reported, generation, value_bool, value_num, value_text, rationale, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			id.New(id.Measurement), t.Owner, t.Experiment, t.Cell, t.ID, v.Metric, grader, strings.HasPrefix(grader, objects.SelfPrefix), generation, b, n, s, v.Rationale, c.now); err != nil {
			return fmt.Errorf("postgres: appending a measurement: %w", err)
		}
	}
	return c.j.add(ctx, objects.EventGraded, map[string]any{"trial": t.ID, "grader": grader, "generation": generation, "metrics": len(values)})
}

// finish records a trial's outcome as done, writes its gradings, advances
// its cell, and settles the experiment.
func (c *change) finish(ctx context.Context, t objects.Trial, o store.Outcome, submitLease, submitDigest string) (objects.Trial, error) {
	t.State, t.Outcome, t.Failure = objects.StateDone, o.Outcome, o.Failure
	started, agentEnded := o.StartedAt.UTC(), o.AgentEndedAt.UTC()
	t.StartedAt, t.AgentEndedAt = &started, &agentEnded
	ended := c.now
	if o.EndedAt != nil {
		ended = o.EndedAt.UTC()
	}
	t.EndedAt = &ended
	t.ExecutorRef, t.Observed, t.Declared, t.Lease = o.Ref, o.Observed, o.Declared, nil
	if o.Evidence != nil {
		t.Evidence = o.Evidence
	}
	if len(o.Evidence) > 0 {
		t.Outputs = &objects.OutputsSummary{Files: len(o.Evidence)}
	}
	failure, err := marshalNullable(t.Failure)
	if err != nil {
		return t, err
	}
	observed, err := marshal(t.Observed)
	if err != nil {
		return t, err
	}
	declared, err := marshalNullable(t.Declared)
	if err != nil {
		return t, err
	}
	evidence, err := marshal(t.Evidence)
	if err != nil {
		return t, err
	}
	outputs, err := marshalNullable(t.Outputs)
	if err != nil {
		return t, err
	}
	var lease, digest *string
	if submitLease != "" {
		lease, digest = &submitLease, &submitDigest
	}
	if _, err := c.tx.Exec(ctx, `UPDATE trials SET state = 'done', outcome = $2, failure = $3, started_at = $4, agent_ended_at = $5, ended_at = $6,
			executor_ref = $7, observed = $8, declared = $9, evidence = $10, outputs = $11, imported = $12,
			lease_id = NULL, lease_subject = NULL, lease_expires_at = NULL, submit_lease = $13, submit_digest = $14, updated_at = $15
		WHERE id = $1`,
		t.ID, t.Outcome, failure, started, agentEnded, ended, t.ExecutorRef, observed, declared, evidence, outputs, t.Imported, lease, digest, c.now); err != nil {
		return t, fmt.Errorf("postgres: recording a trial's outcome: %w", err)
	}
	if err := c.trialState(ctx, t); err != nil {
		return t, err
	}
	if err := c.measure(ctx, t, o); err != nil {
		return t, err
	}
	if err := c.dirty(ctx); err != nil {
		return t, err
	}
	if c.e.Mode == objects.ModeExecute {
		cell, trials, err := c.cell(ctx, t.Cell)
		if err != nil {
			return t, err
		}
		if err := c.apply(ctx, cell, execute.Advance(c.view(cell, trials), t)); err != nil {
			return t, err
		}
	}
	return t, c.settle(ctx)
}

// lapse returns the experiment's trials whose lease lapsed to pending,
// keeping their lease columns, so a renewal or a submit under the lapsed
// lease is told apart, and spending no retry.
func (c *change) lapse(ctx context.Context) (int, error) {
	rows, err := c.tx.Query(ctx, `UPDATE trials SET state = 'pending', updated_at = $2
		WHERE experiment = $1 AND state = 'running' AND lease_expires_at <= $2 RETURNING `+trialColumns, c.e.ID, c.now)
	if err != nil {
		return 0, fmt.Errorf("postgres: returning lapsed trials: %w", err)
	}
	lapsed, err := collectTrials(rows)
	if err != nil {
		return 0, err
	}
	for _, t := range lapsed {
		if err := c.trialState(ctx, t); err != nil {
			return 0, err
		}
	}
	return len(lapsed), nil
}

// openRollouts are a cell's submitted rollouts that have not ended.
func openRollouts(cell objects.Cell) []string {
	var out []string
	for _, r := range cell.Rollouts {
		if r.State == objects.RolloutSubmitted {
			out = append(out, r.Key)
		}
	}
	return out
}

// rolloutRank orders a cell's pending trials for leasing: smoke trials
// first, then retries, then the main rollout's, each by index.
func rolloutRank(cell objects.Cell, key string) int {
	for _, r := range cell.Rollouts {
		if r.Key == key {
			switch r.Kind {
			case objects.RolloutSmoke:
				return 0
			case objects.RolloutRetry:
				return 1
			}
		}
	}
	return 2
}

func (x experiments) Lease(ctx context.Context, r store.LeaseRequest) (store.Leased, error) {
	var out store.Leased
	err := x.begin(ctx, r.Experiment, func(c *change) error {
		switch {
		case c.e.Mode == objects.ModeImport:
			return &apierr.Error{Code: apierr.CodeWrongMode, Detail: "lease: the experiment is in mode import, whose trials are imported", State: c.e.State}
		case c.e.Finished():
			return &apierr.Error{Code: apierr.CodeExperimentFinished, Detail: "lease: the experiment is " + c.e.State, State: c.e.State}
		}
		if _, err := c.lapse(ctx); err != nil {
			return err
		}
		take, err := c.leasable(ctx, r)
		if err != nil || len(take) == 0 {
			return err
		}
		out.Lease, out.ExpiresAt = id.New(id.Lease), c.now.Add(limits.ExternalLease)
		rows, err := c.tx.Query(ctx, `UPDATE trials SET state = 'running', lease_id = $2, lease_subject = $3, lease_expires_at = $4, updated_at = $5
			WHERE id = ANY($1) AND state = 'pending' RETURNING `+trialColumns, take, out.Lease, r.Subject, out.ExpiresAt, c.now)
		if err != nil {
			return fmt.Errorf("postgres: leasing trials: %w", err)
		}
		leased, err := collectTrials(rows)
		if err != nil {
			return err
		}
		order := map[string]int{}
		for i, tid := range take {
			order[tid] = i
		}
		slices.SortFunc(leased, func(a, b objects.Trial) int { return order[a.ID] - order[b.ID] })
		for _, t := range leased {
			if err := c.trialState(ctx, t); err != nil {
				return err
			}
		}
		out.Trials = leased
		return nil
	})
	return out, err
}

// unfinished is one trial of a submitted rollout not yet done, with its id.
type unfinished struct {
	execute.Unfinished
	id string
}

// leasable picks the pending trials a lease takes: every unfinished trial
// of the experiment's open rollouts is dealt the parallelism by its
// scheduling, and the pending ones of the cells the request may take, of
// its candidate or of every candidate when it names none, are taken in the
// scheduling's order, each cell within its share less the trials it
// already holds, and never more than the parallelism less every trial
// already held, nor more than the request's max.
func (c *change) leasable(ctx context.Context, r store.LeaseRequest) ([]string, error) {
	cells, err := collectCellsQuery(ctx, c.tx, `SELECT `+cellColumns+` FROM cells WHERE experiment = $1 ORDER BY task_pos, candidate_pos`, c.e.ID)
	if err != nil {
		return nil, err
	}
	byID := map[string]objects.Cell{}
	for _, cell := range cells {
		byID[cell.ID] = cell
	}
	rows, err := c.tx.Query(ctx, `SELECT id, cell, rollout, idx, state FROM trials WHERE experiment = $1 AND state <> 'done'`, c.e.ID)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading open trials: %w", err)
	}
	var all []unfinished
	for rows.Next() {
		var u unfinished
		var rollout, state string
		if err := rows.Scan(&u.id, &u.Cell, &rollout, &u.Index, &state); err != nil {
			rows.Close()
			return nil, fmt.Errorf("postgres: reading an open trial: %w", err)
		}
		cell, ok := byID[u.Cell]
		if !ok || !slices.Contains(openRollouts(cell), rollout) {
			continue
		}
		u.TaskPos, u.CandidatePos, u.Rank, u.Held = cell.TaskPos, cell.CandidatePos, rolloutRank(cell, rollout), state != objects.StatePending
		all = append(all, u)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading open trials: %w", err)
	}
	trials := make([]execute.Unfinished, len(all))
	for i, u := range all {
		trials[i] = u.Unfinished
	}
	shares := deal(c.e.Scheduling, c.e.Parallelism, cells, trials)
	held := map[string]int{}
	totalHeld := 0
	for _, u := range all {
		if u.Held {
			held[u.Cell]++
			totalHeld++
		}
	}
	room := map[string]int{}
	for _, cell := range cells {
		if (r.Candidate != "" && cell.Candidate != r.Candidate) || !slices.Contains(r.Cells, cell.ID) || objects.Parked(cell.State) {
			continue
		}
		room[cell.ID] = shares[cell.ID] - held[cell.ID]
	}
	limit := c.e.Parallelism - totalHeld
	if r.Max > 0 {
		limit = min(limit, r.Max)
	}
	slices.SortStableFunc(all, func(a, b unfinished) int {
		return cmp.Or(execute.Before(c.e.Scheduling, a.Unfinished, b.Unfinished), strings.Compare(a.id, b.id))
	})
	var take []string
	for _, u := range all {
		if len(take) >= limit {
			break
		}
		if u.Held || room[u.Cell] <= 0 {
			continue
		}
		take = append(take, u.id)
		room[u.Cell]--
	}
	return take, nil
}

// deal is each cell's share of the parallelism under the experiment's
// scheduling: under task_by_task, tasks in the suite's order and a task's
// cells round robin; under interleaved, every held trial and then the
// pending ones in the order every cell takes its turn.
func deal(scheduling string, parallelism int, cells []objects.Cell, trials []execute.Unfinished) map[string]int {
	if scheduling == objects.SchedulingInterleaved {
		return execute.DealInterleaved(parallelism, trials)
	}
	unfinishedOf := map[string]int{}
	for _, t := range trials {
		unfinishedOf[t.Cell]++
	}
	loads := make([]execute.Load, 0, len(cells))
	for _, cell := range cells {
		loads = append(loads, execute.Load{Cell: cell.ID, TaskPos: cell.TaskPos, CandidatePos: cell.CandidatePos, Unfinished: unfinishedOf[cell.ID]})
	}
	return execute.Deal(parallelism, loads)
}

func collectCellsQuery(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]objects.Cell, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading cells: %w", err)
	}
	return collectCells(rows)
}

func (x experiments) Renew(ctx context.Context, eid, lease, subject string) (store.Leased, error) {
	out := store.Leased{Lease: lease}
	err := x.begin(ctx, eid, func(c *change) error {
		rows, err := c.tx.Query(ctx, `SELECT coalesce(lease_subject, ''), lease_expires_at FROM trials
			WHERE experiment = $1 AND lease_id = $2 FOR UPDATE`, c.e.ID, lease)
		if err != nil {
			return fmt.Errorf("postgres: reading a lease's trials: %w", err)
		}
		type hold struct {
			subject string
			expires *time.Time
		}
		holds, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (hold, error) {
			var h hold
			return h, r.Scan(&h.subject, &h.expires)
		})
		if err != nil {
			return fmt.Errorf("postgres: reading a lease's trials: %w", err)
		}
		switch {
		case len(holds) == 0:
			return apierr.OnField(apierr.CodeLeaseNotHeld, "renew", "renew: no trial of this experiment is held under the lease")
		case slices.ContainsFunc(holds, func(h hold) bool { return h.subject != subject }):
			return apierr.OnField(apierr.CodeLeaseNotHeld, "renew", "renew: another subject holds the lease")
		case slices.ContainsFunc(holds, func(h hold) bool { return h.expires == nil || !h.expires.After(c.now) }):
			return apierr.OnField(apierr.CodeLeaseExpired, "renew", "renew: the lease lapsed")
		}
		out.ExpiresAt = c.now.Add(limits.ExternalLease)
		rows, err = c.tx.Query(ctx, `UPDATE trials SET lease_expires_at = $3, updated_at = $4
			WHERE experiment = $1 AND lease_id = $2 AND state = 'running' RETURNING `+trialColumns, c.e.ID, lease, out.ExpiresAt, c.now)
		if err != nil {
			return fmt.Errorf("postgres: renewing a lease: %w", err)
		}
		held, err := collectTrials(rows)
		if err != nil {
			return err
		}
		slices.SortFunc(held, func(a, b objects.Trial) int { return strings.Compare(a.ID, b.ID) })
		out.Trials = held
		return nil
	})
	return out, err
}

func (x experiments) Submit(ctx context.Context, s store.Submission) (objects.Trial, error) {
	t, err := x.Trial(ctx, s.Trial)
	if err != nil {
		return objects.Trial{}, err
	}
	var out objects.Trial
	err = x.begin(ctx, t.Experiment, func(c *change) error {
		var leaseID, leaseSubject, submitLease, submitDigest string
		var expires *time.Time
		row := c.tx.QueryRow(ctx, `SELECT `+trialColumns+`, coalesce(lease_id, ''), coalesce(lease_subject, ''), lease_expires_at,
			coalesce(submit_lease, ''), coalesce(submit_digest, '') FROM trials WHERE id = $1 FOR UPDATE`, s.Trial)
		t, err := scanTrialExtra(row, &leaseID, &leaseSubject, &expires, &submitLease, &submitDigest)
		if err := one(err, "a trial"); err != nil {
			return err
		}
		switch {
		case submitLease != "" && submitLease == s.Lease && submitDigest == s.Digest:
			// The same submit after it succeeded changes nothing.
			out = t
			return nil
		case leaseID != s.Lease || leaseSubject != s.Subject:
			return &apierr.Error{Code: apierr.CodeLeaseNotHeld, Field: "lease", Detail: "submit: the trial is not held under this lease by this subject", State: t.State}
		case t.State == objects.StateDone || t.State == objects.StateChecking:
			return &apierr.Error{Code: apierr.CodeLeaseNotHeld, Field: "lease", Detail: "submit: the trial is " + t.State + " and no longer held", State: t.State}
		case expires == nil || !expires.After(c.now):
			// A lapsed lease keeps its columns on the trial it returned to
			// pending, so its submit is told apart from another lease's.
			return apierr.OnField(apierr.CodeLeaseExpired, "lease", "submit: the lease lapsed")
		}
		out, err = c.finish(ctx, t, s.Outcome, s.Lease, s.Digest)
		return err
	})
	return out, err
}

// scanTrialExtra reads a trial's columns and further ones after them.
func scanTrialExtra(row pgx.Row, extra ...any) (objects.Trial, error) {
	return scanTrial(extraRow{row, extra})
}

// extraRow reads a row of trialColumns followed by more columns.
type extraRow struct {
	row   pgx.Row
	extra []any
}

func (e extraRow) Scan(dest ...any) error { return e.row.Scan(append(dest, e.extra...)...) }

func (x experiments) Import(ctx context.Context, imp store.Import) (objects.Trial, error) {
	var out objects.Trial
	err := x.begin(ctx, imp.Experiment, func(c *change) error {
		switch {
		case c.e.Mode == objects.ModeExecute:
			return &apierr.Error{Code: apierr.CodeWrongMode, Detail: "import: the experiment is in mode execute, whose trials Examen runs", State: c.e.State}
		case c.e.Finished() || c.e.IntakeClosed:
			return &apierr.Error{Code: apierr.CodeExperimentFinished, Detail: "import: the experiment's intake is closed", State: c.e.State}
		}
		if imp.Ref != "" {
			var n int
			if err := c.tx.QueryRow(ctx, `SELECT count(*) FROM trials WHERE experiment = $1 AND imported AND executor_ref = $2`, c.e.ID, imp.Ref).Scan(&n); err != nil {
				return fmt.Errorf("postgres: looking for an imported run: %w", err)
			}
			if n > 0 {
				return apierr.OnField(apierr.CodeAlreadyImported, "ref", "import: a run with this ref is already a trial of the experiment")
			}
		}
		cell, err := scanCell(c.tx.QueryRow(ctx, `SELECT `+cellColumns+` FROM cells WHERE experiment = $1 AND task = $2 AND candidate = $3 FOR UPDATE`,
			c.e.ID, imp.Cell.Task, imp.Cell.Candidate))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			cell = imp.Cell
			cell.ID, cell.Experiment, cell.Owner, cell.State = id.New(id.Cell), c.e.ID, c.e.Owner, objects.StateRunning
			if err := insertCell(ctx, c.tx, cell, c.now); err != nil {
				return err
			}
			if err := c.j.add(ctx, objects.EventCell, cellEvent(cell)); err != nil {
				return err
			}
		case err != nil:
			return fmt.Errorf("postgres: reading a cell: %w", err)
		}
		var held int
		if err := c.tx.QueryRow(ctx, `SELECT count(*) FROM trials WHERE cell = $1`, cell.ID).Scan(&held); err != nil {
			return fmt.Errorf("postgres: counting a cell's trials: %w", err)
		}
		if held >= c.e.Trials {
			return apierr.Newf(apierr.CodeCellFull, "import: the cell holds its %d declared trials", c.e.Trials)
		}
		t := objects.Trial{ID: id.New(id.Trial), Owner: c.e.Owner, Experiment: c.e.ID, Cell: cell.ID, Index: held, State: objects.StatePending, Imported: true}
		if _, err := c.tx.Exec(ctx, `INSERT INTO trials (id, owner, experiment, cell, idx, state, imported, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, 'pending', true, $6, $6)`,
			t.ID, t.Owner, t.Experiment, t.Cell, t.Index, c.now); err != nil {
			if uniqueViolation(err) {
				return apierr.OnField(apierr.CodeAlreadyImported, "ref", "import: a run with this ref is already a trial of the experiment")
			}
			return fmt.Errorf("postgres: storing an imported trial: %w", err)
		}
		t.CreatedAt = c.now
		if err := c.experimentState(ctx, objects.StateRunning); err != nil {
			return err
		}
		out, err = c.finish(ctx, t, imp.Outcome, "", "")
		return err
	})
	return out, err
}

func (x experiments) Close(ctx context.Context, eid string) (objects.Experiment, error) {
	err := x.begin(ctx, eid, func(c *change) error {
		switch {
		case c.e.Mode == objects.ModeExecute:
			return &apierr.Error{Code: apierr.CodeWrongMode, Detail: "close: the experiment is in mode execute, which has no intake", State: c.e.State}
		case c.e.IntakeClosed:
			return nil
		case c.e.State == objects.StateCanceled:
			return &apierr.Error{Code: apierr.CodeExperimentFinished, Detail: "close: the experiment was canceled", State: c.e.State}
		}
		if _, err := c.tx.Exec(ctx, `UPDATE experiments SET intake_closed = true, updated_at = $2 WHERE id = $1`, c.e.ID, c.now); err != nil {
			return fmt.Errorf("postgres: closing the intake: %w", err)
		}
		c.e.IntakeClosed = true
		cells, err := collectCellsQuery(ctx, c.tx, `SELECT `+cellColumns+` FROM cells WHERE experiment = $1 AND state = 'running' ORDER BY id FOR UPDATE`, c.e.ID)
		if err != nil {
			return err
		}
		for _, cell := range cells {
			if err := c.cellState(ctx, &cell, objects.StateComplete, ""); err != nil {
				return err
			}
			if err := c.saveCell(ctx, cell); err != nil {
				return err
			}
		}
		return c.settle(ctx)
	})
	if err != nil {
		return objects.Experiment{}, err
	}
	return x.Get(ctx, eid)
}

func (x experiments) Cancel(ctx context.Context, eid string) (objects.Experiment, error) {
	err := x.begin(ctx, eid, func(c *change) error {
		switch c.e.State {
		case objects.StateCanceled:
			return nil
		case objects.StateComplete:
			return &apierr.Error{Code: apierr.CodeExperimentFinished, Detail: "cancel: the experiment is complete", State: c.e.State}
		}
		cells, err := collectCellsQuery(ctx, c.tx, `SELECT `+cellColumns+` FROM cells WHERE experiment = $1 AND state NOT IN ('complete', 'canceled') ORDER BY id FOR UPDATE`, c.e.ID)
		if err != nil {
			return err
		}
		for _, cell := range cells {
			if err := c.endOpen(ctx, cell.ID, "experiment_canceled", "the experiment was canceled before the trial ended"); err != nil {
				return err
			}
			for i := range cell.Rollouts {
				cell.Rollouts[i].State = objects.RolloutEnded
			}
			if err := c.cellState(ctx, &cell, objects.StateCanceled, "experiment_canceled"); err != nil {
				return err
			}
			if err := c.saveCell(ctx, cell); err != nil {
				return err
			}
		}
		return c.experimentState(ctx, objects.StateCanceled)
	})
	if err != nil {
		return objects.Experiment{}, err
	}
	return x.Get(ctx, eid)
}

func (x experiments) RetryCell(ctx context.Context, cid string) (objects.Cell, error) {
	cell, err := x.Cell(ctx, cid)
	if err != nil {
		return objects.Cell{}, err
	}
	err = x.begin(ctx, cell.Experiment, func(c *change) error {
		cell, trials, err := c.cell(ctx, cid)
		if err != nil {
			return err
		}
		step, parked := execute.Retry(c.view(cell, trials))
		if !parked {
			return &apierr.Error{Code: apierr.CodeCellNotParked, Detail: "retry: the cell is " + cell.State + ", not parked blocked or over_budget", State: cell.State}
		}
		if err := c.apply(ctx, cell, step); err != nil {
			return err
		}
		if c.e.State == objects.StateComplete {
			// New trials make new comparisons of the pairs a final one
			// read; the earlier ones stay.
			if err := c.reopen(ctx); err != nil {
				return err
			}
		}
		return c.settle(ctx)
	})
	if err != nil {
		return objects.Cell{}, err
	}
	return x.Cell(ctx, cid)
}

// reopen moves a complete experiment back to running for a retried cell,
// and starts a new automatic comparison of each pair a final one read.
func (c *change) reopen(ctx context.Context) error {
	if err := c.experimentState(ctx, objects.StateRunning); err != nil {
		return err
	}
	rows, err := c.tx.Query(ctx, `SELECT `+comparisonColumns+` FROM comparisons WHERE experiment = $1 AND `+keptComparisons+` AND final`, c.e.ID)
	if err != nil {
		return fmt.Errorf("postgres: reading final comparisons: %w", err)
	}
	finals, err := collectComparisons(rows)
	if err != nil {
		return err
	}
	for _, f := range finals {
		next := f
		next.ID, next.State, next.Final, next.Computed = id.New(id.Comparison), objects.ComparisonComputing, false, objects.Computed{}
		if err := insertComparison(ctx, c.tx, next, c.now); err != nil {
			return err
		}
	}
	return c.markDeclared(ctx)
}

func (x experiments) ExpireLeases(ctx context.Context) (int, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT DISTINCT experiment FROM trials WHERE state = 'running' AND lease_expires_at <= coalesce($1::timestamptz, now()) LIMIT 100`, x.s.at())
	if err != nil {
		return 0, fmt.Errorf("postgres: finding lapsed leases: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("postgres: finding lapsed leases: %w", err)
	}
	total := 0
	for _, eid := range ids {
		err := x.begin(ctx, eid, func(c *change) error {
			n, err := c.lapse(ctx)
			total += n
			return err
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (x experiments) Queued(ctx context.Context, experiment string, limit int) ([]store.QueuedRollout, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT e.id, e.owner, e.created_by, e.state, e.budget, c.id, r->>'key'
		FROM cells c JOIN experiments e ON e.id = c.experiment, jsonb_array_elements(c.rollouts) r
		WHERE r->>'state' = 'queued' AND e.state NOT IN ('complete', 'canceled') AND c.state NOT IN ('blocked', 'over_budget', 'canceled')
			AND ($2 = '' OR e.id = $2)
		ORDER BY c.updated_at, c.task_pos, c.candidate_pos LIMIT $1`, limit, experiment)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading queued rollouts: %w", err)
	}
	defer rows.Close()
	var out []store.QueuedRollout
	for rows.Next() {
		var q store.QueuedRollout
		if err := rows.Scan(&q.Experiment, &q.Owner, &q.CreatedBy, &q.State, &q.Budget, &q.Cell, &q.Key); err != nil {
			return nil, fmt.Errorf("postgres: reading a queued rollout: %w", err)
		}
		out = append(out, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading queued rollouts: %w", err)
	}
	return out, nil
}

func (x experiments) SubmitRollout(ctx context.Context, cid, key string) error {
	cell, err := x.Cell(ctx, cid)
	if err != nil {
		return err
	}
	return x.begin(ctx, cell.Experiment, func(c *change) error {
		if c.e.Finished() {
			return nil
		}
		cell, _, err := c.cell(ctx, cid)
		if err != nil {
			return err
		}
		i := slices.IndexFunc(cell.Rollouts, func(r objects.Rollout) bool { return r.Key == key && r.State == objects.RolloutQueued })
		if i < 0 || objects.Parked(cell.State) {
			return nil
		}
		cell.Rollouts[i].State = objects.RolloutSubmitted
		if cell.Rollouts[i].Kind == objects.RolloutSmoke && cell.State == objects.StatePending {
			if err := c.cellState(ctx, &cell, objects.StateSmoking, ""); err != nil {
				return err
			}
		}
		if err := c.saveCell(ctx, cell); err != nil {
			return err
		}
		if c.e.State == objects.StatePending {
			return c.experimentState(ctx, objects.StateRunning)
		}
		return nil
	})
}

func (x experiments) Park(ctx context.Context, cid, state, reason string) error {
	cell, err := x.Cell(ctx, cid)
	if err != nil {
		return err
	}
	return x.begin(ctx, cell.Experiment, func(c *change) error {
		cell, _, err := c.cell(ctx, cid)
		if err != nil {
			return err
		}
		if objects.Parked(cell.State) || cell.State == objects.StateComplete {
			return nil
		}
		s := execute.Step{State: state, Reason: reason, CancelOpen: true}
		for _, r := range cell.Rollouts {
			if r.State != objects.RolloutEnded {
				s.Ended = append(s.Ended, r.Key)
			}
		}
		if err := c.apply(ctx, cell, s); err != nil {
			return err
		}
		return c.settle(ctx)
	})
}

func (x experiments) Leasable(ctx context.Context, eid, candidate string) ([]objects.Cell, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT `+cellColumns+` FROM cells c WHERE experiment = $1 AND ($2 = '' OR candidate = $2)
		AND state NOT IN ('blocked', 'over_budget', 'canceled', 'complete')
		AND EXISTS (SELECT 1 FROM trials t WHERE t.cell = c.id
			AND (t.state = 'pending' OR (t.state = 'running' AND t.lease_expires_at <= coalesce($3::timestamptz, now()))))
		ORDER BY task_pos, candidate_pos`, eid, candidate, x.s.at())
	if err != nil {
		return nil, fmt.Errorf("postgres: reading leasable cells: %w", err)
	}
	cells, err := collectCells(rows)
	if err != nil {
		return nil, err
	}
	out := cells[:0]
	for _, c := range cells {
		if len(openRollouts(c)) > 0 {
			out = append(out, c)
		}
	}
	return out, nil
}
