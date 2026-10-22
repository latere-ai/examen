// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/topos"
	"latere.ai/x/examen/internal/usage"
)

// Topos runs the trials of `topos` candidates (specs/005-execution.md,
// "The `topos` executor"): one unattended session per trial, created at
// Topos as the experiment's owner on behalf of its created_by, found by
// the trial's id, read to its end, and stopped when its trial ends
// canceled first. Topos has no rollout primitive yet, so each trial's
// session is created on its own, carrying its rollout's key, and the
// experiment's parallelism is dealt by Claim as it is to a lease.
type Topos struct {
	// Client reaches Topos, and Tokens answers the owner tokens every call
	// carries.
	Client *topos.Client
	Tokens tokens.Source
	// Audience is the audience of Topos's owner tokens.
	Audience string
	// OrigoURL is the root an origo workspace's repository is cloned from,
	// <root>/<repository>.git.
	OrigoURL string

	mu       sync.Mutex
	versions map[string]int
}

// deadline is how long a trial's run may last past its first step before
// examend stops it: its timeout, a topos trial's checks and the grace.
func deadline(timeout time.Duration) time.Duration {
	return timeout + limits.CheckTimeout + limits.TrialGrace
}

// RunTopos takes one round of the `topos` executor's work: for each
// experiment with a topos trial to run, follow or stop, the runs that
// ended are read and their trials ended, the runs whose trials ended
// first are stopped, and the trials the parallelism lets run start.
func (e *Engine) RunTopos(ctx context.Context) error {
	if e.Topos == nil {
		return nil
	}
	ids, err := e.Store.Experiments().Driven(ctx, objects.ExecutorTopos, batch)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := e.toposExperiment(ctx, id); err != nil {
			e.log().WarnContext(ctx, "a round of an experiment's sessions failed; the next tick takes it again", "experiment", id, "error", apierr.LogText(err))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// toposRun is what one trial's session is made from.
type toposRun struct {
	exp       objects.Experiment
	cell      objects.Cell
	task      objects.Task
	candidate objects.Candidate
}

func (e *Engine) toposExperiment(ctx context.Context, eid string) error {
	x := e.Store.Experiments()
	exp, err := x.Get(ctx, eid)
	if err != nil {
		return err
	}
	cells, err := x.AllCells(ctx, eid)
	if err != nil {
		return err
	}
	byID := map[string]objects.Cell{}
	var toposCells []objects.Cell
	for _, c := range cells {
		byID[c.ID] = c
		if slices.ContainsFunc(c.Rollouts, func(r objects.Rollout) bool { return r.Executor == objects.ExecutorTopos }) {
			toposCells = append(toposCells, c)
		}
	}
	var errs []error
	// Runs whose trials ended canceled are stopped first, so a cancel's
	// sessions stop within one tick.
	stopping, err := x.Stopping(ctx, eid)
	if err != nil {
		return err
	}
	for _, t := range stopping {
		errs = append(errs, e.stopRun(ctx, exp, t))
	}
	if exp.Finished() {
		return errors.Join(errs...)
	}
	running, err := x.Trials(ctx, eid, store.TrialFilter{Cells: ids(toposCells), State: objects.StateRunning}, store.Page{Limit: limits.MaxParallelism})
	if err != nil {
		return err
	}
	for _, t := range running {
		run, err := e.runOf(ctx, exp, byID[t.Cell])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if t.ExecutorRef == "" {
			errs = append(errs, e.startRun(ctx, run, t))
			continue
		}
		errs = append(errs, e.followRun(ctx, run, t))
	}
	// The trials the parallelism lets run now, of the cells experiment.run
	// allows, start.
	var open []objects.Cell
	for _, c := range toposCells {
		if !objects.Parked(c.State) && c.State != objects.StateComplete {
			open = append(open, c)
		}
	}
	allowed, err := e.allowed(ctx, exp, open, objects.ExecutorTopos)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	claimed, err := x.Claim(ctx, eid, allowed)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	for _, t := range claimed {
		run, err := e.runOf(ctx, exp, byID[t.Cell])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, e.startRun(ctx, run, t))
	}
	return errors.Join(errs...)
}

// ids are the cells' ids.
func ids(cells []objects.Cell) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = c.ID
	}
	return out
}

// runOf reads a cell's task and candidate, as its experiment's owner
// applied them.
func (e *Engine) runOf(ctx context.Context, exp objects.Experiment, cell objects.Cell) (toposRun, error) {
	run := toposRun{exp: exp, cell: cell}
	vs := e.Store.Versions()
	tv, err := vs.ByDigest(ctx, objects.KindTask, exp.Owner, cell.Task)
	if err != nil {
		return run, fmt.Errorf("work: reading cell %s's task: %w", cell.ID, err)
	}
	if err := json.Unmarshal(tv.Body, &run.task); err != nil {
		return run, fmt.Errorf("work: decoding cell %s's task: %w", cell.ID, err)
	}
	cv, err := vs.ByDigest(ctx, objects.KindCandidate, exp.Owner, cell.Candidate)
	if err != nil {
		return run, fmt.Errorf("work: reading cell %s's candidate: %w", cell.ID, err)
	}
	if err := json.Unmarshal(cv.Body, &run.candidate); err != nil {
		return run, fmt.Errorf("work: decoding cell %s's candidate: %w", cell.ID, err)
	}
	if run.candidate.Topos == nil {
		return run, fmt.Errorf("work: cell %s's candidate is not of kind topos", cell.ID)
	}
	return run, nil
}

// token is an owner token for Topos for a trial's unattended work: the
// experiment's owner on behalf of its created_by.
func (e *Engine) token(ctx context.Context, exp objects.Experiment) (string, error) {
	return e.Topos.Tokens.Token(ctx, tokens.Request{
		Owner: exp.Owner, Subject: exp.CreatedBy, Audience: e.Topos.Audience, Experiment: exp.ID, Purpose: tokens.PurposeTrial,
	})
}

// refusal parks a cell blocked for a refusal of the minter or Topos, and
// leaves an outage to the next tick. It answers whether err was handled.
func (e *Engine) refusal(ctx context.Context, cell string, err error) (bool, error) {
	var mint *tokens.RefusedError
	var core *topos.RefusedError
	switch {
	case errors.As(err, &mint):
		return true, e.Store.Experiments().Park(ctx, cell, objects.StateBlocked, mint.Reason)
	case errors.As(err, &core):
		return true, e.Store.Experiments().Park(ctx, cell, objects.StateBlocked, "topos_refused: "+cmpOr(core.Code, strconv.Itoa(core.Status)))
	case errors.Is(err, tokens.ErrUnavailable), errors.Is(err, topos.ErrUnavailable):
		e.log().WarnContext(ctx, "the minter or Topos did not answer; the run waits", "cell", cell, "error", apierr.LogText(err))
		return true, nil
	}
	return false, err
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// startRun creates a claimed trial's session under the trial's id, so a
// repeat after an outage or a restart answers the session the first
// created, and records it on the trial. A session created for a trial
// that ended meanwhile is stopped. The retry of a run that found no machine
// free starts once its wait passed, holding its place in the parallelism
// until then.
func (e *Engine) startRun(ctx context.Context, run toposRun, t objects.Trial) error {
	if wait, err := e.machineWait(ctx, t); err != nil || wait {
		return err
	}
	token, err := e.token(ctx, run.exp)
	if err != nil {
		_, err = e.refusal(ctx, run.cell.ID, err)
		return err
	}
	body, err := e.sessionOf(ctx, token, run, t)
	if err != nil {
		_, err = e.refusal(ctx, run.cell.ID, err)
		return err
	}
	s, err := e.Topos.Client.Create(ctx, token, t.ID, body)
	if inFlight(err) {
		// Another replica's create under the trial's key is still being
		// answered; the next tick reads the session it made.
		e.log().InfoContext(ctx, "a session's create under the trial's key is still being answered; the run waits", "trial", t.ID)
		return nil
	}
	if err != nil {
		_, err = e.refusal(ctx, run.cell.ID, err)
		return err
	}
	stop, err := e.Store.Experiments().Started(ctx, t.ID, s.ID, s.CreatedAt)
	if err != nil {
		return err
	}
	if stop {
		e.log().InfoContext(ctx, "a trial ended while its session was created; the session is stopped", "trial", t.ID, "session", s.ID)
	}
	return nil
}

// machineWait reports whether a trial waits before its run starts: the
// retry of a trial whose run found no machine free starts
// execute.MachineWait after that trial ended, the wait doubling with each
// such retry before it in its lineage (specs/005-execution.md, "Retries
// and lineage"), so a machine the owner's other sandboxes hold has time to
// come free.
func (e *Engine) machineWait(ctx context.Context, t objects.Trial) (bool, error) {
	if !t.AutoRetry || t.RetryOf == "" {
		return false, nil
	}
	x := e.Store.Experiments()
	byID := map[string]objects.Trial{t.ID: t}
	prev, err := x.Trial(ctx, t.RetryOf)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return false, nil
	case err != nil:
		return false, err
	case !prev.NoMachine() || prev.EndedAt == nil:
		return false, nil
	}
	// The lineage is read back as far as its waits and retries count.
	for p, n := prev, 0; n <= limits.MaxMachineWaits+limits.MaxInfraRetries; n++ {
		byID[p.ID] = p
		if !p.AutoRetry || p.RetryOf == "" {
			break
		}
		if p, err = x.Trial(ctx, p.RetryOf); errors.Is(err, store.ErrNotFound) {
			break
		} else if err != nil {
			return false, err
		}
	}
	wait := execute.MachineWait(execute.RetriesOf(t, byID).Waits)
	if left := prev.EndedAt.Add(wait).Sub(e.now()); left > 0 {
		e.log().DebugContext(ctx, "a run that found no machine free is retried after a wait", "trial", t.ID, "retry_of", prev.ID, "wait_left", left.String())
		return true, nil
	}
	return false, nil
}

// sessionOf is a trial's session: the candidate's agent version on its
// model and reasoning, the task's instruction as its one message,
// unattended and ending when its turn ends, a run of the trial's rollout
// carrying spec 005's labels, in the task's workspace, bounded by the
// task's timeout.
func (e *Engine) sessionOf(ctx context.Context, token string, run toposRun, t objects.Trial) (topos.Create, error) {
	c := run.candidate.Topos
	name, digest, err := objects.ParseRef("topos.agent", c.Agent)
	if err != nil {
		return topos.Create{}, err
	}
	// A version's number never changes, so it is read once per replica.
	e.Topos.mu.Lock()
	version, ok := e.Topos.versions[c.Agent]
	e.Topos.mu.Unlock()
	if !ok {
		if version, err = e.Topos.Client.Version(ctx, token, name, digest); err != nil {
			return topos.Create{}, err
		}
		e.Topos.mu.Lock()
		if e.Topos.versions == nil {
			e.Topos.versions = map[string]int{}
		}
		e.Topos.versions[c.Agent] = version
		e.Topos.mu.Unlock()
	}
	timeout := run.task.TimeoutOf()
	// A person counts runs from 1, as the console does; the index counts
	// from 0, and the metadata keeps it as it is.
	title := run.candidate.Name + " on " + run.task.Name + ", run " + strconv.Itoa(t.Index+1)
	body := topos.Create{
		Agent:     name + "@" + strconv.Itoa(version),
		Message:   run.task.Instruction,
		Title:     title,
		EndOnIdle: true,
		Rollout:   t.Rollout,
		Model:     &topos.Model{Name: c.Model, Reasoning: c.Reasoning},
		Limits:    &topos.Limits{TurnTimeout: timeout.String(), MaxAge: deadline(timeout).String()},
		Metadata: map[string]string{
			"examen.experiment": run.exp.ID, "examen.cell": run.cell.ID, "examen.rollout": t.Rollout,
			"examen.trial": t.ID, "examen.index": strconv.Itoa(t.Index),
		},
	}
	if w := run.task.Workspace; w != nil && w.Kind == objects.WorkspaceOrigo {
		body.Resources = []topos.Repository{{Type: "repository", URL: strings.TrimRight(e.Topos.OrigoURL, "/") + "/" + w.Repository + ".git", Ref: w.Commit}}
	}
	return body, nil
}

// followRun reads a running trial's session, and once it ended, or
// stopped short of its end, ends the trial with what its log records. A
// session past the trial's deadline is stopped and its trial ended
// infra_failed.
func (e *Engine) followRun(ctx context.Context, run toposRun, t objects.Trial) error {
	token, err := e.token(ctx, run.exp)
	if err != nil {
		_, err = e.refusal(ctx, run.cell.ID, err)
		return err
	}
	x := e.Store.Experiments()
	s, err := e.Topos.Client.Get(ctx, token, t.ExecutorRef)
	var core *topos.RefusedError
	if errors.As(err, &core) && core.Status == http.StatusNotFound {
		_, err := x.End(ctx, t.ID, ended(t, objects.OutcomeInfraFailed, "rollout_lost", "the trial's session is gone at Topos"), false)
		return err
	}
	if err != nil {
		_, err = e.refusal(ctx, run.cell.ID, err)
		return err
	}
	if s.Status == topos.StatusRunning || (s.Status == topos.StatusIdle && s.StopReason == "") {
		if t.StartedAt != nil && e.now().After(t.StartedAt.Add(deadline(run.task.TimeoutOf()))) {
			_, err := x.End(ctx, t.ID, ended(t, objects.OutcomeInfraFailed, "deadline_passed", "the session ran past the trial's deadline"), true)
			return err
		}
		return nil
	}
	events, err := e.Topos.Client.Events(ctx, token, s.ID)
	if err != nil {
		_, err = e.refusal(ctx, run.cell.ID, err)
		return err
	}
	r, err := topos.Read(s, events)
	if err != nil || !r.Done {
		return err
	}
	if r.End != "" {
		// A run that stopped idle is ended, so its machine is let go; the
		// trial ends once Topos took the end.
		if err := e.Topos.Client.End(ctx, token, s.ID, r.End); err != nil && !conflict(err) {
			_, err = e.refusal(ctx, run.cell.ID, err)
			return err
		}
	}
	ev, err := topos.EvidenceOf(events)
	if err != nil {
		return err
	}
	o := outcomeOf(run, s, r, events, ev)
	if o.Gradings, err = e.gradingsOf(ctx, run, o.Outcome); err != nil {
		return err
	}
	_, err = x.End(ctx, t.ID, o, false)
	return err
}

// inFlight is Topos's answer to a create under a key whose first request
// is still being answered, as a second replica's create of one claimed
// trial meets: no refusal of the run, which the first request makes.
func inFlight(err error) bool {
	var core *topos.RefusedError
	return errors.As(err, &core) && core.Status == http.StatusConflict && core.Code == "conflict"
}

// conflict is Topos's answer to an end of a session that ended already.
func conflict(err error) bool {
	var core *topos.RefusedError
	return errors.As(err, &core) && core.Status == http.StatusConflict
}

// ended is the outcome of a trial examend ends itself, with no record of
// the session read.
func ended(t objects.Trial, outcome, code, detail string) store.Outcome {
	o := store.Outcome{Outcome: outcome, Failure: &objects.Failure{Code: code, Detail: detail}, Ref: t.ExecutorRef}
	if t.StartedAt != nil {
		o.StartedAt = *t.StartedAt
	}
	o.AgentEndedAt = o.StartedAt
	return o
}

// outcomeOf is what a trial's session gives its end: the outcome, its
// times, the sent and observed layers and the built-in values read from
// the session's model requests, and its evidence where it is kept, the
// log at Topos and the files in the session's own folder.
func outcomeOf(run toposRun, s topos.Session, r topos.Report, events []topos.Event, ev topos.Evidence) store.Outcome {
	l := usage.Of(r.Calls)
	o := store.Outcome{
		Outcome: r.Outcome, Failure: r.Failure, StartedAt: r.StartedAt, AgentEndedAt: r.AgentEndedAt, Ref: s.ID,
		Observed: l.Observe(objects.Observed{CostState: objects.CostUnknown}), Settled: true,
		Builtin: l.Builtin(),
	}
	if len(r.Calls) > 0 {
		o.Sent = &l.Sent
	}
	o.Builtin[objects.BuiltinDigest(objects.MetricTurns)] = float64(r.Turns)
	o.Builtin[objects.BuiltinDigest(objects.MetricToolCalls)] = float64(r.ToolCalls)
	// The transcript is pinned at the last event read, which the log's
	// digest covers, so a grader reads the same bytes whatever the session
	// appended after.
	last := s.LastSeq
	if len(events) > 0 {
		last = events[len(events)-1].Seq
	}
	size := int64(len(r.Log))
	o.Evidence = []objects.EvidenceRef{{
		Kind: "transcript", Owner: run.exp.Owner, Path: "topos/sessions/" + s.ID + "/events@" + strconv.FormatUint(last, 10),
		Digest: r.LogDigest, Size: &size,
	}}
	// The session's one output is the kept copy of its working directory,
	// pinned by the digest of the files its log records.
	if f := s.Folder; f != nil {
		var bytes int64
		for _, w := range ev.Work {
			bytes += w.Size
		}
		o.Evidence = append(o.Evidence, objects.EvidenceRef{Kind: "output", Name: "workspace/", Owner: f.Owner, Path: strings.TrimRight(f.Path, "/") + "/work/",
			Digest: topos.WorkDigest(ev.Work), Size: &bytes})
	}
	return o
}

// stopRun stops the session of a trial that ended while it ran: an
// interrupt while its turn runs, then an end once it is idle, over as
// many ticks as that takes; a session already ended or gone is stopped.
func (e *Engine) stopRun(ctx context.Context, exp objects.Experiment, t objects.Trial) error {
	token, err := e.token(ctx, exp)
	if err != nil {
		var mint *tokens.RefusedError
		if errors.As(err, &mint) {
			// A sign-in that ended no longer acts for the owner, and Topos
			// ends the session at its own deadline.
			e.log().WarnContext(ctx, "a session cannot be stopped without the owner's sign-in; it ends at its own deadline", "trial", t.ID, "session", t.ExecutorRef, "reason", mint.Reason)
			return e.Store.Experiments().Stopped(ctx, t.ID)
		}
		return err
	}
	s, err := e.Topos.Client.Get(ctx, token, t.ExecutorRef)
	var core *topos.RefusedError
	switch {
	case errors.As(err, &core) && core.Status == http.StatusNotFound:
		return e.Store.Experiments().Stopped(ctx, t.ID)
	case err != nil:
		return err
	}
	switch s.Status {
	case topos.StatusEnded:
		return e.Store.Experiments().Stopped(ctx, t.ID)
	case topos.StatusRunning:
		return e.Topos.Client.Interrupt(ctx, token, s.ID)
	}
	if err := e.Topos.Client.End(ctx, token, s.ID, topos.StopCanceled); err != nil && !conflict(err) {
		return err
	}
	e.log().InfoContext(ctx, "a canceled trial's session was stopped", "trial", t.ID, "session", s.ID)
	return e.Store.Experiments().Stopped(ctx, t.ID)
}

// allowed asks experiment.run for each cell of an executor before its
// trials are handed out or started; see Allowed.
func (e *Engine) allowed(ctx context.Context, exp objects.Experiment, cells []objects.Cell, executor string) ([]string, error) {
	var out []string
	for _, c := range cells {
		allow, err := e.run(ctx, exp.ID, exp.Owner, exp.CreatedBy, exp.State, exp.Budget.Name, c.ID, executor)
		switch {
		case errors.Is(err, access.ErrUnavailable):
			e.log().WarnContext(ctx, "the authorizer gave no decision on a cell's trials; they wait", "experiment", exp.ID, "cell", c.ID, "error", apierr.LogText(err))
		case err != nil:
			return nil, err
		case !allow:
			if err := e.Store.Experiments().Park(ctx, c.ID, objects.StateCanceled, ReasonRunDenied); err != nil {
				return nil, err
			}
		default:
			out = append(out, c.ID)
		}
	}
	return out, nil
}
