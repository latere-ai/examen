// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/objects"
)

// factorWorld is one experiment of a suite whose four tasks declare the
// factors object and volition, a closed label metric a check produces, and
// the candidates given, each holding a complete cell of every task whose
// trials carry the labels values gives it. candidates and values are in
// the experiment's order.
func factorWorld(t *testing.T, tasks []string, factors map[string]map[string]string, values map[string]map[string]string, candidates ...string) *world {
	t.Helper()
	w := newWorld(t)
	w.v.put(t, objects.KindMetric, "met_kind", kind, objects.Metric{Name: "kind", Type: objects.TypeLabel, Labels: []string{"a", "b"}, Direction: objects.DirectionNone, Aggregate: objects.AggregateDistribution})
	w.v.put(t, objects.KindGrader, "grd_kinds", "sha256:kinds", objects.Grader{Name: "kinds", Kind: objects.GraderCheck, Produces: []string{kind}})
	var digests []string
	for _, name := range tasks {
		d := "sha256:task-" + name
		digests = append(digests, d)
		w.v.put(t, objects.KindTask, "tsk_"+name, d, objects.Task{Name: name, Checks: []string{"sha256:kinds"}, Factors: factors[name]})
	}
	w.v.put(t, objects.KindSuite, "sui_f", "sha256:suite-f", objects.Suite{Name: "f", Tasks: digests, Metrics: []string{kind, "builtin:completed"}, PrimaryMetric: kind})
	e := objects.Experiment{ID: "exp_f", Owner: "alice", SuiteID: "sui_f"}
	start := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	for ci, cand := range candidates {
		digest := "sha256:" + cand
		e.Candidates = append(e.Candidates, digest)
		e.CandidateIDs = append(e.CandidateIDs, "cnd_"+cand)
		w.v.put(t, objects.KindCandidate, "cnd_"+cand, digest, objects.Candidate{Name: cand, Kind: objects.CandidateExternal, External: &objects.External{Harness: "h", Version: "1", Model: "m-" + cand}})
		for ti, name := range tasks {
			cell := fmt.Sprintf("cel_%s_%s", cand, name)
			w.x.cells["exp_f"] = append(w.x.cells["exp_f"], objects.Cell{ID: cell, Experiment: "exp_f", Task: digests[ti], Candidate: digest,
				Tuple: objects.Tuple{CandidateKind: objects.CandidateExternal, Model: "m-" + cand, TaskHash: digests[ti]}, State: objects.StateComplete, CandidatePos: ci, TaskPos: ti})
			for i, label := range values[cand][name] {
				at := start.Add(time.Duration(i) * time.Minute)
				end := at.Add(30 * time.Second)
				tid := fmt.Sprintf("tri_%s_%d", cell, i)
				w.x.trials["exp_f"] = append(w.x.trials["exp_f"], objects.Trial{ID: tid, Cell: cell, State: objects.StateDone, Outcome: objects.OutcomeCompleted,
					StartedAt: &at, EndedAt: &end, Observed: objects.Observed{CostState: objects.CostUnknown}})
				w.x.ms = append(w.x.ms, objects.Measurement{ID: "msr_" + tid, Experiment: "exp_f", Trial: tid, Metric: kind, Grader: "sha256:kinds", Generation: 1, Value: string(label)})
			}
		}
	}
	slices.SortFunc(w.x.trials["exp_f"], func(a, b objects.Trial) int { return strings.Compare(a.ID, b.ID) })
	w.x.exps["exp_f"] = e
	return w
}

// studyTasks are four tasks of a 2 by 2 design of object and volition.
var studyTasks = []string{"build", "build-want", "do", "do-want"}

var studyFactors = map[string]map[string]string{
	"build":      {"object": "build", "volition": "none"},
	"build-want": {"object": "build", "volition": "want"},
	"do":         {"object": "do", "volition": "none"},
	"do-want":    {"object": "do", "volition": "want"},
}

// A declared comparison of the general form reads its two levels' cells
// over the experiment's design, each named by its stratum, the strata as
// its suite's tasks weighted as their candidate side's tasks: object build
// against do within one candidate, stratified by volition, over the worked
// example's labels gives its p of 752/1,400, which the declared family of
// one corrects to itself.
func TestGeneralInput(t *testing.T) {
	w := factorWorld(t, studyTasks, studyFactors, map[string]map[string]string{
		"c1": {"build": "aaab", "do": "bbba", "build-want": "aab", "do-want": "abb"},
	}, "c1")
	decl := objects.DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": "sha256:c1"}, By: []string{"volition"}}
	e := w.x.exps["exp_f"]
	e.Comparisons = []objects.DeclaredComparison{decl}
	w.x.exps["exp_f"] = e
	zero := 0
	c := objects.Comparison{ID: "cmp_g", Experiment: "exp_f", Candidate: "sha256:c1", DeclaredIndex: &zero, Declaration: &decl, Final: true}
	in, err := Input(t.Context(), c, w.src)
	if err != nil {
		t.Fatal(err)
	}
	want := []analysis.SuiteTask{{Name: "volition=none", Digest: "sha256:task-build"}, {Name: "volition=want", Digest: "sha256:task-build-want"}}
	if !slices.Equal(in.Suite.Tasks, want) || in.Variable != "task" || in.Declared != 1 || in.Exploratory || in.Declaration == nil || in.Declaration.Metric != kind {
		t.Fatalf("the input %+v %s %d %+v", in.Suite.Tasks, in.Variable, in.Declared, in.Declaration)
	}
	if in.Candidate.Cells[0].Task != "volition=none" || in.Candidate.Cells[0].TaskDigest != "sha256:task-build" || in.Reference.Cells[1].TaskDigest != "sha256:task-do-want" {
		t.Fatalf("the sides %+v %+v", in.Candidate.Cells[0], in.Reference.Cells[1])
	}
	if in.Candidate.Candidate != "sha256:c1" || in.Reference.Candidate != "sha256:c1" || in.Candidate.Producers == nil {
		t.Fatalf("the sides' candidates %q %q", in.Candidate.Candidate, in.Reference.Candidate)
	}
	computed, _, err := Analysis{}.Compare(t.Context(), c, w.src)
	if err != nil {
		t.Fatal(err)
	}
	c.Computed, c.State = computed, objects.ComparisonComplete
	at := time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC)
	c.ComputedAt, c.UpdatedAt = &at, at
	a, err := Analysis{}.Declared(t.Context(), e, []objects.Comparison{c}, w.src)
	if err != nil {
		t.Fatal(err)
	}
	got := a.Comparisons[0]
	if got.Metric != kind || got.P == nil || math.Abs(*got.P-752.0/1400) > 1e-12 || *got.Q != *got.P || got.Verdict != "within_noise" || !got.Final {
		t.Fatalf("the declared entry %+v", got)
	}

	// A declaration's own metric is its primary result's, in the general
	// form and on a pair alike.
	decl.Metric = "builtin:completed"
	c.Declaration = &decl
	if in, err := Input(t.Context(), c, w.src); err != nil || in.Suite.PrimaryMetric != "builtin:completed" || in.Declaration.Metric != "builtin:completed" {
		t.Fatalf("a declared metric %v %+v", err, in.Declaration)
	}
	e.Comparisons = []objects.DeclaredComparison{{Candidate: "sha256:c1", Task: "sha256:task-build", ReferenceTask: "sha256:task-do", Metric: "builtin:completed"}}
	w.x.exps["exp_f"] = e
	pair := objects.Comparison{ID: "cmp_p", Experiment: "exp_f", Candidate: "sha256:c1", Task: "sha256:task-build", ReferenceTask: "sha256:task-do", DeclaredIndex: &zero}
	if in, err := Input(t.Context(), pair, w.src); err != nil || in.Suite.PrimaryMetric != "builtin:completed" {
		t.Fatalf("a pair's declared metric %v %s", err, in.Suite.PrimaryMetric)
	}
	if m, level := declaredPrimary(e.Comparisons[0], kind); m != "builtin:completed" || level != "task" {
		t.Fatalf("the primary of a pair of tasks %s %s", m, level)
	}
}

// Without strata a declaration of the general form waits, while it is not
// final, for its two cells, and fails once final without them; one whose
// declaration no longer reads over its experiment, or whose candidate
// cannot be read, is an error.
func TestGeneralInputWaitsAndFails(t *testing.T) {
	w := factorWorld(t, studyTasks, studyFactors, map[string]map[string]string{"c1": {"build": "aaaaa", "do": "bbbbb"}}, "c1")
	decl := objects.DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": "sha256:c1", "volition": "none"}}
	zero := 0
	e := w.x.exps["exp_f"]
	e.Comparisons = []objects.DeclaredComparison{decl}
	w.x.exps["exp_f"] = e
	c := objects.Comparison{ID: "cmp_g", Experiment: "exp_f", Candidate: "sha256:c1", DeclaredIndex: &zero, Declaration: &decl}
	computed, _, err := Analysis{}.Compare(t.Context(), c, w.src)
	if err != nil {
		t.Fatal(err)
	}
	var primary *objects.ResultRow
	for i, r := range computed.Results {
		if r.Metric == kind && r.Level == "task" && r.Task == "build vs do" {
			primary = &computed.Results[i]
		}
	}
	if primary == nil || primary.P == nil || math.Abs(*primary.P-2.0/252) > 1e-12 {
		t.Fatalf("the one pair's primary result %+v", primary)
	}
	// The reference's cell not made yet, as in mode import.
	w.x.cells["exp_f"] = slices.DeleteFunc(w.x.cells["exp_f"], func(cl objects.Cell) bool { return cl.ID == "cel_c1_do" })
	if _, _, err := (Analysis{}).Compare(t.Context(), c, w.src); !errors.Is(err, ErrNotComputed) {
		t.Fatalf("a declaration without its second cell: %v", err)
	}
	c.Final = true
	if _, _, err := (Analysis{}).Compare(t.Context(), c, w.src); err == nil || errors.Is(err, ErrNotComputed) {
		t.Fatalf("a final one without its second cell: %v", err)
	}
	bad := decl
	bad.Levels = []string{"build", "make"}
	c.Declaration = &bad
	if _, err := Input(t.Context(), c, w.src); err == nil {
		t.Fatal("a declaration that no longer reads")
	}
	c.Declaration = &decl
	delete(w.v.byID, "cnd_c1")
	if _, err := Input(t.Context(), c, w.src); err == nil {
		t.Fatal("a candidate that cannot be read")
	}
}

// A declared test of independence over three candidates and two tasks,
// the candidate stratified by task, reproduces the worked example, X² = 7
// with p = 63,720/151,200; before any cell holds a finished trial, and in
// a pending read, it answers its declaration and why it has no figure.
func TestDeclaredIndependence(t *testing.T) {
	tasks := []string{"t1", "t2"}
	w := factorWorld(t, tasks, nil, map[string]map[string]string{
		"c1": {"t1": "aab", "t2": "aa"},
		"c2": {"t1": "abb", "t2": "ab"},
		"c3": {"t1": "bbb", "t2": "bb"},
	}, "c1", "c2", "c3")
	e := w.x.exps["exp_f"]
	e.Independence = &objects.Independence{Metric: kind, Variable: "candidate", By: []string{"task"}}
	w.x.exps["exp_f"] = e
	a, err := Analysis{}.Declared(t.Context(), e, nil, w.src)
	if err != nil {
		t.Fatal(err)
	}
	var res analysis.IndependenceResult
	if err := json.Unmarshal(a.Independence, &res); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Levels, e.Candidates) || res.Variable != "candidate" || res.Method != "stratified_permutation" || res.Exact == nil || !*res.Exact ||
		*res.Resamples != 151200 || math.Abs(*res.P-63720.0/151200) > 1e-12 || res.Tasks != 2 || !res.Final || !a.Final {
		t.Fatalf("the test %+v, final %v", res, a.Final)
	}
	pending, err := Pending(t.Context(), e, w.src)
	if err != nil {
		t.Fatal(err)
	}
	var p pendingIndependence
	if err := json.Unmarshal(pending.Independence, &p); err != nil || p.Reason != analysis.ReasonIndependenceNotComputed || !slices.Equal(p.Levels, e.Candidates) || p.MetricName != "kind" || pending.Final {
		t.Fatalf("pending %s %v", pending.Independence, err)
	}
	for i := range w.x.trials["exp_f"] {
		w.x.trials["exp_f"][i].State, w.x.trials["exp_f"][i].Outcome = objects.StateRunning, ""
	}
	a, err = Analysis{}.Declared(t.Context(), e, nil, w.src)
	if err != nil || a.Final || !strings.Contains(string(a.Independence), analysis.ReasonIndependenceNotComputed) {
		t.Fatalf("before any finished trial %s %v", a.Independence, err)
	}
	// A test that no longer reads over its experiment, and a store that
	// fails, are errors.
	e.Independence.Variable = "tone"
	if _, err := (Analysis{}).Declared(t.Context(), e, nil, w.src); err == nil {
		t.Fatal("a test that no longer reads")
	}
	e.Independence.Variable = "candidate"
	w.x.fail = "AllCells"
	if _, err := (Analysis{}).Declared(t.Context(), e, nil, w.src); !errors.Is(err, errBroken) {
		t.Fatalf("a failing read of cells: %v", err)
	}
	if _, err := Pending(t.Context(), e, w.src); !errors.Is(err, errBroken) {
		t.Fatalf("a failing pending read: %v", err)
	}
}
