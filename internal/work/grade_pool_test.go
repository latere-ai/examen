// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"context"
	"net/http"
	"slices"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// queue is a store whose gradings are a list of judgments: due until
// ended, and skipped by Due while held.
type queue struct {
	store.Store
	mu    sync.Mutex
	rows  []objects.Grading
	held  map[string]bool
	ended map[string]bool
}

func (q *queue) Gradings() store.Gradings { return gradingsOf{q: q} }

// gradingsOf is the queue as the store's gradings; what the pool never calls
// is left to the nil interface.
type gradingsOf struct {
	store.Gradings
	q *queue
}

func (g gradingsOf) Due(ctx context.Context, limit int) ([]objects.Grading, error) {
	return g.q.due(limit), nil
}

func (g gradingsOf) Hold(_ context.Context, ids []string, _ time.Duration) ([]string, error) {
	g.q.hold(ids)
	return ids, nil
}

func (q *queue) add(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rows = append(q.rows, objects.Grading{ID: id, Trial: "trial-" + id, Kind: objects.GraderModelJudge})
}

func (q *queue) due(limit int) []objects.Grading {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []objects.Grading
	for _, r := range q.rows {
		if !q.held[r.ID] && !q.ended[r.ID] && len(out) < limit {
			out = append(out, r)
		}
	}
	return out
}

func (q *queue) hold(ids []string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range ids {
		q.held[id] = true
	}
}

func (q *queue) isHeld(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.held[id]
}

func (q *queue) end(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ended[id] = true
}

// poolEngine is an engine with two judge workers over a queue whose jobs
// are job, which reports each job's grading id on started.
func poolEngine(workers int, job func(ctx context.Context, id string)) (*Engine, *queue) {
	q := &queue{held: map[string]bool{}, ended: map[string]bool{}}
	e := &Engine{Store: q, Keys: keys.NewMemory()}
	e.Grading = &Grading{Tokens: &minter{}, Arca: arca.New("http://arca.invalid", http.DefaultClient), LuxURL: "http://lux.invalid", Workers: workers}
	e.job = func(ctx context.Context, rows []objects.Grading) error {
		job(ctx, rows[0].ID)
		q.end(rows[0].ID)
		return nil
	}
	return e, q
}

// TestGradeLoopStartsDueGradingsWhileOneRuns proves a slow grading holds up
// no other: with two workers, a grading that blocks does not keep one that
// becomes due on a later tick from starting and ending, and the loop
// returns only once the blocked one ended.
func TestGradeLoopStartsDueGradingsWhileOneRuns(t *testing.T) {
	release := make(chan struct{})
	started := make(chan string, 4)
	ended := make(chan string, 4)
	e, q := poolEngine(2, func(_ context.Context, id string) {
		started <- id
		if id == "slow" {
			<-release
		}
		ended <- id
	})
	q.add("slow")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	returned := make(chan struct{})
	go func() { e.GradeLoop(ctx, time.Millisecond); close(returned) }()

	if id := <-started; id != "slow" {
		t.Fatalf("first grading started = %q, want slow", id)
	}
	// A later tick takes what became due while slow still runs.
	q.add("quick")
	if id := <-started; id != "quick" {
		t.Fatalf("second grading started = %q, want quick", id)
	}
	if id := <-ended; id != "quick" {
		t.Fatalf("first grading ended = %q, want quick while slow runs", id)
	}
	cancel()
	select {
	case <-returned:
		t.Fatal("the loop returned while a grading it started still ran")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	<-returned
	if id := <-ended; id != "slow" {
		t.Fatalf("last grading ended = %q, want slow", id)
	}
}

// TestRoundHoldsNoGradingItCannotStart proves a full pool holds no rows: a
// grading beyond the free workers stays unheld, due for the round that has
// a worker.
func TestRoundHoldsNoGradingItCannotStart(t *testing.T) {
	release := make(chan struct{})
	started := make(chan string, 3)
	e, q := poolEngine(2, func(_ context.Context, id string) {
		started <- id
		<-release
	})
	for _, id := range []string{"a", "b", "c"} {
		q.add(id)
	}
	p := e.newPool(true)
	if err := e.round(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	var got []string
	for range 2 {
		got = append(got, <-started)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("started %v, want a and b", got)
	}
	if q.isHeld("c") {
		t.Fatal("c is held with no worker to run it")
	}
	// Another round with the pool still full takes nothing.
	if err := e.round(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if q.isHeld("c") {
		t.Fatal("a round on a full pool held c")
	}
	close(release)
	p.wg.Wait()
	if err := e.round(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	if id := <-started; id != "c" {
		t.Fatalf("started %q, want c", id)
	}
	p.wg.Wait()
}
