// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package execute

import (
	"maps"
	"slices"
	"strconv"
	"testing"
	"time"

	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
)

func TestDeal(t *testing.T) {
	loads := []Load{
		{Cell: "b2", TaskPos: 1, CandidatePos: 1, Unfinished: 5},
		{Cell: "a1", TaskPos: 0, CandidatePos: 0, Unfinished: 2},
		{Cell: "a2", TaskPos: 0, CandidatePos: 1, Unfinished: 9},
		{Cell: "b1", TaskPos: 1, CandidatePos: 0, Unfinished: 1},
	}
	// Task 0 first, round robin: a1 takes 2, a2 takes the rest of 5.
	if got := Deal(5, loads); !maps.Equal(got, map[string]int{"a1": 2, "a2": 3}) {
		t.Fatalf("five: %v", got)
	}
	// Enough for everything.
	if got := Deal(100, loads); !maps.Equal(got, map[string]int{"a1": 2, "a2": 9, "b1": 1, "b2": 5}) {
		t.Fatalf("all: %v", got)
	}
	if got := Deal(0, loads); len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
	// Round robin within a task keeps two cells' trials in one window.
	if got := Deal(4, []Load{{Cell: "x", Unfinished: 10}, {Cell: "y", CandidatePos: 1, Unfinished: 10}}); got["x"] != 2 || got["y"] != 2 {
		t.Fatalf("round robin: %v", got)
	}
}

// Under interleaved scheduling every cell of every task takes one trial
// before any takes a second: with four cells of two tasks and a
// parallelism of six, each cell holds one and the first two in the deal's
// order a second; held trials keep their places, and a parallelism they
// already fill deals nothing more.
func TestInterleavedAcrossTasks(t *testing.T) {
	var trials []Unfinished
	for _, c := range []Unfinished{{Cell: "b2", TaskPos: 1, CandidatePos: 1}, {Cell: "a1"}, {Cell: "a2", CandidatePos: 1}, {Cell: "b1", TaskPos: 1}} {
		for i := range 3 {
			u := c
			u.Rank, u.Index = 2, i
			trials = append(trials, u)
		}
	}
	if got := DealInterleaved(6, trials); !maps.Equal(got, map[string]int{"a1": 2, "a2": 2, "b1": 1, "b2": 1}) {
		t.Fatalf("six: %v", got)
	}
	if got := DealInterleaved(100, trials); !maps.Equal(got, map[string]int{"a1": 3, "a2": 3, "b1": 3, "b2": 3}) {
		t.Fatalf("all: %v", got)
	}
	// b2's trial 0 is held: it keeps its place, and the three slots left go
	// to trial 0 of the other cells, not to a second trial of task 0.
	held := slices.Clone(trials)
	held[0].Held = true
	if got := DealInterleaved(4, held); !maps.Equal(got, map[string]int{"a1": 1, "a2": 1, "b1": 1, "b2": 1}) {
		t.Fatalf("held: %v", got)
	}
	// Held trials beyond a lowered parallelism deal nothing more.
	for i := range held[:4] {
		held[i].Held = true
	}
	if got := DealInterleaved(2, held); !maps.Equal(got, map[string]int{"a1": 1, "b2": 3}) {
		t.Fatalf("lowered: %v", got)
	}
}

// Before orders trials smoke first, then retries, then main trials, each by
// index: across every cell under interleaved, and within a task, the task
// first, under task_by_task.
func TestBefore(t *testing.T) {
	trials := []Unfinished{
		{Cell: "b1", TaskPos: 1, Rank: 2, Index: 1},
		{Cell: "a2", CandidatePos: 1, Rank: 2, Index: 2},
		{Cell: "a1", Rank: 0, Index: 0},
		{Cell: "b1", TaskPos: 1, Rank: 1, Index: 3},
		{Cell: "a1", Rank: 2, Index: 1},
	}
	name := func(ts []Unfinished) []string {
		var out []string
		for _, u := range ts {
			out = append(out, u.Cell+":"+strconv.Itoa(u.Index))
		}
		return out
	}
	inter := slices.Clone(trials)
	slices.SortStableFunc(inter, func(a, b Unfinished) int { return Before(objects.SchedulingInterleaved, a, b) })
	if got, want := name(inter), []string{"a1:0", "b1:3", "a1:1", "b1:1", "a2:2"}; !slices.Equal(got, want) {
		t.Fatalf("interleaved %v, want %v", got, want)
	}
	byTask := slices.Clone(trials)
	slices.SortStableFunc(byTask, func(a, b Unfinished) int { return Before(objects.SchedulingTaskByTask, a, b) })
	if got, want := name(byTask), []string{"a1:0", "a1:1", "a2:2", "b1:3", "b1:1"}; !slices.Equal(got, want) {
		t.Fatalf("task by task %v, want %v", got, want)
	}
}

// trial builds a trial of a rollout.
func trial(id string, index int, rollout, state, outcome string) objects.Trial {
	return objects.Trial{ID: id, Index: index, Rollout: rollout, State: state, Outcome: outcome}
}

func submitted(key, kind string) objects.Rollout {
	r := rollout(key, kind, objects.ExecutorExternal)
	r.State = objects.RolloutSubmitted
	return r
}

func TestSmokePassesAndQueuesMain(t *testing.T) {
	smokeTrial := trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted)
	c := Cell{ID: "c", State: objects.StateSmoking, N: 3, Rollouts: []objects.Rollout{submitted("c.smoke", objects.RolloutSmoke)}, Trials: []objects.Trial{smokeTrial}}
	s := Advance(c, smokeTrial)
	if s.State != objects.StateRunning || !slices.Equal(s.Ended, []string{"c.smoke"}) || len(s.Queue) != 1 {
		t.Fatalf("%+v", s)
	}
	main := s.Queue[0]
	if main.Rollout.Key != "c.main" || main.Rollout.State != objects.RolloutQueued || len(main.Trials) != 2 || main.Trials[0].Index != 1 || main.Trials[1].Index != 2 {
		t.Fatalf("main %+v", main)
	}
	if !s.Changed() || (Step{}).Changed() {
		t.Fatal("changed")
	}
	// N within the smoke trials: the cell completes.
	c.N = 1
	if s := Advance(c, smokeTrial); s.State != objects.StateComplete || len(s.Queue) != 0 {
		t.Fatalf("one trial: %+v", s)
	}
	// A smoke trial still running holds the cell.
	c.N, c.Trials = 3, []objects.Trial{trial("t1", 0, "c.smoke", objects.StateRunning, "")}
	if s := Advance(c, smokeTrial); s.Changed() {
		t.Fatalf("waiting: %+v", s)
	}
}

func TestInfraFailuresRetryThenBlock(t *testing.T) {
	failed := trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeInfraFailed)
	failed.Failure = &objects.Failure{Code: "harness_crashed"}
	c := Cell{ID: "c", State: objects.StateSmoking, N: 3, Rollouts: []objects.Rollout{submitted("c.smoke", objects.RolloutSmoke)}, Trials: []objects.Trial{failed}}
	s := Advance(c, failed)
	if s.State != "" || len(s.Queue) != 1 || s.Queue[0].Rollout.Key != "c.retry.1" || !s.Queue[0].Trials[0].Auto || s.Queue[0].Trials[0].RetryOf != "t1" || !slices.Equal(s.Retried, []string{"t1"}) {
		t.Fatalf("the first retry: %+v", s)
	}
	// After MAX_INFRA_RETRIES automatic retries the index stays failed and
	// the smoking cell parks blocked.
	chain := []objects.Trial{failed}
	failed.Retried = true
	chain[0] = failed
	prev := "t1"
	for i := range limits.MaxInfraRetries {
		r := trial("t"+string(rune('2'+i)), 0, "c.retry."+string(rune('1'+i)), objects.StateDone, objects.OutcomeInfraFailed)
		r.RetryOf, r.AutoRetry, r.Failure = prev, true, &objects.Failure{Code: "harness_crashed"}
		if i < limits.MaxInfraRetries-1 {
			r.Retried = true
		}
		chain = append(chain, r)
		prev = r.ID
	}
	rollouts := []objects.Rollout{rollout("c.smoke", objects.RolloutSmoke, objects.ExecutorExternal)}
	rollouts[0].State = objects.RolloutEnded
	for i := range limits.MaxInfraRetries {
		rollouts = append(rollouts, submitted("c.retry."+string(rune('1'+i)), objects.RolloutRetry))
		if i < limits.MaxInfraRetries-1 {
			rollouts[len(rollouts)-1].State = objects.RolloutEnded
		}
	}
	c = Cell{ID: "c", State: objects.StateSmoking, N: 3, Rollouts: rollouts, Trials: chain}
	s = Advance(c, chain[len(chain)-1])
	if s.State != objects.StateBlocked || len(s.Queue) != 0 || s.Reason != "smoke_infra_failed: "+prev+": harness_crashed" {
		t.Fatalf("blocked: %+v", s)
	}
}

// noMachineChain is a smoking cell whose smoke index ran n times and found
// no machine free each time: the first trial and n-1 automatic retries,
// each in its own rollout, the last just done.
func noMachineChain(n int) (Cell, objects.Trial) {
	var chain []objects.Trial
	rollouts := []objects.Rollout{submitted("c.smoke", objects.RolloutSmoke)}
	prev := ""
	for i := range n {
		key := "c.smoke"
		if i > 0 {
			key = "c.retry." + strconv.Itoa(i)
			rollouts = append(rollouts, submitted(key, objects.RolloutRetry))
		}
		tr := trial("t"+strconv.Itoa(100+i), 0, key, objects.StateDone, objects.OutcomeInfraFailed)
		tr.RetryOf, tr.AutoRetry, tr.Failure = prev, i > 0, &objects.Failure{Code: objects.CodeMachineUnavailable}
		if i < n-1 {
			tr.Retried = true
			rollouts[len(rollouts)-1].State = objects.RolloutEnded
		}
		chain = append(chain, tr)
		prev = tr.ID
	}
	return Cell{ID: "c", State: objects.StateSmoking, N: 1, Rollouts: rollouts, Trials: chain}, chain[n-1]
}

// TestANoMachineRunWaitsWithoutSpendingRetries: a run that found no
// machine free is retried MAX_MACHINE_WAITS times without spending
// MAX_INFRA_RETRIES, then spends them, and then the smoking cell parks; an
// infrastructure failure after waits spends a retry of its own; and each
// wait doubles from MACHINE_WAIT up to MACHINE_WAIT_MAX.
func TestANoMachineRunWaitsWithoutSpendingRetries(t *testing.T) {
	most := limits.MaxMachineWaits + limits.MaxInfraRetries
	for n := 1; n <= most+1; n++ {
		c, last := noMachineChain(n)
		s := Advance(c, last)
		if n <= most {
			if s.State != "" || len(s.Queue) != 1 || !s.Queue[0].Trials[0].Auto || s.Queue[0].Trials[0].RetryOf != last.ID {
				t.Fatalf("run %d that found no machine: %+v, want a retry", n, s)
			}
			continue
		}
		if s.State != objects.StateBlocked || len(s.Queue) != 0 || s.Reason != "smoke_infra_failed: "+last.ID+": "+objects.CodeMachineUnavailable {
			t.Fatalf("run %d past every wait and retry: %+v, want blocked", n, s)
		}
	}
	c, last := noMachineChain(limits.MaxMachineWaits + 1)
	byID := map[string]objects.Trial{}
	for _, tr := range c.Trials {
		byID[tr.ID] = tr
	}
	if r := RetriesOf(last, byID); r != (Retries{Waits: limits.MaxMachineWaits}) {
		t.Fatalf("a chain of waits spent %+v", r)
	}
	crashed := trial("t999", 0, "c.retry.x", objects.StateDone, objects.OutcomeInfraFailed)
	crashed.RetryOf, crashed.AutoRetry, crashed.Failure = c.Trials[1].ID, true, &objects.Failure{Code: "harness_crashed"}
	if r := RetriesOf(crashed, byID); r != (Retries{Waits: 2}) {
		t.Fatalf("a crash after a wait counts %+v, want the waits before it", r)
	}
	orphan := trial("t998", 0, "c.retry.y", objects.StateDone, objects.OutcomeInfraFailed)
	orphan.RetryOf, orphan.AutoRetry = "t_gone", true
	if r := RetriesOf(orphan, byID); r != (Retries{Infra: 1}) {
		t.Fatalf("a retry of a trial the cell does not hold counts %+v", r)
	}
	for waits, want := range map[int]time.Duration{0: 30 * time.Second, 1: 30 * time.Second, 2: time.Minute, 4: 4 * time.Minute, 5: limits.MachineWaitMax, 8: limits.MachineWaitMax} {
		if got := MachineWait(waits); got != want {
			t.Errorf("wait %d is %v, want %v", waits, got, want)
		}
	}
}

func TestRunningCellCompletes(t *testing.T) {
	done := []objects.Trial{
		trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted),
		trial("t2", 1, "c.main", objects.StateDone, objects.OutcomeTimedOut),
		trial("t3", 2, "c.main", objects.StateDone, objects.OutcomeDeclined),
	}
	ended := rollout("c.smoke", objects.RolloutSmoke, objects.ExecutorExternal)
	ended.State = objects.RolloutEnded
	c := Cell{ID: "c", State: objects.StateRunning, N: 3, Rollouts: []objects.Rollout{ended, submitted("c.main", objects.RolloutMain)}, Trials: done}
	if s := Advance(c, done[2]); s.State != objects.StateComplete || !slices.Equal(s.Ended, []string{"c.main"}) {
		t.Fatalf("%+v", s)
	}
	c.Trials = append(done[:2:2], trial("t3", 2, "c.main", objects.StateRunning, ""))
	if s := Advance(c, done[1]); s.State != "" || len(s.Ended) != 0 {
		t.Fatalf("an open main: %+v", s)
	}
	// A parked or complete cell does nothing.
	c.State = objects.StateComplete
	if s := Advance(c, done[1]); s.Changed() {
		t.Fatalf("complete: %+v", s)
	}
}

func TestOverBudgetParks(t *testing.T) {
	spent := trial("t2", 1, "c.main", objects.StateDone, objects.OutcomeOverBudget)
	spent.Failure = &objects.Failure{Code: "budget_exhausted"}
	ended := rollout("c.smoke", objects.RolloutSmoke, objects.ExecutorExternal)
	ended.State = objects.RolloutEnded
	c := Cell{ID: "c", State: objects.StateRunning, N: 3, Rollouts: []objects.Rollout{ended, submitted("c.main", objects.RolloutMain)}, Trials: []objects.Trial{spent}}
	s := Advance(c, spent)
	if s.State != objects.StateOverBudget || !s.CancelOpen || !slices.Equal(s.Ended, []string{"c.main"}) || s.Reason != "budget_exhausted: t2: budget_exhausted" {
		t.Fatalf("%+v", s)
	}
	spent.Failure = nil
	if s := Advance(c, spent); s.Reason != "budget_exhausted: t2: " {
		t.Fatalf("no failure: %q", s.Reason)
	}
}

func TestRetryOfAParkedCell(t *testing.T) {
	failed := trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeInfraFailed)
	blocked := Cell{ID: "c", State: objects.StateBlocked, N: 3, Rollouts: []objects.Rollout{rollout("c.smoke", objects.RolloutSmoke, objects.ExecutorExternal)}, Trials: []objects.Trial{failed}}
	s, ok := Retry(blocked)
	if !ok || s.State != objects.StateSmoking || s.Queue[0].Rollout.Key != "c.smoke.1" || s.Queue[0].Trials[0].RetryOf != "t1" || s.Queue[0].Trials[0].Auto {
		t.Fatalf("smoke again: %+v", s)
	}
	// A cell parked after its smoke runs again each index without a
	// finished trial.
	parked := Cell{ID: "c", State: objects.StateOverBudget, N: 3,
		Rollouts: []objects.Rollout{rollout("c.smoke", objects.RolloutSmoke, objects.ExecutorExternal), rollout("c.main", objects.RolloutMain, objects.ExecutorExternal)},
		Trials: []objects.Trial{
			trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted),
			trial("t2", 1, "c.main", objects.StateDone, objects.OutcomeOverBudget),
			trial("t3", 2, "c.main", objects.StateDone, objects.OutcomeCanceled),
		}}
	s, ok = Retry(parked)
	if !ok || s.State != objects.StateRunning || s.Queue[0].Rollout.Key != "c.retry.1" || len(s.Queue[0].Trials) != 2 || !slices.Equal(s.Retried, []string{"t2", "t3"}) {
		t.Fatalf("after smoke: %+v", s)
	}
	// Nothing left to run completes the cell.
	parked.Trials = parked.Trials[:1]
	parked.N = 1
	if s, ok = Retry(parked); !ok || s.State != objects.StateComplete || len(s.Queue) != 0 {
		t.Fatalf("nothing left: %+v", s)
	}
	if _, ok := Retry(Cell{State: objects.StateRunning}); ok {
		t.Fatal("a running cell is not parked")
	}
	if r := Smoke("c", 5, objects.ExecutorExternal); r.Rollout.Key != "c.smoke" || len(r.Trials) != limits.SmokeTrials {
		t.Fatalf("smoke %+v", r)
	}
}

// TestRolloutsKeepTheCellsExecutor: a cell's smoke rollout runs on its
// candidate's executor, and its retries, its main rollout and the rollouts
// of a retry of the cell run on the executor its first rollout names.
func TestRolloutsKeepTheCellsExecutor(t *testing.T) {
	smoke := Smoke("c", 3, objects.ExecutorTopos)
	if smoke.Rollout.Executor != objects.ExecutorTopos || smoke.Rollout.Handle != "c.smoke" {
		t.Fatalf("smoke %+v", smoke.Rollout)
	}
	sub := smoke.Rollout
	sub.State = objects.RolloutSubmitted
	failed := trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeInfraFailed)
	s := Advance(Cell{ID: "c", State: objects.StateSmoking, N: 3, Rollouts: []objects.Rollout{sub}, Trials: []objects.Trial{failed}}, failed)
	if len(s.Queue) != 1 || s.Queue[0].Rollout.Executor != objects.ExecutorTopos {
		t.Fatalf("the retry of a topos cell: %+v", s.Queue)
	}
	done := trial("t2", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted)
	s = Advance(Cell{ID: "c", State: objects.StateSmoking, N: 3, Rollouts: []objects.Rollout{sub}, Trials: []objects.Trial{done}}, done)
	if len(s.Queue) != 1 || s.Queue[0].Rollout.Kind != objects.RolloutMain || s.Queue[0].Rollout.Executor != objects.ExecutorTopos {
		t.Fatalf("the main rollout of a topos cell: %+v", s.Queue)
	}
	r, ok := Retry(Cell{ID: "c", State: objects.StateBlocked, N: 3, Rollouts: []objects.Rollout{sub}, Trials: []objects.Trial{failed}})
	if !ok || len(r.Queue) != 1 || r.Queue[0].Rollout.Executor != objects.ExecutorTopos {
		t.Fatalf("a retry of a topos cell: %+v", r.Queue)
	}
	if executorOf(Cell{}) != objects.ExecutorExternal {
		t.Fatal("a cell with no rollout runs on the external executor")
	}
}

// TestSmokeChecksGateTheCell: a smoke trial's checks gate its cell
// (specs/005-execution.md): the main rollout waits while one runs, and a
// check that could not do its job on a smoke trial blocks the cell, so the
// main rollout spends nothing on checks that would fail the same way.
func TestSmokeChecksGateTheCell(t *testing.T) {
	smokeTrial := trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted)
	c := Cell{ID: "c", State: objects.StateSmoking, N: 3, Rollouts: []objects.Rollout{submitted("c.smoke", objects.RolloutSmoke)}, Trials: []objects.Trial{smokeTrial},
		Ungraded: map[string]bool{"t1": true}, Checking: map[string]bool{"t1": true}}
	// A cell of smoke trials alone has no main rollout to hold, and grades.
	one := c
	one.N = 1
	if s := Advance(one, smokeTrial); s.State != objects.StateGrading {
		t.Fatalf("one trial with its checks open: %+v", s)
	}
	if s := Advance(c, smokeTrial); len(s.Queue) != 0 || s.State != "" {
		t.Fatalf("the main rollout went ahead of the smoke trial's checks: %+v", s)
	}
	c.Checking, c.CheckFailed = nil, map[string]string{"t1": "sha256:run: not_runnable"}
	if s := Graded(c); s.State != objects.StateBlocked || s.Reason != "smoke_check_failed: t1: sha256:run: not_runnable" {
		t.Fatalf("a smoke check that could not run: %+v", s)
	}
	// A judgment still open holds no main rollout: only checks gate.
	c.CheckFailed = nil
	if s := Graded(c); s.State != objects.StateRunning || len(s.Queue) != 1 {
		t.Fatalf("checks done, a judgment open: %+v", s)
	}
}

// TestACellIsGradingUntilEveryGradingEnds: a cell whose trials are all
// done is grading while a check run or a judgment of them is open, and
// complete once every one ended.
func TestACellIsGradingUntilEveryGradingEnds(t *testing.T) {
	done := trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted)
	c := Cell{ID: "c", State: objects.StateSmoking, N: 1, Rollouts: []objects.Rollout{submitted("c.smoke", objects.RolloutSmoke)}, Trials: []objects.Trial{done},
		Ungraded: map[string]bool{"t1": true}}
	if s := Advance(c, done); s.State != objects.StateGrading {
		t.Fatalf("an ungraded cell: %+v", s)
	}
	ended := rollout("c.smoke", objects.RolloutSmoke, objects.ExecutorTopos)
	ended.State = objects.RolloutEnded
	c.State, c.Rollouts = objects.StateGrading, []objects.Rollout{ended}
	if s := Graded(c); s.Changed() {
		t.Fatalf("still grading: %+v", s)
	}
	c.Ungraded = nil
	if s := Graded(c); s.State != objects.StateComplete {
		t.Fatalf("every grading ended: %+v", s)
	}
}
