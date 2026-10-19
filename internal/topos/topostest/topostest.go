// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package topostest is a stub of the Topos routes the `topos` executor
// calls: an agent's versions, a session's create under an Idempotency-Key,
// its read, its log, an interrupt and an end. A test drives each session
// to the state it reads, and the stub records what it was asked.
package topostest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/topos"
)

// Version is one version of an agent the stub holds.
type Version struct {
	Version int    `json:"version"`
	Digest  string `json:"digest"`
}

// Stub is a Topos holding agents' versions and the sessions created.
type Stub struct {
	*httptest.Server

	mu       sync.Mutex
	versions map[string][]Version
	sessions map[string]*session
	byKey    map[string]string
	order    []string
	refusal  *refusal
	outage   bool
	// Tokens are the bearers each request carried, in order.
	Tokens []string
	// Interrupts are the sessions interrupted, in order.
	Interrupts []string
}

type session struct {
	s      topos.Session
	body   []byte
	events []topos.Event
}

type refusal struct {
	status int
	code   string
}

// New is a stub serving until the test ends.
func New(t *testing.T) *Stub {
	t.Helper()
	st := &Stub{versions: map[string][]Version{}, sessions: map[string]*session{}, byKey: map[string]string{}}
	st.Server = httptest.NewServer(http.HandlerFunc(st.serve))
	t.Cleanup(st.Close)
	return st
}

// Agent gives agent name its versions.
func (st *Stub) Agent(name string, versions ...Version) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.versions[name] = versions
}

// Refuse answers the next create with status and code.
func (st *Stub) Refuse(status int, code string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.refusal = &refusal{status, code}
}

// Down makes every request an outage, or ends one.
func (st *Stub) Down(down bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.outage = down
}

// Sessions are the ids of the sessions created, in order.
func (st *Stub) Sessions() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	return append([]string(nil), st.order...)
}

// Body is the create body of a session, as sent.
func (st *Stub) Body(id string) map[string]any {
	st.mu.Lock()
	defer st.mu.Unlock()
	var out map[string]any
	if s, ok := st.sessions[id]; ok {
		_ = json.Unmarshal(s.body, &out)
	}
	return out
}

// Session is a session as the stub holds it.
func (st *Stub) Session(id string) topos.Session {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sessions[id].s
}

// Set changes a session's status and stop reason and appends events to
// its log.
func (st *Stub) Set(id, status, stop string, events ...topos.Event) {
	st.mu.Lock()
	defer st.mu.Unlock()
	s := st.sessions[id]
	s.s.Status, s.s.StopReason = status, stop
	for _, e := range events {
		e.Seq = uint64(len(s.events) + 1)
		s.events = append(s.events, e)
	}
	s.s.LastSeq = uint64(len(s.events))
}

// Delete forgets a session, as a delete at Topos does.
func (st *Stub) Delete(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, id)
}

// Event is one event of a log, its payload encoded.
func Event(typ, thread string, at time.Time, payload any) topos.Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return topos.Event{ID: "evt_" + strconv.FormatInt(at.UnixNano(), 36), Type: typ, Thread: thread, Time: at, Payload: raw}
}

func (st *Stub) refuse(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
}

func (st *Stub) reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (st *Stub) serve(w http.ResponseWriter, r *http.Request) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.Tokens = append(st.Tokens, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if st.outage {
		st.refuse(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && len(parts) == 3 && parts[0] == "agents" && parts[2] == "versions":
		v, ok := st.versions[parts[1]]
		if !ok {
			st.refuse(w, http.StatusNotFound, "not_found")
			return
		}
		st.reply(w, http.StatusOK, map[string]any{"items": v})
	case r.Method == http.MethodPost && len(parts) == 1 && parts[0] == "sessions":
		st.create(w, r)
	case len(parts) >= 2 && parts[0] == "sessions":
		s, ok := st.sessions[parts[1]]
		if !ok {
			st.refuse(w, http.StatusNotFound, "not_found")
			return
		}
		switch {
		case r.Method == http.MethodGet && len(parts) == 2:
			st.reply(w, http.StatusOK, s.s)
		case r.Method == http.MethodGet && len(parts) == 3 && parts[2] == "events":
			st.reply(w, http.StatusOK, map[string]any{"items": s.events})
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "events":
			if s.s.Status == topos.StatusRunning {
				s.s.Status, s.s.StopReason = topos.StatusIdle, topos.StopInterrupted
			}
			st.Interrupts = append(st.Interrupts, s.s.ID)
			st.reply(w, http.StatusOK, map[string]any{})
		case r.Method == http.MethodPost && len(parts) == 3 && parts[2] == "end":
			var b struct {
				Reason string `json:"reason"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if s.s.Status != topos.StatusIdle {
				st.refuse(w, http.StatusConflict, "conflict")
				return
			}
			s.s.Status, s.s.StopReason = topos.StatusEnded, b.Reason
			st.reply(w, http.StatusOK, s.s)
		default:
			st.refuse(w, http.StatusNotFound, "not_found")
		}
	default:
		st.refuse(w, http.StatusNotFound, "not_found")
	}
}

func (st *Stub) create(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		st.refuse(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if f := st.refusal; f != nil {
		st.refusal = nil
		st.refuse(w, f.status, f.code)
		return
	}
	key := r.Header.Get("Idempotency-Key")
	if id, ok := st.byKey[key]; ok && key != "" {
		if string(st.sessions[id].body) != string(body) {
			st.refuse(w, http.StatusConflict, "idempotency_conflict")
			return
		}
		st.reply(w, http.StatusCreated, st.sessions[id].s)
		return
	}
	var b topos.Create
	if err := json.Unmarshal(body, &b); err != nil {
		st.refuse(w, http.StatusBadRequest, "invalid_request")
		return
	}
	id := "ses_" + strconv.Itoa(len(st.order)+1)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC).Add(time.Duration(len(st.order)) * time.Minute)
	s := &session{body: body, s: topos.Session{ID: id, Status: topos.StatusIdle, Rollout: b.Rollout, Model: b.Model, CreatedAt: now, UpdatedAt: now,
		Folder: &topos.Folder{Owner: "owner", Path: "files/conversations/" + id}}}
	st.sessions[id] = s
	st.byKey[key] = id
	st.order = append(st.order, id)
	st.reply(w, http.StatusCreated, s.s)
}
