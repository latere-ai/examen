// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/work"
)

// client is a harness serving every route of the release over a store of the
// test's own, with the engine that takes unattended work.
type client struct {
	*harness
	st store.Store
}

func newAPI(t *testing.T, tweak func(*Server)) *client {
	t.Helper()
	st := pgStore(t)
	h := newHarness(t, st, routes, func(s *Server) {
		s.Engine = &work.Engine{Store: st, Authz: s.Authz, Analyzer: compare.NotComputed{}, Log: s.Log}
		s.Reserved = []string{"topos", "cella", "lux", "arca", "origo", "examen"}
		if tweak != nil {
			tweak(s)
		}
	})
	return &client{harness: h, st: st}
}

// json decodes an answer's body.
func (a answer) json(t *testing.T) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(a.body, &out); err != nil {
		t.Fatalf("not JSON: %s", a.body)
	}
	return out
}

// must answers a request that must answer one status.
func (x *client) must(status int, method, path, bearer string, body any, headers ...string) map[string]any {
	x.t.Helper()
	a := x.do(method, path, bearer, body, headers...)
	if a.status != status {
		x.t.Fatalf("%s %s: %d, want %d: %s\n%s", method, path, a.status, status, a.body, x.log())
	}
	if len(a.body) == 0 {
		return nil
	}
	return a.json(x.t)
}

// refuse answers a request that must be refused with a code.
func (x *client) refuse(status int, code, method, path, bearer string, body any, headers ...string) map[string]any {
	x.t.Helper()
	a := x.do(method, path, bearer, body, headers...)
	got, details := a.envelope(x.t)
	if a.status != status || got != code {
		x.t.Fatalf("%s %s: %d %s, want %d %s: %s", method, path, a.status, got, status, code, a.body)
	}
	return details
}

// digest applies a version and answers its digest.
func (x *client) apply(kind, name string, body map[string]any) string {
	x.t.Helper()
	out := x.do(http.MethodPut, "/v1/"+kind+"/"+name, "t-alice", body)
	if out.status != http.StatusCreated && out.status != http.StatusOK {
		x.t.Fatalf("apply %s/%s: %d %s", kind, name, out.status, out.body)
	}
	d, _ := out.json(x.t)["digest"].(string)
	return d
}

// fixture is a suite of two tasks measured on a self-reported score, a
// label and the built-ins, and two external candidates that differ in
// declared reasoning.
type fixture struct {
	quality, theme, suite, high, low, taskA, taskB string
}

func (x *client) fixture() fixture {
	var f fixture
	f.quality = x.apply("metrics", "quality", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "higher_is_better", "aggregate": "mean"})
	f.theme = x.apply("metrics", "theme", map[string]any{"type": "label", "vocabulary": "open", "direction": "none", "aggregate": "distribution"})
	f.taskA = x.apply("tasks", "todo-app", map[string]any{"instruction": "Build a terminal todo app.", "outputs": []string{"app/", "notes.md"}, "timeout": "45m", "max_turns": 40})
	f.taskB = x.apply("tasks", "calculator", map[string]any{"instruction": "Build a calculator."})
	f.suite = x.apply("suites", "goalless", map[string]any{
		"tasks": []string{f.taskA, f.taskB}, "metrics": []string{f.quality, f.theme, "builtin:completed", "builtin:declined", "builtin:duration"},
		"primary_metric": f.quality,
	})
	f.high = x.apply("candidates", "harness-high", map[string]any{"kind": "external", "external": map[string]any{"harness": "goalless", "version": "2.1.4", "model": "claude-haiku-5-5", "reasoning": "high"}})
	f.low = x.apply("candidates", "harness-low", map[string]any{"kind": "external", "external": map[string]any{"harness": "goalless", "version": "2.1.4", "model": "claude-haiku-5-5", "reasoning": "low"}})
	return f
}

func TestTheExternalFlow(t *testing.T) { externalFlow(t, newAPI(t, nil)) }

// The same flow through a pooler in transaction mode with every statement
// in exec mode, where a parameter travels as text: JSON bound as bytes
// would reach a jsonb column as a hex literal and be refused.
func TestTheExternalFlowThroughAPooler(t *testing.T) {
	_, pooled := pgtest.Database(t)
	st, _, err := postgres.Open(t.Context(), pooled+"&default_query_exec_mode=exec", postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Error(err)
		}
	})
	h := newHarness(t, st, routes, func(s *Server) {
		s.Engine = &work.Engine{Store: st, Authz: s.Authz, Analyzer: compare.NotComputed{}, Log: s.Log}
	})
	externalFlow(t, &client{harness: h, st: st})
}

func externalFlow(t *testing.T, x *client) {
	t.Helper()
	f := x.fixture()
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": "goalless@" + f.suite, "candidates": []string{f.high, f.low}, "trials": 2, "budget": map[string]any{"name": "study"},
	})
	eid := created["id"].(string)
	if created["state"] != "pending" || created["mode"] != "execute" {
		t.Fatalf("created %v", created)
	}
	// The ticker's work ran with the create: the smoke rollouts are
	// submitted, and the experiment runs.
	got := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil)
	if got["state"] != "running" {
		t.Fatalf("after the create %v", got)
	}
	// Smoke trials first: one per cell of the high candidate.
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	trials := lease["trials"].([]any)
	if len(trials) != 2 {
		t.Fatalf("smoke lease %v", lease)
	}
	first := trials[0].(map[string]any)
	if first["index"].(float64) != 0 || first["timeout"] != "45m" || first["instruction"] != "Build a terminal todo app." {
		t.Fatalf("the first leased trial %v", first)
	}
	// Nothing more is leasable until the smoke trials pass.
	if a := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high}); a.status != http.StatusNoContent {
		t.Fatalf("a second lease %d %s", a.status, a.body)
	}
	start := time.Now().Add(-time.Minute).UTC().Truncate(time.Second)
	for _, raw := range trials {
		tr := raw.(map[string]any)
		out := x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+tr["id"].(string)+"/submit", "t-alice", map[string]any{
			"lease": lease["lease"], "outcome": "completed",
			"started_at": start, "agent_ended_at": start.Add(30 * time.Second),
			"lux_requests": []string{"req_1"}, "service_versions": map[string]string{"lectio": "v0.7.0"},
			"declared":     map[string]string{"harness_version": "2.1.4", "image": "sandbox-agent:2026-02-27"},
			"measurements": []map[string]any{{"metric": f.quality, "value": 0.75}, {"metric": "theme", "value": "  Fixes   Bugs "}},
		})
		if out["state"] != "done" || out["outcome"] != "completed" {
			t.Fatalf("submit %v", out)
		}
	}
	// The smoke passed, so the main trials are leasable.
	main := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high, "max": 5})
	if n := len(main["trials"].([]any)); n != 2 {
		t.Fatalf("main lease %d", n)
	}
	list := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=quality&value=0.75", "t-alice", nil)
	items := list["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("trials by value %v", list)
	}
	values := items[0].(map[string]any)["values"].(map[string]any)
	if values["quality"] != 0.75 || values["theme"] != "fixes bugs" || values["completed"] != true || values["declined"] != false || values["duration"] != 30.0 {
		t.Fatalf("values %v", values)
	}
	if strings.Contains(string(x.do(http.MethodGet, "/v1/experiments/"+eid+"/trials?metric=quality&value=missing", "t-alice", nil).body), `"id"`) == false {
		t.Fatal("the leased main trials have no value")
	}
	grid := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/grid", "t-alice", nil)
	rows := grid["items"].([]any)
	if len(rows) != 2 || grid["metric"].(map[string]any)["name"] != "quality" {
		t.Fatalf("grid %v", grid)
	}
	events := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/events?limit=200", "t-alice", nil)
	if len(events["items"].([]any)) < 10 {
		t.Fatalf("events %v", events)
	}
	cancel := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-alice", nil)
	if cancel["state"] != "canceled" {
		t.Fatalf("cancel %v", cancel)
	}
	_ = access.Caller{}
}
