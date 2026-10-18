// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package access_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/authz/conformance"
	"latere.ai/x/pkg/authz/server"
	"latere.ai/x/pkg/authz/stub"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/config"
	"latere.ai/x/examen/internal/limits"
)

const (
	alice = "https://issuer.example.com|alice"
	bob   = "https://issuer.example.com|bob"
)

func caller(subject string) access.Caller {
	iss, sub, _ := authz.SplitSubject(subject)
	return access.Caller{Subject: subject, Issuer: iss, Sub: sub, Claims: map[string]any{"sub": sub}}
}

// authorizerStub starts the stub authorizer with Examen's vocabulary.
func authorizerStub(t testing.TB, opts ...stub.Option) *stub.Server {
	t.Helper()
	return stub.New(t, append([]stub.Option{stub.WithVocabulary(authorizer.Vocabulary())}, opts...)...)
}

// asking is the authorizer examend asks the stub through, on the clock.
func asking(t testing.TB, s *stub.Server, now func() time.Time) access.Authorizer {
	t.Helper()
	c, err := access.NewClient(access.ClientOptions{URL: s.URL(), Token: config.NewSecret(s.Token()), HTTP: client(0), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return access.NewAuthorizer(c)
}

// reading is a read of an experiment of an owner.
func reading(id, owner string) access.Question {
	return access.Question{Action: authorizer.ActionExperimentRead, Resource: access.Resource(authorizer.ActionExperimentRead, id, map[string]any{
		authorizer.FieldOwner: owner, authorizer.FieldState: "running",
	})}
}

// The stub authorizer, as the test tiers start it, answers the contract for
// every row of the vocabulary.
func TestAuthorizerConformance(t *testing.T) {
	s := authorizerStub(t)
	conformance.Run(t, s.URL(), s.Token(),
		conformance.WithVocabulary(authorizer.Vocabulary()),
		conformance.WithSubjects(alice, bob),
		conformance.WithHTTPClient(client(0)))
}

// The client holds an allow for its ttl, a deny briefly and an outage
// never, and asks every question with no id each time.
func TestWhatTheClientRemembers(t *testing.T) {
	s := authorizerStub(t, stub.WithAllow())
	s.Allow(stub.Rule{Subject: alice, Action: authorizer.ActionExperimentRead, Resource: "exp_1", TTL: 120})
	s.Deny(stub.Rule{Subject: bob, Action: authorizer.ActionExperimentRead, Resource: "exp_1"}, "not_owner")
	s.Allow(stub.Rule{Subject: alice, Action: authorizer.ActionMetricApply})
	clock := time.Now()
	az := asking(t, s, func() time.Time { return clock })
	ask := func(subject string, q access.Question) access.Decision {
		t.Helper()
		d, err := az.Authorize(t.Context(), caller(subject), q)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	asked := func() int { return len(s.Requests()) }
	for range 3 {
		if !ask(alice, reading("exp_1", alice)).Allow {
			t.Fatal("denied")
		}
	}
	if asked() != 1 {
		t.Fatalf("an allow was asked %d times", asked())
	}
	clock = clock.Add(121 * time.Second)
	ask(alice, reading("exp_1", alice))
	if asked() != 2 {
		t.Fatal("an allow past its ttl was not asked again")
	}
	for range 2 {
		if d := ask(bob, reading("exp_1", alice)); d.Allow || d.Reason != "not_owner" {
			t.Fatalf("bob: %+v", d)
		}
	}
	if asked() != 3 {
		t.Fatalf("a deny was asked %d times in its ttl", asked()-2)
	}
	apply := access.Question{Action: authorizer.ActionMetricApply, Resource: access.Resource(authorizer.ActionMetricApply, "", map[string]any{authorizer.FieldName: "accuracy"})}
	for range 2 {
		if !ask(alice, apply).Allow {
			t.Fatal("an apply was denied")
		}
	}
	if asked() != 5 {
		t.Fatalf("a question with no id was not asked each time: %d", asked())
	}
	// The request carries the caller's subject, its halves and its claims.
	last := s.Requests()[len(s.Requests())-1]
	if last.Subject != alice || last.Issuer != "https://issuer.example.com" || last.Sub != "alice" || last.Claims["sub"] != "alice" ||
		last.Resource.String(authorizer.FieldName) != "accuracy" || last.Resource.ID != "" {
		t.Fatalf("the request %+v", last)
	}
}

// Anything that is not a well-formed answer is no decision, and nothing is
// cached of it.
func TestNoDecisionFailsClosed(t *testing.T) {
	s := authorizerStub(t)
	az := asking(t, s, nil)
	for name, fail := range map[string]func(){
		"another status":    func() { s.Fail(http.StatusInternalServerError) },
		"a body that is no": func() { s.FailBody(stub.BodyMalformed) },
		"no allow":          func() { s.FailBody(stub.BodyNoAllow) },
	} {
		t.Run(name, func(t *testing.T) {
			fail()
			defer s.Resume()
			_, err := az.Authorize(t.Context(), caller(alice), reading("exp_2", alice))
			if !errors.Is(err, access.ErrUnavailable) || strings.Contains(err.Error(), s.Token()) {
				t.Fatalf("got %v", err)
			}
		})
	}
	s.Hang()
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	if _, err := az.Authorize(ctx, caller(alice), reading("exp_3", alice)); !errors.Is(err, access.ErrUnavailable) {
		t.Fatalf("no answer: %v", err)
	}
	s.Resume()
	if d, err := az.Authorize(t.Context(), caller(alice), reading("exp_3", alice)); err != nil || !d.Allow {
		t.Fatalf("after the outage: %+v %v", d, err)
	}
}

// A question outside the vocabulary, or about another kind, is refused
// before it is sent.
func TestAQuestionOutsideTheVocabulary(t *testing.T) {
	s := authorizerStub(t)
	az := asking(t, s, nil)
	for _, q := range []access.Question{
		{Action: "experiment.delete", Resource: authz.NewResource(authorizer.KindExperiment, "exp_1", nil)},
		{Action: authorizer.ActionExperimentRead, Resource: authz.NewResource(authorizer.KindSuite, "exp_1", nil)},
	} {
		if _, err := az.Authorize(t.Context(), caller(alice), q); err == nil || errors.Is(err, access.ErrUnavailable) {
			t.Errorf("%s on %s: %v", q.Action, q.Resource.Kind, err)
		}
	}
	if len(s.Requests()) != 0 {
		t.Fatalf("%d questions reached the authorizer", len(s.Requests()))
	}
}

// The limits of an allow are read for the actions that carry them, held
// to the limits of spec 002, and left unread on every other action; limits
// Examen cannot read are no decision.
func TestTheLimitsOfAnAllow(t *testing.T) {
	s := authorizerStub(t)
	full := map[string]any{
		"owner": "https://issuer.example.com|org", "max_parallelism": limits.MaxParallelism + 100, "max_trials": 50,
		"max_active_experiments": limits.MaxActiveExperimentsPerOwner + 1, "executors": []string{"external"},
		"judge_models": []string{"claude-*"}, "credential_audiences": []string{"lectio"}, "budget": "b-1",
	}
	s.Allow(stub.Rule{Action: authorizer.ActionExperimentCreate, Limits: full})
	s.Allow(stub.Rule{Action: authorizer.ActionExperimentUpdate, Limits: full})
	s.Allow(stub.Rule{Action: authorizer.ActionSuiteApply, Limits: full})
	s.Allow(stub.Rule{Action: authorizer.ActionExperimentRead, Limits: map[string]any{"no_such_member": 1}})
	az := asking(t, s, nil)
	ask := func(action, id string) access.Decision {
		t.Helper()
		d, err := az.Authorize(t.Context(), caller(alice), access.Question{Action: action, Resource: access.Resource(action, id, map[string]any{authorizer.FieldOwner: alice})})
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		return d
	}
	create := ask(authorizer.ActionExperimentCreate, "exp_4").Limits
	if create.Owner != "https://issuer.example.com|org" || create.MaxParallelism != limits.MaxParallelism || create.MaxTrials != 50 ||
		create.MaxActiveExperiments != limits.MaxActiveExperimentsPerOwner || !slices.Equal(create.Executors, []string{"external"}) ||
		!slices.Equal(create.JudgeModels, []string{"claude-*"}) || !slices.Equal(create.CredentialAudiences, []string{"lectio"}) || create.Budget != "b-1" {
		t.Fatalf("create %+v", create)
	}
	update := ask(authorizer.ActionExperimentUpdate, "exp_4").Limits
	if update.Owner != "" || update.MaxParallelism != limits.MaxParallelism || update.MaxTrials != 50 || update.Budget != "" || update.Executors != nil {
		t.Fatalf("update %+v", update)
	}
	// A list's allow names the owner context a name is resolved in.
	s.Allow(stub.Rule{Action: authorizer.ActionSuiteList, Limits: map[string]any{"owner": "https://issuer.example.com|org"}})
	if list := ask(authorizer.ActionSuiteList, "").Limits; list.Owner != "https://issuer.example.com|org" || list.MaxTrials != 0 {
		t.Fatalf("list %+v", list)
	}
	apply := ask(authorizer.ActionSuiteApply, "").Limits
	if apply.Owner != "https://issuer.example.com|org" || apply.MaxTrials != 0 || apply.Budget != "" {
		t.Fatalf("apply %+v", apply)
	}
	// A read carries no limits Examen holds, so an unknown key there is
	// not read at all.
	if d := ask(authorizer.ActionExperimentRead, "exp_4"); !d.Allow || d.Limits.Owner != "" {
		t.Fatalf("read %+v", d)
	}
	for _, bad := range []map[string]any{
		{"max_trials": -1}, {"executors": []string{"docker"}}, {"judge_models": []string{""}}, {"no_such_member": true},
	} {
		s.SetRules(stub.Rule{Action: authorizer.ActionExperimentCreate, Allow: true, Limits: bad})
		_, err := az.Authorize(t.Context(), caller(alice), access.Question{Action: authorizer.ActionExperimentCreate, Resource: access.Resource(authorizer.ActionExperimentCreate, "exp_5", nil)})
		if !errors.Is(err, access.ErrUnavailable) || !errors.Is(err, authorizer.ErrUnreadableLimits) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

// A filter narrows a list to owners, and to labels on the lists of objects
// that carry metadata; labels on any other list are no decision.
func TestFilters(t *testing.T) {
	s := authorizerStub(t)
	labels := &authz.Filter{Owners: []string{alice, bob}, Labels: map[string]string{"team": "core"}}
	s.Allow(stub.Rule{Action: authorizer.ActionExperimentList, Filter: labels})
	s.Allow(stub.Rule{Action: authorizer.ActionTaskList, Filter: labels})
	s.Allow(stub.Rule{Action: authorizer.ActionCandidateList, Filter: labels})
	s.Allow(stub.Rule{Action: authorizer.ActionSuiteList, Filter: labels})
	s.Allow(stub.Rule{Action: authorizer.ActionMetricList, Filter: &authz.Filter{Owners: []string{bob}}})
	az := asking(t, s, nil)
	for _, action := range []string{authorizer.ActionExperimentList, authorizer.ActionTaskList, authorizer.ActionCandidateList} {
		d, err := az.Authorize(t.Context(), caller(alice), access.Question{Action: action, Resource: access.List(action, "")})
		if err != nil || d.Filter == nil || d.Filter.Labels["team"] != "core" {
			t.Fatalf("%s: %+v %v", action, d, err)
		}
		if got := d.Owners(caller(alice), ""); !slices.Equal(got, []string{alice, bob}) {
			t.Errorf("%s owners %v", action, got)
		}
		if got := d.Owners(caller(alice), bob); !slices.Equal(got, []string{bob}) {
			t.Errorf("%s narrowed to bob: %v", action, got)
		}
		if got := d.Owners(caller(alice), "https://issuer.example.com|carol"); got != nil {
			t.Errorf("%s widened: %v", action, got)
		}
	}
	if _, err := az.Authorize(t.Context(), caller(alice), access.Question{Action: authorizer.ActionSuiteList, Resource: access.List(authorizer.ActionSuiteList, "")}); !errors.Is(err, access.ErrUnavailable) {
		t.Fatalf("labels on the list of suites: %v", err)
	}
	d, err := az.Authorize(t.Context(), caller(alice), access.Question{Action: authorizer.ActionMetricList, Resource: access.List(authorizer.ActionMetricList, alice)})
	if err != nil || !slices.Equal(d.Owners(caller(alice), ""), []string{bob}) {
		t.Fatalf("metrics: %+v %v", d, err)
	}
	if got := (access.Decision{Allow: true}).Owners(caller(alice), ""); !slices.Equal(got, []string{alice}) {
		t.Fatalf("an allow with no filter lists %v", got)
	}
}

// A resource carries the fields its action sends that are known, the id
// only for an action that sends one, and never a field another action
// sends.
func TestResource(t *testing.T) {
	r := access.Resource(authorizer.ActionExperimentRead, "exp_1", map[string]any{
		authorizer.FieldOwner: alice, authorizer.FieldState: "", authorizer.FieldShared: false,
		authorizer.FieldTrials: 5, authorizer.FieldModels: []string{}, authorizer.FieldSuite: nil,
	})
	if r.Kind != authorizer.KindExperiment || r.ID != "exp_1" || r.String(authorizer.FieldOwner) != alice {
		t.Fatalf("%+v", r)
	}
	if _, has := r.Fields[authorizer.FieldState]; has {
		t.Error("an empty member was sent")
	}
	if v, has := r.Fields[authorizer.FieldShared]; !has || v != false {
		t.Error("a known false was not sent")
	}
	if _, has := r.Fields[authorizer.FieldTrials]; has {
		t.Error("a field the action does not send was sent")
	}
	if l := access.Resource(authorizer.ActionMetricApply, "met_1", map[string]any{authorizer.FieldModels: []string{"x"}}); l.ID != "" || len(l.Fields) != 0 {
		t.Fatalf("an apply sends %+v", l)
	}
	if l := access.Resource(authorizer.ActionExperimentCreate, "exp_2", map[string]any{authorizer.FieldModels: []string{"m"}}); l.ID != "exp_2" || len(l.Fields) != 1 {
		t.Fatalf("a create sends %+v", l)
	}
	// A set is sent empty, as an empty list, where another empty list is
	// left out.
	l := access.Resource(authorizer.ActionExperimentCreate, "exp_3", map[string]any{authorizer.FieldGraderKinds: access.Set(nil), authorizer.FieldModels: []string{}})
	if kinds, ok := l.Fields[authorizer.FieldGraderKinds].([]string); !ok || kinds == nil || len(kinds) != 0 || len(l.Fields) != 1 {
		t.Fatalf("an empty set sends %#v", l.Fields)
	}
	if l := access.List(authorizer.ActionExperimentList, ""); len(l.Fields) != 0 || l.ID != "" {
		t.Fatalf("a list naming no owner sends %+v", l)
	}
	if l := access.List(authorizer.ActionBaselineList, bob); l.String(authorizer.FieldOwner) != bob || l.Kind != authorizer.KindBaseline {
		t.Fatalf("a list naming bob sends %+v", l)
	}
}

// The request info the authorizer is told: the request's id, the peer and
// the user agent.
func TestAsk(t *testing.T) {
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/experiments/exp_1", nil)
	r.Header.Set("X-Request-Id", "req_1")
	r.Header.Set("User-Agent", "examen-test")
	r.RemoteAddr = "192.0.2.1:4242"
	q := access.Ask(r, authorizer.ActionExperimentRead, access.Resource(authorizer.ActionExperimentRead, "exp_1", nil))
	if q.Request.ID != "req_1" || q.Request.IP != "192.0.2.1" || q.Request.UserAgent != "examen-test" || q.Action != authorizer.ActionExperimentRead {
		t.Fatalf("%+v", q)
	}
	r.RemoteAddr = "192.0.2.2"
	if access.RequestInfo(r).IP != "192.0.2.2" {
		t.Fatal("a peer with no port")
	}
}

// The owner policy decides each row of its table.
func TestOwnerPolicy(t *testing.T) {
	res := func(action, id string, members map[string]any) authz.Resource {
		return access.Resource(action, id, members)
	}
	cases := []struct {
		name, subject, action string
		resource              authz.Resource
		allow                 bool
		reason                string
		filter                bool
	}{
		{"the probe", alice, authorizer.ActionExperimentRead, authz.NewResource(authorizer.KindExperiment, authz.ProbeID, map[string]any{"owner": alice}), false, authz.ReasonProbe, false},
		{"no subject", "", authorizer.ActionExperimentRead, res(authorizer.ActionExperimentRead, "exp_1", map[string]any{"owner": alice}), false, authz.ReasonAnonymous, false},
		{"an unknown action", alice, "experiment.delete", authz.NewResource(authorizer.KindExperiment, "exp_1", map[string]any{"owner": alice}), false, access.ReasonUnknownAction, false},
		{"another kind", alice, authorizer.ActionExperimentRead, authz.NewResource(authorizer.KindSuite, "exp_1", map[string]any{"owner": alice}), false, access.ReasonUnknownAction, false},
		{"a list", alice, authorizer.ActionSuiteList, access.List(authorizer.ActionSuiteList, ""), true, "", true},
		{"a list naming another owner", alice, authorizer.ActionExperimentList, access.List(authorizer.ActionExperimentList, bob), true, "", true},
		{"its own experiment", alice, authorizer.ActionExperimentCancel, res(authorizer.ActionExperimentCancel, "exp_1", map[string]any{"owner": alice}), true, "", false},
		{"its own trial lease", alice, authorizer.ActionTrialLease, res(authorizer.ActionTrialLease, "exp_1", map[string]any{"owner": alice}), true, "", false},
		{"another's experiment", bob, authorizer.ActionExperimentRead, res(authorizer.ActionExperimentRead, "exp_1", map[string]any{"owner": alice}), false, authz.ReasonNotOwner, false},
		{"a stored object with no owner", alice, authorizer.ActionExperimentRead, res(authorizer.ActionExperimentRead, "exp_1", nil), false, authz.ReasonNotOwner, false},
		{"another's shared experiment", bob, authorizer.ActionExperimentRead, res(authorizer.ActionExperimentRead, "exp_1", map[string]any{"owner": alice, "shared": true}), true, "", false},
		{"another's experiment shared with nobody", bob, authorizer.ActionExperimentRead, res(authorizer.ActionExperimentRead, "exp_1", map[string]any{"owner": alice, "shared": false}), false, authz.ReasonNotOwner, false},
		{"another's pinned task", bob, authorizer.ActionTaskRead, res(authorizer.ActionTaskRead, "tsk_1", map[string]any{"owner": alice, "pinned": true}), true, "", false},
		{"another's shared suite", bob, authorizer.ActionSuiteRead, res(authorizer.ActionSuiteRead, "sui_1", map[string]any{"owner": alice, "shared": true}), true, "", false},
		{"another's candidate, shared", bob, authorizer.ActionCandidateRead, res(authorizer.ActionCandidateRead, "cnd_1", map[string]any{"owner": alice}), false, authz.ReasonNotOwner, false},
		{"an apply naming no owner", alice, authorizer.ActionSuiteApply, res(authorizer.ActionSuiteApply, "", map[string]any{"name": "release"}), true, "", false},
		{"an apply naming itself", alice, authorizer.ActionMetricApply, res(authorizer.ActionMetricApply, "", map[string]any{"owner": alice}), true, "", false},
		{"an apply naming another", alice, authorizer.ActionMetricApply, res(authorizer.ActionMetricApply, "", map[string]any{"owner": bob}), false, authz.ReasonNotOwner, false},
		{"a create with its minted id", alice, authorizer.ActionExperimentCreate, res(authorizer.ActionExperimentCreate, "exp_9", nil), true, "", false},
		{"a create naming another", alice, authorizer.ActionExperimentCreate, res(authorizer.ActionExperimentCreate, "exp_9", map[string]any{"owner": bob}), false, authz.ReasonNotOwner, false},
		{"a pin naming no owner", alice, authorizer.ActionBaselinePin, res(authorizer.ActionBaselinePin, "", map[string]any{"name": "main"}), true, "", false},
		{"a taxonomy of its own suite", alice, authorizer.ActionTaxonomyApply, res(authorizer.ActionTaxonomyApply, "", map[string]any{"owner": alice}), true, "", false},
		{"another's shared baseline", bob, authorizer.ActionBaselineRead, res(authorizer.ActionBaselineRead, "bas_1", map[string]any{"owner": alice, "shared": true}), true, "", false},
		{"another's baseline", bob, authorizer.ActionBaselineRead, res(authorizer.ActionBaselineRead, "bas_1", map[string]any{"owner": alice}), false, authz.ReasonNotOwner, false},
		{"its own cell retry", alice, authorizer.ActionCellRetry, res(authorizer.ActionCellRetry, "exp_1", map[string]any{"owner": alice, "cell": "cel_1"}), true, "", false},
		{"a run of its own experiment", alice, authorizer.ActionExperimentRun, res(authorizer.ActionExperimentRun, "exp_1", map[string]any{"owner": alice, "created_by": alice}), true, "", false},
		{"a run of an experiment another created", bob, authorizer.ActionExperimentRun, res(authorizer.ActionExperimentRun, "exp_1", map[string]any{"owner": alice, "created_by": bob}), false, authz.ReasonNotOwner, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, err := access.OwnerPolicy{}.Authorize(t.Context(), authz.Request{Subject: tc.subject, Action: tc.action, Resource: tc.resource})
			if err != nil {
				t.Fatal(err)
			}
			if d.Allow != tc.allow || (!tc.allow && d.Reason != tc.reason) || (d.Filter != nil) != tc.filter {
				t.Fatalf("got %+v", d)
			}
			if tc.filter && !slices.Equal(d.Filter.Owners, []string{tc.subject}) {
				t.Fatalf("the filter is %+v", d.Filter)
			}
		})
	}
}

// The owner policy answers the contract's conformance suite through the
// shared scaffold.
func TestOwnerPolicyConformance(t *testing.T) {
	const token = "conformance-bearer"
	srv := httptest.NewServer(server.New(server.Options{Bearer: token, Vocabulary: authorizer.Vocabulary(), Decider: access.OwnerPolicy{}}))
	defer srv.Close()
	conformance.Run(t, srv.URL, token, conformance.WithVocabulary(authorizer.Vocabulary()), conformance.WithSubjects(alice, bob))
}

// The policy's own answer is narrowed by the grants of the caller's token,
// and a token whose grants cannot be read is denied.
func TestOwnerPolicyNarrowsByTheGrants(t *testing.T) {
	grants := func(actions ...string) map[string]any {
		return map[string]any{"token_use": authz.TokenUsePAT, "authorization_details": []any{map[string]any{
			"type": authz.GrantType, "actions": actions,
		}}}
	}
	own := access.Resource(authorizer.ActionExperimentRead, "exp_1", map[string]any{authorizer.FieldOwner: alice})
	harness := grants(authorizer.Core + ":" + authorizer.ActionTrialLease)
	read := authz.Request{Subject: alice, Action: authorizer.ActionExperimentRead, Resource: own, Claims: harness}
	if d, err := (access.OwnerPolicy{}).Authorize(t.Context(), read); err != nil || d.Allow {
		t.Fatalf("a read past the token's grants: %+v %v", d, err)
	}
	lease := authz.Request{Subject: alice, Action: authorizer.ActionTrialLease, Resource: access.Resource(authorizer.ActionTrialLease, "exp_1", map[string]any{authorizer.FieldOwner: alice}), Claims: harness}
	if d, err := (access.OwnerPolicy{}).Decide(t.Context(), lease); err != nil || !d.Allow {
		t.Fatalf("a lease within the token's grants: %+v %v", d, err)
	}
	unreadable := authz.Request{Subject: alice, Action: authorizer.ActionExperimentList, Resource: access.List(authorizer.ActionExperimentList, ""),
		Claims: map[string]any{"token_use": authz.TokenUsePAT, "authorization_details": "not grants"}}
	if d, err := (access.OwnerPolicy{}).Authorize(t.Context(), unreadable); err != nil || d.Allow || d.Reason != authz.ReasonGrant {
		t.Fatalf("unreadable grants: %+v %v", d, err)
	}
}

// New answers the owner policy without an endpoint and the client with
// one; the probe tells an endpoint that reads the request from one that
// does not.
func TestNewAndTheProbe(t *testing.T) {
	base := config.Settings{OIDCIssuers: []string{"https://issuer.example.com"}, OIDCAudiences: []string{audience}}
	a, err := access.New(base, access.Options{})
	if err != nil || a.Mode != access.ModeOwnerPolicy {
		t.Fatalf("%+v %v", a, err)
	}
	if err := a.Check(t.Context()); err != nil {
		t.Fatalf("the owner policy fails its own probe: %v", err)
	}
	d, err := a.Authorizer.Authorize(t.Context(), caller(alice), reading("exp_1", alice))
	if err != nil || !d.Allow {
		t.Fatalf("the owner policy behind the seam: %+v %v", d, err)
	}

	s := authorizerStub(t)
	withEndpoint := base
	withEndpoint.AuthorizerURL, withEndpoint.AuthorizerToken = s.URL(), config.NewSecret(s.Token())
	var observed []string
	a, err = access.New(withEndpoint, access.Options{Observe: func(result string, _ float64) { observed = append(observed, result) }})
	if err != nil || a.Mode != access.ModeAuthorizer {
		t.Fatalf("%+v %v", a, err)
	}
	if err := a.Check(t.Context()); err != nil {
		t.Fatalf("the stub fails the probe: %v", err)
	}
	if d, err := a.Authorizer.Authorize(t.Context(), caller(alice), reading("exp_1", alice)); err != nil || !d.Allow || len(observed) == 0 {
		t.Fatalf("through the endpoint: %+v %v %v", d, err, observed)
	}
	// An endpoint that allows everything allows the probe, and is named.
	yes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{"allow": true}); err != nil {
			t.Error(err)
		}
	}))
	defer yes.Close()
	withEndpoint.AuthorizerURL = yes.URL
	if a, err = access.New(withEndpoint, access.Options{}); err != nil {
		t.Fatal(err)
	}
	if err := a.Check(t.Context()); !errors.Is(err, authz.ErrProbeAllowed) {
		t.Fatalf("an endpoint that allows the probe: %v", err)
	}
	// Warm names an issuer that does not answer.
	if err := a.Warm(t.Context()); err == nil {
		t.Fatal("an issuer that does not answer was warmed")
	}
	if _, err := access.NewClient(access.ClientOptions{URL: yes.URL}); err == nil {
		t.Fatal("a client with nothing to send its calls through")
	}
}

// No file of the package outside its tests reads a claim of the token for
// meaning: the claims reach the authorizer as they came.
func TestNoClaimIsRead(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "Claims[") {
			t.Errorf("%s reads a claim", f)
		}
	}
}

// decider answers every request with one decision and keeps the request.
type decider struct {
	d    authz.Decision
	seen []authz.Request
}

func (f *decider) Authorize(_ context.Context, req authz.Request) (authz.Decision, error) {
	f.seen = append(f.seen, req)
	return f.d, nil
}

// A deny that names no reason still says something, and a caller with no
// claims sends an empty object, never null.
func TestADenyAlwaysHasAReason(t *testing.T) {
	f := &decider{d: authz.Decision{}}
	d, err := access.NewAuthorizer(f).Authorize(t.Context(), access.Caller{Subject: alice}, reading("exp_1", alice))
	if err != nil || d.Allow || d.Reason != "no_reason_given" {
		t.Fatalf("%+v %v", d, err)
	}
	if f.seen[0].Claims == nil {
		t.Fatal("the claims went as null")
	}
}

// A share lets its grantee read an experiment and run nothing of it:
// every action that starts, feeds or changes its execution is denied to
// the grantee even when the question says the experiment is shared with
// it, and allowed to the owner.
func TestASharedExperimentDoesNotRun(t *testing.T) {
	shared := authz.NewResource(authorizer.KindExperiment, "exp_1", map[string]any{
		authorizer.FieldOwner: alice, authorizer.FieldShared: true, authorizer.FieldCreatedBy: alice,
	})
	ask := func(subject, action string) authz.Decision {
		t.Helper()
		d, err := access.OwnerPolicy{}.Authorize(t.Context(), authz.Request{Subject: subject, Action: action, Resource: shared})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := ask(bob, authorizer.ActionExperimentRead); !d.Allow {
		t.Fatalf("the grantee's read: %+v", d)
	}
	runs := []string{
		authorizer.ActionExperimentUpdate, authorizer.ActionCellRetry, authorizer.ActionExperimentCancel,
		authorizer.ActionExperimentImport, authorizer.ActionTrialLease, authorizer.ActionTrialSubmit, authorizer.ActionExperimentRun,
	}
	for _, action := range runs {
		if d := ask(bob, action); d.Allow || d.Reason != authz.ReasonNotOwner {
			t.Errorf("the grantee's %s: %+v, want not_owner", action, d)
		}
		if d := ask(alice, action); !d.Allow {
			t.Errorf("the owner's %s: %+v", action, d)
		}
	}
}
