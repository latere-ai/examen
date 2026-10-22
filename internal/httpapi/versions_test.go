// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
)

func TestApplyAndRead(t *testing.T) {
	x := newAPI(t, nil)
	body := map[string]any{"type": "binary", "direction": "higher_is_better", "aggregate": "rate"}
	first := x.do(http.MethodPut, "/v1/metrics/pass", "t-alice", body)
	if first.status != http.StatusCreated || first.header.Get("ETag") == "" || !strings.HasPrefix(first.header.Get("Location"), "/v1/metrics/pass@sha256:") {
		t.Fatalf("first apply %d %v %s", first.status, first.header, first.body)
	}
	v1 := first.json(t)
	again := x.must(http.StatusOK, http.MethodPut, "/v1/metrics/pass", "t-alice", body)
	if again["digest"] != v1["digest"] || again["id"] != v1["id"] {
		t.Fatalf("a repeat made another version: %v", again)
	}
	// A body that spells the same content another way is the same version.
	x.must(http.StatusOK, http.MethodPut, "/v1/metrics/pass", "t-alice", `{"aggregate":"rate","direction":"higher_is_better","type":"binary","name":"pass"}`)
	changed := x.must(http.StatusCreated, http.MethodPut, "/v1/metrics/pass", "t-alice", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate"})
	if changed["digest"] == v1["digest"] || changed["current"] != true {
		t.Fatalf("changed %v", changed)
	}
	// The earlier version reads unchanged and not current; applying it
	// again makes it current without a new version.
	old := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/pass@"+v1["digest"].(string), "t-alice", nil)
	if old["current"] != false || old["direction"] != "higher_is_better" {
		t.Fatalf("old %v", old)
	}
	x.must(http.StatusOK, http.MethodPut, "/v1/metrics/pass", "t-alice", body)
	// One version reads equal by name, name@digest, digest and id.
	byName := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/pass", "t-alice", nil)
	for _, ref := range []string{"pass@" + v1["digest"].(string), v1["digest"].(string), v1["id"].(string)} {
		got := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/"+ref, "t-alice", nil)
		if got["id"] != byName["id"] || got["current"] != true {
			t.Fatalf("%s reads %v, the name %v", ref, got, byName)
		}
	}
	history := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/pass/versions?limit=1", "t-alice", nil)
	if len(history["items"].([]any)) != 1 || history["next_cursor"] == nil {
		t.Fatalf("history %v", history)
	}
	next := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/pass/versions?limit=1&cursor="+history["next_cursor"].(string), "t-alice", nil)
	if len(next["items"].([]any)) != 1 || next["next_cursor"] != nil {
		t.Fatalf("history's second page %v", next)
	}
	list := x.must(http.StatusOK, http.MethodGet, "/v1/metrics?type=binary", "t-alice", nil)
	if len(list["items"].([]any)) != 1 {
		t.Fatalf("list %v", list)
	}
	all := x.must(http.StatusOK, http.MethodGet, "/v1/metrics?versions=all", "t-alice", nil)
	if len(all["items"].([]any)) != 2 {
		t.Fatalf("every version %v", all)
	}
	// Another subject sees none of it, by name, digest or id, and lists
	// none.
	for _, ref := range []string{"pass", v1["digest"].(string), v1["id"].(string), "pass/versions"} {
		x.refuse(http.StatusNotFound, apierr.CodeMetricNotFound, http.MethodGet, "/v1/metrics/"+ref, "t-bob", nil)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/metrics", "t-bob", nil); len(got["items"].([]any)) != 0 {
		t.Fatalf("bob lists %v", got)
	}
	x.refuse(http.StatusNotFound, apierr.CodeMetricNotFound, http.MethodGet, "/v1/metrics/pass@"+changed["digest"].(string)[:20]+"x", "t-alice", nil)
	x.refuse(http.StatusNotFound, apierr.CodeMetricNotFound, http.MethodGet, "/v1/metrics/other@"+v1["digest"].(string), "t-alice", nil)
	x.refuse(http.StatusNotFound, apierr.CodeMetricNotFound, http.MethodGet, "/v1/metrics/"+id.New(id.Metric), "t-alice", nil)
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodGet, "/v1/metrics?versions=some", "t-alice", nil)
}

func TestApplyPreconditionsAndRefusals(t *testing.T) {
	x := newAPI(t, nil)
	body := map[string]any{"type": "binary", "direction": "higher_is_better", "aggregate": "rate"}
	x.refuse(http.StatusPreconditionFailed, apierr.CodePreconditionFailed, http.MethodPut, "/v1/metrics/pass", "t-alice", body, "If-Match", `"sha256:00"`)
	v := x.must(http.StatusCreated, http.MethodPut, "/v1/metrics/pass", "t-alice", body, "If-None-Match", "*")
	x.refuse(http.StatusPreconditionFailed, apierr.CodePreconditionFailed, http.MethodPut, "/v1/metrics/pass", "t-alice", body, "If-None-Match", "*")
	x.must(http.StatusCreated, http.MethodPut, "/v1/metrics/pass", "t-alice", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate"},
		"If-Match", `"`+v["digest"].(string)+`"`)
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodPut, "/v1/metrics/pass", "t-alice", body, "If-None-Match", "abc")
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodPut, "/v1/metrics/pass", "t-alice", body, "If-Match", "unquoted")
	cases := []struct {
		path   string
		body   any
		status int
		code   string
		field  string
	}{
		{"/v1/metrics/pass", map[string]any{"name": "other", "type": "binary", "direction": "none", "aggregate": "rate"}, 400, apierr.CodeInvalidField, "name"},
		{"/v1/metrics/Pass", body, 400, apierr.CodeInvalidField, "name"},
		{"/v1/suites/" + id.New(id.Suite), map[string]any{}, 400, apierr.CodeInvalidField, "name"},
		{"/v1/metrics/pass", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate", "colour": "red"}, 400, apierr.CodeUnknownField, "colour"},
		{"/v1/metrics/pass", `[1]`, 400, apierr.CodeInvalidRequest, ""},
		{"/v1/metrics/pass", `{"type": 1}`, 400, apierr.CodeInvalidRequest, "type"},
		{"/v1/metrics/cells", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "none", "level": "cell", "of": "sha256:" + strings.Repeat("a", 64), "aggregate": "repeat_rate"}, 422, apierr.CodeReferenceNotFound, "of"},
		{"/v1/metrics/cells", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "none", "level": "cell", "of": "theme", "aggregate": "repeat_rate"}, 422, apierr.CodeUnpinnedReference, "of"},
		{"/v1/graders/judge", map[string]any{"kind": "check", "produces": []string{"sha256:" + strings.Repeat("b", 64)}, "check": map[string]any{"command": []string{"true"}}}, 422, apierr.CodeReferenceNotFound, "produces[0]"},
		{"/v1/graders/judge", map[string]any{"kind": "check", "produces": []string{"pass"}, "check": map[string]any{"command": []string{"true"}}}, 422, apierr.CodeUnpinnedReference, "produces[0]"},
		{"/v1/tasks/t", map[string]any{"checks": []string{"sha256:" + strings.Repeat("c", 64)}}, 422, apierr.CodeReferenceNotFound, "checks[0]"},
		{"/v1/tasks/t", map[string]any{"checks": []string{"tests"}}, 422, apierr.CodeUnpinnedReference, "checks[0]"},
		{"/v1/tasks/t", map[string]any{"outputs": []string{"../x"}}, 400, apierr.CodeInvalidField, "outputs[0]"},
		{"/v1/suites/s", map[string]any{"tasks": []string{"x"}, "metrics": []string{"builtin:completed"}, "primary_metric": "builtin:completed"}, 422, apierr.CodeUnpinnedReference, "tasks[0]"},
		{"/v1/candidates/c", map[string]any{"kind": "program", "program": map[string]any{"image": "x@sha256:" + strings.Repeat("d", 64), "credentials": []any{map[string]any{"audience": "lux", "host": "h", "env": "T"}}}}, 422, apierr.CodeCredentialAudienceReserved, "program.credentials[0].audience"},
		{"/v1/candidates/c", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "coder", "model": "m"}}, 422, apierr.CodeUnpinnedReference, "topos.agent"},
	}
	for _, c := range cases {
		d := x.refuse(c.status, c.code, http.MethodPut, c.path, "t-alice", c.body)
		if c.field != "" && d["field"] != c.field {
			t.Errorf("%s: field %v, want %s", c.path, d["field"], c.field)
		}
	}
	// A suite's references are held to what they pin.
	metric := x.apply("metrics", "quality", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "higher_is_better", "aggregate": "mean"})
	task := x.apply("tasks", "one", map[string]any{"instruction": "do it"})
	same := x.apply("tasks", "two", map[string]any{"instruction": "do it"})
	_ = same
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{"builtin:nope"}, "primary_metric": "builtin:nope"})
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{"sha256:" + strings.Repeat("e", 64)}, "primary_metric": "sha256:" + strings.Repeat("e", 64)})
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{metric}, "primary_metric": metric, "graders": []string{"sha256:" + strings.Repeat("f", 64)}})
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{metric}, "primary_metric": metric, "canonicalizers": []string{"sha256:" + strings.Repeat("f", 64)}})
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{metric}, "primary_metric": metric, "decline_judge": "sha256:" + strings.Repeat("f", 64)})
	check := x.apply("graders", "tests", map[string]any{"kind": "check", "produces": []string{metric}, "check": map[string]any{"command": []string{"make", "test"}}})
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{metric}, "primary_metric": metric, "graders": []string{check}})
	judge := x.apply("graders", "judge", map[string]any{"kind": "model_judge", "produces": []string{metric}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "prompt": "rate it", "inputs": map[string]any{"final_message": true}}})
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/tasks/three", "t-alice", map[string]any{"checks": []string{judge}})
	withCheck := x.apply("tasks", "four", map[string]any{"checks": []string{check}})
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/suites/s", "t-alice", map[string]any{"tasks": []string{withCheck}, "metrics": []string{metric}, "primary_metric": metric, "graders": []string{judge}})
	x.apply("suites", "s", map[string]any{"tasks": []string{withCheck, task}, "metrics": []string{metric}, "primary_metric": metric})
	theme := x.apply("metrics", "theme", map[string]any{"type": "label", "vocabulary": "open", "direction": "none", "aggregate": "distribution"})
	repeat := x.apply("metrics", "repeat", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "none", "level": "cell", "of": theme, "aggregate": "repeat_rate"})
	x.refuse(400, apierr.CodeInvalidField, http.MethodPut, "/v1/suites/r", "t-alice", map[string]any{"tasks": []string{task}, "metrics": []string{metric, repeat}, "primary_metric": metric})
}

func TestListsAndFilters(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	tasks := x.must(http.StatusOK, http.MethodGet, "/v1/tasks?suite="+f.suite, "t-alice", nil)
	if len(tasks["items"].([]any)) != 2 {
		t.Fatalf("tasks of the suite %v", tasks)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/tasks?suite=sha256:"+strings.Repeat("0", 64), "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatalf("tasks of a suite that is none %v", got)
	}
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodGet, "/v1/tasks?suite=goalless", "t-alice", nil)
	x.apply("tasks", "labelled", map[string]any{"metadata": map[string]string{"area": "build"},
		"workspace":   map[string]any{"kind": "origo", "repository": "acme/app", "commit": strings.Repeat("a", 40)},
		"environment": map[string]any{"image": "ghcr.io/acme/env@sha256:" + strings.Repeat("b", 64)}})
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/tasks?metadata.area=build", "t-alice", nil); len(got["items"].([]any)) != 1 {
		t.Fatalf("by metadata %v", got)
	}
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodGet, "/v1/tasks?metadata.area=build&metadata.team=core", "t-alice", nil)
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/candidates?candidate_kind=external&model=claude-haiku-5-5", "t-alice", nil); len(got["items"].([]any)) != 2 {
		t.Fatalf("candidates %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/candidates?agent=sha256:"+strings.Repeat("1", 64), "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatalf("candidates of an agent %v", got)
	}
	// An agent's candidates by its name, at any version, and by one
	// version's digest, however a candidate names it.
	v1, v2 := "sha256:"+strings.Repeat("2", 64), "sha256:"+strings.Repeat("3", 64)
	x.apply("candidates", "helper-v1", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helper@" + v1, "model": "m"}})
	x.apply("candidates", "helper-v2", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helper@" + v2, "model": "m"}})
	x.apply("candidates", "helper-bare", map[string]any{"kind": "topos", "topos": map[string]any{"agent": v1, "model": "m"}})
	x.apply("candidates", "helpers-other", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helpers@" + v1, "model": "m"}})
	names := func(q string) []string {
		var out []string
		for _, it := range x.must(http.StatusOK, http.MethodGet, "/v1/candidates?"+q, "t-alice", nil)["items"].([]any) {
			out = append(out, it.(map[string]any)["name"].(string))
		}
		slices.Sort(out)
		return out
	}
	if got := names("agent=helper"); !slices.Equal(got, []string{"helper-v1", "helper-v2"}) {
		t.Fatalf("candidates of the agent helper %v", got)
	}
	if got := names("agent=" + url.QueryEscape(v1)); !slices.Equal(got, []string{"helper-bare", "helper-v1", "helpers-other"}) {
		t.Fatalf("candidates of the version %v", got)
	}
	if got := names("agent=" + url.QueryEscape("helper@"+v2)); !slices.Equal(got, []string{"helper-v2"}) {
		t.Fatalf("candidates of helper@v2 %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/graders?grader_kind=check&produces="+f.quality, "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatalf("graders %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites?name=goalless&owner="+url.PathEscape(alice), "t-alice", nil); len(got["items"].([]any)) != 1 {
		t.Fatalf("suites by name and owner %v", got)
	}
	// A list naming another owner holds nothing of the caller's own.
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/suites?owner="+url.PathEscape(bob), "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatalf("suites of bob %v", got)
	}
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidCursor, http.MethodGet, "/v1/suites?cursor=nope", "t-alice", nil)
}

// A name sent without an owner is resolved in the owner the list's allow
// names, so a member reads by name what it applied in an organization.
func TestNamesResolveInTheListsOwner(t *testing.T) {
	const org = "https://issuer.example.com|org"
	x := newAPI(t, func(s *Server) { s.Authz = orgPolicy{inner: s.Authz, org: org} })
	x.must(http.StatusCreated, http.MethodPut, "/v1/metrics/pass", "t-alice", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate"})
	got := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/pass", "t-alice", nil)
	if got["owner"] != org {
		t.Fatalf("the metric was applied under %v", got["owner"])
	}
}

// orgPolicy answers every question with an allow naming the organization
// as the owner.
type orgPolicy struct {
	inner access.Authorizer
	org   string
}

func (o orgPolicy) Authorize(ctx context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	d := access.Decision{Allow: true}
	switch q.Action {
	case authorizer.ActionMetricApply, authorizer.ActionMetricList:
		d.Limits.Owner = o.org
	}
	return d, nil
}
