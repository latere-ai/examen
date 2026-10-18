// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

// sseFrame is one frame of a stream.
type sseFrame struct {
	id, event, data string
	comment         bool
}

// readFrames reads frames from a stream until stop says to.
func readFrames(t *testing.T, sc *bufio.Scanner, stop func(sseFrame) bool) []sseFrame {
	t.Helper()
	var out []sseFrame
	var f sseFrame
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if f.event != "" || f.comment {
				out = append(out, f)
				if stop(f) {
					return out
				}
			}
			f = sseFrame{}
		case strings.HasPrefix(line, ":"):
			f.comment = true
		case strings.HasPrefix(line, "id: "):
			f.id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "event: "):
			f.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			f.data = strings.TrimPrefix(line, "data: ")
		}
	}
	return out
}

// open opens a stream through a real server, as a client follows one.
func open(t *testing.T, base, eid, bearer, lastID string) (*http.Response, *bufio.Scanner) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/experiments/"+eid+"/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := resp.Body.Close(); err != nil {
			t.Error(err)
		}
	})
	return resp, bufio.NewScanner(resp.Body)
}

func TestTheStream(t *testing.T) {
	x := newAPI(t, func(s *Server) { s.StreamPoll, s.StreamKeepalive = 5*time.Millisecond, 20*time.Millisecond })
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 1, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	srv := httptest.NewServer(x.h)
	defer srv.Close()

	// A stream opened without Last-Event-ID starts with a snapshot whose id
	// is the latest sequence, then follows the log until the end.
	resp, sc := open(t, srv.URL, eid, "t-alice", "")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", resp.StatusCode, resp.Header)
	}
	frames := readFrames(t, sc, func(f sseFrame) bool { return f.event == "snapshot" })
	snapshotID, err := strconv.Atoi(frames[0].id)
	if err != nil || snapshotID < 1 || !strings.Contains(frames[0].data, eid) {
		t.Fatalf("snapshot %+v", frames[0])
	}
	// A keepalive comment fills the silence.
	if frames := readFrames(t, sc, func(f sseFrame) bool { return f.comment }); !frames[len(frames)-1].comment {
		t.Fatal("no keepalive")
	}
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/cancel", "t-alice", nil)
	frames = readFrames(t, sc, func(f sseFrame) bool { return f.event == "end" })
	last := frames[len(frames)-1]
	if last.event != "end" || !strings.Contains(last.data, "canceled") {
		t.Fatalf("end %+v", last)
	}
	seen := map[string]bool{}
	prev := snapshotID
	for _, f := range frames {
		if f.id == "" {
			continue
		}
		n, err := strconv.Atoi(f.id)
		if err != nil || n != prev+1 {
			t.Fatalf("a gap or a repeat after %d: %+v", prev, f)
		}
		prev = n
		seen[f.event] = true
	}
	if !seen["experiment.canceled"] || !seen["cell.parked"] || !seen["trial"] {
		t.Fatalf("the cancel's events %v", seen)
	}

	// A reconnect with Last-Event-ID replays every later event, in order,
	// and ends.
	_, sc = open(t, srv.URL, eid, "t-alice", strconv.Itoa(snapshotID))
	replay := readFrames(t, sc, func(f sseFrame) bool { return f.event == "end" })
	if replay[0].event == "snapshot" || replay[0].id != strconv.Itoa(snapshotID+1) || replay[len(replay)-1].event != "end" {
		t.Fatalf("replay %+v", replay)
	}

	// Past the latest sequence is invalid_cursor before the stream starts,
	// another subject reads nothing, and one stream more than the bound is
	// too_many_streams.
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidCursor, http.MethodGet, "/v1/experiments/"+eid+"/stream", "t-alice", nil, "Last-Event-ID", "99999")
	x.refuse(http.StatusBadRequest, apierr.CodeInvalidCursor, http.MethodGet, "/v1/experiments/"+eid+"/stream", "t-alice", nil, "Last-Event-ID", "x")
	x.refuse(http.StatusNotFound, apierr.CodeExperimentNotFound, http.MethodGet, "/v1/experiments/"+eid+"/stream", "t-bob", nil)
	x.srv.streams = map[string]int{alice: limits.MaxStreamsPerSubject}
	a := x.do(http.MethodGet, "/v1/experiments/"+eid+"/stream", "t-alice", nil)
	if code, _ := a.envelope(t); a.status != http.StatusTooManyRequests || code != apierr.CodeTooManyStreams || a.header.Get("Retry-After") != "1" {
		t.Fatalf("too many: %d %s", a.status, a.body)
	}
	x.srv.streams = nil
}

// A stream closes at its bearer's expiry, and with an error frame when a
// deny arrives while it is open.
func TestTheStreamCloses(t *testing.T) {
	expiring := tokensWith(func(c *access.Caller) {
		if c.Sub == "short" {
			c.Expires = time.Now().Add(50 * time.Millisecond)
		}
	})
	deny := &switchable{}
	x := newAPI(t, func(s *Server) {
		s.StreamPoll, s.Auth = 5*time.Millisecond, expiring
		deny.inner = s.Authz
		s.Authz = deny
	})
	f := x.fixture()
	eid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", map[string]any{
		"suite": f.suite, "candidates": []string{f.high}, "trials": 1, "budget": map[string]any{"name": "study"},
	})["id"].(string)
	srv := httptest.NewServer(x.h)
	defer srv.Close()
	// The short bearer is alice's too, for the owner policy.
	_, sc := open(t, srv.URL, eid, "t-short", "0")
	frames := readFrames(t, sc, func(sseFrame) bool { return false })
	for _, f := range frames {
		if f.event == "end" || f.event == "error" {
			t.Fatalf("an expired bearer's stream sends nothing more: %+v", f)
		}
	}
	latest, err := x.st.Experiments().LatestSeq(t.Context(), eid)
	if err != nil {
		t.Fatal(err)
	}
	_, sc = open(t, srv.URL, eid, "t-alice", strconv.FormatInt(latest, 10))
	deny.deny(true)
	x.must(http.StatusOK, http.MethodPost, "/v1/experiments/"+eid+"/work/lease", "t-alice", map[string]any{"candidate": f.high})
	frames = readFrames(t, sc, func(f sseFrame) bool { return f.event == "error" })
	if last := frames[len(frames)-1]; last.event != "error" || !strings.Contains(last.data, apierr.CodeExperimentNotFound) {
		t.Fatalf("after a deny %+v", frames)
	}
}

// tokensWith is the test authenticator with one more bearer: "t-short",
// alice's, which tweak may give an expiry.
func tokensWith(tweak func(*access.Caller)) access.Authenticator {
	return authFunc(func(r *http.Request) (access.Caller, error) {
		if r.Header.Get("Authorization") == "Bearer t-short" {
			c := access.Caller{Subject: alice, Issuer: "https://issuer.example.com", Sub: "short"}
			tweak(&c)
			return c, nil
		}
		return tokens{}.Authenticate(r)
	})
}

type authFunc func(*http.Request) (access.Caller, error)

func (f authFunc) Authenticate(r *http.Request) (access.Caller, error) { return f(r) }

// switchable is an authorizer that denies experiment.read once it is told
// to, and otherwise asks the one it wraps.
type switchable struct {
	inner access.Authorizer
	mu    sync.Mutex
	on    bool
}

func (s *switchable) deny(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.on = on
}

func (s *switchable) Authorize(ctx context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	s.mu.Lock()
	on := s.on
	s.mu.Unlock()
	if on && q.Action == authorizer.ActionExperimentRead {
		return access.Decision{Reason: "revoked"}, nil
	}
	return s.inner.Authorize(ctx, c, q)
}
