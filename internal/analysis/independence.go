// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"cmp"
	"fmt"
	"slices"
)

// IndependenceInput is everything the experiment-level test of
// independence reads (spec 008): the experiment's suite, the label metric
// it declared, and one side per level of the cell variable it tests the
// metric against, in order, each holding its cells, named by their
// strata. The suite's tasks are then the strata, each with the task digest
// of its cell on the first side that holds one; with the default
// declaration, the candidate stratified by task, a side is a candidate's
// cells and a stratum a task.
type IndependenceInput struct {
	Parameters Parameters `json:"parameters"`
	Suite      Suite      `json:"suite"`
	// Metric is the digest of a trial-level label metric of the suite.
	Metric string `json:"metric"`
	// Variable, By and Where are the declaration: the cell variable whose
	// levels are the sides, those the strata are made of, and those held
	// to one level.
	Variable string            `json:"variable"`
	By       []string          `json:"by"`
	Where    map[string]string `json:"where,omitempty"`
	Sides    []Side            `json:"sides"`
	// LabelTests is true once label tests are built; until then the
	// result carries its summaries and no p-value or verdict.
	LabelTests   bool                `json:"label_tests,omitzero"`
	Taxonomies   map[string]Taxonomy `json:"taxonomies,omitempty"`
	Calibrations []Calibration       `json:"calibrations,omitempty"`
}

// IndependenceLeftOut is a task of weight above zero the test of
// independence does not include: unpaired when a candidate holds no cell
// of it, too_few_trials otherwise, with each candidate's valued trials.
type IndependenceLeftOut struct {
	Task   string `json:"task"`
	Why    string `json:"why"`
	Counts []int  `json:"counts"`
}

// IndependenceConfound is a confound of the test of independence, with
// one count per candidate, in the experiment's order, and the tasks it
// occurs in.
type IndependenceConfound struct {
	Name   string   `json:"name"`
	Tasks  []string `json:"tasks,omitempty"`
	Counts []int    `json:"counts,omitempty"`
	Grader string   `json:"grader,omitempty"`
}

// IndependenceResult is spec 002's independence result.
type IndependenceResult struct {
	Metric          string                 `json:"metric"`
	MetricName      string                 `json:"metric_name"`
	Variable        string                 `json:"variable"`
	By              []string               `json:"by"`
	Where           map[string]string      `json:"where,omitempty"`
	Levels          []string               `json:"levels"`
	Summaries       []Summary              `json:"summaries"`
	Tasks           int                    `json:"tasks"`
	Coverage        float64                `json:"coverage"`
	LeftOut         []IndependenceLeftOut  `json:"left_out"`
	Method          string                 `json:"method,omitempty"`
	Statistic       string                 `json:"statistic,omitempty"`
	Exact           *bool                  `json:"exact,omitzero"`
	Resamples       *int                   `json:"resamples,omitzero"`
	Seed            string                 `json:"seed"`
	P               *float64               `json:"p,omitzero"`
	Q               *float64               `json:"q,omitzero"`
	Verdict         Verdict                `json:"verdict,omitempty"`
	Reason          string                 `json:"reason"`
	Confounds       []IndependenceConfound `json:"confounds"`
	Final           bool                   `json:"final"`
	AnalysisVersion string                 `json:"analysis_version"`
	Parameters      Parameters             `json:"parameters"`
	InputsDigest    string                 `json:"inputs_digest"`
}

// independenceDocument is the test of independence's inputs document: what
// it reads, as canonical JSON, whose digest seeds its draws.
type independenceDocument struct {
	AnalysisVersion string             `json:"analysis_version"`
	Parameters      Parameters         `json:"parameters"`
	LabelTests      bool               `json:"label_tests"`
	Test            string             `json:"test"`
	Metric          string             `json:"metric"`
	Variable        string             `json:"variable"`
	By              []string           `json:"by"`
	Where           map[string]string  `json:"where,omitempty"`
	Weights         map[string]float64 `json:"weights"`
	Sides           []docSide          `json:"sides"`
}

// scopeExperiment is the seed scope of the experiment-level test.
const scopeExperiment = "experiment"

// Independence computes an experiment's declared test of independence: the
// stratified label test of its candidates over the suite's tasks of weight
// above zero that every candidate holds enough valued trials of, tested
// alone. The same input gives the same result, bit for bit.
func Independence(in IndependenceInput) (IndependenceResult, error) {
	if err := in.Parameters.Validate(); err != nil {
		return IndependenceResult{}, err
	}
	i := slices.IndexFunc(in.Suite.Metrics, func(m Metric) bool { return m.Digest == in.Metric })
	if i < 0 {
		return IndependenceResult{}, fmt.Errorf("%w: the metric %s is not the suite's", ErrInput, in.Metric)
	}
	m := in.Suite.Metrics[i]
	if m.Type != TypeLabel || m.level() != LevelTrial {
		return IndependenceResult{}, fmt.Errorf("%w: a test of independence reads a trial-level label metric, and %s is not one", ErrInput, m.Name)
	}
	if len(in.Sides) < 2 {
		return IndependenceResult{}, fmt.Errorf("%w: a test of independence compares at least two candidates", ErrInput)
	}
	p := in.Parameters
	// A computer of one metric reads each candidate's cells as a
	// comparison's candidate side reads its own, and writes the inputs
	// document's tasks and the suite-level summaries the same way.
	c := &computer{in: Input{Parameters: p, Suite: Suite{Metrics: []Metric{m}, Weights: in.Suite.Weights, Tasks: in.Suite.Tasks, PrimaryMetric: m.Digest},
		Taxonomies: in.Taxonomies, Calibrations: in.Calibrations, LabelTests: in.LabelTests}, p: p, z: p.z()}
	tax := c.taxonomy(m)
	cells := make([]map[string]indepCell, len(in.Sides))
	out := IndependenceResult{
		Metric: m.Digest, MetricName: m.Name, Variable: in.Variable, By: in.By, Where: in.Where, Levels: make([]string, len(in.Sides)), LeftOut: []IndependenceLeftOut{},
		Confounds: []IndependenceConfound{}, Final: true, AnalysisVersion: Version, Parameters: p,
		Method: methodStratified, Statistic: statPearsonX2Sum,
	}
	doc := independenceDocument{AnalysisVersion: Version, Parameters: p, LabelTests: in.LabelTests, Test: "independence", Metric: m.Digest,
		Variable: in.Variable, By: in.By, Where: in.Where,
		Weights: map[string]float64{}, Sides: make([]docSide, len(in.Sides))}
	for s := range in.Sides {
		side := &in.Sides[s]
		out.Levels[s] = side.Candidate
		cells[s] = map[string]indepCell{}
		doc.Sides[s] = docSide{Candidate: side.Candidate, Tasks: []docTask{}, Values: []docMeasurement{}}
		for j := range side.Cells {
			cl := &side.Cells[j]
			ct := sortTrials(cl)
			r := readMetric(cl, ct, m, cl.Producers[m.Digest], tax)
			cells[s][cl.Task] = indepCell{cell: cl, ct: ct, read: r}
		}
	}
	// The tasks of weight above zero, each included when every candidate
	// holds a cell of it with enough valued trials.
	var total, held float64
	var included []string
	// weights are the strata's: a stratum weighs as the task its suite
	// entry names, every side alike.
	weights := map[string]float64{}
	for _, st := range suiteTasksOf(in) {
		w := in.Suite.weight(st.Digest)
		weights[st.Name] = w
		doc.Weights[st.Digest] = w
		if w <= 0 {
			continue
		}
		total += w
		counts := make([]int, len(in.Sides))
		why := ""
		for s := range in.Sides {
			sc, ok := cells[s][st.Name]
			if !ok {
				why = LeftOutUnpaired
				continue
			}
			counts[s] = sc.read.valued()
			out.Final = out.Final && sc.cell.Final
			doc.Sides[s].Tasks = append(doc.Sides[s].Tasks, c.docTaskOf(sc.cell, sc.ct, map[string]*sideRead{m.Digest: sc.read}))
			if why == "" && counts[s] < p.MinTrialsPerTaskInSuite {
				why = LeftOutTooFewTrials
			}
		}
		if why != "" {
			out.LeftOut = append(out.LeftOut, IndependenceLeftOut{Task: st.Name, Why: why, Counts: counts})
			continue
		}
		included = append(included, st.Name)
		held += w
	}
	if total <= 0 {
		return IndependenceResult{}, fmt.Errorf("%w: the suite has no task of weight above zero", ErrInput)
	}
	out.Tasks, out.Coverage = len(included), held/total
	digest, err := documentDigest(doc)
	if err != nil {
		return IndependenceResult{}, err
	}
	out.InputsDigest = digest
	seed := ProcedureSeed(RootSeed(digest), m.Digest, scopeExperiment, procTest)
	out.Seed = seed.String()

	// Each candidate's values of the included tasks, in task order, as
	// positions in one label order over every value read.
	var all [][]value
	for _, name := range included {
		for s := range in.Sides {
			all = append(all, cells[s][name].read.values)
		}
	}
	order := labelOrder(m, tax, all...)
	tasks := make([][][]float64, len(included))
	sides := make([][]sideTask, len(in.Sides))
	lo, hi := 0, 0
	for t, name := range included {
		tasks[t] = make([][]float64, len(in.Sides))
		for s := range in.Sides {
			r := cells[s][name].read
			vals := indices(order, r.values)
			tasks[t][s] = vals
			sides[s] = append(sides[s], sideTask{name: name, w: weights[name], read: r, vals: vals})
			if t == 0 && s == 0 {
				lo, hi = len(vals), len(vals)
			}
			lo, hi = min(lo, len(vals)), max(hi, len(vals))
		}
	}
	out.Summaries = make([]Summary, len(in.Sides))
	for s := range in.Sides {
		out.Summaries[s] = c.suiteLabelSummary(m, sides[s], order, out.Coverage)
	}
	selfReported := independenceConfounds(&out, cells, included, m, in.Calibrations)

	switch {
	case out.Coverage < p.MinSuiteCoverage:
		out.Verdict = Insufficient
		out.Method, out.Statistic = "", ""
		out.Reason = reasonIndependenceCoverage(out.Coverage, p.MinSuiteCoverage, out.LeftOut)
		return out, nil
	case !in.LabelTests:
		out.Method, out.Statistic = "", ""
		out.Reason = reasonLabelsNotBuilt
		return out, nil
	}
	cfg := mcConfig{g: newGenerator(seed), b: resamples(1, p.FalseDiscoveryRate), stop: p.PermutationStopExceedances, tol: p.TieTolerance}
	r, floor := stratifiedLabelTest(tasks, len(order), p.PermutationExactMax, cfg)
	out.Exact, out.Resamples, out.P = ptr(r.exact), ptr(r.resamples), ptr(r.p)
	fdr := p.FalseDiscoveryRate
	switch {
	case selfReported:
		out.Reason = reasonSelfReported(r.p)
	case r.exact && floor > fdr:
		out.Q = ptr(r.p)
		out.Verdict = WithinNoise
		if r.p <= fdr {
			out.Verdict = Changed
		}
		out.Reason = reasonFloor(candidateSizes(len(included), lo, hi), 1, true, fdr)
	case stratifiedX2(tasks, len(order)) == 0:
		out.Q = ptr(r.p)
		out.Verdict = WithinNoise
		out.Reason = reasonNoCandidateDifference
	default:
		out.Q = ptr(r.p)
		out.Verdict = WithinNoise
		if r.p <= fdr {
			out.Verdict = Changed
		}
		out.Reason = reasonIndependence(r.p)
	}
	if !selfReported {
		out.Reason += independenceSuffix(out.Confounds)
	}
	return out, nil
}

// suiteTasksOf is the suite's tasks: its declared ones, or the tasks the
// candidates hold cells of, by name.
func suiteTasksOf(in IndependenceInput) []SuiteTask {
	if len(in.Suite.Tasks) > 0 {
		return in.Suite.Tasks
	}
	seen := map[string]bool{}
	var out []SuiteTask
	for _, s := range in.Sides {
		for _, cl := range s.Cells {
			if !seen[cl.Task] {
				seen[cl.Task] = true
				out = append(out, SuiteTask{Name: cl.Task, Digest: cl.TaskDigest})
			}
		}
	}
	slices.SortFunc(out, func(a, b SuiteTask) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// indepCell is one candidate's cell of a task, as the test of
// independence reads it.
type indepCell struct {
	cell *Cell
	ct   cellTrials
	read *sideRead
}

// stratifiedX2 is the observed statistic of the stratified label test,
// Σ_t X²_t in task order.
func stratifiedX2(tasks [][][]float64, k int) float64 {
	if len(tasks) == 0 {
		return 0
	}
	sc := newX2Scratch(len(tasks[0]), k)
	x := 0.0
	for _, task := range tasks {
		x += labelX2(task, sc)
	}
	return x
}

// independenceConfounds adds the test's confounds over its included tasks:
// trials without a value, self-reported values, excluded trials no
// finished retry replaced, and a judge uncalibrated for the metric. It
// reports whether any value read is self-reported.
func independenceConfounds(out *IndependenceResult, cells []map[string]indepCell, included []string, m Metric, cals []Calibration) bool {
	k := len(cells)
	missing, self, incomplete := make([]int, k), make([]int, k), make([]int, k)
	var missTasks, selfTasks, incTasks []string
	uncalibrated := ""
	for _, name := range included {
		var miss, sr, inc bool
		for s := range cells {
			r := cells[s][name].read
			if n := r.missing(); n > 0 {
				missing[s] += n
				miss = true
			}
			if n := r.selfReported(); n > 0 {
				self[s] += n
				sr = true
			}
			if n := len(cells[s][name].ct.incomplete); n > 0 {
				incomplete[s] += n
				inc = true
			}
			for _, cal := range cals {
				if cal.Metric == m.Digest && cal.Grader == r.grader && !cal.Calibrated {
					uncalibrated = cal.Grader
				}
			}
		}
		if miss {
			missTasks = append(missTasks, name)
		}
		if sr {
			selfTasks = append(selfTasks, name)
		}
		if inc {
			incTasks = append(incTasks, name)
		}
	}
	if len(missTasks) > 0 {
		out.Confounds = append(out.Confounds, IndependenceConfound{Name: confoundMissing, Tasks: missTasks, Counts: missing})
	}
	if len(selfTasks) > 0 {
		out.Confounds = append(out.Confounds, IndependenceConfound{Name: confoundSelfReported, Tasks: selfTasks, Counts: self})
	}
	if len(incTasks) > 0 {
		out.Confounds = append(out.Confounds, IndependenceConfound{Name: confoundIncomplete, Tasks: incTasks, Counts: incomplete})
	}
	if uncalibrated != "" {
		out.Confounds = append(out.Confounds, IndependenceConfound{Name: confoundUncalibrated, Grader: uncalibrated})
	}
	return len(selfTasks) > 0
}
