// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// TestDrivenTrials: an executor that starts its runs itself finds the
// experiments with work for it, claims the trials the parallelism deals,
// records each run once by its ref, ends a trial with its run's outcome,
// sent layer and built-in values, and stops the runs of trials a cancel
// ended; a run recorded for a trial that already ended is to be stopped.
func TestDrivenTrials(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	x := w.st.Experiments()
	eid, cells := w.running(ctx, w.plan(), 2)
	if got, err := x.Driven(ctx, objects.ExecutorExternal, 10); err != nil || !slices.Contains(got, eid) {
		t.Fatalf("driven %v %v", got, err)
	}
	if got, err := x.Driven(ctx, objects.ExecutorTopos, 10); err != nil || slices.Contains(got, eid) {
		t.Fatalf("another executor's experiment is driven: %v %v", got, err)
	}
	claimed, err := x.Claim(ctx, eid, cells[:1])
	if err != nil || len(claimed) != 1 || claimed[0].State != objects.StateRunning || claimed[0].Lease != nil {
		t.Fatalf("claimed %+v %v", claimed, err)
	}
	if again, err := x.Claim(ctx, eid, cells[:1]); err != nil || len(again) != 0 {
		t.Fatalf("a second claim took %+v %v", again, err)
	}
	if none, err := x.Claim(ctx, eid, nil); err != nil || len(none) != 0 {
		t.Fatalf("a claim of no cells took %+v %v", none, err)
	}
	tr := claimed[0]
	at := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	if stop, err := x.Started(ctx, tr.ID, "ses_1", at); err != nil || stop {
		t.Fatalf("started: %v %v", stop, err)
	}
	if stop, err := x.Started(ctx, tr.ID, "ses_1", at); err != nil || stop {
		t.Fatalf("started again with the same run: %v %v", stop, err)
	}
	if _, err := x.Started(ctx, tr.ID, "ses_2", at); err == nil || !strings.Contains(err.Error(), "already names the run") {
		t.Fatalf("another run for one trial: %v", err)
	}
	if got, err := x.Trial(ctx, tr.ID); err != nil || got.ExecutorRef != "ses_1" || got.StartedAt == nil || !got.StartedAt.Equal(at) {
		t.Fatalf("the trial %+v %v", got, err)
	}
	o := outcome(objects.OutcomeCompleted)
	o.Ref, o.Settled = "ses_1", true
	o.Sent = &objects.Sent{Requests: 1, BySetting: []objects.SentSetting{{Model: "m", Requests: 1}}}
	o.Builtin = map[string]any{objects.BuiltinDigest(objects.MetricTurns): 3.0}
	ended, err := x.End(ctx, tr.ID, o, true)
	if err != nil || ended.Outcome != objects.OutcomeCompleted || ended.Sent == nil || ended.Sent.Requests != 1 {
		t.Fatalf("ended %+v %v", ended, err)
	}
	if again, err := x.End(ctx, tr.ID, outcome(objects.OutcomeInfraFailed), false); err != nil || again.Outcome != objects.OutcomeCompleted {
		t.Fatalf("a done trial ended again: %+v %v", again, err)
	}
	ms, err := x.Measurements(ctx, store.MeasurementFilter{Trial: tr.ID, Metric: objects.BuiltinDigest(objects.MetricTurns)}, store.Page{Limit: 5})
	if err != nil || len(ms) != 1 || ms[0].Value != 3.0 {
		t.Fatalf("the built-in turns %+v %v", ms, err)
	}
	stopping, err := x.Stopping(ctx, eid)
	if err != nil || len(stopping) != 1 || stopping[0].ID != tr.ID {
		t.Fatalf("stopping %+v %v", stopping, err)
	}
	if err := x.Stopped(ctx, tr.ID); err != nil {
		t.Fatal(err)
	}
	// A cancel marks the run of a running trial to stop, and a run
	// recorded after its trial ended is to be stopped too.
	claimed, err = x.Claim(ctx, eid, cells[1:])
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed %+v %v", claimed, err)
	}
	if _, err := x.Started(ctx, claimed[0].ID, "ses_3", at); err != nil {
		t.Fatal(err)
	}
	more, err := x.Claim(ctx, eid, cells)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Cancel(ctx, eid); err != nil {
		t.Fatal(err)
	}
	for _, m := range more {
		if stop, err := x.Started(ctx, m.ID, "ses_late_"+m.ID, at); err != nil || !stop {
			t.Fatalf("a run of an ended trial: %v %v", stop, err)
		}
	}
	stopping, err = x.Stopping(ctx, eid)
	if err != nil || len(stopping) != 1+len(more) {
		t.Fatalf("after the cancel, stopping %+v %v", stopping, err)
	}
	if got, err := x.Driven(ctx, objects.ExecutorExternal, 10); err != nil || !slices.Contains(got, eid) {
		t.Fatalf("an experiment with runs to stop is not driven: %v %v", got, err)
	}
	if claimed, err := x.Claim(ctx, eid, cells); err != nil || len(claimed) != 0 {
		t.Fatalf("a canceled experiment's trials were claimed: %+v %v", claimed, err)
	}
}

// TestDrivenWritesFailWhole: a claim and an end that fail at any write
// leave nothing behind and succeed when tried again.
func TestDrivenWritesFailWhole(t *testing.T) {
	w := newWorld(t)
	ctx := t.Context()
	x := w.st.Experiments()
	p := w.plan()
	w.eachFault("claim", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		return func() error {
			_, err := x.Claim(ctx, eid, cells)
			return err
		}
	})
	w.eachFault("end", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		claimed, err := x.Claim(ctx, eid, cells)
		if err != nil || len(claimed) == 0 {
			t.Fatalf("claimed %+v %v", claimed, err)
		}
		return func() error {
			_, err := x.End(ctx, claimed[0].ID, outcome(objects.OutcomeCompleted), true)
			return err
		}
	})
	w.eachFault("started", func() func() error {
		eid, cells := w.running(ctx, p, 2)
		claimed, err := x.Claim(ctx, eid, cells)
		if err != nil || len(claimed) == 0 {
			t.Fatalf("claimed %+v %v", claimed, err)
		}
		return func() error {
			_, err := x.Started(ctx, claimed[0].ID, "ses_1", time.Now())
			return err
		}
	})
}
