// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"latere.ai/x/pkg/authz"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
)

func TestCreateRefusals(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	ok := func(extra map[string]any) map[string]any {
		b := map[string]any{"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}}
		maps.Copy(b, extra)
		return b
	}
	many := make([]string, 33)
	for i := range many {
		many[i] = "sha256:" + strings.Repeat(string(rune('a'+i%6)), 63) + string(rune('0'+i%10))
	}
	cases := []struct {
		body   map[string]any
		status int
		code   string
		field  string
	}{
		{map[string]any{"candidates": []string{f.high}, "budget": map[string]any{"name": "b"}}, 400, apierr.CodeMissingField, "suite"},
		{map[string]any{"suite": f.suite, "budget": map[string]any{"name": "b"}}, 400, apierr.CodeMissingField, "candidates"},
		{map[string]any{"suite": f.suite, "candidates": []string{f.high}}, 400, apierr.CodeMissingField, "budget"},
		{ok(map[string]any{"candidates": many}), 422, apierr.CodeLimitExceeded, "candidates"},
		{ok(map[string]any{"suite": "goalless"}), 422, apierr.CodeUnpinnedReference, "suite"},
		{ok(map[string]any{"candidates": []string{"harness-high"}}), 422, apierr.CodeUnpinnedReference, "candidates[0]"},
		{ok(map[string]any{"candidates": []string{f.high, f.high}}), 400, apierr.CodeInvalidField, "candidates[1]"},
		{ok(map[string]any{"trials": 0}), 400, apierr.CodeInvalidField, "trials"},
		{ok(map[string]any{"trials": 1001}), 422, apierr.CodeLimitExceeded, "trials"},
		{ok(map[string]any{"budget": map[string]any{}}), 400, apierr.CodeInvalidField, "budget"},
		{ok(map[string]any{"budget": map[string]any{"name": "a", "cap_usd_micro": 5}}), 400, apierr.CodeInvalidField, "budget"},
		{ok(map[string]any{"budget": map[string]any{"name": ""}}), 400, apierr.CodeInvalidField, "budget.name"},
		{ok(map[string]any{"budget": map[string]any{"cap_usd_micro": 0}}), 400, apierr.CodeInvalidField, "budget.cap_usd_micro"},
		{ok(map[string]any{"parallelism": 0}), 400, apierr.CodeInvalidField, "parallelism"},
		{ok(map[string]any{"parallelism": 257}), 422, apierr.CodeLimitExceeded, "parallelism"},
		{ok(map[string]any{"reference": map[string]any{}}), 400, apierr.CodeInvalidField, "reference"},
		{ok(map[string]any{"reference": map[string]any{"candidate": f.low}}), 422, apierr.CodeNotInExperiment, "reference.candidate"},
		{ok(map[string]any{"reference": map[string]any{"candidate": "harness-low"}}), 422, apierr.CodeUnpinnedReference, "reference.candidate"},
		{ok(map[string]any{"mode": "dry"}), 400, apierr.CodeInvalidField, "mode"},
		{ok(map[string]any{"metadata": map[string]string{"examen.trial": "x"}}), 400, apierr.CodeInvalidField, "metadata.examen.trial"},
		{ok(map[string]any{"adopt": true}), 400, apierr.CodeUnknownField, "adopt"},
		{ok(map[string]any{"benchmark": "pub_1"}), 400, apierr.CodeUnknownField, "benchmark"},
		{ok(map[string]any{"suite": "sha256:" + strings.Repeat("0", 64)}), 422, apierr.CodeReferenceNotFound, "suite"},
		{ok(map[string]any{"suite": "other@" + f.suite}), 422, apierr.CodeReferenceNotFound, "suite"},
		{ok(map[string]any{"candidates": []string{"sha256:" + strings.Repeat("0", 64)}}), 422, apierr.CodeReferenceNotFound, "candidates[0]"},
		{ok(map[string]any{"budget": map[string]any{"cap_usd_micro": 5000000}}), 422, apierr.CodeCoreNotConfigured, "budget.cap_usd_micro"},
	}
	for _, c := range cases {
		d := x.refuse(c.status, c.code, http.MethodPost, "/v1/experiments", "t-alice", c.body)
		if d["field"] != c.field {
			t.Errorf("%v: field %v, want %s", c.body, d["field"], c.field)
		}
	}
	// What this release does not run is refused by name.
	topos := x.apply("candidates", "agent", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "sha256:" + strings.Repeat("1", 64), "model": "claude-haiku-5-5"}})
	if d := x.refuse(422, apierr.CodeCoreNotConfigured, http.MethodPost, "/v1/experiments", "t-alice", ok(map[string]any{"candidates": []string{topos}})); d["field"] != "candidates[0]" || d["core"] != "topos" {
		t.Fatalf("topos %v", d)
	}
	program := x.apply("candidates", "runner", map[string]any{"kind": "program", "program": map[string]any{"image": "ghcr.io/x/runner@sha256:" + strings.Repeat("2", 64)}})
	x.refuse(422, apierr.CodeExecutorUnavailable, http.MethodPost, "/v1/experiments", "t-alice", ok(map[string]any{"candidates": []string{f.high, program}}))
	check := x.apply("graders", "tests", map[string]any{"kind": "check", "produces": []string{f.quality}, "check": map[string]any{"command": []string{"make", "test"}}})
	checked := x.apply("tasks", "checked", map[string]any{"checks": []string{check}})
	withInputs := x.apply("tasks", "inputs", map[string]any{"inputs": []map[string]any{{"name": "brief.md", "text": "hi"}}})
	judge := x.apply("graders", "judge", map[string]any{"kind": "model_judge", "produces": []string{f.quality}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "prompt": "rate"}})
	agent := x.apply("graders", "agent", map[string]any{"kind": "agent_judge", "produces": []string{f.quality}, "agent_judge": map[string]any{"agent": "sha256:" + strings.Repeat("3", 64), "instructions": "review"}})
	decline := x.apply("graders", "decline", map[string]any{"kind": "model_judge", "produces": []string{"builtin:declined"}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "prompt": "declined?", "inputs": map[string]any{"final_message": true, "transcript": true}}})
	canon := x.apply("graders", "canon", map[string]any{"kind": "model_judge", "produces": []string{f.theme}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "role": "canonicalize"}})
	for i, c := range []struct {
		suite map[string]any
		core  string
		field string
	}{
		{map[string]any{"tasks": []string{checked}, "metrics": []string{f.quality}, "primary_metric": f.quality}, "cella", "suite.tasks[0].checks"},
		{map[string]any{"tasks": []string{withInputs}, "metrics": []string{f.quality}, "primary_metric": f.quality}, "arca", "suite.tasks[0].inputs"},
		{map[string]any{"tasks": []string{f.taskA}, "metrics": []string{f.quality}, "primary_metric": f.quality, "graders": []string{judge}}, "lux", "suite.graders[0]"},
		{map[string]any{"tasks": []string{f.taskA}, "metrics": []string{f.quality}, "primary_metric": f.quality, "graders": []string{agent}}, "topos", "suite.graders[0]"},
		{map[string]any{"tasks": []string{f.taskA}, "metrics": []string{f.quality, "builtin:declined"}, "primary_metric": f.quality, "decline_judge": decline}, "lux", "suite.decline_judge"},
		{map[string]any{"tasks": []string{f.taskA}, "metrics": []string{f.quality, f.theme}, "primary_metric": f.quality, "canonicalizers": []string{canon}}, "lux", "suite.canonicalizers[0]"},
	} {
		digest := x.apply("suites", "unbuilt-"+itoa(i), c.suite)
		d := x.refuse(422, apierr.CodeCoreNotConfigured, http.MethodPost, "/v1/experiments", "t-alice", ok(map[string]any{"suite": digest}))
		if d["core"] != c.core || d["field"] != c.field {
			t.Errorf("%s: %v", c.field, d)
		}
	}
	// A task with inputs is no refusal in mode import, which delivers none.
	inputsSuite := x.apply("suites", "with-inputs", map[string]any{"tasks": []string{withInputs}, "metrics": []string{f.quality}, "primary_metric": f.quality})
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", ok(map[string]any{"suite": inputsSuite, "mode": "import"}))
	// Another subject cannot name alice's versions: its question is asked
	// before the answer that the reference resolves to nothing.
	x.refuse(422, apierr.CodeReferenceNotFound, http.MethodPost, "/v1/experiments", "t-bob", ok(nil))
}

// experiment.create names the kinds of every grader the suite version pins,
// its tasks' checks, decline judge and canonicalizers included, each once
// and sorted, and an empty list for a suite that pins none, so an
// authorizer decides from it whether the work needs an owner token.
func TestCreateSendsGraderKinds(t *testing.T) {
	policy := &limitPolicy{}
	x := newAPI(t, func(s *Server) {
		policy.inner = s.Authz
		s.Authz = policy
	})
	f := x.fixture()
	create := func(suite string) {
		x.do(http.MethodPost, "/v1/experiments", "t-alice", map[string]any{"suite": suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}})
	}
	create(f.suite)
	if kinds, ok := policy.asked[authorizer.FieldGraderKinds].([]string); !ok || len(kinds) != 0 {
		t.Fatalf("a suite with no graders sends %#v", policy.asked[authorizer.FieldGraderKinds])
	}
	check := x.apply("graders", "tests", map[string]any{"kind": "check", "produces": []string{f.quality}, "check": map[string]any{"command": []string{"make", "test"}}})
	checked := x.apply("tasks", "checked", map[string]any{"checks": []string{check}})
	review := x.apply("graders", "review", map[string]any{"kind": "annotation", "produces": []string{f.quality}, "annotation": map[string]any{"instructions": "rate it"}})
	decline := x.apply("graders", "decline", map[string]any{"kind": "model_judge", "produces": []string{"builtin:declined"}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "prompt": "declined?", "inputs": map[string]any{"final_message": true, "transcript": true}}})
	canon := x.apply("graders", "canon", map[string]any{"kind": "model_judge", "produces": []string{f.theme}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "role": "canonicalize"}})
	graded := x.apply("suites", "graded", map[string]any{
		"tasks": []string{checked, f.taskA}, "metrics": []string{f.quality, f.theme, "builtin:declined"}, "primary_metric": f.quality,
		"graders": []string{review}, "decline_judge": decline, "canonicalizers": []string{canon},
	})
	// The create is refused core_not_configured after the question, which
	// names every kind.
	create(graded)
	if kinds, _ := policy.asked[authorizer.FieldGraderKinds].([]string); !slices.Equal(kinds, []string{"annotation", "check", "model_judge"}) {
		t.Fatalf("a suite with graders sends %#v", policy.asked[authorizer.FieldGraderKinds])
	}
}

// askedPolicy records the resource fields of every question it passes on.
type askedPolicy struct {
	inner access.Authorizer
	asked []access.Question
}

func (a *askedPolicy) Authorize(ctx context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	a.asked = append(a.asked, q)
	return a.inner.Authorize(ctx, c, q)
}

// A create's sign_in reaches the authorizer as the field sign_in of
// experiment.create and of no other question; it is not stored on the
// experiment, answered, logged or written to its event log; a create
// without one sends none; and one that is empty, too long or not visible
// ASCII is refused invalid_field on sign_in before any question.
func TestCreatePassesSignInToTheAuthorizerAlone(t *testing.T) {
	policy := &askedPolicy{}
	x := newAPI(t, func(s *Server) {
		policy.inner = s.Authz
		s.Authz = policy
	})
	f := x.fixture()
	const handle = "ksh_7Q2ZJ5M3N4P6R8T0V2X4Z6B8D0"
	policy.asked = nil
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice",
		map[string]any{"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "sign_in": handle})
	creates := 0
	for _, q := range policy.asked {
		v, sent := q.Resource.Fields[authorizer.FieldSignIn]
		switch {
		case q.Action == authorizer.ActionExperimentCreate:
			creates++
			if v != handle {
				t.Errorf("experiment.create carries sign_in %#v, want the create's", v)
			}
		case sent:
			t.Errorf("%s carries sign_in", q.Action)
		}
	}
	if creates != 1 {
		t.Fatalf("%d experiment.create questions, want 1", creates)
	}
	id, _ := created["id"].(string)
	read := x.do(http.MethodGet, "/v1/experiments/"+id, "t-alice", nil)
	events := x.do(http.MethodGet, "/v1/experiments/"+id+"/events", "t-alice", nil)
	for name, body := range map[string]string{
		"the create's answer": string(mustJSON(t, created)), "a read": string(read.body), "the event log": string(events.body), "the log": x.log(),
	} {
		if strings.Contains(body, handle) {
			t.Errorf("%s carries the sign_in", name)
		}
	}

	policy.asked = nil
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice",
		map[string]any{"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}})
	for _, q := range policy.asked {
		if _, sent := q.Resource.Fields[authorizer.FieldSignIn]; sent {
			t.Errorf("a create without sign_in sends one on %s", q.Action)
		}
	}

	for _, bad := range []string{"", strings.Repeat("a", limits.SignInMaxBytes+1), "has space", "tab\there", "\u00e9t\u00e9"} {
		policy.asked = nil
		d := x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodPost, "/v1/experiments", "t-alice",
			map[string]any{"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "sign_in": bad})
		if d["field"] != "sign_in" || len(policy.asked) != 0 {
			t.Errorf("sign_in %q: refused on %v after %d questions", bad, d["field"], len(policy.asked))
		}
	}
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice",
		map[string]any{"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "sign_in": strings.Repeat("a", limits.SignInMaxBytes)})
}

// The limits an allow carries hold a create.
func TestCreateLimits(t *testing.T) {
	limits := &limitPolicy{}
	x := newAPI(t, func(s *Server) {
		limits.inner = s.Authz
		s.Authz = limits
	})
	f := x.fixture()
	body := func(extra map[string]any) map[string]any {
		b := map[string]any{"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 4, "budget": map[string]any{"name": "study"}, "mode": "import"}
		maps.Copy(b, extra)
		return b
	}
	limits.set(map[string]any{"max_parallelism": 4})
	if d := x.refuse(403, apierr.CodeForbidden, http.MethodPost, "/v1/experiments", "t-alice", body(map[string]any{"parallelism": 5})); d["reason"] != "max_parallelism" || d["figure"] != 4.0 {
		t.Fatalf("parallelism %v", d)
	}
	if e := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", body(nil)); e["parallelism"] != 4.0 {
		t.Fatalf("the default within the limit %v", e)
	}
	limits.set(map[string]any{"max_trials": 10})
	if d := x.refuse(403, apierr.CodeForbidden, http.MethodPost, "/v1/experiments", "t-alice", body(nil)); d["reason"] != "max_trials" {
		t.Fatalf("trials %v", d)
	}
	limits.set(map[string]any{"executors": []string{"cella"}})
	if d := x.refuse(403, apierr.CodeForbidden, http.MethodPost, "/v1/experiments", "t-alice", body(nil)); d["reason"] != "executors" {
		t.Fatalf("executors %v", d)
	}
	limits.set(map[string]any{"budget": "assigned"})
	if d := x.refuse(403, apierr.CodeForbidden, http.MethodPost, "/v1/experiments", "t-alice", body(nil)); d["reason"] != "budget" {
		t.Fatalf("budget %v", d)
	}
	e := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", body(map[string]any{"budget": map[string]any{"cap_usd_micro": 5000000}}))
	if e["budget"].(map[string]any)["name"] != "assigned" {
		t.Fatalf("an assigned budget %v", e)
	}
	limits.set(map[string]any{"max_active_experiments": 2})
	if d := x.refuse(403, apierr.CodeForbidden, http.MethodPost, "/v1/experiments", "t-alice", body(nil)); d["reason"] != "max_active_experiments" {
		t.Fatalf("active %v", d)
	}
	limits.set(nil)
	for range 14 {
		x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", body(nil))
	}
	if d := x.refuse(422, apierr.CodeLimitExceeded, http.MethodPost, "/v1/experiments", "t-alice", body(nil)); d["constant"] != "MAX_ACTIVE_EXPERIMENTS_PER_OWNER" {
		t.Fatalf("the constant %v", d)
	}
	limits.deny = "sign_in_not_kept"
	x.refuse(403, apierr.CodeSignInNotKept, http.MethodPost, "/v1/experiments", "t-alice", body(nil))
}

// limitPolicy allows experiment.create with the limits a test sets, or
// denies it with a reason, and asks the policy it wraps everything else.
type limitPolicy struct {
	inner  access.Authorizer
	limits map[string]any
	deny   string
	// asked are the resource fields of the last experiment.create.
	asked map[string]any
}

func (l *limitPolicy) set(m map[string]any) { l.limits = m }

func (l *limitPolicy) Authorize(ctx context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	if q.Action != authorizer.ActionExperimentCreate {
		return l.inner.Authorize(ctx, c, q)
	}
	l.asked = q.Resource.Fields
	if l.deny != "" {
		return access.Decision{Reason: l.deny}, nil
	}
	d := access.Decision{Allow: true}
	for k, v := range l.limits {
		switch k {
		case "max_parallelism":
			d.Limits.MaxParallelism = v.(int)
		case "max_trials":
			d.Limits.MaxTrials = v.(int)
		case "max_active_experiments":
			d.Limits.MaxActiveExperiments = v.(int)
		case "executors":
			d.Limits.Executors = v.([]string)
		case "budget":
			d.Limits.Budget = v.(string)
		}
	}
	return d, nil
}

func TestExperimentReads(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 2, "budget": map[string]any{"name": "study"},
		"metadata": map[string]string{"commit": "9f2c1e7"},
	}, "Idempotency-Key", "create-1")["id"].(string)
	// The same key and body answers the first response again.
	replay := x.do(http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 2, "budget": map[string]any{"name": "study"},
		"metadata": map[string]string{"commit": "9f2c1e7"},
	}, "Idempotency-Key", "create-1")
	if replay.header.Get("Idempotent-Replayed") != "true" || replay.json(t)["id"] != eid {
		t.Fatalf("replay %v %s", replay.header, replay.body)
	}
	for _, q := range []string{"", "?suite=" + f.suite, "?suite=goalless", "?candidate=" + f.high, "?state=running", "?mode=execute", "?metadata.commit=9f2c1e7"} {
		if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments"+q, "t-alice", nil); len(got["items"].([]any)) != 1 {
			t.Errorf("%s: %v", q, got)
		}
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments?state=complete", "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("no complete experiment")
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments", "t-bob", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("bob lists alice's")
	}
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodGet, "/v1/experiments/"+eid, "t-bob", nil)
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodGet, "/v1/experiments/"+id.New(id.Experiment), "t-alice", nil)
	cells := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/cells", "t-alice", nil)["items"].([]any)
	if len(cells) != 4 {
		t.Fatalf("cells %v", cells)
	}
	cell := cells[0].(map[string]any)
	for _, q := range []string{"?state=smoking", "?task=todo-app", "?task=" + f.taskA, "?candidate=" + f.high} {
		if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/cells"+q, "t-alice", nil); len(got["items"].([]any)) < 2 {
			t.Errorf("cells %s: %v", q, got)
		}
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/cells?task=nope", "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("cells of a task the suite does not pin")
	}
	x.must(http.StatusOK, http.MethodGet, "/v1/cells/"+cell["id"].(string), "t-alice", nil)
	x.refuse(404, apierr.CodeCellNotFound, http.MethodGet, "/v1/cells/"+cell["id"].(string), "t-bob", nil)
	x.refuse(404, apierr.CodeCellNotFound, http.MethodGet, "/v1/cells/"+id.New(id.Cell), "t-alice", nil)
	trials := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?limit=1", "t-alice", nil)
	if len(trials["items"].([]any)) != 1 || trials["next_cursor"] == nil {
		t.Fatalf("trials %v", trials)
	}
	tr := trials["items"].([]any)[0].(map[string]any)
	x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tr["id"].(string), "t-alice", nil)
	x.refuse(404, apierr.CodeTrialNotFound, http.MethodGet, "/v1/trials/"+tr["id"].(string), "t-bob", nil)
	x.refuse(404, apierr.CodeTrialNotFound, http.MethodGet, "/v1/trials/"+id.New(id.Trial), "t-alice", nil)
	for _, q := range []string{"?state=pending", "?outcome=completed", "?imported=false", "?retried=false", "?cell=" + cell["id"].(string), "?task=todo-app", "?candidate=" + f.low} {
		x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials"+q, "t-alice", nil)
	}
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=quality", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=nope&value=1", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=quality&value=high", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=completed&value=yes", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/trials?imported=maybe", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/trials?retried=maybe", "t-alice", nil)
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=completed&value=missing", "t-alice", nil); len(got["items"].([]any)) != 4 {
		t.Fatalf("unfinished trials have no value %v", got)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=theme&value=x", "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("no trial holds the label")
	}
	events := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/events?limit=2", "t-alice", nil)
	if len(events["items"].([]any)) != 2 || events["next_cursor"] != "2" {
		t.Fatalf("events %v", events)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/events?after=2&limit=200", "t-alice", nil); got["items"].([]any)[0].(map[string]any)["seq"] != 3.0 {
		t.Fatalf("events after 2 %v", got)
	}
	x.refuse(400, apierr.CodeInvalidCursor, http.MethodGet, "/v1/experiments/"+eid+"/events?after=x", "t-alice", nil)
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/events?limit=0", "t-alice", nil)
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/events?after=10000", "t-alice", nil); len(got["items"].([]any)) != 0 {
		t.Fatal("no event past the latest")
	}
	x.refuse(409, apierr.CodeWrongMode, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-alice", nil)
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/experiments/"+eid+"/close", "t-bob", nil)
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-bob", nil)
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/experiments/"+id.New(id.Experiment)+"/cancel", "t-alice", nil)
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-alice", nil)
	again := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-alice", nil)
	if again["state"] != "canceled" {
		t.Fatalf("a repeated cancel %v", again)
	}
	x.refuse(409, apierr.CodeExperimentFinished, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	// Canceled trials keep their records, and the measurement routes read.
	for _, path := range []string{"/v1/experiments/" + eid + "/measurements?generation=all", "/v1/trials/" + tr["id"].(string) + "/measurements?metric=quality&grader=builtin&generation=1"} {
		x.must(http.StatusOK, http.MethodGet, path, "t-alice", nil)
	}
	x.refuse(400, apierr.CodeInvalidField, http.MethodGet, "/v1/experiments/"+eid+"/measurements?generation=0", "t-alice", nil)
	x.refuse(404, apierr.CodeTrialNotFound, http.MethodGet, "/v1/trials/"+tr["id"].(string)+"/measurements", "t-bob", nil)
	x.refuse(404, apierr.CodeMeasurementNotFound, http.MethodGet, "/v1/measurements/"+id.New(id.Measurement), "t-alice", nil)
}

// An experiment created in an organization's context is the
// organization's and was created by the member who asked: both are
// stored, never changed and answered on every read. Another member runs
// it in the organization's context, the unattended work it starts asks
// experiment.run on behalf of the creator, and a person outside the
// organization reads nothing of it.
func TestAnOrganizationsExperimentKeepsItsCreator(t *testing.T) {
	const org = "https://issuer.example.com|org"
	policy := &orgContext{org: org, members: map[string]bool{alice: true, bob: true}}
	x := newAPI(t, func(s *Server) {
		s.Authz = policy
		s.Engine.Authz = policy
	})
	f := x.fixture()
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})
	if created["owner"] != org || created["created_by"] != alice {
		t.Fatalf("the create answered owner %v and created_by %v", created["owner"], created["created_by"])
	}
	eid := created["id"].(string)
	read := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-bob", nil)
	if read["owner"] != org || read["created_by"] != alice {
		t.Fatalf("a member reads owner %v and created_by %v", read["owner"], read["created_by"])
	}
	stored, err := x.st.Experiments().Get(t.Context(), eid)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Owner != org || stored.CreatedBy != alice {
		t.Fatalf("stored owner %q and created_by %q", stored.Owner, stored.CreatedBy)
	}
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-bob", map[string]any{"candidate": f.high, "max": 1})
	runs := policy.unattended()
	if len(runs) == 0 {
		t.Fatal("the lease asked no experiment.run")
	}
	for _, r := range runs {
		if r.subject != alice || r.createdBy != alice || r.owner != org {
			t.Fatalf("experiment.run asked as %+v, want the creator in the organization", r)
		}
	}
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodGet, "/v1/experiments/"+eid, "t-carol", nil)
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-carol", nil)
	if e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil); e["created_by"] != alice || e["owner"] != org {
		t.Fatalf("after the run the experiment reads %v", e)
	}
}

// orgContext is an authorizer of one organization: a member's apply,
// create and list act in the organization, named by the allow's owner
// and filter, a member acts on the organization's stored objects, and
// experiment.run is allowed while its created_by is a member. Anyone
// else is denied. It records every experiment.run it is asked.
type orgContext struct {
	org     string
	members map[string]bool

	mu   sync.Mutex
	runs []runAsked
}

// runAsked is one experiment.run question: its subject, and the owner
// and created_by its resource carried.
type runAsked struct{ subject, owner, createdBy string }

func (o *orgContext) unattended() []runAsked {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.runs)
}

func (o *orgContext) Authorize(_ context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	owner := q.Resource.String(authorizer.FieldOwner)
	if q.Action == authorizer.ActionExperimentRun {
		createdBy := q.Resource.String(authorizer.FieldCreatedBy)
		o.mu.Lock()
		o.runs = append(o.runs, runAsked{subject: c.Subject, owner: owner, createdBy: createdBy})
		o.mu.Unlock()
		if owner == o.org && o.members[createdBy] && createdBy == c.Subject {
			return access.Decision{Allow: true}, nil
		}
		return access.Decision{Reason: authz.ReasonNotOwner}, nil
	}
	if !o.members[c.Subject] {
		return access.Decision{Reason: authz.ReasonNotOwner}, nil
	}
	switch {
	case authz.IsList(q.Action):
		d := access.Decision{Allow: true, Filter: &authz.Filter{Owners: []string{o.org}}}
		d.Limits.Owner = o.org
		return d, nil
	case owner == "":
		d := access.Decision{Allow: true}
		d.Limits.Owner = o.org
		return d, nil
	case owner == o.org:
		return access.Decision{Allow: true}, nil
	}
	return access.Decision{Reason: authz.ReasonNotOwner}, nil
}
