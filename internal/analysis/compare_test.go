// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func mustCompare(t *testing.T, in Input) Comparison {
	t.Helper()
	c, err := Compare(in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestVerdictRule(t *testing.T) {
	up, down, zero := &Delta{Value: 0.2}, &Delta{Value: -0.2}, &Delta{Value: 0}
	for _, c := range []struct {
		d     Direction
		q     float64
		delta *Delta
		want  Verdict
	}{
		{HigherIsBetter, 0.01, up, Improved},
		{HigherIsBetter, 0.01, down, Regressed},
		{LowerIsBetter, 0.01, down, Improved},
		{LowerIsBetter, 0.01, up, Regressed},
		{NoDirection, 0.01, up, Changed},
		{NoDirection, 0.01, down, Changed},
		{HigherIsBetter, 0.06, up, WithinNoise},
		{NoDirection, 0.06, up, WithinNoise},
		{HigherIsBetter, 0.01, zero, WithinNoise},
		{NoDirection, 0.01, zero, WithinNoise},
		{NoDirection, 0.01, &Delta{PerLabel: map[string]float64{"a": 0.1, "b": -0.1}}, Changed},
		{NoDirection, 0.01, &Delta{PerLabel: map[string]float64{"a": 0}}, WithinNoise},
		{HigherIsBetter, 0.01, nil, WithinNoise},
	} {
		if got := verdict(c.d, c.q, 0.05, c.delta); got != c.want {
			t.Errorf("%s, q = %v, delta %+v: %s, want %s", c.d, c.q, c.delta, got, c.want)
		}
	}
	// The verdict follows q and the sign alone: a significant difference
	// whose interval reaches zero is still improved. 9/10 against 3/10 is
	// significant by Boschloo's test.
	m := binaryMetric("pass", HigherIsBetter)
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": bools(9, 10)}, map[string][]any{"t1": bools(3, 10)}))
	r := find(c, m.Digest, LevelTask, "t1")
	if r.Verdict != Improved || *r.Q > 0.05 {
		t.Errorf("9/10 against 3/10: %s q %v", r.Verdict, *r.Q)
	}
}

func TestInsufficientBelowMinimum(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	min := Defaults().MinTrialsForVerdict
	in := pairInput(m, map[string][]any{"t1": bools(min-1, min-1), "t2": bools(6, 10)}, map[string][]any{"t1": bools(0, 10), "t2": bools(1, 10)})
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if r.Verdict != Insufficient || r.P != nil || r.Q != nil || r.DeltaInterval != nil || r.Method != "" {
		t.Errorf("t1: %+v", r)
	}
	if r.Reason != fmt.Sprintf("task t1 has %d trials with a value; %d needed", min-1, min) {
		t.Errorf("reason %q", r.Reason)
	}
	if c.Correction.Family != 1 {
		t.Errorf("family %d, want 1: an insufficient result is in no family", c.Correction.Family)
	}
	// A reference without values of the metric is insufficient too.
	in = pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": make([]any, 10)})
	r = find(mustCompare(t, in), m.Digest, LevelTask, "t1")
	if r.Verdict != Insufficient || !strings.HasPrefix(r.Reason, "task t1 has 0 trials with a value; 5 needed") {
		t.Errorf("no reference values: %s %q", r.Verdict, r.Reason)
	}
	if reasonTaskBelow("t3", 1, 5) != "task t3 has 1 trial with a value; 5 needed" {
		t.Error(reasonTaskBelow("t3", 1, 5))
	}
}

// A suite-level result includes exactly the tasks with at least
// MIN_TRIALS_PER_TASK_IN_SUITE valued trials per side, is insufficient only
// below MIN_SUITE_COVERAGE, and lists every task left out with why.
func TestCoverage(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	cand := map[string][]any{"t1": bools(5, 6), "t2": bools(5, 6), "t3": bools(5, 6), "t4": bools(5, 6), "t5": bools(5, 6)}
	ref := map[string][]any{"t1": bools(1, 6), "t2": bools(1, 6), "t3": bools(1, 6), "t4": bools(1, 6), "t5": bools(1, 1)}
	c := mustCompare(t, pairInput(m, cand, ref))
	s := find(c, m.Digest, LevelSuite, "")
	if s.Verdict == Insufficient || *s.Candidate.Coverage != 0.8 || *s.Candidate.Tasks != 4 {
		t.Errorf("80%%: %s coverage %v tasks %v", s.Verdict, *s.Candidate.Coverage, *s.Candidate.Tasks)
	}
	if len(s.LeftOut) != 1 || s.LeftOut[0] != (LeftOut{Task: "t5", Why: LeftOutTooFewTrials, Candidate: 6, Reference: 1}) {
		t.Errorf("left out %+v", s.LeftOut)
	}
	// One task fewer on the reference: 60%, insufficient, with the reason.
	delete(ref, "t4")
	c = mustCompare(t, pairInput(m, cand, ref))
	s = find(c, m.Digest, LevelSuite, "")
	if s.Verdict != Insufficient || s.P != nil {
		t.Errorf("60%%: %s", s.Verdict)
	}
	want := "covers 60% of the suite's weight; 80% needed; left out: t4 (not run by the reference), t5 (1 trial)"
	if s.Reason != want {
		t.Errorf("reason %q, want %q", s.Reason, want)
	}
	if len(c.UnpairedTasks.CandidateOnly) != 1 || c.UnpairedTasks.CandidateOnly[0] != "t4" {
		t.Errorf("unpaired %+v", c.UnpairedTasks)
	}
	// A suite task the candidate never ran is left out as unpaired too.
	in := pairInput(m, cand, ref)
	in.Suite.Tasks = []SuiteTask{{"t1", "sha256:t1"}, {"t2", "sha256:t2"}, {"t3", "sha256:t3"}, {"t4", "sha256:t4"}, {"t5", "sha256:t5"}, {"t6", "sha256:t6"}}
	s = find(mustCompare(t, in), m.Digest, LevelSuite, "")
	if !strings.Contains(s.Reason, "t6 (not run by the candidate)") {
		t.Errorf("reason %q", s.Reason)
	}
}

// A suite-level summary is the weighted mean of the per-task summaries over
// its included tasks; a weight-0 task is compared per task and absent from
// every suite-level figure.
func TestSuiteWeights(t *testing.T) {
	m := numericMetric("score", TypeScore, AggMean, HigherIsBetter)
	cand := map[string][]any{"t1": nums(1, 2, 3, 4, 5), "t2": nums(5, 6, 7, 8, 9), "t3": nums(100, 100, 100, 100, 100)}
	ref := map[string][]any{"t1": nums(1, 2, 3, 4, 5), "t2": nums(1, 2, 3, 4, 5), "t3": nums(0, 0, 0, 0, 0)}
	in := pairInput(m, cand, ref)
	in.Suite.Weights = map[string]float64{"sha256:t1": 2, "sha256:t3": 0}
	c := mustCompare(t, in)
	s := find(c, m.Digest, LevelSuite, "")
	if v, _ := s.Candidate.Number(); v != (2*3.0+1*7.0)/3 {
		t.Errorf("S = %v, want (2·3 + 7)/3", v)
	}
	if *s.Candidate.Tasks != 2 || *s.Candidate.Coverage != 1 || len(s.LeftOut) != 0 {
		t.Errorf("tasks %v coverage %v left out %v", *s.Candidate.Tasks, *s.Candidate.Coverage, s.LeftOut)
	}
	if r := find(c, m.Digest, LevelTask, "t3"); r == nil || r.Verdict == "" {
		t.Error("the weight-0 task has no task-level result")
	}
	if v, _ := c.CandidateSuite[m.Digest].Number(); v != (2*3.0+1*7.0)/3 {
		t.Errorf("candidate suite %v", v)
	}
}

func TestDirectionNone(t *testing.T) {
	m := binaryMetric("completed", NoDirection)
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": bools(10, 10)}, map[string][]any{"t1": bools(0, 10)}))
	for _, r := range c.Results {
		if r.Verdict == Improved || r.Verdict == Regressed {
			t.Errorf("%s %s: %s", r.Level, r.Task, r.Verdict)
		}
	}
	if r := find(c, m.Digest, LevelTask, "t1"); r.Verdict != Changed {
		t.Errorf("10/10 against 0/10: %s", r.Verdict)
	}
}

// Equal data on both sides give within_noise for every method.
func TestEqualSidesAreWithinNoise(t *testing.T) {
	label := labelMetric("topic", "a", "b", "c")
	for _, c := range []struct {
		m      Metric
		values []any
	}{
		{binaryMetric("pass", HigherIsBetter), bools(4, 7)},
		{numericMetric("cost", TypeCost, AggMean, LowerIsBetter), nums(1, 2, 3, 5, 8, 13, 21)},
		{numericMetric("time", TypeDuration, AggMedian, LowerIsBetter), nums(1, 2, 3, 5, 8, 13, 21)},
		{numericMetric("tail", TypeDuration, AggP90, LowerIsBetter), nums(1, 2, 3, 5, 8, 13, 21)},
		{label, strs("a", "a", "b", "c", "a", "b", "c")},
	} {
		in := pairInput(c.m, map[string][]any{"t1": c.values, "t2": c.values}, map[string][]any{"t1": c.values, "t2": c.values})
		cmp := mustCompare(t, in)
		for _, r := range cmp.Results {
			if r.Verdict != WithinNoise {
				t.Errorf("%s %s %s: %s (%s)", c.m.Name, r.Level, r.Task, r.Verdict, r.Reason)
			}
		}
	}
	rr := cellMetric("agreement", AggRepeatRate, label)
	in := pairInput(label, map[string][]any{"t1": strs("a", "a", "b", "c", "a", "b")}, map[string][]any{"t1": strs("a", "a", "b", "c", "a", "b")})
	in.Suite.Metrics = append(in.Suite.Metrics, rr)
	for _, r := range mustCompare(t, in).Results {
		if r.Verdict != WithinNoise {
			t.Errorf("%s %s: %s", r.MetricName, r.Level, r.Verdict)
		}
	}
}

// A count metric is tested by the permutation of its declared aggregate's
// statistic.
func TestCountsUseTheirAggregate(t *testing.T) {
	for agg, stat := range map[Aggregate]string{AggMean: statWelchT, AggMedian: statStudentizedQ, AggP90: statStudentizedQ} {
		m := numericMetric("turns", TypeCount, agg, NoDirection)
		c := mustCompare(t, pairInput(m, map[string][]any{"t1": nums(3, 4, 5, 6, 30), "t2": nums(1, 2, 3, 4, 5)}, map[string][]any{"t1": nums(2, 3, 4, 5, 6), "t2": nums(1, 2, 3, 4, 6)}))
		r := find(c, m.Digest, LevelTask, "t1")
		if r.Method != methodPerm || r.Statistic != stat {
			t.Errorf("%s: %s %s, want permutation %s", agg, r.Method, r.Statistic, stat)
		}
		if s := find(c, m.Digest, LevelSuite, ""); s.Method != methodStratified || s.Statistic != stat {
			t.Errorf("%s suite: %s %s", agg, s.Method, s.Statistic)
		}
	}
}

func TestPrimaryStandsAlone(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": bools(8, 10), "t2": bools(7, 10)}, map[string][]any{"t1": bools(2, 10), "t2": bools(3, 10)}))
	s := find(c, m.Digest, LevelSuite, "")
	if s.Q == nil || *s.Q != *s.P {
		t.Errorf("primary q %v p %v", s.Q, s.P)
	}
	if c.Correction.Primary != m.Digest || c.Correction.Family != 2 || c.Correction.Method != "benjamini_hochberg" {
		t.Errorf("correction %+v", c.Correction)
	}
	if !strings.HasSuffix(s.Reason, "for the primary metric, tested alone without correction") {
		t.Errorf("reason %q", s.Reason)
	}
	if c.Resamples.Primary != 10000 {
		t.Errorf("resamples %+v", c.Resamples)
	}
}

func TestExploratory(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(8, 10), "t2": bools(7, 10)}, map[string][]any{"t1": bools(2, 10), "t2": bools(3, 10)})
	in.Exploratory = true
	c := mustCompare(t, in)
	s := find(c, m.Digest, LevelSuite, "")
	if c.Correction.Primary != "" || c.Correction.Family != 3 || !c.Exploratory {
		t.Errorf("correction %+v", c.Correction)
	}
	if !strings.HasSuffix(s.Reason, "after correcting 3 results at 5%; exploratory, so the primary metric is corrected with the rest") {
		t.Errorf("reason %q", s.Reason)
	}
}

// Adding a candidate to an experiment leaves another candidate's q values
// unchanged: a comparison's family is its own, so computing another
// comparison of the same experiment changes nothing of this one.
func TestAddingACandidateChangesNoOtherVerdict(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	ref := map[string][]any{"t1": bools(2, 10), "t2": bools(3, 10)}
	in := pairInput(m, map[string][]any{"t1": bools(8, 10), "t2": bools(7, 10)}, ref)
	before, err := json.Marshal(mustCompare(t, in))
	if err != nil {
		t.Fatal(err)
	}
	other := pairInput(m, map[string][]any{"t1": bools(1, 10), "t2": bools(10, 10)}, ref)
	other.Candidate.Candidate = "sha256:third"
	mustCompare(t, other)
	after, err := json.Marshal(mustCompare(t, in))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("a comparison changed when another candidate was compared")
	}
}

// selfReportedInput is an external candidate whose metric no grader
// produces, so its values are what its harness submitted.
func selfReportedInput() (Input, Metric) {
	m := binaryMetric("solved", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(9, 10), "t2": bools(8, 10)}, map[string][]any{"t1": bools(1, 10), "t2": bools(2, 10)})
	for i := range in.Candidate.Cells {
		c := &in.Candidate.Cells[i]
		c.Tuple.CandidateKind = "external"
		c.Producers = nil
		for j := range c.Trials {
			for k := range c.Trials[j].Measurements {
				c.Trials[j].Measurements[k].Grader = "self:harness"
				c.Trials[j].Measurements[k].SelfReported = true
			}
		}
	}
	return in, m
}

func TestSelfReported(t *testing.T) {
	in, m := selfReportedInput()
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if r.P == nil || r.Q != nil || r.Verdict != "" {
		t.Errorf("p %v q %v verdict %q", r.P, r.Q, r.Verdict)
	}
	cf := confound(r, confoundSelfReported)
	if cf == nil || cf.Candidate != 10 || cf.Reference != 0 {
		t.Errorf("confound %+v", cf)
	}
	if !strings.HasSuffix(r.Reason, "on values the harness reported itself: not a finding, and outside the corrected family") {
		t.Errorf("reason %q", r.Reason)
	}
	if c.Correction.Family != 0 {
		t.Errorf("family %d, want 0", c.Correction.Family)
	}
	if s := find(c, m.Digest, LevelSuite, ""); s.Verdict != "" || s.Q != nil {
		t.Errorf("suite %+v", s)
	}
	if len(c.DeclaredProvenance) != 1 || c.DeclaredProvenance[0] != "candidate" {
		t.Errorf("declared provenance %v", c.DeclaredProvenance)
	}
	// A topos candidate never reads a self-reported value: missing instead.
	in.Candidate.Cells[0].Tuple.CandidateKind = "topos"
	r = find(mustCompare(t, in), m.Digest, LevelTask, "t1")
	if r.Verdict != Insufficient || confound(r, confoundMissing) == nil {
		t.Errorf("topos: %s %+v", r.Verdict, r.Confounds)
	}
}

func TestDeclaredProvenance(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Reference.Cells[0].Tuple.CandidateKind = "external"
	c := mustCompare(t, in)
	if len(c.DeclaredProvenance) != 1 || c.DeclaredProvenance[0] != "reference" {
		t.Errorf("declared provenance %v", c.DeclaredProvenance)
	}
}

func TestModelComparisonIsSingleVariable(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)}))
	if !c.SingleVariable || len(c.Variable) != 1 || c.Variable[0] != FieldModel || len(c.Confounds) != 0 {
		t.Errorf("variable %v single %v confounds %v", c.Variable, c.SingleVariable, c.Confounds)
	}
}

func TestConfounds(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10), "t2": bools(6, 10)}, map[string][]any{"t1": bools(5, 10), "t2": append(bools(6, 8), nil, nil)})
	// The comparison declares the model its variable; t1's reference ran
	// another version of the task.
	in.Variable = "model"
	in.Reference.Cells[0].Tuple.TaskHash = "sha256:t1-old"
	// One request of t2's candidate was served by another model.
	in.Candidate.Cells[1].Trials[0].Requests = []Request{{Model: "model-b", Served: "model-b"}, {Model: "model-b", Served: "model-z"}}
	c := mustCompare(t, in)
	if c.SingleVariable {
		t.Error("a differing task hash leaves the comparison single-variable")
	}
	r1 := find(c, m.Digest, LevelTask, "t1")
	if confound(r1, FieldTaskHash) == nil || confound(r1, layerObservedModel) != nil {
		t.Errorf("t1 confounds %+v", r1.Confounds)
	}
	r2 := find(c, m.Digest, LevelTask, "t2")
	if cf := confound(r2, layerObservedModel); cf == nil || cf.Candidate != 1 || cf.Reference != 0 {
		t.Errorf("t2 observed.model %+v", r2.Confounds)
	}
	if cf := confound(r2, confoundMissing); cf == nil || cf.Candidate != 0 || cf.Reference != 2 {
		t.Errorf("t2 missing %+v", cf)
	}
	if !strings.HasSuffix(r2.Reason, "; 2 of 10 reference trials have no value") {
		t.Errorf("t2 reason %q", r2.Reason)
	}
	s := find(c, m.Digest, LevelSuite, "")
	if cf := confound(s, FieldTaskHash); cf == nil || len(cf.Tasks) != 1 || cf.Tasks[0] != "t1" {
		t.Errorf("suite task_hash %+v", cf)
	}
	if cf := confound(s, layerObservedModel); cf == nil || cf.Tasks[0] != "t2" || cf.Candidate != 1 {
		t.Errorf("suite observed.model %+v", cf)
	}
	want := []string{FieldTaskHash, confoundMissing, layerObservedModel}
	if strings.Join(c.Confounds, ",") != strings.Join(sortedStrings(want), ",") {
		t.Errorf("comparison confounds %v", c.Confounds)
	}
	// A layer confound alone leaves single_variable true.
	in.Reference.Cells[0].Tuple.TaskHash = "sha256:t1"
	if c := mustCompare(t, in); !c.SingleVariable {
		t.Error("a layer confound changed single_variable")
	}
	// Two logical variables differing and none declared: no variable, both
	// fields confounds.
	in.Variable = ""
	in.Reference.Cells[0].Tuple.Reasoning = "high"
	in.Reference.Cells[1].Tuple.Reasoning = "high"
	c = mustCompare(t, in)
	if len(c.Variable) != 0 || c.SingleVariable || confound(find(c, m.Digest, LevelTask, "t1"), FieldModel) == nil {
		t.Errorf("model and reasoning: variable %v single %v", c.Variable, c.SingleVariable)
	}
}

func sortedStrings(v []string) []string {
	out := append([]string{}, v...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestSentLayers(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Variable = "tools"
	// The candidate's first trial sent nothing to its configured model, and
	// its requests carried a reasoning value the reference never sent.
	in.Candidate.Cells[0].Trials[0].Requests = []Request{{Model: "model-x", Served: "model-x"}, {Model: "model-x", Served: "model-x"}}
	in.Candidate.Cells[0].Trials[1].Requests = []Request{{Model: "model-b", Reasoning: "high", Served: "model-b"}}
	in.Reference.Cells[0].Trials[1].Requests = []Request{{Model: "model-a", Reasoning: "low", Served: "model-a"}}
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if cf := confound(r, layerSentModel); cf == nil || cf.Candidate != 2 || cf.Reference != 0 {
		t.Errorf("sent.model %+v", cf)
	}
	if cf := confound(r, layerSentReasoning); cf == nil || cf.Candidate != 1 || cf.Reference != 1 {
		t.Errorf("sent.reasoning %+v", cf)
	}
	// With the variable model, sent.reasoning is not a confound.
	in.Variable = "model"
	if r := find(mustCompare(t, in), m.Digest, LevelTask, "t1"); confound(r, layerSentReasoning) != nil {
		t.Error("sent.reasoning under the variable model")
	}
}

func TestServiceVersions(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10), "t2": bools(5, 10)}, map[string][]any{"t1": bools(5, 10), "t2": bools(5, 10)})
	for i := range in.Candidate.Cells[0].Trials {
		in.Candidate.Cells[0].Trials[i].ServiceVersions = map[string]string{"api": "2.0"}
		in.Reference.Cells[0].Trials[i].ServiceVersions = map[string]string{"api": "1.0"}
	}
	in.Candidate.Cells[0].Trials[0].ServiceVersions = map[string]string{"api": "1.0", "other": "9"}
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if cf := confound(r, layerServiceVersions); cf == nil || cf.Candidate != 9 || cf.Reference != 0 {
		t.Errorf("service versions %+v", r.Confounds)
	}
	if confound(find(c, m.Digest, LevelTask, "t2"), layerServiceVersions) != nil || !c.SingleVariable {
		t.Error("the confound reached t2 or single_variable")
	}
}

func TestDeclaredImage(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10), "t2": bools(5, 10)}, map[string][]any{"t1": bools(5, 10), "t2": bools(5, 10)})
	in.Variable = "model"
	for _, side := range []*Side{&in.Candidate, &in.Reference} {
		for ci := range side.Cells {
			side.Cells[ci].Tuple.CandidateKind = "external"
			for ti := range side.Cells[ci].Trials {
				side.Cells[ci].Trials[ti].DeclaredImage = "harness@sha256:1"
			}
		}
	}
	in.Candidate.Cells[0].Trials[3].DeclaredImage = "harness@sha256:2"
	c := mustCompare(t, in)
	if cf := confound(find(c, m.Digest, LevelTask, "t1"), layerDeclaredImage); cf == nil || cf.Candidate != 1 || cf.Reference != 0 {
		t.Errorf("declared.image %+v", cf)
	}
	if confound(find(c, m.Digest, LevelTask, "t2"), layerDeclaredImage) != nil {
		t.Error("declared.image on t2")
	}
	in.Variable = "agent"
	if confound(find(mustCompare(t, in), m.Digest, LevelTask, "t1"), layerDeclaredImage) != nil {
		t.Error("declared.image under the variable agent")
	}
}

func TestDeclaredReasoningIsSingleVariable(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	for _, side := range []*Side{&in.Candidate, &in.Reference} {
		side.Cells[0].Tuple.CandidateKind = "external"
		side.Cells[0].Tuple.Program = `{"harness":"h","version":"1"}`
		side.Cells[0].Tuple.Model = "model-a"
	}
	in.Reference.Cells[0].Tuple.Reasoning = "low"
	c := mustCompare(t, in)
	if !c.SingleVariable || len(c.Variable) != 1 || c.Variable[0] != FieldReasoning {
		t.Errorf("variable %v single %v", c.Variable, c.SingleVariable)
	}
}

func TestProgramConfigIsSingleVariable(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	for _, side := range []*Side{&in.Candidate, &in.Reference} {
		side.Cells[0].Tuple.CandidateKind = "program"
		side.Cells[0].Tuple.AgentOrImageDigest = "sha256:runner"
		side.Cells[0].Tuple.Model = ""
	}
	in.Candidate.Cells[0].Tuple.Program = `{"config":{"url":"/v2"}}`
	in.Reference.Cells[0].Tuple.Program = `{"config":{"url":"/v1"}}`
	c := mustCompare(t, in)
	want := []string{FieldCandidateKind, FieldAgentOrImageDigest, FieldProgram}
	if !c.SingleVariable || strings.Join(c.Variable, ",") != strings.Join(want, ",") {
		t.Errorf("variable %v single %v", c.Variable, c.SingleVariable)
	}
}

func TestVariableIsOneLogicalVariable(t *testing.T) {
	for _, c := range []struct {
		fields []string
		want   string
	}{
		{[]string{FieldModel}, "model"},
		{[]string{FieldProgram, FieldCandidateKind, FieldAgentOrImageDigest}, "agent"},
		{[]string{FieldWorkspaceRef}, "workspace"},
	} {
		if got, err := LogicalVariable(c.fields); err != nil || got != c.want {
			t.Errorf("%v: %q %v", c.fields, got, err)
		}
	}
	for _, f := range [][]string{{FieldTaskHash}, {FieldGraderDigests}, {FieldProgram}, {FieldModel, FieldReasoning}, {"bogus"}} {
		if _, err := LogicalVariable(f); !errors.Is(err, ErrVariable) {
			t.Errorf("%v: %v, want refused", f, err)
		}
	}
	m := binaryMetric("pass", HigherIsBetter)
	for _, v := range []string{"bogus", "task"} {
		in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
		in.Variable = v
		if _, err := Compare(in); !errors.Is(err, ErrVariable) {
			t.Errorf("variable %q: %v", v, err)
		}
	}
	if f, ok := VariableFields("tools"); !ok || f[0] != FieldTools {
		t.Errorf("tools: %v", f)
	}
}

// A re-graded baseline is read at the candidate's grader and the grader
// confound disappears; a partly re-graded one is not.
func TestReferenceReadAtCandidatesGrader(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Candidate.Cells[0].Producers[m.Digest] = "sha256:judge-v2"
	for i := range in.Candidate.Cells[0].Trials {
		in.Candidate.Cells[0].Trials[i].Measurements[0].Grader = "sha256:judge-v2"
	}
	// The reference holds its own grading and, for every trial, a re-grade.
	ref := &in.Reference.Cells[0]
	for i := range ref.Trials {
		ref.Trials[i].Measurements = append(ref.Trials[i].Measurements, Measurement{ID: fmt.Sprintf("re-%d", i), Metric: m.Digest, Grader: "sha256:judge-v2", Generation: 1, Value: true})
	}
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if confound(r, FieldGraderDigests) != nil || !c.SingleVariable {
		t.Errorf("re-graded: %+v", r.Confounds)
	}
	if k := *r.Reference.Numerator; k != 10 {
		t.Errorf("read at the re-grade, the reference holds %d true values, want 10", k)
	}
	ref.Trials[0].Measurements = ref.Trials[0].Measurements[:1]
	c = mustCompare(t, in)
	r = find(c, m.Digest, LevelTask, "t1")
	if confound(r, FieldGraderDigests) == nil || c.SingleVariable {
		t.Errorf("partly re-graded: %+v", r.Confounds)
	}
	if k := *r.Reference.Numerator; k != 5 {
		t.Errorf("read at its own grader, the reference holds %d true values, want 5", k)
	}
}

func TestIncomplete(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Candidate.Cells[0].Trials = append(in.Candidate.Cells[0].Trials,
		Trial{ID: "c-t1-900", Outcome: OutcomeInfraFailed, Retried: true},
		Trial{ID: "c-t1-901", Outcome: OutcomeInfraFailed},
		Trial{ID: "c-t1-902", Outcome: OutcomeOverBudget},
		Trial{ID: "c-t1-903"},
	)
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if cf := confound(r, confoundIncomplete); cf == nil || cf.Candidate != 2 || cf.Reference != 0 {
		t.Errorf("incomplete %+v", cf)
	}
	if !strings.HasSuffix(r.Reason, "; 2 of 12 candidate trials were left out: failed to run, canceled or over budget") {
		t.Errorf("reason %q", r.Reason)
	}
	ex := r.Candidate.Excluded
	if ex[excludedRetried] != 1 || ex[excludedInfraFailed] != 1 || ex[excludedOverBudget] != 1 || ex[excludedNotDone] != 1 || r.Candidate.N != 10 {
		t.Errorf("excluded %v n %d", ex, r.Candidate.N)
	}
	if !c.SingleVariable {
		t.Error("incomplete changed single_variable")
	}
}

func TestWindow(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	for i := range in.Reference.Cells[0].Trials {
		tr := &in.Reference.Cells[0].Trials[i]
		tr.StartedAt = tr.StartedAt.Add(-41 * 24 * time.Hour)
		tr.EndedAt = tr.EndedAt.Add(-41 * 24 * time.Hour)
	}
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	cf := confound(r, confoundWindow)
	if cf == nil {
		t.Fatalf("no window: %+v", r.Confounds)
	}
	if w, ok := cf.Reference.(Window); !ok || !w.From.Equal(epoch.Add(-41*24*time.Hour)) {
		t.Errorf("reference window %+v", cf.Reference)
	}
	if !strings.HasSuffix(r.Reason, "; the two sides' time windows differ by 41 days") {
		t.Errorf("reason %q", r.Reason)
	}
	if !c.SingleVariable || c.Windows.Reference == nil || !c.Windows.Reference.From.Equal(epoch.Add(-41*24*time.Hour)) {
		t.Errorf("windows %+v", c.Windows)
	}
	if s := find(c, m.Digest, LevelSuite, ""); confound(s, confoundWindow) == nil || confound(s, confoundWindow).Tasks[0] != "t1" {
		t.Error("the suite-level result carries no window")
	}
	// Within WINDOW_TOLERANCE, none.
	in = pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	for i := range in.Reference.Cells[0].Trials {
		in.Reference.Cells[0].Trials[i].StartedAt = in.Reference.Cells[0].Trials[i].StartedAt.Add(-6 * 24 * time.Hour)
		in.Reference.Cells[0].Trials[i].EndedAt = in.Reference.Cells[0].Trials[i].EndedAt.Add(-6 * 24 * time.Hour)
	}
	if confound(find(mustCompare(t, in), m.Digest, LevelTask, "t1"), confoundWindow) != nil {
		t.Error("a window within the tolerance")
	}
	if days(36*time.Hour) != "1.5 days" || days(24*time.Hour) != "1 day" {
		t.Error(days(36*time.Hour), days(24*time.Hour))
	}
}

func TestUncalibrated(t *testing.T) {
	m := labelMetric("topic", "a", "b")
	in := pairInput(m, map[string][]any{"t1": strs("a", "a", "b", "a", "b")}, map[string][]any{"t1": strs("a", "b", "b", "b", "b")})
	in.Calibrations = []Calibration{{Grader: grader, Metric: m.Digest, Pairs: 10, Alpha: 0.4, Calibrated: false}}
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if cf := confound(r, confoundUncalibrated); cf == nil || cf.Grader != grader {
		t.Errorf("uncalibrated %+v", r.Confounds)
	}
	if !strings.HasSuffix(r.Reason, suffixUncalibrated) || !c.SingleVariable {
		t.Errorf("reason %q", r.Reason)
	}
	in.Calibrations[0].Calibrated = true
	if confound(find(mustCompare(t, in), m.Digest, LevelTask, "t1"), confoundUncalibrated) != nil {
		t.Error("a calibrated judge is uncalibrated")
	}
}

func TestAppliesTo(t *testing.T) {
	m := numericMetric("lines", TypeCount, AggMean, NoDirection)
	m.AppliesTo = []Outcome{OutcomeCompleted}
	in := pairInput(m, map[string][]any{"t1": nums(1, 2, 3, 4, 5, 6)}, map[string][]any{"t1": nums(1, 2, 3, 4, 5, 6)})
	in.Candidate.Cells[0].Trials[5].Outcome = OutcomeDeclined
	c := mustCompare(t, in)
	r := find(c, m.Digest, LevelTask, "t1")
	if r.Candidate.N != 5 || r.Candidate.NotApplicable != 1 || r.Candidate.Missing != 0 || r.Candidate.Denominator != 5 {
		t.Errorf("n %d not applicable %d missing %d", r.Candidate.N, r.Candidate.NotApplicable, r.Candidate.Missing)
	}
	if confound(r, confoundMissing) != nil {
		t.Error("a trial outside applies_to is missing")
	}
}

func TestComparisonAcrossTasks(t *testing.T) {
	pass := binaryMetric("pass", HigherIsBetter)
	cost := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	tp1, tp2 := tuple("model-a", "prompt-1"), tuple("model-a", "prompt-2")
	tp2.EnvironmentDigest = "sha256:env-2"
	x := cell("prompt-1", tp1, merge(trialsOf("x", epoch, pass, bools(9, 10)...), trialsOf("x", epoch, cost, nums(1, 2, 1, 2, 1, 2, 1, 2, 1, 2)...)), pass, cost)
	y := cell("prompt-2", tp2, merge(trialsOf("y", epoch, pass, bools(2, 10)...), trialsOf("y", epoch, cost, nums(2, 3, 2, 3, 2, 3, 2, 3, 2, 3)...)), pass, cost)
	in := Input{
		Parameters: Defaults(), Suite: Suite{Metrics: []Metric{pass, cost}, PrimaryMetric: pass.Digest},
		Candidate: Side{Candidate: "sha256:c", Cells: []Cell{x}}, Reference: Side{Candidate: "sha256:c", Cells: []Cell{y}},
		AcrossTasks: true,
	}
	c := mustCompare(t, in)
	if len(c.Results) != 2 || !c.Exploratory || c.Correction.Primary != "" || c.Correction.Family != 2 {
		t.Fatalf("results %d exploratory %v correction %+v", len(c.Results), c.Exploratory, c.Correction)
	}
	r := find(c, pass.Digest, LevelTask, "prompt-1 vs prompt-2")
	if r == nil || confound(r, FieldEnvironmentDigest) == nil || confound(r, FieldTaskHash) != nil {
		t.Fatalf("result %+v", r)
	}
	if len(c.Variable) != 1 || c.Variable[0] != FieldTaskHash || c.SingleVariable {
		t.Errorf("variable %v single %v", c.Variable, c.SingleVariable)
	}
	if !strings.Contains(r.Reason, "after correcting 2 results") {
		t.Errorf("reason %q", r.Reason)
	}
	in.Reference.Cells = append(in.Reference.Cells, y)
	if _, err := Compare(in); !errors.Is(err, ErrInput) {
		t.Errorf("two reference cells: %v", err)
	}
}

func TestCandidateLevelIsDescriptive(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	style := Metric{Digest: "sha256:style", Name: "style", Type: TypeScore, Direction: HigherIsBetter, Level: LevelCandidate}
	in := pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Suite.Metrics = append(in.Suite.Metrics, style)
	in.Candidate.Values = []Measurement{{ID: "v1", Metric: style.Digest, Grader: "sha256:j", Generation: 1, Value: 0.5}, {ID: "v2", Metric: style.Digest, Grader: "sha256:j", Generation: 2, Value: 0.75}}
	in.Reference.Values = []Measurement{{ID: "v3", Metric: style.Digest, Grader: "sha256:j", Generation: 1, Value: 0.5}}
	c := mustCompare(t, in)
	r := find(c, style.Digest, LevelCandidate, "")
	if r == nil || r.Delta == nil || r.Delta.Value != 0.25 || r.P != nil || r.Q != nil || r.Verdict != "" || r.DeltaInterval != nil {
		t.Fatalf("candidate result %+v", r)
	}
	if r.Reason != reasonCandidate || r.Candidate.NoInterval != noIntervalCandidate {
		t.Errorf("reason %q", r.Reason)
	}
	if c.Correction.Family != 1 {
		t.Errorf("family %d", c.Correction.Family)
	}
	if _, ok := c.VerdictCounts[style.Digest]; ok {
		t.Error("a candidate-level result in verdict_counts")
	}
}

// A repeat rate is tested by the exact permutation of its difference; mode
// share and distinct count are descriptive.
func TestDescriptiveCellMeasures(t *testing.T) {
	label := labelMetric("answer", "a", "b", "c", "d", "e")
	rr := cellMetric("agreement", AggRepeatRate, label)
	ms := cellMetric("mode", AggModeShare, label)
	dc := cellMetric("distinct", AggDistinctCount, label)
	in := pairInput(label, map[string][]any{"t1": strs("a", "a", "a", "a", "a"), "t2": strs("a", "a", "b", "a", "a")}, map[string][]any{"t1": strs("a", "b", "c", "d", "e"), "t2": strs("a", "b", "c", "d", "e")})
	in.Suite.Metrics = append(in.Suite.Metrics, rr, ms, dc)
	c := mustCompare(t, in)
	r := find(c, rr.Digest, LevelTask, "t1")
	if r.Statistic != statDifference || !*r.Exact || *r.Resamples != 252 || *r.P != 12.0/252 {
		t.Errorf("repeat rate: %s exact %v resamples %v p %v", r.Statistic, *r.Exact, *r.Resamples, *r.P)
	}
	if r.DeltaInterval != nil || r.NoDeltaInterval != noIntervalRepeatRate || r.Candidate.NoInterval != noIntervalRepeatRate {
		t.Errorf("repeat rate interval %+v %q", r.DeltaInterval, r.NoDeltaInterval)
	}
	if v, _ := r.Candidate.Number(); v != 1 {
		t.Errorf("R = %v", v)
	}
	s := find(c, rr.Digest, LevelSuite, "")
	if v, _ := s.Candidate.Number(); v != (1+0.6)/2 {
		t.Errorf("suite repeat rate %v, want the weighted mean 0.8", v)
	}
	for _, d := range []Metric{ms, dc} {
		for _, level := range []Level{LevelTask, LevelSuite} {
			task := "t1"
			if level == LevelSuite {
				task = ""
			}
			r := find(c, d.Digest, level, task)
			if r.P != nil || r.Q != nil || r.Verdict != "" || r.DeltaInterval != nil || r.Method != "" {
				t.Errorf("%s %s: %+v", d.Name, level, r)
			}
			if r.NoDeltaInterval != noIntervalDescriptive {
				t.Errorf("%s %s: %q", d.Name, level, r.NoDeltaInterval)
			}
		}
		if _, ok := c.VerdictCounts[d.Digest]; ok {
			t.Errorf("%s in verdict_counts", d.Name)
		}
	}
	if r := find(c, ms.Digest, LevelTask, "t2"); *r.Candidate.Numerator != 4 || r.Candidate.Denominator != 5 || r.Reason != reasonModeShare {
		t.Errorf("mode share %+v", r.Candidate)
	}
	if r := find(c, dc.Digest, LevelTask, "t2"); r.Reason != reasonDistinctCount {
		t.Errorf("distinct %q", r.Reason)
	}
	// Family: two task-level repeat rates and the label tests of two tasks
	// and the suite; the primary label suite result stands alone.
	if c.Correction.Family != 5 {
		t.Errorf("family %d", c.Correction.Family)
	}
}

func TestCellMeasuresExcludeLabels(t *testing.T) {
	label := labelMetric("answer", "a", "b", "declined", "none")
	rr := cellMetric("agreement", AggRepeatRate, label)
	ms := cellMetric("mode", AggModeShare, label)
	in := pairInput(label, map[string][]any{"t1": strs("a", "a", "b", "declined", "none", "declined"), "t2": strs("declined", "declined", "declined", "declined", "declined")}, map[string][]any{"t1": strs("a", "b", "b", "a", "b", "a"), "t2": strs("a", "b", "a", "b", "a")})
	in.Suite.Metrics = append(in.Suite.Metrics, rr, ms)
	c := mustCompare(t, in)
	r := find(c, ms.Digest, LevelTask, "t1")
	if *r.Candidate.Numerator != 2 || r.Candidate.Denominator != 3 || r.Candidate.ExcludedLabels["declined"] != 2 || r.Candidate.ExcludedLabels["none"] != 1 {
		t.Errorf("mode share %+v", r.Candidate)
	}
	if v, _ := r.Candidate.Number(); v != 2.0/3 {
		t.Errorf("mode share %v", v)
	}
	for _, d := range []Metric{rr, ms} {
		r := find(c, d.Digest, LevelTask, "t2")
		if r.Candidate.Value != nil || r.Candidate.Denominator != 0 || r.Verdict != Insufficient && r.Verdict != "" {
			t.Errorf("%s of declines: %+v %s", d.Name, r.Candidate, r.Verdict)
		}
	}
	// exclude_labels set empty leaves none out.
	ms.ExcludeLabels = []string{}
	in.Suite.Metrics[2] = ms
	r = find(mustCompare(t, in), ms.Digest, LevelTask, "t2")
	if v, _ := r.Candidate.Number(); v != 1 {
		t.Errorf("with nothing excluded, a cell of declines has the mode share %v", v)
	}
}

func TestTaxonomyReads(t *testing.T) {
	m := labelMetric("topic")
	tax := Taxonomy{ID: "tax_1", Digest: "sha256:tax", Canonical: []string{"billing", "shipping"}, Map: map[string]string{"invoice": "billing", "refund": "billing", "parcel": "shipping"}}
	in := pairInput(m, map[string][]any{"t1": strs("invoice", "refund", "parcel", "weather", "invoice")}, map[string][]any{"t1": strs("parcel", "parcel", "invoice", "parcel", "parcel")})
	in.Taxonomies = map[string]Taxonomy{m.Digest: tax}
	c := mustCompare(t, in)
	if c.Taxonomies[m.Digest] != "tax_1" {
		t.Errorf("taxonomies %v", c.Taxonomies)
	}
	r := find(c, m.Digest, LevelTask, "t1")
	if r.Candidate.Missing != 0 || r.Candidate.Unmapped != 1 || r.Candidate.Denominator != 5 {
		t.Errorf("summary %+v", r.Candidate)
	}
	var weather *LabelShare
	for i := range r.Candidate.Distribution {
		if r.Candidate.Distribution[i].Label == "weather" {
			weather = &r.Candidate.Distribution[i]
		}
	}
	if weather == nil || !weather.Unmapped || weather.Count != 1 {
		t.Errorf("distribution %+v", r.Candidate.Distribution)
	}
	if r.Candidate.Mode != "billing" || math.Abs(r.Delta.PerLabel["billing"]-0.4) > 1e-15 {
		t.Errorf("mode %q delta %v", r.Candidate.Mode, r.Delta.PerLabel)
	}
	if r.Method != methodFFH || r.Statistic != statTableProb {
		t.Errorf("method %s", r.Method)
	}
	doc, _, err := InputsDocument(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(doc, []byte(`"canonical":"billing"`)) || !bytes.Contains(doc, []byte(`"id":"tax_1"`)) {
		t.Error("the inputs document lacks the canonical labels or the record")
	}
	// A new record maps the raw label: the figures move, and so does the
	// inputs digest, so a new comparison is a new record.
	tax2 := tax
	tax2.ID, tax2.Digest = "tax_2", "sha256:tax2"
	tax2.Map = map[string]string{"invoice": "billing", "refund": "billing", "parcel": "shipping", "weather": "shipping"}
	in.Taxonomies = map[string]Taxonomy{m.Digest: tax2}
	c2 := mustCompare(t, in)
	if c2.InputsDigest == c.InputsDigest || find(c2, m.Digest, LevelTask, "t1").Candidate.Unmapped != 0 {
		t.Error("a new taxonomy record changed nothing")
	}
}

// Every result carries a reason from the templates, and every absent
// interval its sentence.
func TestReasons(t *testing.T) {
	for got, want := range map[string]string{
		reasonPrimary(0.0004):                             "p < 0.001 for the primary metric, tested alone without correction",
		reasonFamily(0.048, 0.19, 40, 0.05):               "p = 0.048; q = 0.19 after correcting 40 results at 5%",
		reasonExploratoryPrimary(0.012, 0.31, 41, 0.05):   "p = 0.012; q = 0.31 after correcting 41 results at 5%; exploratory, so the primary metric is corrected with the rest",
		reasonSelfReported(0.03):                          "p = 0.03 on values the harness reported itself: not a finding, and outside the corrected family",
		reasonPair(0.03, 0.06, 3, 0.05):                   "p = 0.03 unadjusted; h = 0.06 after Holm's adjustment over 3 pairs at 5%",
		reasonFloor(sizes(5, 5), 40, false, 0.05):         "with 5 trials per side, no difference can be shown among 40 results",
		reasonFloor(sizes(5, 7), 40, false, 0.05):         "with 5 and 7 trials, no difference can be shown among 40 results",
		reasonFloor(sizes(2, 2), 1, true, 0.05):           "with 2 trials per side, no difference can be shown at 5%",
		reasonFloor(suiteSizes(3, 2, 2), 40, false, 0.05): "with 3 tasks of 2 trials per side, no difference can be shown among 40 results",
		suiteSizes(1, 2, 4):                               "1 task of 2 to 4 trials per side",
		reasonCoverage(0.7, 0.8, []LeftOut{{Task: "t3", Why: LeftOutTooFewTrials, Candidate: 3, Reference: 9}, {Task: "t7", Why: LeftOutUnpaired, unpairedBy: "reference"}}): "covers 70% of the suite's weight; 80% needed; left out: t3 (3 trials), t7 (not run by the reference)",
		noIntervalP90(29):                      "no interval below 29 values for a 90th percentile",
		noIntervalRanks(5, AggMedian, 0.95):    "no interval: 5 values cannot bound a median at 95%",
		noIntervalQuantileDelta(7, AggMedian):  "no interval: each side needs 7 values for a median at this level",
		noIntervalQuantileDelta(35, AggP90):    "no interval: each side needs 35 values for a 90th percentile at this level",
		noIntervalNoSpread(3301, 10000):        "no interval: 3,301 of 10,000 resamples had no spread",
		noIntervalSmallTask("t3", 12, 29):      "no interval: task t3 has 12 values; a 90th percentile needs 29",
		suffixMissing(2, 20, 0, 20):            "; 2 of 20 candidate trials have no value",
		suffixMissing(1, 20, 0, 20):            "; 1 of 20 candidate trials has no value",
		suffixIncomplete(2, 20, 1, 20):         "; 2 of 20 candidate trials and 1 of 20 reference trials were left out: failed to run, canceled or over budget",
		suffixIncomplete(0, 20, 1, 20):         "; 1 of 20 reference trials was left out: failed to run, canceled or over budget",
		suffixWindow(41 * 24 * time.Hour):      "; the two sides' time windows differ by 41 days",
		percent(0.755) + " " + count(-1234567): "75.5% -1,234,567",
	} {
		if got != want {
			t.Errorf("%q, want %q", got, want)
		}
	}
	// Every result of a mixed comparison has a reason, and every summary
	// without an interval its sentence.
	pass := binaryMetric("pass", HigherIsBetter)
	tail := numericMetric("tail", TypeDuration, AggP90, LowerIsBetter)
	med := numericMetric("med", TypeDuration, AggMedian, LowerIsBetter)
	in := pairInput(pass, map[string][]any{"t1": bools(3, 6), "t2": bools(5, 5)}, map[string][]any{"t1": bools(3, 6), "t2": bools(4, 5)})
	in.Suite.Metrics = append(in.Suite.Metrics, tail, med)
	for _, side := range []*Side{&in.Candidate, &in.Reference} {
		for ci := range side.Cells {
			c := &side.Cells[ci]
			for ti := range c.Trials {
				c.Trials[ti].Measurements = append(c.Trials[ti].Measurements,
					Measurement{ID: c.Trials[ti].ID + "-t", Metric: tail.Digest, Grader: grader, Generation: 1, Value: float64(ti + 1)},
					Measurement{ID: c.Trials[ti].ID + "-m", Metric: med.Digest, Grader: grader, Generation: 1, Value: float64(ti + 1)})
			}
			c.Producers[tail.Digest], c.Producers[med.Digest] = grader, grader
		}
	}
	c := mustCompare(t, in)
	for _, r := range c.Results {
		if r.Reason == "" {
			t.Errorf("%s %s %s has no reason", r.MetricName, r.Level, r.Task)
		}
		for _, s := range []Summary{r.Candidate, r.Reference} {
			if s.Interval == nil && s.NoInterval == "" {
				t.Errorf("%s %s %s: a summary without an interval or its sentence", r.MetricName, r.Level, r.Task)
			}
		}
		if r.Verdict != Insufficient && r.Verdict != "" && r.DeltaInterval == nil && r.NoDeltaInterval == "" {
			t.Errorf("%s %s %s: no delta interval and no sentence", r.MetricName, r.Level, r.Task)
		}
	}
	if r := find(c, tail.Digest, LevelTask, "t1"); r.Candidate.NoInterval != noIntervalP90(29) || r.NoDeltaInterval != noIntervalQuantileDelta(35, AggP90) {
		t.Errorf("p90: %q %q", r.Candidate.NoInterval, r.NoDeltaInterval)
	}
	if r := find(c, med.Digest, LevelTask, "t2"); r.Candidate.NoInterval != noIntervalRanks(5, AggMedian, 0.95) {
		t.Errorf("median: %q", r.Candidate.NoInterval)
	}
	if r := find(c, tail.Digest, LevelSuite, ""); r.Candidate.NoInterval != noIntervalSmallTask("t1", 6, 29) || r.NoDeltaInterval != noIntervalSmallTask("t1", 6, 29) {
		t.Errorf("suite p90: %q %q", r.Candidate.NoInterval, r.NoDeltaInterval)
	}
	if r := find(c, pass.Digest, LevelTask, "t1"); r.Reason != reasonZeroDelta && !strings.HasPrefix(r.Reason, "with 6 trials per side") {
		t.Errorf("a zero delta: %q", r.Reason)
	}
}

// A result whose exact test's smallest p-value is above its threshold
// carries the power-floor reason.
func TestPowerFloor(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	cand, ref := map[string][]any{}, map[string][]any{}
	for i := range 40 {
		task := fmt.Sprintf("t%02d", i)
		cand[task], ref[task] = bools(5, 5), bools(0, 5)
	}
	c := mustCompare(t, pairInput(m, cand, ref))
	if c.Correction.Family != 40 {
		t.Fatalf("family %d", c.Correction.Family)
	}
	r := find(c, m.Digest, LevelTask, "t00")
	if r.Reason != "with 5 trials per side, no difference can be shown among 40 results" {
		t.Errorf("reason %q", r.Reason)
	}
	// The floor of an exact permutation test: 5 against 5 is 2/252.
	pl := &plan{}
	pl.setFloor(testResult{exact: true, resamples: 252}, true)
	if pl.floor != 2.0/252 {
		t.Errorf("floor %v", pl.floor)
	}
	pl.setFloor(testResult{exact: true, resamples: 184756}, true)
	if pl.floor != 2.0/184756 || 0.05/4619 >= pl.floor || 0.05/4618 < pl.floor {
		t.Errorf("10 against 10: floor %v, a floor from m = 4,619", pl.floor)
	}
	// The primary result tested alone: a suite of one task of 2 trials per
	// side cannot be shown at 5% by the exact stratified test.
	mean := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	c = mustCompare(t, pairInput(mean, map[string][]any{"t1": nums(1, 2)}, map[string][]any{"t1": nums(8, 9)}))
	s := find(c, mean.Digest, LevelSuite, "")
	if s.Reason != "with 1 task of 2 trials per side, no difference can be shown at 5%" {
		t.Errorf("suite reason %q", s.Reason)
	}
}

// PERMUTATION_RESAMPLES resolves to 10,000 for small families and to
// ceil(20·m/FALSE_DISCOVERY_RATE) above them, and a lone true discovery in
// a family of 500 can pass.
func TestResamplesGrowWithTheFamily(t *testing.T) {
	for m, want := range map[int]int{1: 10000, 25: 10000, 40: 16000, 500: 200000} {
		if got := resamples(m, 0.05); got != want {
			t.Errorf("m = %d: %d, want %d", m, got, want)
		}
	}
	b := resamples(500, 0.05)
	var x, y []float64
	for i := range 15 {
		x, y = append(x, float64(i)), append(y, float64(i)+100)
	}
	r := permutationTest(x, y, welchT, Defaults().PermutationExactMax, mc("lone", b))
	ps := make([]float64, 500)
	ps[0] = r.p
	for i := 1; i < 500; i++ {
		ps[i] = 1
	}
	if q := benjaminiHochberg(ps)[0]; q > 0.05 {
		t.Errorf("a lone discovery: p %v q %v", r.p, q)
	}
	// In a comparison the family's tests draw the resolved B.
	m := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	cand, ref := map[string][]any{}, map[string][]any{}
	for i := range 30 {
		task := fmt.Sprintf("t%02d", i)
		v := make([]float64, 12)
		for j := range v {
			v[j] = float64(j)
		}
		cand[task], ref[task] = nums(v...), nums(v...)
	}
	c := mustCompare(t, pairInput(m, cand, ref))
	if c.Resamples.Family != 12000 {
		t.Errorf("family of 30: %d", c.Resamples.Family)
	}
}

func TestLabelResults(t *testing.T) {
	m := labelMetric("topic", "a", "b", "c")
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": strs("a", "a", "a", "a", "a", "a", "b"), "t2": strs("a", "a", "a", "a", "a", "a", "a")}, map[string][]any{"t1": strs("b", "b", "c", "c", "b", "c", "b"), "t2": strs("c", "c", "c", "b", "b", "b", "c")}))
	r := find(c, m.Digest, LevelTask, "t1")
	if r.DeltaKind != "per_label" || len(r.Delta.PerLabel) != 3 || len(r.DeltaInterval.PerLabel) != 3 || r.Verdict != Changed {
		t.Errorf("label result %+v", r)
	}
	s := find(c, m.Digest, LevelSuite, "")
	if s.Statistic != statLabelDistance || s.Verdict != Changed {
		t.Errorf("suite %s %s", s.Statistic, s.Verdict)
	}
	b, err := json.Marshal(r)
	if err != nil || !bytes.Contains(b, []byte(`"delta":{"a":`)) {
		t.Errorf("JSON %s %v", b, err)
	}
	if len(s.Candidate.Distribution) != 3 || s.Candidate.Mode != "a" {
		t.Errorf("suite distribution %+v", s.Candidate)
	}
}

func TestQuantileResults(t *testing.T) {
	m := numericMetric("time", TypeDuration, AggMedian, LowerIsBetter)
	x := nums(10, 11, 12, 13, 14, 15, 16, 17)
	y := nums(20, 21, 22, 23, 24, 25, 26, 27)
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": x, "t2": x}, map[string][]any{"t1": y, "t2": y}))
	r := find(c, m.Digest, LevelTask, "t1")
	if r.Delta.Value != -10 || r.DeltaInterval == nil || r.Verdict != Improved {
		t.Errorf("median result %+v", r)
	}
	if r.DeltaInterval.Interval[0] > -10 || r.DeltaInterval.Interval[1] < -10 {
		t.Errorf("interval %v", r.DeltaInterval.Interval)
	}
	if r.Candidate.Interval == nil || r.Candidate.Mean == nil || *r.Candidate.Min != 10 || *r.Candidate.Max != 17 {
		t.Errorf("summary %+v", r.Candidate)
	}
	s := find(c, m.Digest, LevelSuite, "")
	if s.Delta.Value != -10 || s.DeltaInterval == nil || s.Candidate.Interval == nil {
		t.Errorf("suite %+v", s)
	}
}

func TestMeanResults(t *testing.T) {
	m := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	x := nums(0.10, 0.12, 0.11, 0.13, 0.09, 0.10, 0.14, 0.12)
	y := nums(0.20, 0.22, 0.19, 0.25, 0.21, 0.18, 0.23, 0.20)
	c := mustCompare(t, pairInput(m, map[string][]any{"t1": x, "t2": x}, map[string][]any{"t1": y, "t2": y}))
	r := find(c, m.Digest, LevelTask, "t1")
	if r.Statistic != statWelchT || !*r.Exact || *r.Resamples != 12870 || r.Verdict != Improved || r.DeltaInterval == nil {
		t.Errorf("mean result %+v", r)
	}
	if r.Candidate.Interval == nil || r.Candidate.SD == nil {
		t.Errorf("summary %+v", r.Candidate)
	}
	s := find(c, m.Digest, LevelSuite, "")
	if s.Statistic != statWelchT || s.DeltaInterval == nil || s.Candidate.Interval == nil || s.Verdict != Improved {
		t.Errorf("suite %+v", s)
	}
	// A cell of 0, 0, 0, 0, 10 shows no interval, and says why.
	c = mustCompare(t, pairInput(m, map[string][]any{"t1": nums(0, 0, 0, 0, 10)}, map[string][]any{"t1": nums(1, 2, 3, 4, 5)}))
	r = find(c, m.Digest, LevelTask, "t1")
	if r.Candidate.Interval != nil || !strings.HasPrefix(r.Candidate.NoInterval, "no interval: 3,") || !strings.HasSuffix(r.Candidate.NoInterval, " of 10,000 resamples had no spread") {
		t.Errorf("no spread: %q", r.Candidate.NoInterval)
	}
}

func TestInvalidInput(t *testing.T) {
	m := binaryMetric("pass", HigherIsBetter)
	bad := m
	bad.Aggregate = AggMean
	in := pairInput(bad, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	if _, err := Compare(in); !errors.Is(err, ErrInput) {
		t.Errorf("binary with mean: %v", err)
	}
	cm := Metric{Digest: "sha256:c", Name: "c", Level: LevelCell, Aggregate: AggRepeatRate, Of: "sha256:none"}
	in = pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Suite.Metrics = append(in.Suite.Metrics, cm)
	if _, err := Compare(in); !errors.Is(err, ErrInput) {
		t.Errorf("a cell-level metric without its of metric: %v", err)
	}
	label := labelMetric("l", "a")
	cm.Of, cm.Aggregate = label.Digest, AggMean
	in.Suite.Metrics = []Metric{m, label, cm}
	if _, err := Compare(in); !errors.Is(err, ErrInput) {
		t.Errorf("a cell-level metric with a mean: %v", err)
	}
	in = pairInput(m, map[string][]any{"t1": bools(5, 10)}, map[string][]any{"t1": bools(5, 10)})
	in.Parameters.ConfidenceLevel = 1
	if _, err := Compare(in); !errors.Is(err, ErrParameters) {
		t.Errorf("a level of 1: %v", err)
	}
	if _, _, err := InputsDocument(in); !errors.Is(err, ErrParameters) {
		t.Errorf("inputs document: %v", err)
	}
}
