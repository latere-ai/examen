// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// A benchmark's results table (spec 008, Benchmark entries; spec 013)
// compares entries of different experiments through the values each entry
// copied at submission. It makes no comparison record: it ranks the
// entries by simultaneous rank intervals from Holm-adjusted pair results
// over the tasks every ranked entry covers.

// EntryCandidate is what an entry copied of its candidate, with the
// candidate's tuple fields the pair confounds' variable rule reads.
type EntryCandidate struct {
	Name               string   `json:"name"`
	Kind               string   `json:"kind"`
	Digest             string   `json:"digest"`
	AgentOrImageDigest string   `json:"agent_or_image_digest"`
	Program            string   `json:"program"`
	Model              string   `json:"model"`
	Reasoning          string   `json:"reasoning"`
	Tools              []string `json:"tools"`
	Harness            string   `json:"harness,omitempty"`
	Version            string   `json:"version,omitempty"`
}

// EntryTuple is a task's cell tuple fields outside the candidate.
type EntryTuple struct {
	TaskHash          string   `json:"task_hash"`
	EnvironmentDigest string   `json:"environment_digest"`
	WorkspaceRef      string   `json:"workspace_ref"`
	GraderDigests     []string `json:"grader_digests"`
}

// EntryLayers are the counts a task's confounds read: requests served by
// another model, requests of trials that sent none to the configured
// model, each reasoning value sent with its requests, each service version
// with its finished trials, each declared image with its finished trials,
// the finished trials whose value is self-reported, and the trials
// excluded from analysis that no finished retry replaced.
type EntryLayers struct {
	ServedByOther        int                       `json:"served_by_other"`
	SentNoneToConfigured int                       `json:"sent_none_to_configured"`
	Reasoning            map[string]int            `json:"reasoning,omitempty"`
	ServiceVersions      map[string]map[string]int `json:"service_versions,omitempty"`
	Images               map[string]int            `json:"images,omitempty"`
	SelfReported         int                       `json:"self_reported"`
	Incomplete           int                       `json:"incomplete"`
}

// EntryTask is one task of an entry's values: the primary metric's value
// of each finished trial in trial id order, a number or a boolean, or for
// a cell-level primary metric the canonical label of its of metric, nil for
// a trial without one.
type EntryTask struct {
	Task   string      `json:"task"`
	Weight float64     `json:"weight"`
	Values []any       `json:"values"`
	Tuple  EntryTuple  `json:"tuple"`
	Layers EntryLayers `json:"layers"`
}

// Entry is one accepted entry of a benchmark version.
type Entry struct {
	Submission string         `json:"submission"`
	Candidate  EntryCandidate `json:"candidate"`
	Window     Window         `json:"window"`
	Taxonomy   string         `json:"taxonomy,omitempty"`
	Values     []EntryTask    `json:"values"`
}

// TableInput is what a results table reads: its parameters, the primary
// metric (with its of metric when it is cell-level), the version's
// reference entry and its accepted entries.
type TableInput struct {
	Parameters Parameters `json:"parameters"`
	Metric     Metric     `json:"metric"`
	Of         *Metric    `json:"of,omitempty"`
	Reference  string     `json:"reference,omitempty"`
	Entries    []Entry    `json:"entries"`
}

// Rank is an entry's rank interval.
type Rank struct {
	Low  int `json:"low"`
	High int `json:"high"`
}

// RankedEntry is one ranked entry of the table.
type RankedEntry struct {
	Submission  string  `json:"submission"`
	Order       int     `json:"order"`
	Rank        Rank    `json:"rank"`
	Summary     Summary `json:"summary"`
	Declared    bool    `json:"declared"`
	VsReference *Result `json:"vs_reference,omitempty"`
}

// UnrankedEntry is an entry the table does not rank, and why.
type UnrankedEntry struct {
	Submission string `json:"submission"`
	Why        string `json:"why"`
}

// Common names the tasks every ranked entry is compared over and the share
// of the suite's weight they hold.
type Common struct {
	Tasks []string `json:"tasks"`
	Share float64  `json:"share"`
}

// TableMethod records how a table was computed.
type TableMethod struct {
	Test         string     `json:"test"`
	Statistic    string     `json:"statistic,omitempty"`
	Adjustment   string     `json:"adjustment"`
	Level        float64    `json:"level"`
	Pairs        int        `json:"pairs"`
	Resamples    int        `json:"resamples"`
	Parameters   Parameters `json:"parameters"`
	InputsDigest string     `json:"inputs_digest"`
	Seed         string     `json:"seed"`
}

// Table is a benchmark version's results table.
type Table struct {
	Method    TableMethod     `json:"method"`
	Reference string          `json:"reference,omitempty"`
	Common    Common          `json:"common"`
	Entries   []RankedEntry   `json:"entries"`
	Unranked  []UnrankedEntry `json:"unranked"`
	Pairs     []Result        `json:"pairs"`
}

// entryTask is one task of an entry as the table computes with it.
type entryTask struct {
	name    string
	w       float64
	vals    []float64 // numbers, 0 and 1, or label positions after exclusion
	valued  int
	missing int
	src     *EntryTask
}

// tableComputer holds one table's computation.
type tableComputer struct {
	in       TableInput
	p        Parameters
	z        float64
	root     Seed
	weights  map[string]float64
	total    float64
	order    []string // label order of a cell-level metric
	entries  map[string]map[string]*entryTask
	byID     map[string]*Entry
	covered  map[string][]string
	ranked   []string
	unranked map[string]string
	common   []string
}

// ComputeTable computes a benchmark version's results table from its
// accepted entries, deterministically.
func ComputeTable(in TableInput) (Table, error) {
	if err := in.Parameters.Validate(); err != nil {
		return Table{}, err
	}
	m := in.Metric
	if m.Type == TypeLabel || m.level() == LevelCandidate {
		return Table{}, fmt.Errorf("%w: a benchmark's primary metric is of level trial or cell and not a label", ErrInput)
	}
	if m.level() == LevelCell && (in.Of == nil || in.Of.Digest != m.Of) {
		return Table{}, fmt.Errorf("%w: a cell-level primary metric needs its of metric", ErrInput)
	}
	if m.level() != LevelCell {
		if err := validMetric(m, nil); err != nil {
			return Table{}, err
		}
	}
	tc := &tableComputer{in: in, p: in.Parameters, z: in.Parameters.z(), weights: map[string]float64{}, byID: map[string]*Entry{}, unranked: map[string]string{}}
	entries := slices.Clone(in.Entries)
	slices.SortFunc(entries, func(a, b Entry) int { return cmp.Compare(a.Submission, b.Submission) })
	tc.in.Entries = entries
	digest, err := tc.digest()
	if err != nil {
		return Table{}, err
	}
	tc.root = RootSeed(digest)
	tc.read()
	tc.rank()
	return tc.assemble(digest), nil
}

// digest is the table's inputs document's digest: analysis_version,
// parameters, the suite's weights and primary metric, the reference, and
// each accepted entry's submission id and values, ascending by submission
// id.
func (tc *tableComputer) digest() (string, error) {
	type docEntry struct {
		Submission string      `json:"submission"`
		Values     []EntryTask `json:"values"`
	}
	weights := map[string]float64{}
	doc := struct {
		AnalysisVersion string             `json:"analysis_version"`
		Parameters      Parameters         `json:"parameters"`
		Weights         map[string]float64 `json:"weights"`
		PrimaryMetric   string             `json:"primary_metric"`
		Reference       string             `json:"reference"`
		Entries         []docEntry         `json:"entries"`
	}{AnalysisVersion: Version, Parameters: tc.p, Weights: weights, PrimaryMetric: tc.in.Metric.Digest, Reference: tc.in.Reference, Entries: []docEntry{}}
	for _, e := range tc.in.Entries {
		for _, t := range e.Values {
			weights[t.Task] = t.Weight
		}
		doc.Entries = append(doc.Entries, docEntry{Submission: e.Submission, Values: e.Values})
	}
	return documentDigest(doc)
}

// read reads every entry's values: numbers, true and false, or for a
// cell-level metric the labels left after its exclude_labels, as positions
// in the order of every label of the table.
func (tc *tableComputer) read() {
	m := tc.in.Metric
	tc.entries = map[string]map[string]*entryTask{}
	tc.covered = map[string][]string{}
	if m.level() == LevelCell {
		seen := map[string]bool{}
		for _, e := range tc.in.Entries {
			for _, t := range e.Values {
				for _, v := range t.Values {
					if s, ok := v.(string); ok && !seen[s] {
						seen[s] = true
						tc.order = append(tc.order, s)
					}
				}
			}
		}
		slices.Sort(tc.order)
	}
	at := map[string]int{}
	for i, l := range tc.order {
		at[l] = i
	}
	ex := excludeLabels(m)
	for i := range tc.in.Entries {
		e := &tc.in.Entries[i]
		tc.byID[e.Submission] = e
		tc.entries[e.Submission] = map[string]*entryTask{}
		for j := range e.Values {
			t := &e.Values[j]
			tc.weights[t.Task] = t.Weight
			et := &entryTask{name: t.Task, w: t.Weight, src: t}
			for _, v := range t.Values {
				if v == nil {
					et.missing++
					continue
				}
				et.valued++
				switch x := v.(type) {
				case bool:
					if x {
						et.vals = append(et.vals, 1)
					} else {
						et.vals = append(et.vals, 0)
					}
				case string:
					if !ex[normalizeLabel(x)] {
						et.vals = append(et.vals, float64(at[x]))
					}
				default:
					f, _ := toFloat(x)
					et.vals = append(et.vals, f)
				}
			}
			if m.level() == LevelCell {
				et.valued = len(et.vals)
			}
			tc.entries[e.Submission][t.Task] = et
			if t.Weight > 0 && et.valued >= tc.p.MinTrialsPerTaskInSuite {
				tc.covered[e.Submission] = append(tc.covered[e.Submission], t.Task)
			}
		}
		slices.Sort(tc.covered[e.Submission])
	}
	for _, w := range tc.weights {
		if w > 0 {
			tc.total += w
		}
	}
}

// share is the share of the suite's weight a set of tasks holds.
func (tc *tableComputer) share(tasks []string) float64 {
	if tc.total <= 0 {
		return 0
	}
	s := 0.0
	for _, t := range tasks {
		s += tc.weights[t]
	}
	return s / tc.total
}

// rank decides the ranked entries and the common tasks: an entry with a
// missing or self-reported value on a task of weight above zero, or whose
// covered tasks hold less than MIN_SUITE_COVERAGE, is unranked; the rest,
// the reference first and then by submission id, are ranked while the
// tasks they all cover still hold MIN_SUITE_COVERAGE.
func (tc *tableComputer) rank() {
	var candidates []string
	for _, e := range tc.in.Entries {
		if why := tc.unrankedWhy(&e); why != "" {
			tc.unranked[e.Submission] = why
			continue
		}
		candidates = append(candidates, e.Submission)
	}
	if i := slices.Index(candidates, tc.in.Reference); i > 0 {
		candidates = slices.Concat([]string{tc.in.Reference}, candidates[:i], candidates[i+1:])
	}
	for _, id := range candidates {
		if tc.ranked == nil {
			tc.ranked = []string{id}
			tc.common = slices.Clone(tc.covered[id])
			continue
		}
		var next, lacks []string
		for _, t := range tc.common {
			if slices.Contains(tc.covered[id], t) {
				next = append(next, t)
			} else {
				lacks = append(lacks, t)
			}
		}
		if s := tc.share(next); s < tc.p.MinSuiteCoverage {
			tc.unranked[id] = fmt.Sprintf("covering it would leave the table's common tasks at %s of the suite's weight; %s needed; it lacks %s",
				percent(s), percent(tc.p.MinSuiteCoverage), strings.Join(lacks, ", "))
			continue
		}
		tc.ranked = append(tc.ranked, id)
		tc.common = next
	}
	if tc.common == nil {
		tc.common = []string{}
	}
}

// unrankedWhy says why an entry cannot be ranked by itself, or "".
func (tc *tableComputer) unrankedWhy(e *Entry) string {
	for _, t := range e.Values {
		if t.Weight <= 0 {
			continue
		}
		if tc.entries[e.Submission][t.Task].missing > 0 {
			return fmt.Sprintf("task %s holds a trial without a value of the primary metric", t.Task)
		}
		if t.Layers.SelfReported > 0 {
			return fmt.Sprintf("task %s holds a self-reported value of the primary metric", t.Task)
		}
	}
	if s := tc.share(tc.covered[e.Submission]); s < tc.p.MinSuiteCoverage {
		return fmt.Sprintf("covers %s of the suite's weight; %s needed", percent(s), percent(tc.p.MinSuiteCoverage))
	}
	return ""
}

// side is an entry's tasks of the common set, in name order.
func (tc *tableComputer) side(id string) []entryTask {
	out := make([]entryTask, len(tc.common))
	for i, t := range tc.common {
		out[i] = *tc.entries[id][t]
	}
	return out
}

func entryWeights(side []entryTask) []float64 {
	w := make([]float64, len(side))
	for i, t := range side {
		w[i] = t.w
	}
	return w
}

func entryValues(side []entryTask) [][]float64 {
	v := make([][]float64, len(side))
	for i, t := range side {
		v[i] = t.vals
	}
	return v
}

func entryStrata(side []entryTask) []stratum {
	out := make([]stratum, len(side))
	for i, t := range side {
		out[i] = stratum{w: t.w, k: ones(t.vals), n: len(t.vals)}
	}
	return out
}

// statistic is the per-task statistic of the table's metric.
func (tc *tableComputer) statistic() func([]float64) float64 {
	m := tc.in.Metric
	switch {
	case m.level() == LevelCell:
		return cellStatistic(m.Aggregate)
	case m.Aggregate == AggRate || m.Aggregate == AggMean:
		return mean
	}
	return quantileStat(quantileLevel(m.Aggregate))
}

// summary is an entry's suite-level summary over the common tasks, with
// the interval of the metric's type drawn from g.
func (tc *tableComputer) summary(side []entryTask, g *generator) Summary {
	m := tc.in.Metric
	s := Summary{Aggregate: m.Aggregate, Tasks: ptr(len(side)), Coverage: ptr(tc.share(tc.common)), Final: true}
	for _, t := range side {
		s.N += t.valued + t.missing
		s.Denominator += t.valued
		s.Missing += t.missing
	}
	if len(side) == 0 {
		s.NoInterval = noIntervalNoValue
		return s
	}
	w := entryWeights(side)
	s.Value = weightedMean(w, perTask(side, tc.statistic()))
	switch {
	case m.level() == LevelCell:
		s.NoInterval = noIntervalDescriptive
		if m.Aggregate == AggRepeatRate {
			s.NoInterval = noIntervalRepeatRate
		}
	case m.Aggregate == AggRate:
		v, neff := kish(entryStrata(side))
		k := 0
		for _, t := range side {
			k += ones(t.vals)
		}
		s.Value, s.Numerator = v, ptr(k)
		s.Interval = ptr(wilson(v, neff, tc.z))
	case m.Aggregate == AggMean:
		b := stratifiedBootstrapMean(w, entryValues(side), nil, tc.p.BootstrapResamples, tc.p.alpha(), g)
		if b.ok {
			s.Interval = ptr(b.iv)
		} else {
			s.NoInterval = noIntervalNoSpread(b.noSpread, tc.p.BootstrapResamples)
		}
	default:
		for _, t := range side {
			if m.Aggregate == AggP90 && len(t.vals) < tc.p.MinTrialsForP90Interval {
				s.NoInterval = noIntervalSmallTask(t.name, len(t.vals), tc.p.MinTrialsForP90Interval)
				return s
			}
		}
		s.Interval = ptr(stratifiedPercentile(w, entryValues(side), nil, tc.statistic(), tc.p.BootstrapResamples, tc.p.alpha(), g))
	}
	return s
}

func perTask(side []entryTask, stat func([]float64) float64) []float64 {
	out := make([]float64, len(side))
	for i, t := range side {
		out[i] = stat(t.vals)
	}
	return out
}

// pairOf is one pair result being computed.
type pairOf struct {
	a, b      string
	res       Result
	p, h      float64
	tested    bool
	floor     float64
	floorOK   bool
	floorWhat string
}

// pairResult computes the pair result of two ranked entries, a the one of
// the lower submission id as the candidate side and b the reference side,
// over the common tasks: the suite-level delta interval of the metric's
// type and the stratified permutation of its statistic.
func (tc *tableComputer) pairResult(a, b string, resamplesB int) *pairOf {
	m := tc.in.Metric
	scope := pairScope(a, b)
	seed := func(proc string) Seed { return ProcedureSeed(tc.root, m.Digest, scope, proc) }
	po := &pairOf{a: a, b: b}
	x, y := tc.side(a), tc.side(b)
	po.res = Result{
		Level: LevelSuite, Metric: m.Digest, MetricName: m.Name, Direction: m.Direction, A: a, B: b,
		Candidate: tc.summary(x, newGenerator(seed(procSummaryCandidate))),
		Reference: tc.summary(y, newGenerator(seed(procSummaryReference))),
		Seed:      seed(procTest).String(),
	}
	for _, id := range tc.allTasks() {
		if !slices.Contains(tc.common, id) && tc.weights[id] > 0 {
			ea, eb := tc.entries[a][id], tc.entries[b][id]
			lo := LeftOut{Task: id, Why: LeftOutTooFewTrials}
			if ea != nil {
				lo.Candidate = ea.valued
			}
			if eb != nil {
				lo.Reference = eb.valued
			}
			if ea == nil || eb == nil {
				lo.Why = LeftOutUnpaired
			}
			po.res.LeftOut = append(po.res.LeftOut, lo)
		}
	}
	if len(x) == 0 {
		return po
	}
	w := entryWeights(x)
	stat := tc.statistic()
	d := weightedMean(w, perTask(x, stat)) - weightedMean(w, perTask(y, stat))
	po.res.Delta, po.res.DeltaKind = &Delta{Value: d}, "difference"
	cfg := mcConfig{g: newGenerator(seed(procTest)), b: resamplesB, stop: tc.p.PermutationStopExceedances, tol: tc.p.TieTolerance}
	strat := make([]stratum2, len(x))
	for i := range x {
		strat[i] = stratum2{w: x[i].w, x: x[i].vals, y: y[i].vals}
	}
	var r testResult
	switch {
	case m.level() == LevelCell:
		po.res.NoDeltaInterval = noIntervalDescriptive
		if m.Aggregate != AggRepeatRate {
			return po
		}
		po.res.NoDeltaInterval = noIntervalRepeatRate
		r = stratifiedTest(strat, stratDifference(repeatRate), tc.p.PermutationExactMax, cfg)
		po.res.Statistic = statDifference
	case m.Aggregate == AggRate:
		po.res.DeltaInterval = &DeltaInterval{Interval: agrestiCaffo(entryStrata(x), entryStrata(y), tc.z)}
		r = stratifiedTest(strat, stratDifference(mean), tc.p.PermutationExactMax, cfg)
		po.res.Statistic = statDifference
	case m.Aggregate == AggMean:
		bt := stratifiedBootstrapMean(w, entryValues(x), entryValues(y), tc.p.BootstrapResamples, tc.p.alpha(), newGenerator(seed(procDeltaInterval)))
		if bt.ok {
			po.res.DeltaInterval = &DeltaInterval{Interval: bt.iv}
		} else {
			po.res.NoDeltaInterval = noIntervalNoSpread(bt.noSpread, tc.p.BootstrapResamples)
		}
		r = stratifiedTest(strat, stratWelch, tc.p.PermutationExactMax, cfg)
		po.res.Statistic = statWelchT
	default:
		q := quantileLevel(m.Aggregate)
		small := ""
		for _, t := range slices.Concat(x, y) {
			if m.Aggregate == AggP90 && len(t.vals) < tc.p.MinTrialsForP90Interval && small == "" {
				small = noIntervalSmallTask(t.name, len(t.vals), tc.p.MinTrialsForP90Interval)
			}
		}
		if small != "" {
			po.res.NoDeltaInterval = small
		} else {
			iv := stratifiedPercentile(w, entryValues(x), entryValues(y), stat, tc.p.BootstrapResamples, tc.p.alpha(), newGenerator(seed(procDeltaInterval)))
			po.res.DeltaInterval = &DeltaInterval{Interval: iv}
		}
		bx, by := make([]float64, len(x)), make([]float64, len(y))
		for i := range x {
			bx[i], by[i] = hallSheather(len(x[i].vals), q, tc.z), hallSheather(len(y[i].vals), q, tc.z)
		}
		r = stratifiedTest(strat, stratStudentizedQuantile(q, bx, by), tc.p.PermutationExactMax, cfg)
		po.res.Statistic = statStudentizedQ
	}
	po.res.Method, po.res.Exact, po.res.Resamples = methodStratified, ptr(r.exact), ptr(r.resamples)
	po.p, po.tested = r.p, true
	if r.exact {
		lo, hi, equal := math.MaxInt, 0, true
		for i := range x {
			nx, ny := len(x[i].vals), len(y[i].vals)
			lo, hi = min(lo, nx, ny), max(hi, nx, ny)
			equal = equal && nx == ny
		}
		po.floor = 1 / float64(r.resamples)
		if equal {
			po.floor = 2 / float64(r.resamples)
		}
		po.floorOK, po.floorWhat = true, suiteSizes(len(x), lo, hi)
	}
	po.res.Confounds = tc.pairConfounds(a, b)
	return po
}

// allTasks are every task of the table, by name.
func (tc *tableComputer) allTasks() []string {
	out := make([]string, 0, len(tc.weights))
	for t := range tc.weights {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// agrees reports whether two entries' candidates agree on every field of
// the logical variables named.
func agrees(a, b EntryCandidate, vars ...string) bool {
	tools := func(t []string) string {
		s := slices.Clone(t)
		slices.Sort(s)
		return strings.Join(slices.Compact(s), "\x00")
	}
	for _, v := range vars {
		switch v {
		case "agent":
			if a.Kind != b.Kind || a.AgentOrImageDigest != b.AgentOrImageDigest || a.Program != b.Program {
				return false
			}
		case "model":
			if a.Model != b.Model {
				return false
			}
		case "reasoning":
			if a.Reasoning != b.Reasoning {
				return false
			}
		case "tools":
			if tools(a.Tools) != tools(b.Tools) {
				return false
			}
		}
	}
	return true
}

// pairConfounds names a pair's confounds over the common tasks: the tuple
// fields outside the candidate that differ, the layer confounds from the
// copied counts by the variable rule, incomplete, declared, window and
// taxonomy.
func (tc *tableComputer) pairConfounds(a, b string) []Confound {
	ea, eb := tc.byID[a], tc.byID[b]
	set := newConfoundSet()
	gd := func(g []string) string {
		s := slices.Clone(g)
		slices.Sort(s)
		return strings.Join(s, "\x00")
	}
	declared := sideCount{}
	for _, task := range tc.common {
		ta, tb := tc.entries[a][task].src, tc.entries[b][task].src
		if ta.Tuple.TaskHash != tb.Tuple.TaskHash {
			set.add(FieldTaskHash, task, nil)
		}
		if ta.Tuple.EnvironmentDigest != tb.Tuple.EnvironmentDigest {
			set.add(FieldEnvironmentDigest, task, nil)
		}
		if ta.Tuple.WorkspaceRef != tb.Tuple.WorkspaceRef {
			set.add(FieldWorkspaceRef, task, nil)
		}
		if gd(ta.Tuple.GraderDigests) != gd(tb.Tuple.GraderDigests) {
			set.add(FieldGraderDigests, task, nil)
		}
		la, lb := ta.Layers, tb.Layers
		if c := (sideCount{la.ServedByOther, lb.ServedByOther}); c.any() {
			set.add(layerObservedModel, task, &c)
		}
		if c := (sideCount{la.SentNoneToConfigured, lb.SentNoneToConfigured}); c.any() {
			set.add(layerSentModel, task, &c)
		}
		if agrees(ea.Candidate, eb.Candidate, "agent", "model", "reasoning") {
			if c := (sideCount{onlyIn(la.Reasoning, lb.Reasoning), onlyIn(lb.Reasoning, la.Reasoning)}); c.any() {
				set.add(layerSentReasoning, task, &c)
			}
		}
		if c := copiedServiceVersions(la.ServiceVersions, lb.ServiceVersions); c.any() {
			set.add(layerServiceVersions, task, &c)
		}
		if agrees(ea.Candidate, eb.Candidate, "agent") {
			if c := (sideCount{onlyIn(la.Images, lb.Images), onlyIn(lb.Images, la.Images)}); c.any() {
				set.add(layerDeclaredImage, task, &c)
			}
		}
		if c := (sideCount{la.Incomplete, lb.Incomplete}); c.any() {
			set.add(confoundIncomplete, task, &c)
		}
		if ea.Candidate.Kind == "external" {
			declared.x += tc.entries[a][task].valued
		}
		if eb.Candidate.Kind == "external" {
			declared.y += tc.entries[b][task].valued
		}
	}
	if declared.any() {
		set.set(Confound{Name: confoundDeclared, Candidate: declared.x, Reference: declared.y})
	}
	wa, wb := window{from: ea.Window.From, to: ea.Window.To, ok: true}, window{from: eb.Window.From, to: eb.Window.To, ok: true}
	if windowGap(wa, wb) > time.Duration(tc.p.WindowTolerance) {
		set.set(Confound{Name: confoundWindow, Candidate: ea.Window, Reference: eb.Window})
	}
	if ea.Taxonomy != eb.Taxonomy {
		set.set(Confound{Name: confoundTaxonomy, Candidate: ea.Taxonomy, Reference: eb.Taxonomy})
	}
	return set.list(true)
}

// copiedServiceVersions counts, from copied counts, the finished trials of
// each side carrying a version of a service named on both sides that the
// other side never recorded.
func copiedServiceVersions(a, b map[string]map[string]int) sideCount {
	var c sideCount
	for svc, vs := range a {
		other, ok := b[svc]
		if !ok {
			continue
		}
		c.x += onlyIn(vs, other)
		c.y += onlyIn(other, vs)
	}
	return c
}

// better reports whether a pair's delta favors a in the metric's
// direction, the higher value for direction none.
func better(d float64, dir Direction) bool {
	if dir == LowerIsBetter {
		return d < 0
	}
	return d > 0
}

// reorient reads a pair result the other way, b against a: the delta and
// its interval negated, p and h the same, each confound's counts and the
// summaries on the other side.
func reorient(r Result) Result {
	out := r
	out.A, out.B = r.B, r.A
	out.Candidate, out.Reference = r.Reference, r.Candidate
	if r.Delta != nil {
		out.Delta = &Delta{Value: -r.Delta.Value}
	}
	if r.DeltaInterval != nil {
		out.DeltaInterval = &DeltaInterval{Interval: Interval{-r.DeltaInterval.Interval[1], -r.DeltaInterval.Interval[0]}}
	}
	switch out.Verdict {
	case Improved:
		out.Verdict = Regressed
	case Regressed:
		out.Verdict = Improved
	}
	out.Confounds = make([]Confound, len(r.Confounds))
	for i, c := range r.Confounds {
		c.Candidate, c.Reference = c.Reference, c.Candidate
		out.Confounds[i] = c
	}
	out.LeftOut = slices.Clone(r.LeftOut)
	for i := range out.LeftOut {
		out.LeftOut[i].Candidate, out.LeftOut[i].Reference = r.LeftOut[i].Reference, r.LeftOut[i].Candidate
	}
	return out
}

// assemble computes every pair, adjusts the ranked pairs by Holm's
// procedure, and writes the table.
func (tc *tableComputer) assemble(digest string) Table {
	m := tc.in.Metric
	level := tc.p.alpha()
	k := len(tc.ranked)
	pairs := k * (k - 1) / 2
	b := resamples(pairs, level)
	t := Table{
		Method: TableMethod{
			Test: methodStratified, Adjustment: "holm", Level: level, Pairs: pairs, Resamples: b,
			Parameters: tc.p, InputsDigest: digest, Seed: tc.root.String(),
		},
		Reference: tc.in.Reference,
		Common:    Common{Tasks: tc.common, Share: tc.share(tc.common)},
		Entries:   []RankedEntry{},
		Unranked:  []UnrankedEntry{},
		Pairs:     []Result{},
	}
	ids := make([]string, 0, len(tc.in.Entries))
	for _, e := range tc.in.Entries {
		ids = append(ids, e.Submission)
	}
	isRanked := map[string]bool{}
	for _, id := range tc.ranked {
		isRanked[id] = true
	}
	var ranked []*pairOf
	var all []*pairOf
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			a, bb := ids[i], ids[j]
			if isRanked[a] && isRanked[bb] {
				po := tc.pairResult(a, bb, b)
				ranked = append(ranked, po)
				all = append(all, po)
				continue
			}
			who := a
			if isRanked[a] {
				who = bb
			}
			po := &pairOf{a: a, b: bb, res: Result{
				Level: LevelSuite, Metric: m.Digest, MetricName: m.Name, Direction: m.Direction, A: a, B: bb,
				Verdict: Insufficient, Reason: fmt.Sprintf("entry %s is not ranked: %s", who, tc.unranked[who]),
			}}
			all = append(all, po)
		}
	}
	if len(ranked) > 0 {
		t.Method.Statistic = ranked[0].res.Statistic
	}
	ps, present := make([]float64, len(ranked)), make([]bool, len(ranked))
	for i, po := range ranked {
		ps[i], present[i] = po.p, po.tested
		if !po.tested {
			ps[i] = math.NaN()
		}
	}
	hs := holm(ps, present)
	betterThan := map[string]int{}
	worseThan := map[string]int{}
	for i, po := range ranked {
		res := &po.res
		if !po.tested {
			// A mode share or a count of distinct labels is descriptive.
			res.Reason = reasonModeShare
			if m.Aggregate == AggDistinctCount {
				res.Reason = reasonDistinctCount
			}
			continue
		}
		po.h = hs[i]
		res.P, res.H = ptr(po.p), ptr(po.h)
		res.Verdict = verdict(m.Direction, po.h, level, res.Delta)
		switch {
		case po.floorOK && po.floor > level/float64(max(pairs, 1)):
			res.Reason = reasonFloorPairs(po.floorWhat, pairs)
		case res.Delta.Value == 0:
			res.Reason = reasonZeroDelta
		default:
			res.Reason = reasonPair(po.p, po.h, pairs, level)
		}
		if po.h <= level && res.Delta.Value != 0 {
			if better(res.Delta.Value, m.Direction) {
				betterThan[po.b]++
				worseThan[po.a]++
			} else {
				betterThan[po.a]++
				worseThan[po.b]++
			}
		}
	}
	for _, po := range all {
		t.Pairs = append(t.Pairs, po.res)
	}
	// The order: each ranked entry's summary over the common tasks, best
	// first in the metric's direction, ties in submission id order.
	type row struct {
		id string
		s  Summary
		v  float64
	}
	var rows []row
	for _, id := range tc.ranked {
		g := newGenerator(ProcedureSeed(tc.root, m.Digest, "entry:"+id, procSummaryCandidate))
		s := tc.summary(tc.side(id), g)
		v, _ := s.Number()
		rows = append(rows, row{id, s, v})
	}
	slices.SortStableFunc(rows, func(x, y row) int {
		if x.v != y.v {
			if (m.Direction == LowerIsBetter) == (x.v < y.v) {
				return -1
			}
			return 1
		}
		return cmp.Compare(x.id, y.id)
	})
	for i, r := range rows {
		re := RankedEntry{
			Submission: r.id, Order: i + 1, Summary: r.s,
			Rank:     Rank{Low: 1 + betterThan[r.id], High: k - worseThan[r.id]},
			Declared: tc.byID[r.id].Candidate.Kind == "external",
		}
		if ref := tc.in.Reference; ref != "" && ref != r.id && tc.byID[ref] != nil {
			re.VsReference = tc.versus(t.Pairs, r.id, ref)
		}
		t.Entries = append(t.Entries, re)
	}
	for _, id := range ids {
		if why, ok := tc.unranked[id]; ok {
			t.Unranked = append(t.Unranked, UnrankedEntry{Submission: id, Why: why})
		}
	}
	return t
}

// versus is the stored pair result of an entry against another, oriented
// entry less other.
func (tc *tableComputer) versus(pairs []Result, entry, other string) *Result {
	for _, r := range pairs {
		switch {
		case r.A == entry && r.B == other:
			cp := r
			return &cp
		case r.A == other && r.B == entry:
			cp := reorient(r)
			return &cp
		}
	}
	return nil
}

// ComparePair answers the stored pair result of two accepted entries of a
// table, oriented the first less the second.
func (t Table) ComparePair(a, b string) (Result, bool) {
	tc := &tableComputer{}
	if r := tc.versus(t.Pairs, a, b); r != nil {
		return *r, true
	}
	return Result{}, false
}
