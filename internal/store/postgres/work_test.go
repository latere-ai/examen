// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

const owner = "https://issuer.example.com|alice"

// world is a store over a database of its own, with the direct URL a test
// installs faults through.
type world struct {
	t   *testing.T
	st  *postgres.Store
	dsn string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	return newClockedWorld(t, nil)
}

// newClockedWorld is a world whose store reads the clock given.
func newClockedWorld(t *testing.T, now func() time.Time) *world {
	t.Helper()
	direct, _ := pgtest.Database(t)
	st, _, err := postgres.Open(t.Context(), direct, postgres.Options{Now: now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	w := &world{t: t, st: st, dsn: direct}
	w.exec(`CREATE TABLE test_faults (tbl text NOT NULL, op text NOT NULL, nth integer NOT NULL, seen integer NOT NULL DEFAULT 0)`)
	w.exec(`CREATE FUNCTION examen_test_fault() RETURNS trigger LANGUAGE plpgsql AS $$
		DECLARE n integer;
		BEGIN
			UPDATE test_faults SET seen = seen + 1 WHERE tbl = TG_TABLE_NAME AND op = TG_OP RETURNING seen - nth INTO n;
			IF n = 0 THEN
				RAISE EXCEPTION 'an injected fault on % of %', TG_OP, TG_TABLE_NAME;
			END IF;
			IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
			RETURN NEW;
		END $$`)
	for _, table := range faultTables {
		w.exec(`CREATE TRIGGER a_test_fault BEFORE INSERT OR UPDATE OR DELETE ON ` + table + ` FOR EACH ROW EXECUTE FUNCTION examen_test_fault()`)
	}
	return w
}

var faultTables = []string{"versions", "heads", "experiments", "cells", "trials", "measurements", "events", "baselines", "taxonomies", "comparisons", "comparison_inputs"}

func (w *world) exec(sql string, args ...any) {
	w.t.Helper()
	if err := pgtest.Exec(context.Background(), w.dsn, sql, args...); err != nil {
		w.t.Fatalf("%s: %v", sql, err)
	}
}

// fault makes the nth write of an operation to a table, within one
// transaction, fail; a transaction that fails rolls the count back, so
// every attempt counts from the start.
func (w *world) fault(table, op string, nth int) {
	w.exec(`DELETE FROM test_faults`)
	if nth > 0 {
		w.exec(`INSERT INTO test_faults (tbl, op, nth) VALUES ($1, $2, $3)`, table, op, nth)
	}
}

// eachFault runs an operation once per write it makes, failing that write,
// on a fresh state each time it succeeds; every failure must be the
// injected one, and nothing a failed attempt wrote may stay.
func (w *world) eachFault(name string, setup func() func() error) {
	w.t.Helper()
	for _, table := range faultTables {
		for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
			act := setup()
			for n := 1; ; n++ {
				if n > 200 {
					w.t.Fatalf("%s: %s %s never succeeded", name, op, table)
				}
				w.fault(table, op, n)
				err := act()
				if err == nil {
					break
				}
				if !strings.Contains(err.Error(), "an injected fault") {
					w.t.Fatalf("%s: %s %s #%d: %v", name, op, table, n, err)
				}
			}
			w.fault("", "", 0)
		}
	}
}

// put applies a version of a kind.
func (w *world) put(kind, name string, v any) objects.Version {
	w.t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		w.t.Fatal(err)
	}
	digest, err := id.DigestJSON(raw)
	if err != nil {
		w.t.Fatal(err)
	}
	out, _, err := w.st.Versions().Apply(w.t.Context(), objects.Version{ID: id.New(objects.Prefix(kind)), Kind: kind, Owner: owner, Name: name, Digest: digest, Body: raw}, store.Precondition{})
	if err != nil {
		w.t.Fatal(err)
	}
	return out
}

// plan is an experiment of two external candidates on one task, stored by
// make.
type plan struct {
	task, suite, a, b objects.Version
}

func (w *world) plan() plan {
	var p plan
	p.task = w.put(objects.KindTask, "one", objects.Task{Name: "one", Instruction: "do it"})
	p.suite = w.put(objects.KindSuite, "s", objects.Suite{Name: "s", Tasks: []string{p.task.Digest}, Metrics: []string{"builtin:completed", "builtin:declined"}, PrimaryMetric: "builtin:completed"})
	p.a = w.put(objects.KindCandidate, "a", objects.Candidate{Name: "a", Kind: objects.CandidateExternal, External: &objects.External{Harness: "h", Version: "1"}})
	p.b = w.put(objects.KindCandidate, "b", objects.Candidate{Name: "b", Kind: objects.CandidateExternal, External: &objects.External{Harness: "h", Version: "2"}})
	return p
}

// experiment is a new experiment of the plan in a mode, with n trials.
func (w *world) experiment(p plan, mode string, n int) (store.NewExperiment, []string) {
	e := objects.Experiment{ID: id.New(id.Experiment), Owner: owner, Suite: p.suite.Digest, SuiteID: p.suite.ID, SuiteName: "s",
		Candidates: []string{p.a.Digest, p.b.Digest}, CandidateIDs: []string{p.a.ID, p.b.ID}, Trials: n, Budget: objects.Budget{Name: "b"},
		Parallelism: 4, Reference: objects.Reference{Candidate: p.a.Digest}, Mode: mode, CreatedBy: owner, Metadata: map[string]string{"k": "v"}}
	ne := store.NewExperiment{Experiment: e, MaxActive: 1000, Comparisons: []objects.Comparison{{
		ID: id.New(id.Comparison), Candidate: p.b.Digest, ReferenceCandidate: p.a.Digest, Automatic: true, State: objects.ComparisonComputing,
	}}}
	var cells []string
	if mode == objects.ModeExecute {
		for i, c := range []objects.Version{p.a, p.b} {
			cell := objects.Cell{ID: id.New(id.Cell), Task: p.task.Digest, TaskID: p.task.ID, Candidate: c.Digest, CandidateID: c.ID, CandidatePos: i}
			smoke := execute.Smoke(cell.ID, n)
			ne.Cells = append(ne.Cells, store.NewCell{Cell: cell, Smoke: &store.Rollout{Rollout: smoke.Rollout, Trials: []store.RolloutTrial{{Index: 0}}}})
			cells = append(cells, cell.ID)
		}
	}
	return ne, cells
}

// running is a stored experiment of the plan whose smoke rollouts are
// submitted.
func (w *world) running(ctx context.Context, p plan, n int) (string, []string) {
	w.t.Helper()
	ne, cells := w.experiment(p, objects.ModeExecute, n)
	if err := w.st.Experiments().Create(ctx, ne); err != nil {
		w.t.Fatal(err)
	}
	for _, c := range cells {
		if err := w.st.Experiments().SubmitRollout(ctx, c, c+".smoke"); err != nil {
			w.t.Fatal(err)
		}
	}
	return ne.Experiment.ID, cells
}

// outcome is a run's outcome.
func outcome(o string) store.Outcome {
	start := time.Date(2026, 3, 2, 14, 1, 0, 0, time.UTC)
	out := store.Outcome{Outcome: o, StartedAt: start, AgentEndedAt: start.Add(time.Minute), Declined: true, Harness: "h",
		Observed: objects.Observed{CostState: objects.CostUnknown}}
	if o == objects.OutcomeInfraFailed || o == objects.OutcomeOverBudget {
		out.Failure = &objects.Failure{Code: "x"}
	} else {
		out.SelfReported = []store.Measured{{Metric: "sha256:" + strings.Repeat("a", 64), Value: "label"}}
	}
	return out
}

func TestWritesFailWhole(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	x := w.st.Experiments()
	ctx := t.Context()
	w.eachFault("create", func() func() error {
		ne, _ := w.experiment(p, objects.ModeExecute, 2)
		return func() error { return x.Create(ctx, ne) }
	})
	w.eachFault("submit a rollout", func() func() error {
		ne, cells := w.experiment(p, objects.ModeExecute, 2)
		if err := x.Create(ctx, ne); err != nil {
			t.Fatal(err)
		}
		return func() error { return x.SubmitRollout(ctx, cells[0], cells[0]+".smoke") }
	})
	leased := func(o string) func() func() error {
		return func() func() error {
			eid, cells := w.running(ctx, p, 2)
			l, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells})
			if err != nil || len(l.Trials) != 1 {
				t.Fatalf("lease %+v %v", l, err)
			}
			return func() error {
				_, err := x.Submit(ctx, store.Submission{Trial: l.Trials[0].ID, Lease: l.Lease, Subject: owner, Digest: "d", Outcome: outcome(o)})
				return err
			}
		}
	}
	w.eachFault("submit completed", leased(objects.OutcomeCompleted))
	w.eachFault("submit infra_failed", leased(objects.OutcomeInfraFailed))
	w.eachFault("submit over_budget", leased(objects.OutcomeOverBudget))
	w.eachFault("lease", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		return func() error {
			_, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells})
			return err
		}
	})
	w.eachFault("renew", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		l, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells})
		if err != nil {
			t.Fatal(err)
		}
		return func() error {
			_, err := x.Renew(ctx, eid, l.Lease, owner)
			return err
		}
	})
	w.eachFault("cancel", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		if _, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells}); err != nil {
			t.Fatal(err)
		}
		return func() error { _, err := x.Cancel(ctx, eid); return err }
	})
	w.eachFault("park", func() func() error {
		_, cells := w.running(ctx, p, 2)
		return func() error { return x.Park(ctx, cells[0], objects.StateCanceled, "run_denied") }
	})
	importCell := func(eid string) objects.Cell {
		return objects.Cell{Task: p.task.Digest, TaskID: p.task.ID, Candidate: p.a.Digest, CandidateID: p.a.ID}
	}
	w.eachFault("import", func() func() error {
		ne, _ := w.experiment(p, objects.ModeImport, 2)
		if err := x.Create(ctx, ne); err != nil {
			t.Fatal(err)
		}
		eid := ne.Experiment.ID
		return func() error {
			o := outcome(objects.OutcomeCompleted)
			o.Ref = "run-1"
			_, err := x.Import(ctx, store.Import{Experiment: eid, Cell: importCell(eid), Outcome: o})
			return err
		}
	})
	w.eachFault("close", func() func() error {
		ne, _ := w.experiment(p, objects.ModeImport, 2)
		if err := x.Create(ctx, ne); err != nil {
			t.Fatal(err)
		}
		eid := ne.Experiment.ID
		if _, err := x.Import(ctx, store.Import{Experiment: eid, Cell: importCell(eid), Outcome: outcome(objects.OutcomeCompleted)}); err != nil {
			t.Fatal(err)
		}
		return func() error { _, err := x.Close(ctx, eid); return err }
	})
	w.eachFault("retry", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		if err := x.Park(ctx, cells[0], objects.StateBlocked, "smoke_infra_failed"); err != nil {
			t.Fatal(err)
		}
		_ = eid
		return func() error { _, err := x.RetryCell(ctx, cells[0]); return err }
	})
	w.eachFault("compute", func() func() error {
		eid, _ := w.running(ctx, p, 2)
		cs, err := x.Comparisons(ctx, store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		return func() error {
			return x.Computed(ctx, cs[0].ID, objects.ComparisonComplete, objects.Computed{Confounds: []string{}}, []string{"msr_1", "msr_2"})
		}
	})
	w.eachFault("comparison", func() func() error {
		eid, _ := w.running(ctx, p, 2)
		return func() error {
			return x.CreateComparison(ctx, objects.Comparison{ID: id.New(id.Comparison), Owner: owner, Experiment: eid, Candidate: p.b.Digest, ReferenceCandidate: p.a.Digest, State: objects.ComparisonComputing})
		}
	})
	w.eachFault("apply", func() func() error {
		n := id.New(id.Metric)[4:12]
		return func() error {
			_, _, err := w.st.Versions().Apply(ctx, objects.Version{ID: id.New(id.Metric), Kind: objects.KindMetric, Owner: owner, Name: "m" + n, Digest: "sha256:" + strings.Repeat("b", 56) + n, Body: json.RawMessage(`{}`)}, store.Precondition{})
			return err
		}
	})
}

func TestRecordsFailWhole(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	x := w.st.Experiments()
	ne, _ := w.experiment(p, objects.ModeImport, 2)
	if err := x.Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	suite := objects.SuiteName{Owner: owner, Name: "s"}
	w.eachFault("pin", func() func() error {
		name := "b" + strings.ToLower(id.New(id.Baseline)[4:10])
		return func() error {
			_, _, err := w.st.Baselines().Pin(ctx, objects.Baseline{ID: id.New(id.Baseline), Owner: owner, Name: name, Suite: suite, Experiment: ne.Experiment.ID, Candidate: p.a.Digest, CreatedBy: owner}, store.Precondition{})
			return err
		}
	})
	w.eachFault("append", func() func() error {
		metric := "sha256:" + strings.Repeat("c", 52) + strings.ToLower(id.New(id.Taxonomy)[4:16])
		return func() error {
			_, _, err := w.st.Taxonomies().Append(ctx, objects.Taxonomy{ID: id.New(id.Taxonomy), Owner: owner, Suite: suite, Metric: metric,
				Canonical: []string{"a"}, Map: map[string]string{"x": "a"}, Digest: "sha256:d", CreatedBy: owner, Source: objects.TaxonomySource{Owner: owner}}, store.Precondition{})
			return err
		}
	})
}

// What a transaction refuses on the state it read is the API's error.
func TestStateRefusals(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	x := w.st.Experiments()
	code := func(err error) string {
		var e *apierr.Error
		if errors.As(err, &e) {
			return e.Code
		}
		return "not an apierr: " + errStr(err)
	}
	eid, cells := w.running(ctx, p, 2)
	l, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Renew(ctx, eid, l.Lease, "https://issuer.example.com|bob"); code(err) != apierr.CodeLeaseNotHeld {
		t.Fatalf("renew by another %v", err)
	}
	if _, err := x.Submit(ctx, store.Submission{Trial: l.Trials[0].ID, Lease: l.Lease, Subject: "https://issuer.example.com|bob", Outcome: outcome(objects.OutcomeCompleted)}); code(err) != apierr.CodeLeaseNotHeld {
		t.Fatalf("submit by another %v", err)
	}
	if _, err := x.Submit(ctx, store.Submission{Trial: id.New(id.Trial), Lease: l.Lease, Subject: owner}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a missing trial %v", err)
	}
	if _, err := x.Lease(ctx, store.LeaseRequest{Experiment: id.New(id.Experiment)}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a missing experiment %v", err)
	}
	if _, err := x.RetryCell(ctx, id.New(id.Cell)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a missing cell %v", err)
	}
	if err := x.SubmitRollout(ctx, id.New(id.Cell), "k"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a missing cell %v", err)
	}
	if err := x.Park(ctx, id.New(id.Cell), objects.StateCanceled, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a missing cell %v", err)
	}
	if err := x.Computed(ctx, id.New(id.Comparison), objects.ComparisonComplete, objects.Computed{}, nil); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a missing comparison %v", err)
	}
	// A rollout that is not queued, and a cell that is parked, submit
	// nothing; a parked cell parks no further.
	if err := x.SubmitRollout(ctx, cells[0], "nope"); err != nil {
		t.Fatal(err)
	}
	if err := x.Park(ctx, cells[1], objects.StateCanceled, "x"); err != nil {
		t.Fatal(err)
	}
	if err := x.Park(ctx, cells[1], objects.StateBlocked, "y"); err != nil {
		t.Fatal(err)
	}
	if c, err := x.Cell(ctx, cells[1]); err != nil || c.ParkedReason != "x" {
		t.Fatalf("parked once %+v %v", c, err)
	}
	if _, err := x.Cancel(ctx, eid); err != nil {
		t.Fatal(err)
	}
	if err := x.SubmitRollout(ctx, cells[0], cells[0]+".smoke"); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Import(ctx, store.Import{Experiment: eid}); code(err) != apierr.CodeWrongMode {
		t.Fatalf("an import into mode execute %v", err)
	}
	// The active experiments of an owner are counted with the insert.
	ne, _ := w.experiment(p, objects.ModeImport, 1)
	ne.MaxActive = 0
	if err := x.Create(ctx, ne); !errors.Is(err, store.ErrActiveLimit) {
		t.Fatalf("over the active limit %v", err)
	}
	if err := pgtest.Exec(ctx, w.dsn, `DELETE FROM experiments WHERE id = $1`, eid); err != nil {
		t.Fatalf("deleting an experiment cascades past the append-only rule: %v", err)
	}
}

func errStr(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// A measurement is never updated, and never deleted while its trial
// exists; deleting its experiment takes it.
func TestMeasurementsAreAppendOnly(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	x := w.st.Experiments()
	ne, _ := w.experiment(p, objects.ModeImport, 2)
	if err := x.Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	tr, err := x.Import(ctx, store.Import{Experiment: ne.Experiment.ID, Cell: objects.Cell{Task: p.task.Digest, TaskID: p.task.ID, Candidate: p.a.Digest, CandidateID: p.a.ID}, Outcome: outcome(objects.OutcomeCompleted)})
	if err != nil {
		t.Fatal(err)
	}
	ms, err := x.MeasurementsOf(ctx, []string{tr.ID})
	if err != nil || len(ms) != 4 {
		t.Fatalf("builtin, the decline judge and the harness's own: %v %v", ms, err)
	}
	for _, sql := range []string{`UPDATE measurements SET rationale = 'x'`, `DELETE FROM measurements`} {
		if err := pgtest.Exec(ctx, w.dsn, sql); err == nil || !strings.Contains(err.Error(), "never") {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for _, sql := range []string{`UPDATE versions SET name = 'x'`, `DELETE FROM versions`} {
		if err := pgtest.Exec(ctx, w.dsn, sql); err == nil || !strings.Contains(err.Error(), "never changed") {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	if err := pgtest.Exec(ctx, w.dsn, `DELETE FROM experiments WHERE id = $1`, ne.Experiment.ID); err != nil {
		t.Fatalf("deleting the experiment takes its measurements: %v", err)
	}
	// The generations and the filters of the measurement list.
	if got, err := x.Measurements(ctx, store.MeasurementFilter{Trial: tr.ID, Generation: "x"}, store.Page{Limit: 10}); err == nil {
		t.Fatalf("a generation that is not a number %v", got)
	}
}

// A closed store answers every read with an error.
func TestReadsOnAClosedStore(t *testing.T) {
	direct, _ := pgtest.Database(t)
	st, _, err := postgres.Open(t.Context(), direct, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	x := st.Experiments()
	page := store.Page{Limit: 1}
	suite := objects.SuiteName{Owner: owner, Name: "s"}
	calls := []func() error{
		func() error { _, err := x.Get(ctx, "e"); return err },
		func() error { _, err := x.List(ctx, store.ExperimentFilter{Owners: []string{owner}}, page); return err },
		func() error { _, err := x.Cells(ctx, "e", store.CellFilter{}, page); return err },
		func() error { _, err := x.AllCells(ctx, "e"); return err },
		func() error { _, err := x.Cell(ctx, "c"); return err },
		func() error { _, err := x.Counts(ctx, "e"); return err },
		func() error { _, err := x.Trials(ctx, "e", store.TrialFilter{}, page); return err },
		func() error { _, err := x.Trial(ctx, "t"); return err },
		func() error { _, err := x.Measurements(ctx, store.MeasurementFilter{}, page); return err },
		func() error { _, err := x.MeasurementsOf(ctx, []string{"t"}); return err },
		func() error { _, err := x.Measurement(ctx, "m"); return err },
		func() error { _, err := x.Events(ctx, "e", 0, 1); return err },
		func() error { _, err := x.LatestSeq(ctx, "e"); return err },
		func() error { _, err := x.Cancel(ctx, "e"); return err },
		func() error { _, err := x.Close(ctx, "e"); return err },
		func() error { _, err := x.RetryCell(ctx, "c"); return err },
		func() error { _, err := x.Import(ctx, store.Import{Experiment: "e"}); return err },
		func() error { _, err := x.Lease(ctx, store.LeaseRequest{Experiment: "e"}); return err },
		func() error { _, err := x.Renew(ctx, "e", "l", owner); return err },
		func() error { _, err := x.Submit(ctx, store.Submission{Trial: "t"}); return err },
		func() error { _, err := x.ExpireLeases(ctx); return err },
		func() error { _, err := x.Queued(ctx, "", 1); return err },
		func() error { return x.SubmitRollout(ctx, "c", "k") },
		func() error { return x.Park(ctx, "c", "canceled", "x") },
		func() error { _, err := x.Leasable(ctx, "e", "c"); return err },
		func() error { _, err := x.Comparisons(ctx, store.ComparisonFilter{}, page); return err },
		func() error { _, err := x.Comparison(ctx, "c"); return err },
		func() error { return x.CreateComparison(ctx, objects.Comparison{}) },
		func() error { _, err := x.Dirty(ctx, 1); return err },
		func() error { return x.Computed(ctx, "c", "complete", objects.Computed{}, nil) },
		func() error { return x.Clean(ctx, "c", time.Now()) },
		func() error { return x.Create(ctx, store.NewExperiment{}) },
		func() error {
			_, _, err := st.Versions().Apply(ctx, objects.Version{}, store.Precondition{})
			return err
		},
		func() error { _, err := st.Versions().ByID(ctx, "metric", "x"); return err },
		func() error { _, err := st.Versions().ByDigest(ctx, "metric", owner, "x"); return err },
		func() error { _, err := st.Versions().Current(ctx, "metric", owner, "x"); return err },
		func() error { _, err := st.Versions().History(ctx, "metric", owner, "x", page); return err },
		func() error {
			_, err := st.Versions().List(ctx, "task", store.VersionFilter{Owners: []string{owner}, Labels: map[string]string{"a": "b"}}, page)
			return err
		},
		func() error { _, err := st.Versions().Many(ctx, []string{"x"}); return err },
		func() error {
			_, _, err := st.Baselines().Pin(ctx, objects.Baseline{}, store.Precondition{})
			return err
		},
		func() error { _, err := st.Baselines().ByID(ctx, "b"); return err },
		func() error { _, err := st.Baselines().Current(ctx, owner, suite, "b"); return err },
		func() error { _, err := st.Baselines().History(ctx, owner, suite, "b", page); return err },
		func() error { _, err := st.Baselines().List(ctx, []string{owner}, suite, page); return err },
		func() error {
			_, _, err := st.Taxonomies().Append(ctx, objects.Taxonomy{}, store.Precondition{})
			return err
		},
		func() error { _, err := st.Taxonomies().ByID(ctx, "t"); return err },
		func() error { _, err := st.Taxonomies().Current(ctx, suite, "m"); return err },
		func() error { _, err := st.Taxonomies().History(ctx, suite, "m", page); return err },
		func() error { _, err := st.Taxonomies().Labels(ctx, suite, "m"); return err },
	}
	for i, call := range calls {
		if err := call(); err == nil {
			t.Errorf("call %d answered on a closed store", i)
		}
	}
}

// Lapsed leases return their trials to pending whole, or not at all; a
// cell's trials lease smoke first, then retries, then the main rollout's,
// by index; and a retry of a complete experiment's parked cell reopens it
// whole.
func TestLapsesLeasesAndReopensFailWhole(t *testing.T) {
	var mu sync.Mutex
	now := time.Now().UTC().Truncate(time.Second)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	advance := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }
	w := newClockedWorld(t, clock)
	p := w.plan()
	ctx := t.Context()
	x := w.st.Experiments()
	w.eachFault("expire", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		if _, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells}); err != nil {
			t.Fatal(err)
		}
		advance(limits.ExternalLease + time.Second)
		return func() error {
			_, err := x.ExpireLeases(ctx)
			return err
		}
	})
	// Four trials: once the smoke passes, the main rollout's three lease
	// by index.
	eid, cells := w.running(ctx, p, 4)
	l, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Submit(ctx, store.Submission{Trial: l.Trials[0].ID, Lease: l.Lease, Subject: owner, Digest: "d", Outcome: outcome(objects.OutcomeCompleted)}); err != nil {
		t.Fatal(err)
	}
	queued, err := x.Queued(ctx, eid, 10)
	if err != nil || len(queued) != 1 {
		t.Fatalf("the main rollout is queued %v %v", queued, err)
	}
	if err := x.SubmitRollout(ctx, queued[0].Cell, queued[0].Key); err != nil {
		t.Fatal(err)
	}
	main, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells, Max: 2})
	if err != nil || len(main.Trials) != 2 || main.Trials[0].Index != 1 || main.Trials[1].Index != 2 {
		t.Fatalf("main by index %+v %v", main, err)
	}
	// A complete experiment with a parked cell reopens at its retry.
	w.eachFault("reopen", func() func() error {
		eid, cells := w.running(ctx, p, 1)
		l, err := x.Lease(ctx, store.LeaseRequest{Experiment: eid, Candidate: p.a.Digest, Subject: owner, Cells: cells})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := x.Submit(ctx, store.Submission{Trial: l.Trials[0].ID, Lease: l.Lease, Subject: owner, Digest: "d", Outcome: outcome(objects.OutcomeOverBudget)}); err != nil {
			t.Fatal(err)
		}
		if err := x.Park(ctx, cells[1], objects.StateBlocked, "smoke_infra_failed"); err != nil {
			t.Fatal(err)
		}
		if e, err := x.Get(ctx, eid); err != nil || e.State != objects.StateComplete {
			t.Fatalf("complete with parked cells %v %v", e.State, err)
		}
		return func() error { _, err := x.RetryCell(ctx, cells[1]); return err }
	})
}
