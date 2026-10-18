// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"slices"
	"time"
)

// plan is one result being computed.
type plan struct {
	level    Level
	metric   Metric
	task     *pairedTask   // a task-level result's task
	tasks    []*pairedTask // a suite-level result's included tasks
	left     []LeftOut
	coverage float64
	res      Result

	insufficient, descriptive, selfReported, primary, inFamily bool

	b      int // relabelings of a Monte Carlo test
	tested bool
	p, q   float64
	zero   bool // the delta is zero

	floor     float64 // the smallest p-value an exact test can give
	floorOK   bool
	floorWhat string

	conf               *confoundSet
	belowTask          string
	belowN             int
	missing, missingN  sideCount
	incomplete, rows   sideCount
	windowGap          time.Duration
	windowed, uncalibr bool
}

// suiteTasks are the candidate suite's tasks: its declared ones, or the
// candidate side's cells.
func (c *computer) suiteTasks() []SuiteTask {
	if len(c.in.Suite.Tasks) > 0 {
		return c.in.Suite.Tasks
	}
	out := make([]SuiteTask, len(c.in.Candidate.Cells))
	for i, cell := range c.in.Candidate.Cells {
		out[i] = SuiteTask{Name: cell.Task, Digest: cell.TaskDigest}
	}
	slices.SortFunc(out, func(a, b SuiteTask) int {
		if a.Name < b.Name {
			return -1
		}
		if a.Name > b.Name {
			return 1
		}
		return 0
	})
	return out
}

// valuedFor is a side's valued trials of a metric on a paired task: the
// denominator of a cell-level metric, the valued trials otherwise.
func (c *computer) valuedFor(pt *pairedTask, m Metric) (int, int) {
	r := c.read(pt, m)
	if m.level() == LevelCell {
		x, y := c.cellStats(pt, m)
		return x.d(), y.d()
	}
	return r.x.valued(), r.y.valued()
}

// cellStats reads a cell-level metric on both sides of a paired task.
func (c *computer) cellStats(pt *pairedTask, m Metric) (cellStat, cellStat) {
	r := c.read(pt, m)
	of := c.trialMetric(m)
	order := labelOrder(of, c.taxonomy(of), r.x.values, r.y.values)
	return readCellStat(m, r.x, order), readCellStat(m, r.y, order)
}

// plan lays out every result: per metric in the suite's order, a
// task-level result per paired task, then the suite-level result; a
// candidate-level metric's one result.
func (c *computer) plan() {
	for _, m := range c.in.Suite.Metrics {
		if m.level() == LevelCandidate {
			c.plans = append(c.plans, &plan{level: LevelCandidate, metric: m})
			continue
		}
		for _, pt := range c.tasks {
			c.plans = append(c.plans, &plan{level: LevelTask, metric: m, task: pt})
		}
		if c.in.AcrossTasks {
			continue
		}
		if pl := c.suitePlan(m); pl != nil {
			c.plans = append(c.plans, pl)
		}
	}
}

// suitePlan lays out a suite-level result: the suite's tasks of weight
// above zero that are paired and hold at least MIN_TRIALS_PER_TASK_IN_SUITE
// valued trials on both sides are included, the others left out with why,
// and coverage is the included tasks' share of the weight.
func (c *computer) suitePlan(m Metric) *plan {
	pl := &plan{level: LevelSuite, metric: m}
	var total, held float64
	candCells := map[string]bool{}
	for _, cell := range c.in.Candidate.Cells {
		candCells[cell.Task] = true
	}
	for _, st := range c.suiteTasks() {
		w := c.in.Suite.weight(st.Digest)
		if w <= 0 {
			continue
		}
		total += w
		pt, ok := c.byName[st.Name]
		if !ok {
			lo := LeftOut{Task: st.Name, Why: LeftOutUnpaired, unpairedBy: "reference"}
			if !candCells[st.Name] {
				lo.unpairedBy = "candidate"
			} else if r := c.own[st.Name+"\x00"+c.trialMetric(m).Digest]; r != nil {
				lo.Candidate = r.valued()
			}
			pl.left = append(pl.left, lo)
			continue
		}
		nx, ny := c.valuedFor(pt, m)
		if nx < c.p.MinTrialsPerTaskInSuite || ny < c.p.MinTrialsPerTaskInSuite {
			pl.left = append(pl.left, LeftOut{Task: st.Name, Why: LeftOutTooFewTrials, Candidate: nx, Reference: ny})
			continue
		}
		pl.tasks = append(pl.tasks, pt)
		held += w
	}
	if total <= 0 {
		return nil
	}
	pl.coverage = held / total
	return pl
}

// classify decides each result's part before any test runs, from its
// counts, its values' provenance and its comparison alone: insufficient,
// descriptive, self-reported, the primary result, or a member of the
// family; and so the family's size m and each test's relabelings.
func (c *computer) classify() {
	for _, pl := range c.plans {
		m := pl.metric
		switch pl.level {
		case LevelCandidate:
			pl.descriptive = true
			continue
		case LevelTask:
			nx, ny := c.valuedFor(pl.task, m)
			if nx < c.p.MinTrialsForVerdict || ny < c.p.MinTrialsForVerdict {
				pl.insufficient = true
				pl.belowTask = pl.task.x.Task
				pl.belowN = nx
				if ny < nx {
					pl.belowTask, pl.belowN = pl.task.y.Task, ny
				}
			}
			r := c.read(pl.task, m)
			pl.selfReported = r.x.selfReported()+r.y.selfReported() > 0
		case LevelSuite:
			pl.insufficient = pl.coverage < c.p.MinSuiteCoverage
			for _, pt := range pl.tasks {
				r := c.read(pt, m)
				if r.x.selfReported()+r.y.selfReported() > 0 {
					pl.selfReported = true
				}
			}
			pl.primary = m.Digest == c.in.Suite.PrimaryMetric && !c.in.Exploratory
		}
		if pl.level == LevelTask && c.in.AcrossTasks && c.in.Declared > 0 {
			// A declared comparison across tasks has no suite-level
			// result: its primary result is the primary metric's one
			// task-level result.
			pl.primary = m.Digest == c.in.Suite.PrimaryMetric
		}
		pl.descriptive = m.Aggregate == AggModeShare || m.Aggregate == AggDistinctCount ||
			(m.Type == TypeLabel && !c.in.LabelTests)
		pl.inFamily = !pl.insufficient && !pl.descriptive && !pl.selfReported && !pl.primary
	}
	for _, pl := range c.plans {
		if pl.inFamily {
			c.family++
		}
	}
	alone, family := resamples(c.primaryM(), c.p.FalseDiscoveryRate), resamples(c.family, c.p.FalseDiscoveryRate)
	for _, pl := range c.plans {
		pl.b = alone
		if pl.inFamily {
			pl.b = family
		}
	}
}

// seed is a result's procedure seed.
func (c *computer) seed(pl *plan, procedure string) Seed {
	scope := "suite"
	switch pl.level {
	case LevelTask:
		scope = pl.task.scope
	case LevelCandidate:
		scope = "candidate"
	}
	return ProcedureSeed(c.root, pl.metric.Digest, scope, procedure)
}

func (c *computer) gen(pl *plan, procedure string) *generator {
	return newGenerator(c.seed(pl, procedure))
}

// mcConfig is a result's Monte Carlo configuration, drawn from its test
// seed.
func (c *computer) mc(pl *plan) mcConfig {
	return mcConfig{g: c.gen(pl, procTest), b: pl.b, stop: c.p.PermutationStopExceedances, tol: c.p.TieTolerance}
}

// compute fills a result's summaries, delta, delta interval and test.
func (c *computer) compute(pl *plan) {
	m := pl.metric
	pl.res = Result{Level: pl.level, Metric: m.Digest, MetricName: m.Name, Direction: m.Direction}
	pl.res.Seed = c.seed(pl, procTest).String()
	pl.conf = newConfoundSet()
	switch pl.level {
	case LevelCandidate:
		c.computeCandidate(pl)
		return
	case LevelTask:
		pl.res.Task = pl.task.name
		pl.res.TaskDigests = &TaskDigests{Candidate: pl.task.x.TaskDigest, Reference: pl.task.y.TaskDigest}
		c.computeTask(pl)
		c.taskConfounds(pl, pl.task, false)
	case LevelSuite:
		pl.res.LeftOut = pl.left
		c.computeSuite(pl)
		for _, pt := range pl.tasks {
			c.taskConfounds(pl, pt, true)
		}
		c.windowConfound(pl)
	}
	if pl.level == LevelTask {
		c.windowConfound(pl)
	}
	pl.res.Confounds = pl.conf.list(pl.level == LevelSuite)
}

// setTest records a test's outcome on a result.
func (pl *plan) setTest(method, statistic string, r testResult, withResamples bool) {
	pl.res.Method, pl.res.Statistic = method, statistic
	pl.res.Exact = ptr(r.exact)
	if withResamples {
		pl.res.Resamples = ptr(r.resamples)
	}
	pl.p, pl.tested = r.p, true
}

// setFloor records an exact permutation test's smallest p-value: one
// relabeling of all, two when the sides are of one size, since swapping
// them gives the same discrepancy.
func (pl *plan) setFloor(r testResult, equalSides bool) {
	if !r.exact {
		return
	}
	pl.floor = 1 / float64(r.resamples)
	if equalSides {
		pl.floor = 2 / float64(r.resamples)
	}
	pl.floorOK = true
}

// setDelta records a numeric delta.
func (pl *plan) setDelta(d float64) {
	pl.res.Delta = &Delta{Value: d}
	pl.res.DeltaKind = "difference"
	pl.zero = d == 0
}

// runsTest reports whether a result is tested: neither insufficient nor
// descriptive.
func (pl *plan) runsTest() bool { return !pl.insufficient && !pl.descriptive }

// computeCandidate is a candidate-level result: both sides' values and the
// delta of a numeric metric, descriptive.
func (c *computer) computeCandidate(pl *plan) {
	m := pl.metric
	x := candidateValue(&c.in.Candidate, m, c.in.Candidate.Producers[m.Digest])
	y := candidateValue(&c.in.Reference, m, c.in.Reference.Producers[m.Digest])
	pl.res.Candidate, pl.res.Reference = candidateSummary(x), candidateSummary(y)
	if x != nil && y != nil {
		fx, okx := toFloat(x.Value)
		fy, oky := toFloat(y.Value)
		if okx && oky && m.Type != TypeBinary {
			pl.setDelta(fx - fy)
		}
	}
}

// computeTask fills a task-level result by the metric's type and
// aggregate.
func (c *computer) computeTask(pl *plan) {
	m := pl.metric
	pt := pl.task
	r := c.read(pt, m)
	switch {
	case m.level() == LevelCell:
		c.taskCellLevel(pl, pt)
	case m.Type == TypeLabel:
		c.taskLabel(pl, r)
	default:
		pl.res.Candidate = cellSummary(m, r.x, c.p, c.gen(pl, procSummaryCandidate))
		pl.res.Reference = cellSummary(m, r.y, c.p, c.gen(pl, procSummaryReference))
		x, y := r.x.nums(), r.y.nums()
		if len(x) == 0 || len(y) == 0 {
			return
		}
		switch m.Aggregate {
		case AggRate:
			c.taskRate(pl, x, y)
		case AggMean:
			c.taskMean(pl, x, y)
		default:
			c.taskQuantile(pl, x, y)
		}
	}
}

// boschlooFloor is the smallest p-value Boschloo's test gives sides of n_x
// and n_y trials: the table of every candidate trial true and every
// reference trial false, on the fixed grid. It depends on the sizes alone,
// so a comparison computes it once per pair of sizes.
func (c *computer) boschlooFloor(nx, ny int) float64 {
	key := [2]int{nx, ny}
	if f, ok := c.floors[key]; ok {
		return f
	}
	f, _ := boschloo(nx, nx, 0, ny, c.p.BoschlooGridPoints, c.p.TieTolerance)
	if c.floors == nil {
		c.floors = map[[2]int]float64{}
	}
	c.floors[key] = f
	return f
}

// ones counts the true values of a binary side.
func ones(v []float64) int {
	k := 0
	for _, x := range v {
		if x == 1 {
			k++
		}
	}
	return k
}

func (c *computer) taskRate(pl *plan, x, y []float64) {
	kx, ky := ones(x), ones(y)
	nx, ny := len(x), len(y)
	pl.setDelta(float64(kx)/float64(nx) - float64(ky)/float64(ny))
	if !pl.runsTest() {
		return
	}
	pl.res.DeltaInterval = &DeltaInterval{Interval: newcombe(kx, nx, ky, ny, c.z)}
	p, _ := boschloo(kx, nx, ky, ny, c.p.BoschlooGridPoints, c.p.TieTolerance)
	pl.setTest(methodBoschloo, statFisherP, testResult{p: p, exact: true}, false)
	pl.floor, pl.floorOK = c.boschlooFloor(nx, ny), true
	pl.floorWhat = sizes(nx, ny)
}

func (c *computer) taskMean(pl *plan, x, y []float64) {
	pl.setDelta(mean(x) - mean(y))
	if !pl.runsTest() {
		return
	}
	b := bootstrapMeanDiff(x, y, c.p.BootstrapResamples, c.p.alpha(), c.gen(pl, procDeltaInterval))
	if b.ok {
		pl.res.DeltaInterval = &DeltaInterval{Interval: b.iv}
	} else {
		pl.res.NoDeltaInterval = noIntervalNoSpread(b.noSpread, c.p.BootstrapResamples)
	}
	r := permutationTest(x, y, welchT, c.p.PermutationExactMax, c.mc(pl))
	pl.setTest(methodPerm, statWelchT, r, true)
	pl.setFloor(r, len(x) == len(y))
	pl.floorWhat = sizes(len(x), len(y))
}

func (c *computer) taskQuantile(pl *plan, x, y []float64) {
	m := pl.metric
	q := quantileLevel(m.Aggregate)
	sx, sy := sorted(x), sorted(y)
	pl.setDelta(quantile(sx, q) - quantile(sy, q))
	if !pl.runsTest() {
		return
	}
	level := math.Sqrt(c.p.ConfidenceLevel)
	ix, okx := orderStatInterval(sx, q, level, c.p.TieTolerance)
	iy, oky := orderStatInterval(sy, q, level, c.p.TieTolerance)
	if okx && oky {
		pl.res.DeltaInterval = &DeltaInterval{Interval: Interval{ix[0] - iy[1], ix[1] - iy[0]}}
	} else {
		pl.res.NoDeltaInterval = noIntervalQuantileDelta(minRanks(q, level, c.p.TieTolerance), m.Aggregate)
	}
	stat := studentizedQuantile(q, hallSheather(len(x), q, c.z), hallSheather(len(y), q, c.z))
	r := permutationTest(x, y, stat, c.p.PermutationExactMax, c.mc(pl))
	pl.setTest(methodPerm, statStudentizedQ, r, true)
	pl.setFloor(r, len(x) == len(y))
	pl.floorWhat = sizes(len(x), len(y))
}

// indices writes labels as their positions in the order.
func indices(order []string, vs []value) []float64 {
	at := map[string]int{}
	for i, l := range order {
		at[l] = i
	}
	out := make([]float64, len(vs))
	for i, v := range vs {
		out[i] = float64(at[v.label])
	}
	return out
}

// perLabel lists the labels a label result reports: every label with a
// count on either side, and every declared label of a closed vocabulary.
func perLabel(m Metric, order []string, cx, cy []int) []int {
	var out []int
	for i := range order {
		if cx[i]+cy[i] > 0 || (m.Vocabulary != Open && slices.Contains(m.Labels, order[i])) {
			out = append(out, i)
		}
	}
	return out
}

func (c *computer) taskLabel(pl *plan, r *pairRead) {
	m := pl.metric
	order := labelOrder(m, c.taxonomy(m), r.x.values, r.y.values)
	pl.res.Candidate = labelSummary(m, r.x, order, c.z)
	pl.res.Reference = labelSummary(m, r.y, order, c.z)
	nx, ny := r.x.valued(), r.y.valued()
	if nx == 0 || ny == 0 {
		return
	}
	cx, _ := labelCounts(order, r.x.values)
	cy, _ := labelCounts(order, r.y.values)
	delta := map[string]float64{}
	ivs := map[string]Interval{}
	pl.zero = true
	for _, i := range perLabel(m, order, cx, cy) {
		d := float64(cx[i])/float64(nx) - float64(cy[i])/float64(ny)
		delta[order[i]] = d
		if d != 0 {
			pl.zero = false
		}
		ivs[order[i]] = newcombe(cx[i], nx, cy[i], ny, c.z)
	}
	pl.res.Delta = &Delta{PerLabel: delta}
	pl.res.DeltaKind = "per_label"
	if pl.insufficient {
		return
	}
	pl.res.DeltaInterval = &DeltaInterval{PerLabel: ivs}
	if pl.descriptive {
		return
	}
	tr, method, stat := labelTest(indices(order, r.x.values), indices(order, r.y.values), len(order), c.p.PermutationExactMax, c.mc(pl))
	pl.setTest(method, stat, tr, true)
}

func (c *computer) taskCellLevel(pl *plan, pt *pairedTask) {
	m := pl.metric
	x, y := c.cellStats(pt, m)
	pl.res.Candidate, pl.res.Reference = cellLevelSummary(m, x), cellLevelSummary(m, y)
	if !x.has(m.Aggregate) || !y.has(m.Aggregate) {
		return
	}
	stat := cellStatistic(m.Aggregate)
	pl.setDelta(stat(x.labels) - stat(y.labels))
	if m.Aggregate != AggRepeatRate {
		pl.res.NoDeltaInterval = noIntervalDescriptive
		return
	}
	if !pl.runsTest() {
		return
	}
	pl.res.NoDeltaInterval = noIntervalRepeatRate
	r := permutationTest(x.labels, y.labels, repeatRateDiff, c.p.PermutationExactMax, c.mc(pl))
	pl.setTest(methodPerm, statDifference, r, true)
	pl.setFloor(r, x.d() == y.d())
	pl.floorWhat = sizes(x.d(), y.d())
}
