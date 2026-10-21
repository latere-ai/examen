// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

var (
	checkDigest = "sha256:" + strings.Repeat("c", 64)
	judgeDigest = "sha256:" + strings.Repeat("d", 64)
	gradedValue = "sha256:" + strings.Repeat("e", 64)
)

// gradedTrial runs a one-trial experiment of one candidate to a done
// trial given a check and a judge to run, and answers the experiment, its
// cell and the trial.
func (w *world) gradedTrial(ctx context.Context, p plan) (string, string, objects.Trial) {
	w.t.Helper()
	x := w.st.Experiments()
	ne, cells := w.experiment(p, objects.ModeExecute, 1)
	ne.Experiment.Candidates, ne.Experiment.CandidateIDs = ne.Experiment.Candidates[:1], ne.Experiment.CandidateIDs[:1]
	ne.Cells, cells = ne.Cells[:1], cells[:1]
	ne.Comparisons = nil
	if err := x.Create(ctx, ne); err != nil {
		w.t.Fatal(err)
	}
	if err := x.SubmitRollout(ctx, cells[0], cells[0]+".smoke"); err != nil {
		w.t.Fatal(err)
	}
	l, err := x.Lease(ctx, store.LeaseRequest{Experiment: ne.Experiment.ID, Candidate: p.a.Digest, Subject: owner, Cells: cells})
	if err != nil || len(l.Trials) != 1 {
		w.t.Fatalf("lease %+v %v", l, err)
	}
	o := outcome(objects.OutcomeCompleted)
	o.Gradings = []objects.NewGrading{{Grader: checkDigest, Kind: objects.GraderCheck, Position: 0}, {Grader: judgeDigest, Kind: objects.GraderModelJudge, Position: 1}}
	t, err := x.Submit(ctx, store.Submission{Trial: l.Trials[0].ID, Lease: l.Lease, Subject: owner, Digest: "d", Outcome: o})
	if err != nil {
		w.t.Fatal(err)
	}
	return ne.Experiment.ID, cells[0], t
}

// TestGradingsRunInOrderAndEndOnce: a done trial's gradings are pending,
// its cell grading; a judge is due only once its trial's checks ended; a
// held grading is held by one taker; an ended grading appends its values
// as its grader's generation and never ends twice; and the cell and the
// experiment complete once every grading ended.
func TestGradingsRunInOrderAndEndOnce(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	x, g := w.st.Experiments(), w.st.Gradings()
	eid, cid, trial := w.gradedTrial(ctx, p)
	if cell, _ := x.Cell(ctx, cid); cell.State != objects.StateGrading {
		t.Fatalf("the cell of a trial with gradings is %s", cell.State)
	}
	if got, _ := x.Trial(ctx, trial.ID); len(got.Checks) != 1 || len(got.Judgments) != 1 || !strings.Contains(string(got.Judgments[0]), "pending") {
		t.Fatalf("the trial's gradings %s %s", got.Checks, got.Judgments)
	}
	due, err := g.Due(ctx, 10)
	if err != nil || len(due) != 1 || due[0].Grader != checkDigest || due[0].Kind != objects.GraderCheck {
		t.Fatalf("due before the check ended: %+v %v", due, err)
	}
	held, err := g.Hold(ctx, []string{due[0].ID}, time.Hour)
	if err != nil || len(held) != 1 {
		t.Fatalf("hold: %v %v", held, err)
	}
	if again, err := g.Hold(ctx, []string{due[0].ID}, time.Hour); err != nil || len(again) != 0 {
		t.Fatalf("a held grading was held twice: %v %v", again, err)
	}
	if err := g.Wait(ctx, due[0].ID, -time.Minute, 3); err != nil {
		t.Fatal(err)
	}
	due, _ = g.Due(ctx, 10)
	if len(due) != 1 || due[0].Attempts != 3 {
		t.Fatalf("a grading put off: %+v", due)
	}
	run := objects.CheckRun{Grader: checkDigest, State: objects.CheckOK}
	if err := g.End(ctx, store.GradingEnd{ID: due[0].ID, State: objects.CheckOK, Record: run, Values: []store.Measured{{Metric: gradedValue, Value: true}}}); err != nil {
		t.Fatal(err)
	}
	if err := g.End(ctx, store.GradingEnd{ID: due[0].ID, State: objects.CheckError, Record: run}); err != nil {
		t.Fatal(err)
	}
	list, err := g.OfTrials(ctx, []string{trial.ID})
	if err != nil || len(list) != 2 || list[0].State != objects.CheckOK || !strings.Contains(string(list[0].Record), `"generation": 1`) {
		t.Fatalf("the check after its end, and an end again: %+v %v", list, err)
	}
	ms, _ := x.MeasurementsOf(ctx, []string{trial.ID})
	found := false
	for _, m := range ms {
		found = found || (m.Grader == checkDigest && m.Metric == gradedValue && m.Value == true && m.Generation == 1)
	}
	if !found {
		t.Fatalf("the check's value was not appended: %+v", ms)
	}
	due, _ = g.Due(ctx, 10)
	if len(due) != 1 || due[0].Grader != judgeDigest {
		t.Fatalf("the judge after the check ended: %+v", due)
	}
	if err := g.End(ctx, store.GradingEnd{ID: due[0].ID, State: objects.JudgmentRefused, Record: objects.Judgment{Grader: judgeDigest, State: objects.JudgmentRefused}}); err != nil {
		t.Fatal(err)
	}
	cell, _ := x.Cell(ctx, cid)
	exp, _ := x.Get(ctx, eid)
	if cell.State != objects.StateComplete || exp.State != objects.StateComplete {
		t.Fatalf("after every grading ended: cell %s, experiment %s", cell.State, exp.State)
	}
	if err := g.HoldKey(ctx, eid, judgeDigest, "key-1"); err != nil {
		t.Fatal(err)
	}
	if err := g.HoldKey(ctx, eid, judgeDigest, "key-1"); err != nil {
		t.Fatal(err)
	}
	keys, err := g.UnreleasedKeys(ctx, 10)
	if err != nil || len(keys) != 1 || keys[0].KeyID != "key-1" || keys[0].Owner != owner {
		t.Fatalf("the judge keys to release: %+v %v", keys, err)
	}
	if err := g.ReleasedKey(ctx, eid, "key-1"); err != nil {
		t.Fatal(err)
	}
	if keys, _ := g.UnreleasedKeys(ctx, 10); len(keys) != 0 {
		t.Fatalf("a released key is still to release: %+v", keys)
	}
	if list, err := g.OfTrials(ctx, nil); err != nil || list != nil {
		t.Fatalf("no trials: %v %v", list, err)
	}
	if err := g.End(ctx, store.GradingEnd{ID: "none"}); err == nil {
		t.Fatal("an unknown grading ended")
	}
}

// TestACancelEndsOpenGradings: a canceled experiment's pending gradings
// end not_run, and a grading's spend refusal parks its cell over_budget.
func TestACancelEndsOpenGradings(t *testing.T) {
	w := newWorld(t)
	p := w.plan()
	ctx := t.Context()
	x, g := w.st.Experiments(), w.st.Gradings()
	eid, _, trial := w.gradedTrial(ctx, p)
	if _, err := x.Cancel(ctx, eid); err != nil {
		t.Fatal(err)
	}
	list, _ := g.OfTrials(ctx, []string{trial.ID})
	var rec map[string]any
	if err := json.Unmarshal(list[1].Record, &rec); err != nil || list[1].State != objects.CheckNotRun || !strings.Contains(rec["reason"].(string), "canceled") {
		t.Fatalf("a canceled experiment's gradings: %+v %v", list, err)
	}
	if due, _ := g.Due(ctx, 10); len(due) != 0 {
		t.Fatalf("a canceled experiment's gradings are due: %+v", due)
	}

	_, cid, trial := w.gradedTrial(ctx, p)
	list, _ = g.OfTrials(ctx, []string{trial.ID})
	if err := g.End(ctx, store.GradingEnd{ID: list[1].ID, State: objects.JudgmentOverBudget, Record: objects.Judgment{State: objects.JudgmentOverBudget},
		Park: objects.StateOverBudget, ParkReason: "budget_exhausted: judge"}); err != nil {
		t.Fatal(err)
	}
	cell, _ := x.Cell(ctx, cid)
	list, _ = g.OfTrials(ctx, []string{trial.ID})
	if cell.State != objects.StateOverBudget || cell.ParkedReason != "budget_exhausted: judge" || list[0].State != objects.CheckNotRun {
		t.Fatalf("a spend refusal: cell %s %q, check %s", cell.State, cell.ParkedReason, list[0].State)
	}
}

// TestAGradingsEndFailsWhole: a fault anywhere in a grading's end leaves
// nothing of it, and it ends once the fault is gone.
func TestAGradingsEndFailsWhole(t *testing.T) {
	w := newWorld(t)
	w.exec(`CREATE TRIGGER a_test_fault BEFORE INSERT OR UPDATE OR DELETE ON gradings FOR EACH ROW EXECUTE FUNCTION examen_test_fault()`)
	p := w.plan()
	ctx := t.Context()
	g := w.st.Gradings()
	w.eachFault("end a grading", func() func() error {
		_, _, trial := w.gradedTrial(ctx, p)
		list, err := g.OfTrials(ctx, []string{trial.ID})
		if err != nil {
			t.Fatal(err)
		}
		return func() error {
			return g.End(ctx, store.GradingEnd{ID: list[0].ID, State: objects.CheckOK, Record: objects.CheckRun{State: objects.CheckOK},
				Values: []store.Measured{{Metric: gradedValue, Value: 1.0}}})
		}
	})
}

// TestGradingsOnAClosedStore: every call answers an error.
func TestGradingsOnAClosedStore(t *testing.T) {
	direct, _ := pgtest.Database(t)
	st, _, err := postgres.Open(t.Context(), direct, postgres.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	g := st.Gradings()
	for i, call := range []func() error{
		func() error { _, err := g.Due(ctx, 1); return err },
		func() error { _, err := g.Hold(ctx, []string{"x"}, time.Minute); return err },
		func() error { return g.Wait(ctx, "x", time.Minute, 1) },
		func() error { return g.End(ctx, store.GradingEnd{ID: "x"}) },
		func() error { _, err := g.OfTrials(ctx, []string{"t"}); return err },
		func() error { return g.HoldKey(ctx, "e", "g", "k") },
		func() error { _, err := g.UnreleasedKeys(ctx, 1); return err },
		func() error { return g.ReleasedKey(ctx, "e", "k") },
	} {
		if err := call(); err == nil {
			t.Errorf("call %d answered on a closed store", i)
		}
	}
}
