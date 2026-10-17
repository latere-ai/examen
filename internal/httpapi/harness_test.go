// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

func TestMain(m *testing.M) { pgtest.Main(m) }

const (
	alice = "https://issuer.example.com|alice"
	bob   = "https://issuer.example.com|bob"
)

// tokens is an authenticator that knows a bearer per subject: "t-alice"
// is alice, "t-bob" bob, "t-expired" a token the verifier refuses, and
// "t-broken" one it fails on.
type tokens struct{}

func (tokens) Authenticate(r *http.Request) (access.Caller, error) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case !ok || raw == "":
		return access.Caller{}, access.ErrMissingToken
	case raw == "t-expired":
		return access.Caller{}, &access.TokenError{Reason: "expired"}
	case raw == "t-unavailable":
		return access.Caller{}, &access.TokenError{Reason: "issuer_unavailable", Retryable: true}
	case raw == "t-broken":
		return access.Caller{}, errors.New("the verifier broke")
	}
	sub := strings.TrimPrefix(raw, "t-")
	return access.Caller{Subject: "https://issuer.example.com|" + sub, Issuer: "https://issuer.example.com", Sub: sub}, nil
}

// unavailable is an authorizer that never answers.
type unavailable struct{}

func (unavailable) Authorize(context.Context, access.Caller, access.Question) (access.Decision, error) {
	return access.Decision{}, access.ErrUnavailable
}

// broken is an authorizer whose question was a mistake of the handler.
type broken struct{}

func (broken) Authorize(context.Context, access.Caller, access.Question) (access.Decision, error) {
	return access.Decision{}, errors.New("the question was not one of the vocabulary")
}

// harness is a server over a store, with the routes a test serves.
type harness struct {
	t      *testing.T
	srv    *Server
	h      http.Handler
	logs   *bytes.Buffer
	logsMu *sync.Mutex
}

// lockedWriter keeps the log of handlers that run at once whole.
type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

func newHarness(t *testing.T, st store.Store, served []route, tweak func(*Server)) *harness {
	t.Helper()
	logs, mu := &bytes.Buffer{}, &sync.Mutex{}
	srv := &Server{
		Store: st, Auth: tokens{}, Authz: access.NewAuthorizer(access.OwnerPolicy{}), BasePath: "/v1",
		Log: slog.New(slog.NewJSONHandler(lockedWriter{mu, logs}, nil)),
	}
	if tweak != nil {
		tweak(srv)
	}
	return &harness{t: t, srv: srv, h: srv.handler(served), logs: logs, logsMu: mu}
}

// pgStore is a store over a fresh database of the test's own.
func pgStore(t *testing.T) store.Store {
	t.Helper()
	return pgtest.Open(t, false, postgres.Options{})
}

// answer is one response, read.
type answer struct {
	status int
	header http.Header
	body   []byte
}

func (a answer) envelope(t *testing.T) (code string, details map[string]any) {
	t.Helper()
	var e struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(a.body, &e); err != nil {
		t.Fatalf("not an error envelope: %s", a.body)
	}
	if e.Error.Message != apierr.Message(e.Error.Code) {
		t.Fatalf("the message of %s is %q", e.Error.Code, e.Error.Message)
	}
	return e.Error.Code, e.Error.Details
}

// do sends one request as a bearer, with a body and headers.
func (h *harness) do(method, path, bearer string, body any, headers ...string) answer {
	h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			h.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	r := httptest.NewRequestWithContext(h.t.Context(), method, path, rd)
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	return answer{status: rec.Code, header: rec.Header(), body: rec.Body.Bytes()}
}

func (h *harness) log() string {
	h.logsMu.Lock()
	defer h.logsMu.Unlock()
	return h.logs.String()
}

// thing is what the test routes create and list.
type thing struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner string `json:"owner,omitempty"`
}

// counter counts the calls of a route.
type counter struct {
	mu sync.Mutex
	n  int
}

func (c *counter) add() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
	return c.n
}

// creating is an idempotent create that asks experiment.create, as the
// routes of the next build step will: it decodes its body, asks, and
// answers 201 with what it made.
func creating(made *counter) route {
	return route{method: http.MethodPost, path: "/things", idempotent: true, handle: func(s *Server, w http.ResponseWriter, r *http.Request, c access.Caller) error {
		var in struct {
			Name  string `json:"name"`
			Owner string `json:"owner"`
		}
		if err := decode(r, &in); err != nil {
			return err
		}
		q := access.Ask(r, authorizer.ActionExperimentCreate, access.Resource(authorizer.ActionExperimentCreate, "exp_1", map[string]any{authorizer.FieldOwner: in.Owner}))
		d, err := s.ask(r, c, q)
		if err != nil {
			return err
		}
		if !d.Allow {
			return apierr.Denied(q.Action, d.Reason)
		}
		n := made.add()
		reply(w, http.StatusCreated, thing{ID: "thing-" + strconv.Itoa(n), Name: in.Name, Owner: c.Subject})
		return nil
	}}
}

// listing is a list of n things, paged.
func listing(n int) route {
	return route{method: http.MethodGet, path: "/things", handle: func(_ *Server, w http.ResponseWriter, r *http.Request, _ access.Caller) error {
		p, err := pageOf(r, "things")
		if err != nil {
			return err
		}
		var items []thing
		for i := range n {
			id := "t" + string(rune('a'+i))
			if id > p.After && len(items) <= p.Limit {
				items = append(items, thing{ID: id})
			}
		}
		replyPage(w, "things", p, items, func(t thing) string { return t.ID })
		return nil
	}}
}

// failing answers an error of its own.
func failing(path string, err error) route {
	return route{method: http.MethodPost, path: path, idempotent: true, handle: func(*Server, http.ResponseWriter, *http.Request, access.Caller) error {
		return err
	}}
}
