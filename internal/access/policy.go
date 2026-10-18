// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"context"
	"slices"
	"strings"

	"latere.ai/x/pkg/authz"

	"latere.ai/x/examen/authorizer"
)

// ReasonUnknownAction is the deny of an action outside the vocabulary, or of
// one asked about another kind than it acts on. The other reasons the owner
// policy denies with are the shared contract's: probe, anonymous, not_owner
// and grant.
const ReasonUnknownAction = "unknown_action"

// creates are the actions that make an object under the owner they name,
// or under the caller's subject when they name none.
var creates = []string{
	authorizer.ActionMetricApply, authorizer.ActionGraderApply, authorizer.ActionTaskApply, authorizer.ActionSuiteApply,
	authorizer.ActionTaxonomyApply, authorizer.ActionCandidateApply, authorizer.ActionExperimentCreate, authorizer.ActionBaselinePin,
}

// readsShared are the reads the policy allows of another owner's object an
// experiment of the caller's pins, or a share names for the caller.
var readsShared = []string{
	authorizer.ActionTaskRead, authorizer.ActionMetricRead, authorizer.ActionGraderRead,
	authorizer.ActionSuiteRead, authorizer.ActionTaxonomyRead,
}

// OwnerPolicy is what decides with EXAMEN_AUTHORIZER_URL unset
// (specs/004-identity-and-authorization.md). Its rows, in the order they are
// tried:
//
//   - the probe id, as any subject: deny, probe;
//   - no subject: deny, anonymous;
//   - an action outside the vocabulary, or asked about another kind: deny,
//     unknown_action;
//   - a list: allow, with a filter that narrows it to the subject's own;
//   - a stored object whose owner is the subject, any action: allow;
//   - a read of a task, metric, grader, suite or taxonomy that is shared
//     with the subject or pinned by an experiment of the subject's: allow;
//   - a read of an experiment or a baseline shared with the subject:
//     allow;
//   - an apply, create or pin that names no owner, or names the subject:
//     allow;
//   - experiment.run whose created_by and owner are the subject: allow;
//   - anything else: deny, not_owner.
//
// A subject therefore acts on what it owns and nothing else, so
// organizations, and services acting for their users, need an authorizer.
// No row allows an action that runs an experiment because a share names
// the subject: a share is a grant of reading, and running stays with the
// experiment's creator, its owner and the owner's members
// (specs/004-identity-and-authorization.md), which under this policy are
// one subject.
// The policy has no administrators and names no limit of its own. Its
// answer is narrowed by the grants of the caller's token, as an
// authorizer's is.
type OwnerPolicy struct{}

// Authorize answers one request, so the owner policy and an operator's
// endpoint are one seam to everything above. A token whose grants cannot be
// read is denied: its reach is unknown, and the closed answer is a deny.
func (p OwnerPolicy) Authorize(_ context.Context, req authz.Request) (authz.Decision, error) {
	d := p.decide(req)
	if !d.Allow {
		return d, nil
	}
	grants, readable := grantsOf(req.Claims)
	if !readable {
		return authz.Decision{Reason: authz.ReasonGrant}, nil
	}
	return authz.Restrict(authorizer.Core, d, req, grants), nil
}

// grantsOf reads the grants a token carries. readable is false for a claim
// that does not read as grants, which is a deny and no outage.
func grantsOf(claims map[string]any) (grants authz.Grants, readable bool) {
	grants, err := authz.ParseGrants(claims)
	return grants, err == nil
}

// Decide is Authorize under the name latere.ai/x/pkg/authz/server calls a
// decider by, so the policy examend runs in process is also an endpoint an
// operator can serve.
func (p OwnerPolicy) Decide(ctx context.Context, req authz.Request) (authz.Decision, error) {
	return p.Authorize(ctx, req)
}

// decide is the policy's own answer, before the grants narrow it.
func (OwnerPolicy) decide(req authz.Request) authz.Decision {
	kind := authorizer.Kind(req.Action)
	switch {
	case strings.EqualFold(req.Resource.ID, authz.ProbeID):
		return authz.Decision{Reason: authz.ReasonProbe}
	case req.Subject == "":
		return authz.Decision{Reason: authz.ReasonAnonymous}
	case kind == "" || kind != req.Resource.Kind:
		return authz.Decision{Reason: ReasonUnknownAction}
	}
	allow := authz.Decision{Allow: true}
	owner := req.Resource.String(authorizer.FieldOwner)
	switch {
	case authz.IsList(req.Action):
		return authz.Decision{Allow: true, Filter: &authz.Filter{Owners: []string{req.Subject}}}
	case req.Resource.ID != "" && owner != "" && owner == req.Subject:
		// A stored object always has an owner; one with none is a resource
		// the handler did not finish, and is nobody's.
		return allow
	case slices.Contains(readsShared, req.Action) && (flag(req.Resource, authorizer.FieldShared) || flag(req.Resource, authorizer.FieldPinned)):
		return allow
	case (req.Action == authorizer.ActionExperimentRead || req.Action == authorizer.ActionBaselineRead) && flag(req.Resource, authorizer.FieldShared):
		return allow
	case slices.Contains(creates, req.Action) && (owner == "" || owner == req.Subject):
		return allow
	case req.Action == authorizer.ActionExperimentRun && owner == req.Subject && req.Resource.String(authorizer.FieldCreatedBy) == req.Subject:
		return allow
	}
	return authz.Decision{Reason: authz.ReasonNotOwner}
}

// flag reads a field that is true or absent.
func flag(r authz.Resource, field string) bool {
	v, ok := r.Fields[field].(bool)
	return ok && v
}
