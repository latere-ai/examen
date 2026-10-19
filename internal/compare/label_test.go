// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// Measurements answers the candidate-level values of a candidate in an
// experiment as the store pages them: newest first by id, Limit+1 read.
func (x *experiments) Measurements(_ context.Context, f store.MeasurementFilter, p store.Page) ([]objects.Measurement, error) {
	if x.fail == "Measurements" {
		return nil, errBroken
	}
	var all []objects.Measurement
	for _, m := range x.ms {
		if f.CandidateLevel && m.Trial == "" && m.Experiment == f.Experiment && m.Candidate == f.Candidate {
			all = append(all, m)
		}
	}
	slices.SortFunc(all, func(a, b objects.Measurement) int { return strings.Compare(b.ID, a.ID) })
	var out []objects.Measurement
	for _, m := range all {
		if p.After != "" && m.ID >= p.After {
			continue
		}
		out = append(out, m)
		if len(out) == p.Limit+1 {
			break
		}
	}
	return out, nil
}

const (
	kind      = "sha256:kind"
	rank      = "sha256:rank"
	judge     = "sha256:judge"
	rankJudge = "sha256:rank-judge"
)

// labelWorld is one experiment whose suite's primary metric is a closed
// label a model judge produces, with a candidate-level score a judge of
// level candidate produces: candNew's runs of both tasks are all games,
// candOld's all tools, six of each per cell.
func labelWorld(t *testing.T) *world {
	t.Helper()
	w := newWorld(t)
	w.v.put(t, objects.KindMetric, "met_kind", kind, objects.Metric{Name: "kind", Type: objects.TypeLabel, Labels: []string{"game", "tool", "other"}, Direction: objects.DirectionNone, Aggregate: objects.AggregateDistribution})
	w.v.put(t, objects.KindMetric, "met_rank", rank, objects.Metric{Name: "rank", Type: objects.TypeScore, Range: []float64{0, 1}, Direction: objects.HigherIsBetter, Level: objects.LevelCandidate})
	w.v.put(t, objects.KindGrader, "grd_judge", judge, objects.Grader{Name: "artifact", Kind: objects.GraderModelJudge, Produces: []string{kind}})
	w.v.put(t, objects.KindGrader, "grd_rank", rankJudge, objects.Grader{Name: "ranker", Kind: objects.GraderModelJudge, Level: objects.LevelCandidate, Produces: []string{rank}})
	w.v.put(t, objects.KindSuite, "sui_lab", "sha256:suite-lab", objects.Suite{Name: "lab", Tasks: []string{taskA, taskB}, Metrics: []string{kind, rank}, PrimaryMetric: kind, Graders: []string{judge, rankJudge}})
	w.x.exps["exp_lab"] = objects.Experiment{ID: "exp_lab", Owner: "alice", SuiteID: "sui_lab", Candidates: []string{candNew, candOld}}
	start := time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		cand, label string
	}{{candNew, "game"}, {candOld, "tool"}} {
		for _, task := range []string{taskA, taskB} {
			cell := fmt.Sprintf("cel_lab_%s_%s", c.label, task[len("sha256:"):])
			w.x.cells["exp_lab"] = append(w.x.cells["exp_lab"], objects.Cell{ID: cell, Experiment: "exp_lab", Task: task, Candidate: c.cand,
				Tuple: objects.Tuple{CandidateKind: objects.CandidateExternal, Model: c.label, TaskHash: task}, State: objects.StateComplete})
			for i := range 6 {
				at := start.Add(time.Duration(i) * time.Minute)
				end := at.Add(30 * time.Second)
				id := fmt.Sprintf("tri_%s_%d", cell, i)
				w.x.trials["exp_lab"] = append(w.x.trials["exp_lab"], objects.Trial{ID: id, Cell: cell, State: objects.StateDone, Outcome: objects.OutcomeCompleted,
					StartedAt: &at, EndedAt: &end, Observed: objects.Observed{CostState: objects.CostUnknown}})
				w.x.ms = append(w.x.ms, objects.Measurement{ID: "msr_" + id, Experiment: "exp_lab", Trial: id, Metric: kind, Grader: judge, Generation: 1, Value: c.label})
			}
		}
	}
	slices.SortFunc(w.x.trials["exp_lab"], func(a, b objects.Trial) int { return strings.Compare(a.ID, b.ID) })
	w.x.ms = append(w.x.ms,
		objects.Measurement{ID: "msr_rank_new", Experiment: "exp_lab", Candidate: candNew, Metric: rank, Grader: rankJudge, Generation: 1, Value: 0.8},
		objects.Measurement{ID: "msr_rank_old", Experiment: "exp_lab", Candidate: candOld, Metric: rank, Grader: rankJudge, Generation: 1, Value: 0.5},
		// Another experiment's value of the same candidate is not read.
		objects.Measurement{ID: "msr_rank_elsewhere", Experiment: "exp_new", Candidate: candNew, Metric: rank, Grader: rankJudge, Generation: 1, Value: 0.1},
	)
	return w
}

// A served comparison tests a label metric: its task-level results by
// Fisher, Freeman and Halton's exact test, corrected in the family, and
// its primary metric's suite-level result by the stratified label test,
// tested alone; and it reads each side's candidate-level values, at the
// grader of level candidate its suite names, into a descriptive
// candidate-level result.
func TestServedLabelTestsAndCandidateValues(t *testing.T) {
	w := labelWorld(t)
	c := objects.Comparison{ID: "cmp_lab", Experiment: "exp_lab", Candidate: candNew, ReferenceCandidate: candOld, Final: true}
	in, err := Input(t.Context(), c, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if !in.LabelTests {
		t.Fatal("a served comparison does not test its label metrics")
	}
	if len(in.Candidate.Values) != 1 || in.Candidate.Values[0].ID != "msr_rank_new" || in.Candidate.Producers[rank] != rankJudge {
		t.Fatalf("the candidate side's values %+v, producers %v", in.Candidate.Values, in.Candidate.Producers)
	}
	if len(in.Reference.Values) != 1 || in.Reference.Values[0].Value != 0.5 {
		t.Fatalf("the reference side's values %+v", in.Reference.Values)
	}
	computed, read, err := Analysis{}.Compare(t.Context(), c, w.src)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(read, "msr_rank_new") || !slices.Contains(read, "msr_rank_old") || slices.Contains(read, "msr_rank_elsewhere") {
		t.Fatalf("the measurements read %v", read)
	}
	row := func(metric, level, task string) objects.ResultRow {
		t.Helper()
		for _, r := range computed.Results {
			if r.Metric == metric && r.Level == level && r.Task == task {
				return r
			}
		}
		t.Fatalf("no %s %s %s result", metric, level, task)
		return objects.ResultRow{}
	}
	// Six games against six tools: of the 924 tables with these margins
	// the observed one and its mirror are the two least likely, each
	// 1/924, so p = 2/924; the family holds the two tasks' equal p-values.
	for _, task := range []string{"todo-app", "calculator"} {
		r := row(kind, "task", task)
		if r.Method != "fisher_freeman_halton" || r.Statistic != "table_probability" || r.P == nil || math.Abs(*r.P-2.0/924) > 1e-12 {
			t.Fatalf("%s: %s %s p %v", task, r.Method, r.Statistic, r.P)
		}
		if r.Q == nil || math.Abs(*r.Q-2.0/924) > 1e-12 || r.Verdict != string(analysis.Changed) {
			t.Fatalf("%s: q %v verdict %q (%s)", task, r.Q, r.Verdict, r.Reason)
		}
	}
	primary := row(kind, "suite", "")
	if primary.Method != "stratified_permutation" || primary.Statistic != "pearson_x2_sum" || primary.P == nil || *primary.P > 0.001 ||
		primary.Q == nil || *primary.Q != *primary.P || primary.Verdict != string(analysis.Changed) {
		t.Fatalf("the primary result %s %s p %v q %v %q (%s)", primary.Method, primary.Statistic, primary.P, primary.Q, primary.Verdict, primary.Reason)
	}
	cand := row(rank, "candidate", "")
	if string(cand.Candidate) == "" || !strings.Contains(string(cand.Candidate), `"value":0.8`) || !strings.Contains(string(cand.Reference), `"value":0.5`) ||
		cand.P != nil || cand.Verdict != "" {
		t.Fatalf("the candidate-level result %s against %s, p %v verdict %q", cand.Candidate, cand.Reference, cand.P, cand.Verdict)
	}
	w.x.fail = "Measurements"
	if _, err := Input(t.Context(), c, w.src); !errors.Is(err, errBroken) {
		t.Fatalf("a failing read of candidate-level values: %v", err)
	}
}

// Candidate-level values are read across every page the store answers.
func TestCandidateValuesPage(t *testing.T) {
	w := labelWorld(t)
	for i := range trialPage + 1 {
		w.x.ms = append(w.x.ms, objects.Measurement{ID: fmt.Sprintf("msr_rank_page_%05d", i), Experiment: "exp_lab", Candidate: candOld, Metric: rank, Grader: rankJudge, Generation: i + 2, Value: 0.25})
	}
	got, err := candidateValues(t.Context(), w.x, "exp_lab", candOld)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != trialPage+2 || !slices.IsSortedFunc(got, func(a, b analysis.Measurement) int { return strings.Compare(a.ID, b.ID) }) {
		t.Fatalf("read %d values", len(got))
	}
}
