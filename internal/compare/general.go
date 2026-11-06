// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"

	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// designOf is every cell an experiment's tasks and candidates make, with
// the cell variables each carries, read from the candidate versions it
// resolved at create, so a cell an import has not made yet is in it too.
func designOf(ctx context.Context, vs store.Versions, e objects.Experiment, su suiteOf) ([]objects.DesignCell, error) {
	bodies := make([]objects.Candidate, len(e.CandidateIDs))
	for i, cid := range e.CandidateIDs {
		v, err := vs.ByID(ctx, objects.KindCandidate, cid)
		if err != nil {
			return nil, fmt.Errorf("compare: reading a candidate of %s: %w", e.ID, err)
		}
		if err := json.Unmarshal(v.Body, &bodies[i]); err != nil {
			return nil, fmt.Errorf("compare: reading a candidate of %s: %w", e.ID, err)
		}
	}
	return su.resolved.Design(e.Candidates, bodies), nil
}

// storedCells are an experiment's cells by task and candidate digest.
func storedCells(ctx context.Context, x store.Experiments, experiment string) (map[[2]string]objects.Cell, error) {
	all, err := x.AllCells(ctx, experiment)
	if err != nil {
		return nil, err
	}
	out := make(map[[2]string]objects.Cell, len(all))
	for _, c := range all {
		out[[2]string{c.Task, c.Candidate}] = c
	}
	return out, nil
}

// stratumCells reads the stored cells of strata as the analysis reads
// them, each named by its stratum: the cells pair by that name.
func stratumCells(ctx context.Context, x store.Experiments, su suiteOf, experiment string, cells []objects.Cell, names []string) ([]analysis.Cell, error) {
	read, err := cellsOf(ctx, x, su, experiment, cells)
	if err != nil {
		return nil, err
	}
	for i := range read {
		read[i].Task = names[i]
	}
	return read, nil
}

// generalInput reads a declared comparison of the general form (spec 008,
// Comparisons over cell variables): its two levels' cells over the
// experiment's design, each named by its stratum, the strata as the
// suite's tasks, each weighted as its candidate side's task, the metric of
// its primary result, and its logical variable.
func generalInput(ctx context.Context, c objects.Comparison, e objects.Experiment, su suiteOf, src Source, in analysis.Input) (analysis.Input, error) {
	d := *c.Declaration
	design, err := designOf(ctx, src.Versions, e, su)
	if err != nil {
		return analysis.Input{}, err
	}
	sel, err := objects.Select(d, design, "declaration")
	if err != nil {
		return analysis.Input{}, fmt.Errorf("compare: the declaration of %s does not read over its experiment: %w", c.ID, err)
	}
	stored, err := storedCells(ctx, src.Experiments, e.ID)
	if err != nil {
		return analysis.Input{}, err
	}
	var sides [2]struct {
		cells     []objects.Cell
		names     []string
		candidate string
	}
	in.Suite.Tasks = make([]analysis.SuiteTask, 0, len(sel.Strata))
	for _, st := range sel.Strata {
		weighted := st.Candidate
		if weighted == nil {
			weighted = st.Reference
		}
		in.Suite.Tasks = append(in.Suite.Tasks, analysis.SuiteTask{Name: st.Name, Digest: weighted.Task})
		for s, dc := range []*objects.DesignCell{st.Candidate, st.Reference} {
			if dc == nil {
				continue
			}
			if cell, ok := stored[[2]string{dc.Task, dc.Candidate}]; ok {
				sides[s].cells = append(sides[s].cells, cell)
				sides[s].names = append(sides[s].names, st.Name)
			}
		}
	}
	sides[0].candidate = sel.CandidateOf()
	sides[1].candidate = (objects.Selection{Strata: swapped(sel.Strata)}).CandidateOf()
	for s, into := range []*analysis.Side{&in.Candidate, &in.Reference} {
		cells, err := stratumCells(ctx, src.Experiments, su, e.ID, sides[s].cells, sides[s].names)
		if err != nil {
			return analysis.Input{}, err
		}
		*into = analysis.Side{Candidate: sides[s].candidate, Cells: cells}
		if sides[s].candidate == "" {
			continue
		}
		// A side of one candidate reads its candidate-level values.
		if into.Values, err = candidateValues(ctx, src.Experiments, e.ID, sides[s].candidate); err != nil {
			return analysis.Input{}, err
		}
		into.Producers = su.candidateProducers
	}
	if len(d.By) == 0 && (len(in.Candidate.Cells) != 1 || len(in.Reference.Cells) != 1) {
		return analysis.Input{}, fmt.Errorf("%w: %s compares two cells of which the experiment holds %d and %d", errCellsMissing, c.ID, len(in.Candidate.Cells), len(in.Reference.Cells))
	}
	if d.Metric != "" {
		in.Suite.PrimaryMetric = d.Metric
	}
	in.Variable = objects.LogicalVariableOf(d.Variable)
	in.Declaration = &analysis.Declaration{
		Variable: d.Variable, Levels: d.Levels, Where: d.Where, By: d.By, Metric: in.Suite.PrimaryMetric,
	}
	if in.Declaration.Where == nil {
		in.Declaration.Where = map[string]string{}
	}
	if in.Declaration.By == nil {
		in.Declaration.By = []string{}
	}
	return in, nil
}

// swapped is strata with their two sides exchanged.
func swapped(strata []objects.Stratum) []objects.Stratum {
	out := slices.Clone(strata)
	for i := range out {
		out[i].Candidate, out[i].Reference = out[i].Reference, out[i].Candidate
	}
	return out
}

// pendingIndependence is a declared test of independence before any cell
// it reads holds a finished trial: what it declared, and why it has no
// figure yet.
type pendingIndependence struct {
	Metric     string            `json:"metric"`
	MetricName string            `json:"metric_name"`
	Variable   string            `json:"variable"`
	By         []string          `json:"by"`
	Where      map[string]string `json:"where,omitempty"`
	Levels     []string          `json:"levels"`
	Reason     string            `json:"reason"`
	Final      bool              `json:"final"`
}

// independence computes an experiment's declared test of independence
// (spec 008): one side per level of its variable, each holding its cells
// named by their strata, and the strata as the suite's tasks, each
// weighted as the task of its cell on the first level that holds one. It
// answers the result as the declared analysis keeps it and whether it is
// final; before any cell it reads holds a finished trial, or when pending
// asks for no figure, what it declared and why it has no figure yet.
func independence(ctx context.Context, e objects.Experiment, su suiteOf, src Source, pending bool) (jsontext.Value, bool, error) {
	ind := *e.Independence
	design, err := designOf(ctx, src.Versions, e, su)
	if err != nil {
		return nil, false, err
	}
	sel, err := objects.SelectIndependence(ind, design, e.Candidates, "independence")
	if err != nil {
		return nil, false, fmt.Errorf("compare: the test of independence of %s does not read over its experiment: %w", e.ID, err)
	}
	stored, err := storedCells(ctx, src.Experiments, e.ID)
	if err != nil {
		return nil, false, err
	}
	suite := su.suite
	suite.Tasks = make([]analysis.SuiteTask, 0, len(sel.Strata))
	cells := make([][]objects.Cell, len(sel.Levels))
	names := make([][]string, len(sel.Levels))
	for _, st := range sel.Strata {
		first := slices.IndexFunc(st.Cells, func(c *objects.DesignCell) bool { return c != nil })
		suite.Tasks = append(suite.Tasks, analysis.SuiteTask{Name: st.Name, Digest: st.Cells[first].Task})
		for l, dc := range st.Cells {
			if dc == nil {
				continue
			}
			if cell, ok := stored[[2]string{dc.Task, dc.Candidate}]; ok {
				cells[l] = append(cells[l], cell)
				names[l] = append(names[l], st.Name)
			}
		}
	}
	in := analysis.IndependenceInput{
		Parameters: analysis.Defaults(), Suite: suite, Metric: ind.Metric, Variable: ind.Variable, By: ind.By, Where: ind.Where,
		Sides: make([]analysis.Side, len(sel.Levels)), LabelTests: true,
	}
	if in.By == nil {
		in.By = []string{}
	}
	started := false
	for l, level := range sel.Levels {
		read, err := stratumCells(ctx, src.Experiments, su, e.ID, cells[l], names[l])
		if err != nil {
			return nil, false, err
		}
		in.Sides[l] = analysis.Side{Candidate: level, Cells: read}
		started = started || finished(in.Sides[l])
	}
	if pending || !started {
		raw, err := json.Marshal(pendingIndependence{
			Metric: ind.Metric, MetricName: su.resolved.Metrics[ind.Metric].Name, Variable: ind.Variable, By: in.By, Where: ind.Where,
			Levels: sel.Levels, Reason: analysis.ReasonIndependenceNotComputed,
		}, storedJSON)
		return raw, false, err
	}
	if in.Taxonomies, err = taxonomies(ctx, src.Taxonomies, su); err != nil {
		return nil, false, err
	}
	if in.Calibrations, err = calibrations(ctx, src.Experiments, e.Owner, su.resolved.Graders, su.suite.Metrics, in.Sides...); err != nil {
		return nil, false, err
	}
	res, err := analysis.Independence(in)
	if err != nil {
		return nil, false, fmt.Errorf("compare: the test of independence of %s: %w", e.ID, err)
	}
	raw, err := json.Marshal(res, storedJSON)
	return raw, res.Final, err
}

// declaredPrimary is the metric and the level of a declaration's primary
// result: its own metric or the suite's primary metric, at task level for
// a pair of tasks and a general declaration without strata, and at suite
// level otherwise.
func declaredPrimary(d objects.DeclaredComparison, suitePrimary string) (string, string) {
	metric := cmp.Or(d.Metric, suitePrimary)
	if d.ReferenceTask != "" || (d.General() && len(d.By) == 0) {
		return metric, "task"
	}
	return metric, "suite"
}
