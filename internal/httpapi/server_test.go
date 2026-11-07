// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"latere.ai/x/pkg/requestid"

	"latere.ai/x/examen/api"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/version"
)

// fakeStore is a store whose readiness and keys a test chooses. The tables
// a test does not choose are nil, and a route that reaches one fails the
// test.
type fakeStore struct {
	store.Store
	ready error
	keys  store.Idempotency
}

func (f *fakeStore) Idempotency() store.Idempotency { return f.keys }
func (f *fakeStore) Ready(context.Context) error    { return f.ready }
func (f *fakeStore) Close() error                   { return nil }
func (f *fakeStore) setReady(err error) *fakeStore  { f.ready = err; return f }

// The probes need no token: /livez answers while the process runs, /readyz
// names the failing check, draining or database, and /version the build.
func TestProbes(t *testing.T) {
	var draining atomic.Bool
	st := &fakeStore{}
	h := newHarness(t, st, nil, func(s *Server) { s.Draining = draining.Load })
	if a := h.do(http.MethodGet, "/livez", "", nil); a.status != http.StatusOK {
		t.Fatalf("/livez %d", a.status)
	}
	if a := h.do(http.MethodGet, "/readyz", "", nil); a.status != http.StatusOK {
		t.Fatalf("/readyz %d %s", a.status, a.body)
	}
	st.setReady(errors.New("store: the database does not answer"))
	if a := h.do(http.MethodGet, "/readyz", "", nil); a.status != http.StatusServiceUnavailable || !strings.Contains(string(a.body), "database:") {
		t.Fatalf("/readyz with the database lost: %d %s", a.status, a.body)
	}
	st.setReady(nil)
	draining.Store(true)
	if a := h.do(http.MethodGet, "/readyz", "", nil); a.status != http.StatusServiceUnavailable || !strings.Contains(string(a.body), "draining:") {
		t.Fatalf("/readyz after the stop signal: %d %s", a.status, a.body)
	}
	a := h.do(http.MethodGet, "/version", "", nil)
	var b struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		BuildTime string `json:"build_time"`
	}
	if err := json.Unmarshal(a.body, &b); err != nil || a.status != http.StatusOK || b.Version != version.Version || b.Commit != version.Commit {
		t.Fatalf("/version %d %s", a.status, a.body)
	}
	// A server with no drain hook is never draining.
	if a := newHarness(t, &fakeStore{}, nil, nil).do(http.MethodGet, "/readyz", "", nil); a.status != http.StatusOK {
		t.Fatalf("/readyz with no drain hook: %d", a.status)
	}
}

// The build identity is public below the base path, the prefix the ingress
// forwards, and is the body the root serves; with no base path there is no
// second route to add.
func TestVersionBelowTheBasePath(t *testing.T) {
	oldV, oldC, oldD := version.Version, version.Commit, version.Date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldV, oldC, oldD })
	version.Version, version.Commit, version.Date = "v1.2.3", "abc1234", "2026-11-05T00:00:00Z"
	h := newHarness(t, &fakeStore{}, nil, func(s *Server) { s.BasePath = "/v1/evals" })
	a := h.do(http.MethodGet, "/v1/evals/version", "", nil)
	want := `{"version":"v1.2.3","commit":"abc1234","build_time":"2026-11-05T00:00:00Z"}`
	if a.status != http.StatusOK || strings.TrimSpace(string(a.body)) != want {
		t.Fatalf("/v1/evals/version with no token: %d %s", a.status, a.body)
	}
	if root := h.do(http.MethodGet, "/version", "", nil); string(root.body) != string(a.body) {
		t.Errorf("the root /version %s differs from %s", root.body, a.body)
	}
	if a := h.do(http.MethodPost, "/v1/evals/version", "", nil); a.status == http.StatusOK {
		t.Errorf("POST /v1/evals/version answered %d", a.status)
	}
	if a := newHarness(t, &fakeStore{}, nil, func(s *Server) { s.BasePath = "" }).do(http.MethodGet, "/version", "", nil); a.status != http.StatusOK {
		t.Errorf("/version with no base path: %d", a.status)
	}
}

// The contract is served below the base path to anyone; a path no route
// serves is the contract's 404, for every method, below the base path and
// beside it.
func TestTheContractAndNotFound(t *testing.T) {
	h := newHarness(t, &fakeStore{}, nil, nil)
	a := h.do(http.MethodGet, "/v1/openapi.yaml", "", nil)
	if a.status != http.StatusOK || a.header.Get("Content-Type") != "application/yaml" || string(a.body) != string(api.OpenAPI) {
		t.Fatalf("the contract: %d %q", a.status, a.header.Get("Content-Type"))
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1/nothing"}, {http.MethodPost, "/v1/openapi.yaml"}, {http.MethodDelete, "/v1/"},
		{http.MethodGet, "/nothing"}, {http.MethodPut, "/"}, {http.MethodPost, "/livez"},
	} {
		a := h.do(c.method, c.path, "t-alice", nil)
		if code, d := a.envelope(t); a.status != http.StatusNotFound || code != apierr.CodeNotFound || d["detail"] == "" {
			t.Errorf("%s %s: %d %s", c.method, c.path, a.status, a.body)
		}
	}
}

// The whole surface moves under the base path.
func TestBasePath(t *testing.T) {
	h := newHarness(t, &fakeStore{}, []route{listing(1)}, func(s *Server) { s.BasePath = "/api" })
	if a := h.do(http.MethodGet, "/api/openapi.yaml", "", nil); a.status != http.StatusOK {
		t.Fatalf("the contract under /api: %d", a.status)
	}
	if a := h.do(http.MethodGet, "/api/things", "t-alice", nil); a.status != http.StatusOK {
		t.Fatalf("a route under /api: %d %s", a.status, a.body)
	}
	if a := h.do(http.MethodGet, "/v1/things", "t-alice", nil); a.status != http.StatusNotFound {
		t.Fatalf("a route under the default base path: %d", a.status)
	}
	root := newHarness(t, &fakeStore{}, []route{listing(1)}, func(s *Server) { s.BasePath = "" })
	if a := root.do(http.MethodGet, "/things", "t-alice", nil); a.status != http.StatusOK {
		t.Fatalf("a route at the root: %d", a.status)
	}
	if a := root.do(http.MethodGet, "/livez", "", nil); a.status != http.StatusOK {
		t.Fatalf("a probe beside routes at the root: %d", a.status)
	}
	if a := root.do(http.MethodGet, "/elsewhere", "", nil); a.status != http.StatusNotFound {
		t.Fatalf("no route at the root: %d", a.status)
	}
}

// Every route below the base path but the contract needs a bearer: none is
// missing_token, a refused one invalid_token with the verifier's reason,
// retryable only while the issuer is unavailable.
func TestRoutesNeedABearer(t *testing.T) {
	h := newHarness(t, &fakeStore{}, []route{listing(1)}, nil)
	for _, c := range []struct {
		bearer, code, reason string
		retryable            bool
	}{
		{"", apierr.CodeMissingToken, "", false},
		{"t-expired", apierr.CodeInvalidToken, "expired", false},
		{"t-unavailable", apierr.CodeInvalidToken, "issuer_unavailable", true},
	} {
		a := h.do(http.MethodGet, "/v1/things", c.bearer, nil)
		code, d := a.envelope(t)
		if a.status != http.StatusUnauthorized || code != c.code || d["retryable"] != c.retryable || (c.reason != "" && d["reason"] != c.reason) {
			t.Errorf("%q: %d %s", c.bearer, a.status, a.body)
		}
	}
	a := h.do(http.MethodGet, "/v1/things", "t-broken", nil)
	if code, _ := a.envelope(t); a.status != http.StatusInternalServerError || code != apierr.CodeInternal || !strings.Contains(h.log(), "the verifier broke") {
		t.Fatalf("a verifier that broke: %d %s", a.status, a.body)
	}
	// An error nobody classified is logged under the route's pattern, never
	// its path, which may hold what a caller put in it.
	byID := route{method: http.MethodGet, path: "/things/{id}", handle: func(*Server, http.ResponseWriter, *http.Request, access.Caller) error {
		return errors.New("a bug")
	}}
	h = newHarness(t, &fakeStore{}, []route{byID}, nil)
	a = h.do(http.MethodGet, "/v1/things/secret-in-path", "t-alice", nil)
	if a.status != http.StatusInternalServerError || !strings.Contains(h.log(), "GET /v1/things/{id}") || strings.Contains(h.log(), "secret-in-path") {
		t.Fatalf("the log of an unclassified error: %d %s", a.status, h.log())
	}
}

// A route asks before it acts; a deny that names no stored object is
// forbidden with the reason, or the code the reason names; an authorizer
// that gives no answer is authorizer_unavailable with nothing done.
func TestARouteAsks(t *testing.T) {
	made := &counter{}
	h := newHarness(t, pgStore(t), []route{creating(made)}, nil)
	if a := h.do(http.MethodPost, "/v1/things", "t-alice", map[string]any{"name": "one"}); a.status != http.StatusCreated {
		t.Fatalf("a create: %d %s", a.status, a.body)
	}
	a := h.do(http.MethodPost, "/v1/things", "t-alice", map[string]any{"name": "two", "owner": bob})
	if code, d := a.envelope(t); a.status != http.StatusForbidden || code != apierr.CodeForbidden || d["reason"] != "not_owner" {
		t.Fatalf("a create naming another owner: %d %s", a.status, a.body)
	}
	down := newHarness(t, pgStore(t), []route{creating(made)}, func(s *Server) { s.Authz = unavailable{} })
	a = down.do(http.MethodPost, "/v1/things", "t-alice", map[string]any{"name": "three"})
	if code, d := a.envelope(t); a.status != http.StatusServiceUnavailable || code != apierr.CodeAuthorizerUnavailable || d["retryable"] != true {
		t.Fatalf("no answer: %d %s", a.status, a.body)
	}
	mistaken := newHarness(t, pgStore(t), []route{creating(made)}, func(s *Server) { s.Authz = broken{} })
	if a := mistaken.do(http.MethodPost, "/v1/things", "t-alice", map[string]any{"name": "four"}); a.status != http.StatusInternalServerError {
		t.Fatalf("a mistaken question: %d %s", a.status, a.body)
	}
	if made.n != 1 {
		t.Fatalf("%d things were made", made.n)
	}
}

// A subject past API_REQUESTS_PER_MINUTE on one replica is rate_limited
// with Retry-After in whole seconds; another subject is not.
func TestRateLimit(t *testing.T) {
	clock := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	h := newHarness(t, &fakeStore{}, []route{listing(1)}, func(s *Server) { s.Now = func() time.Time { return clock } })
	for i := range limits.APIRequestsPerMinute {
		if a := h.do(http.MethodGet, "/v1/things", "t-alice", nil); a.status != http.StatusOK {
			t.Fatalf("request %d: %d", i+1, a.status)
		}
	}
	a := h.do(http.MethodGet, "/v1/things", "t-alice", nil)
	code, _ := a.envelope(t)
	wait, err := strconv.Atoi(a.header.Get("Retry-After"))
	if a.status != http.StatusTooManyRequests || code != apierr.CodeRateLimited || err != nil || wait < 1 || wait > 60 {
		t.Fatalf("past the rate: %d %q %s", a.status, a.header.Get("Retry-After"), a.body)
	}
	if a := h.do(http.MethodGet, "/v1/things", "t-bob", nil); a.status != http.StatusOK {
		t.Fatalf("another subject: %d", a.status)
	}
}

// A client address past ADDRESS_REQUESTS_PER_MINUTE on one replica is
// rate_limited before its token is read, so requests with no valid token
// are held too; another address is not. Behind a trusted proxy each client
// it names has a rate of its own, and a peer the installation does not
// trust is held by its own address whatever X-Forwarded-For says. The
// routes that take no token are held to PUBLIC_REQUESTS_PER_MINUTE from
// one address, and the probes to nothing.
func TestAddressRateLimit(t *testing.T) {
	clock := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	auth := &countedAuth{inner: tokens{}}
	h := newHarness(t, &fakeStore{}, []route{listing(1)}, func(s *Server) {
		s.Now, s.Auth = func() time.Time { return clock }, auth
		s.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	})
	send := func(path, peer, forwarded string) answer {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
		r.RemoteAddr = peer
		r.Header.Set("Authorization", "Bearer t-expired")
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		w := httptest.NewRecorder()
		h.h.ServeHTTP(w, r)
		return answer{status: w.Code, header: w.Header(), body: w.Body.Bytes()}
	}
	limited := func(a answer) bool {
		code, _ := a.envelope(t)
		return a.status == http.StatusTooManyRequests && code == apierr.CodeRateLimited && a.header.Get("Retry-After") != ""
	}
	for i := range limits.AddressRequestsPerMinute {
		if a := send("/v1/things", "192.0.2.1:1234", fmt.Sprintf("203.0.113.%d", i%200)); a.status != http.StatusUnauthorized {
			t.Fatalf("request %d from an untrusted peer: %d %s", i+1, a.status, a.body)
		}
	}
	read := auth.n
	if a := send("/v1/things", "192.0.2.1:1234", "203.0.113.250"); !limited(a) || auth.n != read {
		t.Fatalf("past the rate, naming a new address: %d %s, the token read %d times more", a.status, a.body, auth.n-read)
	}
	if a := send("/v1/things", "192.0.2.2:1234", ""); a.status != http.StatusUnauthorized {
		t.Fatalf("another peer: %d %s", a.status, a.body)
	}
	for i := range limits.AddressRequestsPerMinute {
		if a := send("/v1/things", "10.0.0.1:1234", "203.0.113.7"); a.status != http.StatusUnauthorized {
			t.Fatalf("request %d of a client behind the proxy: %d %s", i+1, a.status, a.body)
		}
	}
	if a := send("/v1/things", "10.0.0.1:1234", "203.0.113.7"); !limited(a) {
		t.Fatalf("a client behind the proxy past the rate: %d %s", a.status, a.body)
	}
	if a := send("/v1/things", "10.0.0.1:1234", "203.0.113.8"); a.status != http.StatusUnauthorized {
		t.Fatalf("another client behind the proxy: %d %s", a.status, a.body)
	}
	for _, path := range []string{"/v1/version", "/v1/openapi.yaml"} {
		peer := "198.51.100.1:1234"
		if path == "/v1/openapi.yaml" {
			peer = "198.51.100.2:1234"
		}
		for i := range limits.PublicRequestsPerMinute {
			if a := send(path, peer, ""); a.status != http.StatusOK {
				t.Fatalf("%s request %d: %d %s", path, i+1, a.status, a.body)
			}
		}
		if a := send(path, peer, ""); !limited(a) {
			t.Fatalf("%s past the public rate: %d %s", path, a.status, a.body)
		}
	}
	for i := range limits.PublicRequestsPerMinute + 1 {
		if a := send("/livez", "198.51.100.1:1234", ""); a.status != http.StatusOK {
			t.Fatalf("probe %d: %d %s", i+1, a.status, a.body)
		}
	}
}

// countedAuth counts the requests whose token it reads.
type countedAuth struct {
	inner access.Authenticator
	n     int
}

func (c *countedAuth) Authenticate(r *http.Request) (access.Caller, error) {
	c.n++
	return c.inner.Authenticate(r)
}

// A body past MAX_REQUEST_BYTES is request_too_large: refused before it is
// read when it declares its length, and cut at the bound when it does not.
func TestRequestTooLarge(t *testing.T) {
	made := &counter{}
	h := newHarness(t, &fakeStore{}, []route{creating(made)}, nil)
	big := `{"name":"` + strings.Repeat("x", limits.MaxRequestBytes) + `"}`
	a := h.do(http.MethodPost, "/v1/things", "t-alice", big)
	if code, _ := a.envelope(t); a.status != http.StatusRequestEntityTooLarge || code != apierr.CodeRequestTooLarge {
		t.Fatalf("a declared length: %d %s", a.status, a.body)
	}
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/things", strings.NewReader(big))
	r.ContentLength = -1
	r.Header.Set("Authorization", "Bearer t-alice")
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body of no declared length: %d %s", rec.Code, rec.Body)
	}
	if made.n != 0 {
		t.Fatal("a body past the bound made something")
	}
}

// Every answer carries a request id: the caller's when it is short visible
// ASCII, one of the server's otherwise, which the authorizer is told.
func TestRequestIDs(t *testing.T) {
	h := newHarness(t, &fakeStore{}, nil, nil)
	if a := h.do(http.MethodGet, "/v1/openapi.yaml", "", nil, RequestIDHeader, "req-from-caller"); a.header.Get(RequestIDHeader) != "req-from-caller" {
		t.Fatalf("the caller's id: %q", a.header.Get(RequestIDHeader))
	}
	for _, bad := range []string{"", strings.Repeat("x", requestid.MaxLen+1), "has space", "caf\u00e9"} {
		got := h.do(http.MethodGet, "/v1/openapi.yaml", "", nil, RequestIDHeader, bad).header.Get(RequestIDHeader)
		if got == bad || !strings.HasPrefix(got, "req_") {
			t.Errorf("%q answered %q", bad, got)
		}
	}
	seen := make(chan string, 1)
	asking := route{method: http.MethodGet, path: "/asks", handle: func(_ *Server, w http.ResponseWriter, r *http.Request, _ access.Caller) error {
		seen <- access.RequestInfo(r).ID
		w.WriteHeader(http.StatusNoContent)
		return nil
	}}
	h = newHarness(t, &fakeStore{}, []route{asking}, nil)
	a := h.do(http.MethodGet, "/v1/asks", "t-alice", nil)
	if id := <-seen; id == "" || id != a.header.Get(RequestIDHeader) {
		t.Fatalf("the authorizer is told %q and the answer carries %q", id, a.header.Get(RequestIDHeader))
	}
}

// The served routes are the contract's and the public ones.
func TestRoutes(t *testing.T) {
	got := Routes()
	if len(got) != len(routes)+len(public) || got[len(got)-1] != (Route{http.MethodGet, "/openapi.yaml"}) {
		t.Fatalf("%v", got)
	}
	if (&Server{Store: &fakeStore{}}).Handler() == nil {
		t.Fatal("no handler")
	}
}

// The request's span reads the client address from the first entry of
// X-Forwarded-For, so the header the handlers below the span see holds
// the address the authorizer is told and nothing a client wrote.
func TestTheSpanSeesTheTrustedClientAddress(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct {
		name, peer, forwarded string
		want                  []string
	}{
		{"an untrusted peer's header is dropped", "192.0.2.1:4242", "203.0.113.9", nil},
		{"a client's own entry before a trusted proxy's is dropped", "10.1.2.3:4242", "198.51.100.7, 203.0.113.9", []string{"203.0.113.9"}},
		{"entries across headers count as one list", "10.1.2.3:4242", "198.51.100.7|203.0.113.9, 10.9.9.9", []string{"203.0.113.9"}},
		{"a chain of trusted proxies only names the peer", "10.1.2.3:4242", "10.4.5.6", nil},
		{"no header stays none", "10.1.2.3:4242", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan []string, 1)
			asking := route{method: http.MethodGet, path: "/asks", handle: func(_ *Server, w http.ResponseWriter, r *http.Request, _ access.Caller) error {
				seen <- r.Header.Values("X-Forwarded-For")
				w.WriteHeader(http.StatusNoContent)
				return nil
			}}
			h := newHarness(t, &fakeStore{}, []route{asking}, func(s *Server) { s.TrustedProxies = trusted })
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/asks", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Authorization", "Bearer t-alice")
			if tc.forwarded != "" {
				for v := range strings.SplitSeq(tc.forwarded, "|") {
					r.Header.Add("X-Forwarded-For", v)
				}
			}
			h.h.ServeHTTP(httptest.NewRecorder(), r)
			if got := <-seen; !slices.Equal(got, tc.want) {
				t.Fatalf("X-Forwarded-For %q, want %q", got, tc.want)
			}
		})
	}
}

// The authorizer is told the client address: the peer's, with X-Forwarded-For
// believed only from a trusted proxy, and an IPv4-mapped peer unmapped.
func TestAuthorizerIsToldTheClientAddress(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	for _, tc := range []struct {
		name, peer, forwarded string
		trusted               []netip.Prefix
		want                  string
	}{
		{"no proxy trusted", "192.0.2.1:4242", "203.0.113.9", nil, "192.0.2.1"},
		{"a trusted proxy names the client", "10.1.2.3:4242", "203.0.113.9", trusted, "203.0.113.9"},
		{"an untrusted peer cannot name one", "192.0.2.1:4242", "203.0.113.9", trusted, "192.0.2.1"},
		{"a mapped peer is unmapped", "[::ffff:192.0.2.7]:4242", "", nil, "192.0.2.7"},
		{"a mapped client is unmapped", "10.1.2.3:4242", "::ffff:203.0.113.9", trusted, "203.0.113.9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := make(chan string, 1)
			asking := route{method: http.MethodGet, path: "/asks", handle: func(_ *Server, w http.ResponseWriter, r *http.Request, _ access.Caller) error {
				seen <- access.RequestInfo(r).IP
				w.WriteHeader(http.StatusNoContent)
				return nil
			}}
			h := newHarness(t, &fakeStore{}, []route{asking}, func(s *Server) { s.TrustedProxies = tc.trusted })
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/asks", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("Authorization", "Bearer t-alice")
			if tc.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tc.forwarded)
			}
			h.h.ServeHTTP(httptest.NewRecorder(), r)
			if got := <-seen; got != tc.want {
				t.Fatalf("told %q, want %q", got, tc.want)
			}
		})
	}
}
