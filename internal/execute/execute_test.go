// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package execute

import (
	"maps"
	"slices"
	"testing"

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

// trial builds a trial of a rollout.
func trial(id string, index int, rollout, state, outcome string) objects.Trial {
	return objects.Trial{ID: id, Index: index, Rollout: rollout, State: state, Outcome: outcome}
}

func submitted(key, kind string) objects.Rollout {
	r := rollout(key, kind)
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
	rollouts := []objects.Rollout{rollout("c.smoke", objects.RolloutSmoke)}
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

func TestRunningCellCompletes(t *testing.T) {
	done := []objects.Trial{
		trial("t1", 0, "c.smoke", objects.StateDone, objects.OutcomeCompleted),
		trial("t2", 1, "c.main", objects.StateDone, objects.OutcomeTimedOut),
		trial("t3", 2, "c.main", objects.StateDone, objects.OutcomeDeclined),
	}
	ended := rollout("c.smoke", objects.RolloutSmoke)
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
	ended := rollout("c.smoke", objects.RolloutSmoke)
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
	blocked := Cell{ID: "c", State: objects.StateBlocked, N: 3, Rollouts: []objects.Rollout{rollout("c.smoke", objects.RolloutSmoke)}, Trials: []objects.Trial{failed}}
	s, ok := Retry(blocked)
	if !ok || s.State != objects.StateSmoking || s.Queue[0].Rollout.Key != "c.smoke.1" || s.Queue[0].Trials[0].RetryOf != "t1" || s.Queue[0].Trials[0].Auto {
		t.Fatalf("smoke again: %+v", s)
	}
	// A cell parked after its smoke runs again each index without a
	// finished trial.
	parked := Cell{ID: "c", State: objects.StateOverBudget, N: 3,
		Rollouts: []objects.Rollout{rollout("c.smoke", objects.RolloutSmoke), rollout("c.main", objects.RolloutMain)},
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
	if r := Smoke("c", 5); r.Rollout.Key != "c.smoke" || len(r.Trials) != limits.SmokeTrials {
		t.Fatalf("smoke %+v", r)
	}
}
