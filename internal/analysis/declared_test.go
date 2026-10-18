// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// A declared comparison's primary result is in no comparison's family: it
// carries its p and no q or verdict, draws PERMUTATION_RESAMPLES resolved
// with m = D, and reads its power floor at FALSE_DISCOVERY_RATE / D. Its
// other results are its own family. A declared comparison across tasks is
// not exploratory, and its primary result is the primary metric's one
// task-level result. The inputs document carries D, and one that is not
// declared is the document it was.
func TestDeclaredComparison(t *testing.T) {
	pass := binaryMetric("pass", HigherIsBetter)
	cost := numericMetric("cost", TypeCost, AggMean, LowerIsBetter)
	mk := func() Input {
		cand := map[string][]any{"t1": bools(6, 7), "t2": bools(6, 7)}
		ref := map[string][]any{"t1": bools(1, 7), "t2": bools(1, 7)}
		in := pairInput(pass, cand, ref)
		in.Suite.Metrics = append(in.Suite.Metrics, cost)
		for i := range in.Candidate.Cells {
			in.Candidate.Cells[i].Trials = merge(in.Candidate.Cells[i].Trials, trialsOf("c-"+in.Candidate.Cells[i].Task, epoch, cost, nums(1, 2, 1, 2, 1, 2, 1)...))
			in.Candidate.Cells[i].Producers[cost.Digest] = grader
			in.Reference.Cells[i].Trials = merge(in.Reference.Cells[i].Trials, trialsOf("r-"+in.Reference.Cells[i].Task, epoch, cost, nums(2, 3, 2, 3, 2, 3, 2)...))
			in.Reference.Cells[i].Producers[cost.Digest] = grader
		}
		in.Parameters = smallParameters()
		return in
	}
	plain := mustCompare(t, mk())
	in := mk()
	in.Declared = 33
	c := mustCompare(t, in)
	s := find(c, pass.Digest, LevelSuite, "")
	if s.P == nil || s.Q != nil || s.Verdict != "" || !strings.Contains(s.Reason, "corrected with the primary results of 33 declared comparisons") {
		t.Fatalf("declared primary %+v", s)
	}
	if c.Exploratory || c.Correction.Primary != pass.Digest || c.Correction.Declared != 33 || c.Correction.Family != plain.Correction.Family {
		t.Errorf("correction %+v, exploratory %v; the comparison's own family was %d", c.Correction, c.Exploratory, plain.Correction.Family)
	}
	if c.Resamples.Primary != 13200 || plain.Resamples.Primary != 10000 {
		t.Errorf("the primary result's B: declared %d, alone %d", c.Resamples.Primary, plain.Resamples.Primary)
	}
	if t1 := find(c, pass.Digest, LevelTask, "t1"); t1.Q == nil || t1.Verdict == "" {
		t.Errorf("a task-level result of the declared comparison is in its own family: %+v", t1)
	}
	// The inputs documents: one that is not declared names no D, so its
	// digest is what it was before declared comparisons existed.
	docPlain, _, err := InputsDocument(mk())
	if err != nil {
		t.Fatal(err)
	}
	docDeclared, _, err := InputsDocument(in)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(docPlain, []byte(`"declared"`)) || !bytes.Contains(docDeclared, []byte(`"declared":33`)) {
		t.Errorf("documents %s / %s", docPlain[:120], docDeclared[:120])
	}

	// Across tasks, declared: not exploratory, the primary result is the
	// task-level result of the primary metric, with its p alone.
	x := cell("build", tuple("model-a", "build"), trialsOf("x", epoch, pass, bools(5, 5)...), pass)
	y := cell("do", tuple("model-a", "do"), trialsOf("y", epoch, pass, bools(0, 5)...), pass)
	across := Input{Parameters: smallParameters(), Suite: Suite{Metrics: []Metric{pass}, PrimaryMetric: pass.Digest},
		Candidate: Side{Candidate: "sha256:c", Cells: []Cell{x}}, Reference: Side{Candidate: "sha256:c", Cells: []Cell{y}}, AcrossTasks: true, Declared: 11}
	a := mustCompare(t, across)
	r := find(a, pass.Digest, LevelTask, "build vs do")
	if a.Exploratory || a.Correction.Family != 0 || r.P == nil || r.Q != nil || r.Verdict != "" {
		t.Fatalf("declared across tasks: exploratory %v correction %+v result %+v", a.Exploratory, a.Correction, r)
	}
	// Boschloo's smallest p for 5 against 5 is 0.0019531: below
	// 0.05 / 11 = 0.0045, so at D = 11 the floor does not bind, and above
	// 0.05 / 26 = 0.0019231, so at D = 26 the reason says no difference
	// can be shown.
	if !strings.Contains(r.Reason, "of a declared comparison") {
		t.Errorf("reason at D = 11: %q", r.Reason)
	}
	across.Declared = 26
	r = find(mustCompare(t, across), pass.Digest, LevelTask, "build vs do")
	if r.Reason != "with 5 trials per side, no difference can be shown among 26 declared comparisons" {
		t.Errorf("reason at D = 26: %q", r.Reason)
	}
	across.Declared = -1
	if _, err := Compare(across); err == nil {
		t.Error("a negative declared family")
	}
}

// The declared family corrects every declared comparison's primary p-value
// together over D, a missing one counted as 1: the worked example of spec
// 008 gives q = 0.04, 0.0533, none and 0.0533 at D = 4, one finding at
// 5%. An insufficient result keeps its verdict and no q, a self-reported
// one its p and no verdict, one not computed says so, and the same
// primaries give the same family.
func TestDeclaredFamily(t *testing.T) {
	up := &Delta{Value: 0.4}
	f := func(v float64) *float64 { return &v }
	primaries := []DeclaredPrimary{
		{Comparison: "cmp_1", Metric: "sha256:m", Direction: HigherIsBetter, P: f(0.01), Delta: up, Reason: "own", Final: true},
		{Comparison: "cmp_2", Metric: "sha256:m", Direction: HigherIsBetter, P: f(0.04), Delta: up, Final: true},
		{Comparison: "cmp_3", Metric: "sha256:m", Direction: HigherIsBetter, Verdict: Insufficient, Reason: "task t3 has 3 trials with a value; 5 needed", Final: true},
		{Comparison: "cmp_4", Metric: "sha256:m", Direction: HigherIsBetter, P: f(0.03), Delta: up, Final: true},
	}
	fam := CorrectDeclared(primaries, 0.05)
	want := []struct {
		q       float64
		verdict Verdict
	}{{0.04, Improved}, {0.16 / 3, WithinNoise}, {-1, Insufficient}, {0.16 / 3, WithinNoise}}
	for i, w := range want {
		e := fam.Comparisons[i]
		if w.q < 0 {
			if e.Q != nil || e.Verdict != w.verdict || e.Reason != primaries[i].Reason {
				t.Errorf("entry %d: %+v", i, e)
			}
			continue
		}
		if e.Q == nil || math.Abs(*e.Q-w.q) > 1e-12 || e.Verdict != w.verdict {
			t.Errorf("entry %d: %+v, want q %v %s", i, e, w.q, w.verdict)
		}
	}
	if fam.Size != 4 || fam.Method != "benjamini_hochberg" || fam.Level != 0.05 || !fam.Final {
		t.Errorf("family %+v", fam)
	}
	if r := fam.Comparisons[0].Reason; r != "p = 0.01; q = 0.04 after correcting the primary results of 4 declared comparisons at 5%" {
		t.Errorf("reason %q", r)
	}
	// Over the three p-values alone every one would pass: counting the
	// insufficient one is what holds the family to its declared size.
	if q := benjaminiHochberg([]float64{0.01, 0.04, 0.03}); q[0] != 0.03 || q[1] > 0.05 || q[2] > 0.05 {
		t.Errorf("three alone %v", q)
	}

	self := append([]DeclaredPrimary{}, primaries...)
	self[0].SelfReported = true
	self[1] = DeclaredPrimary{Metric: "sha256:m", Direction: HigherIsBetter}
	fam = CorrectDeclared(self, 0.05)
	if e := fam.Comparisons[0]; e.P == nil || e.Q != nil || e.Verdict != "" {
		t.Errorf("self-reported %+v", e)
	}
	if e := fam.Comparisons[1]; e.Comparison != "" || e.Reason != "not computed yet: the candidate has no finished trial" || e.Final || fam.Final {
		t.Errorf("not computed %+v, final %v", e, fam.Final)
	}
	a, _ := json.Marshal(CorrectDeclared(primaries, 0.05))
	b, _ := json.Marshal(CorrectDeclared(primaries, 0.05))
	if !bytes.Equal(a, b) {
		t.Error("the same primaries gave two families")
	}
}
