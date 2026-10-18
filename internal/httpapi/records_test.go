// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
)

// pastRun is the body of an external harness's past run.
func pastRun(task, candidate, ref string, extra map[string]any) map[string]any {
	start := time.Date(2026, 3, 2, 14, 1, 0, 0, time.UTC)
	body := map[string]any{
		"executor": "external", "task": task, "candidate": candidate, "outcome": "completed",
		"started_at": start, "agent_ended_at": start.Add(492 * time.Second), "ended_at": start.Add(510 * time.Second),
		"declared": map[string]string{"harness_version": "2.1.4", "image": "sandbox-agent:2026-02-27"},
		"outputs":  []map[string]any{{"path": "notes.md", "arca": map[string]string{"path": "files/runs/exp07/run-03/notes.md", "digest": "sha256:" + strings.Repeat("a", 64)}}},
		"ref":      ref,
	}
	maps.Copy(body, extra)
	return body
}

// completeImport is a complete experiment in mode import with one run of
// each candidate on the first task.
func (x *client) completeImport(f fixture, tasks ...string) string {
	x.t.Helper()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 3, "budget": map[string]any{"name": "study"}, "mode": "import",
	})["id"].(string)
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", pastRun(f.taskA, f.high, "run-1", nil))
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", pastRun(f.taskA, f.low, "run-2", nil))
	for i, task := range tasks {
		for j, c := range []string{f.high, f.low} {
			x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", pastRun(task, c, "more-"+itoa(i)+"-"+itoa(j), map[string]any{"outputs": nil}))
		}
	}
	closed := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-alice", nil)
	if closed["state"] != "complete" {
		x.t.Fatalf("closed %v", closed)
	}
	return eid
}

func TestBaselines(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.completeImport(f)
	pin := map[string]any{"experiment": eid, "candidate": f.high}
	first := x.do(http.MethodPut, "/v1/suites/goalless/baselines/main", "t-alice", pin, "If-None-Match", "*")
	if first.status != http.StatusCreated || !strings.HasPrefix(first.header.Get("Location"), "/v1/baselines/bas_") {
		t.Fatalf("pin %d %s", first.status, first.body)
	}
	b := first.json(t)
	again := x.must(http.StatusOK, http.MethodPut, "/v1/suites/goalless/baselines/main", "t-alice", pin)
	if again["id"] != b["id"] {
		t.Fatalf("a repeat made another record %v", again)
	}
	x.refuse(http.StatusPreconditionFailed, apierr.CodePreconditionFailed, http.MethodPut, "/v1/suites/goalless/baselines/main", "t-alice", map[string]any{"experiment": eid, "candidate": f.low}, "If-Match", `"bas_other"`)
	moved := x.must(http.StatusCreated, http.MethodPut, "/v1/suites/goalless/baselines/main", "t-alice", map[string]any{"experiment": eid, "candidate": f.low}, "If-Match", `"`+b["id"].(string)+`"`)
	if moved["previous"] != b["id"] {
		t.Fatalf("moved %v", moved)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/baselines/main", "t-alice", nil); got["id"] != moved["id"] {
		t.Fatalf("current %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/baselines/main/history", "t-alice", nil); len(got["items"].([]any)) != 2 {
		t.Fatalf("history %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/baselines", "t-alice", nil); len(got["items"].([]any)) != 1 {
		t.Fatalf("list %v", got)
	}
	x.must(http.StatusOK, http.MethodGet, "/v1/baselines/"+b["id"].(string), "t-alice", nil)
	x.refuse(http.StatusNotFound, apierr.CodeBaselineNotFound, http.MethodGet, "/v1/baselines/"+b["id"].(string), "t-bob", nil)
	x.refuse(http.StatusNotFound, apierr.CodeBaselineNotFound, http.MethodGet, "/v1/baselines/"+id.New(id.Baseline), "t-alice", nil)
	x.refuse(http.StatusNotFound, apierr.CodeBaselineNotFound, http.MethodGet, "/v1/suites/goalless/baselines/nope", "t-alice", nil)
	x.refuse(http.StatusNotFound, apierr.CodeBaselineNotFound, http.MethodGet, "/v1/suites/goalless/baselines/nope/history", "t-alice", nil)

	// An experiment that names the baseline as its reference.
	ref := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 1, "budget": map[string]any{"name": "study"}, "mode": "import",
		"reference": map[string]any{"baseline": moved["id"]},
	})
	if ref["reference"].(map[string]any)["baseline"] != moved["id"] {
		t.Fatalf("reference %v", ref)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments?baseline="+moved["id"].(string), "t-alice", nil); len(got["items"].([]any)) != 1 {
		t.Fatalf("experiments of the baseline %v", got)
	}
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "reference": map[string]any{"baseline": id.New(id.Baseline)},
	})

	// The refusals of a pin: an experiment another owner may not read is
	// reference_not_found whatever its state; one not complete, of another
	// suite, or without the candidate.
	running := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "mode": "import",
	})["id"].(string)
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-bob", pin)
	x.refuse(404, apierr.CodeSuiteNotFound, http.MethodPut, "/v1/suites/other/baselines/x", "t-alice", pin)
	x.refuse(409, apierr.CodeExperimentNotComplete, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", map[string]any{"experiment": running, "candidate": f.high})
	x.refuse(422, apierr.CodeNotInExperiment, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", map[string]any{"experiment": eid, "candidate": "sha256:" + strings.Repeat("9", 64)})
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", map[string]any{"experiment": id.New(id.Experiment), "candidate": f.high})
	x.apply("suites", "other", map[string]any{"tasks": []string{f.taskA}, "metrics": []string{f.quality}, "primary_metric": f.quality})
	x.refuse(422, apierr.CodeSuiteMismatch, http.MethodPut, "/v1/suites/other/baselines/x", "t-alice", pin)
	x.refuse(400, apierr.CodeMissingField, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", map[string]any{"candidate": f.high})
	x.refuse(400, apierr.CodeMissingField, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", map[string]any{"experiment": eid})
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/suites/goalless/baselines/Bad", "t-alice", pin)
	x.refuse(422, apierr.CodeUnpinnedReference, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", map[string]any{"experiment": eid, "candidate": "harness-high"})
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/suites/goalless/baselines/x", "t-alice", pin, "If-None-Match", "x")
	x.refuse(422, apierr.CodeSuiteMismatch, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite":      "other@" + x.must(http.StatusOK, http.MethodGet, "/v1/suites/other", "t-alice", nil)["digest"].(string),
		"candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "reference": map[string]any{"baseline": b["id"]},
	})
}

func TestTaxonomies(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 3, "budget": map[string]any{"name": "study"}, "mode": "import",
	})["id"].(string)
	for i, label := range []string{"Fixes Bugs", "fixes  bugs", "Adds tests"} {
		x.must(http.StatusCreated, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", pastRun(f.taskA, f.high, "run-"+itoa(i), map[string]any{
			"measurements": []map[string]any{{"metric": "theme", "value": label}},
		}))
	}
	unmapped := x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/taxonomies/theme/unmapped", "t-alice", nil)
	items := unmapped["items"].([]any)
	if len(items) != 2 || items[0].(map[string]any)["label"] != "fixes bugs" || items[0].(map[string]any)["count"] != 2.0 {
		t.Fatalf("unmapped %v", unmapped)
	}
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodGet, "/v1/suites/goalless/taxonomies/theme", "t-alice", nil)
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodGet, "/v1/suites/goalless/taxonomies/theme/history", "t-alice", nil)
	body := map[string]any{"canonical": []string{"repair"}, "map": map[string]string{"Fixes Bugs": "repair"}}
	first := x.do(http.MethodPut, "/v1/suites/goalless/taxonomies/theme", "t-alice", body, "If-None-Match", "*")
	if first.status != http.StatusCreated {
		t.Fatalf("append %d %s", first.status, first.body)
	}
	tax := first.json(t)
	if tax["version"] != 1.0 || tax["map"].(map[string]any)["fixes bugs"] != "repair" {
		t.Fatalf("first record %v", tax)
	}
	if again := x.must(http.StatusOK, http.MethodPut, "/v1/suites/goalless/taxonomies/"+f.theme, "t-alice", body); again["id"] != tax["id"] {
		t.Fatalf("a repeat %v", again)
	}
	next := x.must(http.StatusCreated, http.MethodPut, "/v1/suites/goalless/taxonomies/theme", "t-alice",
		map[string]any{"canonical": []string{"repair", "testing"}, "map": map[string]string{"fixes bugs": "repair", "adds tests": "testing"}},
		"If-Match", `"`+tax["id"].(string)+`"`)
	if next["version"] != 2.0 || next["previous"] != tax["id"] {
		t.Fatalf("second record %v", next)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/taxonomies/theme/unmapped", "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatalf("all mapped %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/taxonomies/theme/history", "t-alice", nil); len(got["items"].([]any)) != 2 {
		t.Fatalf("history %v", got)
	}
	x.must(http.StatusOK, http.MethodGet, "/v1/suites/goalless/taxonomies/theme", "t-alice", nil)
	x.must(http.StatusOK, http.MethodGet, "/v1/taxonomies/"+tax["id"].(string), "t-alice", nil)
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodGet, "/v1/taxonomies/"+tax["id"].(string), "t-bob", nil)
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodGet, "/v1/taxonomies/"+id.New(id.Taxonomy), "t-alice", nil)
	cases := []struct {
		status int
		code   string
		body   map[string]any
		header []string
	}{
		{412, apierr.CodePreconditionFailed, map[string]any{"canonical": []string{"repair", "testing", "docs"}, "map": map[string]string{"fixes bugs": "repair", "adds tests": "testing"}}, []string{"If-Match", `"tax_old"`}},
		{400, apierr.CodeInvalidField, map[string]any{"canonical": []string{"repair"}, "map": map[string]string{"fixes bugs": "repair"}}, nil},
		{400, apierr.CodeInvalidField, map[string]any{"canonical": []string{"a", "a"}, "map": map[string]string{}}, nil},
		{400, apierr.CodeInvalidField, map[string]any{"canonical": []string{"repair", "testing"}, "map": map[string]string{"fixes bugs": "nope", "adds tests": "testing"}}, nil},
		{400, apierr.CodeInvalidField, map[string]any{"canonical": []string{"repair", "testing"}, "map": map[string]string{"fixes bugs": "repair", "adds tests": "testing", "   ": "repair"}}, nil},
		{400, apierr.CodeInvalidField, map[string]any{"canonical": []string{"repair", "testing"}, "map": map[string]string{"fixes bugs": "repair", "adds tests": "testing", "Adds Tests": "repair"}}, nil},
	}
	for _, c := range cases {
		x.refuse(c.status, c.code, http.MethodPut, "/v1/suites/goalless/taxonomies/theme", "t-alice", c.body, c.header...)
	}
	big := make([]string, 4097)
	for i := range big {
		big[i] = "l" + itoa(i)
	}
	x.refuse(422, apierr.CodeLimitExceeded, http.MethodPut, "/v1/suites/goalless/taxonomies/theme", "t-alice", map[string]any{"canonical": big, "map": map[string]string{}})
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodPut, "/v1/suites/goalless/taxonomies/quality", "t-alice", body)
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodGet, "/v1/suites/nope/taxonomies/theme", "t-alice", nil)
	x.refuse(404, apierr.CodeTaxonomyNotFound, http.MethodGet, "/v1/suites/goalless/taxonomies/theme", "t-bob", nil)
}
