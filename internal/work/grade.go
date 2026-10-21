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
	"strings"
	"sync"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/grade"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/topos"
)

// Grading is what Examen grades a finished trial with (specs/006-grading.md):
// its checks in a check sandbox of Cella over the run's files read from
// Arca, and its model judges through Lux on a Key of the key source. Each
// core is reached with owner tokens for its audience; a part whose core
// is not configured is nil and runs nothing.
type Grading struct {
	Tokens tokens.Source
	// Arca reads a run's kept files and a check's files, as the owner.
	Arca         *arca.Client
	ArcaAudience string
	// Cella runs check sandboxes in Environment, its default when empty,
	// from Image, the installation's default image when empty.
	Cella         *cella.Client
	CellaAudience string
	Environment   string
	Image         string
	// LuxURL is the root of Lux's lux door, where a judge's call goes on
	// its Key; HTTP sends it.
	LuxURL string
	HTTP   *http.Client
	// Workers is how many gradings run at once on this replica.
	Workers int
}

// Checks reports whether checks run: Cella and Arca are reached.
func (g *Grading) Checks() bool {
	return g != nil && g.Cella != nil && g.Arca != nil && g.Tokens != nil
}

// Judges reports whether model judges run: Lux and Arca are reached. The
// key source is the engine's.
func (g *Grading) Judges() bool {
	return g != nil && g.LuxURL != "" && g.Arca != nil && g.Tokens != nil
}

// holdChecks is how long a replica holds a trial's checks: their bound,
// the sandbox's start and the grace.
const holdChecks = limits.CheckTimeout + limits.TrialGrace + sandboxStart

// holdJudge is how long a replica holds a judgment: its attempts' calls.
const holdJudge = 10 * time.Minute

// sandboxStart bounds how long a check sandbox's create waits for it to
// run.
const sandboxStart = 5 * time.Minute

// gradeWait is how long a grading waits after an outage of a core before
// it is taken again.
const gradeWait = time.Minute

// gradeOutages bounds the outages a grading waits out before it ends
// without a value.
const gradeOutages = 30

// Grade runs one round of the gradings due: each trial's checks together in
// one check sandbox, and each judgment on its own, at most Workers at once,
// and answers once every one it took ended or waits.
func (e *Engine) Grade(ctx context.Context) error {
	g := e.Grading
	if g == nil || (!g.Checks() && !g.Judges()) {
		return nil
	}
	due, err := e.Store.Gradings().Due(ctx, batch)
	if err != nil {
		return err
	}
	type job struct {
		hold time.Duration
		rows []objects.Grading
	}
	var jobs []job
	checks := map[string]int{}
	for _, d := range due {
		switch {
		case d.Kind == objects.GraderCheck && g.Checks():
			if i, ok := checks[d.Trial]; ok {
				jobs[i].rows = append(jobs[i].rows, d)
				continue
			}
			checks[d.Trial] = len(jobs)
			jobs = append(jobs, job{hold: holdChecks, rows: []objects.Grading{d}})
		case d.Kind == objects.GraderModelJudge && g.Judges() && e.Keys != nil:
			jobs = append(jobs, job{hold: holdJudge, rows: []objects.Grading{d}})
		}
	}
	workers := max(g.Workers, 1)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, j := range jobs {
		ids := make([]string, len(j.rows))
		for i, r := range j.rows {
			ids[i] = r.ID
		}
		held, err := e.Store.Gradings().Hold(ctx, ids, j.hold)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var rows []objects.Grading
		for _, r := range j.rows {
			if slices.Contains(held, r.ID) {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			var err error
			if rows[0].Kind == objects.GraderCheck {
				err = e.runChecks(ctx, rows)
			} else {
				err = e.runJudge(ctx, rows[0])
			}
			if err != nil {
				e.log().WarnContext(ctx, "a grading did not end; it is taken again", "trial", rows[0].Trial, "grader", rows[0].Grader, "error", apierr.LogText(err))
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// GradeLoop runs a round of the gradings due every interval until ctx
// ends, apart from Tick, since a trial's checks take minutes.
func (e *Engine) GradeLoop(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := e.Grade(ctx); err != nil && ctx.Err() == nil {
				e.log().WarnContext(ctx, "a round of grading did not finish; the next takes what it left", "error", apierr.LogText(err))
			}
		}
	}
}

// later puts a held grading off after an outage of a core: due again after
// gradeWait, until gradeOutages of them, when it ends with no value.
func (e *Engine) later(ctx context.Context, r objects.Grading, end func(reason string) store.GradingEnd, cause error) error {
	if r.Attempts+1 >= gradeOutages {
		return e.Store.Gradings().End(ctx, end("the run waited out "+fmt.Sprint(gradeOutages)+" outages: "+apierr.LogText(cause)))
	}
	e.log().InfoContext(ctx, "a grading waits for a core", "trial", r.Trial, "grader", r.Grader, "error", apierr.LogText(cause))
	return e.Store.Gradings().Wait(ctx, r.ID, gradeWait, r.Attempts+1)
}

// gradedRun is what a trial's graders read of it: the experiment, the
// trial, its cell's task, its suite's metrics by digest, and its evidence
// from its session's log.
type gradedRun struct {
	exp        objects.Experiment
	trial      objects.Trial
	task       objects.Task
	taskDigest string
	metrics    map[string]objects.Metric
	ev         topos.Evidence
	// folder is the owner's space and folder that holds the kept copy of
	// the run's working directory, "" when the run kept none.
	folderOwner, folder string
}

// errEvidenceChanged is evidence whose digest is not the one the trial
// records.
var errEvidenceChanged = errors.New("work: the trial's evidence changed since it was recorded")

// runOfTrial reads what a trial's graders read: the session's log through
// the transcript's pinned event, its digest checked against the one the
// trial records.
func (e *Engine) runOfTrial(ctx context.Context, tid, purpose string) (gradedRun, error) {
	x := e.Store.Experiments()
	var r gradedRun
	t, err := x.Trial(ctx, tid)
	if err != nil {
		return r, err
	}
	exp, err := x.Get(ctx, t.Experiment)
	if err != nil {
		return r, err
	}
	cell, err := x.Cell(ctx, t.Cell)
	if err != nil {
		return r, err
	}
	r.exp, r.trial, r.taskDigest = exp, t, cell.Task
	vs := e.Store.Versions()
	tv, err := vs.ByDigest(ctx, objects.KindTask, exp.Owner, cell.Task)
	if err != nil {
		return r, fmt.Errorf("work: reading trial %s's task: %w", tid, err)
	}
	if err := json.Unmarshal(tv.Body, &r.task); err != nil {
		return r, fmt.Errorf("work: decoding trial %s's task: %w", tid, err)
	}
	sv, err := vs.ByDigest(ctx, objects.KindSuite, exp.Owner, exp.Suite)
	if err != nil {
		return r, fmt.Errorf("work: reading trial %s's suite: %w", tid, err)
	}
	var su objects.Suite
	if err := json.Unmarshal(sv.Body, &su); err != nil {
		return r, fmt.Errorf("work: decoding trial %s's suite: %w", tid, err)
	}
	r.metrics = map[string]objects.Metric{}
	for _, md := range su.Metrics {
		if b, ok := objects.Builtin(md); ok {
			r.metrics[md] = b
			continue
		}
		mv, err := vs.ByDigest(ctx, objects.KindMetric, exp.Owner, md)
		if err != nil {
			return r, fmt.Errorf("work: reading a metric of trial %s's suite: %w", tid, err)
		}
		var m objects.Metric
		if err := json.Unmarshal(mv.Body, &m); err != nil {
			return r, fmt.Errorf("work: decoding a metric of trial %s's suite: %w", tid, err)
		}
		r.metrics[md] = m
	}
	var transcript *objects.EvidenceRef
	for i, ref := range t.Evidence {
		switch {
		case ref.Kind == "transcript":
			transcript = &t.Evidence[i]
		case ref.Kind == objects.EvidenceOutput && ref.Name == "workspace/":
			r.folderOwner, r.folder = ref.Owner, strings.TrimSuffix(ref.Path, "/")
		}
	}
	if transcript == nil || e.Topos == nil {
		return r, fmt.Errorf("work: trial %s has no session log to grade", tid)
	}
	session, seq, ok := sessionRef(transcript.Path)
	if !ok {
		return r, fmt.Errorf("work: trial %s's transcript is no session log", tid)
	}
	token, err := e.Topos.Tokens.Token(ctx, tokens.Request{Owner: exp.Owner, Subject: exp.CreatedBy, Audience: e.Topos.Audience, Experiment: exp.ID, Purpose: purpose})
	if err != nil {
		return r, err
	}
	events, err := e.Topos.Client.Events(ctx, token, session)
	var core *topos.RefusedError
	if errors.As(err, &core) && core.Status == http.StatusNotFound {
		return r, fmt.Errorf("%w: the session's log is gone at Topos", errEvidenceChanged)
	}
	if err != nil {
		return r, err
	}
	events = slices.DeleteFunc(events, func(ev topos.Event) bool { return ev.Seq > seq })
	if r.ev, err = topos.EvidenceOf(events); err != nil {
		return r, err
	}
	if r.ev.LogDigest != transcript.Digest {
		return r, errEvidenceChanged
	}
	return r, nil
}

// sessionRef reads a session log's evidence path, topos/sessions/<id>/events@<seq>.
func sessionRef(path string) (string, uint64, bool) {
	rest, ok := strings.CutPrefix(path, "topos/sessions/")
	if !ok {
		return "", 0, false
	}
	id, at, ok := strings.Cut(rest, "/events@")
	if !ok || id == "" {
		return "", 0, false
	}
	var seq uint64
	if _, err := fmt.Sscan(at, &seq); err != nil {
		return "", 0, false
	}
	return id, seq, true
}

// workFile is one kept file of a run's working directory as read: its
// record in the log and its bytes, nil for a file kept by digest alone.
type workFile struct {
	topos.WorkFile
	Data []byte
}

// readWork reads the kept copy of a run's working directory from the
// owner's files store, each file's SHA-256 checked against the log's
// record of it. A file larger than OUTPUT_FILE_KEEP_MAX_BYTES, or past
// MAX_OUTPUT_FILES or MAX_OUTPUT_BYTES, is kept by digest alone.
func (e *Engine) readWork(ctx context.Context, r gradedRun, purpose string) ([]workFile, error) {
	if len(r.ev.Work) == 0 {
		return nil, nil
	}
	if r.folder == "" {
		return nil, fmt.Errorf("%w: the log keeps files and the trial names no folder", errEvidenceChanged)
	}
	g := e.Grading
	token, err := g.Tokens.Token(ctx, tokens.Request{Owner: r.exp.Owner, Subject: r.exp.CreatedBy, Audience: g.ArcaAudience, Experiment: r.exp.ID, Purpose: purpose})
	if err != nil {
		return nil, err
	}
	var out []workFile
	var total int64
	for i, f := range r.ev.Work {
		wf := workFile{WorkFile: f}
		if f.Size > limits.OutputFileKeepMaxBytes || i >= limits.MaxOutputFiles || total+f.Size > limits.MaxOutputBytes {
			out = append(out, wf)
			continue
		}
		data, err := g.Arca.Read(ctx, token, r.folderOwner, r.folder+"/"+f.Path, limits.OutputFileKeepMaxBytes)
		var refused *arca.RefusedError
		if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
			return nil, fmt.Errorf("%w: %s is gone from the run's folder", errEvidenceChanged, f.Path)
		}
		if err != nil {
			return nil, err
		}
		if hexSHA(data) != f.SHA256 {
			return nil, fmt.Errorf("%w: %s's bytes are not the ones the log recorded", errEvidenceChanged, f.Path)
		}
		wf.Data, total = data, total+f.Size
		out = append(out, wf)
	}
	return out, nil
}

// signInEnded is why a minter's refusal ends a grading: the owner's
// sign-in no longer acts for them, so nothing more is read or run as them.
func signInEnded(err error) (string, bool) {
	var mint *tokens.RefusedError
	if errors.As(err, &mint) {
		return "the owner's sign-in no longer acts for them: " + mint.Reason, true
	}
	return "", false
}

// outage reports whether err is a core not answering, which a grading
// waits out.
func outage(err error) bool {
	return errors.Is(err, arca.ErrUnavailable) || errors.Is(err, cella.ErrUnavailable) || errors.Is(err, topos.ErrUnavailable) ||
		errors.Is(err, tokens.ErrUnavailable)
}

// produced are a grader's metrics the suite lists, in its order.
func produced(g objects.Grader, metrics map[string]objects.Metric) []grade.Produced {
	var out []grade.Produced
	for _, d := range g.Produces {
		if m, ok := metrics[d]; ok {
			out = append(out, grade.Produced{Digest: d, Metric: m})
		}
	}
	return out
}

// graderOf reads a grader version of the experiment's owner by digest,
// and its version id.
func (e *Engine) graderOf(ctx context.Context, owner, digest string) (objects.Grader, string, error) {
	v, err := e.Store.Versions().ByDigest(ctx, objects.KindGrader, owner, digest)
	if err != nil {
		return objects.Grader{}, "", fmt.Errorf("work: reading grader %s: %w", digest, err)
	}
	var g objects.Grader
	if err := json.Unmarshal(v.Body, &g); err != nil {
		return objects.Grader{}, "", fmt.Errorf("work: decoding grader %s: %w", digest, err)
	}
	return g, v.ID, nil
}

// ReleaseJudgeKeys releases the judge Keys of experiments that ended.
func (e *Engine) ReleaseJudgeKeys(ctx context.Context) error {
	if e.Keys == nil || e.Grading == nil {
		return nil
	}
	held, err := e.Store.Gradings().UnreleasedKeys(ctx, batch)
	if err != nil {
		return err
	}
	var errs []error
	for _, h := range held {
		if err := e.Keys.Release(ctx, h.KeyID); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := e.Store.Gradings().ReleasedKey(ctx, h.Experiment, h.KeyID); err != nil {
			errs = append(errs, err)
			continue
		}
		e.log().InfoContext(ctx, "an ended experiment's judge key was released", "experiment", h.Experiment, "key_id", h.KeyID)
	}
	return errors.Join(errs...)
}

// gradingsOf are the gradings a topos trial of the outcome gets once it is
// done (specs/006-grading.md): its task's checks in their order, then the
// task's and the suite's model judges, each judge only when one of the
// metrics it produces applies to a trial of the outcome. A grader of a kind
// this installation does not run is left out, as the create refused it.
func (e *Engine) gradingsOf(ctx context.Context, run toposRun, outcome string) ([]objects.NewGrading, error) {
	g := e.Grading
	if g == nil || objects.Excluded(outcome) {
		return nil, nil
	}
	vs := e.Store.Versions()
	sv, err := vs.ByDigest(ctx, objects.KindSuite, run.exp.Owner, run.exp.Suite)
	if err != nil {
		return nil, fmt.Errorf("work: reading experiment %s's suite: %w", run.exp.ID, err)
	}
	var su objects.Suite
	if err := json.Unmarshal(sv.Body, &su); err != nil {
		return nil, fmt.Errorf("work: decoding experiment %s's suite: %w", run.exp.ID, err)
	}
	var out []objects.NewGrading
	if g.Checks() {
		for _, d := range run.task.Checks {
			out = append(out, objects.NewGrading{Grader: d, Kind: objects.GraderCheck, Position: len(out)})
		}
	}
	if !g.Judges() || e.Keys == nil {
		return out, nil
	}
	for _, d := range slices.Concat(run.task.Graders, su.Graders) {
		gr, _, err := e.graderOf(ctx, run.exp.Owner, d)
		if err != nil {
			return nil, err
		}
		if gr.Kind != objects.GraderModelJudge || gr.LevelOf() != objects.LevelTrial {
			continue
		}
		applies := false
		for _, md := range gr.Produces {
			if !su.Lists(md) {
				continue
			}
			m, ok := objects.Builtin(md)
			if !ok {
				mv, err := vs.ByDigest(ctx, objects.KindMetric, run.exp.Owner, md)
				if err != nil {
					return nil, fmt.Errorf("work: reading a metric of grader %s: %w", d, err)
				}
				if err := json.Unmarshal(mv.Body, &m); err != nil {
					return nil, fmt.Errorf("work: decoding a metric of grader %s: %w", d, err)
				}
			}
			applies = applies || m.Applies(outcome)
		}
		if applies {
			out = append(out, objects.NewGrading{Grader: d, Kind: objects.GraderModelJudge, Position: len(out)})
		}
	}
	return out, nil
}
