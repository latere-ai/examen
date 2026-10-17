// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store"
)

// The same key from the same subject on the same route answers the first
// response again and makes nothing; a body that spells the same content
// another way is the same request; another body, or another route, is
// idempotency_key_reused; a request without the header is not
// deduplicated; a route that takes no key ignores it.
func TestIdempotency(t *testing.T) {
	made := &counter{}
	plain := route{method: http.MethodPost, path: "/plain", handle: func(_ *Server, w http.ResponseWriter, _ *http.Request, _ access.Caller) error {
		made.add()
		w.WriteHeader(http.StatusNoContent)
		return nil
	}}
	other := creating(made)
	other.path = "/others"
	h := newHarness(t, pgStore(t), []route{creating(made), other, plain}, nil)
	first := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"one"}`, idempotencyHeader, "key-1")
	if first.status != http.StatusCreated || first.header.Get(replayedHeader) != "" {
		t.Fatalf("the first: %d %s", first.status, first.body)
	}
	again := h.do(http.MethodPost, "/v1/things", "t-alice", "{ \"name\" : \"one\" }", idempotencyHeader, "key-1")
	if again.status != http.StatusCreated || string(again.body) != string(first.body) || again.header.Get(replayedHeader) != "true" ||
		again.header.Get("Content-Type") != first.header.Get("Content-Type") {
		t.Fatalf("the repeat: %d %s %v", again.status, again.body, again.header)
	}
	if made.n != 1 {
		t.Fatalf("%d things made", made.n)
	}
	for name, a := range map[string]answer{
		"another body":  h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"two"}`, idempotencyHeader, "key-1"),
		"another route": h.do(http.MethodPost, "/v1/others", "t-alice", `{"name":"one"}`, idempotencyHeader, "key-1"),
	} {
		if code, d := a.envelope(t); a.status != http.StatusConflict || code != apierr.CodeIdempotencyKeyReused || d["field"] != idempotencyHeader {
			t.Errorf("%s: %d %s", name, a.status, a.body)
		}
	}
	// Another subject's key of the same spelling is its own.
	if a := h.do(http.MethodPost, "/v1/things", "t-bob", `{"name":"one"}`, idempotencyHeader, "key-1"); a.status != http.StatusCreated || a.header.Get(replayedHeader) != "" {
		t.Fatalf("bob: %d %s", a.status, a.body)
	}
	// Without the header, every request runs.
	for range 2 {
		if a := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"one"}`); a.status != http.StatusCreated {
			t.Fatalf("no key: %d", a.status)
		}
	}
	// A route that takes no key runs every time.
	for range 2 {
		if a := h.do(http.MethodPost, "/v1/plain", "t-alice", nil, idempotencyHeader, "key-2"); a.status != http.StatusNoContent {
			t.Fatalf("a route with no key: %d", a.status)
		}
	}
	if made.n != 6 {
		t.Fatalf("%d things made", made.n)
	}
	// A refusal is an answer too, and replays as one.
	refused := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"x","colour":"red"}`, idempotencyHeader, "key-3")
	replayed := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"x","colour":"red"}`, idempotencyHeader, "key-3")
	if refused.status != http.StatusBadRequest || replayed.status != http.StatusBadRequest || replayed.header.Get(replayedHeader) != "true" {
		t.Fatalf("a refusal: %d %d", refused.status, replayed.status)
	}
	// A body that is not JSON has a digest of its bytes.
	if a := h.do(http.MethodPost, "/v1/things", "t-alice", `not json`, idempotencyHeader, "key-4"); a.status != http.StatusBadRequest {
		t.Fatalf("not json: %d", a.status)
	}
}

// A key is 1 to IDEMPOTENCY_KEY_MAX_BYTES bytes of visible ASCII; a body
// past the bound is refused before a key is reserved.
func TestAnIdempotencyKeyBreaksItsRule(t *testing.T) {
	made := &counter{}
	h := newHarness(t, pgStore(t), []route{creating(made)}, nil)
	for _, key := range []string{strings.Repeat("k", 256), "has space", "tab\there", "ümlaut"} {
		a := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"one"}`, idempotencyHeader, key)
		if code, d := a.envelope(t); a.status != http.StatusBadRequest || code != apierr.CodeInvalidField || d["field"] != idempotencyHeader {
			t.Errorf("%q: %d %s", key, a.status, a.body)
		}
	}
	if a := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"one"}`, idempotencyHeader, strings.Repeat("k", 255)); a.status != http.StatusCreated {
		t.Fatalf("a key of the longest length: %d %s", a.status, a.body)
	}
	// A body of no declared length past the bound is cut while the key is
	// read, before any key is reserved, so the key stays free.
	big := `{"name":"` + strings.Repeat("x", limits.MaxRequestBytes) + `"}`
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/things", strings.NewReader(big))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer t-alice")
	r.Header.Set(idempotencyHeader, "big")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body past the bound: %d %s", rec.Code, rec.Body)
	}
	if a := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"small"}`, idempotencyHeader, "big"); a.status != http.StatusCreated || a.header.Get(replayedHeader) != "" {
		t.Fatalf("the key after a body past the bound: %d %s", a.status, a.body)
	}
}

// A repeat that arrives while the first request is still being served is
// request_in_progress, retryable; once the first is answered, the repeat
// replays it.
func TestARepeatWhileTheFirstIsServed(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	slow := route{method: http.MethodPost, path: "/slow", idempotent: true, handle: func(_ *Server, w http.ResponseWriter, _ *http.Request, _ access.Caller) error {
		close(entered)
		<-release
		reply(w, http.StatusCreated, thing{ID: "slow"})
		return nil
	}}
	h := newHarness(t, pgStore(t), []route{slow}, nil)
	var wg sync.WaitGroup
	var first answer
	wg.Go(func() { first = h.do(http.MethodPost, "/v1/slow", "t-alice", `{}`, idempotencyHeader, "key-slow") })
	<-entered
	a := h.do(http.MethodPost, "/v1/slow", "t-alice", `{}`, idempotencyHeader, "key-slow")
	if code, d := a.envelope(t); a.status != http.StatusConflict || code != apierr.CodeRequestInProgress || d["retryable"] != true {
		t.Fatalf("a repeat in progress: %d %s", a.status, a.body)
	}
	close(release)
	wg.Wait()
	if first.status != http.StatusCreated {
		t.Fatalf("the first: %d", first.status)
	}
	if a := h.do(http.MethodPost, "/v1/slow", "t-alice", `{}`, idempotencyHeader, "key-slow"); a.status != http.StatusCreated || a.header.Get(replayedHeader) != "true" {
		t.Fatalf("after the first: %d %s", a.status, a.body)
	}
}

// A first request answered with a 5xx keeps no answer, so a retry runs
// again.
func TestAFailedFirstRunsAgain(t *testing.T) {
	var calls counter
	flaky := route{method: http.MethodPost, path: "/flaky", idempotent: true, handle: func(_ *Server, w http.ResponseWriter, _ *http.Request, _ access.Caller) error {
		if calls.add() == 1 {
			return apierr.New(apierr.CodeUnavailable, "the database does not answer")
		}
		reply(w, http.StatusCreated, thing{ID: "made"})
		return nil
	}}
	h := newHarness(t, pgStore(t), []route{flaky}, nil)
	if a := h.do(http.MethodPost, "/v1/flaky", "t-alice", `{}`, idempotencyHeader, "key-f"); a.status != http.StatusServiceUnavailable {
		t.Fatalf("the first: %d", a.status)
	}
	if a := h.do(http.MethodPost, "/v1/flaky", "t-alice", `{}`, idempotencyHeader, "key-f"); a.status != http.StatusCreated || a.header.Get(replayedHeader) != "" {
		t.Fatalf("the retry: %d %v", a.status, a.header)
	}
}

// keys is an idempotency table whose answers a test chooses.
type keys struct {
	beginErr, finishErr, abandonErr error
	finished, abandoned             int
}

func (k *keys) Begin(_ context.Context, key store.IdempotencyKey, _ time.Duration) (store.IdempotencyKey, bool, error) {
	if k.beginErr != nil {
		return store.IdempotencyKey{}, false, k.beginErr
	}
	return key, true, nil
}

func (k *keys) Finish(context.Context, store.IdempotencyKey, time.Duration) error {
	k.finished++
	return k.finishErr
}

func (k *keys) Abandon(context.Context, string, string) error {
	k.abandoned++
	return k.abandonErr
}

func (k *keys) Prune(context.Context, int) (int, error) { return 0, nil }

// A key the database cannot reserve is unavailable with nothing done; an
// answer that cannot be kept, or a key that cannot be abandoned, is logged
// and the caller still has its answer.
func TestIdempotencyWhenTheDatabaseFails(t *testing.T) {
	made := &counter{}
	down := &keys{beginErr: errors.New("store: the database does not answer at postgres://examen:pw@db/x?sslmode=disable")}
	h := newHarness(t, &fakeStore{keys: down}, []route{creating(made)}, nil)
	a := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"one"}`, idempotencyHeader, "k")
	if code, _ := a.envelope(t); a.status != http.StatusServiceUnavailable || code != apierr.CodeUnavailable || made.n != 0 {
		t.Fatalf("a key that cannot be reserved: %d %s", a.status, a.body)
	}
	if strings.Contains(h.log(), "sslmode") {
		t.Fatalf("the log carries the URL's query: %s", h.log())
	}

	lossy := &keys{finishErr: errors.New("finish failed"), abandonErr: errors.New("abandon failed")}
	h = newHarness(t, &fakeStore{keys: lossy}, []route{creating(made), failing("/fails", errors.New("a bug"))}, nil)
	if a := h.do(http.MethodPost, "/v1/things", "t-alice", `{"name":"one"}`, idempotencyHeader, "k"); a.status != http.StatusCreated || lossy.finished != 1 {
		t.Fatalf("an answer that cannot be kept: %d %d", a.status, lossy.finished)
	}
	if a := h.do(http.MethodPost, "/v1/fails", "t-alice", `{}`, idempotencyHeader, "k2"); a.status != http.StatusInternalServerError || lossy.abandoned != 1 {
		t.Fatalf("a key that cannot be abandoned: %d %d", a.status, lossy.abandoned)
	}
	for _, want := range []string{"finish failed", "abandon failed", "a bug"} {
		if !strings.Contains(h.log(), want) {
			t.Errorf("the log lacks %q: %s", want, h.log())
		}
	}
}

// The recorder keeps the first status a handler wrote, and an answer with
// no status is 200.
func TestRecorder(t *testing.T) {
	r := &recorder{ResponseWriter: discard{h: http.Header{}}, status: http.StatusOK}
	r.WriteHeader(http.StatusCreated)
	r.WriteHeader(http.StatusInternalServerError)
	if r.status != http.StatusCreated {
		t.Fatalf("%d", r.status)
	}
	w := &recorder{ResponseWriter: discard{h: http.Header{}}, status: http.StatusOK}
	if _, err := w.Write([]byte("x")); err != nil || w.status != http.StatusOK || w.body.String() != "x" {
		t.Fatalf("%d %q %v", w.status, w.body.String(), err)
	}
}

type discard struct{ h http.Header }

func (d discard) Header() http.Header       { return d.h }
func (discard) Write(b []byte) (int, error) { return len(b), nil }
func (discard) WriteHeader(int)             {}
