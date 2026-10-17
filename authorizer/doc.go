// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package authorizer is the vocabulary an authorization endpoint for Examen
// is written against: the actions examend asks, the resource kind each one
// acts on, the fields a resource carries, and the limits an allow may
// carry. Import it to write the endpoint EXAMEN_AUTHORIZER_URL points at,
// in Go, without keeping a copy of the strings.
//
// The envelope on the wire is latere.ai/x/pkg/authz's, and this package
// declares none of it: examend posts a request with the caller's subject,
// its claims, an action and a resource, and reads back an allow or a deny.
// Vocabulary is the action table as an authz.Vocabulary, which the client
// refuses an unknown action against, latere.ai/x/pkg/authz/server
// validates against, and latere.ai/x/pkg/authz/conformance drives its cases
// from:
//
//	conformance.Run(t, url, token, conformance.WithVocabulary(authorizer.Vocabulary()))
//
// The table holds the actions the routes of this release ask, in the order
// of specs/004-identity-and-authorization.md; an action of a later step
// joins it in the release whose routes ask it, and the changelog names it.
//
// A subject is the issuer and the sub joined, "https://issuer.example.com|alice",
// and the claims are the token's, verbatim. Examen reads none of them.
//
// The resource carries its fields flat beside its kind and id, under the
// names of the Field constants; Fields lists the fields each action may
// send, and SendsID whether it sends an id. No field is named kind or id,
// which the envelope keeps for the resource itself: an object's own type
// or kind is sent as metric_type, grader_kind, candidate_kind or
// workspace_kind. A field Examen does not know for a request is absent,
// never empty.
//
// An allow may carry limits, the Limits type in its JSON form. A member the
// endpoint does not set is left out. DecodeLimits is the reading half, and
// is here so an endpoint's tests can hold their answers to it: an unknown
// key, a figure below zero, an empty glob or audience, an unreadable
// attribution and an unknown executor are each errors, and examend fails
// the request closed.
//
// The promise, as for every public package of this module: additive within
// a module major. An action string never changes and never disappears, a
// resource kind stays the kind it is, and a limits member keeps its wire
// name and its meaning.
package authorizer
