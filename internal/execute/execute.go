// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package execute holds the decisions of specs/005-execution.md that read
// state and write none: how an experiment's parallelism is dealt to its
// cells, what a cell does when one of its trials reaches done (retries of
// infrastructure failures with their lineage, the smoke gate and the cost
// check, completion), and what a retry of a parked cell runs. The store
// applies each decision in the transaction that read the state it was made
// from, beside the events it journals.
package execute

import (
	"cmp"
	"slices"
	"strconv"

	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
)

// Load is what one cell holds of an experiment's parallelism: its place in
// the deal and its trials not yet done in submitted rollouts.
type Load struct {
	Cell         string
	TaskPos      int
	CandidatePos int
	Unfinished   int
}

// Deal deals an experiment's parallelism to its cells one trial at a
// time: tasks in the suite's order, and within a task round robin across
// its cells in the experiment's candidate order, until the parallelism is
// dealt or no cell has an unfinished trial left. A cell's share is what it
// was dealt.
func Deal(parallelism int, loads []Load) map[string]int {
	ordered := slices.Clone(loads)
	slices.SortFunc(ordered, func(a, b Load) int {
		return cmp.Or(cmp.Compare(a.TaskPos, b.TaskPos), cmp.Compare(a.CandidatePos, b.CandidatePos))
	})
	shares := map[string]int{}
	left := parallelism
	for i := 0; i < len(ordered) && left > 0; {
		// One task's cells, dealt round robin.
		j := i
		for j < len(ordered) && ordered[j].TaskPos == ordered[i].TaskPos {
			j++
		}
		for dealt := true; dealt && left > 0; {
			dealt = false
			for _, l := range ordered[i:j] {
				if left > 0 && shares[l.Cell] < l.Unfinished {
					shares[l.Cell]++
					left--
					dealt = true
				}
			}
		}
		i = j
	}
	return shares
}

// Unfinished is one trial of a submitted rollout that is not yet done: its
// cell, the cell's place in the deal, its rollout's rank (smoke 0, retry 1,
// main 2) and its index, and whether it is held, leased or checking, and so
// already counts against the parallelism.
type Unfinished struct {
	Cell         string
	TaskPos      int
	CandidatePos int
	Rank         int
	Index        int
	Held         bool
}

// Before orders two unfinished trials the way an experiment's scheduling
// deals and leases them. Under task_by_task a task's trials come before
// the next task's, and within a task a cell's smoke trials, then its
// retries, then its main trials, each by index, the task's cells taking
// turns trial by trial. Under interleaved every cell of every task takes
// its turn: smoke trials, then retries, then main trials, each by index,
// across every cell in the suite's task order and the experiment's
// candidate order.
func Before(scheduling string, a, b Unfinished) int {
	if scheduling == objects.SchedulingInterleaved {
		return cmp.Or(cmp.Compare(a.Rank, b.Rank), cmp.Compare(a.Index, b.Index),
			cmp.Compare(a.TaskPos, b.TaskPos), cmp.Compare(a.CandidatePos, b.CandidatePos))
	}
	return cmp.Or(cmp.Compare(a.TaskPos, b.TaskPos), cmp.Compare(a.Rank, b.Rank), cmp.Compare(a.Index, b.Index),
		cmp.Compare(a.CandidatePos, b.CandidatePos))
}

// DealInterleaved deals an experiment's parallelism under interleaved
// scheduling: every held trial keeps its place, and the rest of the
// parallelism goes to the pending trials in the order Before gives, so
// every cell of every task takes one trial before any takes its next. A
// cell's share is its held trials and the pending ones dealt to it. When
// the held trials already fill the parallelism, as after it was lowered,
// nothing more is dealt.
func DealInterleaved(parallelism int, trials []Unfinished) map[string]int {
	shares := map[string]int{}
	var pending []Unfinished
	for _, t := range trials {
		if t.Held {
			shares[t.Cell]++
			parallelism--
			continue
		}
		pending = append(pending, t)
	}
	slices.SortStableFunc(pending, func(a, b Unfinished) int { return Before(objects.SchedulingInterleaved, a, b) })
	for _, t := range pending[:max(0, min(parallelism, len(pending)))] {
		shares[t.Cell]++
	}
	return shares
}

// Cell is what a decision reads of one cell: its state, the trials it
// declares, its rollouts and every trial it holds.
type Cell struct {
	ID       string
	State    string
	N        int
	Rollouts []objects.Rollout
	Trials   []objects.Trial
}

// NewTrial is a trial a decision mints: its index, the trial it retries,
// and whether it is an automatic retry of an infrastructure failure.
type NewTrial struct {
	Index   int
	RetryOf string
	Auto    bool
}

// NewRollout is a rollout a decision queues, with the trials it runs.
type NewRollout struct {
	Rollout objects.Rollout
	Trials  []NewTrial
}

// Step is what a cell does: its new state and parked reason, the rollouts
// that ended, the rollouts it queues, the trials a new trial retries, and
// whether its trials not yet done end canceled.
type Step struct {
	State      string
	Reason     string
	Ended      []string
	Queue      []NewRollout
	Retried    []string
	CancelOpen bool
}

// Changed reports whether the step changes anything.
func (s Step) Changed() bool {
	return s.State != "" || len(s.Ended) > 0 || len(s.Queue) > 0 || len(s.Retried) > 0 || s.CancelOpen
}

// smoke is how many of a cell's indices are smoke trials.
func smoke(n int) int { return min(n, limits.SmokeTrials) }

// last is, per index, the latest attempt: the one no later trial retries.
func last(trials []objects.Trial, retried map[string]bool) map[int]objects.Trial {
	out := map[int]objects.Trial{}
	for _, t := range trials {
		if t.Retried || retried[t.ID] {
			continue
		}
		if prev, ok := out[t.Index]; !ok || t.ID > prev.ID {
			out[t.Index] = t
		}
	}
	return out
}

// rolloutOpen reports whether a rollout has a trial not yet done.
func rolloutOpen(key string, trials []objects.Trial) bool {
	return slices.ContainsFunc(trials, func(t objects.Trial) bool { return t.Rollout == key && t.State != objects.StateDone })
}

// count is how many rollouts of a kind a cell has had.
func count(rollouts []objects.Rollout, kind string) int {
	n := 0
	for _, r := range rollouts {
		if r.Kind == kind {
			n++
		}
	}
	return n
}

// retriesUsed is how many automatic retries the chain ending in t holds.
func retriesUsed(t objects.Trial, byID map[string]objects.Trial) int {
	n := 0
	for t.AutoRetry {
		n++
		prev, ok := byID[t.RetryOf]
		if !ok {
			break
		}
		t = prev
	}
	return n
}

// rollout is a queued rollout of the external executor.
func rollout(key, kind string) objects.Rollout {
	return objects.Rollout{Key: key, Kind: kind, Executor: objects.ExecutorExternal, Handle: key, Parallelism: 1, State: objects.RolloutQueued}
}

// Advance decides what a cell does once one of its trials, just, reached
// done (specs/005-execution.md). An over_budget trial parks the cell and
// ends its open trials canceled. A rollout whose every trial is done
// ends, and each of its infra_failed trials whose index has had fewer than
// MAX_INFRA_RETRIES automatic retries gets a new trial in the cell's next
// retry rollout. A smoking cell whose last attempt at every smoke index is
// done parks blocked when one ended infra_failed, and otherwise passes its
// cost check and queues its main rollout: the estimate is the smoke
// trials' grading cost plus the cost Lux holds of them, and no grader of
// this release spends and no Lux record is read, so it is zero, which no
// remainder is below. A cell with no rollout open or queued is graded and
// complete; its graders, the built-ins, ran with each trial's done.
func Advance(c Cell, just objects.Trial) Step {
	var s Step
	if objects.Parked(c.State) || c.State == objects.StateComplete {
		return s
	}
	if just.Outcome == objects.OutcomeOverBudget {
		code := ""
		if just.Failure != nil {
			code = just.Failure.Code
		}
		s.State, s.CancelOpen = objects.StateOverBudget, true
		s.Reason = "budget_exhausted: " + just.ID + ": " + code
		for _, r := range c.Rollouts {
			if r.State != objects.RolloutEnded {
				s.Ended = append(s.Ended, r.Key)
			}
		}
		return s
	}
	byID := map[string]objects.Trial{}
	for _, t := range c.Trials {
		byID[t.ID] = t
	}
	retried := map[string]bool{}
	var retries []NewTrial
	for _, r := range c.Rollouts {
		if r.State != objects.RolloutSubmitted || rolloutOpen(r.Key, c.Trials) {
			continue
		}
		s.Ended = append(s.Ended, r.Key)
		for _, t := range c.Trials {
			if t.Rollout == r.Key && t.Outcome == objects.OutcomeInfraFailed && !t.Retried && retriesUsed(t, byID) < limits.MaxInfraRetries {
				retries = append(retries, NewTrial{Index: t.Index, RetryOf: t.ID, Auto: true})
				retried[t.ID] = true
				s.Retried = append(s.Retried, t.ID)
			}
		}
	}
	rollouts := slices.Clone(c.Rollouts)
	if len(retries) > 0 {
		key := c.ID + ".retry." + strconv.Itoa(count(c.Rollouts, objects.RolloutRetry)+1)
		r := NewRollout{Rollout: rollout(key, objects.RolloutRetry), Trials: retries}
		s.Queue = append(s.Queue, r)
		rollouts = append(rollouts, r.Rollout)
	}
	open := func() bool {
		for _, r := range rollouts {
			if r.State == objects.RolloutQueued || (r.State == objects.RolloutSubmitted && !slices.Contains(s.Ended, r.Key)) {
				return true
			}
		}
		return false
	}
	switch c.State {
	case objects.StateSmoking:
		attempts := last(c.Trials, retried)
		for i := range smoke(c.N) {
			t, ok := attempts[i]
			if !ok || t.State != objects.StateDone {
				return s
			}
		}
		for i := range smoke(c.N) {
			if t := attempts[i]; t.Outcome == objects.OutcomeInfraFailed {
				code := ""
				if t.Failure != nil {
					code = t.Failure.Code
				}
				s.State, s.Reason = objects.StateBlocked, "smoke_infra_failed: "+t.ID+": "+code
				return s
			}
		}
		if c.N > smoke(c.N) && count(c.Rollouts, objects.RolloutMain) == 0 {
			main := NewRollout{Rollout: rollout(c.ID+".main", objects.RolloutMain)}
			for i := smoke(c.N); i < c.N; i++ {
				main.Trials = append(main.Trials, NewTrial{Index: i})
			}
			s.Queue = append(s.Queue, main)
			s.State = objects.StateRunning
			return s
		}
		if !open() {
			s.State = objects.StateComplete
		}
	case objects.StateRunning:
		if !open() {
			s.State = objects.StateComplete
		}
	}
	return s
}

// Retry decides what a retry of a cell parked blocked or over_budget runs,
// and false for a cell in any other state. A cell that never queued its
// main rollout runs its smoke trials again, each a new trial retrying the
// last attempt, in the rollout <cell>.smoke.<round>. Any other passes its
// cost check again, zero as at Advance, and runs again every index whose
// last attempt is not a finished trial, in its next retry rollout. The
// trials a retry adds are not automatic retries.
func Retry(c Cell) (Step, bool) {
	var s Step
	if c.State != objects.StateBlocked && c.State != objects.StateOverBudget {
		return s, false
	}
	attempts := last(c.Trials, nil)
	if count(c.Rollouts, objects.RolloutMain) == 0 {
		round := count(c.Rollouts, objects.RolloutSmoke)
		r := NewRollout{Rollout: rollout(c.ID+".smoke."+strconv.Itoa(round), objects.RolloutSmoke)}
		for i := range smoke(c.N) {
			nt := NewTrial{Index: i}
			if t, ok := attempts[i]; ok {
				nt.RetryOf = t.ID
				s.Retried = append(s.Retried, t.ID)
			}
			r.Trials = append(r.Trials, nt)
		}
		s.Queue, s.State = []NewRollout{r}, objects.StateSmoking
		return s, true
	}
	r := NewRollout{Rollout: rollout(c.ID+".retry."+strconv.Itoa(count(c.Rollouts, objects.RolloutRetry)+1), objects.RolloutRetry)}
	for i := range c.N {
		t, ok := attempts[i]
		if ok && t.Finished() {
			continue
		}
		nt := NewTrial{Index: i}
		if ok {
			nt.RetryOf = t.ID
			s.Retried = append(s.Retried, t.ID)
		}
		r.Trials = append(r.Trials, nt)
	}
	if len(r.Trials) == 0 {
		s.State = objects.StateComplete
		return s, true
	}
	s.Queue, s.State = []NewRollout{r}, objects.StateRunning
	return s, true
}

// Smoke is the smoke rollout an experiment's cell starts with, and its
// trials.
func Smoke(cell string, n int) NewRollout {
	r := NewRollout{Rollout: rollout(cell+".smoke", objects.RolloutSmoke)}
	for i := range smoke(n) {
		r.Trials = append(r.Trials, NewTrial{Index: i})
	}
	return r
}
