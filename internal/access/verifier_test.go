// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package access_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/authkit"
	"latere.ai/x/pkg/authkit/conformance"
	"latere.ai/x/pkg/authkit/issuertest"
	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/otel"

	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/config"
)

// audience is the audience the gate's identity block declares, and the
// default of EXAMEN_OIDC_AUDIENCE.
const audience = "examen"

// client is the instrumented client the server reads key sets with.
func client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: otel.Transport(nil)}
}

// issuer starts a stub issuer whose tokens default to this server's
// audience.
func issuer(t testing.TB) *issuertest.Server {
	t.Helper()
	return issuertest.New(t, issuertest.WithDefaultAudience(audience))
}

// verifying builds the verifier examend runs, over the issuers.
func verifying(t testing.TB, audiences []string, issuers ...string) *access.Verifier {
	t.Helper()
	v, err := access.NewVerifier(access.VerifierOptions{Issuers: issuers, Audiences: audiences, HTTP: client(10 * time.Second)})
	if err != nil {
		t.Fatalf("the verifier would not build: %v", err)
	}
	return v
}

// bearing is a request that carries the token as its bearer.
func bearing(t testing.TB, token string) *http.Request {
	t.Helper()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/experiments", nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

// The verifier passes the shared conformance suite, built as examend builds
// it: it verifies its own audience, calls the issuer for nothing but the
// key set, and refuses a token for another audience or for nobody.
func TestVerifierConformance(t *testing.T) {
	conformance.Run(t, conformance.Service{
		Audience: audience,
		New: func(tb testing.TB, issuerURL, _ string) authkit.Authenticator {
			tb.Helper()
			return verifying(tb, []string{audience}, issuerURL).Authenticator()
		},
	})
}

// A verified token becomes a caller whose subject is <iss>|<sub> and whose
// claims are the token's, verbatim, and two issuers that agree on a sub are
// two subjects.
func TestAVerifiedTokenBecomesACaller(t *testing.T) {
	one, two := issuer(t), issuer(t)
	v := verifying(t, []string{audience, "api.example.com"}, one.URL()+"/", two.URL())
	if got := v.Issuers(); !slices.Equal(got, []string{one.URL(), two.URL()}) {
		t.Fatalf("issuers %v", got)
	}
	token := one.Mint(issuertest.Claims{Sub: "alice", Extra: map[string]any{"tenant": "acme", "session": map[string]any{"id": "ses_1"}}})
	c, err := v.Authenticate(bearing(t, token))
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject != one.URL()+"|alice" || c.Subject != authz.Subject(one.URL(), "alice") || c.Issuer != one.URL() || c.Sub != "alice" {
		t.Fatalf("caller %+v", c)
	}
	session, _ := c.Claims["session"].(map[string]any)
	if c.Claims["tenant"] != "acme" || session["id"] != "ses_1" {
		t.Fatalf("claims %v", c.Claims)
	}
	other, err := v.Verify(two.Mint(issuertest.Claims{Sub: "alice"}))
	if err != nil {
		t.Fatal(err)
	}
	if other.Subject == c.Subject {
		t.Fatal("two issuers that agree on a sub are one subject")
	}
	// The second audience of the list is accepted as the first is.
	if _, err := v.Verify(one.Mint(issuertest.Claims{Sub: "alice", Aud: issuertest.StringList{"api.example.com"}})); err != nil {
		t.Fatalf("the second audience: %v", err)
	}
}

// A token for another audience, of an unlisted issuer, expired or not yet
// valid past the skew, or with a bad signature is refused with the reason,
// and the error never repeats the token.
func TestWhatTheVerifierRefuses(t *testing.T) {
	iss, other := issuer(t), issuer(t)
	v := verifying(t, []string{audience}, iss.URL())
	now := time.Now()
	good := iss.Mint(issuertest.Claims{Sub: "alice"})
	at := strings.LastIndexByte(good, '.') + 1
	forged := good[:at] + map[bool]string{true: "A", false: "B"}[good[at] != 'A'] + good[at+1:]
	cases := []struct{ name, token, reason string }{
		{"another audience", iss.Mint(issuertest.Claims{Sub: "alice", Aud: issuertest.StringList{"other"}}), "audience"},
		{"expired past the skew", iss.Mint(issuertest.Claims{Sub: "alice", Exp: now.Add(-5 * time.Minute).Unix()}), "expired"},
		{"not valid yet past the skew", iss.Mint(issuertest.Claims{Sub: "alice", Nbf: now.Add(5 * time.Minute).Unix()}), "nbf"},
		{"an unlisted issuer", other.Mint(issuertest.Claims{Sub: "alice"}), "issuer"},
		{"a bad signature", forged, "signature"},
		{"not a token", "not-a-token", "malformed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := v.Authenticate(bearing(t, c.token))
			var te *access.TokenError
			if !errors.As(err, &te) || te.Reason != c.reason || te.Retryable {
				t.Fatalf("got %v, want the reason %q", err, c.reason)
			}
			if strings.Contains(err.Error(), c.token) || te.Unwrap() == nil {
				t.Errorf("the error repeats the token or hides its cause: %v", err)
			}
		})
	}
	basic := bearing(t, "")
	basic.Header.Set("Authorization", "Basic YWxpY2U6c2VjcmV0")
	for _, r := range []*http.Request{bearing(t, ""), basic} {
		if _, err := v.Authenticate(r); !errors.Is(err, access.ErrMissingToken) {
			t.Errorf("no bearer: %v", err)
		}
	}
	// An issuer that does not answer is a refusal a caller may retry.
	down, err := access.NewVerifier(access.VerifierOptions{
		Issuers: []string{iss.URL()}, Audiences: []string{audience}, HTTP: client(300 * time.Millisecond),
	})
	if err != nil {
		t.Fatal(err)
	}
	iss.Hang()
	_, err = down.Verify(iss.Mint(issuertest.Claims{Sub: "alice"}))
	iss.Resume()
	var te *access.TokenError
	if !errors.As(err, &te) || te.Reason != "issuer_unavailable" || !te.Retryable {
		t.Fatalf("an issuer that does not answer: %v", err)
	}
}

// exp and nbf are read with 30 seconds of skew.
func TestTheSkewOnExpAndNbf(t *testing.T) {
	iss := issuer(t)
	v := verifying(t, []string{audience}, iss.URL())
	now := time.Now()
	within := access.ClockSkew / 3
	for name, claims := range map[string]issuertest.Claims{
		"expired within the skew":       {Sub: "alice", Exp: now.Add(-within).Unix()},
		"not valid yet within the skew": {Sub: "alice", Nbf: now.Add(within).Unix()},
	} {
		if _, err := v.Verify(iss.Mint(claims)); err != nil {
			t.Errorf("%s: refused: %v", name, err)
		}
	}
}

func TestWarmReadsEachIssuersKeys(t *testing.T) {
	iss := issuer(t)
	if err := verifying(t, []string{audience}, iss.URL()).Warm(t.Context()); err != nil {
		t.Fatal(err)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	if err := verifying(t, []string{audience}, gone.URL).Warm(t.Context()); err == nil {
		t.Error("warming an issuer that does not answer reports nothing")
	}
}

// A server that lists no issuer, names no audience or has no client is
// refused, naming the variable.
func TestAVerifierNeedsAnIssuerAndAnAudience(t *testing.T) {
	for name, o := range map[string]access.VerifierOptions{
		"no issuer":         {Audiences: []string{audience}, HTTP: client(time.Second)},
		"no audience":       {Issuers: []string{"https://issuer.example.com"}, HTTP: client(time.Second)},
		"an empty audience": {Issuers: []string{"https://issuer.example.com"}, Audiences: []string{""}, HTTP: client(time.Second)},
		"no client":         {Issuers: []string{"https://issuer.example.com"}, Audiences: []string{audience}},
	} {
		if _, err := access.NewVerifier(o); err == nil {
			t.Errorf("%s: built", name)
		}
	}
	if _, err := access.New(config.Settings{OIDCAudiences: []string{audience}}, access.Options{}); err == nil || !strings.Contains(err.Error(), "EXAMEN_OIDC_ISSUERS") {
		t.Errorf("no issuer: %v", err)
	}
}
