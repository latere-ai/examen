// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// trialPage is how many trials one read of a side takes.
const trialPage = 1000

// suiteOf is an experiment's suite version as the analysis reads it: the
// resolved suite, for each task's producing graders, and its metrics,
// weights and primary metric.
type suiteOf struct {
	resolved  objects.Resolved
	producers map[string]map[string]string
	// candidateProducers maps each candidate-level metric to the one grader
	// of level candidate among the suite's graders that produces it.
	candidateProducers map[string]string
	suite              analysis.Suite
	name               objects.SuiteName
}

// readSuite reads the suite version an experiment runs and every version it
// pins, in the experiment's owner's context. A stored suite version passed
// its checks when it was applied, so a pin that no longer resolves is an
// error of the store, not of the request.
func readSuite(ctx context.Context, vs store.Versions, e objects.Experiment) (suiteOf, error) {
	v, err := vs.ByID(ctx, objects.KindSuite, e.SuiteID)
	if err != nil {
		return suiteOf{}, fmt.Errorf("compare: reading the suite of %s: %w", e.ID, err)
	}
	var su objects.Suite
	if err := json.Unmarshal(v.Body, &su); err != nil {
		return suiteOf{}, fmt.Errorf("compare: reading the suite of %s: %w", e.ID, err)
	}
	res := objects.Resolved{Suite: su, Digest: v.Digest, Tasks: map[string]objects.Task{}, Metrics: map[string]objects.Metric{}, Graders: map[string]objects.Grader{}, TaskIDs: map[string]string{}}
	read := func(kind, digest string, into any) error {
		ver, err := vs.ByDigest(ctx, kind, v.Owner, digest)
		if err != nil {
			return fmt.Errorf("compare: reading the %s %s of the suite %s: %w", kind, digest, v.Digest, err)
		}
		if err := json.Unmarshal(ver.Body, into); err != nil {
			return fmt.Errorf("compare: reading the %s %s of the suite %s: %w", kind, digest, v.Digest, err)
		}
		return nil
	}
	grader := func(digest string) error {
		if _, done := res.Graders[digest]; done || digest == "" {
			return nil
		}
		var g objects.Grader
		if err := read(objects.KindGrader, digest, &g); err != nil {
			return err
		}
		res.Graders[digest] = g
		return nil
	}
	for _, d := range su.Tasks {
		var t objects.Task
		if err := read(objects.KindTask, d, &t); err != nil {
			return suiteOf{}, err
		}
		res.Tasks[d] = t
		for _, c := range t.Checks {
			if err := grader(c); err != nil {
				return suiteOf{}, err
			}
		}
	}
	for _, d := range su.Metrics {
		if b, ok := objects.Builtin(d); ok {
			res.Metrics[d] = b
			continue
		}
		var m objects.Metric
		if err := read(objects.KindMetric, d, &m); err != nil {
			return suiteOf{}, err
		}
		res.Metrics[d] = m
	}
	for _, d := range append(slices.Clone(su.Graders), su.Canonicalizers...) {
		if err := grader(d); err != nil {
			return suiteOf{}, err
		}
	}
	if err := grader(su.DeclineJudge); err != nil {
		return suiteOf{}, err
	}
	out := suiteOf{resolved: res, producers: res.Producers(), candidateProducers: map[string]string{}, name: objects.SuiteName{Owner: v.Owner, Name: v.Name}}
	for _, md := range su.Metrics {
		if res.Metrics[md].LevelOf() != objects.LevelCandidate {
			continue
		}
		for _, gd := range su.Graders {
			if g := res.Graders[gd]; g.LevelOf() == objects.LevelCandidate && slices.Contains(g.Produces, md) {
				out.candidateProducers[md] = gd
				break
			}
		}
	}
	out.suite = analysis.Suite{PrimaryMetric: su.PrimaryMetric, Weights: su.Weights}
	for _, d := range su.Tasks {
		out.suite.Tasks = append(out.suite.Tasks, analysis.SuiteTask{Name: res.Tasks[d].Name, Digest: d})
	}
	for _, d := range su.Metrics {
		out.suite.Metrics = append(out.suite.Metrics, metricOf(d, res.Metrics[d]))
	}
	return out, nil
}

// metricOf is a metric version as the analysis reads it.
func metricOf(digest string, m objects.Metric) analysis.Metric {
	out := analysis.Metric{
		Digest: digest, Name: m.Name, Type: analysis.Type(m.Type), Aggregate: analysis.Aggregate(m.Aggregate),
		Direction: analysis.Direction(m.Direction), Level: analysis.Level(m.Level), Vocabulary: analysis.Vocabulary(m.Vocabulary),
		Labels: m.Labels, Of: m.Of,
	}
	if m.Type == objects.TypeLabel && m.Vocabulary == "" {
		out.Vocabulary = analysis.Closed
	}
	for _, o := range m.AppliesTo {
		out.AppliesTo = append(out.AppliesTo, analysis.Outcome(o))
	}
	if m.ExcludeLabels != nil {
		out.ExcludeLabels = append([]string{}, *m.ExcludeLabels...)
	}
	return out
}

// tupleOf is a cell's provenance tuple as the analysis reads it: the tool
// set as a sorted set, and the program as its canonical JSON.
func tupleOf(t objects.Tuple) analysis.Tuple {
	tools := slices.Clone(t.Tools)
	slices.Sort(tools)
	if tools == nil {
		tools = []string{}
	}
	return analysis.Tuple{
		CandidateKind: t.CandidateKind, AgentOrImageDigest: t.AgentOrImageDigest, Program: string(t.Program),
		Model: t.Model, Reasoning: t.Reasoning, Tools: tools, TaskHash: t.TaskHash,
		EnvironmentDigest: t.EnvironmentDigest, WorkspaceRef: t.WorkspaceRef, GraderDigests: slices.Clone(t.GraderDigests),
	}
}

// costOf is a trial's cost as the stored analysis's spend reads it: priced
// when its cost is known, and the costs its APIs reported for themselves
// kept apart. A trial whose observed layer names no cost state is unknown,
// never zero.
func costOf(o objects.Observed) analysis.Cost {
	c := analysis.Cost{Status: analysis.CostUnknown}
	switch o.CostState {
	case objects.CostKnown:
		c.Status = analysis.CostPriced
		if o.CostUSD != nil {
			c.USD = *o.CostUSD
		}
	case objects.CostUnpriced:
		c.Status = analysis.CostUnpriced
	}
	for _, name := range sortedNames(o.ServiceUsage) {
		c.Service += float64(o.ServiceUsage[name].CostUSDMicro) / 1e6
	}
	return c
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// trialOf is a stored trial with its measurements as the analysis reads
// it. A trial not done has no outcome; an annotation's value is the
// annotation queue's, which no comparison reads.
func trialOf(t objects.Trial, ms []objects.Measurement) analysis.Trial {
	out := analysis.Trial{ID: t.ID, Retried: t.Retried, ServiceVersions: t.Observed.ServiceVersions, Cost: costOf(t.Observed)}
	if t.State == objects.StateDone {
		out.Outcome = analysis.Outcome(t.Outcome)
	}
	if t.StartedAt != nil {
		out.StartedAt = t.StartedAt.UTC()
	}
	if t.EndedAt != nil {
		out.EndedAt = t.EndedAt.UTC()
	}
	if t.Declared != nil {
		out.DeclaredImage = t.Declared.Image
	}
	for _, m := range ms {
		if m.Annotator != "" {
			continue
		}
		out.Measurements = append(out.Measurements, analysis.Measurement{
			ID: m.ID, Metric: m.Metric, Grader: m.Grader, Generation: m.Generation, Value: m.Value, SelfReported: m.SelfReported,
		})
	}
	return out
}

// trialsOf reads every trial of the cells, oldest last, and their
// measurements, by cell.
func trialsOf(ctx context.Context, x store.Experiments, experiment string, cells []string) (map[string][]analysis.Trial, error) {
	if len(cells) == 0 {
		return map[string][]analysis.Trial{}, nil
	}
	var all []objects.Trial
	p := store.Page{Limit: trialPage}
	for {
		page, err := x.Trials(ctx, experiment, store.TrialFilter{Cells: cells}, p)
		if err != nil {
			return nil, err
		}
		if len(page) <= p.Limit {
			all = append(all, page...)
			break
		}
		all = append(all, page[:p.Limit]...)
		p.After = page[p.Limit-1].ID
	}
	ids := make([]string, len(all))
	for i, t := range all {
		ids[i] = t.ID
	}
	ms, err := x.MeasurementsOf(ctx, ids)
	if err != nil {
		return nil, err
	}
	byTrial := map[string][]objects.Measurement{}
	for _, m := range ms {
		byTrial[m.Trial] = append(byTrial[m.Trial], m)
	}
	out := map[string][]analysis.Trial{}
	for _, t := range all {
		out[t.Cell] = append(out[t.Cell], trialOf(t, byTrial[t.ID]))
	}
	return out, nil
}

// candidateValues reads the current candidate-level values of one
// candidate in one experiment, every page of them, as the analysis reads
// them: each grader's latest generation.
func candidateValues(ctx context.Context, x store.Experiments, experiment, candidate string) ([]analysis.Measurement, error) {
	f := store.MeasurementFilter{Experiment: experiment, Candidate: candidate, CandidateLevel: true}
	p := store.Page{Limit: trialPage}
	var out []analysis.Measurement
	for {
		page, err := x.Measurements(ctx, f, p)
		if err != nil {
			return nil, fmt.Errorf("compare: reading the candidate-level values of %s: %w", candidate, err)
		}
		more := len(page) > p.Limit
		if more {
			page = page[:p.Limit]
		}
		for _, m := range page {
			out = append(out, analysis.Measurement{ID: m.ID, Metric: m.Metric, Grader: m.Grader, Generation: m.Generation, Value: m.Value, SelfReported: m.SelfReported})
		}
		if !more {
			break
		}
		p.After = page[len(page)-1].ID
	}
	// The store pages newest first; the analysis reads values by id.
	slices.SortFunc(out, func(a, b analysis.Measurement) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// cellsOf reads cells of one experiment as the analysis reads them, each
// with its trials and its task's producing graders in that experiment's
// suite.
func cellsOf(ctx context.Context, x store.Experiments, su suiteOf, experiment string, cells []objects.Cell) ([]analysis.Cell, error) {
	ids := make([]string, len(cells))
	for i, c := range cells {
		ids[i] = c.ID
	}
	trials, err := trialsOf(ctx, x, experiment, ids)
	if err != nil {
		return nil, err
	}
	out := make([]analysis.Cell, 0, len(cells))
	for _, c := range cells {
		task, ok := su.resolved.Tasks[c.Task]
		if !ok {
			return nil, fmt.Errorf("compare: the cell %s names the task %s, which its suite does not pin", c.ID, c.Task)
		}
		out = append(out, analysis.Cell{
			Task: task.Name, TaskDigest: c.Task, Tuple: tupleOf(c.Tuple), Producers: su.producers[c.Task],
			Trials: trials[c.ID], Final: c.State == objects.StateComplete,
		})
	}
	return out, nil
}

// side is a candidate's cells of one experiment, or of one task of it.
type side struct {
	experiment objects.Experiment
	suite      suiteOf
	candidate  string
	cells      []objects.Cell
}

// sideOf reads an experiment's cells of a candidate, those of one task
// when it names one.
func sideOf(ctx context.Context, src Source, e objects.Experiment, su suiteOf, candidate, task string) (side, error) {
	all, err := src.Experiments.AllCells(ctx, e.ID)
	if err != nil {
		return side{}, err
	}
	s := side{experiment: e, suite: su, candidate: candidate}
	for _, c := range all {
		if c.Candidate == candidate && (task == "" || c.Task == task) {
			s.cells = append(s.cells, c)
		}
	}
	return s, nil
}

// taxonomies are the records each open-vocabulary metric of a suite is read
// at: the suite's current record of it, when it has one.
func taxonomies(ctx context.Context, t store.Taxonomies, su suiteOf) (map[string]analysis.Taxonomy, error) {
	out := map[string]analysis.Taxonomy{}
	for _, m := range su.suite.Metrics {
		if m.Type != analysis.TypeLabel || m.Vocabulary != analysis.Open {
			continue
		}
		rec, err := t.Current(ctx, su.name, m.Digest)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[m.Digest] = analysis.Taxonomy{ID: rec.ID, Digest: rec.Digest, Canonical: rec.Canonical, Map: rec.Map}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
