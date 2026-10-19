// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

const owner = "https://issuer.example.com|alice"

// gate answers experiment.run as the test sets it.
type gate struct {
	mu   sync.Mutex
	mode string
	asks int
}

func (g *gate) set(mode string) { g.mu.Lock(); g.mode = mode; g.mu.Unlock() }

func (g *gate) Authorize(_ context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asks++
	if q.Action != authorizer.ActionExperimentRun || c.Subject != owner || len(c.Claims) != 0 ||
		q.Resource.String(authorizer.FieldPurpose) != authorizer.PurposeTrial || q.Resource.String(authorizer.FieldCell) == "" {
		return access.Decision{}, errors.New("an unexpected question")
	}
	switch g.mode {
	case "deny":
		return access.Decision{Reason: "stopped"}, nil
	case "unavailable":
		return access.Decision{}, access.ErrUnavailable
	case "broken":
		return access.Decision{}, errors.New("the client broke")
	}
	return access.Decision{Allow: true}, nil
}

// figures answers a fixed comparison, an error, or not computed.
type figures struct{ mode string }

func (f figures) Compare(context.Context, objects.Comparison, compare.Source) (objects.Computed, []string, error) {
	switch f.mode {
	case "fail":
		return objects.Computed{}, nil, errors.New("the analysis broke")
	case "none":
		return objects.Computed{}, nil, compare.ErrNotComputed
	}
	return objects.Computed{Confounds: []string{}, Results: []objects.ResultRow{{Level: "suite", Verdict: "within_noise"}}, AnalysisVersion: "examen.analysis/v1"}, []string{"msr_a"}, nil
}

func (figures) Summaries(context.Context, objects.Experiment, []objects.Cell, string, compare.Source) (map[string]json.RawMessage, error) {
	return nil, compare.ErrNotComputed
}

func (figures) Declared(context.Context, objects.Experiment, []objects.Comparison, compare.Source) (objects.DeclaredAnalysis, error) {
	return objects.DeclaredAnalysis{}, compare.ErrNotComputed
}

// seed stores one experiment of two external candidates on one task, in
// mode execute, with its smoke rollouts queued and its automatic
// comparison, and answers its id and its cells' ids.
func seed(t *testing.T, st store.Store) (string, []string) {
	t.Helper()
	return seedWith(t, st, nil)
}

// seedWith is seed with the new experiment changed by change before it is
// stored.
func seedWith(t *testing.T, st store.Store, change func(*store.NewExperiment)) (string, []string) {
	t.Helper()
	ctx := t.Context()
	put := func(kind, name string, v any) objects.Version {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := id.DigestJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		out, _, err := st.Versions().Apply(ctx, objects.Version{ID: id.New(objects.Prefix(kind)), Kind: kind, Owner: owner, Name: name, Digest: digest, Body: raw}, store.Precondition{})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	task := put(objects.KindTask, "one", objects.Task{Name: "one", Instruction: "do it"})
	suite := put(objects.KindSuite, "s", objects.Suite{Name: "s", Tasks: []string{task.Digest}, Metrics: []string{"builtin:completed"}, PrimaryMetric: "builtin:completed"})
	a := put(objects.KindCandidate, "a", objects.Candidate{Name: "a", Kind: objects.CandidateExternal, External: &objects.External{Harness: "h", Version: "1"}})
	b := put(objects.KindCandidate, "b", objects.Candidate{Name: "b", Kind: objects.CandidateExternal, External: &objects.External{Harness: "h", Version: "2"}})
	e := objects.Experiment{ID: id.New(id.Experiment), Owner: owner, Suite: suite.Digest, SuiteID: suite.ID, SuiteName: "s",
		Candidates: []string{a.Digest, b.Digest}, CandidateIDs: []string{a.ID, b.ID}, Trials: 2, Budget: objects.Budget{Name: "b"},
		Parallelism: 4, Reference: objects.Reference{Candidate: a.Digest}, Mode: objects.ModeExecute, CreatedBy: owner}
	ne := store.NewExperiment{Experiment: e, MaxActive: 16, Comparisons: []objects.Comparison{{
		ID: id.New(id.Comparison), Candidate: b.Digest, ReferenceCandidate: a.Digest, Automatic: true, State: objects.ComparisonComputing,
	}}}
	var cells []string
	for i, c := range []objects.Version{a, b} {
		cell := objects.Cell{ID: id.New(id.Cell), Task: task.Digest, TaskID: task.ID, Candidate: c.Digest, CandidateID: c.ID, CandidatePos: i}
		smoke := execute.Smoke(cell.ID, e.Trials)
		nc := store.NewCell{Cell: cell, Smoke: &store.Rollout{Rollout: smoke.Rollout, Trials: []store.RolloutTrial{{Index: 0}}}}
		ne.Cells = append(ne.Cells, nc)
		cells = append(cells, cell.ID)
	}
	if change != nil {
		change(&ne)
	}
	if err := st.Experiments().Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	return e.ID, cells
}

// declaring answers figures, and a declared analysis or an error.
type declaring struct {
	figures
	fail bool
}

func (d declaring) Declared(_ context.Context, e objects.Experiment, family []objects.Comparison, _ compare.Source) (objects.DeclaredAnalysis, error) {
	if d.fail {
		return objects.DeclaredAnalysis{}, errors.New("the declared analysis broke")
	}
	return objects.DeclaredAnalysis{Experiment: e.ID, Family: objects.DeclaredFamily{Method: "benjamini_hochberg", Size: len(e.Comparisons), Level: 0.05},
		Comparisons: []objects.DeclaredResult{{Index: 0, Comparison: family[0].ID, Reason: "computed"}}}, nil
}

// Computing a declared comparison recomputes its experiment's declared
// analysis and stores it; one that fails is an error of the round.
func TestComputeDeclares(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	eid, _ := seedWith(t, st, func(ne *store.NewExperiment) {
		ne.Experiment.Comparisons = []objects.DeclaredComparison{{Candidate: ne.Comparisons[0].Candidate, ReferenceCandidate: ne.Comparisons[0].ReferenceCandidate}}
		zero := 0
		ne.Comparisons[0].DeclaredIndex = &zero
	})
	e := &Engine{Store: st, Authz: &gate{}, Analyzer: declaring{fail: true}}
	if err := e.Compute(t.Context()); err == nil || !strings.Contains(err.Error(), "the declared analysis broke") {
		t.Fatalf("a failed declared analysis: %v", err)
	}
	if _, found, err := st.Experiments().DeclaredAnalysis(t.Context(), eid); err != nil || found {
		t.Fatalf("a failed declared analysis stored one: %v %v", found, err)
	}
	if err := st.Experiments().Computed(t.Context(), mustComparison(t, st, eid).ID, objects.ComparisonComplete, objects.Computed{Confounds: []string{}, Results: []objects.ResultRow{}}, nil); err != nil {
		t.Fatal(err)
	}
	// Mark it again, as a finished trial does, and compute.
	c := mustComparison(t, st, eid)
	c.ID = id.New(id.Comparison)
	c.State, c.Computed = objects.ComparisonComputing, objects.Computed{}
	if err := st.Experiments().CreateComparison(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	e.Analyzer = declaring{}
	if err := e.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, found, err := st.Experiments().DeclaredAnalysis(t.Context(), eid)
	if err != nil || !found || a.Family.Size != 1 || a.Comparisons[0].Comparison != c.ID {
		t.Fatalf("the declared analysis %+v %v %v", a, found, err)
	}
}

// independent is an analyzer whose declared analysis holds a test of
// independence and no declared comparison.
type independent struct {
	figures
	asked *int
}

func (d independent) Declared(_ context.Context, e objects.Experiment, family []objects.Comparison, _ compare.Source) (objects.DeclaredAnalysis, error) {
	*d.asked++
	return objects.DeclaredAnalysis{Experiment: e.ID, Family: objects.DeclaredFamily{Method: "benjamini_hochberg", Level: 0.05},
		Comparisons: []objects.DeclaredResult{}, Independence: json.RawMessage(`{"p":0.5}`)}, nil
}

// An experiment that declares a test of independence and no comparison has
// its declared analysis recomputed whenever one of its comparisons is, and
// one that declares neither is never asked for one.
func TestComputeDeclaresIndependence(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	eid, _ := seedWith(t, st, func(ne *store.NewExperiment) {
		ne.Experiment.Independence = &objects.Independence{Metric: "builtin:completed", Variable: "candidate", By: []string{"task"}}
	})
	if e, err := st.Experiments().Get(t.Context(), eid); err != nil || e.Independence == nil || e.Independence.Variable != "candidate" {
		t.Fatalf("the stored test %+v %v", e.Independence, err)
	}
	asked := 0
	e := &Engine{Store: st, Authz: &gate{}, Analyzer: independent{asked: &asked}}
	if err := e.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	a, found, err := st.Experiments().DeclaredAnalysis(t.Context(), eid)
	if err != nil || !found || string(a.Independence) != `{"p": 0.5}` || asked != 1 {
		t.Fatalf("the declared analysis %s %v %v, asked %d", a.Independence, found, err, asked)
	}
	plain, _ := seed(t, st)
	asked = 0
	if err := e.Compute(t.Context()); err != nil || asked != 0 {
		t.Fatalf("an experiment that declares nothing was asked %d times: %v", asked, err)
	}
	if _, found, err := st.Experiments().DeclaredAnalysis(t.Context(), plain); err != nil || found {
		t.Fatalf("an experiment that declares nothing has a declared analysis: %v %v", found, err)
	}
}

// mustComparison is an experiment's first comparison.
func mustComparison(t *testing.T, st store.Store, eid string) objects.Comparison {
	t.Helper()
	list, err := st.Experiments().Comparisons(t.Context(), store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 10})
	if err != nil || len(list) == 0 {
		t.Fatalf("comparisons %v %v", list, err)
	}
	return list[len(list)-1]
}

func TestAdvance(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	g := &gate{}
	var logs bytes.Buffer
	e := &Engine{Store: st, Authz: g, Analyzer: figures{}, Log: slog.New(slog.NewJSONHandler(&logs, nil))}
	eid, cells := seed(t, st)
	g.set("unavailable")
	if err := e.Advance(t.Context(), eid); err != nil {
		t.Fatal(err)
	}
	if exp, err := st.Experiments().Get(t.Context(), eid); err != nil || exp.State != objects.StatePending {
		t.Fatalf("an outage submits nothing: %v %v", exp.State, err)
	}
	if !strings.Contains(logs.String(), "no decision on a rollout") {
		t.Fatal("the outage is not logged")
	}
	g.set("broken")
	if err := e.Advance(t.Context(), eid); err == nil {
		t.Fatal("a broken client is an error")
	}
	g.set("")
	if err := e.Advance(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	exp, err := st.Experiments().Get(t.Context(), eid)
	if err != nil || exp.State != objects.StateRunning || exp.Progress.Cells[objects.StateSmoking] != 2 {
		t.Fatalf("submitted: %+v %v", exp.Progress, err)
	}
	// A second advance finds nothing queued.
	if err := e.Advance(t.Context(), eid); err != nil {
		t.Fatal(err)
	}
	// Handing out trials asks again: an outage leaves a cell out, a deny
	// parks it canceled with run_denied.
	all, err := st.Experiments().AllCells(t.Context(), eid)
	if err != nil {
		t.Fatal(err)
	}
	g.set("unavailable")
	if ok, err := e.Allowed(t.Context(), exp, all); err != nil || len(ok) != 0 {
		t.Fatalf("an outage allows none: %v %v", ok, err)
	}
	g.set("broken")
	if _, err := e.Allowed(t.Context(), exp, all); err == nil {
		t.Fatal("a broken client is an error")
	}
	g.set("")
	if ok, err := e.Allowed(t.Context(), exp, all); err != nil || len(ok) != 2 {
		t.Fatalf("allowed %v %v", ok, err)
	}
	g.set("deny")
	if ok, err := e.Allowed(t.Context(), exp, all[:1]); err != nil || len(ok) != 0 {
		t.Fatalf("denied %v %v", ok, err)
	}
	c, err := st.Experiments().Cell(t.Context(), cells[0])
	if err != nil || c.State != objects.StateCanceled || c.ParkedReason != ReasonRunDenied {
		t.Fatalf("the denied cell %+v %v", c, err)
	}
}

// A rollout queued for a cell the authorizer stops parks it.
func TestAdvanceDenied(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	g := &gate{mode: "deny"}
	e := &Engine{Store: st, Authz: g, Analyzer: figures{}}
	eid, _ := seed(t, st)
	if err := e.Advance(t.Context(), eid); err != nil {
		t.Fatal(err)
	}
	exp, err := st.Experiments().Get(t.Context(), eid)
	if err != nil || exp.Progress.Cells[objects.StateCanceled] != 2 || exp.State != objects.StateComplete {
		t.Fatalf("every cell denied completes the experiment: %s %+v %v", exp.State, exp.Progress, err)
	}
}

func TestCompute(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	eid, _ := seed(t, st)
	for _, c := range []struct {
		mode, state string
	}{{"none", objects.ComparisonComputing}, {"fail", objects.ComparisonFailed}, {"", objects.ComparisonComplete}} {
		cs, err := st.Experiments().Comparisons(t.Context(), store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		// Mark it dirty again, as a trial reaching done would.
		if err := st.Experiments().CreateComparison(t.Context(), objects.Comparison{ID: id.New(id.Comparison), Owner: owner, Experiment: eid, Candidate: cs[0].Candidate, ReferenceCandidate: cs[0].ReferenceCandidate, State: objects.ComparisonComputing}); err != nil {
			t.Fatal(err)
		}
		e := &Engine{Store: st, Authz: &gate{}, Analyzer: figures{mode: c.mode}}
		if err := e.Compute(t.Context()); err != nil {
			t.Fatalf("%s: %v", c.mode, err)
		}
		dirty, err := st.Experiments().Dirty(t.Context(), 10)
		if err != nil || len(dirty) != 0 {
			t.Fatalf("%s: dirty after a computation %v %v", c.mode, dirty, err)
		}
		cs, err = st.Experiments().Comparisons(t.Context(), store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 10})
		if err != nil || cs[0].State != c.state {
			t.Fatalf("%s: %+v %v", c.mode, cs[0], err)
		}
	}
	events, err := st.Experiments().Events(t.Context(), eid, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != objects.EventComparison {
		t.Fatalf("a computation journals a comparison event: %+v", events[len(events)-1])
	}
}

// The ticker lapses leases, submits queued rollouts and computes
// comparisons, and stops with its context.
func TestTickAndLoop(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	eid, _ := seed(t, st)
	e := &Engine{Store: st, Authz: &gate{}, Analyzer: figures{}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { e.Loop(ctx, time.Millisecond); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		exp, err := st.Experiments().Get(t.Context(), eid)
		if err != nil {
			t.Fatal(err)
		}
		cs, err := st.Experiments().Comparisons(t.Context(), store.ComparisonFilter{Experiment: eid}, store.Page{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if exp.State == objects.StateRunning && cs[0].State == objects.ComparisonComplete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the ticker did not take the work")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	// A failing round is logged and the loop goes on.
	var logs bytes.Buffer
	failing := &Engine{Store: st, Authz: &gate{mode: "broken"}, Analyzer: figures{}, Log: slog.New(slog.NewJSONHandler(&logs, nil))}
	if err := failing.Tick(t.Context()); err != nil {
		t.Fatalf("nothing is queued, so a broken authorizer is never asked: %v", err)
	}
	if failing.log() == nil || (&Engine{}).log() == nil {
		t.Fatal("no logger")
	}
}
