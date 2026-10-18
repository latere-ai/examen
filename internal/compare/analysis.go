// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"

	"latere.ai/x/examen/internal/analysis"
	"latere.ai/x/examen/internal/objects"
)

// Analysis is the analyzer of specs/008-analysis.md: it reads a
// comparison's two sides and a cell's trials from the store, computes them
// with internal/analysis, and answers the records the store keeps.
type Analysis struct{}

// Compare computes a comparison from what both its sides hold now. An
// automatic comparison that is not final waits for its candidate's first
// finished trial and answers ErrNotComputed until then; a final one and
// one on request are computed whatever the sides hold. The parameters a
// comparison recorded when it was first computed are the ones it is
// recomputed with, so a progressive comparison reads one set throughout.
func (Analysis) Compare(ctx context.Context, c objects.Comparison, src Source) (objects.Computed, []string, error) {
	in, err := Input(ctx, c, src)
	if err != nil {
		return objects.Computed{}, nil, err
	}
	if c.Automatic && !c.Final && !finished(in.Candidate) {
		return objects.Computed{}, nil, ErrNotComputed
	}
	out, err := analysis.Compare(in)
	if err != nil {
		return objects.Computed{}, nil, fmt.Errorf("compare: computing %s: %w", c.ID, err)
	}
	read, err := analysis.MeasurementsRead(in)
	if err != nil {
		return objects.Computed{}, nil, fmt.Errorf("compare: the measurements %s read: %w", c.ID, err)
	}
	computed, err := Record(out)
	if err != nil {
		return objects.Computed{}, nil, fmt.Errorf("compare: recording %s: %w", c.ID, err)
	}
	return computed, read, nil
}

// finished reports whether a side holds a finished trial: done with an
// outcome the analysis reads, and not retried.
func finished(s analysis.Side) bool {
	for _, c := range s.Cells {
		for _, t := range c.Trials {
			switch t.Outcome {
			case "", analysis.OutcomeInfraFailed, analysis.OutcomeOverBudget, analysis.OutcomeCanceled:
			default:
				if !t.Retried {
					return true
				}
			}
		}
	}
	return false
}

// Input reads everything a comparison computes from: the candidate's suite
// version, the candidate side's cells in its experiment, and the reference
// side's: the baseline's candidate's cells in the baseline's experiment, at
// that experiment's own producing graders, another candidate's cells of
// the same experiment, or for a comparison across tasks the candidate's
// cell of the reference task.
func Input(ctx context.Context, c objects.Comparison, src Source) (analysis.Input, error) {
	e, err := src.Experiments.Get(ctx, c.Experiment)
	if err != nil {
		return analysis.Input{}, fmt.Errorf("compare: reading the experiment of %s: %w", c.ID, err)
	}
	su, err := readSuite(ctx, src.Versions, e)
	if err != nil {
		return analysis.Input{}, err
	}
	params := analysis.Defaults()
	if len(c.Parameters) > 0 {
		if err := json.Unmarshal(c.Parameters, &params); err != nil {
			return analysis.Input{}, fmt.Errorf("compare: the recorded parameters of %s: %w", c.ID, err)
		}
	}
	in := analysis.Input{Parameters: params, Suite: su.suite, Exploratory: c.Exploratory}
	cand, err := sideOf(ctx, src, e, su, c.Candidate, c.Task)
	if err != nil {
		return analysis.Input{}, err
	}
	var ref side
	switch {
	case c.Baseline != "":
		b, err := src.Baselines.ByID(ctx, c.Baseline)
		if err != nil {
			return analysis.Input{}, fmt.Errorf("compare: reading the baseline of %s: %w", c.ID, err)
		}
		be, err := src.Experiments.Get(ctx, b.Experiment)
		if err != nil {
			return analysis.Input{}, fmt.Errorf("compare: reading the baseline's experiment of %s: %w", c.ID, err)
		}
		bsu, err := readSuite(ctx, src.Versions, be)
		if err != nil {
			return analysis.Input{}, err
		}
		if ref, err = sideOf(ctx, src, be, bsu, b.Candidate, ""); err != nil {
			return analysis.Input{}, err
		}
	case c.ReferenceCandidate != "":
		if ref, err = sideOf(ctx, src, e, su, c.ReferenceCandidate, ""); err != nil {
			return analysis.Input{}, err
		}
	case c.ReferenceTask != "":
		in.AcrossTasks = true
		if ref, err = sideOf(ctx, src, e, su, c.Candidate, c.ReferenceTask); err != nil {
			return analysis.Input{}, err
		}
	default:
		return analysis.Input{}, fmt.Errorf("compare: %s names no reference", c.ID)
	}
	if !in.AcrossTasks && len(c.Variable) > 0 {
		v, err := analysis.LogicalVariable(c.Variable)
		if err != nil {
			return analysis.Input{}, fmt.Errorf("compare: the variable of %s: %w", c.ID, err)
		}
		in.Variable = v
	}
	for _, s := range []struct {
		from side
		into *analysis.Side
	}{{cand, &in.Candidate}, {ref, &in.Reference}} {
		cells, err := cellsOf(ctx, src.Experiments, s.from.suite, s.from.experiment.ID, s.from.cells)
		if err != nil {
			return analysis.Input{}, err
		}
		*s.into = analysis.Side{Candidate: s.from.candidate, Cells: cells}
	}
	if in.AcrossTasks && (len(in.Candidate.Cells) != 1 || len(in.Reference.Cells) != 1) {
		return analysis.Input{}, fmt.Errorf("compare: %s compares two tasks of which the candidate holds %d and %d cells", c.ID, len(in.Candidate.Cells), len(in.Reference.Cells))
	}
	if in.Taxonomies, err = taxonomies(ctx, src.Taxonomies, su); err != nil {
		return analysis.Input{}, err
	}
	return in, nil
}

// Record is a comparison as the store keeps it: every member the analysis
// fills, the variable and declared provenance it resolved among them, and
// its parameters with PERMUTATION_RESAMPLES as resolved for its primary
// result and its family.
func Record(c analysis.Comparison) (objects.Computed, error) {
	single := c.SingleVariable
	out := objects.Computed{
		SingleVariable: &single, Confounds: c.Confounds, Results: make([]objects.ResultRow, 0, len(c.Results)),
		AnalysisVersion: c.AnalysisVersion, InputsDigest: c.InputsDigest, Seed: c.Seed,
		VariableFields: c.Variable, Declared: c.DeclaredProvenance,
	}
	if out.VariableFields == nil {
		out.VariableFields = []string{}
	}
	if out.Declared == nil {
		out.Declared = []string{}
	}
	if out.Confounds == nil {
		out.Confounds = []string{}
	}
	var err error
	raw := func(v any) json.RawMessage {
		if err != nil {
			return nil
		}
		var b []byte
		b, err = json.Marshal(v)
		return b
	}
	for _, r := range c.Results {
		row := objects.ResultRow{
			Level: string(r.Level), Metric: r.Metric, MetricName: r.MetricName, Direction: string(r.Direction), Task: r.Task,
			Candidate: raw(r.Candidate), Reference: raw(r.Reference), DeltaKind: r.DeltaKind, NoDeltaInterval: r.NoDeltaInterval,
			Method: r.Method, Statistic: r.Statistic, Exact: r.Exact, Resamples: r.Resamples, Seed: r.Seed,
			P: r.P, Q: r.Q, H: r.H, Verdict: string(r.Verdict), Reason: r.Reason, Confounds: raw(r.Confounds),
		}
		if r.Confounds == nil {
			row.Confounds = json.RawMessage(`[]`)
		}
		if r.TaskDigests != nil {
			row.TaskDigests = raw(r.TaskDigests)
		}
		if r.Delta != nil {
			row.Delta = raw(r.Delta)
		}
		if r.DeltaInterval != nil {
			row.DeltaInterval = raw(r.DeltaInterval)
		}
		if r.Level == analysis.LevelSuite {
			left := r.LeftOut
			if left == nil {
				left = []analysis.LeftOut{}
			}
			row.LeftOut = raw(left)
		}
		out.Results = append(out.Results, row)
	}
	if c.Taxonomies != nil {
		out.Taxonomies = raw(c.Taxonomies)
	}
	out.UnpairedTasks = raw(c.UnpairedTasks)
	out.CandidateSuite = raw(c.CandidateSuite)
	out.Windows = raw(c.Windows)
	out.VerdictCounts = raw(c.VerdictCounts)
	out.Correction = raw(c.Correction)
	params := map[string]any{}
	if p := raw(c.Parameters); err == nil {
		err = json.Unmarshal(p, &params)
	}
	params["permutation_resamples"] = c.Resamples
	out.Parameters = raw(params)
	return out, err
}

// Summaries are the cells' summaries of one metric, by cell id, each
// computed by spec 008's stored analysis from the cell's trials: by the
// same procedures as a comparison, from the cell's own inputs document, so
// the same trials give the same figures bit for bit. A cell's summary of a
// candidate-level metric is no figure, and is absent.
func (Analysis) Summaries(ctx context.Context, e objects.Experiment, cells []objects.Cell, metric string, src Source) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	if len(cells) == 0 {
		return out, nil
	}
	su, err := readSuite(ctx, src.Versions, e)
	if err != nil {
		return nil, err
	}
	tax, err := taxonomies(ctx, src.Taxonomies, su)
	if err != nil {
		return nil, err
	}
	read, err := cellsOf(ctx, src.Experiments, su, e.ID, cells)
	if err != nil {
		return nil, err
	}
	for i, cell := range read {
		a, err := analysis.AnalyzeCell(analysis.CellInput{Parameters: analysis.Defaults(), Suite: su.suite, Cell: cell, Taxonomies: maps.Clone(tax)})
		if err != nil {
			return nil, fmt.Errorf("compare: the stored analysis of %s: %w", cells[i].ID, err)
		}
		s, ok := a.Summaries[metric]
		if !ok {
			continue
		}
		b, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		out[cells[i].ID] = b
	}
	return out, nil
}
