// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// paged is a Topos answering versions and events a page at a time, two
// to a page, and recording each request's bearer and Idempotency-Key.
func paged(t *testing.T) (*Client, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("Authorization")+" "+r.Header.Get("Idempotency-Key"))
		page := func(items []any) {
			start, _ := strconv.Atoi(r.URL.Query().Get("cursor"))
			end := min(start+2, len(items))
			next := ""
			if end < len(items) {
				next = strconv.Itoa(end)
			}
			_ = json.MarshalWrite(w, map[string]any{"items": items[start:end], "next_cursor": next})
		}
		switch {
		case r.URL.Path == "/agents/helper/versions":
			page([]any{map[string]any{"version": 1, "digest": "sha256:a"}, map[string]any{"version": 2, "digest": "sha256:b"}, map[string]any{"version": 3, "digest": "sha256:c"}})
		case r.URL.Path == "/sessions/ses_1/events":
			page([]any{map[string]any{"seq": 1, "type": "a"}, map[string]any{"seq": 2, "type": "b"}, map[string]any{"seq": 3, "type": "c"}})
		case r.URL.Path == "/sessions" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.MarshalWrite(w, map[string]any{"id": "ses_1", "status": "idle", "rollout": "r"})
		case r.URL.Path == "/sessions/ses_1/events" || r.URL.Path == "/sessions/ses_1/end":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/sessions/busy":
			w.WriteHeader(http.StatusTooManyRequests)
		case r.URL.Path == "/sessions/broken":
			_, _ = w.Write([]byte("{"))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden"}}`))
		}
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", srv.Client()), &seen
}

// TestTheClient: a version is found across pages by its digest and an
// absent one is 404; a log is read across pages; a create carries its
// Idempotency-Key and every call the owner token; a refusal carries its
// status and code, and 429, 5xx, an answer that does not parse and a
// Topos that cannot be reached are outages that name no token.
func TestTheClient(t *testing.T) {
	c, seen := paged(t)
	ctx := t.Context()
	if v, err := c.Version(ctx, "secret-token", "helper", "sha256:c"); err != nil || v != 3 {
		t.Fatalf("version %d, %v", v, err)
	}
	_, err := c.Version(ctx, "secret-token", "helper", "sha256:z")
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Status != http.StatusNotFound {
		t.Fatalf("an absent version: %v", err)
	}
	evs, err := c.Events(ctx, "secret-token", "ses_1")
	if err != nil || len(evs) != 3 || evs[2].Seq != 3 {
		t.Fatalf("events %+v, %v", evs, err)
	}
	s, err := c.Create(ctx, "secret-token", "tri_1", Create{Agent: "helper@3", EndOnIdle: true})
	if err != nil || s.ID != "ses_1" || s.Rollout != "r" {
		t.Fatalf("create %+v, %v", s, err)
	}
	if err := c.Interrupt(ctx, "secret-token", "ses_1"); err != nil {
		t.Fatal(err)
	}
	if err := c.End(ctx, "secret-token", "ses_1", StopCanceled); err != nil {
		t.Fatal(err)
	}
	for _, line := range *seen {
		if !strings.Contains(line, "Bearer secret-token") {
			t.Fatalf("a request carried no owner token: %s", line)
		}
		if strings.HasPrefix(line, "POST /sessions ") && !strings.HasSuffix(line, " tri_1") {
			t.Fatalf("the create carried no key: %s", line)
		}
	}
	if _, err := c.Get(ctx, "secret-token", "ses_2"); !errors.As(err, &refused) || refused.Status != http.StatusForbidden || refused.Code != "forbidden" {
		t.Fatalf("a refusal: %v", err)
	}
	for _, id := range []string{"busy", "broken"} {
		if _, err := c.Get(ctx, "secret-token", id); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret") {
			t.Fatalf("%s: %v", id, err)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	gone := New(srv.URL, nil)
	srv.Close()
	if _, err := gone.Get(ctx, "secret-token", "ses_1"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("an unreachable Topos: %v", err)
	}
}
