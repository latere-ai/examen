// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

// A create's declared comparisons are refused, each with its code, when a
// member is missing, a reference is named twice or not at all, a candidate
// is compared with itself, a pair is declared twice in either orientation,
// a variable does not fit, a task or a candidate is not the experiment's,
// or there are more than MAX_DECLARED_COMPARISONS; a test of independence
// is not accepted before label tests are built.
func TestDeclaredComparisonsRefused(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	create := func(comparisons any) map[string]any {
		return map[string]any{"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 2, "budget": map[string]any{"name": "study"},
			"mode": "import", "comparisons": comparisons}
	}
	pair := func(c, r string) map[string]any { return map[string]any{"candidate": c, "reference_candidate": r} }
	tasks := func(c, a, b string) map[string]any {
		return map[string]any{"candidate": c, "task": a, "reference_task": b}
	}
	other := "sha256:" + strings.Repeat("e", 64)
	many := make([]any, limits.MaxDeclaredComparisons+1)
	for i := range many {
		many[i] = pair(f.low, f.high)
	}
	for _, c := range []struct {
		name   string
		body   any
		status int
		code   string
	}{
		{"no candidate", []any{map[string]any{}}, 400, apierr.CodeMissingField},
		{"no reference", []any{map[string]any{"candidate": f.high}}, 400, apierr.CodeInvalidField},
		{"two references", []any{map[string]any{"candidate": f.high, "reference_candidate": f.low, "task": "todo-app", "reference_task": "calculator"}}, 400, apierr.CodeInvalidField},
		{"a candidate against itself", []any{pair(f.high, f.high)}, 400, apierr.CodeInvalidField},
		{"a pair in both orientations", []any{pair(f.high, f.low), pair(f.low, f.high)}, 400, apierr.CodeInvalidField},
		{"a candidate not in the experiment", []any{pair(other, f.high)}, 422, apierr.CodeNotInExperiment},
		{"a reference not in the experiment", []any{pair(f.high, other)}, 422, apierr.CodeNotInExperiment},
		{"an unknown variable", []any{map[string]any{"candidate": f.high, "reference_candidate": f.low, "variable": "colour"}}, 400, apierr.CodeInvalidField},
		{"task on a pair of candidates", []any{map[string]any{"candidate": f.high, "reference_candidate": f.low, "variable": "task"}}, 400, apierr.CodeInvalidField},
		{"one task", []any{map[string]any{"candidate": f.high, "task": "todo-app"}}, 400, apierr.CodeMissingField},
		{"a model on a pair of tasks", []any{map[string]any{"candidate": f.high, "task": "todo-app", "reference_task": "calculator", "variable": "model"}}, 400, apierr.CodeInvalidField},
		{"a task the suite does not pin", []any{tasks(f.high, "nope", "calculator")}, 422, apierr.CodeNotInExperiment},
		{"one task twice", []any{tasks(f.high, "todo-app", "todo-app")}, 400, apierr.CodeInvalidField},
		{"a pair of tasks in both orientations", []any{tasks(f.high, "todo-app", "calculator"), tasks(f.high, f.taskB, f.taskA)}, 400, apierr.CodeInvalidField},
		{"too many", many, 422, apierr.CodeLimitExceeded},
	} {
		details := x.refuse(c.status, c.code, http.MethodPost, "/v1/experiments", "t-alice", create(c.body))
		if field, _ := details["field"].(string); c.code != apierr.CodeLimitExceeded && !strings.HasPrefix(field, "comparisons[") {
			t.Errorf("%s: the refusal names %q", c.name, field)
		}
	}
	body := create([]any{pair(f.low, f.high)})
	body["independence"] = map[string]any{"metric": f.theme}
	x.refuse(400, apierr.CodeUnknownField, http.MethodPost, "/v1/experiments", "t-alice", body)
}

// An experiment's declared comparisons are made with it, the automatic
// pair's own record carrying its declaration, and computed as the
// automatic comparison is; the declared analysis answers every entry
// before any is computed, and at completion corrects the primary results
// together over the three declared comparisons, while each comparison
// keeps its primary result's p alone.
func TestDeclaredComparisons(t *testing.T) {
	x := analyzed(t)
	f := x.fixture()
	suite := x.apply("suites", "declared", map[string]any{
		"tasks": []string{f.taskA, f.taskB}, "metrics": []string{"builtin:declined", "builtin:completed", "builtin:duration"}, "primary_metric": "builtin:declined",
	})
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": suite, "candidates": []string{f.high, f.low}, "trials": 6, "budget": map[string]any{"name": "study"}, "mode": "import",
		"comparisons": []any{
			map[string]any{"candidate": f.low, "reference_candidate": f.high},
			map[string]any{"candidate": f.high, "task": "todo-app", "reference_task": "calculator"},
			map[string]any{"candidate": f.low, "task": f.taskA, "reference_task": "calculator"},
		},
	})
	eid := created["id"].(string)
	declared := created["comparisons"].([]any)
	if len(declared) != 3 || declared[1].(map[string]any)["task"] != f.taskA || declared[2].(map[string]any)["reference_task"] != f.taskB {
		t.Fatalf("the declarations %v", declared)
	}
	list := func(q string) []map[string]any {
		t.Helper()
		var out []map[string]any
		for _, it := range x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?"+q, "t-alice", nil)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		slices.SortFunc(out, func(a, b map[string]any) int { return int(a["declared"].(float64) - b["declared"].(float64)) })
		return out
	}
	kept := list("declared=true")
	if len(kept) != 3 || kept[0]["automatic"] != true || kept[1]["automatic"] != false || kept[2]["automatic"] != false {
		t.Fatalf("declared comparisons %v", kept)
	}
	for _, c := range kept {
		if c["exploratory"] != false {
			t.Errorf("a declared comparison is exploratory: %v", c)
		}
	}
	if auto := list("automatic=true"); len(auto) != 1 || auto[0]["id"] != kept[0]["id"] {
		t.Fatalf("the automatic comparison %v", auto)
	}
	if len(x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?declared=false", "t-alice", nil)["items"].([]any)) != 0 {
		t.Error("a comparison that is not declared")
	}

	read := func() map[string]any {
		t.Helper()
		return x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/declared", "t-alice", nil)
	}
	pending := read()
	fam := pending["family"].(map[string]any)
	if fam["size"] != 3.0 || fam["method"] != "benjamini_hochberg" || fam["level"] != 0.05 || pending["final"] != false || len(pending["comparisons"].([]any)) != 3 {
		t.Fatalf("before any computation %v", pending)
	}
	for _, e := range pending["comparisons"].([]any) {
		if e.(map[string]any)["reason"] != "not computed yet: the candidate has no finished trial" {
			t.Errorf("entry %v", e)
		}
	}

	// On calculator the reference declines three runs in six and the
	// candidate none; on todo-app neither declines.
	six := func(declines int) []pastTrial {
		var out []pastTrial
		for i := range 6 {
			o := "completed"
			if i < declines {
				o = "declined"
			}
			out = append(out, pastTrial{o, 30, nil})
		}
		return out
	}
	x.importRuns(eid, f.taskB, f.high, "high/calculator", six(3))
	x.importRuns(eid, f.taskB, f.low, "low/calculator", six(0))
	x.importRuns(eid, f.taskA, f.high, "high/todo", six(0))
	x.importRuns(eid, f.taskA, f.low, "low/todo", six(0))
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-alice", nil)
	if err := x.srv.Engine.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	final := read()
	if final["final"] != true || final["computed_at"] == nil {
		t.Fatalf("at completion %v", final)
	}
	var ps []float64
	entries := final["comparisons"].([]any)
	for i, raw := range entries {
		e := raw.(map[string]any)
		if e["comparison"] != kept[i]["id"] || e["metric"] != "builtin:declined" || e["final"] != true || e["p"] == nil || e["q"] == nil {
			t.Fatalf("entry %d %v", i, e)
		}
		if !strings.HasSuffix(e["reason"].(string), "after correcting the primary results of 3 declared comparisons at 5%") {
			t.Errorf("entry %d reason %q", i, e["reason"])
		}
		ps = append(ps, e["p"].(float64))
	}
	// The q values are Benjamini and Hochberg's over the three p-values.
	order := []int{0, 1, 2}
	slices.SortStableFunc(order, func(a, b int) int {
		switch {
		case ps[a] < ps[b]:
			return -1
		case ps[a] > ps[b]:
			return 1
		}
		return 0
	})
	running := 1.0
	want := make([]float64, 3)
	for rank := 3; rank >= 1; rank-- {
		i := order[rank-1]
		running = math.Min(running, math.Min(1, 3*ps[i]/float64(rank)))
		want[i] = running
	}
	for i, raw := range entries {
		if q := raw.(map[string]any)["q"].(float64); math.Abs(q-want[i]) > 1e-12 {
			t.Errorf("entry %d q %v, want %v from p %v", i, q, want[i], ps)
		}
	}
	// The low candidate's two tasks hold the same rate: no difference.
	if e := entries[2].(map[string]any); e["verdict"] != "within_noise" || e["p"] != 1.0 {
		t.Errorf("low across tasks %v", e)
	}

	// Each comparison keeps its primary result's p, and neither q nor a
	// verdict: the automatic pair's at suite level, a pair of tasks' at
	// task level.
	auto := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+kept[0]["id"].(string), "t-alice", nil)
	var suiteRows []resultRow
	decodeInto(t, auto["results"], &suiteRows)
	primary := findRow(t, suiteRows, "declined", "suite", "")
	if primary.P == nil || primary.Q != nil || primary.Verdict != "" || !strings.Contains(primary.Reason, "of a declared comparison") {
		t.Errorf("the automatic pair's primary result %+v", primary)
	}
	if c := auto["correction"].(map[string]any); c["declared"] != 3.0 || c["primary"] != "builtin:declined" {
		t.Errorf("correction %v", c)
	}
	var rows []resultRow
	decodeInto(t, x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+kept[1]["id"].(string)+"/rows?limit=200", "t-alice", nil)["items"], &rows)
	across := findRow(t, rows, "declined", "task", "todo-app vs calculator")
	if across.P == nil || across.Q != nil || across.Verdict != "" {
		t.Errorf("a pair of tasks' primary result %+v", across)
	}
	if other := findRow(t, rows, "completed", "task", "todo-app vs calculator"); other.Q == nil || other.Verdict == "" {
		t.Errorf("a declared comparison's other result is in its own family: %+v", other)
	}
}
