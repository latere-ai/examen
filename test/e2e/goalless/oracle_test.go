// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package goalless_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path"
	"slices"
	"strings"
)

// oracle is the study's own figures, transcribed from its RESULTS.md tables
// into a file kept outside this repository (README.md): what the import
// must reproduce, a count equal and a mean or median to the whole line.
type oracle struct {
	// RunDirectories and Experiments are the run directories the study
	// holds and the Examen experiments they become.
	RunDirectories int          `json:"run_directories"`
	Experiments    int          `json:"experiments"`
	Cells          []oracleCell `json:"cells"`
}

// oracleCell is one cell's figures: the run directories under Runs, or
// only those Only names, which are one cell.
type oracleCell struct {
	Runs        string   `json:"runs"`
	Only        []string `json:"only"`
	Finished    *int     `json:"finished"`
	InfraFailed *int     `json:"infra_failed"`
	// LOC are the per-run values in run order, null for a run without one;
	// LOCValues the values in any order, for a table that does not list
	// runs.
	LOC         []*float64 `json:"loc"`
	LOCValues   []float64  `json:"loc_values"`
	LOCValued   *int       `json:"loc_valued"`
	LOCMin      *float64   `json:"loc_min"`
	LOCMax      *float64   `json:"loc_max"`
	LOCMean     *float64   `json:"loc_mean"`
	LOCMedian   *float64   `json:"loc_median"`
	Nonblank    []*float64 `json:"loc_nonblank"`
	NonblankAvg *float64   `json:"loc_nonblank_mean"`
	NonblankMed *float64   `json:"loc_nonblank_median"`
	Implemented *int       `json:"implemented"`
}

func readOracle(p string) (*oracle, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var o oracle
	if err := json.Unmarshal(b, &o); err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	return &o, nil
}

// trial is the part of a listed trial the oracle reads.
type trial struct {
	ID      string         `json:"id"`
	Ref     string         `json:"executor_ref"`
	Outcome string         `json:"outcome"`
	Values  map[string]any `json:"values"`
}

// checkOracle holds the import to the study's figures.
func (a *api) checkOracle(o *oracle, exps []*experiment) {
	a.t.Helper()
	trials := map[string]trial{}
	grids := map[string]map[string]map[[2]string]gridCell{}
	for _, e := range exps {
		for _, t := range all[trial](a, "/v1/experiments/"+e.id+"/trials") {
			trials[t.Ref] = t
		}
		grids[e.id] = map[string]map[[2]string]gridCell{"loc": a.grid(e, "loc"), "loc_nonblank": a.grid(e, "loc_nonblank")}
	}
	matched := 0
	for _, c := range o.Cells {
		var runs []studyRun
		var exp *experiment
		for _, e := range exps {
			for _, r := range e.runs {
				if strings.HasPrefix(r.Ref, c.Runs+"/") && (len(c.Only) == 0 || slices.Contains(c.Only, path.Base(r.Ref))) {
					runs, exp = append(runs, r), e
				}
			}
		}
		if len(runs) == 0 {
			a.t.Errorf("%s: no run imported", c.Runs)
			continue
		}
		cell := [2]string{runs[0].Task, exp.candidates[runs[0].Config.name()]}
		for _, r := range runs {
			if r.Task != cell[0] || exp.candidates[r.Config.name()] != cell[1] {
				a.t.Errorf("%s: its runs fall in more than one cell", c.Runs)
			}
		}
		where := c.Runs
		if len(c.Only) > 0 {
			where += " " + strings.Join(c.Only, ",")
		}
		finished, failed, implemented := 0, 0, 0
		var loc, nonblank []*float64
		for _, r := range runs {
			t := trials[r.Ref]
			if t.Outcome == "infra_failed" {
				failed++
				continue
			}
			finished++
			loc, nonblank = append(loc, number(t.Values["loc"])), append(nonblank, number(t.Values["loc_nonblank"]))
			if t.Values["implemented"] == true {
				implemented++
			}
		}
		check := func(what string, got int, want *int) {
			if want != nil && got != *want {
				a.t.Errorf("%s: %s %d, the study's table %d", where, what, got, *want)
			}
		}
		check("finished", finished, c.Finished)
		check("infra_failed", failed, c.InfraFailed)
		check("implemented", implemented, c.Implemented)
		valued := present(loc)
		check("valued loc", len(valued), c.LOCValued)
		if c.LOC != nil && !equalRuns(loc, c.LOC) {
			a.t.Errorf("%s: loc per run %v, the study's table %v", where, show(loc), show(c.LOC))
		}
		if c.Nonblank != nil && !equalRuns(nonblank, c.Nonblank) {
			a.t.Errorf("%s: loc_nonblank per run %v, the study's table %v", where, show(nonblank), show(c.Nonblank))
		}
		if c.LOCValues != nil {
			got, want := slices.Sorted(slices.Values(valued)), slices.Sorted(slices.Values(c.LOCValues))
			if !slices.Equal(got, want) {
				a.t.Errorf("%s: loc values %v, the study's table %v", where, got, want)
			}
		}
		if len(valued) > 0 {
			if c.LOCMin != nil && slices.Min(valued) != *c.LOCMin {
				a.t.Errorf("%s: loc from %v, the study's table %v", where, slices.Min(valued), *c.LOCMin)
			}
			if c.LOCMax != nil && slices.Max(valued) != *c.LOCMax {
				a.t.Errorf("%s: loc to %v, the study's table %v", where, slices.Max(valued), *c.LOCMax)
			}
		}
		a.checkRounded(where+" loc", grids[exp.id]["loc"][cell].Summary, c.LOCMean, c.LOCMedian)
		a.checkRounded(where+" loc_nonblank", grids[exp.id]["loc_nonblank"][cell].Summary, c.NonblankAvg, c.NonblankMed)
		matched++
	}
	a.t.Logf("held %d cells to the study's figures", matched)
}

// checkRounded holds a stored summary's mean and median, rounded to whole
// lines, to the study's.
func (a *api) checkRounded(where string, s *summary, mean, median *float64) {
	a.t.Helper()
	if mean == nil && median == nil {
		return
	}
	if s == nil {
		a.t.Errorf("%s: no summary", where)
		return
	}
	if mean != nil && (s.Mean == nil || math.Round(*s.Mean) != *mean) {
		a.t.Errorf("%s: mean %v, the study's table %v", where, deref(s.Mean), *mean)
	}
	if median != nil && (s.Value == nil || math.Round(*s.Value) != *median) {
		a.t.Errorf("%s: median %v, the study's table %v", where, deref(s.Value), *median)
	}
}

func number(v any) *float64 {
	if f, ok := v.(float64); ok {
		return &f
	}
	return nil
}

func present(vs []*float64) []float64 {
	var out []float64
	for _, v := range vs {
		if v != nil {
			out = append(out, *v)
		}
	}
	return out
}

func equalRuns(got, want []*float64) bool {
	return slices.EqualFunc(got, want, func(a, b *float64) bool { return (a == nil) == (b == nil) && (a == nil || *a == *b) })
}

func show(vs []*float64) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = "null"
		if v != nil {
			out[i] = fmt.Sprint(*v)
		}
	}
	return out
}

func deref(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}
