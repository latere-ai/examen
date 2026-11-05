// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package httpapi serves the contract of specs/003-api.md: the routes under
// EXAMEN_BASE_PATH, the probes beside them, and the conventions every route
// shares. Every route below the base path but the contract itself
// authenticates, is held to its subject's request rate, builds the resource
// from the request and the stored object, asks one question, and acts within
// the decision (specs/004-identity-and-authorization.md). An error has one
// code, one fixed sentence for a person, and the developer detail apart.
package httpapi

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"latere.ai/x/pkg/health"
	"latere.ai/x/pkg/otel"
	"latere.ai/x/pkg/ratelimit"

	"latere.ai/x/examen/api"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	keysrc "latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/version"
	"latere.ai/x/examen/internal/work"
)

// analyzer is the server's analyzer, compare.NotComputed when none is set.
func (s *Server) analyzer() compare.Analyzer {
	if s.Analyzer != nil {
		return s.Analyzer
	}
	return compare.NotComputed{}
}

// advance submits the rollouts a request queued, when the server takes
// unattended work; a failure is logged and left to the ticker.
func (s *Server) advance(r *http.Request, experiment string) {
	if s.Engine == nil {
		return
	}
	if err := s.Engine.Advance(context.WithoutCancel(r.Context()), experiment); err != nil {
		s.log().WarnContext(r.Context(), "queued rollouts were not submitted; the ticker takes them", "experiment", experiment, "error", apierr.LogText(err))
	}
}

// Server is the API of one process.
type Server struct {
	Store store.Store
	// Auth verifies a caller and Authz answers its questions.
	Auth  access.Authenticator
	Authz access.Authorizer
	// BasePath is the prefix of every route: empty, or a path that starts
	// with a slash and does not end with one.
	BasePath string
	// Draining reports whether the replica has had its stop signal, which
	// fails readiness at once. Nil is never.
	Draining func() bool
	// Now is the clock of the request rate. Nil takes time.Now.
	Now func() time.Time
	Log *slog.Logger
	// Engine takes the unattended work a request makes: it submits the
	// rollouts a create, a submit or a retry queues once experiment.run
	// allows them. Nil leaves them to the ticker.
	Engine *work.Engine
	// Analyzer answers the figures of a grid and a comparison. Nil is
	// compare.NotComputed.
	Analyzer compare.Analyzer
	// Reserved are the audiences Examen acts through, which no program
	// candidate's credential may name.
	Reserved []string
	// StreamPoll and StreamKeepalive replace STREAM_POLL_INTERVAL and
	// STREAM_KEEPALIVE when they are not zero, for a test.
	StreamPoll      time.Duration
	StreamKeepalive time.Duration

	// Keys is the key source an experiment's external Key and its Budget's
	// figures come from (specs/004-identity-and-authorization.md). Nil
	// hands no run a Key, and an experiment's answer carries no figures.
	Keys keysrc.Source

	streamsMu sync.Mutex
	streams   map[string]int
	keyState  keySource
}

// route is one route of the contract this server serves, below the base
// path, with the handler that serves it for a verified caller.
type route struct {
	method, path string
	// idempotent routes take an Idempotency-Key (specs/003-api.md).
	idempotent bool
	// secret, on an idempotent route, is the member of its 200 answer
	// that carries a credential: the copy kept for a repeat leaves it out,
	// and refill answers it afresh on the repeat.
	secret string
	refill func(*Server, *http.Request, access.Caller) (any, error)
	handle func(*Server, http.ResponseWriter, *http.Request, access.Caller) error
}

// routes are the routes of specs/003-api.md this release serves that take
// a token, in the contract's order.
var routes = served()

// served lists the routes of this release.
func served() []route {
	out := versionRoutes()
	out = append(out, recordRoutes()...)
	return append(out, experimentRoutes()...)
}

// Route is one route the server serves, as the contract spells it below
// the base path.
type Route struct{ Method, Path string }

// public are the routes below the base path that take no token: the
// contract.
var public = []Route{
	{http.MethodGet, "/openapi.yaml"},
}

// Routes lists every route the server serves below the base path.
func Routes() []Route {
	out := make([]Route, 0, len(routes)+len(public))
	for _, rt := range routes {
		out = append(out, Route{rt.method, rt.path})
	}
	return append(out, public...)
}

// Probes are the paths of the probes, outside the base path.
var Probes = []string{"/livez", "/readyz", "/version"}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Handler is the whole surface: the probes at the root and the API below
// the base path.
func (s *Server) Handler() http.Handler { return s.handler(routes) }

// handler serves the given routes below the base path.
func (s *Server) handler(served []route) http.Handler {
	now := s.Now
	if now == nil {
		now = time.Now
	}
	limiter := ratelimit.New(ratelimit.Config{PerMinute: limits.APIRequestsPerMinute, Now: now})
	mux := http.NewServeMux()
	probes := s.probes()
	for _, p := range Probes {
		mux.Handle("GET "+p, probes)
	}
	b := s.BasePath
	if b != "" {
		// The build identity is also public below the base path, the one
		// prefix the ingress forwards, so a release check reads it from
		// outside. It takes no token and is the body /version serves.
		mux.Handle("GET "+b+"/version", versionAt(probes))
	}
	for _, rt := range served {
		mux.Handle(rt.method+" "+b+rt.path, s.serve(rt, limiter))
	}
	document := api.Served(b)
	mux.HandleFunc("GET "+b+"/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		if _, err := w.Write(document); err != nil {
			s.log().WarnContext(r.Context(), "the contract was not written whole", "error", err.Error())
		}
	})
	// No route at a path is the contract's 404 in its envelope, for every
	// method, below the base path and beside it.
	notFound := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.answer(w, r, apierr.Newf(apierr.CodeNotFound, "no route serves %s at this path", r.Method))
	})
	mux.Handle(b+"/", notFound)
	if b != "" {
		mux.Handle("/", notFound)
	}
	return otel.Handler(identified(s.bounded(mux)), "examend",
		otel.WithRouteTemplate(func(r *http.Request) string { return r.Pattern }),
		otel.WithSkip(func(r *http.Request) bool {
			return isProbe(r.URL.Path) || (b != "" && r.URL.Path == b+"/version")
		}))
}

// versionAt serves the answer of the probes' /version at the path it is
// mounted on, so the build identity below the base path is the very body
// the root serves.
func versionAt(probes http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rooted := r.Clone(r.Context())
		rooted.URL.Path, rooted.URL.RawPath = "/version", ""
		probes.ServeHTTP(w, rooted)
	})
}

func isProbe(path string) bool {
	return slices.Contains(Probes, path)
}

// serve runs one route: the caller verified, its request rate held, the
// key of an idempotent route kept, and the handler's error answered.
func (s *Server) serve(rt route, limiter *ratelimit.Buckets) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := s.Auth.Authenticate(r)
		if err != nil {
			s.answer(w, r, tokenError(err))
			return
		}
		if a := limiter.Allow(c.Subject); !a.OK {
			e := apierr.Newf(apierr.CodeRateLimited, "%d requests a minute from one subject to one replica", a.PerMinute)
			e.RetryAfter = a.Retry
			s.answer(w, r, e)
			return
		}
		if rt.idempotent && r.Header.Get(idempotencyHeader) != "" {
			s.idempotent(w, r, c, rt)
			return
		}
		if err := rt.handle(s, w, r, c); err != nil {
			s.answer(w, r, err)
		}
	})
}

// tokenError is the error of a bearer the verifier refused: missing_token
// for a request that carries none, invalid_token with the verifier's word
// for why for one it refused, and internal for anything else.
func tokenError(err error) error {
	var te *access.TokenError
	switch {
	case errors.Is(err, access.ErrMissingToken):
		return apierr.New(apierr.CodeMissingToken, "the request carries no bearer token")
	case errors.As(err, &te):
		e := apierr.New(apierr.CodeInvalidToken, "the bearer token was refused: "+te.Reason)
		e.Reason = te.Reason
		e.Retryable = &te.Retryable
		return e
	}
	return apierr.Internal(err)
}

// answer writes an error. An error nobody classified is logged with its
// text, any URL in it cut to its scheme, host and path, under the route's
// pattern and never its path, and answered as internal with no detail.
func (s *Server) answer(w http.ResponseWriter, r *http.Request, err error) {
	if e := apierr.Write(w, err); e.Code == apierr.CodeInternal {
		s.log().ErrorContext(r.Context(), "a request failed", "route", r.Pattern, "error", apierr.LogText(e.Cause()))
	}
}

// ask asks one question and turns its failure into the route's error.
func (s *Server) ask(r *http.Request, c access.Caller, q access.Question) (access.Decision, error) {
	d, err := s.Authz.Authorize(r.Context(), c, q)
	switch {
	case errors.Is(err, access.ErrUnavailable):
		s.log().WarnContext(r.Context(), "the authorizer gave no decision", "route", r.Pattern, "error", apierr.LogText(err))
		return access.Decision{}, apierr.New(apierr.CodeAuthorizerUnavailable, q.Action+": the authorizer gave no well-formed answer")
	case err != nil:
		return access.Decision{}, apierr.Internal(err)
	}
	return d, nil
}

// probes serve /livez, /readyz and /version. /readyz is ready while the
// replica has not had its stop signal and the database answers with a
// schema this binary serves; it does not depend on the authorizer, the
// issuers or any core, whose outage fails the requests that need them.
func (s *Server) probes() http.Handler {
	return health.Handler(health.Options{
		Ready: health.Checks(
			health.Check{Name: "draining", Run: s.draining},
			health.Check{Name: "database", Run: s.Store.Ready},
		),
		Timeout: time.Second,
		Version: version.Version, Commit: version.Commit, BuildTime: version.Date,
	})
}

func (s *Server) draining(context.Context) error {
	if s.Draining != nil && s.Draining() {
		return errors.New("the replica is shutting down")
	}
	return nil
}

// RequestIDHeader carries a request's id both ways.
const RequestIDHeader = "X-Request-Id"

// maxRequestIDBytes bounds an id a caller sends; a longer one, or one with
// a byte that is not visible ASCII, is replaced.
const maxRequestIDBytes = 128

// identified gives every request an id: the one the caller sent, when it is
// short visible ASCII, or one of its own. The id is set on the request, so
// the authorizer is told it, and on the answer.
func identified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get(RequestIDHeader)
		if !visibleASCII(rid, maxRequestIDBytes) {
			rid = "req_" + strings.ToLower(rand.Text())
			r.Header.Set(RequestIDHeader, rid)
		}
		w.Header().Set(RequestIDHeader, rid)
		next.ServeHTTP(w, r)
	})
}

// visibleASCII reports whether s is 1 to n bytes of visible ASCII.
func visibleASCII(s string, n int) bool {
	if s == "" || len(s) > n {
		return false
	}
	for i := range len(s) {
		if s[i] < 0x21 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

// bounded holds every request body to MAX_REQUEST_BYTES: one that declares
// more is refused before a byte of it is read, and one that sends more is
// cut at the bound, which a read reports.
func (s *Server) bounded(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > limits.MaxRequestBytes {
			s.answer(w, r, apierr.Newf(apierr.CodeRequestTooLarge, "the body declares %d bytes, above MAX_REQUEST_BYTES (%d)", r.ContentLength, limits.MaxRequestBytes))
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, limits.MaxRequestBytes)
		next.ServeHTTP(w, r)
	})
}
