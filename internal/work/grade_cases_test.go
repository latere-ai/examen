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

	"io"
	"net/http/httptest"
	"net/url"

	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/grade"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/topos"
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
	_, g = run(t, func(b *bench) { b.lx.raw = "not json" })
	if g.State != objects.GradingPending {
		t.Fatalf("an answer that does not parse ended the judgment: %+v", g)
	}
	_, g = run(t, func(b *bench) { b.e.Grading.LuxURL = "http://127.0.0.1:1" })
	if g.State != objects.GradingPending {
		t.Fatalf("a Lux not reached ended the judgment: %+v", g)
	}
	_, g = run(t, func(b *bench) { b.lx.unpriced = true })
	if j := record[objects.Judgment](t, g); j.State != objects.JudgmentOK || j.Cost != nil || j.Attempts[0].LuxRequest != "msg_1" {
		t.Fatalf("an answer with no cost: %+v", j)
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

// TestJudgeEvidenceReadsTheRun: a judge's inputs are read from the run: the
// transcript as its log, one file of the working directory by its path, a
// file that is not text and one past the kept bound listed rather than
// read, and a file named that the kept copy does not hold yields nothing.
func TestJudgeEvidenceReadsTheRun(t *testing.T) {
	fs := &files{objects: map[string][]byte{}}
	srv := httptest.NewServer(http.HandlerFunc(fs.serve))
	defer srv.Close()
	m := &minter{}
	e := &Engine{Grading: &Grading{Tokens: m, Arca: arca.New(srv.URL, srv.Client()), ArcaAudience: "arca"}}
	text, binary := []byte("print(1)\n"), []byte{0x89, 'P', 'N', 'G', 0}
	fs.put("files/f/work/a.py", text)
	fs.put("files/f/work/logo.png", binary)
	r := gradedRun{exp: objects.Experiment{ID: "exp_1", Owner: owner, CreatedBy: owner}, folderOwner: owner, folder: "files/f/work",
		ev: topos.Evidence{Log: []byte("{\"seq\":1}\n"), FinalMessage: "done", Work: []topos.WorkFile{
			{Path: "a.py", Size: int64(len(text)), SHA256: sha(text)},
			{Path: "big.bin", Size: limits.OutputFileKeepMaxBytes + 1, SHA256: "ff"},
			{Path: "logo.png", Size: int64(len(binary)), SHA256: sha(binary)},
		}}}
	ev, err := e.judgeEvidence(t.Context(), r, objects.JudgeInputs{Transcript: true, FinalMessage: true, Files: []string{"notes.md"}, Outputs: []string{"workspace/", "workspace/a.py", "other/"}})
	if err != nil {
		t.Fatal(err)
	}
	if *ev.Transcript != "{\"seq\":1}\n" || *ev.FinalMessage != "done" || len(ev.Files) != 1 || ev.Files[0].Text != "" {
		t.Fatalf("the transcript, message and files %+v", ev)
	}
	whole, one, other := ev.Outputs[0], ev.Outputs[1], ev.Outputs[2]
	if len(whole.Files) != 1 || whole.Files[0].Name != "workspace/a.py" || len(whole.Listing) != 2 ||
		whole.Listing[0].Why != grade.WhyNotKept || whole.Listing[1].Why != grade.WhyNotText {
		t.Fatalf("the whole output %+v", whole)
	}
	if len(one.Files) != 1 || len(one.Listing) != 0 || len(other.Files)+len(other.Listing) != 0 {
		t.Fatalf("one file %+v, another output %+v", one, other)
	}
	// A kept file whose folder the trial does not name is evidence that
	// changed, and a sign-in that ended stops the read.
	r.folder = ""
	if _, err := e.readWork(t.Context(), r, tokens.PurposeJudge); !errors.Is(err, errEvidenceChanged) {
		t.Fatalf("no folder: %v", err)
	}
	r.folder = "files/f/work"
	m.err = &tokens.RefusedError{Reason: "sign_in_not_kept"}
	if _, ok := signInEnded(func() error { _, err := e.readWork(t.Context(), r, tokens.PurposeJudge); return err }()); !ok {
		t.Fatal("a refused token did not end the read")
	}
	if _, ok := signInEnded(errors.New("other")); ok {
		t.Fatal("another error read as a sign-in that ended")
	}
	if scrubURL(&url.Error{Op: "Get", URL: "http://x", Err: io.EOF}) != io.EOF || scrubURL(io.EOF) != io.EOF {
		t.Fatal("scrubURL")
	}
	if (&luxError{status: 403, code: "forbidden"}).Error() == "" || !hasCode([]byte(`{"error":{"code":"spend_exceeded"}}`), spendCodes) || hasCode([]byte("x"), spendCodes) {
		t.Fatal("the Lux error helpers")
	}
}

// TestGradingsOfAnOutcome: a trial is given its task's checks and the
// judges of the task and the suite whose metrics apply to its outcome; an
// annotation queue and a judge of no listed metric give none, and an
// engine that grades nothing gives none.
func TestGradingsOfAnOutcome(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	ctx := t.Context()
	fs := &files{objects: map[string][]byte{}}
	g := seedGraded(ctx, t, st, fs, 1)
	e := gradingEngine(t, st, topostest.New(t), &minter{}, fs, &sandboxes{archives: map[string][]byte{}}, &gateway{})
	exp, err := st.Experiments().Get(ctx, g.eid)
	if err != nil {
		t.Fatal(err)
	}
	cell, _ := st.Experiments().Cell(ctx, g.cid)
	run, err := e.runOf(ctx, exp, cell)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := e.gradingsOf(ctx, run, objects.OutcomeCompleted)
	if err != nil || len(completed) != 3 || completed[2].Grader != g.judge || completed[2].Position != 2 {
		t.Fatalf("a completed run's gradings %+v %v", completed, err)
	}
	if none, err := e.gradingsOf(ctx, run, objects.OutcomeInfraFailed); err != nil || len(none) != 0 {
		t.Fatalf("an excluded run is graded: %+v %v", none, err)
	}
	e.Keys = nil
	if checks, _ := e.gradingsOf(ctx, run, objects.OutcomeCompleted); len(checks) != 2 {
		t.Fatalf("with no key source, the checks alone: %+v", checks)
	}
	e.Grading = nil
	if none, _ := e.gradingsOf(ctx, run, objects.OutcomeCompleted); len(none) != 0 {
		t.Fatalf("an engine that grades nothing: %+v", none)
	}
	if _, _, err := e.graderOf(ctx, owner, "sha256:"+strings.Repeat("0", 64)); err == nil {
		t.Fatal("an unknown grader was read")
	}
}

// labeler is a minter that answers labels with its tokens, or fails them.
type labeler struct {
	minter
	labels map[string]string
	fail   error
}

func (l *labeler) Labels(context.Context, tokens.Request) (map[string]string, error) {
	return l.labels, l.fail
}

// TestTheCheckSandboxsTroubles: a sandbox that did not start, a write it
// did not take and a command it did not answer wait; a write it refused
// ends the checks not run, and a command it refused is a check that could
// not start; the labels the minter gives are on the sandbox, and labels it
// cannot give wait.
func TestTheCheckSandboxsTroubles(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	pending := func(b *bench) bool { return b.gradings(t)[0].State == objects.GradingPending }

	b := newBench(t, st)
	b.cl.phase = cella.PhaseFailed
	b.grade(t)
	if !pending(b) || len(b.cl.deleted) != 1 {
		t.Fatalf("a sandbox that did not start: %+v, %d deleted", b.gradings(t)[0], len(b.cl.deleted))
	}
	b = newBench(t, st)
	b.cl.put = http.StatusServiceUnavailable
	b.grade(t)
	if !pending(b) {
		t.Fatal("a write Cella did not answer ended the checks")
	}
	b = newBench(t, st)
	b.cl.put = http.StatusBadRequest
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "not written") {
		t.Fatalf("a refused write: %+v", run)
	}
	b = newBench(t, st)
	b.cl.exec = http.StatusBadGateway
	b.grade(t)
	if !pending(b) {
		t.Fatal("a command Cella did not answer ended the checks")
	}
	b = newBench(t, st)
	b.cl.exec = http.StatusBadRequest
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckNotRunnable || !strings.Contains(run.Reason, "no such command") {
		t.Fatalf("a refused command: %+v", run)
	}

	b = newBench(t, st)
	l := &labeler{labels: map[string]string{"tenant": "p-1"}}
	b.e.Grading.Tokens = l
	b.grade(t)
	if labels := b.cl.manifests[0]["metadata"].(map[string]any)["labels"].(map[string]any); labels["tenant"] != "p-1" || labels["examen/trial"] != b.trial.ID {
		t.Fatalf("the sandbox's labels %v", labels)
	}
	b = newBench(t, st)
	b.e.Grading.Tokens = &labeler{fail: tokens.ErrUnavailable}
	b.grade(t)
	if !pending(b) || len(b.cl.manifests) != 0 {
		t.Fatal("labels the minter could not give did not wait")
	}

	// Check files whose folder is gone are not run, with why.
	b = newBench(t, st)
	b.fs.mu.Lock()
	for p := range b.fs.objects {
		if strings.HasPrefix(p, "files/evals/graders/") {
			delete(b.fs.objects, p)
		}
	}
	b.fs.mu.Unlock()
	b.grade(t)
	if run := record[objects.CheckRun](t, b.gradings(t)[0]); run.State != objects.CheckNotRun || !strings.Contains(run.Reason, "files_changed") {
		t.Fatalf("check files that are gone: %+v", run)
	}
}

// TestJudgmentsThatEndEarly: a refusal is refused, a key source with no
// budget parks the cell over_budget, and a judge none of whose metrics
// applies to the trial is not called.
func TestJudgmentsThatEndEarly(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	ok := cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 1}}`}
	b := newBench(t, st)
	b.cl.answers[b.g.inventory] = ok
	b.lx.stop = "refusal"
	b.grade(t)
	b.grade(t)
	if j := record[objects.Judgment](t, b.gradings(t)[2]); j.State != objects.JudgmentRefused || len(b.lx.calls) != 1 {
		t.Fatalf("a refusal: %+v after %d calls", j, len(b.lx.calls))
	}
	b = newBench(t, st)
	b.cl.answers[b.g.inventory] = ok
	b.grade(t)
	b.e.Keys.(*keys.Memory).Err = keys.ErrBudgetExhausted
	b.grade(t)
	if cell, _ := st.Experiments().Cell(t.Context(), b.g.cid); cell.State != objects.StateOverBudget || len(b.lx.calls) != 0 {
		t.Fatalf("a key source with no budget: %s after %d calls", cell.State, len(b.lx.calls))
	}
}

// faultStore is a store whose reads of one kind fail while its fault is
// set, so a grading meets a store that does not answer.
type faultStore struct {
	store.Store
	versions, trials, measurements, keys bool
}

var errStoreDown = errors.New("the store is down")

func (f *faultStore) Versions() store.Versions { return faultVersions{f.Store.Versions(), f} }

func (f *faultStore) Experiments() store.Experiments {
	return faultExperiments{f.Store.Experiments(), f}
}

func (f *faultStore) Gradings() store.Gradings { return faultGradings{f.Store.Gradings(), f} }

type faultVersions struct {
	store.Versions
	f *faultStore
}

func (v faultVersions) ByDigest(ctx context.Context, kind, owner, digest string) (objects.Version, error) {
	if v.f.versions {
		return objects.Version{}, errStoreDown
	}
	return v.Versions.ByDigest(ctx, kind, owner, digest)
}

type faultExperiments struct {
	store.Experiments
	f *faultStore
}

func (x faultExperiments) Trial(ctx context.Context, id string) (objects.Trial, error) {
	if x.f.trials {
		return objects.Trial{}, errStoreDown
	}
	return x.Experiments.Trial(ctx, id)
}

func (x faultExperiments) MeasurementsOf(ctx context.Context, trials []string) ([]objects.Measurement, error) {
	if x.f.measurements {
		return nil, errStoreDown
	}
	return x.Experiments.MeasurementsOf(ctx, trials)
}

type faultGradings struct {
	store.Gradings
	f *faultStore
}

func (g faultGradings) HoldKey(ctx context.Context, experiment, grader, keyID string) error {
	if g.f.keys {
		return errStoreDown
	}
	return g.Gradings.HoldKey(ctx, experiment, grader, keyID)
}

// TestAStoreThatDoesNotAnswer: a grading whose reads the store does not
// answer is left pending for the next round, and the round says why.
func TestAStoreThatDoesNotAnswer(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	for name, set := range map[string]func(*faultStore){
		"versions":     func(f *faultStore) { f.versions = true },
		"trials":       func(f *faultStore) { f.trials = true },
		"measurements": func(f *faultStore) { f.measurements = true },
		"keys":         func(f *faultStore) { f.keys = true },
	} {
		b := newBench(t, st)
		b.cl.answers[b.g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 1}}`}
		f := &faultStore{Store: st}
		set(f)
		b.e.Store = f
		for range 2 {
			err := b.e.Grade(t.Context())
			if name == "versions" || name == "trials" {
				if !errors.Is(err, errStoreDown) {
					t.Fatalf("%s: the round answered %v", name, err)
				}
				break
			}
			if err != nil && !errors.Is(err, errStoreDown) {
				t.Fatalf("%s: %v", name, err)
			}
		}
		list := b.gradings(t)
		if list[len(list)-1].State != objects.GradingPending {
			t.Fatalf("%s: a store that did not answer ended the judgment: %+v", name, list[len(list)-1])
		}
	}
}
