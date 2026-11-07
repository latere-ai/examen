// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/cella"
	keysource "latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/origo"
	"latere.ai/x/examen/internal/topos"
	"latere.ai/x/examen/internal/topos/topostest"
	"latere.ai/x/examen/internal/work"
)

// referencesOf are a task's references, one of each form.
func referencesOf() []map[string]any {
	return []map[string]any{
		{"name": "notes.txt", "text": "SECRET expected answer"},
		{"name": "table.csv", "arca": map[string]any{"path": "files/evals/reference/table.csv", "digest": "sha256:" + strings.Repeat("1", 64)}},
		{"name": "key.json", "origo": map[string]any{"repository": "alice/gold", "commit": strings.Repeat("e", 40), "path": "gold/key.json"}},
	}
}

// TestAnOrigoReferenceNeedsOrigoToReachItsChecks: a reference reaches the
// task's checks alone, in their sandbox, so a task with checks and an
// origo reference is refused at the create where the installation names no
// Origo to read it from, and accepted where it does; a task with no checks
// has no sandbox for it, and a text or Arca reference is read from the
// stores the checks already need.
func TestAnOrigoReferenceNeedsOrigoToReachItsChecks(t *testing.T) {
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 7, Digest: helperDigest})
	m := &mint{}
	grading := &work.Grading{Tokens: m, Arca: arca.New("https://arca.example", nil), Cella: cella.New("https://cella.example", nil)}
	x := newAPI(t, func(s *Server) {
		s.Engine.Topos = &work.Topos{Client: topos.New(stub.URL, stub.Client()), Tokens: m, Audience: "topos"}
		s.Engine.Keys = keysource.NewMemory()
		s.Engine.Grading = grading
	})
	quality := x.apply("metrics", "quality", map[string]any{"type": "score", "range": []float64{0, 1}, "direction": "higher_is_better", "aggregate": "mean"})
	check := x.apply("graders", "tests", map[string]any{"kind": "check", "produces": []string{"quality@" + quality}, "check": map[string]any{"command": []string{"make", "test"}}})
	refs := referencesOf()
	task := func(name string, checks bool, refs ...map[string]any) string {
		body := map[string]any{"instruction": "Build something.", "reference": refs}
		if checks {
			body["checks"] = []string{"tests@" + check}
		}
		return x.apply("tasks", name, body)
	}
	cand := x.apply("candidates", "helper", map[string]any{"kind": "topos", "topos": map[string]any{"agent": "helper@" + helperDigest, "model": "anthropic/claude-haiku-5.5"}})
	create := func(name, task string) map[string]any {
		suite := x.apply("suites", name, map[string]any{"tasks": []string{name + "-task@" + task}, "metrics": []string{"builtin:completed", "quality@" + quality}, "primary_metric": "builtin:completed"})
		return map[string]any{"suite": name + "@" + suite, "candidates": []string{cand}, "trials": 1, "budget": map[string]any{"name": "b"}}
	}

	withOrigo := task("origo-task", true, refs[2])
	body := create("origo-checked", withOrigo)
	d := x.refuse(http.StatusUnprocessableEntity, apierr.CodeCoreNotConfigured, http.MethodPost, "/v1/experiments", "t-alice", body)
	if d["field"] != "suite.tasks[0].reference[0]" || d["core"] != "origo" {
		t.Fatalf("the refusal %v", d)
	}
	// The third of three is named by its place.
	mixed := task("mixed-task", true, refs...)
	if d := x.refuse(http.StatusUnprocessableEntity, apierr.CodeCoreNotConfigured, http.MethodPost, "/v1/experiments", "t-alice", create("mixed", mixed)); d["field"] != "suite.tasks[0].reference[2]" {
		t.Fatalf("the refusal %v", d)
	}
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", create("unchecked", task("plain-task", false, refs...)))
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", create("local", task("local-task", true, refs[0], refs[1])))

	grading.Origo, grading.OrigoAudience = origo.New("https://origo.example", nil), "origo"
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", body)
	x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", create("mixed-again", mixed))
}

// TestALeaseCarriesNoReference: the answer to an external harness's lease
// holds a task's instruction, outputs and timeout and none of its
// references, neither a name, a path nor the bytes of one: references are
// for the task's checks alone, never for the system under test.
func TestALeaseCarriesNoReference(t *testing.T) {
	x := newAPI(t, nil)
	task := x.apply("tasks", "gold", map[string]any{"instruction": "Extract the fields.", "outputs": []string{"fields.json"}, "reference": referencesOf()})
	suite := x.apply("suites", "gold", map[string]any{"tasks": []string{"gold@" + task}, "metrics": []string{"builtin:completed"}, "primary_metric": "builtin:completed"})
	cand := x.apply("candidates", "harness", map[string]any{"kind": "external", "external": map[string]any{"harness": "goalless", "version": "2.1.4", "model": "claude-haiku-5-5"}})
	created := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{"suite": "gold@" + suite, "candidates": []string{cand}, "trials": 1, "budget": map[string]any{"name": "b"}})
	a := x.do(http.MethodPost, "/v1/experiments/"+created["id"].(string)+"/work/lease", "t-alice", map[string]any{})
	if a.status != http.StatusOK {
		t.Fatalf("the lease: %d %s", a.status, a.body)
	}
	sent := string(a.body)
	if !strings.Contains(sent, "Extract the fields.") {
		t.Fatalf("the lease holds no task: %s", sent)
	}
	for _, leak := range []string{"notes.txt", "table.csv", "key.json", "SECRET", "files/evals/reference", "gold/key.json", "reference"} {
		if strings.Contains(sent, leak) {
			t.Errorf("the lease carries %q: %s", leak, sent)
		}
	}
}
