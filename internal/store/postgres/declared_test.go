// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"

	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
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

// An experiment's test of independence and a declaration of the general
// form are stored with it and read back; Declare asks for the declared
// analysis of an experiment that declares a test of independence alone;
// and the candidate-level values of a candidate are read apart from its
// trials' values, at each grader's latest generation.
func TestDeclarationsAndCandidateValues(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	p := w.plan()
	ne, _ := w.experiment(p, objects.ModeImport, 2)
	ne.Experiment.Independence = &objects.Independence{Metric: "builtin:completed", Variable: "object", By: []string{"candidate"}, Where: map[string]string{"volition": "want"}}
	decl := objects.DeclaredComparison{Variable: "object", Levels: []string{"build", "do"}, Where: map[string]string{"candidate": p.a.Digest}, By: []string{"volition"}, Metric: "builtin:completed"}
	ne.Experiment.Comparisons = []objects.DeclaredComparison{decl}
	zero := 0
	ne.Comparisons = append(ne.Comparisons, objects.Comparison{ID: id.New(id.Comparison), Candidate: p.a.Digest, DeclaredIndex: &zero, Declaration: &decl, State: objects.ComparisonComputing})
	if err := w.st.Experiments().Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	eid := ne.Experiment.ID
	got, err := w.st.Experiments().Get(ctx, eid)
	if err != nil || got.Independence == nil || got.Independence.Where["volition"] != "want" || got.Comparisons[0].Metric != "builtin:completed" || got.Comparisons[0].By[0] != "volition" {
		t.Fatalf("the declarations %+v %+v %v", got.Independence, got.Comparisons, err)
	}
	cmp, err := w.st.Experiments().Comparison(ctx, ne.Comparisons[1].ID)
	if err != nil || cmp.Declaration == nil || cmp.Declaration.Variable != "object" || cmp.ReferenceCandidate != "" {
		t.Fatalf("the general comparison %+v %v", cmp, err)
	}
	if auto, err := w.st.Experiments().Comparison(ctx, ne.Comparisons[0].ID); err != nil || auto.Declaration != nil {
		t.Fatalf("a comparison of no declaration %+v %v", auto.Declaration, err)
	}
	alone, _ := w.experiment(p, objects.ModeImport, 2)
	alone.Experiment.Independence = ne.Experiment.Independence
	if err := w.st.Experiments().Create(ctx, alone); err != nil {
		t.Fatal(err)
	}
	asked := false
	if err := w.st.Experiments().Declare(ctx, alone.Experiment.ID, func(e objects.Experiment, family []objects.Comparison) (objects.DeclaredAnalysis, error) {
		asked = true
		return objects.DeclaredAnalysis{Experiment: e.ID, Comparisons: []objects.DeclaredResult{}}, nil
	}); err != nil || !asked {
		t.Fatalf("an experiment of a test of independence alone was asked %v: %v", asked, err)
	}

	// Candidate-level values beside a trial's: two generations of one
	// grader, and another candidate's.
	tr, err := w.st.Experiments().Import(ctx, store.Import{Experiment: eid, Cell: objects.Cell{Task: p.task.Digest, TaskID: p.task.ID, Candidate: p.a.Digest, CandidateID: p.a.ID}, Outcome: outcome(objects.OutcomeCompleted)})
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range []struct {
		candidate  string
		generation int
		value      float64
	}{{p.a.Digest, 1, 0.25}, {p.a.Digest, 2, 0.75}, {p.b.Digest, 1, 0.5}} {
		if err := pgtest.Exec(ctx, w.dsn, `INSERT INTO measurements (id, owner, experiment, candidate, metric, grader, self_reported, generation, value_num, rationale, created_at)
			VALUES ($1, $2, $3, $4, 'sha256:rank', 'sha256:ranker', false, $5, $6, '', now())`, "msr_cand"+strconv.Itoa(i), owner, eid, v.candidate, v.generation, v.value); err != nil {
			t.Fatal(err)
		}
	}
	vals, err := w.st.Experiments().Measurements(ctx, store.MeasurementFilter{Experiment: eid, Candidate: p.a.Digest, CandidateLevel: true}, store.Page{Limit: 10})
	if err != nil || len(vals) != 1 || vals[0].Value != 0.75 || vals[0].Trial != "" {
		t.Fatalf("candidate-level values %+v %v", vals, err)
	}
	all, err := w.st.Experiments().Measurements(ctx, store.MeasurementFilter{Experiment: eid, Candidate: p.a.Digest}, store.Page{Limit: 10})
	if err != nil || !slices.ContainsFunc(all, func(m objects.Measurement) bool { return m.Trial == tr.ID }) {
		t.Fatalf("without the filter a candidate's trials' values are read too: %+v %v", all, err)
	}
}

// A judge's calibration pairs are the owner's trials holding the judge's
// latest value of a metric and at least one person's: each annotator's
// latest under each annotation grader, a trial no person annotated left
// out, and another owner's trials never read.
func TestCalibrationPairs(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	p := w.plan()
	ne, _ := w.experiment(p, objects.ModeImport, 3)
	if err := w.st.Experiments().Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	eid := ne.Experiment.ID
	var trials []objects.Trial
	for range 3 {
		tr, err := w.st.Experiments().Import(ctx, store.Import{Experiment: eid, Cell: objects.Cell{Task: p.task.Digest, TaskID: p.task.ID, Candidate: p.a.Digest, CandidateID: p.a.ID}, Outcome: outcome(objects.OutcomeCompleted)})
		if err != nil {
			t.Fatal(err)
		}
		trials = append(trials, tr)
	}
	insert := func(n int, tr objects.Trial, owner, grader, annotator string, generation int, value string) {
		t.Helper()
		if err := pgtest.Exec(ctx, w.dsn, `INSERT INTO measurements (id, owner, experiment, cell, trial, metric, grader, annotator, self_reported, generation, value_text, rationale, created_at)
			VALUES ($1, $2, $3, $4, $5, 'sha256:kind', $6, $7, false, $8, $9, '', now())`, "msr_cal"+strconv.Itoa(n), owner, eid, tr.Cell, tr.ID, grader, annotator, generation, value); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, trials[0], owner, "sha256:judge", "", 1, "tool")
	insert(1, trials[0], owner, "sha256:judge", "", 2, "game")
	insert(2, trials[0], owner, "sha256:people", "human:ann", 1, "tool")
	insert(3, trials[0], owner, "sha256:people", "human:ann", 2, "game")
	insert(4, trials[0], owner, "sha256:people", "human:bob", 1, "game")
	insert(5, trials[1], owner, "sha256:judge", "", 1, "tool")
	insert(6, trials[1], owner, "sha256:people", "human:ann", 1, "game")
	// A trial the judge graded and no person annotated, and a value of
	// another owner's.
	insert(7, trials[2], owner, "sha256:judge", "", 1, "game")
	insert(8, trials[2], "https://issuer.example.com|eve", "sha256:people", "human:eve", 1, "game")
	pairs, err := w.st.Experiments().CalibrationPairs(ctx, owner, "sha256:judge", "sha256:kind")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]store.CalibrationPair{
		trials[0].ID: {Trial: trials[0].ID, Judge: "game", People: []any{"game", "game"}},
		trials[1].ID: {Trial: trials[1].ID, Judge: "tool", People: []any{"game"}},
	}
	if len(pairs) != 2 {
		t.Fatalf("pairs %+v", pairs)
	}
	for _, got := range pairs {
		w := want[got.Trial]
		if got.Judge != w.Judge || !slices.Equal(got.People, w.People) {
			t.Errorf("trial %s: %+v, want %+v", got.Trial, got, w)
		}
	}
	if none, err := w.st.Experiments().CalibrationPairs(ctx, owner, "sha256:other", "sha256:kind"); err != nil || len(none) != 0 {
		t.Fatalf("a judge of no grading %+v %v", none, err)
	}
}
