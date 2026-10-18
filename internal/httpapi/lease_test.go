// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"net/http"
	"slices"
	"testing"

	"latere.ai/x/examen/internal/apierr"
)

// A lease that names no candidate takes the trials of every external
// candidate in the experiment's scheduling order, and each trial it hands
// out names its candidate with the configuration the harness declared; an
// experiment answers its scheduling, task_by_task when the create named
// none, and a scheduling outside the two is refused.
func TestLeaseWithoutCandidate(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	create := func(scheduling string) map[string]any {
		body := map[string]any{"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 1, "budget": map[string]any{"name": "study"}, "parallelism": 8}
		if scheduling != "" {
			body["scheduling"] = scheduling
		}
		return x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", body)
	}
	if got := create("")["scheduling"]; got != "task_by_task" {
		t.Fatalf("the default scheduling is %v", got)
	}
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidField, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "scheduling": "random",
	})
	e := create("interleaved")
	if e["scheduling"] != "interleaved" {
		t.Fatalf("scheduling %v", e["scheduling"])
	}
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+e["id"].(string)+"/work/lease", "t-alice", map[string]any{})
	var got []string
	for _, raw := range lease["trials"].([]any) {
		tr := raw.(map[string]any)
		cand := tr["candidate"].(map[string]any)
		ext := cand["external"].(map[string]any)
		if ext["harness"] != "goalless" || ext["model"] != "claude-haiku-5-5" || cand["digest"] == "" {
			t.Fatalf("a leased trial's candidate %v", cand)
		}
		got = append(got, tr["task"].(map[string]any)["name"].(string)+"/"+cand["name"].(string))
	}
	// One smoke trial per cell, every cell of every task in turn.
	if want := []string{"todo-app/harness-high", "todo-app/harness-low", "calculator/harness-high", "calculator/harness-low"}; !slices.Equal(got, want) {
		t.Fatalf("leased %v, want %v", got, want)
	}
	x.must(http.StatusNoContent, http.MethodPost, "/v1/experiments/"+e["id"].(string)+"/work/lease", "t-alice", map[string]any{})
}
