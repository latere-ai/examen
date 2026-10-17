// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"net/http"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/otel"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/config"
)

// Who decides what a caller may do, as the start log names it.
const (
	// ModeAuthorizer asks the endpoint EXAMEN_AUTHORIZER_URL names.
	ModeAuthorizer = "authorizer"
	// ModeOwnerPolicy decides in process. It is the phrase examend logs at
	// start.
	ModeOwnerPolicy = "owner policy"
)

// fetchTimeout bounds one read of an issuer's discovery document or key
// set.
const fetchTimeout = 10 * time.Second

// Options are what a server hands access that no variable sets.
type Options struct {
	// Now is the clock of the verifier and of the client's cache. Nil takes
	// time.Now.
	Now func() time.Time
	// Observe receives every authorizer call's result and duration, for
	// the server's metric. Optional.
	Observe func(result string, seconds float64)
}

// Access is what a server's handlers depend on, and which mode is in force
// behind the authorizer.
type Access struct {
	Authenticator Authenticator
	Authorizer    Authorizer
	// Mode names who decides, for the line a server logs at start.
	Mode string

	verifier *Verifier
	decider  authz.Authorizer
}

// New builds the identity of a server from its settings: a verifier over
// EXAMEN_OIDC_ISSUERS and EXAMEN_OIDC_AUDIENCE, and the endpoint
// EXAMEN_AUTHORIZER_URL names asked with its bearer, or the owner policy
// when there is none. Every client it builds carries the tracing
// transport, so a call's span joins the caller's trace. It sends nothing.
func New(s config.Settings, o Options) (*Access, error) {
	v, err := NewVerifier(VerifierOptions{
		Issuers: s.OIDCIssuers, Audiences: s.OIDCAudiences, Now: o.Now,
		HTTP: &http.Client{Timeout: fetchTimeout, Transport: otel.Transport(nil)},
	})
	if err != nil {
		return nil, err
	}
	a := &Access{Authenticator: v, verifier: v, Mode: ModeOwnerPolicy, decider: OwnerPolicy{}}
	if s.AuthorizerURL != "" {
		client, err := NewClient(ClientOptions{
			URL: s.AuthorizerURL, Token: s.AuthorizerToken,
			// The contract's timeout bounds a call through the request's
			// context, so the client sets none of its own.
			HTTP: &http.Client{Transport: otel.Transport(nil)},
			Now:  o.Now, Observe: o.Observe,
		})
		if err != nil {
			return nil, err
		}
		a.decider, a.Mode = client, ModeAuthorizer
	}
	a.Authorizer = NewAuthorizer(a.decider)
	return a, nil
}

// ClientOptions configure the client that asks an operator's endpoint.
type ClientOptions struct {
	// URL and Token are EXAMEN_AUTHORIZER_URL and the bearer read from
	// EXAMEN_AUTHORIZER_TOKEN_FILE.
	URL   string
	Token config.Secret
	// HTTP sends the calls. Required.
	HTTP *http.Client
	// Now is the clock the cache runs on. Nil takes time.Now.
	Now func() time.Time
	// Observe receives every call's result and duration. Optional.
	Observe func(result string, seconds float64)
}

// NewClient builds the authorizer client. It sends nothing. The rules are
// the shared contract's: an allow is cached for its ttl, a deny briefly,
// and an answer that is no decision never; a call whose connection failed
// is tried once more; and anything but a well-formed answer is no decision.
// The vocabulary rides along, so an action outside Examen's table costs no
// round trip.
func NewClient(o ClientOptions) (*authz.Client, error) {
	return authz.NewClient(authz.Options{
		URL: o.URL, Token: o.Token.Reveal(), HTTP: o.HTTP, Now: o.Now, Observe: o.Observe,
		Vocabulary: authorizer.Vocabulary(),
	})
}

// Warm reads every issuer's key set once, and names each issuer that did
// not answer.
func (a *Access) Warm(ctx context.Context) error { return a.verifier.Warm(ctx) }

// Check sends the probe every authorizer denies. It reports an endpoint
// that did not answer and one that allowed the probe, which is an endpoint
// that does not read the request.
func (a *Access) Check(ctx context.Context) error { return Check(ctx, a.decider) }
