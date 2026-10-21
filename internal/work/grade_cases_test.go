// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/topos/topostest"
)

// bench is one experiment whose one trial ended and waits for its
// gradings, with every stub it is graded against.
type bench struct {
	st    store.Store
	stub  *topostest.Stub
	fs    *files
	cl    *sandboxes
	lx    *gateway
	m     *minter
	e     *Engine
	g     graded
	trial objects.Trial
}

func newBench(t *testing.T, st store.Store) *bench {
	t.Helper()
	b := &bench{st: st, stub: topostest.New(t), fs: &files{objects: map[string][]byte{}},
		cl: &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}},
		lx: &gateway{answers: []string{`{"kind":{"value":"game","rationale":"r"},"idea":{"value":"snake","rationale":"r"}}`}}, m: &minter{}}
	b.stub.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	ctx := t.Context()
	b.g = seedGraded(ctx, t, st, b.fs, 1)
	b.e = gradingEngine(t, st, b.stub, b.m, b.fs, b.cl, b.lx)
	if err := b.e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runSession(b.stub, b.fs, b.stub.Sessions()[0])
	if err := b.e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	b.trial = trialsOf(t, st, b.g.eid)[0]
	return b
}

// gradings are the trial's gradings, in their order.
func (b *bench) gradings(t *testing.T) []objects.Grading {
	t.Helper()
	list, err := b.st.Gradings().OfTrials(t.Context(), []string{b.trial.ID})
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// release lets the trial's gradings be taken again at once.
func (b *bench) release(t *testing.T) {
	t.Helper()
	for _, g := range b.gradings(t) {
		if err := b.st.Gradings().Wait(t.Context(), g.ID, -time.Hour, g.Attempts); err != nil {
			t.Fatal(err)
		}
	}
}

func (b *bench) grade(t *testing.T) {
	t.Helper()
	if err := b.e.Grade(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func record[T any](t *testing.T, g objects.Grading) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(g.Record, &out); err != nil {
		t.Fatalf("the record of %s: %s %v", g.Grader, g.Record, err)
	}
	return out
}

// TestCheckRunsThatFailThemselves: a command that cannot start, one its
// bound killed, and a sandbox that would not start, each end with no
// value; a sign-in that no longer acts for the owner and a refused
// sandbox end the checks not run.
func TestCheckRunsThatFailThemselves(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	b := newBench(t, st)
	b.cl.answers[b.g.inventory] = cella.Result{ExitCode: 127, Stderr: "python3: not found"}
	b.cl.answers[b.g.runner] = cella.Result{ExitCode: 124, Duration: int64((10 * time.Minute) / time.Millisecond), Truncated: true}
	b.grade(t)
	list := b.gradings(t)
	inv, run := record[objects.CheckRun](t, list[0]), record[objects.CheckRun](t, list[1])
	if inv.State != objects.CheckNotRunnable || inv.StderrTail != "python3: not found" || run.State != objects.CheckTimedOut || !strings.Contains(run.Reason, "cut by Cella") {
		t.Fatalf("the runs %+v %+v", inv, run)
	}

	b = newBench(t, st)
	b.m.err = &tokens.RefusedError{Reason: "sign_in_not_kept"}
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "no longer acts") {
		t.Fatalf("a sign-in that ended: %+v", run)
	}

	b = newBench(t, st)
	b.cl.refuse = "forbidden"
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[1]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "Cella refused") {
		t.Fatalf("a refused sandbox: %+v", run)
	}
}

// TestEvidenceThatChanged: a log whose digest is not the trial's, and a
// file whose bytes are not the ones the log recorded, run no check and
// send no judgment.
func TestEvidenceThatChanged(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	b := newBench(t, st)
	b.fs.put("files/conversations/"+b.stub.Sessions()[0]+"/work/game.py", []byte("print('changed')\n"))
	b.grade(t)
	list := b.gradings(t)
	if run := record[objects.CheckRun](t, list[0]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "evidence_changed") {
		t.Fatalf("changed files: %+v", run)
	}
	b.grade(t)
	if j := record[objects.Judgment](t, b.gradings(t)[2]); j.State != objects.JudgmentEvidenceChanged || len(b.lx.calls) != 0 {
		t.Fatalf("a judgment over changed files: %+v", j)
	}

	// A log that grew past its pinned event reads as it was recorded.
	b = newBench(t, st)
	b.stub.Set(b.stub.Sessions()[0], "ended", "completed", topostest.Event("session.status", "", time.Date(2026, 10, 19, 12, 9, 0, 0, time.UTC), map[string]any{"status": "ended"}))
	b.cl.answers[b.g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 1}}`}
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckOK {
		t.Fatalf("a log that grew past its pinned event: %+v", run)
	}

	// A log that is gone is evidence that changed.
	b = newBench(t, st)
	b.stub.Delete(b.stub.Sessions()[0])
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "evidence_changed") {
		t.Fatalf("a log that is gone: %+v", run)
	}
}

// TestJudgmentStatesAtLux: a refusal is refused with no second call, a
// refused model is gateway_refused, a key the source refuses is
// gateway_refused, and an outage of Lux or the key source waits.
func TestJudgmentStatesAtLux(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	run := func(t *testing.T, setup func(*bench)) (*bench, objects.Grading) {
		t.Helper()
		b := newBench(t, st)
		b.cl.answers[b.g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 1}}`}
		b.grade(t)
		setup(b)
		b.grade(t)
		return b, b.gradings(t)[2]
	}
	b, g := run(t, func(b *bench) { b.lx.status, b.lx.code = http.StatusForbidden, "model_not_allowed" })
	if j := record[objects.Judgment](t, g); j.State != objects.JudgmentGatewayRefused || len(b.lx.calls) != 1 || j.Attempts[0].LuxRequest != "req_refused" {
		t.Fatalf("a refused model: %+v", j)
	}
	b, g = run(t, func(b *bench) { b.lx.answers = []string{`{}`}; b.lx.status = 0 })
	_ = b
	if j := record[objects.Judgment](t, g); j.State != objects.JudgmentMalformed {
		t.Fatalf("an empty answer: %+v", j)
	}
	_, g = run(t, func(b *bench) { b.e.Keys.(*keys.Memory).Err = &keys.RefusedError{Reason: "plan_suspended"} })
	if j := record[objects.Judgment](t, g); j.State != objects.JudgmentGatewayRefused || !strings.Contains(j.Reason, "plan_suspended") {
		t.Fatalf("a refused key: %+v", j)
	}
	_, g = run(t, func(b *bench) { b.e.Keys.(*keys.Memory).Err = keys.ErrUnavailable })
	if g.State != objects.GradingPending || g.Attempts != 1 {
		t.Fatalf("a key source outage ended the judgment: %+v", g)
	}
	_, g = run(t, func(b *bench) { b.lx.status, b.lx.code = http.StatusBadGateway, "upstream" })
	if g.State != objects.GradingPending || g.Attempts != 1 {
		t.Fatalf("a Lux outage ended the judgment: %+v", g)
	}
	_, g = run(t, func(b *bench) { b.lx.status, b.lx.code = http.StatusTooManyRequests, "spend_exceeded" })
	if j := record[objects.Judgment](t, g); j.State != objects.JudgmentOverBudget {
		t.Fatalf("a spend refusal on 429: %+v", j)
	}
}

// TestALongOutageEnds: a grading put off past its outages ends with no
// value, and a round with no due grading does nothing.
func TestALongOutageEnds(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	b := newBench(t, st)
	b.fs.outage = true
	for i := range gradeOutages {
		b.grade(t)
		if i < gradeOutages-1 {
			b.release(t)
		}
	}
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "outages") {
		t.Fatalf("after %d outages: %+v", gradeOutages, run)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 120*time.Millisecond)
	defer cancel()
	b.e.GradeLoop(ctx, 20*time.Millisecond)
	b.e.Keys.(*keys.Memory).Err = errors.New("down")
	if err := b.e.ReleaseJudgeKeys(t.Context()); err != nil {
		t.Fatalf("no ended experiment's key: %v", err)
	}
}
