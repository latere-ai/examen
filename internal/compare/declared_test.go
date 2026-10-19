// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// storedDeclared is a stored declared comparison of a primary result row.
func storedDeclared(t *testing.T, id string, index int, final, fresh bool, rows ...objects.ResultRow) objects.Comparison {
	t.Helper()
	at := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	c := objects.Comparison{ID: id, DeclaredIndex: &index, State: objects.ComparisonComplete, Final: final, UpdatedAt: at}
	computed := at
	if !fresh {
		computed = at.Add(-time.Minute)
	}
	c.ComputedAt = &computed
	c.Parameters = json.RawMessage(`{"false_discovery_rate":0.1}`)
	c.Results = rows
	return c
}

// The declared analysis reads each declared comparison's newest record:
// the primary metric's suite-level result of a pair of candidates and the
// task-level result of a pair of tasks, a per-label delta, a self-reported
// value, an insufficient result, a failed record and one not computed;
// it corrects them at the level the comparisons recorded, and counts a
// final record marked again since its last computation as not final.
func TestDeclared(t *testing.T) {
	w := newWorld(t)
	e := objects.Experiment{ID: "exp_new", Owner: "alice", SuiteID: "sui_new", Comparisons: make([]objects.DeclaredComparison, 6)}
	e.Comparisons[1] = objects.DeclaredComparison{Candidate: candNew, Task: taskA, ReferenceTask: taskB}
	f := func(v float64) *float64 { return &v }
	pair := storedDeclared(t, "cmp_0", 0, true, true, objects.ResultRow{Level: "suite", Metric: score, Direction: "higher_is_better", P: f(0.01), Delta: json.RawMessage(`0.3`), Confounds: json.RawMessage(`[]`)})
	tasks := storedDeclared(t, "cmp_1", 1, true, true,
		objects.ResultRow{Level: "suite", Metric: score, P: f(0.9)},
		objects.ResultRow{Level: "task", Metric: score, Direction: "none", P: f(0.02), Delta: json.RawMessage(`{"a":0,"b":0}`)})
	tasks.ReferenceTask = taskB
	self := storedDeclared(t, "cmp_2", 2, true, false, objects.ResultRow{Level: "suite", Metric: score, P: f(0.001), Confounds: json.RawMessage(`[{"name":"self_reported"}]`)})
	short := storedDeclared(t, "cmp_3", 3, true, true, objects.ResultRow{Level: "suite", Metric: score, Verdict: "insufficient", Reason: "covers 50% of the suite's weight; 80% needed"})
	failed := storedDeclared(t, "cmp_4", 4, true, true)
	failed.State = objects.ComparisonFailed
	a, err := Analysis{}.Declared(t.Context(), e, []objects.Comparison{pair, tasks, self, short, failed}, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if a.Experiment != "exp_new" || a.Family.Size != 6 || a.Family.Level != 0.1 || a.Final || len(a.Comparisons) != 6 {
		t.Fatalf("analysis %+v", a)
	}
	got := a.Comparisons
	// p of 0.01 and 0.02 among six, four counted as 1: q = 0.06 for both.
	for _, i := range []int{0, 1} {
		if got[i].Q == nil || math.Abs(*got[i].Q-0.06) > 1e-12 || got[i].Verdict != "improved" && got[i].Verdict != "changed" || !got[i].Final {
			t.Errorf("entry %d %+v", i, got[i])
		}
	}
	if got[1].Verdict != "changed" || got[1].Metric != score {
		t.Errorf("a pair of tasks with a zero per-label delta %+v", got[1])
	}
	if got[2].P == nil || got[2].Q != nil || got[2].Verdict != "" || got[2].Final {
		t.Errorf("a self-reported entry, marked again since computed %+v", got[2])
	}
	if got[3].Verdict != "insufficient" || got[3].Q != nil || got[3].Reason != "covers 50% of the suite's weight; 80% needed" {
		t.Errorf("an insufficient entry %+v", got[3])
	}
	if got[4].Comparison != "cmp_4" || got[4].P != nil || got[4].Reason != "not computed: the comparison could not be computed" {
		t.Errorf("a failed entry %+v", got[4])
	}
	if got[5].Comparison != "" || got[5].Reason != "not computed yet: the candidate has no finished trial" {
		t.Errorf("an entry not computed %+v", got[5])
	}

	pending, err := Pending(t.Context(), e, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Family.Size != 6 || pending.Final || pending.Comparisons[0].Metric != score || pending.Family.Level != 0.05 || pending.Independence != nil {
		t.Errorf("pending %+v", pending)
	}

	// A delta or confounds that do not read are errors, and so is a suite
	// that cannot be read.
	broken := storedDeclared(t, "cmp_0", 0, true, true, objects.ResultRow{Level: "suite", Metric: score, P: f(0.01), Delta: json.RawMessage(`"x"`)})
	if _, err := (Analysis{}).Declared(t.Context(), e, []objects.Comparison{broken}, w.src); err == nil {
		t.Error("a delta that does not read")
	}
	broken = storedDeclared(t, "cmp_0", 0, true, true, objects.ResultRow{Level: "suite", Metric: score, P: f(0.01), Confounds: json.RawMessage(`{`)})
	if _, err := (Analysis{}).Declared(t.Context(), e, []objects.Comparison{broken}, w.src); err == nil {
		t.Error("confounds that do not read")
	}
	gone := e
	gone.SuiteID = "sui_gone"
	if _, err := (Analysis{}).Declared(t.Context(), gone, nil, w.src); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a suite that cannot be read: %v", err)
	}
	if _, ok := recordedLevel(json.RawMessage(`{}`)); ok {
		t.Error("a level from parameters that name none")
	}
}

// A declared comparison across tasks waits, while it is not final, for
// both its cells; a declared comparison carries D and is not exploratory.
func TestDeclaredInput(t *testing.T) {
	w := newWorld(t)
	e := w.x.exps["exp_new"]
	e.Comparisons = make([]objects.DeclaredComparison, 4)
	w.x.exps["exp_new"] = e
	index := 2
	c := objects.Comparison{ID: "cmp_d", Experiment: "exp_new", Candidate: candNew, ReferenceCandidate: candOld, DeclaredIndex: &index, Exploratory: true}
	in, err := Input(t.Context(), c, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if in.Declared != 4 || in.Exploratory {
		t.Errorf("declared %d exploratory %v", in.Declared, in.Exploratory)
	}
	across := objects.Comparison{ID: "cmp_t", Experiment: "exp_new", Candidate: candOld, Task: taskA, ReferenceTask: taskB, DeclaredIndex: &index}
	if _, _, err := (Analysis{}).Compare(t.Context(), across, w.src); !errors.Is(err, ErrNotComputed) {
		t.Errorf("a declared comparison across tasks without its second cell: %v", err)
	}
	across.Final = true
	if _, _, err := (Analysis{}).Compare(t.Context(), across, w.src); err == nil || errors.Is(err, ErrNotComputed) {
		t.Errorf("a final one fails: %v", err)
	}
}
