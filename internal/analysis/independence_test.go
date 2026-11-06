// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"encoding/json/v2"
	"math"
	"slices"
	"strings"
	"testing"
)

// labels writes a string of one-letter labels as positions: a is 0, b 1.
func labels(s string) []float64 {
	out := make([]float64, len(s))
	for i, r := range s {
		out[i] = float64(r - 'a')
	}
	return out
}

// sideLabels writes each task's sides, each a string of one-letter labels.
func sideLabels(tasks ...[]string) [][][]float64 {
	out := make([][][]float64, len(tasks))
	for t, sides := range tasks {
		for _, s := range sides {
			out[t] = append(out[t], labels(s))
		}
	}
	return out
}

// The stratified label test against the worked examples of spec 008,
// whose p-values were enumerated relabeling by relabeling in exact
// rationals: two sides, opposite shifts on two tasks, three sides, and
// tasks of one label each. An exact test's floor is its symmetric
// relabelings over all of them.
func TestStratifiedLabelTest(t *testing.T) {
	p := Defaults()
	cases := []struct {
		name      string
		tasks     [][][]float64
		x2        float64
		p         float64
		relabeled int
		floor     float64
	}{
		{"two sides", sideLabels([]string{"aaab", "bbba"}, []string{"aab", "abb"}), 8.0 / 3, 752.0 / 1400, 1400, 2.0 / 1400},
		{"opposite shifts", sideLabels([]string{"aaaa", "bbbb"}, []string{"bbbb", "aaaa"}), 16, 4.0 / 4900, 4900, 2.0 / 4900},
		{"three sides", sideLabels([]string{"aab", "abb", "bbb"}, []string{"aa", "ab", "bb"}), 7, 63720.0 / 151200, 151200, 6.0 / 151200},
		{"one label per task", sideLabels([]string{"aaa", "aaa"}, []string{"bb", "bb"}), 0, 1, 120, 2.0 / 120},
	}
	for _, tc := range cases {
		if got := stratifiedX2(tc.tasks, 2); math.Abs(got-tc.x2) > 1e-12 {
			t.Errorf("%s: X² = %v, want %v", tc.name, got, tc.x2)
		}
		r, floor := stratifiedLabelTest(tc.tasks, 2, p.PermutationExactMax, mc(tc.name, 10000))
		if !r.exact || r.resamples != tc.relabeled || math.Abs(r.p-tc.p) > 1e-12 {
			t.Errorf("%s: %+v, want p %v of %d relabelings", tc.name, r, tc.p, tc.relabeled)
		}
		if math.Abs(floor-tc.floor) > 1e-15 {
			t.Errorf("%s: floor %v, want %v", tc.name, floor, tc.floor)
		}
	}
	// Two tasks of twelve trials per side have 7,312,459,672,336
	// relabelings, so the test draws them. Its p-value repeats bit for
	// bit from one seed, and lands near the exact 511544080/2704312009 =
	// 0.18916, which the hypergeometric distribution of each task's table
	// gives: sequential stopping at 200 exceedances has a standard error
	// of about 0.013 there.
	big := sideLabels([]string{"aaaaaaaaabbb", "aaaabbbbbbbb"}, []string{"aaaaaabbbbbb", "aaaaabbbbbbb"})
	a, floor := stratifiedLabelTest(big, 2, p.PermutationExactMax, mc("big", 10000))
	b, _ := stratifiedLabelTest(big, 2, p.PermutationExactMax, mc("big", 10000))
	if a != b || a.exact || floor != 0 {
		t.Fatalf("the same seed gave %+v and %+v", a, b)
	}
	if exact := 511544080.0 / 2704312009; math.Abs(a.p-exact) > 0.06 {
		t.Errorf("Monte Carlo p %v, the exact one %v", a.p, exact)
	}
	if got := stratifiedX2(big, 2); math.Abs(got-48.0/11) > 1e-12 {
		t.Errorf("X² = %v, want 48/11", got)
	}
}

// A comparison's suite-level label result is the two-sided stratified
// label test: on the worked example of opposite shifts, where the two
// sides' weighted distributions are equal, it finds the difference, and
// as the primary metric it is tested alone.
func TestOppositeShiftsAreFound(t *testing.T) {
	m := labelMetric("kind", "a", "b")
	in := pairInput(m, map[string][]any{"t1": strs("a", "a", "a", "a"), "t2": strs("b", "b", "b", "b")},
		map[string][]any{"t1": strs("b", "b", "b", "b"), "t2": strs("a", "a", "a", "a")})
	in.LabelTests = true
	in.Parameters.MinTrialsForVerdict = 2
	c := mustCompare(t, in)
	s := find(c, m.Digest, LevelSuite, "")
	if s.Statistic != statPearsonX2Sum || s.P == nil || math.Abs(*s.P-4.0/4900) > 1e-12 || s.Verdict != Changed || *s.Q != *s.P {
		t.Fatalf("suite result %+v", s)
	}
	if s.Delta == nil || s.Delta.PerLabel["a"] != 0 || s.Delta.PerLabel["b"] != 0 {
		t.Errorf("the weighted distributions differ: %+v", s.Delta)
	}
}

// independenceInput is an experiment of candidates on tasks, the values of
// one label metric per candidate and task.
func independenceInput(m Metric, values ...map[string][]any) IndependenceInput {
	in := IndependenceInput{Parameters: Defaults(), Suite: Suite{Metrics: []Metric{m}, PrimaryMetric: m.Digest}, Metric: m.Digest, LabelTests: true}
	for i, byTask := range values {
		name := string(rune('a' + i))
		side := Side{Candidate: "sha256:" + name}
		for _, task := range sortedKeys(byTask) {
			side.Cells = append(side.Cells, cell(task, tuple("model-"+name, task), trialsOf(name+"-"+task, epoch, m, byTask[task]...), m))
		}
		in.Sides = append(in.Sides, side)
	}
	return in
}

// The experiment's test of independence over three candidates matches the
// worked example; it is tested alone, includes the tasks every candidate
// holds enough trials of, is insufficient below coverage, has no p-value
// before label tests are built, no verdict on self-reported values, states
// its power floor, and recomputes bit for bit with the seed scope
// experiment.
func TestIndependence(t *testing.T) {
	m := labelMetric("kind", "a", "b")
	three := func() IndependenceInput {
		return independenceInput(m,
			map[string][]any{"t1": strs("a", "a", "b"), "t2": strs("a", "a")},
			map[string][]any{"t1": strs("a", "b", "b"), "t2": strs("a", "b")},
			map[string][]any{"t1": strs("b", "b", "b"), "t2": strs("b", "b")})
	}
	r, err := Independence(three())
	if err != nil {
		t.Fatal(err)
	}
	if r.P == nil || math.Abs(*r.P-59.0/140) > 1e-12 || *r.Q != *r.P || r.Verdict != WithinNoise || !*r.Exact || *r.Resamples != 151200 {
		t.Fatalf("three candidates: %+v", r)
	}
	if r.Method != methodStratified || r.Statistic != statPearsonX2Sum || r.Tasks != 2 || r.Coverage != 1 || len(r.Summaries) != 3 {
		t.Errorf("the result's shape %+v", r)
	}
	if r.Reason != "p = 0.42 for the experiment's declared test of independence, tested alone without correction" {
		t.Errorf("reason %q", r.Reason)
	}
	again, err := Independence(three())
	if err != nil {
		t.Fatal(err)
	}
	b1, _ := json.Marshal(r, byteExact)
	b2, _ := json.Marshal(again, byteExact)
	if string(b1) != string(b2) {
		t.Error("the same input gave two results")
	}
	if want := ProcedureSeed(RootSeed(r.InputsDigest), m.Digest, "experiment", procTest).String(); r.Seed != want {
		t.Errorf("seed %s, want the scope experiment's %s", r.Seed, want)
	}

	// Separated candidates on enough tasks are changed.
	sep := independenceInput(m,
		map[string][]any{"t1": strs("a", "a", "a", "a", "a"), "t2": strs("a", "a", "a", "a", "a"), "t3": strs("a", "a", "a", "a", "a")},
		map[string][]any{"t1": strs("b", "b", "b", "b", "b"), "t2": strs("b", "b", "b", "b", "b"), "t3": strs("b", "b", "b", "b", "b")})
	if r, err := Independence(sep); err != nil || r.Verdict != Changed || *r.P > 0.05 {
		t.Errorf("separated: %+v %v", r, err)
	}

	// A task one candidate lacks or holds too few trials of is left out;
	// below coverage the result is insufficient and names them.
	short := independenceInput(m,
		map[string][]any{"t1": strs("a", "a", "a"), "t2": strs("a"), "t3": strs("a", "b")},
		map[string][]any{"t1": strs("b", "b", "b"), "t2": strs("b", "b")})
	r, err = Independence(short)
	if err != nil {
		t.Fatal(err)
	}
	if r.Verdict != Insufficient || r.P != nil || len(r.LeftOut) != 2 || r.LeftOut[0].Why != LeftOutTooFewTrials || r.LeftOut[1].Why != LeftOutUnpaired {
		t.Fatalf("short: %+v", r)
	}
	if want := "covers 33.3% of the suite's weight; 80% needed; left out: t2 (1 trial), t3 (not run by every candidate)"; r.Reason != want {
		t.Errorf("reason %q, want %q", r.Reason, want)
	}

	// Before label tests are built it carries its summaries alone.
	off := three()
	off.LabelTests = false
	if r, err := Independence(off); err != nil || r.P != nil || r.Verdict != "" || r.Reason != reasonLabelsNotBuilt || len(r.Summaries) != 3 {
		t.Errorf("before label tests: %+v %v", r, err)
	}

	// A self-reported value keeps its p and no verdict.
	self := three()
	self.Sides[0].Cells[0].Trials[0].Measurements[0].SelfReported = true
	if r, err := Independence(self); err != nil || r.P == nil || r.Q != nil || r.Verdict != "" || !strings.Contains(r.Reason, "reported itself") {
		t.Errorf("self-reported: %+v %v", r, err)
	}

	// Two candidates of two trials on two tasks cannot reach 5%: the
	// smallest p is 2/36.
	tiny := independenceInput(m,
		map[string][]any{"t1": strs("a", "a"), "t2": strs("a", "a")},
		map[string][]any{"t1": strs("b", "b"), "t2": strs("b", "b")})
	if r, err := Independence(tiny); err != nil || r.Reason != "with 2 tasks of 2 trials per candidate, no difference can be shown at 5%" || r.Verdict != WithinNoise {
		t.Errorf("tiny: %+v %v", r, err)
	}

	// A metric of another type, one candidate, and a metric outside the
	// suite are refused.
	bad := three()
	bad.Sides = bad.Sides[:1]
	if _, err := Independence(bad); err == nil {
		t.Error("one candidate")
	}
	bad = three()
	bad.Metric = "sha256:other"
	if _, err := Independence(bad); err == nil {
		t.Error("a metric outside the suite")
	}
	num := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	bad = three()
	bad.Suite.Metrics, bad.Metric = append(bad.Suite.Metrics, num), num.Digest
	if _, err := Independence(bad); err == nil {
		t.Error("a numeric metric")
	}

	// Missing values and excluded trials add their confounds and say so.
	gaps := three()
	gaps.Sides[1].Cells[0].Trials = append(gaps.Sides[1].Cells[0].Trials, Trial{ID: "b-t1-100", Outcome: OutcomeCompleted, StartedAt: epoch, EndedAt: epoch})
	gaps.Sides[2].Cells[1].Trials = append(gaps.Sides[2].Cells[1].Trials, Trial{ID: "c-t2-100", Outcome: OutcomeInfraFailed, StartedAt: epoch, EndedAt: epoch})
	r, err = Independence(gaps)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, c := range r.Confounds {
		names = append(names, c.Name)
	}
	if !slices.Equal(names, []string{confoundMissing, confoundIncomplete}) || !strings.HasSuffix(r.Reason, "; 1 trial has no value; 1 trial was left out: failed to run, canceled or over budget") {
		t.Errorf("confounds %v, reason %q", names, r.Reason)
	}
}
