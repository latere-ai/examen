// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"bytes"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/topos"
	"latere.ai/x/examen/internal/topos/topostest"
)

// gradingHeapBudget is how much more live heap grading a run may hold
// than before it started, whatever the size of the run's kept files.
const gradingHeapBudget = 16 << 20

// largeWork is the shape of the run whose grading ended an examend
// replica of 256 MiB: 225 files and about 42 MB, most of it text, a
// binary among them. The bytes are made once and served from memory, so
// they sit in the heap before grading starts and count toward no growth.
func largeWork() map[string][]byte {
	out := map[string][]byte{}
	for i := range 200 {
		out[fmt.Sprintf("notes/%03d.md", i)] = bytes.Repeat([]byte("a note line\n"), 340)
	}
	for i := range 24 {
		out[fmt.Sprintf("data/%02d.csv", i)] = bytes.Repeat([]byte("1,2,3,4,5,6,7,8\n"), 105_000)
	}
	out["data/model.bin"] = bytes.Repeat([]byte{0, 1, 2, 3}, 512<<10)
	return out
}

// runLargeSession drives the stub's one session to the end of a run that
// left largeWork in its folder.
func runLargeSession(work map[string][]byte) func(stub *topostest.Stub, fs *files, sid string) {
	return func(stub *topostest.Stub, fs *files, sid string) {
		at := time.Date(2026, 10, 22, 5, 40, 0, 0, time.UTC)
		var written []topos.WorkFile
		for p, b := range work {
			fs.put("files/conversations/"+sid+"/work/"+p, b)
			written = append(written, topos.WorkFile{Path: p, Size: int64(len(b)), Mode: 0o644, SHA256: sha(b)})
		}
		sort.Slice(written, func(i, j int) bool { return written[i].Path < written[j].Path })
		stub.Set(sid, topos.StatusEnded, topos.StopCompleted,
			topostest.Event(topos.EventSessionStatus, "", at, map[string]any{"status": "running"}),
			topostest.Event(topos.EventWorkSaved, "", at.Add(time.Minute), map[string]any{"covers": 2, "written": written}),
			topostest.Event(topos.EventAgentMessage, "", at.Add(2*time.Minute), map[string]any{"stop_reason": "end_turn",
				"message": map[string]any{"role": "assistant", "blocks": []map[string]any{{"type": "text", "text": "I wrote notes and data."}}}}),
			topostest.Event(topos.EventSessionStatus, "", at.Add(3*time.Minute), map[string]any{"status": "ended", "stop_reason": topos.StopCompleted}),
		)
	}
}

// liveHeap samples the live heap, collected first, until stop is closed,
// and answers the most it saw.
func liveHeap(stop <-chan struct{}) <-chan uint64 {
	out := make(chan uint64, 1)
	go func() {
		var peak uint64
		var m runtime.MemStats
		for {
			runtime.GC()
			runtime.ReadMemStats(&m)
			peak = max(peak, m.HeapAlloc)
			select {
			case <-stop:
				out <- peak
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	return out
}

func heapNow() uint64 {
	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// TestGradingHoldsNoRunWhole: grading the run whose files once ran an
// examend replica out of memory, its checks and its judge, holds no more
// than gradingHeapBudget of live heap over what it held before, though the
// run kept about 42 MB. The check sandbox receives every kept file with
// its bytes intact; the judge reads the files its bound reaches and no
// more, and lists the rest.
func TestGradingHoldsNoRunWhole(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	work := largeWork()
	var total int64
	for _, b := range work {
		total += int64(len(b))
	}
	if total < 40<<20 || len(work) != 225 {
		t.Fatalf("the run keeps %d bytes in %d files", total, len(work))
	}
	b := newBenchOf(t, st, runLargeSession(work))
	b.cl.stream = true
	b.cl.answers[b.g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 3}}`}
	b.cl.answers[b.g.runner] = cella.Result{Stdout: `{"metrics": {"runs": true}}`}

	before := heapNow()
	stop := make(chan struct{})
	peak := liveHeap(stop)
	b.grade(t)
	b.fs.mu.Lock()
	checkReads := b.fs.reads
	b.fs.mu.Unlock()
	b.grade(t)
	close(stop)
	grew := int64(<-peak) - int64(before)
	t.Logf("grading a run of %d files and %d bytes grew the live heap by at most %d bytes", len(work), total, grew)

	list := b.gradings(t)
	for _, g := range list {
		if g.State == objects.GradingPending {
			t.Fatalf("a grading did not end: %+v", g)
		}
	}
	if j := record[objects.Judgment](t, list[len(list)-1]); j.State != objects.JudgmentOK || len(j.Cuts) == 0 {
		t.Fatalf("the judgment %+v", j)
	}
	if grew > gradingHeapBudget {
		t.Fatalf("grading held %d bytes of live heap more than before; the budget is %d", grew, gradingHeapBudget)
	}

	// Every kept file reached the check sandbox with its bytes.
	var wantFiles []string
	for p, data := range work {
		wantFiles = append(wantFiles, p)
		got := b.cl.received["examen/outputs/workspace/"+p]
		if got != strconv.Itoa(len(data))+" "+sha(data) {
			t.Fatalf("%s reached the sandbox as %q", p, got)
		}
	}
	if checkReads < len(wantFiles) {
		t.Fatalf("the checks read %d of %d files", checkReads, len(wantFiles))
	}
	// The judge read only the files its bound reached.
	b.fs.mu.Lock()
	judgeReads := b.fs.reads - checkReads
	b.fs.mu.Unlock()
	if judgeReads == 0 || judgeReads >= len(work)/2 {
		t.Fatalf("the judge read %d of %d files", judgeReads, len(work))
	}
	b.lx.mu.Lock()
	call := fmt.Sprint(b.lx.calls[len(b.lx.calls)-1])
	b.lx.mu.Unlock()
	if !strings.Contains(call, "bytes elided") || len(call) > 2*limits.JudgeMaxInputBytes {
		t.Fatalf("the judge's call is %d bytes and says nothing was left out", len(call))
	}
}

// TestTwoLargeRunsGradedAtOnce: two runs of that shape graded by one
// replica at once still hold within twice the budget, since nothing of a
// run is held whole.
func TestTwoLargeRunsGradedAtOnce(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	work := largeWork()
	var benches []*bench
	for range 2 {
		b := newBenchOf(t, st, runLargeSession(work))
		b.cl.stream = true
		b.cl.answers[b.g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 3}}`}
		benches = append(benches, b)
	}
	before := heapNow()
	stop := make(chan struct{})
	peak := liveHeap(stop)
	var wg sync.WaitGroup
	for _, b := range benches {
		wg.Go(func() {
			for range 2 {
				if err := b.e.Grade(t.Context()); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	grew := int64(<-peak) - int64(before)
	t.Logf("grading two such runs at once grew the live heap by at most %d bytes", grew)
	for _, b := range benches {
		for _, g := range b.gradings(t) {
			if g.State == objects.GradingPending {
				t.Fatalf("a grading did not end: %+v", g)
			}
		}
	}
	if grew > 2*gradingHeapBudget {
		t.Fatalf("two gradings at once held %d bytes more than before; the budget is %d", grew, 2*gradingHeapBudget)
	}
}
