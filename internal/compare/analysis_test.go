// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// The stores below hold what a test puts in them and answer the reads the
// analyzer makes; any other method is the embedded nil interface's.

type versions struct {
	store.Versions
	byID     map[string]objects.Version
	byDigest map[string]objects.Version // kind + " " + digest
}

func (v *versions) ByID(_ context.Context, kind, id string) (objects.Version, error) {
	if ver, ok := v.byID[id]; ok && ver.Kind == kind {
		return ver, nil
	}
	return objects.Version{}, store.ErrNotFound
}

func (v *versions) ByDigest(_ context.Context, kind, _ string, digest string) (objects.Version, error) {
	if ver, ok := v.byDigest[kind+" "+digest]; ok {
		return ver, nil
	}
	return objects.Version{}, store.ErrNotFound
}

func (v *versions) put(t *testing.T, kind, id, digest string, body any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ver := objects.Version{ID: id, Kind: kind, Owner: "alice", Name: id, Digest: digest, Body: b}
	v.byID[id], v.byDigest[kind+" "+digest] = ver, ver
}

type experiments struct {
	store.Experiments
	exps   map[string]objects.Experiment
	cells  map[string][]objects.Cell
	trials map[string][]objects.Trial // by experiment, ids ascending
	ms     []objects.Measurement
	fail   string // the method that fails
	// pairs are the calibration pairs of a judge on a metric, by
	// owner, grader and metric.
	pairs map[[3]string][]store.CalibrationPair
}

var errBroken = errors.New("the store broke")

func (x *experiments) Get(_ context.Context, id string) (objects.Experiment, error) {
	if x.fail == "Get" {
		return objects.Experiment{}, errBroken
	}
	if e, ok := x.exps[id]; ok {
		return e, nil
	}
	return objects.Experiment{}, store.ErrNotFound
}

func (x *experiments) AllCells(_ context.Context, id string) ([]objects.Cell, error) {
	if x.fail == "AllCells" {
		return nil, errBroken
	}
	return x.cells[id], nil
}

// Trials pages as the store does: newest first by id, Limit+1 read.
func (x *experiments) Trials(_ context.Context, id string, f store.TrialFilter, p store.Page) ([]objects.Trial, error) {
	if x.fail == "Trials" {
		return nil, errBroken
	}
	var out []objects.Trial
	all := x.trials[id]
	for _, t := range slices.Backward(all) {
		if (p.After == "" || t.ID < p.After) && slices.Contains(f.Cells, t.Cell) {
			out = append(out, t)
		}
		if len(out) == p.Limit+1 {
			break
		}
	}
	return out, nil
}

func (x *experiments) MeasurementsOf(_ context.Context, trials []string) ([]objects.Measurement, error) {
	if x.fail == "MeasurementsOf" {
		return nil, errBroken
	}
	var out []objects.Measurement
	for _, m := range x.ms {
		if slices.Contains(trials, m.Trial) {
			out = append(out, m)
		}
	}
	return out, nil
}

type baselines struct {
	store.Baselines
	b map[string]objects.Baseline
}

func (b *baselines) ByID(_ context.Context, id string) (objects.Baseline, error) {
	if r, ok := b.b[id]; ok {
		return r, nil
	}
	return objects.Baseline{}, store.ErrNotFound
}

type taxonomyStore struct {
	store.Taxonomies
	t map[string]objects.Taxonomy
}

func (tx *taxonomyStore) Current(_ context.Context, _ objects.SuiteName, metric string) (objects.Taxonomy, error) {
	if r, ok := tx.t[metric]; ok {
		return r, nil
	}
	return objects.Taxonomy{}, store.ErrNotFound
}

// world is two experiments of one suite: the current one, whose candidate
// is compared, and an earlier one a baseline pins, whose suite version has
// a check grader produce the score where the current one has none.
type world struct {
	src Source
	x   *experiments
	v   *versions
}

const (
	score   = "sha256:score"
	topic   = "sha256:topic"
	taskA   = "sha256:task-a"
	taskB   = "sha256:task-b"
	check   = "sha256:check"
	candNew = "sha256:cand-new"
	candOld = "sha256:cand-old"
)

func newWorld(t *testing.T) *world {
	t.Helper()
	v := &versions{byID: map[string]objects.Version{}, byDigest: map[string]objects.Version{}}
	v.put(t, objects.KindMetric, "met_score", score, objects.Metric{Name: "quality", Type: objects.TypeScore, Range: []float64{0, 1}, Direction: objects.HigherIsBetter, Aggregate: objects.AggregateMean})
	v.put(t, objects.KindMetric, "met_topic", topic, objects.Metric{Name: "topic", Type: objects.TypeLabel, Vocabulary: objects.VocabularyOpen, Direction: objects.DirectionNone, Aggregate: objects.AggregateDistribution})
	v.put(t, objects.KindTask, "tsk_a", taskA, objects.Task{Name: "todo-app"})
	v.put(t, objects.KindTask, "tsk_a_checked", taskA+"-checked", objects.Task{Name: "todo-app", Checks: []string{check}})
	v.put(t, objects.KindTask, "tsk_b", taskB, objects.Task{Name: "calculator"})
	v.put(t, objects.KindGrader, "grd_check", check, objects.Grader{Name: "tests", Kind: objects.GraderCheck, Produces: []string{score}})
	metrics := []string{score, topic, "builtin:completed"}
	v.put(t, objects.KindSuite, "sui_new", "sha256:suite-new", objects.Suite{Name: "s", Tasks: []string{taskA, taskB}, Metrics: metrics, PrimaryMetric: score, Weights: map[string]float64{taskB: 0}})
	v.put(t, objects.KindSuite, "sui_old", "sha256:suite-old", objects.Suite{Name: "s", Tasks: []string{taskA + "-checked"}, Metrics: metrics, PrimaryMetric: score})

	x := &experiments{exps: map[string]objects.Experiment{
		"exp_new": {ID: "exp_new", Owner: "alice", SuiteID: "sui_new"},
		"exp_old": {ID: "exp_old", Owner: "alice", SuiteID: "sui_old"},
	}, cells: map[string][]objects.Cell{}, trials: map[string][]objects.Trial{}}
	tuple := func(task, model string) objects.Tuple {
		return objects.Tuple{CandidateKind: objects.CandidateExternal, Program: jsontext.Value(`{"harness":"h"}`), Model: model, Tools: []string{"write", "read"}, TaskHash: task}
	}
	x.cells["exp_new"] = []objects.Cell{
		{ID: "cel_a", Experiment: "exp_new", Task: taskA, Candidate: candNew, Tuple: tuple(taskA, "m-2"), State: objects.StateComplete},
		{ID: "cel_b", Experiment: "exp_new", Task: taskB, Candidate: candNew, Tuple: tuple(taskB, "m-2"), State: objects.StateRunning},
		{ID: "cel_other", Experiment: "exp_new", Task: taskA, Candidate: candOld, Tuple: tuple(taskA, "m-1"), State: objects.StateComplete},
	}
	x.cells["exp_old"] = []objects.Cell{{ID: "cel_old", Experiment: "exp_old", Task: taskA + "-checked", Candidate: candOld, Tuple: tuple(taskA, "m-1"), State: objects.StateComplete}}
	start := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	add := func(exp, cell string, n int, grader string, self bool, value func(i int) float64) {
		for i := range n {
			at, end := start.Add(time.Duration(i)*time.Minute), start.Add(time.Duration(i)*time.Minute+30*time.Second)
			id := fmt.Sprintf("tri_%s_%04d", cell, i)
			x.trials[exp] = append(x.trials[exp], objects.Trial{ID: id, Cell: cell, State: objects.StateDone, Outcome: objects.OutcomeCompleted,
				StartedAt: &at, EndedAt: &end, Observed: objects.Observed{CostState: objects.CostUnknown}})
			x.ms = append(x.ms, objects.Measurement{ID: "msr_" + id, Trial: id, Metric: score, Grader: grader, SelfReported: self, Generation: 1, Value: value(i)})
		}
		slices.SortFunc(x.trials[exp], func(a, b objects.Trial) int { return strings.Compare(a.ID, b.ID) })
	}
	add("exp_new", "cel_a", 1001, "self:h", true, func(i int) float64 { return float64(i%10) / 10 })
	add("exp_new", "cel_b", 2, "self:h", true, func(int) float64 { return 0.5 })
	add("exp_new", "cel_other", 5, "self:h", true, func(int) float64 { return 0.2 })
	add("exp_old", "cel_old", 5, check, false, func(int) float64 { return 0.3 })
	// An annotation is the queue's, and no comparison reads it.
	x.ms = append(x.ms, objects.Measurement{ID: "msr_annotation", Trial: "tri_cel_old_0000", Metric: score, Grader: check, Generation: 2, Value: 1.0, Annotator: "bob"})
	w := &world{x: x, v: v}
	w.src = Source{Experiments: x, Versions: v, Baselines: &baselines{b: map[string]objects.Baseline{
		"bas_1": {ID: "bas_1", Experiment: "exp_old", Candidate: candOld},
	}}, Taxonomies: &taxonomyStore{t: map[string]objects.Taxonomy{
		topic: {ID: "tax_1", Digest: "sha256:tax", Canonical: []string{"life"}, Map: map[string]string{"game of life": "life"}},
	}}}
	return w
}

// A comparison against a baseline reads the baseline's candidate's cells
// in the baseline's experiment at that experiment's own producing graders,
// every trial of a side across pages, the suite's weights and current
// taxonomy record, and no annotation.
func TestInputAgainstABaseline(t *testing.T) {
	w := newWorld(t)
	in, err := Input(t.Context(), objects.Comparison{ID: "cmp_1", Experiment: "exp_new", Candidate: candNew, Baseline: "bas_1", Variable: []string{"model"}}, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if in.Variable != "model" || in.AcrossTasks || in.Exploratory || in.Parameters != analysis.Defaults() {
		t.Fatalf("the request %+v", in)
	}
	if len(in.Suite.Tasks) != 2 || in.Suite.Tasks[1] != (analysis.SuiteTask{Name: "calculator", Digest: taskB}) || in.Suite.Weights[taskB] != 0 || in.Suite.PrimaryMetric != score {
		t.Fatalf("the suite %+v", in.Suite)
	}
	if m := in.Suite.Metrics[2]; m.Digest != "builtin:completed" || m.Type != analysis.TypeBinary || m.Aggregate != analysis.AggRate || m.Direction != analysis.NoDirection {
		t.Fatalf("a built-in metric %+v", m)
	}
	if tax := in.Taxonomies[topic]; tax.ID != "tax_1" || tax.Map["game of life"] != "life" {
		t.Fatalf("taxonomies %+v", in.Taxonomies)
	}
	if len(in.Candidate.Cells) != 2 || in.Candidate.Candidate != candNew {
		t.Fatalf("the candidate side %+v", in.Candidate.Candidate)
	}
	a := in.Candidate.Cells[0]
	if a.Task != "todo-app" || len(a.Trials) != 1001 || !a.Final || in.Candidate.Cells[1].Final {
		t.Fatalf("the candidate's cells: %s with %d trials, final %v", a.Task, len(a.Trials), a.Final)
	}
	if _, has := a.Producers[score]; has || a.Producers["builtin:completed"] != objects.GraderBuiltin {
		t.Fatalf("the candidate reads its score self-reported: %v", a.Producers)
	}
	if !slices.Equal(a.Tuple.Tools, []string{"read", "write"}) || a.Tuple.Program != `{"harness":"h"}` {
		t.Fatalf("the tuple %+v", a.Tuple)
	}
	ref := in.Reference
	if ref.Candidate != candOld || len(ref.Cells) != 1 || ref.Cells[0].Task != "todo-app" || ref.Cells[0].Producers[score] != check {
		t.Fatalf("the baseline side %+v", ref)
	}
	for _, tr := range ref.Cells[0].Trials {
		for _, m := range tr.Measurements {
			if m.ID == "msr_annotation" {
				t.Fatal("an annotation was read")
			}
		}
	}
	read, err := analysis.MeasurementsRead(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 1001+2+5 {
		t.Fatalf("read %d measurements", len(read))
	}
}

// A comparison of another candidate reads its cells of the same experiment;
// across tasks, the candidate's two cells; recorded parameters are read
// back; and each read the analyzer cannot make is its error.
func TestInputSides(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	in, err := Input(ctx, objects.Comparison{ID: "cmp_2", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, Exploratory: true}, w.src)
	if err != nil || len(in.Reference.Cells) != 1 || len(in.Reference.Cells[0].Trials) != 5 || !in.Exploratory {
		t.Fatalf("another candidate %v %+v", err, in.Reference)
	}
	across, err := Input(ctx, objects.Comparison{ID: "cmp_3", Experiment: "exp_new", Candidate: candNew, Task: taskB, ReferenceTask: taskA, Variable: objects.Variables["task"]}, w.src)
	if err != nil || !across.AcrossTasks || across.Variable != "" || across.Candidate.Cells[0].Task != "calculator" || across.Reference.Cells[0].Task != "todo-app" {
		t.Fatalf("across tasks %v %+v", err, across)
	}
	params := analysis.Defaults()
	params.ConfidenceLevel = 0.9
	raw, err := json.Marshal(map[string]any{"confidence_level": 0.9, "false_discovery_rate": 0.05, "min_trials_for_verdict": 5, "min_trials_per_task_in_suite": 2,
		"min_suite_coverage": 0.8, "min_trials_for_p90_interval": 29, "bootstrap_resamples": 10000, "permutation_exact_max": 200000,
		"permutation_stop_exceedances": 200, "boschloo_grid_points": 1000, "tie_tolerance": 1e-9, "window_tolerance": "168h0m0s",
		"permutation_resamples": map[string]int{"primary": 10000, "family": 10000}})
	if err != nil {
		t.Fatal(err)
	}
	withParams := objects.Comparison{ID: "cmp_4", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld,
		Parameters: raw}
	recorded, err := Input(ctx, withParams, w.src)
	if err != nil || recorded.Parameters != params {
		t.Fatalf("recorded parameters %v %+v", err, recorded.Parameters)
	}
	for name, c := range map[string]objects.Comparison{
		"no experiment":         {ID: "c", Experiment: "exp_gone", Candidate: candNew, ReferenceCandidate: candOld},
		"no reference":          {ID: "c", Experiment: "exp_new", Candidate: candNew},
		"no baseline":           {ID: "c", Experiment: "exp_new", Candidate: candNew, Baseline: "bas_gone"},
		"a variable of no name": {ID: "c", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, Variable: []string{"grader_digests"}},
		"no cell of the task":   {ID: "c", Experiment: "exp_new", Candidate: candOld, Task: taskB, ReferenceTask: taskA},
	} {
		if _, err := Input(ctx, c, w.src); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	unreadable := objects.Comparison{ID: "c", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld,
		Parameters: jsontext.Value(`[]`)}
	if _, err := Input(ctx, unreadable, w.src); err == nil {
		t.Error("unreadable parameters: no error")
	}
	for _, method := range []string{"AllCells", "Trials", "MeasurementsOf"} {
		w.x.fail = method
		if _, err := Input(ctx, objects.Comparison{ID: "c", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld}, w.src); !errors.Is(err, errBroken) {
			t.Errorf("%s failing: %v", method, err)
		}
	}
	w.x.fail = ""
	// A suite whose pins no longer resolve is an error of the store.
	delete(w.v.byDigest, objects.KindTask+" "+taskB)
	if _, err := Input(ctx, objects.Comparison{ID: "c", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld}, w.src); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a missing task: %v", err)
	}
}

// The analyzer computes a comparison and records it whole, waits on an
// automatic comparison until its candidate has a finished trial, and
// summarizes cells by the stored analysis's procedures.
func TestAnalysis(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	var a Analyzer = Analysis{}
	computed, read, err := a.Compare(ctx, objects.Comparison{ID: "cmp_1", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, Automatic: true}, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 1001+2+5 || computed.InputsDigest == "" || computed.AnalysisVersion != analysis.Version || computed.SingleVariable == nil || !*computed.SingleVariable {
		t.Fatalf("computed %+v, read %d", computed, len(read))
	}
	if !slices.Equal(computed.VariableFields, []string{"model"}) || len(computed.Declared) != 2 {
		t.Fatalf("variable %v declared %v", computed.VariableFields, computed.Declared)
	}
	// Nothing finished on the candidate's side: an automatic comparison
	// waits, a final one and one on request are computed.
	for i := range w.x.trials["exp_new"] {
		if w.x.trials["exp_new"][i].Cell != "cel_other" {
			w.x.trials["exp_new"][i].State, w.x.trials["exp_new"][i].Outcome = objects.StateRunning, ""
		}
	}
	if _, _, err := a.Compare(ctx, objects.Comparison{ID: "cmp_1", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, Automatic: true}, w.src); !errors.Is(err, ErrNotComputed) {
		t.Fatalf("an automatic comparison before any finished trial: %v", err)
	}
	for _, c := range []objects.Comparison{
		{ID: "cmp_1", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, Automatic: true, Final: true},
		{ID: "cmp_2", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, Exploratory: true},
	} {
		if got, _, err := a.Compare(ctx, c, w.src); err != nil || len(got.Results) == 0 {
			t.Fatalf("%s: %v", c.ID, err)
		}
	}
	if _, _, err := a.Compare(ctx, objects.Comparison{ID: "c", Experiment: "exp_gone"}, w.src); err == nil {
		t.Fatal("a comparison of no experiment was computed")
	}
	e := w.x.exps["exp_new"]
	sums, err := a.Summaries(ctx, e, w.x.cells["exp_new"], "builtin:completed", w.src)
	if err != nil {
		t.Fatal(err)
	}
	var s analysis.Summary
	if err := json.Unmarshal(sums["cel_other"], &s); err != nil || s.N != 5 || s.Aggregate != analysis.AggRate {
		t.Fatalf("summary %v %s", err, sums["cel_other"])
	}
	if got, err := a.Summaries(ctx, e, nil, score, w.src); err != nil || len(got) != 0 {
		t.Fatalf("no cells %v %v", got, err)
	}
	w.x.fail = "Trials"
	if _, err := a.Summaries(ctx, e, w.x.cells["exp_new"], score, w.src); !errors.Is(err, errBroken) {
		t.Fatalf("a failing read %v", err)
	}
}

// A comparison's record holds every member the analysis fills: each
// result's summaries and delta as rendered, a suite-level result's
// left_out, an empty confound list as a list, and the parameters with
// PERMUTATION_RESAMPLES as resolved.
func TestRecord(t *testing.T) {
	p, q := 0.01, 0.02
	c := analysis.Comparison{
		Variable: []string{"model"}, SingleVariable: true, DeclaredProvenance: []string{"candidate"},
		Results: []analysis.Result{
			{Level: analysis.LevelSuite, Metric: score, MetricName: "quality", Direction: analysis.HigherIsBetter, Candidate: analysis.Summary{N: 3},
				Delta: &analysis.Delta{Value: 0.1}, DeltaInterval: &analysis.DeltaInterval{Interval: analysis.Interval{0.01, 0.2}}, P: &p, Q: &q, Verdict: analysis.Improved, Reason: "r"},
			{Level: analysis.LevelTask, Task: "todo-app", TaskDigests: &analysis.TaskDigests{Candidate: taskA, Reference: taskA}, Metric: score,
				Confounds: []analysis.Confound{{Name: "self_reported", Tasks: []string{"todo-app"}, Candidate: 3}}},
		},
		Taxonomies: map[string]string{topic: "tax_1"}, Parameters: analysis.Defaults(),
		Resamples: analysis.Resolved{Primary: 10000, Family: 16000}, AnalysisVersion: analysis.Version, InputsDigest: "sha256:x", Seed: "ab",
	}
	got, err := Record(c)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.VariableFields, []string{"model"}) || !slices.Equal(got.Declared, []string{"candidate"}) || len(got.Confounds) != 0 || got.Confounds == nil {
		t.Fatalf("members %+v", got)
	}
	suite, task := got.Results[0], got.Results[1]
	if string(suite.LeftOut) != "[]" || string(suite.Confounds) != "[]" || string(suite.Delta) != "0.1" || string(suite.DeltaInterval) != "[0.01,0.2]" || *suite.Q != q || suite.Verdict != "improved" {
		t.Fatalf("the suite-level row %+v", suite)
	}
	if task.LeftOut != nil || !strings.Contains(string(task.Confounds), "self_reported") || !strings.Contains(string(task.TaskDigests), taskA) {
		t.Fatalf("the task-level row %+v", task)
	}
	var params map[string]any
	if err := json.Unmarshal(got.Parameters, &params); err != nil || params["permutation_resamples"].(map[string]any)["family"] != 16000.0 || params["confidence_level"] != 0.95 {
		t.Fatalf("parameters %s", got.Parameters)
	}
	if string(got.Taxonomies) != `{"sha256:topic":"tax_1"}` {
		t.Fatalf("taxonomies %s", got.Taxonomies)
	}
	if empty, err := Record(analysis.Comparison{}); err != nil || empty.Declared == nil || empty.VariableFields == nil || empty.Taxonomies != nil {
		t.Fatalf("an empty record %+v %v", empty, err)
	}
}

// A trial's cost reads its observed layer: priced when known, unpriced and
// unknown kept apart, a missing state unknown and never zero, and the
// costs its APIs reported summed apart.
func TestCostOf(t *testing.T) {
	usd := 0.25
	for _, c := range []struct {
		o    objects.Observed
		want analysis.Cost
	}{
		{objects.Observed{CostState: objects.CostKnown, CostUSD: &usd}, analysis.Cost{USD: 0.25, Status: analysis.CostPriced}},
		{objects.Observed{CostState: objects.CostKnown}, analysis.Cost{Status: analysis.CostPriced}},
		{objects.Observed{CostState: objects.CostUnpriced}, analysis.Cost{Status: analysis.CostUnpriced}},
		{objects.Observed{}, analysis.Cost{Status: analysis.CostUnknown}},
		{objects.Observed{CostState: objects.CostUnknown, ServiceUsage: map[string]objects.ServiceUsage{"a": {CostUSDMicro: 1500000}, "b": {CostUSDMicro: 500000}}},
			analysis.Cost{Status: analysis.CostUnknown, Service: 2}},
	} {
		if got := costOf(c.o); got != c.want {
			t.Errorf("%+v: %+v, want %+v", c.o, got, c.want)
		}
	}
}

// A metric reads as the analysis's: its applies_to as outcomes, an absent
// exclude_labels as nil and an empty one as empty, and a label metric that
// names no vocabulary as closed.
func TestMetricOf(t *testing.T) {
	none := []string{}
	m := metricOf("sha256:m", objects.Metric{Name: "medium", Type: objects.TypeLabel, Labels: []string{"a"}, Direction: objects.DirectionNone, Aggregate: objects.AggregateDistribution, AppliesTo: []string{"completed"}})
	if m.Vocabulary != analysis.Closed || !slices.Equal(m.AppliesTo, []analysis.Outcome{analysis.OutcomeCompleted}) || m.ExcludeLabels != nil {
		t.Fatalf("%+v", m)
	}
	cell := metricOf("sha256:c", objects.Metric{Name: "fix", Type: objects.TypeScore, Level: objects.LevelCell, Of: "sha256:m", ExcludeLabels: &none, Aggregate: objects.AggregateModeShare})
	if cell.Level != analysis.LevelCell || cell.ExcludeLabels == nil || len(cell.ExcludeLabels) != 0 || cell.Of != "sha256:m" {
		t.Fatalf("%+v", cell)
	}
}

// Each read the analyzer cannot make, of a suite, its pins, a baseline's
// experiment or a taxonomy, is its error, and a computation of an input
// the analysis refuses is the analysis's error.
func TestReadErrors(t *testing.T) {
	ctx := t.Context()
	base := objects.Comparison{ID: "c", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld}
	for name, break_ := range map[string]func(w *world){
		"no suite":           func(w *world) { delete(w.v.byID, "sui_new") },
		"a suite of no JSON": func(w *world) { ver := w.v.byID["sui_new"]; ver.Body = jsontext.Value(`[`); w.v.byID["sui_new"] = ver },
		"no metric":          func(w *world) { delete(w.v.byDigest, objects.KindMetric+" "+topic) },
		"a metric of no JSON": func(w *world) {
			ver := w.v.byDigest[objects.KindMetric+" "+topic]
			ver.Body = jsontext.Value(`[`)
			w.v.byDigest[objects.KindMetric+" "+topic] = ver
		},
		"no suite grader": func(w *world) {
			w.v.put(t, objects.KindSuite, "sui_new", "sha256:suite-new", objects.Suite{Name: "s", Tasks: []string{taskA}, Metrics: []string{score}, PrimaryMetric: score, Graders: []string{"sha256:gone"}})
		},
		"no decline judge": func(w *world) {
			w.v.put(t, objects.KindSuite, "sui_new", "sha256:suite-new", objects.Suite{Name: "s", Tasks: []string{taskA}, Metrics: []string{score}, PrimaryMetric: score, DeclineJudge: "sha256:gone"})
		},
		"a cell of no task of the suite": func(w *world) {
			w.v.put(t, objects.KindSuite, "sui_new", "sha256:suite-new", objects.Suite{Name: "s", Tasks: []string{taskA}, Metrics: []string{score}, PrimaryMetric: score})
		},
		"a taxonomy that does not answer":    func(w *world) { w.src.Taxonomies = failingTaxonomies{} },
		"an experiment that does not answer": func(w *world) { w.x.fail = "Get" },
	} {
		w := newWorld(t)
		break_(w)
		if _, err := Input(ctx, base, w.src); err == nil {
			t.Errorf("%s: no error", name)
		}
		if _, err := (Analysis{}).Summaries(ctx, w.x.exps["exp_new"], w.x.cells["exp_new"], score, w.src); err == nil && name != "an experiment that does not answer" {
			t.Errorf("%s: summaries without an error", name)
		}
	}
	// The baseline's side: its experiment and its suite.
	w := newWorld(t)
	w.src.Baselines.(*baselines).b["bas_gone"] = objects.Baseline{ID: "bas_gone", Experiment: "exp_gone", Candidate: candOld}
	pinned := base
	pinned.ReferenceCandidate, pinned.Baseline = "", "bas_gone"
	if _, err := Input(ctx, pinned, w.src); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a baseline of no experiment: %v", err)
	}
	delete(w.v.byID, "sui_old")
	pinned.Baseline = "bas_1"
	if _, err := Input(ctx, pinned, w.src); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a baseline of no suite: %v", err)
	}
	// A metric no suite could be applied with is refused by the analysis.
	w = newWorld(t)
	w.v.put(t, objects.KindMetric, "met_score", score, objects.Metric{Name: "quality", Type: objects.TypeBinary, Direction: objects.HigherIsBetter, Aggregate: objects.AggregateMean})
	if _, _, err := (Analysis{}).Compare(ctx, base, w.src); !errors.Is(err, analysis.ErrInput) {
		t.Errorf("an aggregate that does not fit its type: %v", err)
	}
	if _, err := (Analysis{}).Summaries(ctx, w.x.exps["exp_new"], w.x.cells["exp_new"], score, w.src); !errors.Is(err, analysis.ErrInput) {
		t.Errorf("summaries of an aggregate that does not fit its type: %v", err)
	}
}

type failingTaxonomies struct{ store.Taxonomies }

func (failingTaxonomies) Current(context.Context, objects.SuiteName, string) (objects.Taxonomy, error) {
	return objects.Taxonomy{}, errBroken
}

// A candidate-level metric has no cell summary; a trial's declared image
// and an absent tool set read as the analysis reads them.
func TestCellReads(t *testing.T) {
	w := newWorld(t)
	w.v.put(t, objects.KindMetric, "met_rank", "sha256:rank", objects.Metric{Name: "rank", Type: objects.TypeScore, Range: []float64{0, 1}, Direction: objects.HigherIsBetter, Level: objects.LevelCandidate})
	w.v.put(t, objects.KindSuite, "sui_new", "sha256:suite-new", objects.Suite{Name: "s", Tasks: []string{taskA, taskB}, Metrics: []string{score, "sha256:rank"}, PrimaryMetric: score})
	sums, err := (Analysis{}).Summaries(t.Context(), w.x.exps["exp_new"], w.x.cells["exp_new"], "sha256:rank", w.src)
	if err != nil || len(sums) != 0 {
		t.Fatalf("a candidate-level metric's cell summaries %v %v", sums, err)
	}
	if tu := tupleOf(objects.Tuple{}); tu.Tools == nil || len(tu.Tools) != 0 {
		t.Fatalf("an absent tool set %+v", tu)
	}
	at := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	tr := trialOf(objects.Trial{ID: "t", State: objects.StateRunning, Outcome: "", StartedAt: &at, Declared: &objects.Declared{Image: "img:1"}}, nil)
	if tr.DeclaredImage != "img:1" || tr.Outcome != "" || !tr.EndedAt.IsZero() {
		t.Fatalf("a running trial %+v", tr)
	}
}

// A task's own graders produce its trials' values as the suite's do, so a
// comparison reads a model judge a task names beside its checks.
func TestATaskGraderProducesItsValues(t *testing.T) {
	v := &versions{byID: map[string]objects.Version{}, byDigest: map[string]objects.Version{}}
	v.put(t, objects.KindMetric, "met_topic", topic, objects.Metric{Name: "topic", Type: objects.TypeLabel, Vocabulary: objects.VocabularyOpen, Direction: objects.DirectionNone, Aggregate: objects.AggregateDistribution})
	v.put(t, objects.KindMetric, "met_score", score, objects.Metric{Name: "quality", Type: objects.TypeScore, Range: []float64{0, 1}, Direction: objects.HigherIsBetter, Aggregate: objects.AggregateMean})
	v.put(t, objects.KindGrader, "grd_check", check, objects.Grader{Name: "tests", Kind: objects.GraderCheck, Produces: []string{score}})
	v.put(t, objects.KindGrader, "grd_judge", "sha256:judge", objects.Grader{Name: "artifact", Kind: objects.GraderModelJudge, Produces: []string{topic},
		ModelJudge: &objects.ModelJudge{Model: "m", Prompt: "Name what the run made."}})
	v.put(t, objects.KindTask, "tsk_a", taskA, objects.Task{Name: "todo-app", Checks: []string{check}, Graders: []string{"sha256:judge"}})
	v.put(t, objects.KindSuite, "sui", "sha256:suite", objects.Suite{Name: "s", Tasks: []string{taskA}, Metrics: []string{score, topic}, PrimaryMetric: topic})
	su, err := readSuite(context.Background(), v, objects.Experiment{ID: "exp_1", SuiteID: "sui"})
	if err != nil {
		t.Fatal(err)
	}
	if got := su.producers[taskA]; got[topic] != "sha256:judge" || got[score] != check {
		t.Fatalf("the task's producers are %v", got)
	}
}
