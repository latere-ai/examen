// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// An experiment's declared comparisons are stored with it; Declare hands
// the newest record of each to the analysis under the experiment's lock
// and stores what it answers, which DeclaredAnalysis reads back; a later
// record of a declared comparison replaces the earlier one in what Declare
// reads; an analysis that fails stores nothing; and an experiment that
// declared nothing has no declared analysis.
func TestDeclare(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	p := w.plan()
	ne, _ := w.experiment(p, objects.ModeImport, 2)
	ne.Experiment.Comparisons = []objects.DeclaredComparison{
		{Candidate: p.b.Digest, ReferenceCandidate: p.a.Digest},
		{Candidate: p.a.Digest, Task: p.task.Digest, ReferenceTask: p.task.Digest + "x"},
	}
	zero, one := 0, 1
	ne.Comparisons[0].DeclaredIndex = &zero
	ne.Comparisons = append(ne.Comparisons, objects.Comparison{
		ID: id.New(id.Comparison), Candidate: p.a.Digest, Task: p.task.Digest, ReferenceTask: p.task.Digest + "x",
		DeclaredIndex: &one, State: objects.ComparisonComputing,
	})
	if err := w.st.Experiments().Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	eid := ne.Experiment.ID
	got, err := w.st.Experiments().Get(ctx, eid)
	if err != nil || len(got.Comparisons) != 2 || got.Comparisons[1].ReferenceTask != p.task.Digest+"x" {
		t.Fatalf("the declarations %+v %v", got.Comparisons, err)
	}
	if _, found, err := w.st.Experiments().DeclaredAnalysis(ctx, eid); err != nil || found {
		t.Fatalf("before any computation %v %v", found, err)
	}
	declare := func() ([]string, error) {
		var read []string
		err := w.st.Experiments().Declare(ctx, eid, func(e objects.Experiment, family []objects.Comparison) (objects.DeclaredAnalysis, error) {
			for _, c := range family {
				read = append(read, c.ID)
			}
			return objects.DeclaredAnalysis{Experiment: e.ID, Family: objects.DeclaredFamily{Method: "benjamini_hochberg", Size: len(e.Comparisons), Level: 0.05},
				Comparisons: []objects.DeclaredResult{{Index: 0, Comparison: family[0].ID, Reason: "r"}}}, nil
		})
		return read, err
	}
	read, err := declare()
	if err != nil || len(read) != 2 || read[0] != ne.Comparisons[0].ID || read[1] != ne.Comparisons[1].ID {
		t.Fatalf("read %v %v", read, err)
	}
	a, found, err := w.st.Experiments().DeclaredAnalysis(ctx, eid)
	if err != nil || !found || a.Family.Size != 2 || a.ComputedAt == nil || a.Comparisons[0].Comparison != ne.Comparisons[0].ID {
		t.Fatalf("stored %+v %v %v", a, found, err)
	}
	// A newer record of the first declared comparison is the one read.
	newer := ne.Comparisons[0]
	newer.ID, newer.Experiment, newer.Owner = id.New(id.Comparison), eid, owner
	if err := w.st.Experiments().CreateComparison(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if read, err := declare(); err != nil || read[0] != newer.ID {
		t.Fatalf("after a newer record %v %v", read, err)
	}
	if list, err := w.st.Experiments().Comparisons(ctx, store.ComparisonFilter{Experiment: eid, Declared: ptrTo(true)}, store.Page{Limit: 10}); err != nil || len(list) != 3 {
		t.Fatalf("declared comparisons %d %v", len(list), err)
	}
	broke := errors.New("the analysis broke")
	if err := w.st.Experiments().Declare(ctx, eid, func(objects.Experiment, []objects.Comparison) (objects.DeclaredAnalysis, error) {
		return objects.DeclaredAnalysis{}, broke
	}); !errors.Is(err, broke) {
		t.Fatalf("a failed analysis %v", err)
	}
	if a2, _, err := w.st.Experiments().DeclaredAnalysis(ctx, eid); err != nil || a2.Comparisons[0].Comparison != newer.ID {
		t.Fatalf("a failed analysis changed the stored one %+v %v", a2, err)
	}

	// An experiment that declared nothing: Declare asks nothing.
	plain, _ := w.experiment(p, objects.ModeImport, 2)
	if err := w.st.Experiments().Create(context.Background(), plain); err != nil {
		t.Fatal(err)
	}
	if err := w.st.Experiments().Declare(ctx, plain.Experiment.ID, func(objects.Experiment, []objects.Comparison) (objects.DeclaredAnalysis, error) {
		t.Error("asked for an experiment that declared nothing")
		return objects.DeclaredAnalysis{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.st.Experiments().DeclaredAnalysis(ctx, id.New(id.Experiment)); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an unknown experiment %v", err)
	}
}

func ptrTo[T any](v T) *T { return &v }
