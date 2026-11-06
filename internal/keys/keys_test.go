// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const bearer = "keys-bearer"

// endpoint is a key endpoint whose answer to each path a test chooses, and
// what it was asked.
func endpoint(t *testing.T, answer func(w http.ResponseWriter, r *http.Request, body map[string]any)) *Endpoint {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+bearer {
			t.Errorf("%s %s carried %q", r.Method, r.URL.Path, r.Header.Get("Authorization"))
		}
		body := map[string]any{}
		if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Errorf("a body that does not parse: %s", raw)
			}
		}
		answer(w, r, body)
	}))
	t.Cleanup(srv.Close)
	return NewEndpoint(srv.URL+"/internal/examen/keys/", func() string { return bearer }, srv.Client())
}

func write(w http.ResponseWriter, status int, body string) {
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// TestTheEndpointLeasesAKey posts specs/004's ask and reads its answer,
// with an expiry or with none for a Key that ends with its experiment.
func TestTheEndpointLeasesAKey(t *testing.T) {
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	var asked map[string]any
	e := endpoint(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		asked = body
		if r.Method != http.MethodPost || r.URL.Path != "/internal/examen/keys" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		write(w, 200, fmt.Sprintf(`{"key":"lux_v","key_id":"k1","expires_at":%q,"remaining":41250000,"currency":"USD"}`, expires.Format(time.RFC3339)))
	})
	l, err := e.Lease(t.Context(), Request{Owner: "o", Subject: "s", Experiment: "exp_1", Purpose: PurposeExternal, Labels: map[string]string{LabelExperiment: "exp_1"}})
	if err != nil || l.Value != "lux_v" || l.ID != "k1" || !l.Expires.Equal(expires) || l.Remaining != 41250000 || l.Currency != "USD" {
		t.Fatalf("lease %v %+v", err, l)
	}
	if asked["purpose"] != PurposeExternal || asked["experiment"] != "exp_1" || asked["owner"] != "o" || asked["subject"] != "s" ||
		len(asked["models"].([]any)) != 0 || asked["labels"].(map[string]any)[LabelExperiment] != "exp_1" {
		t.Errorf("asked %v", asked)
	}
	if _, ok := asked["ttl"]; ok {
		t.Errorf("an external Key's ask carries a ttl: %v", asked)
	}
	if s := fmt.Sprintf("%v %+v %#v", l, l, l); strings.Contains(s, "lux_v") {
		t.Errorf("a formatted lease shows its value: %s", s)
	}
	none := endpoint(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		write(w, 200, `{"key":"lux_v","key_id":"k1","remaining":0,"currency":"USD"}`)
	})
	if l, err := none.Lease(t.Context(), Request{Purpose: PurposeExternal}); err != nil || !l.Expires.IsZero() {
		t.Errorf("a Key with no expiry: %v %v", err, l.Expires)
	}
}

// TestTheEndpointsRefusals reads each answer of specs/004's table.
func TestTheEndpointsRefusals(t *testing.T) {
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	for _, c := range []struct {
		name   string
		status int
		body   string
		want   func(error) bool
	}{
		{"402", 402, `{}`, func(err error) bool { return errors.Is(err, ErrBudgetExhausted) }},
		{"403 with a reason", 403, `{"reason":"experiment_ended"}`, func(err error) bool {
			var r *RefusedError
			return errors.As(err, &r) && r.Reason == "experiment_ended"
		}},
		{"403 with an envelope", 403, `{"error":{"code":"plan_suspended"}}`, func(err error) bool {
			var r *RefusedError
			return errors.As(err, &r) && r.Reason == "plan_suspended"
		}},
		{"403 with none", 403, `not json`, func(err error) bool {
			var r *RefusedError
			return errors.As(err, &r) && r.Reason == ReasonKeyRefused
		}},
		{"503", 503, `{}`, func(err error) bool { return errors.Is(err, ErrUnavailable) }},
		{"a body that does not parse", 200, `{`, func(err error) bool { return errors.Is(err, ErrUnavailable) }},
		{"a member missing", 200, `{"key":"lux_v","key_id":"k1","currency":"USD"}`, func(err error) bool { return errors.Is(err, ErrUnavailable) }},
		{"an expiry past", 200, `{"key":"lux_v","key_id":"k1","remaining":1,"currency":"USD","expires_at":"` + past + `"}`, func(err error) bool { return errors.Is(err, ErrUnavailable) }},
		{"404", 404, `{}`, func(err error) bool { return errors.Is(err, ErrNoKey) }},
	} {
		e := endpoint(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) { write(w, c.status, c.body) })
		_, err := e.Lease(t.Context(), Request{Purpose: PurposeExternal})
		if !c.want(err) {
			t.Errorf("%s: %v", c.name, err)
		}
		if err != nil && (strings.Contains(err.Error(), "127.0.0.1") || strings.Contains(err.Error(), bearer)) {
			t.Errorf("%s: the error names the endpoint or its bearer: %v", c.name, err)
		}
	}
}

// TestTheEndpointRetriesAConnectionOnce: a refused connection is tried
// again once, and a second refusal is an outage that names no address.
func TestTheEndpointRetriesAConnectionOnce(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	var dials atomic.Int32
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, network, a string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, a)
	}}}
	e := NewEndpoint("http://"+addr, func() string { return bearer }, client)
	_, err = e.Lease(t.Context(), Request{Purpose: PurposeExternal})
	if !errors.Is(err, ErrUnavailable) || dials.Load() != 2 || strings.Contains(err.Error(), addr) {
		t.Errorf("%v after %d dials", err, dials.Load())
	}
}

// TestTheEndpointsBudgetReleaseAndUsage reads the Budget's figures,
// releases a Key, a Key already gone among them, and reads a page of
// records less nothing it needs.
func TestTheEndpointsBudgetReleaseAndUsage(t *testing.T) {
	from, to := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	e := endpoint(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.Method + " " + r.URL.Path {
		case "POST /internal/examen/keys/budget":
			if body["budget"] != "eb-1" || body["experiment"] != "exp_1" {
				t.Errorf("budget asked %v", body)
			}
			write(w, 200, `{"name":"eb-1","cap":5000000,"spent":1250000,"remaining":3750000,"currency":"USD"}`)
		case "DELETE /internal/examen/keys/k1":
			w.WriteHeader(http.StatusNoContent)
		case "DELETE /internal/examen/keys/gone":
			write(w, 404, `{"error":{"code":"key_not_found"}}`)
		case "POST /internal/examen/keys/usage":
			if body["from"] != from.Format(time.RFC3339) || body["to"] != to.Format(time.RFC3339) || body["cursor"] != "c1" {
				t.Errorf("usage asked %v", body)
			}
			write(w, 200, `{"records":[{"id":"req_1","at":"2026-10-19T09:30:00Z","key":{"id":"key_1"},"model":{"name":"anthropic/claude-haiku-5.5","id":"mdl_1"},
				"provider":{"name":"anthropic"},"upstreamModel":"claude-haiku-5-5","status":"ok","error":"",
				"tokens":{"input":10,"output":5,"cachedInput":2,"cacheWrite":0,"reasoning":1,"estimated":false},
				"cost":{"amount":123,"currency":"USD","priced":true},"requestLabels":{"examen.trial":"tri_1"}}],"next_cursor":"c2"}`)
		default:
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
	})
	b, err := e.Budget(t.Context(), Request{Experiment: "exp_1", Budget: "eb-1"})
	if err != nil || b != (Budget{Name: "eb-1", Cap: 5000000, Spent: 1250000, Remaining: 3750000, Currency: "USD"}) {
		t.Errorf("budget %v %+v", err, b)
	}
	for _, id := range []string{"k1", "gone"} {
		if err := e.Release(t.Context(), id); err != nil {
			t.Errorf("release %s: %v", id, err)
		}
	}
	page, err := e.Usage(t.Context(), UsageRequest{Experiment: "exp_1", From: from, To: to, Cursor: "c1"})
	if err != nil || page.NextCursor != "c2" || len(page.Records) != 1 {
		t.Fatalf("usage %v %+v", err, page)
	}
	r := page.Records[0]
	if r.RequestID != "req_1" || r.SentModel != "anthropic/claude-haiku-5.5" || r.Served() != "claude-haiku-5-5" || r.Provider != "anthropic" ||
		r.Labels[LabelTrial] != "tri_1" || r.Cost != 123 || !r.Priced || r.Tokens.CachedInput != 2 || r.Tokens.Reasoning != 1 || len(r.Raw) == 0 {
		t.Errorf("record %+v", r)
	}
}

// TestParseRecord refuses a record with no id, no time or a status Lux
// does not write, and a record no model served is served by none.
func TestParseRecord(t *testing.T) {
	for name, raw := range map[string]string{
		"no id":      `{"at":"2026-10-19T09:30:00Z","status":"ok"}`,
		"no time":    `{"id":"req_1","status":"ok"}`,
		"a status":   `{"id":"req_1","at":"2026-10-19T09:30:00Z","status":"done"}`,
		"not a JSON": `[`,
	} {
		if _, err := ParseRecord(jsontext.Value(raw)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
	r, err := ParseRecord(jsontext.Value(`{"id":"req_1","at":"2026-10-19T09:30:00Z","status":"refused","error":"model_not_found","model":{"name":""}}`))
	if err != nil || r.Served() != "" || r.Status != StatusRefused || r.Error != "model_not_found" {
		t.Errorf("%v %+v", err, r)
	}
}

// TestTheCacheAsksOnceWithinItsTTL: within TTL the cache answers the Key it
// holds, after it or a Forget it asks again, and a failure is not held.
func TestTheCacheAsksOnceWithinItsTTL(t *testing.T) {
	src := NewMemory()
	now := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	c := &Cache{Source: src, TTL: time.Minute, Now: func() time.Time { return now }}
	r := Request{Experiment: "exp_1", Purpose: PurposeExternal}
	for range 3 {
		if _, err := c.External(t.Context(), r); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(src.Asked()); n != 1 {
		t.Fatalf("asked %d times within the TTL", n)
	}
	now = now.Add(time.Minute)
	if _, err := c.External(t.Context(), r); err != nil || len(src.Asked()) != 2 {
		t.Fatalf("after the TTL: %v, asked %d", err, len(src.Asked()))
	}
	c.Forget("exp_1")
	src.Err = ErrUnavailable
	if _, err := c.External(t.Context(), r); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a failure: %v", err)
	}
	src.Err = nil
	if _, err := c.External(t.Context(), r); err != nil || len(src.Asked()) != 4 {
		t.Fatalf("after a failure: %v, asked %d", err, len(src.Asked()))
	}
}

// TestTheMemorySource: one Key per experiment until it is released, one
// per judge beside it, none for another purpose, the figures a test set,
// its records a page at a time behind a cursor, and its error answered by
// every call.
func TestTheMemorySource(t *testing.T) {
	m := NewMemory()
	ctx := t.Context()
	if _, err := m.Lease(ctx, Request{Experiment: "exp_1", Purpose: PurposeReport}); err == nil {
		t.Error("a report's Key was answered")
	}
	judge := func(grader string) Lease {
		l, err := m.Lease(ctx, Request{Experiment: "exp_1", Purpose: PurposeJudge, Labels: map[string]string{"examen.grader": grader}})
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	j1, j1again, j2 := judge("grd_1"), judge("grd_1"), judge("grd_2")
	if j1.ID != j1again.ID || j1.ID == j2.ID {
		t.Errorf("judges' Keys: %v %v %v", j1.ID, j1again.ID, j2.ID)
	}
	a, _ := m.Lease(ctx, Request{Experiment: "exp_1", Purpose: PurposeExternal})
	b, _ := m.Lease(ctx, Request{Experiment: "exp_1", Purpose: PurposeExternal})
	if a.ID != b.ID || a.ID == j1.ID || len(m.Asked()) != 6 {
		t.Fatalf("two asks answered %v and %v", a.ID, b.ID)
	}
	if err := m.Release(ctx, a.ID); err != nil || !m.Released(a.ID) {
		t.Fatalf("release: %v", err)
	}
	if c, _ := m.Lease(ctx, Request{Experiment: "exp_1", Purpose: PurposeExternal}); c.ID == a.ID {
		t.Error("a released Key was answered again")
	}
	if _, err := m.Budget(ctx, Request{Experiment: "exp_1"}); !errors.Is(err, ErrNoKey) {
		t.Errorf("figures never set: %v", err)
	}
	m.SetBudget("exp_1", Budget{Name: "b", Cap: 1, Currency: "USD"})
	if got, err := m.Budget(ctx, Request{Experiment: "exp_1"}); err != nil || got.Cap != 1 {
		t.Errorf("figures: %+v %v", got, err)
	}
	if _, err := m.Usage(ctx, UsageRequest{Experiment: "exp_none"}); !errors.Is(err, ErrNoKey) {
		t.Errorf("records of an experiment with no Key: %v", err)
	}
	at := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	for i := range 3 {
		m.AddRecord("exp_1", Record{RequestID: fmt.Sprintf("req_%d", i), At: at})
	}
	m.PageSize = 2
	window := UsageRequest{Experiment: "exp_1", From: at.Add(-time.Minute), To: at.Add(time.Minute)}
	first, err := m.Usage(ctx, window)
	if err != nil || len(first.Records) != 2 || first.NextCursor == "" {
		t.Fatalf("the first page: %+v %v", first, err)
	}
	window.Cursor = first.NextCursor
	if last, err := m.Usage(ctx, window); err != nil || len(last.Records) != 1 || last.NextCursor != "" {
		t.Fatalf("the last page: %+v %v", last, err)
	}
	window.Cursor = "not-a-cursor"
	if _, err := m.Usage(ctx, window); !errors.Is(err, ErrUnavailable) {
		t.Errorf("a cursor it did not answer: %v", err)
	}
	if l, ok := m.KeyOf("exp_1"); !ok || l.ID == "" {
		t.Error("the experiment's Key")
	}
	m.Err = ErrUnavailable
	_, lerr := m.Lease(ctx, Request{Experiment: "exp_1", Purpose: PurposeExternal})
	_, berr := m.Budget(ctx, Request{Experiment: "exp_1"})
	_, uerr := m.Usage(ctx, window)
	if lerr == nil || m.Release(ctx, "k") == nil || berr == nil || uerr == nil {
		t.Error("an error set is not answered by every call")
	}
}
