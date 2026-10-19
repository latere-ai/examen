// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/apierr"
)

// design is a suite of four tasks in a 2 by 2 design of object and
// volition, a closed label metric, and three candidates.
type design struct {
	kind, suite    string
	high, low, mid string
}

func (x *client) design() design {
	x.t.Helper()
	var d design
	d.kind = x.apply("metrics", "kind", map[string]any{"type": "label", "labels": []string{"a", "b"}, "direction": "none", "aggregate": "distribution"})
	var tasks []string
	for _, tk := range []struct{ name, object, volition string }{
		{"build", "build", "none"}, {"build-want", "build", "want"}, {"do", "do", "none"}, {"do-want", "do", "want"},
	} {
		tasks = append(tasks, x.apply("tasks", tk.name, map[string]any{"instruction": "Go.", "factors": map[string]string{"object": tk.object, "volition": tk.volition}}))
	}
	d.suite = x.apply("suites", "design", map[string]any{"tasks": tasks, "metrics": []string{d.kind, "builtin:completed"}, "primary_metric": d.kind})
	cand := func(name, model string) string {
		return x.apply("candidates", name, map[string]any{"kind": "external", "external": map[string]any{"harness": "goalless", "version": "2.1.4", "model": model}})
	}
	d.high, d.low, d.mid = cand("high", "m-high"), cand("low", "m-low"), cand("mid", "m-mid")
	return d
}

// labelRuns imports one completed run per label of a candidate on a task,
// each reporting the label for kind.
func (x *client) labelRuns(eid, task, candidate, labels string) {
	x.t.Helper()
	start := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	for i, l := range labels {
		at := start.Add(time.Duration(i) * time.Minute)
		x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", map[string]any{
			"executor": "external", "task": task, "candidate": candidate, "outcome": "completed",
			"started_at": at, "agent_ended_at": at.Add(time.Minute), "ended_at": at.Add(time.Minute),
			"declared":     map[string]string{"harness_version": "2.1.4", "image": "sandbox-agent:2026-02-27"},
			"ref":          "runs/" + candidate[len(candidate)-6:] + "/" + task[len(task)-6:] + "/" + itoa(i),
			"measurements": []map[string]any{{"metric": "kind", "value": string(l)}},
		})
	}
}

// A task's factors are refused at apply under a name Examen reserves; a
// declaration of the general form, a declared metric and a test of
// independence are refused at create, each naming the member at fault.
func TestGeneralDeclarationsRefused(t *testing.T) {
	x := newAPI(t, nil)
	d := x.design()
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/tasks/reserved", "t-alice", map[string]any{"factors": map[string]string{"model": "m"}})
	create := func(comparisons []any, independence any) map[string]any {
		body := map[string]any{"suite": d.suite, "candidates": []string{d.high, d.low}, "trials": 2, "budget": map[string]any{"name": "study"}, "mode": "import"}
		if comparisons != nil {
			body["comparisons"] = comparisons
		}
		if independence != nil {
			body["independence"] = independence
		}
		return body
	}
	general := func(variable string, levels []string, where map[string]string, by []string) map[string]any {
		g := map[string]any{"variable": variable, "levels": levels}
		if where != nil {
			g["where"] = where
		}
		if by != nil {
			g["by"] = by
		}
		return g
	}
	for _, c := range []struct {
		name         string
		comparisons  []any
		independence any
		status       int
		code, field  string
	}{
		{"two forms", []any{map[string]any{"candidate": d.high, "variable": "object", "levels": []string{"build", "do"}}}, nil, 400, apierr.CodeInvalidField, "comparisons[0]"},
		{"no variable", []any{map[string]any{"levels": []string{"build", "do"}}}, nil, 400, apierr.CodeMissingField, "comparisons[0].variable"},
		{"by on a pair", []any{map[string]any{"candidate": d.high, "reference_candidate": d.low, "by": []string{"task"}}}, nil, 400, apierr.CodeInvalidField, "comparisons[0]"},
		{"one level", []any{general("object", []string{"build"}, nil, nil)}, nil, 400, apierr.CodeInvalidField, "comparisons[0].levels"},
		{"a variable no cell carries", []any{general("tone", []string{"a", "b"}, nil, nil)}, nil, 400, apierr.CodeInvalidField, "comparisons[0].variable"},
		{"a level no cell holds", []any{general("object", []string{"build", "make"}, nil, nil)}, nil, 422, apierr.CodeNotInExperiment, "comparisons[0].levels[1]"},
		{"two cells of one side", []any{general("object", []string{"build", "do"}, map[string]string{"candidate": d.high}, nil)}, nil, 400, apierr.CodeInvalidField, "comparisons[0].by"},
		{"declared twice", []any{
			general("object", []string{"build", "do"}, map[string]string{"candidate": d.high}, []string{"volition"}),
			general("object", []string{"do", "build"}, map[string]string{"candidate": d.high}, []string{"volition"}),
		}, nil, 400, apierr.CodeInvalidField, "comparisons[1]"},
		{"a metric the suite does not list", []any{map[string]any{"candidate": d.high, "reference_candidate": d.low, "metric": "quality"}}, nil, 400, apierr.CodeInvalidField, "comparisons[0].metric"},
		{"a pair twice, its metric by name and by digest", []any{
			map[string]any{"candidate": d.low, "reference_candidate": d.high, "metric": "completed"},
			map[string]any{"candidate": d.high, "reference_candidate": d.low, "metric": "builtin:completed"},
		}, nil, 400, apierr.CodeInvalidField, "comparisons[1]"},
		{"a pair twice, in the pair form and the general one", []any{
			map[string]any{"candidate": d.high, "reference_candidate": d.low},
			general("candidate", []string{d.low, d.high}, nil, []string{"task"}),
		}, nil, 400, apierr.CodeInvalidField, "comparisons[1]"},
		{"a pair of tasks twice, in the task form and the general one", []any{
			map[string]any{"candidate": d.high, "task": "build", "reference_task": "do"},
			general("task", []string{"do", "build"}, map[string]string{"candidate": d.high}, nil),
		}, nil, 400, apierr.CodeInvalidField, "comparisons[1]"},
		{"strata named in another order", []any{
			general("volition", []string{"none", "want"}, nil, []string{"candidate", "object"}),
			general("volition", []string{"none", "want"}, nil, []string{"object", "candidate"}),
		}, nil, 400, apierr.CodeInvalidField, "comparisons[1]"},
		{"no metric of independence", nil, map[string]any{"variable": "candidate"}, 400, apierr.CodeMissingField, "independence.metric"},
		{"a metric that is no label", nil, map[string]any{"metric": "builtin:completed"}, 400, apierr.CodeInvalidField, "independence.metric"},
		{"a variable of one level", nil, map[string]any{"metric": d.kind, "where": map[string]string{"model": "m-high"}}, 400, apierr.CodeInvalidField, "independence.variable"},
		{"a stratum of two cells", nil, map[string]any{"metric": d.kind, "by": []string{"volition"}}, 400, apierr.CodeInvalidField, "independence.by"},
		{"no stratum of two levels", nil, map[string]any{"metric": d.kind, "variable": "object", "by": []string{"task"}}, 400, apierr.CodeInvalidField, "independence.by"},
	} {
		details := x.refuse(c.status, c.code, http.MethodPost, "/v1/experiments", "t-alice", create(c.comparisons, c.independence))
		if field, _ := details["field"].(string); field != c.field {
			t.Errorf("%s: the refusal names %q, want %q", c.name, field, c.field)
		}
	}
	// The automatic pair declared with a metric of its own is a record of
	// its own, beside the automatic comparison, its metric by digest.
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", create([]any{map[string]any{"candidate": d.low, "reference_candidate": d.high, "metric": "completed"}}, nil))
	if m := created["comparisons"].([]any)[0].(map[string]any)["metric"]; m != "builtin:completed" {
		t.Fatalf("the declared metric %v", m)
	}
	items := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+created["id"].(string)+"/comparisons", "t-alice", nil)["items"].([]any)
	automatic := 0
	for _, it := range items {
		c := it.(map[string]any)
		if c["automatic"] == true {
			automatic++
			if c["declared"] != nil {
				t.Errorf("the automatic comparison took a declaration with its own metric: %v", c)
			}
		}
	}
	if len(items) != 2 || automatic != 1 {
		t.Fatalf("comparisons %v", items)
	}
}

// Declared comparisons of the general form and a test of independence
// through the store and the work queue: the create answers them resolved,
// a declaration's comparison carries it and names its results by stratum,
// with object build against do within one candidate stratified by volition
// reproducing the worked example's p of 752/1,400; and the test of
// independence of kind against the candidate, stratified by task, over
// the tasks of no volition reproduces the worked example's p of
// 63,720/151,200. The runs report their labels themselves, so each p
// stands with no verdict.
func TestGeneralDeclarationsAndIndependence(t *testing.T) {
	x := analyzed(t)
	d := x.design()
	tasks := map[string]string{}
	for _, it := range x.must(http.StatusOK, http.MethodGet, "/v1/tasks?suite="+d.suite, "t-alice", nil)["items"].([]any) {
		tk := it.(map[string]any)
		tasks[tk["name"].(string)] = tk["digest"].(string)
	}
	run := func(eid string) {
		x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-alice", nil)
		if err := x.srv.Engine.Tick(t.Context()); err != nil {
			t.Fatal(err)
		}
	}

	// Object build against do within the high candidate, by volition.
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": d.suite, "candidates": []string{d.high}, "trials": 4, "budget": map[string]any{"name": "study"}, "mode": "import",
		"comparisons": []any{map[string]any{"variable": "object", "levels": []string{"build", "do"}, "where": map[string]string{"candidate": "high@" + d.high}, "by": []string{"volition"}}},
	})
	eid := created["id"].(string)
	decl := created["comparisons"].([]any)[0].(map[string]any)
	if decl["where"].(map[string]any)["candidate"] != d.high || decl["variable"] != "object" || created["independence"] != nil {
		t.Fatalf("the declaration as stored %v", created)
	}
	x.labelRuns(eid, tasks["build"], d.high, "aaab")
	x.labelRuns(eid, tasks["do"], d.high, "bbba")
	x.labelRuns(eid, tasks["build-want"], d.high, "aab")
	x.labelRuns(eid, tasks["do-want"], d.high, "abb")
	run(eid)
	var kept []map[string]any
	for _, it := range x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?declared=true", "t-alice", nil)["items"].([]any) {
		kept = append(kept, it.(map[string]any))
	}
	if len(kept) != 1 || kept[0]["candidate"] != d.high || kept[0]["declaration"] == nil || kept[0]["reference_candidate"] != nil || kept[0]["state"] != "complete" {
		t.Fatalf("the declared comparison %v", kept)
	}
	type row struct {
		Level      string   `json:"level"`
		Task       string   `json:"task"`
		MetricName string   `json:"metric_name"`
		Verdict    string   `json:"verdict"`
		P          *float64 `json:"p"`
		Q          *float64 `json:"q"`
	}
	// A comparison answers its suite-level results, and its rows the
	// task-level ones.
	var rows, suite []row
	decodeInto(t, x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+kept[0]["id"].(string), "t-alice", nil)["results"], &suite)
	decodeInto(t, x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+kept[0]["id"].(string)+"/rows?limit=200", "t-alice", nil)["items"], &rows)
	rows = append(rows, suite...)
	var names []string
	var primary *float64
	for _, r := range rows {
		if r.Level == "task" && !slices.Contains(names, r.Task) {
			names = append(names, r.Task)
		}
		if r.Level == "suite" && r.MetricName == "kind" {
			primary = r.P
			if r.Q != nil || r.Verdict != "" {
				t.Errorf("a declared, self-reported primary result %+v", r)
			}
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"volition=none", "volition=want"}) || primary == nil || math.Abs(*primary-752.0/1400) > 1e-12 {
		t.Fatalf("the strata %v, the primary p %v", names, primary)
	}
	entry := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/declared", "t-alice", nil)["comparisons"].([]any)[0].(map[string]any)
	if entry["metric"] != d.kind || entry["p"] != *primary || entry["q"] != nil {
		t.Fatalf("the declared entry %v", entry)
	}

	// The test of independence over three candidates.
	created = x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": d.suite, "candidates": []string{d.high, d.low, d.mid}, "trials": 4, "budget": map[string]any{"name": "study"}, "mode": "import",
		"independence": map[string]any{"metric": "kind", "where": map[string]string{"volition": "none"}},
	})
	eid = created["id"].(string)
	ind := created["independence"].(map[string]any)
	if ind["metric"] != d.kind || ind["variable"] != "candidate" || ind["by"].([]any)[0] != "task" {
		t.Fatalf("the test as stored %v", ind)
	}
	pending := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/declared", "t-alice", nil)
	if p := pending["independence"].(map[string]any); p["reason"] != "not computed yet: no cell has a finished trial" || len(p["levels"].([]any)) != 3 || pending["final"] != false {
		t.Fatalf("pending %v", pending)
	}
	x.labelRuns(eid, tasks["build"], d.high, "aab")
	x.labelRuns(eid, tasks["do"], d.high, "aa")
	x.labelRuns(eid, tasks["build"], d.low, "abb")
	x.labelRuns(eid, tasks["do"], d.low, "ab")
	x.labelRuns(eid, tasks["build"], d.mid, "bbb")
	x.labelRuns(eid, tasks["do"], d.mid, "bb")
	run(eid)
	declared := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/declared", "t-alice", nil)
	got := declared["independence"].(map[string]any)
	if p, _ := got["p"].(float64); math.Abs(p-63720.0/151200) > 1e-12 || got["method"] != "stratified_permutation" || got["verdict"] != nil {
		t.Fatalf("the test of independence %v", got)
	}
	if !strings.Contains(got["reason"].(string), "reported itself") || got["tasks"] != 2.0 || declared["family"].(map[string]any)["size"] != 0.0 {
		t.Fatalf("the test's reason and strata %v", declared)
	}
}
