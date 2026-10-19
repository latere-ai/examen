// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	keysrc "latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/work"
)

// newKeyedAPI is newClockedAPI with a key source the test holds, which the
// server and its engine both use.
func newKeyedAPI(t *testing.T) (*client, *clock, *keysrc.Memory, *work.Engine) {
	t.Helper()
	return newKeyedAPIAt(t, false)
}

// newKeyedAPIAt is newKeyedAPI over a store reached directly, or through a
// pooler in transaction mode with every statement in exec mode, where a
// parameter travels as text.
func newKeyedAPIAt(t *testing.T, pooled bool) (*client, *clock, *keysrc.Memory, *work.Engine) {
	t.Helper()
	clk := &clock{now: time.Now().UTC().Truncate(time.Second)}
	var st *postgres.Store
	if pooled {
		_, at := pgtest.Database(t)
		var err error
		if st, _, err = postgres.Open(t.Context(), at+"&default_query_exec_mode=exec", postgres.Options{Now: clk.read}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := st.Close(); err != nil {
				t.Error(err)
			}
		})
	} else {
		st = pgtest.Open(t, false, postgres.Options{Now: clk.read})
	}
	src := keysrc.NewMemory()
	var engine *work.Engine
	h := newHarness(t, st, routes, func(s *Server) {
		engine = &work.Engine{Store: st, Authz: s.Authz, Analyzer: compare.NotComputed{}, Log: s.Log, Keys: src, Now: clk.read}
		s.Engine, s.Keys = engine, src
	})
	return &client{harness: h, st: st}, clk, src, engine
}

// record is one of Lux's records of a call made with an experiment's Key.
func record(t *testing.T, id string, at time.Time, label, model, upstream string, cost int64, priced bool) keysrc.Record {
	t.Helper()
	labels := map[string]string{}
	if label != "" {
		labels[keysrc.LabelTrial] = label
	}
	raw, err := json.Marshal(map[string]any{"id": id, "at": at, "model": map[string]string{"name": model}, "upstreamModel": upstream,
		"status": "ok", "tokens": map[string]int64{"input": 100, "cachedInput": 20, "output": 50, "reasoning": 10},
		"cost": map[string]any{"amount": cost, "currency": "USD", "priced": priced}, "requestLabels": labels})
	if err != nil {
		t.Fatal(err)
	}
	r, err := keysrc.ParseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestTheExperimentKey is the external Key of specs/005-execution.md: made
// at the create, handed on with each lease beside each trial's label and
// never shown in a read of the experiment; a run's cost, sent and observed
// layers read from the records its label marks, a call no run claims
// counted toward the experiment alone; the Budget's figures on the
// experiment's answer; and the Key released when the experiment ends.
func TestTheExperimentKey(t *testing.T) { experimentKey(t, false) }

// TestTheExperimentKeyThroughAPooler is the same through a pooler in exec
// mode, where a record's JSON bound as bytes would not reach its column.
func TestTheExperimentKeyThroughAPooler(t *testing.T) { experimentKey(t, true) }

func experimentKey(t *testing.T, pooled bool) {
	x, clk, src, engine := newKeyedAPIAt(t, pooled)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	key, ok := src.KeyOf(eid)
	asked := src.Asked()
	if !ok || len(asked) != 1 || asked[0].Purpose != keysrc.PurposeExternal || asked[0].Owner != alice || asked[0].Subject != alice || asked[0].Experiment != eid {
		t.Fatalf("the create asked %+v", asked)
	}
	a := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high, "max": 1})
	lease := a.json(t)
	if a.status != http.StatusOK || lease["model_key"] != key.Value || a.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("the lease answered %d %v, Cache-Control %q", a.status, lease, a.header.Get("Cache-Control"))
	}
	tr := lease["trials"].([]any)[0].(map[string]any)
	tid := tr["id"].(string)
	if tr["lux_labels"] != "examen.trial="+tid {
		t.Errorf("the trial's label is %v", tr["lux_labels"])
	}
	renewed := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"renew": lease["lease"]})
	if renewed["model_key"] != key.Value {
		t.Errorf("a renewal answered %v", renewed["model_key"])
	}
	// The run makes a labeled call, the setup a call of its own with no
	// label, and another call names a trial of no experiment of this Key.
	at := clk.read().Add(-time.Minute)
	src.AddRecord(eid, record(t, "req_1", at, tid, "anthropic/claude-haiku-5.5", "claude-haiku-5-5-20261001", 1234, true))
	src.AddRecord(eid, record(t, "req_2", at, "", "anthropic/claude-haiku-5.5", "claude-haiku-5-5-20261001", 66, true))
	src.AddRecord(eid, record(t, "req_3", at, "tri_elsewhere", "openai/gpt-5-mini", "gpt-5-mini", 0, false))
	x.must(http.StatusOK, http.MethodPost, "/v1/trials/"+tid+"/submit", "t-alice", run(lease["lease"], "completed", nil))
	src.SetBudget(eid, keysrc.Budget{Name: "study", Cap: 5_000_000, Spent: 1300, Remaining: 4_998_700, Currency: "USD"})

	// Before the settle delay the run's cost is unknown; after it, read.
	if err := engine.Usage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid, "t-alice", nil); got["observed"].(map[string]any)["cost_state"] != "unknown" {
		t.Fatalf("a run within the settle delay: %v", got["observed"])
	}
	clk.advance(limits.UsageEvery + limits.UsageSettleDelay)
	if err := engine.Usage(t.Context()); err != nil {
		t.Fatal(err)
	}
	got := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid, "t-alice", nil)
	observed := got["observed"].(map[string]any)
	if observed["cost_state"] != "known" || observed["cost_usd"] != 0.001234 || observed["input_tokens"] != float64(120) || observed["output_tokens"] != float64(50) {
		t.Errorf("the run's observed layer: %v", observed)
	}
	if served := observed["served"].([]any); len(served) != 1 || served[0].(map[string]any)["model"] != "claude-haiku-5-5-20261001" {
		t.Errorf("served %v", served)
	}
	sent := got["sent"].(map[string]any)
	if sent["requests"] != float64(1) || sent["by_setting"].([]any)[0].(map[string]any)["model"] != "anthropic/claude-haiku-5.5" {
		t.Errorf("sent %v", sent)
	}
	ms := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid+"/measurements?grader=builtin", "t-alice", nil)
	var cost any
	for _, m := range ms["items"].([]any) {
		if m.(map[string]any)["metric"] == "builtin:cost_usd" {
			cost = m.(map[string]any)["value"]
		}
	}
	if cost != 0.001234 {
		t.Errorf("the built-in cost is %v in %v", cost, ms["items"])
	}
	exp := x.do(http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil)
	if strings.Contains(string(exp.body), key.Value) {
		t.Fatal("a read of the experiment carries the Key's value")
	}
	e := exp.json(t)
	budget, usage := e["budget"].(map[string]any), e["usage"].(map[string]any)
	if budget["name"] != "study" || budget["cap_usd_micro"] != float64(5_000_000) || budget["spent_usd_micro"] != float64(1300) || budget["remaining_usd_micro"] != float64(4_998_700) {
		t.Errorf("budget %v", budget)
	}
	unattributed := usage["unattributed"].(map[string]any)
	if usage["requests"] != float64(3) || usage["cost_usd_micro"] != float64(1300) || usage["unpriced_requests"] != float64(1) ||
		unattributed["requests"] != float64(2) || unattributed["cost_usd_micro"] != float64(66) || unattributed["unpriced_requests"] != float64(1) {
		t.Errorf("usage %v", usage)
	}
	// A call recorded after the run settled unsettles it, and the next
	// read counts it.
	src.AddRecord(eid, record(t, "req_4", at, tid, "anthropic/claude-haiku-5.5", "claude-haiku-5-5-20261001", 1000, true))
	clk.advance(limits.UsageEvery)
	if err := engine.Usage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := x.must(http.StatusOK, http.MethodGet, "/v1/trials/"+tid, "t-alice", nil); got["observed"].(map[string]any)["cost_usd"] != 0.002234 {
		t.Errorf("a late call: %v", got["observed"])
	}
	if strings.Contains(x.log(), key.Value) {
		t.Fatal("a log line carries the Key's value")
	}
	// The cancel releases the Key.
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-alice", nil)
	if !src.Released(key.ID) {
		t.Error("the canceled experiment's Key was not released")
	}
}

// TestAKeySourceThatRefusesLeasesNothing: a lease asks for the Key before
// it takes a trial, so a refusal or an outage of the key source takes
// none, and a create the key source did not answer still stands.
func TestAKeySourceThatRefusesLeasesNothing(t *testing.T) {
	x, _, src, _ := newKeyedAPI(t)
	f := x.fixture()
	src.Err = &keysrc.RefusedError{Reason: "experiment_ended"}
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	details := x.refuse(http.StatusForbidden, apierr.CodeForbidden, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	if details["reason"] != "experiment_ended" {
		t.Errorf("details %v", details)
	}
	src.Err = keysrc.ErrUnavailable
	details = x.refuse(http.StatusServiceUnavailable, apierr.CodeOwnerTokensUnavailable, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	if details["reason"] != "key_source" {
		t.Errorf("details %v", details)
	}
	src.Err = keysrc.ErrBudgetExhausted
	x.refuse(http.StatusUnprocessableEntity, apierr.CodeBudgetRefused, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	trials := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid+"/trials?state=running", "t-alice", nil)
	if len(trials["items"].([]any)) != 0 {
		t.Fatalf("a refused lease took %v", trials["items"])
	}
	// The experiment's answer leaves the figures out and stands.
	src.Err = nil
	e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil)
	if _, ok := e["budget"].(map[string]any)["cap_usd_micro"]; ok || e["usage"] != nil {
		t.Errorf("an experiment whose key source answered no figures: %v %v", e["budget"], e["usage"])
	}
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	if lease["model_key"] == nil || lease["model_key"] == "" {
		t.Errorf("a lease once the key source answers: %v", lease)
	}
}

// TestALeaseWithNoKeySource hands on no Key, and still each trial's label.
func TestALeaseWithNoKeySource(t *testing.T) {
	x := newAPI(t, nil)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	lease := x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	tr := lease["trials"].([]any)[0].(map[string]any)
	if _, ok := lease["model_key"]; ok || tr["lux_labels"] != "examen.trial="+tr["id"].(string) {
		t.Errorf("a lease with no key source: %v", lease)
	}
	if e := x.must(http.StatusOK, http.MethodGet, "/v1/experiments/"+eid, "t-alice", nil); e["usage"] != nil {
		t.Errorf("usage with no key: %v", e["usage"])
	}
}

// TestARepeatedLeaseKeepsNoKey: a lease sent with an Idempotency-Key keeps
// its answer without the Key, and its repeat answers the Key afresh, with
// no-store.
func TestARepeatedLeaseKeepsNoKey(t *testing.T) {
	x, _, src, _ := newKeyedAPI(t)
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 2, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	key, _ := src.KeyOf(eid)
	body := map[string]any{"candidate": f.high, "max": 1}
	first := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", body, "Idempotency-Key", "lease-1")
	if first.status != http.StatusOK || first.json(t)["model_key"] != key.Value {
		t.Fatalf("the first lease: %d %s", first.status, first.body)
	}
	held, fresh, err := x.st.Idempotency().Begin(t.Context(), store.IdempotencyKey{Subject: alice, Key: "lease-1"}, time.Minute)
	if err != nil || fresh || !held.Done || strings.Contains(string(held.Body), key.Value) || !strings.Contains(string(held.Body), "lux_labels") {
		t.Fatalf("the kept answer: fresh %v, %s, %v", fresh, held.Body, err)
	}
	again := x.do(http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", body, "Idempotency-Key", "lease-1")
	got := again.json(t)
	if again.status != http.StatusOK || again.header.Get("Idempotent-Replayed") != "true" || again.header.Get("Cache-Control") != "no-store" ||
		got["model_key"] != key.Value || got["lease"] != first.json(t)["lease"] {
		t.Fatalf("the repeat: %d %v %v", again.status, again.header, got)
	}
	src.Err = keysrc.ErrUnavailable
	x.srv.keyCache().Forget(eid)
	x.refuse(http.StatusServiceUnavailable, apierr.CodeOwnerTokensUnavailable, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", body, "Idempotency-Key", "lease-1")
}
