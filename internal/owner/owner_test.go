// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package owner

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// minter is a stub minter answering each ask with answer, counting asks
// and keeping the last body.
func minter(t *testing.T, answer func(w http.ResponseWriter, body map[string]any)) (*Minter, *atomic.Int32, *map[string]any) {
	t.Helper()
	var n atomic.Int32
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret-bearer" {
			t.Errorf("the minter was asked with %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		last = body
		answer(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewMinter(srv.URL, func() string { return "secret-bearer" }, srv.Client()), &n, &last
}

var ask = Request{Owner: "iss|alice", Subject: "iss|alice", Audience: "topos", Experiment: "exp_1", Purpose: PurposeTrial}

// TestTokensAreKeptUntilTheirRenewal: an ask posts the owner, subject,
// audience, the experiment and purpose as claims and the longest lifetime
// asked; the token answered is reused until OWNER_TOKEN_RENEWAL before its
// expiry and never past OWNER_TOKEN_MAX_LIFETIME, and another audience is
// another token.
func TestTokensAreKeptUntilTheirRenewal(t *testing.T) {
	m, n, last := minter(t, func(w http.ResponseWriter, body map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok-" + body["audience"].(string), "expires_in": 300})
	})
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	tok, err := m.Token(t.Context(), ask)
	if err != nil || tok != "tok-topos" {
		t.Fatalf("token %q, %v", tok, err)
	}
	b, _ := json.Marshal(*last)
	if want := `{"audience":"topos","claims":{"examen":{"experiment":"exp_1","purpose":"trial"}},"owner":"iss|alice","subject":"iss|alice","ttl":900}`; string(b) != want {
		t.Fatalf("the ask was %s, want %s", b, want)
	}
	now = now.Add(2*time.Minute + 59*time.Second)
	if _, err := m.Token(t.Context(), ask); err != nil || n.Load() != 1 {
		t.Fatalf("a fresh token was asked again: %d asks, %v", n.Load(), err)
	}
	now = now.Add(time.Second)
	if _, err := m.Token(t.Context(), ask); err != nil || n.Load() != 2 {
		t.Fatalf("a token at its renewal was not asked again: %d asks, %v", n.Load(), err)
	}
	other := ask
	other.Audience = "arca"
	if tok, err := m.Token(t.Context(), other); err != nil || tok != "tok-arca" || n.Load() != 3 {
		t.Fatalf("another audience: %q, %d asks, %v", tok, n.Load(), err)
	}
}

// TestALongTokenIsUsedNoLongerThanTheMaxLifetime: a token that lives an
// hour is used OWNER_TOKEN_MAX_LIFETIME at most.
func TestALongTokenIsUsedNoLongerThanTheMaxLifetime(t *testing.T) {
	m, n, _ := minter(t, func(w http.ResponseWriter, _ map[string]any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok", "expires_in": 3600})
	})
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	for _, step := range []time.Duration{0, 14 * time.Minute, time.Minute} {
		now = now.Add(step)
		if _, err := m.Token(t.Context(), ask); err != nil {
			t.Fatal(err)
		}
	}
	if n.Load() != 2 {
		t.Fatalf("%d asks over 15 minutes, want 2", n.Load())
	}
}

// TestRefusalsAndOutages: a 403 naming sign_in_not_kept is that refusal,
// any other 403 owner_token_refused, and every other answer an outage that
// names neither the bearer nor a token.
func TestRefusalsAndOutages(t *testing.T) {
	for _, c := range []struct {
		name   string
		status int
		body   string
		reason string
	}{
		{"sign-in not kept", http.StatusForbidden, `{"reason":"sign_in_not_kept"}`, ReasonSignInNotKept},
		{"another refusal", http.StatusForbidden, `{"reason":"not_owner"}`, ReasonRefused},
		{"a refusal with no body", http.StatusForbidden, ``, ReasonRefused},
		{"an outage", http.StatusServiceUnavailable, ``, ""},
		{"no token", http.StatusOK, `{"expires_in":300}`, ""},
		{"no lifetime", http.StatusOK, `{"token":"secret-token"}`, ""},
		{"not json", http.StatusOK, `secret-token`, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := minter(t, func(w http.ResponseWriter, _ map[string]any) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			})
			_, err := m.Token(t.Context(), ask)
			var refused *RefusedError
			switch {
			case c.reason != "" && (!errors.As(err, &refused) || refused.Reason != c.reason):
				t.Fatalf("err %v, want refused %s", err, c.reason)
			case c.reason == "" && !errors.Is(err, ErrUnavailable):
				t.Fatalf("err %v, want unavailable", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("the error carries a secret: %v", err)
			}
		})
	}
}

// TestAMinterNotReachedIsUnavailable: a minter that cannot be reached is
// an outage, and its address is in no error.
func TestAMinterNotReachedIsUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()
	m := NewMinter(addr, func() string { return "b" }, nil)
	_, err := m.Token(t.Context(), ask)
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), strings.TrimPrefix(addr, "http://")) {
		t.Fatalf("err %v", err)
	}
}
