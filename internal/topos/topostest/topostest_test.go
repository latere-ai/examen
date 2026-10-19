// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topostest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/topos"
)

// TestTheStub: a create under a key answers its session again for the
// same body and conflicts for another; a refusal and an outage are
// answered once and until lifted; an end of a running session conflicts
// and an interrupt idles it; an unknown session, agent or route is 404.
func TestTheStub(t *testing.T) {
	st := New(t)
	c := topos.New(st.URL, st.Client())
	ctx := t.Context()
	body := topos.Create{Agent: "a@1", Rollout: "r", EndOnIdle: true}
	s, err := c.Create(ctx, "tok", "k1", body)
	if err != nil || s.ID != "ses_1" || st.Body(s.ID)["agent"] != "a@1" {
		t.Fatalf("create %+v %v", s, err)
	}
	if again, err := c.Create(ctx, "tok", "k1", body); err != nil || again.ID != s.ID {
		t.Fatalf("a repeat %+v %v", again, err)
	}
	var refused *topos.RefusedError
	other := body
	other.Agent = "a@2"
	if _, err := c.Create(ctx, "tok", "k1", other); !errors.As(err, &refused) || refused.Code != "idempotency_conflict" {
		t.Fatalf("another body under the key: %v", err)
	}
	st.Refuse(http.StatusForbidden, "forbidden")
	if _, err := c.Create(ctx, "tok", "k2", body); !errors.As(err, &refused) || refused.Status != http.StatusForbidden {
		t.Fatalf("a refusal: %v", err)
	}
	if _, err := c.Create(ctx, "tok", "k2", body); err != nil {
		t.Fatalf("a refusal lasted: %v", err)
	}
	st.Set(s.ID, topos.StatusRunning, "", Event(topos.EventSessionStatus, "", time.Now(), map[string]string{"status": "running"}))
	if err := c.End(ctx, "tok", s.ID, topos.StopCanceled); !errors.As(err, &refused) || refused.Status != http.StatusConflict {
		t.Fatalf("an end of a running session: %v", err)
	}
	if err := c.Interrupt(ctx, "tok", s.ID); err != nil || st.Session(s.ID).StopReason != topos.StopInterrupted || len(st.Interrupts) != 1 {
		t.Fatalf("an interrupt: %v %+v", err, st.Session(s.ID))
	}
	if evs, err := c.Events(ctx, "tok", s.ID); err != nil || len(evs) != 1 {
		t.Fatalf("events %v %v", evs, err)
	}
	st.Down(true)
	if _, err := c.Get(ctx, "tok", s.ID); !errors.Is(err, topos.ErrUnavailable) {
		t.Fatalf("an outage: %v", err)
	}
	st.Down(false)
	st.Delete(s.ID)
	for _, call := range []func() error{
		func() error { _, err := c.Get(ctx, "tok", s.ID); return err },
		func() error { _, err := c.Version(ctx, "tok", "nobody", "sha256:a"); return err },
		func() error { return c.End(ctx, "tok", "ses_9", topos.StopCanceled) },
	} {
		if err := call(); !errors.As(err, &refused) || refused.Status != http.StatusNotFound {
			t.Fatalf("an unknown object: %v", err)
		}
	}
	resp, err := st.Client().Post(st.URL+"/sessions", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a body that does not parse: %d %v", resp.StatusCode, err)
	}
	if st.Body("ses_none") != nil || len(st.Tokens) == 0 || st.Tokens[0] != "tok" {
		t.Fatalf("the stub's records: %v %v", st.Body("ses_none"), st.Tokens)
	}
	if got := st.Sessions(); len(got) != 2 {
		t.Fatalf("sessions %v", got)
	}
}

// TestTheStubsEdges: an agent's version is found by its digest; an
// unknown route of a session and of the stub is 404; a value that does
// not encode is a 500; and an event whose payload does not encode is the
// test's own mistake, a panic.
func TestTheStubsEdges(t *testing.T) {
	st := New(t)
	st.Agent("helper", Version{Version: 4, Digest: "sha256:d"})
	c := topos.New(st.URL, st.Client())
	if v, err := c.Version(t.Context(), "tok", "helper", "sha256:d"); err != nil || v != 4 {
		t.Fatalf("version %d %v", v, err)
	}
	s, err := c.Create(t.Context(), "tok", "k", topos.Create{Agent: "helper@4"})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/sessions/" + s.ID + "/other", "/elsewhere"} {
		resp, err := st.Client().Get(st.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if err := resp.Body.Close(); err != nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: %d %v", path, resp.StatusCode, err)
		}
	}
	rec := httptest.NewRecorder()
	st.reply(rec, http.StatusOK, make(chan int))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("a value that does not encode: %d", rec.Code)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an event that does not encode did not panic")
		}
	}()
	Event("x", "", time.Now(), make(chan int))
}
