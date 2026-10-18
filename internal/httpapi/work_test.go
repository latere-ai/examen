// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/work"
)

// clock is a store clock a test moves.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) read() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

func (c *clock) advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

// newClockedAPI is newAPI over a store whose clock the test moves.
func newClockedAPI(t *testing.T) (*client, *clock) {
	t.Helper()
	clk := &clock{now: time.Now().UTC().Truncate(time.Second)}
	st := pgtest.Open(t, false, postgres.Options{Now: clk.read})
	h := newHarness(t, st, routes, func(s *Server) {
		s.Engine = &work.Engine{Store: st, Authz: s.Authz, Analyzer: compare.NotComputed{}, Log: s.Log}
	})
	return &client{harness: h, st: st}, clk
}

// run is a submit body of an outcome.
func run(lease any, outcome string, extra map[string]any) map[string]any {
	start := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	b := map[string]any{"lease": lease, "outcome": outcome, "started_at": start, "agent_ended_at": start.Add(time.Minute)}
	maps.Copy(b, extra)
	return b
}

func TestLeasesLapseAndRenew(t *testing.T) {
	x, clk := newClockedAPI(t)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 3, "budget": map[string]any{"name": "study"}, "parallelism": 1,
	})["id"].(string)
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	trials := lease["trials"].([]any)
	if len(trials) != 1 {
		t.Fatalf("parallelism 1 leases one trial: %v", lease)
	}
	tid := trials[0].(map[string]any)["id"].(string)
	got := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid, "t-alice", nil)
	if got["state"] != "running" || got["lease"].(map[string]any)["id"] != lease["lease"] {
		t.Fatalf("leased %v", got)
	}
	// Another subject cannot renew it, and a renewal extends it.
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-bob", map[string]any{"renew": lease["lease"]})
	clk.advance(5 * time.Minute)
	renewed := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"renew": lease["lease"]})
	if renewed["expires_at"] == lease["expires_at"] || len(renewed["trials"].([]any)) != 1 || renewed["trials"].([]any)[0].(map[string]any)["inputs"] != nil {
		t.Fatalf("renewed %v", renewed)
	}
	x.refuse(409, apierr.CodeLeaseNotHeld, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"renew": "lse_" + strings.Repeat("0", 26)})
	// Past EXTERNAL_LEASE the trial returns to pending, keeping its id,
	// and a renewal or a submit under the lapsed lease is lease_expired.
	clk.advance(limits.ExternalLease + time.Second)
	if n, err := x.st.Experiments().ExpireLeases(t.Context()); err != nil || n != 1 {
		t.Fatalf("expired %d %v", n, err)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid, "t-alice", nil); got["state"] != "pending" || got["lease"] != nil {
		t.Fatalf("lapsed %v", got)
	}
	x.refuse(409, apierr.CodeLeaseExpired, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"renew": lease["lease"]})
	x.refuse(409, apierr.CodeLeaseExpired, http.MethodPost, "/v1/trials/"+tid+"/submit", "t-alice", run(lease["lease"], "completed", nil))
	// The trial is leased again, by bob's lease the trial is not held.
	again := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	if again["trials"].([]any)[0].(map[string]any)["id"] != tid {
		t.Fatalf("the same trial is leased again %v", again)
	}
	x.refuse(409, apierr.CodeLeaseNotHeld, http.MethodPost, "/v1/trials/"+tid+"/submit", "t-alice", run(lease["lease"], "completed", nil))
	// A lapse found by the lease itself, without the ticker.
	clk.advance(limits.ExternalLease + time.Second)
	third := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	out := x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+tid+"/submit", "t-alice", run(third["lease"], "completed", nil))
	if out["state"] != "done" {
		t.Fatalf("submitted %v", out)
	}
	// A repeat of the same submit answers the trial and changes nothing;
	// another body under the same lease is not held.
	if again := x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+tid+"/submit", "t-alice", run(third["lease"], "completed", nil)); again["ended_at"] != out["ended_at"] {
		t.Fatalf("a repeat changed the trial %v", again)
	}
	x.refuse(409, apierr.CodeLeaseNotHeld, http.MethodPost, "/v1/trials/"+tid+"/submit", "t-alice", run(third["lease"], "declined", nil))
	// A submit ends the lease on its trial, so a lease whose trials were
	// all submitted holds none to renew.
	x.refuse(409, apierr.CodeLeaseNotHeld, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"renew": third["lease"]})
}

func TestRetriesBlockAndResume(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	failed := run(nil, "infra_failed", map[string]any{"failure": map[string]string{"code": "harness_crashed", "detail": "the sandbox died"}})
	// The smoke trial of the first task fails every attempt the retries
	// allow, and its cell parks blocked.
	var cell string
	for attempt := 0; attempt <= limits.MaxInfraRetries; attempt++ {
		lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high, "max": 1})
		tr := lease["trials"].([]any)[0].(map[string]any)
		cell = tr["cell"].(string)
		failed["lease"] = lease["lease"]
		x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+tr["id"].(string)+"/submit", "t-alice", failed)
	}
	got := x.must(http.StatusOK, http.MethodGet, "/v1/cells/"+cell, "t-alice", nil)
	if got["state"] != "blocked" || !strings.HasPrefix(got["parked_reason"].(string), "smoke_infra_failed: ") {
		t.Fatalf("blocked %v", got)
	}
	retried := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?retried=true&cell="+cell, "t-alice", nil)
	if len(retried["items"].([]any)) != limits.MaxInfraRetries {
		t.Fatalf("the retried trials %v", retried)
	}
	x.refuse(404, apierr.CodeCellNotFound, http.MethodPost, "/v1/cells/"+cell+"/retry", "t-bob", nil)
	x.refuse(404, apierr.CodeCellNotFound, http.MethodPost, "/v1/cells/"+id.New(id.Cell)+"/retry", "t-alice", nil)
	resumed := x.must(http.StatusOK, http.MethodPost, "/v1/cells/"+cell+"/retry", "t-alice", nil)
	if resumed["state"] != "smoking" {
		t.Fatalf("resumed %v", resumed)
	}
	x.refuse(409, apierr.CodeCellNotParked, http.MethodPost, "/v1/cells/"+cell+"/retry", "t-alice", nil)
	// The smoke trial now completes, and the main trial follows.
	for range 4 {
		lease := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
		if lease.status == http.StatusNoContent || lease.status == http.StatusConflict {
			break
		}
		l := lease.json(t)
		if lease.status != http.StatusOK {
			t.Fatalf("lease %d %v", lease.status, l)
		}
		for _, raw := range l["trials"].([]any) {
			x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+raw.(map[string]any)["id"].(string)+"/submit", "t-alice", run(l["lease"], "completed", nil))
		}
	}
	e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil)
	if e["state"] != "complete" {
		t.Fatalf("complete %v", e)
	}
	completed := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/events?limit=200&after=0", "t-alice", nil)
	if !strings.Contains(string(mustJSON(t, completed)), "experiment.completed") {
		t.Fatal("no experiment.completed event")
	}
}

func TestOverBudgetParksAndRetryReopens(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	trials := lease["trials"].([]any)
	spent := trials[0].(map[string]any)
	x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+spent["id"].(string)+"/submit", "t-alice",
		run(lease["lease"], "over_budget", map[string]any{"failure": map[string]string{"code": "budget_exhausted"}}))
	for _, raw := range trials[1:] {
		x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+raw.(map[string]any)["id"].(string)+"/submit", "t-alice", run(lease["lease"], "completed", nil))
	}
	parked := x.must(http.StatusOK, http.MethodGet, "/v1/cells/"+spent["cell"].(string), "t-alice", nil)
	if parked["state"] != "over_budget" {
		t.Fatalf("parked %v", parked)
	}
	// Every other cell runs to its end.
	for range 8 {
		for _, cand := range []string{f.high, f.low} {
			a := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": cand})
			if a.status != http.StatusOK {
				continue
			}
			l := a.json(t)
			for _, raw := range l["trials"].([]any) {
				x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+raw.(map[string]any)["id"].(string)+"/submit", "t-alice", run(l["lease"], "completed", nil))
			}
		}
	}
	if e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil); e["state"] != "complete" {
		t.Fatalf("complete with a parked cell %v", e)
	}
	finals := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons?final=true&automatic=true", "t-alice", nil)
	if len(finals["items"].([]any)) != 1 {
		t.Fatalf("the final automatic comparison %v", finals)
	}
	// A retry of the parked cell reopens the experiment, with a new
	// comparison of the pair beside the final one.
	if c := x.must(http.StatusOK, http.MethodPost, "/v1/cells/"+spent["cell"].(string)+"/retry", "t-alice", nil); c["state"] != "smoking" && c["state"] != "running" {
		t.Fatalf("the retried cell %v", c)
	}
	if e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil); e["state"] != "running" {
		t.Fatalf("reopened %v", e)
	}
	all := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/comparisons", "t-alice", nil)
	if len(all["items"].([]any)) != 2 {
		t.Fatalf("comparisons after the retry %v", all)
	}
}

func TestSubmitAndImportRefusals(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 1, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	tr := lease["trials"].([]any)[0].(map[string]any)
	path := "/v1/trials/" + tr["id"].(string) + "/submit"
	bad := []struct {
		extra  map[string]any
		status int
		code   string
	}{
		{map[string]any{"outcome": "canceled"}, 400, apierr.CodeInvalidField},
		{map[string]any{"outcome": "infra_failed"}, 400, apierr.CodeMissingField},
		{map[string]any{"failure": map[string]string{"code": "x"}}, 400, apierr.CodeInvalidField},
		{map[string]any{"outcome": "infra_failed", "failure": map[string]string{"detail": "x"}}, 400, apierr.CodeMissingField},
		{map[string]any{"started_at": nil}, 400, apierr.CodeMissingField},
		{map[string]any{"agent_ended_at": nil}, 400, apierr.CodeMissingField},
		{map[string]any{"agent_ended_at": time.Now().Add(-2 * time.Hour)}, 400, apierr.CodeInvalidField},
		{map[string]any{"ref": strings.Repeat("r", 256)}, 400, apierr.CodeInvalidField},
		{map[string]any{"outputs": []map[string]any{{"path": "other.md", "arca": map[string]string{"path": "a", "digest": "sha256:" + strings.Repeat("a", 64)}}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"outputs": []map[string]any{{"path": "notes.md"}}}, 400, apierr.CodeMissingField},
		{map[string]any{"outputs": []map[string]any{{"path": "notes.md", "data": "aGk="}}}, 400, apierr.CodeUnknownField},
		{map[string]any{"outputs": []map[string]any{{"path": "notes.md", "arca": map[string]string{"path": "../a", "digest": "sha256:" + strings.Repeat("a", 64)}}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"outputs": []map[string]any{{"path": "notes.md", "arca": map[string]string{"path": "a", "digest": "md5:1"}}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"outputs": []map[string]any{{"path": "notes.md", "arca": map[string]string{"path": "a", "digest": "sha256:" + strings.Repeat("a", 64)}}, {"path": "notes.md", "arca": map[string]string{"path": "b", "digest": "sha256:" + strings.Repeat("b", 64)}}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"final_message": "done"}, 400, apierr.CodeUnknownField},
		{map[string]any{"lux_requests": []string{""}}, 400, apierr.CodeInvalidField},
		{map[string]any{"service_versions": map[string]string{"lectio": ""}}, 400, apierr.CodeInvalidField},
		{map[string]any{"service_usage": map[string]any{"lectio": map[string]any{"cost_usd_micro": -1}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"service_usage": map[string]any{"lectio": map[string]any{"cost_usd_micro": 1, "units": map[string]any{"pages": -1}}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"declared": map[string]string{"image": strings.Repeat("i", 256)}}, 400, apierr.CodeInvalidField},
		{map[string]any{"declared": map[string]string{"harness_version": "9.9.9"}}, 422, apierr.CodeProvenanceMismatch},
		{map[string]any{"measurements": []map[string]any{{"metric": "nope", "value": 1}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"measurements": []map[string]any{{"metric": "completed", "value": true}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"measurements": []map[string]any{{"metric": "quality", "value": 2}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"measurements": []map[string]any{{"metric": "quality", "value": 0.5}, {"metric": "quality", "value": 0.6}}}, 400, apierr.CodeInvalidField},
		{map[string]any{"measurements": []map[string]any{{"metric": "quality", "value": 0.5, "rationale": strings.Repeat("r", 4097)}}}, 422, apierr.CodeLimitExceeded},
		{map[string]any{"outcome": "infra_failed", "failure": map[string]string{"code": "x"}, "measurements": []map[string]any{{"metric": "quality", "value": 0.5}}}, 400, apierr.CodeInvalidField},
	}
	for i, b := range bad {
		body := run(lease["lease"], "completed", b.extra)
		for k, v := range b.extra {
			if v == nil {
				delete(body, k)
			}
		}
		d := x.refuse(b.status, b.code, http.MethodPost, path, "t-alice", body)
		if b.code == apierr.CodeProvenanceMismatch && d["fields"].([]any)[0] != "version" {
			t.Errorf("case %d: fields %v", i, d["fields"])
		}
	}
	x.refuse(400, apierr.CodeMissingField, http.MethodPost, path, "t-alice", run("", "completed", nil))
	x.refuse(404, apierr.CodeTrialNotFound, http.MethodPost, path, "t-bob", run(lease["lease"], "completed", nil))
	x.refuse(404, apierr.CodeTrialNotFound, http.MethodPost, "/v1/trials/"+id.New(id.Trial)+"/submit", "t-alice", run(lease["lease"], "completed", nil))

	// The lease's own refusals.
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{map[string]any{}, 400, apierr.CodeMissingField},
		{map[string]any{"candidate": f.high, "renew": "lse_x"}, 400, apierr.CodeInvalidField},
		{map[string]any{"candidate": f.high, "max": 0}, 400, apierr.CodeInvalidField},
		{map[string]any{"candidate": "harness-high"}, 422, apierr.CodeUnpinnedReference},
		{map[string]any{"candidate": f.low}, 422, apierr.CodeNotInExperiment},
	} {
		x.refuse(c.status, c.code, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", c.body)
	}
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, "/v1/experiments/"+id.New(id.Experiment)+"/work/lease", "t-alice", map[string]any{"candidate": f.high})

	// Imports into an experiment in mode execute are wrong_mode; the
	// import's own refusals.
	x.refuse(409, apierr.CodeWrongMode, http.MethodPost, "/v1/experiments/"+eid+"/imports", "t-alice", pastRun(f.taskA, f.high, "r", nil))
	imp := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 1, "budget": map[string]any{"name": "study"}, "mode": "import",
	})["id"].(string)
	x.refuse(409, apierr.CodeWrongMode, http.MethodPost, "/v1/experiments/"+imp+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	ip := "/v1/experiments/" + imp + "/imports"
	for _, c := range []struct {
		body   map[string]any
		status int
		code   string
	}{
		{pastRun(f.taskA, f.high, "r", map[string]any{"executor": ""}), 400, apierr.CodeMissingField},
		{pastRun(f.taskA, f.high, "r", map[string]any{"executor": "docker"}), 400, apierr.CodeInvalidField},
		{pastRun(f.taskA, f.high, "r", map[string]any{"executor": "topos"}), 422, apierr.CodeCoreNotConfigured},
		{pastRun(f.taskA, f.high, "r", map[string]any{"executor_ref": "ses_1"}), 400, apierr.CodeInvalidField},
		{pastRun("todo-app", f.high, "r", nil), 422, apierr.CodeUnpinnedReference},
		{pastRun(f.taskA, "harness-high", "r", nil), 422, apierr.CodeUnpinnedReference},
		{pastRun(f.taskA, f.low, "r", nil), 422, apierr.CodeNotInExperiment},
		{pastRun(f.taskA, f.high, "r", map[string]any{"ended_at": nil}), 400, apierr.CodeMissingField},
		{pastRun(f.taskA, f.high, "r", map[string]any{"ended_at": time.Date(2026, 3, 2, 14, 2, 0, 0, time.UTC)}), 400, apierr.CodeInvalidField},
		{pastRun(f.taskA, f.high, "r", map[string]any{"ended_at": time.Now().Add(time.Hour)}), 400, apierr.CodeInvalidField},
		{pastRun(f.taskA, f.high, "r", map[string]any{"declared": map[string]string{"image": "x"}}), 400, apierr.CodeMissingField},
		{pastRun(f.taskA, f.high, "r", map[string]any{"declared": map[string]string{"harness_version": "1.0"}}), 422, apierr.CodeProvenanceMismatch},
	} {
		for k, v := range c.body {
			if v == nil {
				delete(c.body, k)
			}
		}
		x.refuse(c.status, c.code, http.MethodPost, ip, "t-alice", c.body)
	}
	x.refuse(404, apierr.CodeExperimentNotFound, http.MethodPost, ip, "t-bob", pastRun(f.taskA, f.high, "r", nil))
	x.must(http.StatusCreated, http.MethodPost, ip, "t-alice", pastRun(f.taskA, f.high, "run-1", nil))
	x.refuse(409, apierr.CodeAlreadyImported, http.MethodPost, ip, "t-alice", pastRun(f.taskA, f.high, "run-1", nil))
	x.refuse(409, apierr.CodeCellFull, http.MethodPost, ip, "t-alice", pastRun(f.taskA, f.high, "run-2", nil))
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+imp+"/close", "t-alice", nil)
	if again := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+imp+"/close", "t-alice", nil); again["state"] != "complete" {
		t.Fatalf("a repeated close %v", again)
	}
	x.refuse(409, apierr.CodeExperimentFinished, http.MethodPost, ip, "t-alice", pastRun(f.taskA, f.high, "run-3", nil))
	x.refuse(409, apierr.CodeExperimentFinished, http.MethodPost, "/v1/experiments/"+imp+"/cancel", "t-alice", nil)
	canceled := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "budget": map[string]any{"name": "study"}, "mode": "import",
	})["id"].(string)
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+canceled+"/cancel", "t-alice", nil)
	x.refuse(409, apierr.CodeExperimentFinished, http.MethodPost, "/v1/experiments/"+canceled+"/close", "t-alice", nil)
}

// An experiment an authorizer stops running parks its cells canceled with
// run_denied, and one it gives no decision on waits.
func TestRunDeniedAndUnavailable(t *testing.T) {
	gate := &runGate{}
	x := newAPI(t, func(s *Server) {
		gate.inner = s.Authz
		s.Authz = gate
		s.Engine.Authz = gate
	})
	f := x.fixture()
	gate.set("unavailable")
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	if e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil); e["state"] != "pending" {
		t.Fatalf("an outage holds the rollouts %v", e)
	}
	gate.set("")
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high, "max": 1})
	gate.set("unavailable")
	if a := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high}); a.status != http.StatusNoContent {
		t.Fatalf("an outage hands out nothing: %d %s", a.status, a.body)
	}
	gate.set("deny")
	if a := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high}); a.status != http.StatusNoContent {
		t.Fatalf("a deny hands out nothing: %d %s", a.status, a.body)
	}
	cells := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/cells?state=canceled", "t-alice", nil)["items"].([]any)
	if len(cells) != 1 || cells[0].(map[string]any)["parked_reason"] != work.ReasonRunDenied {
		t.Fatalf("parked %v", cells)
	}
}

// runGate answers experiment.run as a test sets it: allow when empty,
// deny, or unavailable; every other question goes to the policy it wraps.
type runGate struct {
	inner access.Authorizer
	mu    sync.Mutex
	mode  string
}

func (g *runGate) set(mode string) { g.mu.Lock(); g.mode = mode; g.mu.Unlock() }

func (g *runGate) Authorize(ctx context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	g.mu.Lock()
	mode := g.mode
	g.mu.Unlock()
	if q.Action == authorizer.ActionExperimentRun {
		switch mode {
		case "unavailable":
			return access.Decision{}, access.ErrUnavailable
		case "deny":
			return access.Decision{Reason: "stopped"}, nil
		}
	}
	return g.inner.Authorize(ctx, c, q)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
