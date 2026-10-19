// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

// stepClock is a clock a test moves.
type stepClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *stepClock) read() time.Time                 { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *stepClock) add(d time.Duration)             { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
func (c *stepClock) set(t time.Time)                 { c.mu.Lock(); c.now = t; c.mu.Unlock() }
func (c *stepClock) since(d time.Duration) time.Time { return c.read().Add(-d) }

func callRecord(t *testing.T, id string, at time.Time, label string, cost int64) keys.Record {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"id": id, "at": at, "model": map[string]string{"name": "anthropic/claude-haiku-5.5"},
		"upstreamModel": "claude-haiku-5-5", "status": "ok", "tokens": map[string]int64{"input": 10, "output": 5},
		"cost": map[string]any{"amount": cost, "currency": "USD", "priced": true}, "requestLabels": map[string]string{keys.LabelTrial: label}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := keys.ParseRecord(raw)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestUsageReadsUntilTheEndAndReleases: an experiment's records are read
// every USAGE_EVERY and no sooner, its reads end once one covers its end
// with the lookback past it, a key source that does not answer is tried
// again, and the Key of an ended experiment is released once.
func TestUsageReadsUntilTheEndAndReleases(t *testing.T) {
	clk := &stepClock{now: time.Now().UTC().Truncate(time.Second)}
	st := pgtest.Open(t, false, postgres.Options{Now: clk.read})
	eid, _ := seed(t, st)
	src := keys.NewMemory()
	e := &Engine{Store: st, Authz: &gate{}, Analyzer: figures{}, Keys: src, Now: clk.read}
	ctx := t.Context()
	l, err := src.Lease(ctx, keys.Request{Experiment: eid, Purpose: keys.PurposeExternal})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Usage().Hold(ctx, eid, owner, l.ID); err != nil {
		t.Fatal(err)
	}
	if id, err := st.Usage().KeyID(ctx, eid); err != nil || id != l.ID {
		t.Fatalf("the held key: %q %v", id, err)
	}
	src.AddRecord(eid, callRecord(t, "req_1", clk.since(time.Minute), "", 70))
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	// A second read within USAGE_EVERY reads nothing new.
	src.AddRecord(eid, callRecord(t, "req_2", clk.since(time.Minute), "", 30))
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := st.Usage().Summary(ctx, eid)
	if err != nil || u.Requests != 1 || u.Unattributed.CostUSDMicro != 70 || u.ReadTo == nil {
		t.Fatalf("after one read: %+v %v", u, err)
	}
	clk.add(limits.UsageEvery)
	src.Err = keys.ErrUnavailable
	if err := e.Usage(ctx); !errors.Is(err, keys.ErrUnavailable) {
		t.Fatalf("a key source that does not answer: %v", err)
	}
	src.Err = nil
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	if u, _ := st.Usage().Summary(ctx, eid); u.Requests != 2 {
		t.Fatalf("the read after the outage: %+v", u)
	}
	// Nothing is released while the experiment runs; once it ends, its Key
	// is, once, and its reads go on until the lookback past the end.
	if err := e.Release(ctx, ""); err != nil || src.Released(l.ID) {
		t.Fatalf("a running experiment's key released: %v", err)
	}
	if _, err := st.Experiments().Cancel(ctx, eid); err != nil {
		t.Fatal(err)
	}
	src.Err = keys.ErrUnavailable
	if err := e.Tick(ctx); !errors.Is(err, keys.ErrUnavailable) {
		t.Fatalf("a release the key source does not answer: %v", err)
	}
	src.Err = nil
	if err := e.Release(ctx, eid); err != nil || !src.Released(l.ID) {
		t.Fatalf("the ended experiment's key: %v, released %v", err, src.Released(l.ID))
	}
	if held, err := st.Usage().Unreleased(ctx, "", 10); err != nil || len(held) != 0 {
		t.Fatalf("still held: %+v %v", held, err)
	}
	clk.add(limits.UsageEvery)
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	clk.set(clk.read().Add(limits.UsageLookback + limits.UsageEvery))
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	if due, err := st.Usage().Due(ctx, clk.read().Add(time.Hour), 10); err != nil || len(due) != 0 {
		t.Fatalf("an ended experiment read past its lookback is still due: %+v %v", due, err)
	}
	// An experiment that holds no Key has no usage, and an engine with no
	// key source reads and releases nothing.
	other, _ := seed(t, st)
	if u, err := st.Usage().Summary(ctx, other); err != nil || u != nil {
		t.Fatalf("an experiment with no key: %+v %v", u, err)
	}
	if id, err := st.Usage().KeyID(ctx, other); err != nil || id != "" {
		t.Fatalf("the key of an experiment with none: %q %v", id, err)
	}
	none := &Engine{Store: st, Authz: &gate{}, Analyzer: figures{}}
	if err := errors.Join(none.Usage(ctx), none.Release(ctx, "")); err != nil {
		t.Fatal(err)
	}
	if none.now().IsZero() {
		t.Fatal("no clock")
	}
}

// TestASettledTrialKeepsItsLabelsRun: a record whose label names a trial of
// the experiment is that trial's, settled into a cost, and a record a
// submitted request id names is claimed at the settle.
func TestASettledTrialKeepsItsLabelsRun(t *testing.T) {
	clk := &stepClock{now: time.Now().UTC().Truncate(time.Second)}
	st := pgtest.Open(t, false, postgres.Options{Now: clk.read})
	eid, cells := seed(t, st)
	src := keys.NewMemory()
	e := &Engine{Store: st, Authz: &gate{}, Analyzer: figures{}, Keys: src, Now: clk.read}
	ctx := t.Context()
	l, err := src.Lease(ctx, keys.Request{Experiment: eid, Purpose: keys.PurposeExternal})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Usage().Hold(ctx, eid, owner, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Usage().Hold(ctx, eid, owner, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Advance(ctx, eid); err != nil {
		t.Fatal(err)
	}
	held, err := st.Experiments().Lease(ctx, store.LeaseRequest{Experiment: eid, Subject: owner, Max: 1, Cells: cells})
	if err != nil || len(held.Trials) == 0 {
		t.Fatalf("lease: %+v %v", held, err)
	}
	tr := held.Trials[0]
	src.AddRecord(eid, callRecord(t, "req_a", clk.since(time.Minute), tr.ID, 500))
	src.AddRecord(eid, callRecord(t, "req_b", clk.since(time.Minute), "", 250))
	started := clk.since(2 * time.Minute)
	ended := clk.since(time.Minute)
	sub := store.Submission{Trial: tr.ID, Lease: held.Lease, Subject: owner, Digest: "sha256:" + strings.Repeat("0", 64), Outcome: store.Outcome{
		Outcome: objects.OutcomeCompleted, StartedAt: started, AgentEndedAt: ended,
		Observed: objects.Observed{CostState: objects.CostUnknown, LuxRequests: []string{"req_b"}}}}
	if _, err := st.Experiments().Submit(ctx, sub); err != nil {
		t.Fatal(err)
	}
	clk.add(limits.UsageEvery + limits.UsageSettleDelay)
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.Experiments().Trial(ctx, tr.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Observed.CostState != objects.CostKnown || got.Observed.CostUSD == nil || *got.Observed.CostUSD != 0.00075 || got.Sent == nil || got.Sent.Requests != 2 {
		t.Fatalf("the settled trial: %+v sent %+v", got.Observed, got.Sent)
	}
	if u, _ := st.Usage().Summary(ctx, eid); u.Unattributed.Requests != 0 {
		t.Fatalf("a record its submitted id names counts apart: %+v", u)
	}
	// A run that failed to start gets its layers from its records and no
	// built-in values: it is no finished trial.
	again, err := st.Experiments().Lease(ctx, store.LeaseRequest{Experiment: eid, Subject: owner, Max: 1, Cells: cells})
	if err != nil || len(again.Trials) == 0 {
		t.Fatalf("lease: %+v %v", again, err)
	}
	failed := again.Trials[0]
	src.AddRecord(eid, callRecord(t, "req_c", clk.since(time.Minute), failed.ID, 40))
	sub = store.Submission{Trial: failed.ID, Lease: again.Lease, Subject: owner, Digest: "sha256:" + strings.Repeat("1", 64), Outcome: store.Outcome{
		Outcome: objects.OutcomeInfraFailed, Failure: &objects.Failure{Code: "harness_exit"}, StartedAt: clk.since(2 * time.Minute), AgentEndedAt: clk.since(time.Minute),
		Observed: objects.Observed{CostState: objects.CostUnknown}}}
	if _, err := st.Experiments().Submit(ctx, sub); err != nil {
		t.Fatal(err)
	}
	clk.add(limits.UsageEvery + limits.UsageSettleDelay)
	if err := e.Usage(ctx); err != nil {
		t.Fatal(err)
	}
	got, err = st.Experiments().Trial(ctx, failed.ID)
	if err != nil || got.Observed.CostState != objects.CostKnown || got.Observed.CostUSD == nil || *got.Observed.CostUSD != 0.00004 {
		t.Fatalf("a failed run's layers: %+v %v", got.Observed, err)
	}
	ms, err := st.Experiments().Measurements(ctx, store.MeasurementFilter{Trial: failed.ID}, store.Page{Limit: 10})
	if err != nil || len(ms) != 0 {
		t.Fatalf("a failed run's measurements: %+v %v", ms, err)
	}
}
