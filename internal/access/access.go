// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package access is examend's side of specs/004-identity-and-authorization.md:
// who a caller is, and who decides what that caller may do.
//
// Who is an Authenticator. A bearer is verified against one of the issuers
// EXAMEN_OIDC_ISSUERS lists, through latere.ai/x/pkg/authkit/jwt, and
// becomes a Caller: the rendered subject, its two halves apart, and every
// claim of the token verbatim. No claim is read for meaning here.
//
// What is an Authorizer. One question per decision, in the vocabulary of
// latere.ai/x/examen/authorizer, goes to the endpoint EXAMEN_AUTHORIZER_URL
// names through the client of latere.ai/x/pkg/authz, which caches, retries
// and fails closed by that contract's rules. With no endpoint configured the
// owner policy of this package answers. A handler cannot tell which is in
// force.
//
// A handler is written in four steps: authenticate, build the resource from
// the request and the stored object, ask, and act within the decision.
package access

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"

	"latere.ai/x/pkg/authz"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/limits"
)

// Caller is a verified bearer. Subject is the one string an owner field, a
// created_by and an authorizer request carry: the issuer and the sub joined
// by a pipe. Claims is every claim of the token, verbatim. It is handed to
// the authorizer as it came and read by nothing in Examen.
type Caller struct {
	Subject string
	Issuer  string
	Sub     string
	Claims  map[string]any
}

// Authenticator turns a request's bearer into a caller.
type Authenticator interface {
	// Authenticate verifies the request's bearer: ErrMissingToken for a
	// request that carries none, and a *TokenError for one that is refused.
	Authenticate(r *http.Request) (Caller, error)
}

// Authorizer answers one question about one caller.
type Authorizer interface {
	// Authorize asks whether the caller may do the question's action to its
	// resource. A deny is a Decision and not an error. ErrUnavailable is a
	// question that got no decision, and the request fails closed with it.
	Authorize(ctx context.Context, caller Caller, q Question) (Decision, error)
}

// The errors of a question.
var (
	// ErrMissingToken is a request that carries no bearer.
	ErrMissingToken = errors.New("access: the request carries no bearer token")
	// ErrUnavailable is a question that got no well-formed answer: the
	// authorizer did not answer, answered another status, a body that does
	// not parse or carries no allow, limits that cannot be read, or a
	// filter that cannot be applied.
	ErrUnavailable = errors.New("access: the authorizer gave no decision")
)

// TokenError is a bearer the verifier refused. Reason is the verifier's
// word for why, from jwt.ReasonOf, and never a part of the token.
type TokenError struct {
	Reason    string
	Retryable bool
	err       error
}

func (e *TokenError) Error() string { return "access: the bearer token was refused: " + e.Reason }

// Unwrap is the verifier's own error.
func (e *TokenError) Unwrap() error { return e.err }

// Question is what a handler asks: the action of its route, the resource
// built from the request and the stored object, and what the authorizer
// learns about the request itself.
type Question struct {
	Action   string
	Resource authz.Resource
	Request  authz.Caller
}

// Ask builds the question a request asks.
func Ask(r *http.Request, action string, resource authz.Resource) Question {
	return Question{Action: action, Resource: resource, Request: RequestInfo(r)}
}

// RequestInfo is what the authorizer is told about the request: its id,
// the peer address as the server sees it, and the user agent.
func RequestInfo(r *http.Request) authz.Caller {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// An address with no port is already the host.
		host = r.RemoteAddr
	}
	return authz.Caller{ID: r.Header.Get("X-Request-Id"), IP: host, UserAgent: r.UserAgent()}
}

// Decision is the answer to a question.
type Decision struct {
	// Allow says whether the caller may. Nothing else is set on a deny but
	// Reason.
	Allow bool
	// Reason is the authorizer's own word for its answer. On a deny it is
	// the developer detail of the refusal and never the sentence a person
	// reads.
	Reason string
	// Limits are what the request is held to: the members the question's
	// action reads (specs/004-identity-and-authorization.md), each held to
	// the limit of spec 002 it narrows. Every other member is zero.
	Limits authorizer.Limits
	// Filter narrows a list to owners, and, on the lists of objects that
	// carry metadata, to labels. Nil on an allow of a list is the caller's
	// own.
	Filter *authz.Filter
}

// Owners are the owners a list may show: the filter's, or the caller's own
// when the allow named none, narrowed by the owner the request named, which
// never widens them.
func (d Decision) Owners(caller Caller, requested string) []string {
	owners := []string{caller.Subject}
	if d.Filter != nil && len(d.Filter.Owners) > 0 {
		owners = slices.Clone(d.Filter.Owners)
	}
	if requested == "" {
		return owners
	}
	if slices.Contains(owners, requested) {
		return []string{requested}
	}
	return nil
}

// asker asks one authz.Authorizer, the shared client over the operator's
// endpoint or the owner policy, and reads its answer. The cache, the retry
// and the timeout are the client's. Nothing here decides.
type asker struct{ inner authz.Authorizer }

// NewAuthorizer wraps whichever authorizer a server runs.
func NewAuthorizer(inner authz.Authorizer) Authorizer { return &asker{inner: inner} }

func (a *asker) Authorize(ctx context.Context, caller Caller, q Question) (Decision, error) {
	// A question outside the vocabulary, or about another kind than its
	// action acts on, is a mistake in a handler. It is refused before it is
	// sent, so the mistake cannot be answered with an allow.
	if kind := authorizer.Kind(q.Action); kind == "" || kind != q.Resource.Kind {
		return Decision{}, fmt.Errorf("access: the action %q does not act on the kind %q", q.Action, q.Resource.Kind)
	}
	d, err := a.inner.Authorize(ctx, envelope(caller, q))
	if err != nil {
		// The error underneath may name the endpoint, so it is wrapped for
		// the log and never shown to the caller.
		return Decision{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, q.Action, err)
	}
	if !d.Allow {
		return Decision{Reason: reasonOf(d)}, nil
	}
	out := Decision{Allow: true, Reason: d.Reason, Filter: d.Filter}
	if d.Filter != nil && len(d.Filter.Labels) > 0 && !labeled(q.Action) {
		// Only tasks, candidates and experiments carry metadata, so a
		// filter by labels on any other list cannot be applied, and a list
		// it cannot narrow is no answer.
		return Decision{}, fmt.Errorf("%w: the filter of %s names labels", ErrUnavailable, q.Action)
	}
	if readsLimits(q.Action) {
		l, err := authorizer.DecodeLimits(d)
		if err != nil {
			return Decision{}, fmt.Errorf("%w: %s: %w", ErrUnavailable, q.Action, err)
		}
		out.Limits = held(q.Action, l)
	}
	return out, nil
}

// labeled reports whether a list action's objects carry metadata a filter's
// labels apply to.
func labeled(action string) bool {
	switch action {
	case authorizer.ActionTaskList, authorizer.ActionCandidateList, authorizer.ActionExperimentList:
		return true
	}
	return false
}

// applies are the actions whose allow names the owner a created object is
// recorded under.
var applies = []string{
	authorizer.ActionMetricApply, authorizer.ActionGraderApply, authorizer.ActionTaskApply, authorizer.ActionSuiteApply,
	authorizer.ActionTaxonomyApply, authorizer.ActionCandidateApply, authorizer.ActionExperimentCreate, authorizer.ActionBaselinePin,
}

// readsLimits reports whether the allow of an action carries limits Examen
// holds. The limits of any other allow are not read.
func readsLimits(action string) bool {
	return slices.Contains(applies, action) || action == authorizer.ActionExperimentUpdate
}

// held keeps the members an action's allow is read for, each narrowed to
// the limit of spec 002 it may lower and never raise.
func held(action string, l authorizer.Limits) authorizer.Limits {
	var out authorizer.Limits
	if slices.Contains(applies, action) {
		out.Owner = l.Owner
	}
	switch action {
	case authorizer.ActionExperimentCreate:
		out.MaxParallelism, out.MaxTrials = l.MaxParallelism, l.MaxTrials
		out.MaxActiveExperiments = l.MaxActiveExperiments
		out.Executors, out.JudgeModels, out.CredentialAudiences = l.Executors, l.JudgeModels, l.CredentialAudiences
		out.Budget = l.Budget
	case authorizer.ActionExperimentUpdate:
		out.MaxParallelism, out.MaxTrials = l.MaxParallelism, l.MaxTrials
	}
	out.MaxParallelism = min(out.MaxParallelism, limits.MaxParallelism)
	out.MaxActiveExperiments = min(out.MaxActiveExperiments, limits.MaxActiveExperimentsPerOwner)
	return out
}

// envelope is what one call carries: the caller's subject and its claims
// verbatim, the action, the resource, and what is known of the request.
func envelope(c Caller, q Question) authz.Request {
	claims := c.Claims
	if claims == nil {
		claims = map[string]any{}
	}
	return authz.Request{
		Subject: c.Subject, Issuer: c.Issuer, Sub: c.Sub, Claims: claims,
		Action: q.Action, Resource: q.Resource, Request: q.Request,
	}
}

// reasonOf is the authorizer's reason, or a word when it named none, so a
// refusal always says something.
func reasonOf(d authz.Decision) string {
	if d.Reason == "" {
		return "no_reason_given"
	}
	return d.Reason
}

// Check sends the probe every authorizer denies and reports one that
// answered nothing or allowed it. An allow is an endpoint that does not
// read the request.
func Check(ctx context.Context, a authz.Authorizer) error {
	return authz.Check(ctx, a, authorizer.ActionExperimentRead, authorizer.KindExperiment)
}
