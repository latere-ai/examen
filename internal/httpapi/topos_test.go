// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/cella"
	keysource "latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/objects"
	ownertoken "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/topos"
	"latere.ai/x/examen/internal/topos/topostest"
	"latere.ai/x/examen/internal/work"
)

// mint answers an owner token per audience, or the error the test sets.
type mint struct {
	mu   sync.Mutex
	err  error
	asks []ownertoken.Request
}

func (m *mint) Token(_ context.Context, r ownertoken.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asks = append(m.asks, r)
	if m.err != nil {
		return "", m.err
	}
	return "tok-" + r.Audience, nil
}

func (m *mint) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

const helperDigest = "sha256:" + "4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d"

// toposAPI is a server whose engine runs topos trials at stub, with m's
// owner tokens and origo repositories cloned from origo.
func toposAPI(t *testing.T, stub *topostest.Stub, m *mint, origo string) *client {
	t.Helper()
	return newAPI(t, func(s *Server) {
		s.Engine.Topos = &work.Topos{Client: topos.New(stub.URL, stub.Client()), Tokens: m, Audience: "topos", OrigoURL: origo}
		// Five minutes past the stub's first session, so no run is past its
		// deadline whatever day the test runs.
		s.Engine.Now = func() time.Time { return time.Date(2026, 10, 19, 12, 5, 0, 0, time.UTC) }
	})
}

// TestAToposExperimentRunsItsTrialsAsSessions: an experiment of two topos
// candidates, one agent version on two reasoning levels, resolves the
// version as the caller at its create and runs each trial as a session
// that the API then shows done, with its outcome, cost and evidence.
func TestAToposExperimentRunsItsTrialsAsSessions(t *testing.T) {
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 7, Digest: helperDigest})
	m := &mint{}
	x := toposAPI(t, stub, m, "https://code.example")
	commit := strings.Repeat("e", 40)
	empty := x.apply("tasks", "build", map[string]any{"instruction": "Build something.", "outputs": []string{"workspace/"}})
	seeded := x.apply("tasks", "do-want-seeded", map[string]any{"instruction": "Do something you want.",
		"workspace": map[string]any{"kind": "origo", "repository": "alice/seed", "commit": commit}})
	suite := x.apply("suites", "open-build", map[string]any{"tasks": []string{empty, seeded}, "metrics": []string{"builtin:completed"}, "primary_metric": "builtin:completed"})
	low := x.apply("candidates", "helper-low", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helper@" + helperDigest, "model": "anthropic/claude-haiku-4.5", "reasoning": "low"}})
	high := x.apply("candidates", "helper-55", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helper@" + helperDigest, "model": "anthropic/claude-haiku-5.5", "reasoning": "low"}})
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": "open-build@" + suite, "candidates": []string{low, high}, "trials": 1, "budget": map[string]any{"name": "b"}, "scheduling": "interleaved",
	})
	eid := created["id"].(string)
	if len(m.asks) != 2 || m.asks[0].Purpose != ownertoken.PurposeResolve || m.asks[0].Subject != alice || m.asks[0].Experiment != eid {
		t.Fatalf("the create asked for %+v", m.asks)
	}
	ctx := t.Context()
	if err := x.srv.Engine.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sessions := stub.Sessions()
	if len(sessions) != 4 {
		t.Fatalf("one tick started %d sessions, want 4", len(sessions))
	}
	repos := 0
	for _, sid := range sessions {
		if _, ok := stub.Body(sid)["resources"]; ok {
			repos++
		}
		at := time.Date(2026, 10, 19, 12, 30, 0, 0, time.UTC)
		stub.Set(sid, topos.StatusEnded, topos.StopCompleted,
			topostest.Event(topos.EventSessionStatus, "", at, map[string]any{"status": "running"}),
			topostest.Event(topos.EventModelRequest, "", at.Add(time.Second), map[string]any{"model": "m", "outcome": "ok", "cost_usd_micro": 500, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}}),
			topostest.Event(topos.EventSessionStatus, "", at.Add(time.Minute), map[string]any{"status": "ended", "stop_reason": "completed"}),
		)
	}
	if repos != 2 {
		t.Fatalf("%d sessions were given the seeded task's repository, want 2", repos)
	}
	if err := x.srv.Engine.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil)
	if got["state"] != "complete" {
		t.Fatalf("the experiment is %v", got["state"])
	}
	trials := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials", "t-alice", nil)["items"].([]any)
	for _, raw := range trials {
		tr := raw.(map[string]any)
		obs := tr["observed"].(map[string]any)
		if tr["outcome"] != "completed" || obs["cost_usd"] != 0.0005 || len(tr["evidence"].([]any)) != 2 || !strings.HasPrefix(tr["executor_ref"].(string), "ses_") {
			t.Fatalf("a trial %v", tr)
		}
	}
}

// TestAToposCreateIsRefusedForWhatCannotRun: a candidate that narrows its
// tools, a task with no instruction or an Arca workspace, an origo
// workspace with no Origo, a version Topos does not hold, a sign-in the
// minter keeps none of, a minter or a Topos that does not answer: each is
// refused at the create, before anything runs.
func TestAToposCreateIsRefusedForWhatCannotRun(t *testing.T) {
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 7, Digest: helperDigest})
	m := &mint{}
	x := toposAPI(t, stub, m, "")
	task := x.apply("tasks", "build", map[string]any{"instruction": "Build something."})
	silent := x.apply("tasks", "silent", map[string]any{"outputs": []string{"x"}})
	seeded := x.apply("tasks", "seeded", map[string]any{"instruction": "Go.", "workspace": map[string]any{"kind": "origo", "repository": "a/b", "commit": strings.Repeat("e", 40)}})
	arca := x.apply("tasks", "arca", map[string]any{"instruction": "Go.", "workspace": map[string]any{"kind": "arca", "path": "files/x", "digest": "sha256:" + strings.Repeat("1", 64)}})
	suite := func(name, task string) string {
		return name + "@" + x.apply("suites", name, map[string]any{"tasks": []string{task}, "metrics": []string{"builtin:completed"}, "primary_metric": "builtin:completed"})
	}
	agent := func(digest string, extra map[string]any) map[string]any {
		out := map[string]any{"agent": "helper@" + digest, "model": "anthropic/claude-haiku-5.5"}
		maps.Copy(out, extra)
		return out
	}
	cand := x.apply("candidates", "helper", map[string]any{"kind": "topos", "topos": agent(helperDigest, nil)})
	narrowed := x.apply("candidates", "narrowed", map[string]any{"kind": "topos", "topos": agent(helperDigest, map[string]any{"tools": []string{"bash"}})})
	unknown := x.apply("candidates", "unknown", map[string]any{"kind": "topos", "topos": agent("sha256:"+strings.Repeat("9", 64), nil)})
	create := func(s, c string) map[string]any {
		return map[string]any{"suite": s, "candidates": []string{c}, "trials": 1, "budget": map[string]any{"name": "b"}}
	}
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
		field  string
	}{
		{create(suite("one", task), narrowed), 422, apierr.CodeExecutorUnavailable, "candidates[0].topos.tools"},
		{create(suite("silent", silent), cand), 400, apierr.CodeMissingField, "suite.tasks[0].instruction"},
		{create(suite("arca", arca), cand), 422, apierr.CodeExecutorUnavailable, "suite.tasks[0].workspace"},
		{create(suite("seeded", seeded), cand), 422, apierr.CodeCoreNotConfigured, "suite.tasks[0].workspace"},
		{create(suite("one", task), unknown), 422, apierr.CodeUnresolvedReference, "candidates[0].topos.agent"},
	} {
		if d := x.refuse(c.status, c.code, http.MethodPost, "/v1/experiments", "t-alice", c.body); d["field"] != c.field {
			t.Errorf("%v: field %v, want %s", c.body, d["field"], c.field)
		}
	}
	m.fail(&ownertoken.RefusedError{Reason: ownertoken.ReasonSignInNotKept})
	x.refuse(403, apierr.CodeSignInNotKept, http.MethodPost, "/v1/experiments", "t-alice", create(suite("one", task), cand))
	m.fail(&ownertoken.RefusedError{Reason: ownertoken.ReasonRefused})
	if d := x.refuse(403, apierr.CodeForbidden, http.MethodPost, "/v1/experiments", "t-alice", create(suite("one", task), cand)); d["reason"] != ownertoken.ReasonRefused {
		t.Fatalf("a refused token %v", d)
	}
	m.fail(ownertoken.ErrUnavailable)
	x.refuse(503, apierr.CodeOwnerTokensUnavailable, http.MethodPost, "/v1/experiments", "t-alice", create(suite("one", task), cand))
	m.fail(nil)
	stub.Down(true)
	x.refuse(502, apierr.CodeCoreUnavailable, http.MethodPost, "/v1/experiments", "t-alice", create(suite("one", task), cand))
}

// TestAGradedToposCreate: where the installation grades, an experiment of
// topos candidates is created with its tasks' checks and graders and its
// suite's model judges, a grader's metadata answered with Examen's pinned
// preamble; checks and judges of external candidates, a judge of level
// candidate, a canonicalizer and a configured decline judge are refused.
func TestAGradedToposCreate(t *testing.T) {
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 7, Digest: helperDigest})
	m := &mint{}
	x := newAPI(t, func(s *Server) {
		s.Engine.Topos = &work.Topos{Client: topos.New(stub.URL, stub.Client()), Tokens: m, Audience: "topos"}
		s.Engine.Keys = keysource.NewMemory()
		s.Engine.Grading = &work.Grading{Tokens: m, Arca: arca.New("https://arca.example", nil), Cella: cella.New("https://cella.example", nil), LuxURL: "https://lux.example"}
	})
	quality := x.apply("metrics", "quality", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "higher_is_better", "aggregate": "mean"})
	theme := x.apply("metrics", "theme", map[string]any{"type": "label", "labels": []string{"a", "b"}, "direction": "none", "aggregate": "distribution"})
	check := x.apply("graders", "tests", map[string]any{"kind": "check", "produces": []string{"quality@" + quality}, "check": map[string]any{"command": []string{"make", "test"}}})
	judgeRef := x.apply("graders", "judge", map[string]any{"kind": "model_judge", "produces": []string{"theme@" + theme},
		"model_judge": map[string]any{"model": "anthropic/claude-haiku-5.5", "prompt": "label", "inputs": map[string]any{"checks": true, "outputs": []string{"workspace/"}}}})
	got := x.must(http.StatusOK, http.MethodGet, "/v1/graders/judge", "t-alice", nil)
	if got["model_judge"].(map[string]any)["preamble"] != objects.JudgePreamble {
		t.Fatalf("the judge's preamble is not pinned: %v", got["model_judge"])
	}
	// The grader as answered applies again as the same version.
	again := x.apply("graders", "judge", map[string]any{"kind": "model_judge", "produces": []string{"theme@" + theme},
		"model_judge": map[string]any{"model": "anthropic/claude-haiku-5.5", "prompt": "label", "preamble": objects.JudgePreamble, "inputs": map[string]any{"checks": true, "outputs": []string{"workspace/"}}}})
	if again != judgeRef {
		t.Fatalf("a judge sent back with its preamble is another version: %s %s", again, judgeRef)
	}
	task := x.apply("tasks", "build", map[string]any{"instruction": "Build something.", "checks": []string{"tests@" + check}, "graders": []string{"judge@" + judgeRef}})
	suite := x.apply("suites", "graded", map[string]any{"tasks": []string{"build@" + task}, "metrics": []string{"builtin:completed", "quality@" + quality, "theme@" + theme}, "primary_metric": "builtin:completed"})
	cand := x.apply("candidates", "helper", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helper@" + helperDigest, "model": "anthropic/claude-haiku-5.5"}})
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{"suite": "graded@" + suite, "candidates": []string{cand}, "trials": 1, "budget": map[string]any{"name": "b"}})

	ext := x.apply("candidates", "mine", map[string]any{"kind": "external", "external": map[string]any{"harness": "h", "version": "1"}})
	if d := x.refuse(422, apierr.CodeExecutorUnavailable, http.MethodPost, "/v1/experiments", "t-alice",
		map[string]any{"suite": "graded@" + suite, "candidates": []string{ext}, "trials": 1, "budget": map[string]any{"name": "b"}}); d["field"] != "suite.tasks[0].checks" {
		t.Fatalf("checks of an external candidate: %v", d)
	}
	plain := x.apply("tasks", "plain", map[string]any{"instruction": "Go."})
	profile := x.apply("graders", "profile", map[string]any{"kind": "model_judge", "level": "candidate", "produces": []string{"theme@" + theme},
		"model_judge": map[string]any{"model": "anthropic/claude-haiku-5.5", "prompt": "profile"}})
	decline := x.apply("graders", "decline", map[string]any{"kind": "model_judge", "produces": []string{"builtin:declined"},
		"model_judge": map[string]any{"model": "anthropic/claude-haiku-5.5", "prompt": "declined?", "inputs": map[string]any{"final_message": true, "transcript": true}}})
	for name, c := range map[string]struct {
		suite map[string]any
		field string
	}{
		"a judge of level candidate": {map[string]any{"tasks": []string{"plain@" + plain}, "metrics": []string{"builtin:completed", "theme@" + theme}, "primary_metric": "builtin:completed", "graders": []string{"profile@" + profile}}, "suite.graders[0]"},
		"a decline judge":            {map[string]any{"tasks": []string{"plain@" + plain}, "metrics": []string{"builtin:completed", "builtin:declined"}, "primary_metric": "builtin:completed", "decline_judge": "decline@" + decline}, "suite.decline_judge"},
	} {
		s := x.apply("suites", strings.ReplaceAll(name, " ", "-"), c.suite)
		if d := x.refuse(422, apierr.CodeExecutorUnavailable, http.MethodPost, "/v1/experiments", "t-alice",
			map[string]any{"suite": strings.ReplaceAll(name, " ", "-") + "@" + s, "candidates": []string{cand}, "trials": 1, "budget": map[string]any{"name": "b"}}); d["field"] != c.field {
			t.Errorf("%s: %v", name, d)
		}
	}
}
