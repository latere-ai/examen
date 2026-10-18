// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/api"
	"latere.ai/x/examen/internal/apierr"
)

func TestDurations(t *testing.T) {
	for d, want := range map[time.Duration]string{
		30 * time.Minute: "30m", 2 * time.Hour: "2h", 90 * time.Minute: "1h30m", 45 * time.Second: "45s", 0: "0s", 90 * time.Second: "1m30s",
	} {
		if got := duration(d); got != want {
			t.Errorf("%v: %q, want %q", d, got, want)
		}
	}
}

// The document a server serves names its own base path.
func TestTheServedDocument(t *testing.T) {
	if string(api.Served("/v1")) != string(api.OpenAPI) {
		t.Fatal("the default base path serves the document as written")
	}
	if !strings.Contains(string(api.Served("/v1/evals")), "  - url: /v1/evals\n") || !strings.Contains(string(api.Served("")), "  - url: /\n") {
		t.Fatal("the servers entry names the base path")
	}
	x := newAPI(t, func(s *Server) { s.BasePath = "/v1" })
	if a := x.do(http.MethodGet, "/v1/openapi.yaml", "", nil); a.status != http.StatusOK || !strings.Contains(string(a.body), "url: /v1") {
		t.Fatalf("%d", a.status)
	}
}

func TestReadsAcrossOwnersAndOutages(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	judge := x.apply("graders", "judge", map[string]any{"kind": "model_judge", "produces": []string{f.quality}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "prompt": "rate"}})
	got := x.must(http.StatusOK, http.MethodGet, "/v1/graders/"+x.must(http.StatusOK, http.MethodGet, "/v1/graders/judge", "t-alice", nil)["id"].(string), "t-alice", nil)
	if got["digest"] != judge {
		t.Fatalf("by id %v", got)
	}
	// Another subject's read by id asks about another owner's object, and
	// is refused as missing.
	x.refuse(http.StatusNotFound, apierr.CodeGraderNotFound, http.MethodGet, "/v1/graders/"+got["id"].(string), "t-bob", nil)
	cid := x.must(http.StatusOK, http.MethodGet, "/v1/candidates/harness-high", "t-alice", nil)["id"].(string)
	x.refuse(http.StatusNotFound, apierr.CodeCandidateNotFound, http.MethodGet, "/v1/candidates/"+cid, "t-bob", nil)

	eid := x.completeImport(f)
	ms := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/measurements?metric=completed&candidate="+f.high, "t-alice", nil)["items"].([]any)
	if len(ms) != 1 {
		t.Fatalf("measurements %v", ms)
	}
	m := ms[0].(map[string]any)
	if one := x.must(http.StatusOK, http.MethodGet, "/v1/measurements/"+m["id"].(string), "t-alice", nil); one["value"] != true || one["grader"] != "builtin" {
		t.Fatalf("one measurement %v", one)
	}
	x.refuse(http.StatusNotFound, apierr.CodeMeasurementNotFound, http.MethodGet, "/v1/measurements/"+m["id"].(string), "t-bob", nil)
	trials := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?imported=true", "t-alice", nil)["items"].([]any)
	tid := trials[0].(map[string]any)["id"].(string)
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid+"/measurements?generation=all", "t-alice", nil); len(got["items"].([]any)) != 3 {
		t.Fatalf("a trial's measurements %v", got)
	}
	cell := trials[0].(map[string]any)["cell"].(string)
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/measurements?cell="+cell+"&grader=builtin:decline", "t-alice", nil); len(got["items"].([]any)) != 1 {
		t.Fatalf("the decline judge's %v", got)
	}
	for _, path := range []string{
		"/v1/experiments/" + eid + "/measurements?generation=-1", "/v1/trials/" + tid + "/measurements?limit=0",
		"/v1/experiments/" + eid + "/cells?limit=0", "/v1/experiments/" + eid + "/comparisons?limit=0", "/v1/comparisons?limit=0",
		"/v1/experiments?limit=0", "/v1/experiments/" + eid + "/grid?limit=0", "/v1/experiments/" + eid + "/trials?limit=0",
		"/v1/suites/goalless/baselines?limit=0", "/v1/metrics?limit=0", "/v1/metrics/quality/versions?limit=0",
	} {
		x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodGet, path, "t-alice", nil)
	}
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidCursor, http.MethodGet, "/v1/suites/goalless/taxonomies/theme/history?cursor=x", "t-alice", nil)
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidCursor, http.MethodGet, "/v1/suites/goalless/baselines/main/history?cursor=x", "t-alice", nil)
	// An authorizer that does not answer fails every question closed.
	down := newAPI(t, func(s *Server) { s.Authz = unavailable{} })
	for _, path := range []string{"/v1/metrics/pass", "/v1/metrics", "/v1/experiments", "/v1/suites/goalless/baselines/main"} {
		down.refuse(http.StatusServiceUnavailable, apierr.CodeAuthorizerUnavailable, http.MethodGet, path, "t-alice", nil)
	}
	down.refuse(http.StatusServiceUnavailable, apierr.CodeAuthorizerUnavailable, http.MethodPut, "/v1/metrics/pass", "t-alice", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate"})
	// An owner named in the query is the context of a name and of an
	// apply.
	owned := x.must(http.StatusCreated, http.MethodPut, "/v1/metrics/owned?owner="+url.PathEscape(alice), "t-alice", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate"})
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/metrics/owned?owner="+url.PathEscape(alice), "t-alice", nil); got["id"] != owned["id"] {
		t.Fatalf("by owner %v", got)
	}
	x.refuse(http.StatusForbidden, apierr.CodeForbidden, http.MethodPut, "/v1/metrics/owned?owner="+url.PathEscape(bob), "t-alice", map[string]any{"type": "binary", "direction": "none", "aggregate": "rate"})
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=completed&value=true", "t-alice", nil); len(got["items"].([]any)) != 2 {
		t.Fatalf("completed trials %v", got)
	}
	label := x.apply("metrics", "kind", map[string]any{"type": "label", "labels": []string{"a", "b"}, "direction": "none", "aggregate": "distribution"})
	_ = label
	if (&Server{}).analyzer() == nil || (&Server{}).log() == nil {
		t.Fatal("defaults")
	}
}
