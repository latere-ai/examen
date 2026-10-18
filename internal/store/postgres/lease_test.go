// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"slices"
	"testing"

	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// grid is an experiment of two tasks and two external candidates, its four
// cells in the deal's order: task 0 with candidates a and b, then task 1.
type grid struct {
	experiment string
	cells      []string // t0a, t0b, t1a, t1b
	byCell     map[string]string
}

// grid stores an experiment of two tasks and the plan's two candidates
// under a scheduling and a parallelism, each cell with one rollout of n
// trials, indices 0 to n-1, submitted when its cell is in submit.
func (w *world) grid(ctx context.Context, scheduling string, parallelism, n int, submit func(cell int) bool) grid {
	w.t.Helper()
	p := w.plan()
	two := w.put(objects.KindTask, "two", objects.Task{Name: "two", Instruction: "do it again"})
	suite := w.put(objects.KindSuite, "s2", objects.Suite{Name: "s2", Tasks: []string{p.task.Digest, two.Digest}, Metrics: []string{"builtin:completed"}, PrimaryMetric: "builtin:completed"})
	e := objects.Experiment{ID: id.New(id.Experiment), Owner: owner, Suite: suite.Digest, SuiteID: suite.ID, SuiteName: "s2",
		Candidates: []string{p.a.Digest, p.b.Digest}, CandidateIDs: []string{p.a.ID, p.b.ID}, Trials: n, Budget: objects.Budget{Name: "b"},
		Parallelism: parallelism, Reference: objects.Reference{Candidate: p.a.Digest}, Mode: objects.ModeExecute, Scheduling: scheduling, CreatedBy: owner}
	ne := store.NewExperiment{Experiment: e, MaxActive: 1000}
	g := grid{experiment: e.ID, byCell: map[string]string{}}
	names := []string{"t0a", "t0b", "t1a", "t1b"}
	for ti, task := range []objects.Version{p.task, two} {
		for ci, c := range []objects.Version{p.a, p.b} {
			cell := objects.Cell{ID: id.New(id.Cell), Task: task.Digest, TaskID: task.ID, Candidate: c.Digest, CandidateID: c.ID, TaskPos: ti, CandidatePos: ci}
			r := &store.Rollout{Rollout: objects.Rollout{Key: cell.ID + ".smoke", Kind: objects.RolloutSmoke, Executor: objects.ExecutorExternal, State: objects.RolloutQueued}}
			for i := range n {
				r.Trials = append(r.Trials, store.RolloutTrial{Index: i})
			}
			ne.Cells = append(ne.Cells, store.NewCell{Cell: cell, Smoke: r})
			g.cells = append(g.cells, cell.ID)
			g.byCell[cell.ID] = names[len(g.cells)-1]
		}
	}
	if err := w.st.Experiments().Create(ctx, ne); err != nil {
		w.t.Fatal(err)
	}
	for i, c := range g.cells {
		if submit == nil || submit(i) {
			w.submitRollout(ctx, c)
		}
	}
	return g
}

func (w *world) submitRollout(ctx context.Context, cell string) {
	w.t.Helper()
	if err := w.st.Experiments().SubmitRollout(ctx, cell, cell+".smoke"); err != nil {
		w.t.Fatal(err)
	}
}

// lease leases trials of the grid's experiment, of a candidate or of every
// one, and names each by its cell and index, in the lease's order.
func (w *world) lease(ctx context.Context, g grid, candidate string, max int) (store.Leased, []string) {
	w.t.Helper()
	out, err := w.st.Experiments().Lease(ctx, store.LeaseRequest{Experiment: g.experiment, Candidate: candidate, Subject: owner, Max: max, Cells: g.cells})
	if err != nil {
		w.t.Fatal(err)
	}
	var got []string
	for _, t := range out.Trials {
		got = append(got, g.byCell[t.Cell]+":"+string(rune('0'+t.Index)))
	}
	return out, got
}

// Under interleaved scheduling every cell of every task takes one trial
// before any takes its next: the parallelism of four goes to trial 0 of
// each of the four cells, and the slot a finished trial frees goes to the
// next trial in that order, trial 1 of the first cell, not to the rest of
// the first task.
func TestLeaseInterleavesAcrossTasks(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	g := w.grid(ctx, objects.SchedulingInterleaved, 4, 3, nil)
	first, got := w.lease(ctx, g, "", 0)
	if want := []string{"t0a:0", "t0b:0", "t1a:0", "t1b:0"}; !slices.Equal(got, want) {
		t.Fatalf("leased %v, want %v", got, want)
	}
	if _, got := w.lease(ctx, g, "", 0); len(got) != 0 {
		t.Fatalf("the parallelism is held, and a lease took %v", got)
	}
	if _, err := w.st.Experiments().Submit(ctx, store.Submission{Trial: first.Trials[0].ID, Lease: first.Lease, Subject: owner, Digest: "d", Outcome: outcome(objects.OutcomeCompleted)}); err != nil {
		t.Fatal(err)
	}
	if _, got := w.lease(ctx, g, "", 0); !slices.Equal(got, []string{"t0a:1"}) {
		t.Fatalf("the freed slot went to %v, want t0a:1", got)
	}
}

// A lease that names no candidate takes trials of every candidate in the
// scheduling's order: under task_by_task, the first task's cells take the
// parallelism, trial by trial, before the second task's take any; one
// trial at a time follows the same order; and a lease naming a candidate
// takes that candidate's alone.
func TestLeaseWithoutCandidate(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	g := w.grid(ctx, objects.SchedulingTaskByTask, 4, 3, nil)
	var order []string
	for range 4 {
		_, got := w.lease(ctx, g, "", 1)
		order = append(order, got...)
	}
	if want := []string{"t0a:0", "t0b:0", "t0a:1", "t0b:1"}; !slices.Equal(order, want) {
		t.Fatalf("one at a time leased %v, want %v", order, want)
	}

	w2 := newWorld(t)
	g2 := w2.grid(ctx, objects.SchedulingInterleaved, 8, 2, nil)
	p := w2.cellCandidate(ctx, g2, 1)
	if _, got := w2.lease(ctx, g2, p, 0); !slices.Equal(got, []string{"t0b:0", "t1b:0", "t0b:1", "t1b:1"}) {
		t.Fatalf("a lease naming candidate b leased %v", got)
	}
	if _, got := w2.lease(ctx, g2, "", 0); !slices.Equal(got, []string{"t0a:0", "t1a:0", "t0a:1", "t1a:1"}) {
		t.Fatalf("a lease naming none leased %v", got)
	}
}

// cellCandidate is the candidate digest of one of the grid's cells.
func (w *world) cellCandidate(ctx context.Context, g grid, i int) string {
	w.t.Helper()
	c, err := w.st.Experiments().Cell(ctx, g.cells[i])
	if err != nil {
		w.t.Fatal(err)
	}
	return c.Candidate
}

// A lease never holds more trials at once than the experiment's
// parallelism: when trials of a later task are already held and an earlier
// task's rollout opens, the deal gives the earlier task the shares, and
// the lease still takes nothing until a held trial ends.
func TestLeaseHoldsTheParallelism(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	g := w.grid(ctx, objects.SchedulingTaskByTask, 2, 3, func(cell int) bool { return cell >= 2 })
	held, got := w.lease(ctx, g, "", 0)
	if !slices.Equal(got, []string{"t1a:0", "t1b:0"}) {
		t.Fatalf("leased %v", got)
	}
	w.submitRollout(ctx, g.cells[0])
	if _, got := w.lease(ctx, g, "", 0); len(got) != 0 {
		t.Fatalf("with the parallelism held, a lease took %v", got)
	}
	if _, err := w.st.Experiments().Submit(ctx, store.Submission{Trial: held.Trials[0].ID, Lease: held.Lease, Subject: owner, Digest: "d", Outcome: outcome(objects.OutcomeCompleted)}); err != nil {
		t.Fatal(err)
	}
	if _, got := w.lease(ctx, g, "", 0); !slices.Equal(got, []string{"t0a:0"}) {
		t.Fatalf("the freed slot went to %v, want t0a:0", got)
	}
}
