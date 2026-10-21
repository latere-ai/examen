// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// Experiments is the table of experiments and what they hold.
func (s *Store) Experiments() store.Experiments { return experiments{s} }

type experiments struct{ s *Store }

const experimentColumns = `id, owner, suite, suite_id, suite_name, candidates, candidate_ids, trials, budget, parallelism,
	coalesce(reference_baseline, ''), coalesce(reference_candidate, ''), mode, scheduling, comparisons, independence, state, created_by, metadata, intake_closed, created_at, updated_at`

func scanExperiment(row pgx.Row) (objects.Experiment, error) {
	var e objects.Experiment
	var metadata, declared, independence []byte
	if err := row.Scan(&e.ID, &e.Owner, &e.Suite, &e.SuiteID, &e.SuiteName, &e.Candidates, &e.CandidateIDs, &e.Trials, &e.Budget.Name,
		&e.Parallelism, &e.Reference.Baseline, &e.Reference.Candidate, &e.Mode, &e.Scheduling, &declared, &independence, &e.State, &e.CreatedBy, &metadata, &e.IntakeClosed,
		&e.CreatedAt, &e.UpdatedAt); err != nil {
		return objects.Experiment{}, err
	}
	e.CreatedAt, e.UpdatedAt = e.CreatedAt.UTC(), e.UpdatedAt.UTC()
	e.Judgments = []json.RawMessage{}
	if err := unmarshal(metadata, &e.Metadata); err != nil {
		return objects.Experiment{}, err
	}
	if err := declaredOf(declared, independence, &e); err != nil {
		return objects.Experiment{}, err
	}
	if len(e.Metadata) == 0 {
		e.Metadata = nil
	}
	return e, nil
}

// journal writes an experiment's events in the transaction of the change
// they report, each with the log's next sequence, and advances the
// experiment's counter when it is done.
type journal struct {
	tx    pgx.Tx
	exp   string
	owner string
	seq   int64
	now   time.Time
}

func (j *journal) add(ctx context.Context, eventType string, data any) error {
	raw, err := marshal(data)
	if err != nil {
		return err
	}
	var delivery *string
	if objects.Sink(eventType) {
		// No sink is configured while this release runs, so a sink event is
		// journaled acknowledged and nothing is sent.
		acknowledged := "acknowledged"
		delivery = &acknowledged
	}
	if _, err := j.tx.Exec(ctx, `INSERT INTO events (id, owner, experiment, seq, type, data, at, delivery) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		id.New(id.Event), j.owner, j.exp, j.seq, eventType, raw, j.now, delivery); err != nil {
		return fmt.Errorf("postgres: journaling an event: %w", err)
	}
	j.seq++
	return nil
}

// close stores the sequence the next event takes.
func (j *journal) close(ctx context.Context) error {
	if _, err := j.tx.Exec(ctx, `UPDATE experiments SET next_seq = $2 WHERE id = $1`, j.exp, j.seq); err != nil {
		return fmt.Errorf("postgres: advancing the event log: %w", err)
	}
	return nil
}

// The data of each event, as specs/003-api.md names its members.
func cellEvent(c objects.Cell) map[string]any {
	return map[string]any{"id": c.ID, "task": c.Task, "candidate": c.Candidate, "state": c.State, "parked_reason": c.ParkedReason}
}

func trialEvent(t objects.Trial) map[string]any {
	return map[string]any{"id": t.ID, "cell": t.Cell, "index": t.Index, "state": t.State, "outcome": t.Outcome, "retry_of": t.RetryOf, "imported": t.Imported}
}

func (x experiments) Create(ctx context.Context, ne store.NewExperiment) error {
	e := ne.Experiment
	return pgx.BeginFunc(ctx, x.s.pool, func(tx pgx.Tx) error {
		now, err := x.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		// The owner's active experiments are counted with the insert,
		// under one lock on the owner.
		if err := lock(ctx, tx, "examen.active:"+e.Owner); err != nil {
			return err
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM experiments WHERE owner = $1 AND state NOT IN ('complete', 'canceled')`, e.Owner).Scan(&active); err != nil {
			return fmt.Errorf("postgres: counting active experiments: %w", err)
		}
		if active >= ne.MaxActive {
			return store.ErrActiveLimit
		}
		metadata, err := marshal(e.Metadata)
		if err != nil {
			return err
		}
		if e.Metadata == nil {
			metadata = "{}"
		}
		var refBaseline, refCandidate *string
		if e.Reference.Baseline != "" {
			refBaseline = &e.Reference.Baseline
		} else {
			refCandidate = &e.Reference.Candidate
		}
		scheduling := e.Scheduling
		if scheduling == "" {
			scheduling = objects.SchedulingTaskByTask
		}
		declared := e.Comparisons
		if declared == nil {
			declared = []objects.DeclaredComparison{}
		}
		rawDeclared, err := marshal(declared)
		if err != nil {
			return err
		}
		var independence *string
		if e.Independence != nil {
			raw, err := marshal(e.Independence)
			if err != nil {
				return err
			}
			independence = &raw
		}
		if _, err := tx.Exec(ctx, `INSERT INTO experiments (id, owner, suite, suite_id, suite_name, candidates, candidate_ids, trials, budget, parallelism,
				reference_baseline, reference_candidate, mode, scheduling, comparisons, independence, state, created_by, metadata, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, 'pending', $17, $18, $19, $19)`,
			e.ID, e.Owner, e.Suite, e.SuiteID, e.SuiteName, e.Candidates, e.CandidateIDs, e.Trials, e.Budget.Name, e.Parallelism,
			refBaseline, refCandidate, e.Mode, scheduling, rawDeclared, independence, e.CreatedBy, metadata, now); err != nil {
			return fmt.Errorf("postgres: storing an experiment: %w", err)
		}
		j := &journal{tx: tx, exp: e.ID, owner: e.Owner, seq: 1, now: now}
		if err := j.add(ctx, objects.EventExperiment, map[string]any{"id": e.ID, "state": objects.StatePending, "progress": emptyProgress()}); err != nil {
			return err
		}
		for _, nc := range ne.Cells {
			c := nc.Cell
			c.Experiment, c.Owner, c.State = e.ID, e.Owner, objects.StatePending
			var trials []store.RolloutTrial
			if nc.Smoke != nil {
				c.Rollouts = []objects.Rollout{nc.Smoke.Rollout}
				trials = nc.Smoke.Trials
			}
			if err := insertCell(ctx, tx, c, now); err != nil {
				return err
			}
			if err := j.add(ctx, objects.EventCell, cellEvent(c)); err != nil {
				return err
			}
			if nc.Smoke != nil {
				if err := insertTrials(ctx, tx, j, c, nc.Smoke.Rollout.Key, trials, now); err != nil {
					return err
				}
			}
		}
		for _, cmp := range ne.Comparisons {
			cmp.Experiment, cmp.Owner = e.ID, e.Owner
			if err := insertComparison(ctx, tx, cmp, now); err != nil {
				return err
			}
		}
		return j.close(ctx)
	})
}

func emptyProgress() objects.Progress {
	return objects.Progress{Cells: map[string]int{}, Trials: map[string]int{}, Outcomes: map[string]int{}}
}

// insertCell stores a cell.
func insertCell(ctx context.Context, tx pgx.Tx, c objects.Cell, now time.Time) error {
	tuple, err := marshal(c.Tuple)
	if err != nil {
		return err
	}
	if c.Rollouts == nil {
		c.Rollouts = []objects.Rollout{}
	}
	rollouts, err := marshal(c.Rollouts)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO cells (id, owner, experiment, task, task_id, candidate, candidate_id, task_pos, candidate_pos, tuple, rollouts, state, parked_reason, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $14)`,
		c.ID, c.Owner, c.Experiment, c.Task, c.TaskID, c.Candidate, c.CandidateID, c.TaskPos, c.CandidatePos, tuple, rollouts, c.State, c.ParkedReason, now); err != nil {
		return fmt.Errorf("postgres: storing a cell: %w", err)
	}
	return nil
}

// insertTrials mints a rollout's trials, pending, and journals each.
func insertTrials(ctx context.Context, tx pgx.Tx, j *journal, c objects.Cell, rollout string, trials []store.RolloutTrial, now time.Time) error {
	for _, rt := range trials {
		t := objects.Trial{ID: id.New(id.Trial), Owner: c.Owner, Experiment: c.Experiment, Cell: c.ID, Index: rt.Index, State: objects.StatePending, RetryOf: rt.RetryOf}
		var retryOf *string
		if rt.RetryOf != "" {
			retryOf = &rt.RetryOf
		}
		if _, err := tx.Exec(ctx, `INSERT INTO trials (id, owner, experiment, cell, idx, rollout, state, retry_of, auto_retry, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8, $9, $9)`,
			t.ID, t.Owner, t.Experiment, t.Cell, t.Index, rollout, retryOf, rt.Auto, now); err != nil {
			return fmt.Errorf("postgres: minting a trial: %w", err)
		}
		if err := j.add(ctx, objects.EventTrial, trialEvent(t)); err != nil {
			return err
		}
	}
	return nil
}

func (x experiments) Get(ctx context.Context, eid string) (objects.Experiment, error) {
	e, err := scanExperiment(x.s.pool.QueryRow(ctx, `SELECT `+experimentColumns+` FROM experiments WHERE id = $1`, eid))
	if err := one(err, "an experiment"); err != nil {
		return objects.Experiment{}, err
	}
	p, err := progress(ctx, x.s.pool, eid)
	if err != nil {
		return objects.Experiment{}, err
	}
	e.Progress = &p
	return e, nil
}

// progress counts an experiment's cells by state, and its trials by state
// and by outcome.
func progress(ctx context.Context, q querier, eid string) (objects.Progress, error) {
	p := emptyProgress()
	count := func(sql string, into map[string]int) error {
		rows, err := q.Query(ctx, sql, eid)
		if err != nil {
			return fmt.Errorf("postgres: counting progress: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var k string
			var n int
			if err := rows.Scan(&k, &n); err != nil {
				return fmt.Errorf("postgres: reading progress: %w", err)
			}
			into[k] = n
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: reading progress: %w", err)
		}
		return nil
	}
	if err := count(`SELECT state, count(*) FROM cells WHERE experiment = $1 GROUP BY state`, p.Cells); err != nil {
		return p, err
	}
	if err := count(`SELECT state, count(*) FROM trials WHERE experiment = $1 GROUP BY state`, p.Trials); err != nil {
		return p, err
	}
	return p, count(`SELECT outcome, count(*) FROM trials WHERE experiment = $1 AND outcome IS NOT NULL GROUP BY outcome`, p.Outcomes)
}

func (x experiments) List(ctx context.Context, f store.ExperimentFilter, p store.Page) ([]objects.Experiment, error) {
	w := &where{}
	w.add("owner = ANY(%s)", f.Owners)
	if f.Suite != "" {
		w.add("suite = %s", f.Suite)
	}
	if f.SuiteName != "" {
		w.add("suite_name = %s", f.SuiteName)
	}
	if f.Candidate != "" {
		w.add("%s = ANY(candidates)", f.Candidate)
	}
	if f.State != "" {
		w.add("state = %s", f.State)
	}
	if f.Mode != "" {
		w.add("mode = %s", f.Mode)
	}
	if f.Baseline != "" {
		w.add("reference_baseline = %s", f.Baseline)
	}
	if len(f.Labels) > 0 {
		b, err := labels(f.Labels)
		if err != nil {
			return nil, err
		}
		w.add("metadata @> %s::jsonb", b)
	}
	tail := w.page("id", p)
	rows, err := x.s.pool.Query(ctx, `SELECT `+experimentColumns+` FROM experiments`+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing experiments: %w", err)
	}
	defer rows.Close()
	var out []objects.Experiment
	for rows.Next() {
		e, err := scanExperiment(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading an experiment: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading experiments: %w", err)
	}
	for i := range out {
		pr, err := progress(ctx, x.s.pool, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Progress = &pr
	}
	return out, nil
}

const cellColumns = `id, owner, experiment, task, task_id, candidate, candidate_id, task_pos, candidate_pos, tuple, rollouts, state, parked_reason, created_at, updated_at`

func scanCell(row pgx.Row) (objects.Cell, error) {
	var c objects.Cell
	var tuple, rollouts []byte
	if err := row.Scan(&c.ID, &c.Owner, &c.Experiment, &c.Task, &c.TaskID, &c.Candidate, &c.CandidateID, &c.TaskPos, &c.CandidatePos,
		&tuple, &rollouts, &c.State, &c.ParkedReason, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return objects.Cell{}, err
	}
	c.CreatedAt, c.UpdatedAt, c.Adopted = c.CreatedAt.UTC(), c.UpdatedAt.UTC(), []string{}
	if err := unmarshal(tuple, &c.Tuple); err != nil {
		return objects.Cell{}, err
	}
	// The column is never NULL and holds an array, so the slice is never
	// nil.
	return c, unmarshal(rollouts, &c.Rollouts)
}

func collectCells(rows pgx.Rows) ([]objects.Cell, error) {
	defer rows.Close()
	var out []objects.Cell
	for rows.Next() {
		c, err := scanCell(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a cell: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading cells: %w", err)
	}
	return out, nil
}

func (x experiments) Cells(ctx context.Context, eid string, f store.CellFilter, p store.Page) ([]objects.Cell, error) {
	w := &where{}
	w.add("experiment = %s", eid)
	if f.State != "" {
		w.add("state = %s", f.State)
	}
	if f.Task != "" {
		w.add("task = %s", f.Task)
	}
	if f.Candidate != "" {
		w.add("candidate = %s", f.Candidate)
	}
	tail := w.page("id", p)
	rows, err := x.s.pool.Query(ctx, `SELECT `+cellColumns+` FROM cells`+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing cells: %w", err)
	}
	return collectCells(rows)
}

func (x experiments) AllCells(ctx context.Context, eid string) ([]objects.Cell, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT `+cellColumns+` FROM cells WHERE experiment = $1 ORDER BY task_pos, candidate_pos`, eid)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing cells: %w", err)
	}
	return collectCells(rows)
}

func (x experiments) Cell(ctx context.Context, cid string) (objects.Cell, error) {
	c, err := scanCell(x.s.pool.QueryRow(ctx, `SELECT `+cellColumns+` FROM cells WHERE id = $1`, cid))
	return c, one(err, "a cell")
}

func (x experiments) Counts(ctx context.Context, eid string) (map[string]store.TrialCounts, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT cell, state, coalesce(outcome, ''), retried, count(*) FROM trials WHERE experiment = $1 GROUP BY cell, state, outcome, retried`, eid)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting trials: %w", err)
	}
	defer rows.Close()
	out := map[string]store.TrialCounts{}
	for rows.Next() {
		var cell, state, outcome string
		var retried bool
		var n int
		if err := rows.Scan(&cell, &state, &outcome, &retried, &n); err != nil {
			return nil, fmt.Errorf("postgres: reading trial counts: %w", err)
		}
		c, ok := out[cell]
		if !ok {
			c = store.TrialCounts{States: map[string]int{}, Outcomes: map[string]int{}}
		}
		c.States[state] += n
		if outcome != "" {
			c.Outcomes[outcome] += n
		}
		if retried {
			c.Retried += n
		}
		out[cell] = c
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading trial counts: %w", err)
	}
	return out, nil
}

const trialColumns = `id, owner, experiment, cell, idx, rollout, executor_ref, coalesce(lease_id, ''), coalesce(lease_subject, ''), lease_expires_at,
	state, coalesce(outcome, ''), failure, coalesce(retry_of, ''), retried, auto_retry, observed, declared, evidence, outputs, imported,
	started_at, agent_ended_at, ended_at, created_at, sent`

func scanTrial(row pgx.Row) (objects.Trial, error) {
	var t objects.Trial
	var leaseID, leaseSubject string
	var leaseExpires *time.Time
	var failure, observed, declared, evidence, outputs, sent []byte
	if err := row.Scan(&t.ID, &t.Owner, &t.Experiment, &t.Cell, &t.Index, &t.Rollout, &t.ExecutorRef, &leaseID, &leaseSubject, &leaseExpires,
		&t.State, &t.Outcome, &failure, &t.RetryOf, &t.Retried, &t.AutoRetry, &observed, &declared, &evidence, &outputs, &t.Imported,
		&t.StartedAt, &t.AgentEndedAt, &t.EndedAt, &t.CreatedAt, &sent); err != nil {
		return objects.Trial{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	t.StartedAt, t.AgentEndedAt, t.EndedAt = ptr(t.StartedAt), ptr(t.AgentEndedAt), ptr(t.EndedAt)
	// A trial shows its lease only while it is leased; the columns stay
	// after a lease lapses, to tell a lapsed lease from another.
	if leaseID != "" && t.State == objects.StateRunning && leaseExpires != nil {
		t.Lease = &objects.Lease{ID: leaseID, Subject: leaseSubject, ExpiresAt: leaseExpires.UTC()}
	}
	t.Checks, t.Judgments, t.Evidence = []json.RawMessage{}, []json.RawMessage{}, []objects.EvidenceRef{}
	for _, c := range []struct {
		raw []byte
		v   any
	}{{failure, &t.Failure}, {observed, &t.Observed}, {declared, &t.Declared}, {evidence, &t.Evidence}, {outputs, &t.Outputs}, {sent, &t.Sent}} {
		if err := unmarshal(c.raw, c.v); err != nil {
			return objects.Trial{}, err
		}
	}
	return t, nil
}

func collectTrials(rows pgx.Rows) ([]objects.Trial, error) {
	defer rows.Close()
	var out []objects.Trial
	for rows.Next() {
		t, err := scanTrial(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a trial: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading trials: %w", err)
	}
	return out, nil
}

func (x experiments) Trials(ctx context.Context, eid string, f store.TrialFilter, p store.Page) ([]objects.Trial, error) {
	w := &where{}
	w.add("experiment = %s", eid)
	if f.Cells != nil {
		w.add("cell = ANY(%s)", f.Cells)
	}
	if f.State != "" {
		w.add("state = %s", f.State)
	}
	if f.Outcome != "" {
		w.add("outcome = %s", f.Outcome)
	}
	if f.Imported != nil {
		w.add("imported = %s", *f.Imported)
	}
	if f.Retried != nil {
		w.add("retried = %s", *f.Retried)
	}
	tail := w.page("id", p)
	rows, err := x.s.pool.Query(ctx, `SELECT `+trialColumns+` FROM trials`+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing trials: %w", err)
	}
	trials, err := collectTrials(rows)
	if err != nil {
		return nil, err
	}
	return trials, x.withGradings(ctx, trials)
}

func (x experiments) Trial(ctx context.Context, tid string) (objects.Trial, error) {
	t, err := scanTrial(x.s.pool.QueryRow(ctx, `SELECT `+trialColumns+` FROM trials WHERE id = $1`, tid))
	if err := one(err, "a trial"); err != nil {
		return t, err
	}
	list := []objects.Trial{t}
	err = x.withGradings(ctx, list)
	return list[0], err
}

// withGradings fills each trial's checks and judgments from its gradings
// (specs/002-objects.md): each check run's and judgment's record in the
// trial's order once it ended, and its grader and pending before.
func (x experiments) withGradings(ctx context.Context, trials []objects.Trial) error {
	ids := make([]string, len(trials))
	at := map[string]int{}
	for i, t := range trials {
		ids[i], at[t.ID] = t.ID, i
	}
	list, err := gradings(x).OfTrials(ctx, ids)
	if err != nil {
		return err
	}
	for _, g := range list {
		rec := g.Record
		if g.State == objects.GradingPending || len(rec) == 0 {
			raw, err := json.Marshal(map[string]string{"grader": g.Grader, "state": objects.GradingPending})
			if err != nil {
				return fmt.Errorf("postgres: rendering a pending grading: %w", err)
			}
			rec = raw
		}
		t := &trials[at[g.Trial]]
		if g.Kind == objects.GraderCheck {
			t.Checks = append(t.Checks, rec)
		} else {
			t.Judgments = append(t.Judgments, rec)
		}
	}
	return nil
}

const measurementColumns = `id, owner, experiment, coalesce(cell, ''), coalesce(trial, ''), coalesce(candidate, ''), metric, grader, annotator,
	self_reported, generation, value_bool, value_num, value_text, rationale, created_at`

func scanMeasurement(row pgx.Row) (objects.Measurement, error) {
	var m objects.Measurement
	var b *bool
	var n *float64
	var s *string
	if err := row.Scan(&m.ID, &m.Owner, &m.Experiment, &m.Cell, &m.Trial, &m.Candidate, &m.Metric, &m.Grader, &m.Annotator,
		&m.SelfReported, &m.Generation, &b, &n, &s, &m.Rationale, &m.CreatedAt); err != nil {
		return objects.Measurement{}, err
	}
	m.CreatedAt = m.CreatedAt.UTC()
	m.Value = valueOf(b, n, s)
	return m, nil
}

func collectMeasurements(rows pgx.Rows) ([]objects.Measurement, error) {
	defer rows.Close()
	var out []objects.Measurement
	for rows.Next() {
		m, err := scanMeasurement(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a measurement: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading measurements: %w", err)
	}
	return out, nil
}

func (x experiments) Measurements(ctx context.Context, f store.MeasurementFilter, p store.Page) ([]objects.Measurement, error) {
	w := &where{}
	if f.Experiment != "" {
		w.add("experiment = %s", f.Experiment)
	}
	if f.Trial != "" {
		w.add("trial = %s", f.Trial)
	}
	if f.Cells != nil {
		w.add("cell = ANY(%s)", f.Cells)
	}
	if f.Candidate != "" {
		w.add("(candidate = %s OR cell IN (SELECT id FROM cells WHERE candidate = %s))", f.Candidate, f.Candidate)
	}
	if f.Metric != "" {
		w.add("metric = %s", f.Metric)
	}
	if f.Grader != "" {
		w.add("grader = %s", f.Grader)
	}
	if f.CandidateLevel {
		w.conds = append(w.conds, "trial IS NULL")
	}
	from := `measurements`
	switch f.Generation {
	case "", "current":
		// The latest generation per trial, grader and annotator, and per
		// experiment, candidate and grader for a candidate-level value.
		from = `(SELECT *, max(generation) OVER (PARTITION BY experiment, coalesce(trial, candidate), grader, annotator) AS latest FROM measurements) m`
		w.conds = append(w.conds, "generation = latest")
	case "all":
	default:
		g, err := strconv.Atoi(f.Generation)
		if err != nil {
			return nil, errors.New("postgres: a generation that is not a number")
		}
		w.add("generation = %s", g)
	}
	tail := w.page("id", p)
	rows, err := x.s.pool.Query(ctx, `SELECT `+measurementColumns+` FROM `+from+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing measurements: %w", err)
	}
	return collectMeasurements(rows)
}

func (x experiments) CalibrationPairs(ctx context.Context, owner, grader, metric string) ([]store.CalibrationPair, error) {
	// The judge's latest generation on each trial, and each annotator's
	// latest under each annotation grader on the same trials.
	rows, err := x.s.pool.Query(ctx, `
		WITH judge AS (
			SELECT DISTINCT ON (trial) trial, value_bool, value_num, value_text FROM measurements
			WHERE owner = $1 AND grader = $2 AND metric = $3 AND annotator = '' AND trial IS NOT NULL
			ORDER BY trial, generation DESC
		), people AS (
			SELECT DISTINCT ON (trial, grader, annotator) trial, annotator, grader, value_bool, value_num, value_text FROM measurements
			WHERE owner = $1 AND metric = $3 AND annotator <> '' AND trial IN (SELECT trial FROM judge)
			ORDER BY trial, grader, annotator, generation DESC
		)
		SELECT j.trial, j.value_bool, j.value_num, j.value_text, p.value_bool, p.value_num, p.value_text
		FROM judge j JOIN people p ON p.trial = j.trial ORDER BY j.trial, p.grader, p.annotator`, owner, grader, metric)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading calibration pairs: %w", err)
	}
	defer rows.Close()
	var out []store.CalibrationPair
	for rows.Next() {
		var trial string
		var jb, pb *bool
		var jn, pn *float64
		var js, ps *string
		if err := rows.Scan(&trial, &jb, &jn, &js, &pb, &pn, &ps); err != nil {
			return nil, fmt.Errorf("postgres: reading a calibration pair: %w", err)
		}
		if len(out) == 0 || out[len(out)-1].Trial != trial {
			out = append(out, store.CalibrationPair{Trial: trial, Judge: valueOf(jb, jn, js)})
		}
		last := &out[len(out)-1]
		last.People = append(last.People, valueOf(pb, pn, ps))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading calibration pairs: %w", err)
	}
	return out, nil
}

// valueOf is a measurement's value from the column that holds it.
func valueOf(b *bool, n *float64, s *string) any {
	switch {
	case b != nil:
		return *b
	case n != nil:
		return *n
	case s != nil:
		return *s
	}
	return nil
}

func (x experiments) MeasurementsOf(ctx context.Context, trials []string) ([]objects.Measurement, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT `+measurementColumns+` FROM measurements WHERE trial = ANY($1) ORDER BY trial, id`, trials)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading measurements: %w", err)
	}
	return collectMeasurements(rows)
}

func (x experiments) Measurement(ctx context.Context, mid string) (objects.Measurement, error) {
	m, err := scanMeasurement(x.s.pool.QueryRow(ctx, `SELECT `+measurementColumns+` FROM measurements WHERE id = $1`, mid))
	return m, one(err, "a measurement")
}

func (x experiments) Events(ctx context.Context, eid string, after int64, limit int) ([]objects.Event, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT id, experiment, seq, type, data, at FROM events WHERE experiment = $1 AND seq > $2 ORDER BY seq LIMIT $3`, eid, after, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading events: %w", err)
	}
	defer rows.Close()
	var out []objects.Event
	for rows.Next() {
		var ev objects.Event
		if err := rows.Scan(&ev.ID, &ev.Experiment, &ev.Seq, &ev.Type, &ev.Data, &ev.At); err != nil {
			return nil, fmt.Errorf("postgres: reading an event: %w", err)
		}
		ev.At = ev.At.UTC()
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading events: %w", err)
	}
	return out, nil
}

func (x experiments) LatestSeq(ctx context.Context, eid string) (int64, error) {
	var next int64
	err := x.s.pool.QueryRow(ctx, `SELECT next_seq FROM experiments WHERE id = $1`, eid).Scan(&next)
	if err := one(err, "an experiment"); err != nil {
		return 0, err
	}
	return next - 1, nil
}
