// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"testing"
)

// stratumCell is a cell of a task named by the stratum it is in.
func stratumCell(stratum, task string, m Metric, values ...string) Cell {
	c := cell(task, tuple("model-a", task), trialsOf(task, epoch, m, strs(values...)...), m)
	c.Task = stratum
	return c
}

// generalInput is a declared comparison of the general form, object build
// against do stratified by volition, over the worked example of the
// stratified label test with two sides: t1 a, a, a, b against b, b, b, a,
// and t2 a, a, b against a, b, b.
func generalInput(m Metric) Input {
	return Input{
		Parameters: Defaults(),
		Suite: Suite{Metrics: []Metric{m}, PrimaryMetric: m.Digest, Tasks: []SuiteTask{
			{Name: "volition=none", Digest: "sha256:build"}, {Name: "volition=want", Digest: "sha256:build-want"},
		}},
		Candidate: Side{Candidate: "sha256:cand", Cells: []Cell{
			stratumCell("volition=none", "build", m, "a", "a", "a", "b"),
			stratumCell("volition=want", "build-want", m, "a", "a", "b"),
		}},
		Reference: Side{Candidate: "sha256:cand", Cells: []Cell{
			stratumCell("volition=none", "do", m, "b", "b", "b", "a"),
			stratumCell("volition=want", "do-want", m, "a", "b", "b"),
		}},
		Variable: "task", Declared: 3, LabelTests: true,
		Declaration: &Declaration{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{}, By: []string{"volition"}, Metric: m.Digest},
	}
}

// A declared comparison of the general form pairs its two levels' cells by
// stratum: two strata whose sides hold different tasks give the stratified
// label test of the worked example, p = 752/1,400, as its primary result,
// corrected in the declared family and so with no q or verdict here; its
// variable is the task, the only tuple field that differs; and its
// declaration is in its inputs document.
func TestGeneralDeclaration(t *testing.T) {
	m := labelMetric("kind", "a", "b")
	in := generalInput(m)
	c := mustCompare(t, in)
	s := find(c, m.Digest, LevelSuite, "")
	if s == nil || s.Method != methodStratified || s.Statistic != statPearsonX2Sum || !*s.Exact || *s.Resamples != 1400 || math.Abs(*s.P-752.0/1400) > 1e-12 {
		t.Fatalf("the primary result %+v", s)
	}
	if s.Q != nil || s.Verdict != "" || *s.Candidate.Tasks != 2 || *s.Candidate.Coverage != 1 {
		t.Fatalf("a declared primary result %+v", s)
	}
	for _, name := range []string{"volition=none", "volition=want"} {
		r := find(c, m.Digest, LevelTask, name)
		// Four and three trials a side: each stratum alone is below
		// MIN_TRIALS_FOR_VERDICT, and the stratified test reads both.
		if r == nil || r.Verdict != Insufficient || r.TaskDigests == nil || r.TaskDigests.Candidate == r.TaskDigests.Reference {
			t.Fatalf("the stratum %s: %+v", name, r)
		}
	}
	if r := find(c, m.Digest, LevelTask, "volition=none"); r.TaskDigests.Candidate != "sha256:build" || r.TaskDigests.Reference != "sha256:do" {
		t.Fatalf("the stratum's tasks %+v", r.TaskDigests)
	}
	if !slices.Equal(c.Variable, []string{FieldTaskHash}) || !c.SingleVariable || c.Exploratory || c.Correction.Primary != m.Digest || c.Correction.Declared != 3 {
		t.Fatalf("the comparison %v %v %v %+v", c.Variable, c.SingleVariable, c.Exploratory, c.Correction)
	}
	doc, _, err := InputsDocument(in)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(doc, []byte(`"declaration":{"by":["volition"],"levels":["build","do"],"metric":"sha256:kind","variable":"object","where":{}}`)) {
		t.Fatalf("the inputs document lacks the declaration: %s", doc)
	}
	legacy, _, err := InputsDocument(pairInput(m, map[string][]any{"t1": strs("a")}, map[string][]any{"t1": strs("b")}))
	if err != nil || bytes.Contains(legacy, []byte(`"declaration"`)) {
		t.Fatalf("a comparison of no declaration carries one: %v", err)
	}
}

// Without strata a declaration of the general form is one pair of cells:
// one task-level result per metric, named by its stratum, which is the
// primary result, and no suite-level result; two cells on a side are no
// such input.
func TestGeneralDeclarationWithoutStrata(t *testing.T) {
	m := labelMetric("kind", "a", "b")
	in := generalInput(m)
	in.Declaration.By = []string{}
	in.Suite.Tasks = []SuiteTask{{Name: "build vs do", Digest: "sha256:build"}}
	in.Candidate.Cells = []Cell{stratumCell("build vs do", "build", m, "a", "a", "a", "a", "a")}
	in.Reference.Cells = []Cell{stratumCell("build vs do", "do", m, "b", "b", "b", "b", "b")}
	c := mustCompare(t, in)
	if find(c, m.Digest, LevelSuite, "") != nil {
		t.Fatal("a declaration without strata has a suite-level result")
	}
	r := find(c, m.Digest, LevelTask, "build vs do")
	// Five a against five b: the observed table and its mirror are the
	// two of the 252 least likely, so p = 2/252.
	if r == nil || r.P == nil || math.Abs(*r.P-2.0/252) > 1e-12 || r.Q != nil || r.Verdict != "" || c.Correction.Family != 0 {
		t.Fatalf("the primary result %+v, family %d", r, c.Correction.Family)
	}
	in.Candidate.Cells = append(in.Candidate.Cells, stratumCell("other", "other", m, "a"))
	if _, err := Compare(in); !errors.Is(err, ErrInput) {
		t.Fatalf("two cells on a side: %v", err)
	}
}

// The variable task is a declaration's alone: a comparison of no
// declaration that names it is refused.
func TestTaskVariableNeedsADeclaration(t *testing.T) {
	m := labelMetric("kind", "a", "b")
	in := generalInput(m)
	in.Declaration = nil
	if _, err := Compare(in); !errors.Is(err, ErrVariable) {
		t.Fatalf("the variable task without a declaration: %v", err)
	}
}
