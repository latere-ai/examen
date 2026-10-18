// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/work"
)

// figures is an analyzer that answers fixed figures: a summary for every
// cell, and for every comparison one suite-level result and one task-level
// result per task of the suite.
type figures struct{ fail bool }

func (f figures) Compare(ctx context.Context, c objects.Comparison, src compare.Source) (objects.Computed, []string, error) {
	if f.fail {
		return objects.Computed{}, nil, errors.New("the analysis broke")
	}
	cells, err := src.Experiments.AllCells(ctx, c.Experiment)
	if err != nil {
		return objects.Computed{}, nil, err
	}
	single := true
	out := objects.Computed{SingleVariable: &single, Confounds: []string{}, AnalysisVersion: "examen.analysis/v1"}
	metric := ""
	for _, cl := range cells {
		if cl.Candidate != c.Candidate {
			continue
		}
		v, err := src.Versions.ByID(ctx, objects.KindTask, cl.TaskID)
		if err != nil {
			return objects.Computed{}, nil, err
		}
		var task objects.Task
		if err := json.Unmarshal(v.Body, &task); err != nil {
			return objects.Computed{}, nil, err
		}
		e, err := src.Experiments.Get(ctx, c.Experiment)
		if err != nil {
			return objects.Computed{}, nil, err
		}
		sv, err := src.Versions.ByID(ctx, objects.KindSuite, e.SuiteID)
		if err != nil {
			return objects.Computed{}, nil, err
		}
		var su objects.Suite
		if err := json.Unmarshal(sv.Body, &su); err != nil {
			return objects.Computed{}, nil, err
		}
		metric = su.PrimaryMetric
		out.Results = append(out.Results, objects.ResultRow{Level: "task", Metric: metric, MetricName: "quality", Task: task.Name,
			Reference: json.RawMessage(`{"n":1}`), Verdict: "insufficient", Reason: "task " + task.Name + " has 1 trial; 5 needed"})
	}
	out.Results = append(out.Results, objects.ResultRow{Level: "suite", Metric: metric, MetricName: "quality", Verdict: "insufficient"})
	return out, []string{"msr_1"}, nil
}

func (figures) Summarize(context.Context, objects.Cell, string, compare.Source) (json.RawMessage, error) {
	return json.RawMessage(`{"aggregate":"mean","n":1}`), nil
}

func TestComparisons(t *testing.T) {
	x := newAPI(t, func(s *Server) {
		s.Analyzer = figures{}
		s.Engine.Analyzer = figures{}
	})
	f := x.fixture()
	eid := x.completeImport(f, f.taskB)
	// The automatic comparison of the low candidate against the first is
	// final once the experiment completed; the engine computes it.
	if err := x.srv.Engine.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	auto := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?automatic=true&final=true&exploratory=false", "t-alice", nil)["items"].([]any)
	if len(auto) != 1 {
		t.Fatalf("automatic %v", auto)
	}
	cmp := auto[0].(map[string]any)
	if cmp["state"] != "complete" || cmp["candidate"] != f.low || cmp["reference_candidate"] != f.high || len(cmp["results"].([]any)) != 1 {
		t.Fatalf("the automatic comparison %v", cmp)
	}
	if dp := cmp["declared_provenance"].([]any); len(dp) != 2 {
		t.Fatalf("both sides are external %v", dp)
	}
	cid := cmp["id"].(string)
	got := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid, "t-alice", nil)
	if got["results"].([]any)[0].(map[string]any)["level"] != "suite" {
		t.Fatalf("suite-level results alone %v", got)
	}
	rows := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid+"/rows?limit=1", "t-alice", nil)
	if len(rows["items"].([]any)) != 1 || rows["next_cursor"] == nil {
		t.Fatalf("rows %v", rows)
	}
	next := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid+"/rows?limit=1&cursor="+rows["next_cursor"].(string), "t-alice", nil)
	if len(next["items"].([]any)) != 1 || next["next_cursor"] != nil {
		t.Fatalf("the second page of rows %v", next)
	}
	for _, q := range []string{"?metric=quality", "?task=todo-app", "?verdict=insufficient"} {
		if got := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons/"+cid+"/rows"+q, "t-alice", nil); len(got["items"].([]any)) == 0 {
			t.Errorf("rows %s: %v", q, got)
		}
	}
	x.refuse(400, apierr.CodeInvalidCursor, http.MethodGet, "/v1/comparisons/"+cid+"/rows?cursor="+cursorFor("rows/"+cid, "x"), "t-alice", nil)
	x.refuse(404, apierr.CodeComparisonNotFound, http.MethodGet, "/v1/comparisons/"+cid, "t-bob", nil)
	x.refuse(404, apierr.CodeComparisonNotFound, http.MethodGet, "/v1/comparisons/"+id.New(id.Comparison)+"/rows", "t-alice", nil)
	for _, q := range []string{"", "?experiment=" + eid, "?suite=goalless", "?suite=" + f.suite, "?candidate=" + f.low, "?single_variable=true", "?owner=" + url.PathEscape(alice)} {
		if got := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons"+q, "t-alice", nil); len(got["items"].([]any)) != 1 {
			t.Errorf("comparisons %s: %v", q, got)
		}
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/comparisons", "t-bob", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("bob lists alice's comparisons")
	}
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/comparisons?final=maybe", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/comparisons?single_variable=maybe", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?automatic=maybe", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?exploratory=maybe", "t-alice", nil)

	// The grid reads the analysis's summaries and the column's verdicts.
	grid := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid?limit=1", "t-alice", nil)
	row := grid["items"].([]any)[0].(map[string]any)
	cells := row["cells"].([]any)
	if len(cells) != 2 || grid["next_cursor"] == nil {
		t.Fatalf("grid %v", grid)
	}
	low := cells[1].(map[string]any)
	if low["verdict"] != "insufficient" || low["comparison"] != cid || low["summary"] == nil || cells[0].(map[string]any)["verdict"] != nil {
		t.Fatalf("the grid's columns %v", cells)
	}
	page2 := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid?limit=1&cursor="+grid["next_cursor"].(string), "t-alice", nil)
	if items := page2["items"].([]any); len(items) != 1 || len(items[0].(map[string]any)["cells"].([]any)) != 2 || page2["next_cursor"] != nil {
		t.Fatalf("the second task %v", page2)
	}
	for _, q := range []string{"?metric=theme", "?candidate=" + f.low, "?task=todo-app", "?state=complete", "?comparison=" + cid} {
		x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid"+q, "t-alice", nil)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid?state=blocked", "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("no row holds a blocked cell")
	}
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/grid?metric=nope", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/grid?comparison=cmp_nope", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidCursor, http.MethodGet, "/v1/experiments/"+eid+"/grid?cursor="+cursorFor("grid/"+eid, "x"), "t-alice", nil)

	// A comparison on request is exploratory and computed once.
	made := x.must(http.StatusCreated, http.MethodPost, "/v1/comparisons", "t-alice", map[string]any{"experiment": eid, "candidate": f.high, "reference_candidate": f.low, "variable": "reasoning"})
	if made["automatic"] != false || made["exploratory"] != true || made["final"] != true || made["state"] != "complete" || made["variable"].([]any)[0] != "reasoning" {
		t.Fatalf("on request %v", made)
	}
	b := x.must(http.StatusCreated, http.MethodPut, "/v1/suites/goalless/baselines/main", "t-alice", map[string]any{"experiment": eid, "candidate": f.high})
	x.must(http.StatusCreated, http.MethodPost, "/v1/comparisons", "t-alice", map[string]any{"experiment": eid, "candidate": f.low, "baseline": b["id"]})
	across := x.must(http.StatusCreated, http.MethodPost, "/v1/comparisons", "t-alice", map[string]any{"experiment": eid, "candidate": f.high, "variable": "task", "task": "todo-app", "reference_task": "calculator"})
	if across["task"] != f.taskA || across["reference_task"] != f.taskB || across["variable"].([]any)[0] != "task_hash" {
		t.Fatalf("across tasks %v", across)
	}
}

func TestComparisonRequestRefusals(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.completeImport(f)
	ok := map[string]any{"experiment": eid, "candidate": f.low, "reference_candidate": f.high}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		maps.Copy(out, ok)
		for k, v := range extra {
			if v == nil {
				delete(out, k)
			} else {
				out[k] = v
			}
		}
		return out
	}
	cases := []struct {
		body   map[string]any
		status int
		code   string
	}{
		{with(map[string]any{"experiment": nil}), 400, apierr.CodeMissingField},
		{with(map[string]any{"candidate": nil}), 400, apierr.CodeMissingField},
		{with(map[string]any{"reference_candidate": nil}), 400, apierr.CodeInvalidField},
		{with(map[string]any{"baseline": "bas_x"}), 400, apierr.CodeInvalidField},
		{with(map[string]any{"reference_candidate": nil, "reference_task": "todo-app"}), 400, apierr.CodeInvalidField},
		{with(map[string]any{"variable": "task"}), 400, apierr.CodeInvalidField},
		{with(map[string]any{"reference_candidate": nil, "variable": "task", "task": "todo-app", "reference_task": "todo-app"}), 400, apierr.CodeInvalidField},
		{with(map[string]any{"variable": "colour"}), 400, apierr.CodeInvalidField},
		{with(map[string]any{"experiment": id.New(id.Experiment)}), 404, apierr.CodeExperimentNotFound},
		{with(map[string]any{"candidate": "harness-low"}), 422, apierr.CodeUnpinnedReference},
		{with(map[string]any{"candidate": "sha256:" + strings.Repeat("9", 64)}), 422, apierr.CodeNotInExperiment},
		{with(map[string]any{"reference_candidate": "harness-high"}), 422, apierr.CodeUnpinnedReference},
		{with(map[string]any{"reference_candidate": "sha256:" + strings.Repeat("9", 64)}), 422, apierr.CodeNotInExperiment},
		{with(map[string]any{"reference_candidate": nil, "baseline": id.New(id.Baseline)}), 422, apierr.CodeReferenceNotFound},
		{with(map[string]any{"reference_candidate": nil, "variable": "task", "task": "todo-app", "reference_task": "calculator"}), 422, apierr.CodeNotInExperiment},
		{with(map[string]any{"reference_candidate": nil, "variable": "task", "task": "nope", "reference_task": "todo-app"}), 422, apierr.CodeNotInExperiment},
	}
	for _, c := range cases {
		x.refuse(c.status, c.code, http.MethodPost, "/v1/comparisons", "t-alice", c.body)
	}
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/comparisons", "t-bob", ok)
	other := x.apply("suites", "other", map[string]any{"tasks": []string{f.taskA}, "metrics": []string{f.quality}, "primary_metric": f.quality})
	oeid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": other, "candidates": []string{f.high}, "trials": 1, "budget": map[string]any{"name": "s"}, "mode": "import",
	})["id"].(string)
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+oeid+"/imports", "t-alice", pastRun(f.taskA, f.high, "o-1", nil))
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+oeid+"/close", "t-alice", nil)
	b := x.must(http.StatusCreated, http.MethodPut, "/v1/suites/other/baselines/main", "t-alice", map[string]any{"experiment": oeid, "candidate": f.high})
	x.refuse(422, apierr.CodeSuiteMismatch, http.MethodPost, "/v1/comparisons", "t-alice", with(map[string]any{"reference_candidate": nil, "baseline": b["id"]}))
}

// A failing analysis marks the comparison failed.
func TestAFailingAnalysis(t *testing.T) {
	x := newAPI(t, func(s *Server) { s.Engine.Analyzer = figures{fail: true} })
	f := x.fixture()
	eid := x.completeImport(f)
	if err := x.srv.Engine.Compute(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons", "t-alice", nil)["items"].([]any)
	if got[0].(map[string]any)["state"] != "failed" {
		t.Fatalf("failed %v", got)
	}
	if !strings.Contains(x.log(), "a comparison failed") {
		t.Fatal("the failure is not logged")
	}
	_ = work.ReasonRunDenied
}
