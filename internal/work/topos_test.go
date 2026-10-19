// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/topos"
	"latere.ai/x/examen/internal/topos/topostest"
)

const agentDigest = "sha256:" + "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

// minter answers a token per audience, or the error the test sets, and
// keeps every ask.
type minter struct {
	mu   sync.Mutex
	err  error
	asks []tokens.Request
}

func (m *minter) Token(_ context.Context, r tokens.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asks = append(m.asks, r)
	if m.err != nil {
		return "", m.err
	}
	return "tok-" + r.Audience, nil
}

// toposGate answers experiment.run as gate does, for the topos executor.
type toposGate struct{ executors []string }

func (g *toposGate) Authorize(_ context.Context, c access.Caller, q access.Question) (access.Decision, error) {
	if q.Action != authorizer.ActionExperimentRun || c.Subject != owner {
		return access.Decision{}, nil
	}
	g.executors = append(g.executors, q.Resource.String(authorizer.FieldExecutor))
	return access.Decision{Allow: true}, nil
}

// seedTopos stores one experiment of one topos candidate on one task in an
// origo workspace, n trials a cell, its smoke rollout queued, and answers
// its id and its cell's id.
func seedTopos(t *testing.T, st store.Store, n int) (string, string) {
	t.Helper()
	ctx := t.Context()
	put := func(kind, name string, v any) objects.Version {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := id.DigestJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		out, _, err := st.Versions().Apply(ctx, objects.Version{ID: id.New(objects.Prefix(kind)), Kind: kind, Owner: owner, Name: name, Digest: digest, Body: raw}, store.Precondition{})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	commit := strings.Repeat("c", 40)
	task := put(objects.KindTask, "seeded", objects.Task{Name: "seeded", Instruction: "Do something you want.", Timeout: "20m",
		Workspace: &objects.Workspace{Kind: objects.WorkspaceOrigo, Repository: "acme/seed", Commit: commit}})
	suite := put(objects.KindSuite, "s", objects.Suite{Name: "s", Tasks: []string{task.Digest}, Metrics: []string{"builtin:completed"}, PrimaryMetric: "builtin:completed"})
	cand := put(objects.KindCandidate, "helper-low", objects.Candidate{Name: "helper-low", Kind: objects.CandidateTopos,
		Topos: &objects.Topos{Agent: "helper@" + agentDigest, Model: "anthropic/claude-haiku-5.5", Reasoning: "low"}})
	e := objects.Experiment{ID: id.New(id.Experiment), Owner: owner, Suite: suite.Digest, SuiteID: suite.ID, SuiteName: "s",
		Candidates: []string{cand.Digest}, CandidateIDs: []string{cand.ID}, Trials: n, Budget: objects.Budget{Name: "b"},
		Parallelism: 4, Reference: objects.Reference{Candidate: cand.Digest}, Mode: objects.ModeExecute, CreatedBy: owner}
	cell := objects.Cell{ID: id.New(id.Cell), Task: task.Digest, TaskID: task.ID, Candidate: cand.Digest, CandidateID: cand.ID}
	smoke := execute.Smoke(cell.ID, n, objects.ExecutorTopos)
	ne := store.NewExperiment{Experiment: e, MaxActive: 16, Cells: []store.NewCell{{Cell: cell, Smoke: &store.Rollout{Rollout: smoke.Rollout, Trials: []store.RolloutTrial{{Index: 0}}}}}}
	if err := st.Experiments().Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	return e.ID, cell.ID
}

// toposEngine is an engine whose topos executor reaches stub with m's
// tokens.
func toposEngine(st store.Store, stub *topostest.Stub, m *minter, g access.Authorizer) *Engine {
	return &Engine{Store: st, Authz: g, Analyzer: figures{mode: "none"}, Topos: &Topos{
		Client: topos.New(stub.URL, stub.Client()), Tokens: m, Audience: "topos", OrigoURL: "https://code.example/",
	}}
}

// trialsOf are an experiment's trials, oldest first.
func trialsOf(t *testing.T, st store.Store, eid string) []objects.Trial {
	t.Helper()
	list, err := st.Experiments().Trials(t.Context(), eid, store.TrialFilter{}, store.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(list, func(a, b objects.Trial) int { return strings.Compare(a.ID, b.ID) })
	return list
}

// finish ends a stub session as a run that worked for a minute: its turn
// started, one model request priced at cost, one tool call, and the
// agent's answer, then the end.
func finish(stub *topostest.Stub, sid, stop string, cost int64) {
	at := time.Date(2026, 10, 19, 12, 1, 0, 0, time.UTC)
	stub.Set(sid, topos.StatusEnded, stop,
		topostest.Event(topos.EventSessionStatus, "", at, map[string]any{"status": "running"}),
		topostest.Event(topos.EventModelRequest, "", at.Add(10*time.Second), map[string]any{
			"model": "anthropic/claude-haiku-5.5", "outcome": "ok", "cost_usd_micro": cost, "cost_source": "provider",
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 40, "cache_read_input_tokens": 20, "reasoning_tokens": 5},
		}),
		topostest.Event(topos.EventAgentToolUse, "", at.Add(20*time.Second), map[string]any{"name": "bash"}),
		topostest.Event(topos.EventAgentMessage, "", at.Add(50*time.Second), map[string]any{"stop_reason": "end_turn"}),
		topostest.Event(topos.EventSessionStatus, "", at.Add(time.Minute), map[string]any{"status": "ended", "stop_reason": stop}),
	)
}

// TestToposRunsEachTrialAsASession: a topos cell's smoke trial runs as an
// unattended session of the candidate's agent version on its model and
// reasoning, created as the owner on behalf of created_by under the
// trial's id, a run of its rollout, in the task's repository at its
// commit; its end gives the trial its outcome, layers, built-in values and
// evidence; the main rollout follows; and a cancel stops the session of
// the trial it ended.
func TestToposRunsEachTrialAsASession(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 2, Digest: "sha256:other"}, topostest.Version{Version: 3, Digest: agentDigest})
	m, g := &minter{}, &toposGate{}
	e := toposEngine(st, stub, m, g)
	e.Now = func() time.Time { return time.Date(2026, 10, 19, 12, 5, 0, 0, time.UTC) }
	ctx := t.Context()
	eid, cid := seedTopos(t, st, 2)

	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sessions := stub.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("one tick created %d sessions, want 1", len(sessions))
	}
	trials := trialsOf(t, st, eid)
	if len(trials) != 1 || trials[0].State != objects.StateRunning || trials[0].ExecutorRef != sessions[0] || trials[0].StartedAt == nil {
		t.Fatalf("the smoke trial %+v", trials)
	}
	body := stub.Body(sessions[0])
	meta, _ := body["metadata"].(map[string]any)
	model, _ := body["model"].(map[string]any)
	resources, _ := body["resources"].([]any)
	switch {
	case body["agent"] != "helper@3" || body["message"] != "Do something you want." || body["attended"] != false || body["end_on_idle"] != true:
		t.Fatalf("the session's create %v", body)
	case body["rollout"] != cid+".smoke" || meta["examen.trial"] != trials[0].ID || meta["examen.experiment"] != eid || meta["examen.cell"] != cid || meta["examen.index"] != "0":
		t.Fatalf("the session's labels: rollout %v, metadata %v", body["rollout"], meta)
	case model["name"] != "anthropic/claude-haiku-5.5" || model["reasoning"] != "low":
		t.Fatalf("the session's model %v", model)
	case len(resources) != 1 || resources[0].(map[string]any)["url"] != "https://code.example/acme/seed.git" || resources[0].(map[string]any)["ref"] != strings.Repeat("c", 40):
		t.Fatalf("the session's repositories %v", resources)
	case body["limits"].(map[string]any)["turn_timeout"] != "20m0s" || body["limits"].(map[string]any)["max_age"] != "40m0s":
		t.Fatalf("the session's limits %v", body["limits"])
	}
	for _, a := range m.asks {
		if a.Owner != owner || a.Subject != owner || a.Audience != "topos" || a.Experiment != eid || a.Purpose != tokens.PurposeTrial {
			t.Fatalf("an owner token was asked as %+v", a)
		}
	}
	for _, tok := range stub.Tokens {
		if tok != "tok-topos" {
			t.Fatalf("Topos was called with %q", tok)
		}
	}
	for _, x := range g.executors {
		if x != objects.ExecutorTopos {
			t.Fatalf("experiment.run was asked for executor %q", x)
		}
	}

	// A tick while the session runs changes nothing.
	stub.Set(sessions[0], topos.StatusRunning, "")
	if err := e.Tick(ctx); err != nil || len(stub.Sessions()) != 1 {
		t.Fatalf("a tick while the session ran: %v, %d sessions", err, len(stub.Sessions()))
	}
	finish(stub, sessions[0], topos.StopCompleted, 1200)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	trials = trialsOf(t, st, eid)
	smoke := trials[0]
	switch {
	case smoke.State != objects.StateDone || smoke.Outcome != objects.OutcomeCompleted:
		t.Fatalf("the smoke trial ended %s %s", smoke.State, smoke.Outcome)
	case smoke.Observed.CostState != objects.CostKnown || smoke.Observed.CostUSD == nil || *smoke.Observed.CostUSD != 0.0012:
		t.Fatalf("the smoke trial's observed layer %+v", smoke.Observed)
	case smoke.Sent == nil || smoke.Sent.Requests != 1 || smoke.Sent.BySetting[0].Model != "anthropic/claude-haiku-5.5" || smoke.Sent.BySetting[0].Reasoning != "low":
		t.Fatalf("the smoke trial's sent layer %+v", smoke.Sent)
	case smoke.StartedAt == nil || !smoke.StartedAt.Equal(time.Date(2026, 10, 19, 12, 1, 0, 0, time.UTC)) || smoke.AgentEndedAt.Sub(*smoke.StartedAt) != time.Minute:
		t.Fatalf("the smoke trial's times %v %v", smoke.StartedAt, smoke.AgentEndedAt)
	case len(smoke.Evidence) != 2 || smoke.Evidence[0].Kind != "transcript" || !strings.HasPrefix(smoke.Evidence[0].Digest, "sha256:") ||
		smoke.Evidence[1].Path != "files/conversations/"+sessions[0]+"/work/":
		t.Fatalf("the smoke trial's evidence %+v", smoke.Evidence)
	}
	values := map[string]any{}
	ms, err := st.Experiments().Measurements(ctx, store.MeasurementFilter{Trial: smoke.ID, Generation: "current"}, store.Page{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		values[strings.TrimPrefix(m.Metric, "builtin:")] = m.Value
	}
	for metric, want := range map[string]any{"completed": true, "cost_usd": 0.0012, "turns": 1.0, "tool_calls": 1.0, "input_tokens": 120.0, "output_tokens": 40.0, "duration": 60.0} {
		if values[metric] != want {
			t.Fatalf("built-in %s is %v, want %v (all %v)", metric, values[metric], want, values)
		}
	}

	// The main rollout runs the cell's other index as a second session.
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sessions = stub.Sessions()
	if len(sessions) != 2 || stub.Body(sessions[1])["rollout"] != cid+".main" {
		t.Fatalf("the main rollout's session: %v", sessions)
	}
	stub.Set(sessions[1], topos.StatusRunning, "")

	// A cancel ends the trial at once and stops its session over the next
	// ticks: an interrupt, then an end.
	if _, err := st.Experiments().Cancel(ctx, eid); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if s := stub.Session(sessions[1]); s.Status != topos.StatusEnded || s.StopReason != topos.StopCanceled || len(stub.Interrupts) != 1 {
		t.Fatalf("the canceled trial's session is %s %s, %d interrupts", s.Status, s.StopReason, len(stub.Interrupts))
	}
	if left, err := st.Experiments().Stopping(ctx, eid); err != nil || len(left) != 0 {
		t.Fatalf("runs left to stop %v %v", left, err)
	}
}

// TestToposRefusalsParkAndOutagesWait: a minter that keeps no sign-in and
// a Topos that refuses the create park the cell blocked with the reason;
// an outage leaves the trial running with no session, and the next tick
// creates it under the same key.
func TestToposRefusalsParkAndOutagesWait(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 1, Digest: agentDigest})
	ctx := t.Context()
	cellOf := func(cid string) objects.Cell {
		c, err := st.Experiments().Cell(ctx, cid)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	m := &minter{err: &tokens.RefusedError{Reason: tokens.ReasonSignInNotKept}}
	e := toposEngine(st, stub, m, &toposGate{})
	_, cid := seedTopos(t, st, 1)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if c := cellOf(cid); c.State != objects.StateBlocked || c.ParkedReason != "sign_in_not_kept" {
		t.Fatalf("a sign-in not kept: cell %s %q", c.State, c.ParkedReason)
	}

	m.err = nil
	_, cid = seedTopos(t, st, 1)
	stub.Refuse(http.StatusForbidden, "forbidden")
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if c := cellOf(cid); c.State != objects.StateBlocked || c.ParkedReason != "topos_refused: forbidden" {
		t.Fatalf("a refused create: cell %s %q", c.State, c.ParkedReason)
	}

	eid, _ := seedTopos(t, st, 1)
	stub.Down(true)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if tr := trialsOf(t, st, eid); tr[0].State != objects.StateRunning || tr[0].ExecutorRef != "" {
		t.Fatalf("after an outage the trial is %+v", tr[0])
	}
	stub.Down(false)
	before := len(stub.Sessions())
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if tr := trialsOf(t, st, eid); len(stub.Sessions()) != before+1 || tr[0].ExecutorRef != stub.Sessions()[before] {
		t.Fatalf("after the outage: %d sessions, trial %+v", len(stub.Sessions()), tr[0])
	}
	// The same trial's create again answers the session it made.
	if err := e.Tick(ctx); err != nil || len(stub.Sessions()) != before+1 {
		t.Fatalf("a repeat made another session: %v", err)
	}
}

// TestToposLostAndLateSessions: a session gone at Topos ends its trial
// infra_failed with rollout_lost and the cell retries it; a session that
// stopped idle is read for its outcome and ended; a session past the
// trial's deadline ends its trial infra_failed and is stopped.
func TestToposLostAndLateSessions(t *testing.T) {
	clock := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 1, Digest: agentDigest})
	e := toposEngine(st, stub, &minter{}, &toposGate{})
	e.Now = func() time.Time { return clock }
	ctx := t.Context()

	eid, _ := seedTopos(t, st, 1)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	first := stub.Sessions()[0]
	stub.Delete(first)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	tr := trialsOf(t, st, eid)
	if tr[0].Outcome != objects.OutcomeInfraFailed || tr[0].Failure == nil || tr[0].Failure.Code != "rollout_lost" {
		t.Fatalf("a lost session's trial %+v", tr[0])
	}
	// Its retry runs as a new session; it stops idle at its turn limit.
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	retry := stub.Sessions()[len(stub.Sessions())-1]
	stub.Set(retry, topos.StatusIdle, topos.StopTurnLimit)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	tr = trialsOf(t, st, eid)
	if last := tr[len(tr)-1]; last.Outcome != objects.OutcomeTimedOut || last.RetryOf != tr[0].ID {
		t.Fatalf("the retry %+v", last)
	}
	if s := stub.Session(retry); s.Status != topos.StatusEnded || s.StopReason != topos.StopCompleted {
		t.Fatalf("the idle session was left %s %s", s.Status, s.StopReason)
	}

	// A run past its deadline is ended and its session stopped.
	late, _ := seedTopos(t, st, 1)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sid := stub.Sessions()[len(stub.Sessions())-1]
	stub.Set(sid, topos.StatusRunning, "")
	clock = clock.Add(2 * time.Hour)
	for range 3 {
		if err := e.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tr = trialsOf(t, st, late)
	if tr[0].Outcome != objects.OutcomeInfraFailed || tr[0].Failure.Code != "deadline_passed" {
		t.Fatalf("a late run %+v", tr[0])
	}
	if s := stub.Session(sid); s.Status != topos.StatusEnded || s.StopReason != topos.StopCanceled {
		t.Fatalf("the late session is %s %s", s.Status, s.StopReason)
	}
}

// TestASessionCreatedForAnEndedTrialIsStopped: a trial canceled while its
// session was being created records the session and stops it.
func TestASessionCreatedForAnEndedTrialIsStopped(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 1, Digest: agentDigest})
	e := toposEngine(st, stub, &minter{}, &toposGate{})
	ctx := t.Context()
	eid, cid := seedTopos(t, st, 1)
	if err := e.Advance(ctx, ""); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.Experiments().Claim(ctx, eid, []string{cid})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed %v %v", claimed, err)
	}
	if _, err := st.Experiments().Cancel(ctx, eid); err != nil {
		t.Fatal(err)
	}
	run, err := e.runOf(ctx, mustExperiment(t, st, eid), mustCell(t, st, cid))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.startRun(ctx, run, claimed[0]); err != nil {
		t.Fatal(err)
	}
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sid := stub.Sessions()[0]
	if s := stub.Session(sid); s.Status != topos.StatusEnded || s.StopReason != topos.StopCanceled {
		t.Fatalf("the late session is %s %s", s.Status, s.StopReason)
	}
	if tr := trialsOf(t, st, eid); tr[0].Outcome != objects.OutcomeCanceled || tr[0].ExecutorRef != sid {
		t.Fatalf("the trial %+v", tr[0])
	}
}

func mustExperiment(t *testing.T, st store.Store, eid string) objects.Experiment {
	t.Helper()
	e, err := st.Experiments().Get(t.Context(), eid)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustCell(t *testing.T, st store.Store, cid string) objects.Cell {
	t.Helper()
	c, err := st.Experiments().Cell(t.Context(), cid)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
