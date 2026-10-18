// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"time"
)

// sideTask is one side's reading of one included task of a suite-level
// figure.
type sideTask struct {
	name string
	w    float64
	read *sideRead
	stat cellStat // a cell-level metric's reading
	vals []float64
}

// sum adds two maps of counts.
func addCounts(dst map[string]int, src map[string]int) map[string]int {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]int{}
	}
	for k, v := range src {
		dst[k] += v
	}
	return dst
}

// suiteBase fills a suite-level summary's counts over a side's included
// tasks.
func suiteBase(m Metric, side []sideTask, coverage float64) Summary {
	s := Summary{Aggregate: m.Aggregate, Final: true, Tasks: ptr(len(side)), Coverage: ptr(coverage)}
	for _, t := range side {
		s.N += t.read.n
		s.NotApplicable += t.read.notApplicable
		s.Missing += t.read.missing()
		s.Excluded = addCounts(s.Excluded, t.read.trials.excluded)
		if m.level() == LevelCell {
			s.Denominator += t.stat.d()
			s.ExcludedLabels = addCounts(s.ExcludedLabels, t.stat.excluded)
		} else {
			s.Denominator += t.read.valued()
		}
		s.Final = s.Final && t.read.cell.Final
	}
	return s
}

func weights(side []sideTask) []float64 {
	w := make([]float64, len(side))
	for i, t := range side {
		w[i] = t.w
	}
	return w
}

func valuesOf(side []sideTask) [][]float64 {
	v := make([][]float64, len(side))
	for i, t := range side {
		v[i] = t.vals
	}
	return v
}

// strata are a binary side's included tasks as weighted counts.
func strata(side []sideTask) []stratum {
	out := make([]stratum, len(side))
	for i, t := range side {
		out[i] = stratum{w: t.w, k: ones(t.vals), n: len(t.vals)}
	}
	return out
}

// smallTask is the first included task, of either side, with fewer values
// than a p90 interval needs.
func smallTask(min int, sides ...[]sideTask) (string, int, bool) {
	for _, side := range sides {
		for _, t := range side {
			if len(t.vals) < min {
				return t.name, len(t.vals), true
			}
		}
	}
	return "", 0, false
}

// perTaskStat is the weighted mean of a statistic over a side's tasks.
func perTaskStat(side []sideTask, stat func([]float64) float64) float64 {
	s := make([]float64, len(side))
	for i, t := range side {
		s[i] = stat(t.vals)
	}
	return weightedMean(weights(side), s)
}

// quantileStat is the type 7 quantile q of unsorted values.
func quantileStat(q float64) func([]float64) float64 {
	return func(v []float64) float64 { return quantile(sorted(v), q) }
}

// suiteSummary is a side's suite-level summary of a metric that is not a
// label: the weighted mean of the per-task summaries with the interval of
// its type, drawn from g.
func (c *computer) suiteSummary(m Metric, side []sideTask, coverage float64, g *generator) Summary {
	s := suiteBase(m, side, coverage)
	if len(side) == 0 {
		s.NoInterval = noIntervalNoValue
		return s
	}
	w := weights(side)
	switch {
	case m.level() == LevelCell:
		s.Value = perTaskStat(side, cellStatistic(m.Aggregate))
		s.NoInterval = noIntervalDescriptive
		if m.Aggregate == AggRepeatRate {
			s.NoInterval = noIntervalRepeatRate
		}
	case m.Aggregate == AggRate:
		v, neff := kish(strata(side))
		k := 0
		for _, t := range side {
			k += ones(t.vals)
		}
		s.Value, s.Numerator = v, ptr(k)
		s.Interval = ptr(wilson(v, neff, c.z))
	case m.Aggregate == AggMean:
		v, _ := stratifiedMean(w, valuesOf(side))
		s.Value = v
		b := stratifiedBootstrapMean(w, valuesOf(side), nil, c.p.BootstrapResamples, c.p.alpha(), g)
		if b.ok {
			s.Interval = ptr(b.iv)
		} else {
			s.NoInterval = noIntervalNoSpread(b.noSpread, c.p.BootstrapResamples)
		}
	default:
		q := quantileLevel(m.Aggregate)
		s.Value = perTaskStat(side, quantileStat(q))
		if name, n, small := smallTask(c.p.MinTrialsForP90Interval, side); m.Aggregate == AggP90 && small {
			s.NoInterval = noIntervalSmallTask(name, n, c.p.MinTrialsForP90Interval)
			break
		}
		iv := stratifiedPercentile(w, valuesOf(side), nil, quantileStat(q), c.p.BootstrapResamples, c.p.alpha(), g)
		s.Interval = ptr(iv)
	}
	return s
}

// suiteLabelSummary is a side's suite-level summary of a label metric: the
// weighted proportion of each label with Wilson's interval on Kish's
// effective sample size.
func (c *computer) suiteLabelSummary(m Metric, side []sideTask, order []string, coverage float64) Summary {
	s := suiteBase(m, side, coverage)
	if len(side) == 0 {
		s.NoInterval = noIntervalNoValue
		return s
	}
	w := weights(side)
	k := len(order)
	props := weightedProportions(w, valuesOf(side), k)
	counts := make([]int, k)
	unmapped := map[string]bool{}
	for _, t := range side {
		for _, v := range t.read.values {
			if v.unmapped {
				unmapped[v.label] = true
				s.Unmapped++
			}
		}
		for _, x := range t.vals {
			counts[int(x)]++
		}
	}
	shares := map[string]float64{}
	best := -1
	for i, l := range order {
		if m.Vocabulary == Open && counts[i] == 0 {
			continue
		}
		st := make([]stratum, len(side))
		for j, t := range side {
			st[j] = stratum{w: t.w, n: len(t.vals)}
			for _, x := range t.vals {
				if int(x) == i {
					st[j].k++
				}
			}
		}
		_, neff := kish(st)
		s.Distribution = append(s.Distribution, LabelShare{
			Label: l, Count: counts[i], Proportion: props[i], Interval: ptr(wilson(props[i], neff, c.z)), Unmapped: unmapped[l],
		})
		shares[l] = props[i]
		if counts[i] > 0 && (best < 0 || props[i] > props[best]) {
			best = i
		}
	}
	s.Value = shares
	if best >= 0 {
		s.Mode = order[best]
	}
	return s
}

// sides reads both sides of a suite-level result's included tasks, the
// values as numbers or, for a label metric and a cell-level metric, as
// positions in the label order of every included value of both sides.
func (c *computer) sides(pl *plan) (x, y []sideTask, order []string) {
	m := pl.metric
	of := c.trialMetric(m)
	if of.Type == TypeLabel {
		var all [][]value
		for _, pt := range pl.tasks {
			r := c.read(pt, m)
			all = append(all, r.x.values, r.y.values)
		}
		order = labelOrder(of, c.taxonomy(of), all...)
	}
	for _, pt := range pl.tasks {
		r := c.read(pt, m)
		tx := sideTask{name: pt.name, w: pt.weight, read: r.x}
		ty := sideTask{name: pt.name, w: pt.weight, read: r.y}
		switch {
		case m.level() == LevelCell:
			tx.stat, ty.stat = readCellStat(m, r.x, order), readCellStat(m, r.y, order)
			tx.vals, ty.vals = tx.stat.labels, ty.stat.labels
		case m.Type == TypeLabel:
			tx.vals, ty.vals = indices(order, r.x.values), indices(order, r.y.values)
		default:
			tx.vals, ty.vals = r.x.nums(), r.y.nums()
		}
		x = append(x, tx)
		y = append(y, ty)
	}
	return x, y, order
}

// stratify pairs the two sides' included tasks for the stratified test.
func stratify(x, y []sideTask) []stratum2 {
	out := make([]stratum2, len(x))
	for i := range x {
		out[i] = stratum2{w: x[i].w, x: x[i].vals, y: y[i].vals}
	}
	return out
}

// stratFloor records an exact stratified test's smallest p-value, doubled
// when every task's two sides are of one size, and the sizes its reason
// names.
func (pl *plan) stratFloor(r testResult, x, y []sideTask) {
	lo, hi := math.MaxInt, 0
	equal := true
	for i := range x {
		nx, ny := len(x[i].vals), len(y[i].vals)
		lo, hi = min(lo, nx, ny), max(hi, nx, ny)
		equal = equal && nx == ny
	}
	pl.setFloor(r, equal)
	pl.floorWhat = suiteSizes(len(x), lo, hi)
}

// computeSuite fills a suite-level result.
func (c *computer) computeSuite(pl *plan) {
	m := pl.metric
	x, y, order := c.sides(pl)
	gx, gy := c.gen(pl, procSummaryCandidate), c.gen(pl, procSummaryReference)
	if m.Type == TypeLabel {
		pl.res.Candidate = c.suiteLabelSummary(m, x, order, pl.coverage)
		pl.res.Reference = c.suiteLabelSummary(m, y, order, pl.coverage)
	} else {
		pl.res.Candidate = c.suiteSummary(m, x, pl.coverage, gx)
		pl.res.Reference = c.suiteSummary(m, y, pl.coverage, gy)
	}
	if len(x) == 0 {
		return
	}
	w := weights(x)
	switch {
	case m.level() == LevelCell:
		stat := cellStatistic(m.Aggregate)
		pl.setDelta(perTaskStat(x, stat) - perTaskStat(y, stat))
		pl.res.NoDeltaInterval = noIntervalDescriptive
		if m.Aggregate != AggRepeatRate {
			return
		}
		pl.res.NoDeltaInterval = noIntervalRepeatRate
		if !pl.runsTest() {
			return
		}
		r := stratifiedTest(stratify(x, y), stratDifference(repeatRate), c.p.PermutationExactMax, c.mc(pl))
		pl.setTest(methodStratified, statDifference, r, true)
		pl.stratFloor(r, x, y)
	case m.Type == TypeLabel:
		c.suiteLabelDelta(pl, x, y, order)
	case m.Aggregate == AggRate:
		sx, _ := kish(strata(x))
		sy, _ := kish(strata(y))
		pl.setDelta(sx - sy)
		if !pl.runsTest() {
			return
		}
		pl.res.DeltaInterval = &DeltaInterval{Interval: agrestiCaffo(strata(x), strata(y), c.z)}
		r := stratifiedTest(stratify(x, y), stratDifference(mean), c.p.PermutationExactMax, c.mc(pl))
		pl.setTest(methodStratified, statDifference, r, true)
		pl.stratFloor(r, x, y)
	case m.Aggregate == AggMean:
		sx, _ := stratifiedMean(w, valuesOf(x))
		sy, _ := stratifiedMean(w, valuesOf(y))
		pl.setDelta(sx - sy)
		if !pl.runsTest() {
			return
		}
		b := stratifiedBootstrapMean(w, valuesOf(x), valuesOf(y), c.p.BootstrapResamples, c.p.alpha(), c.gen(pl, procDeltaInterval))
		if b.ok {
			pl.res.DeltaInterval = &DeltaInterval{Interval: b.iv}
		} else {
			pl.res.NoDeltaInterval = noIntervalNoSpread(b.noSpread, c.p.BootstrapResamples)
		}
		r := stratifiedTest(stratify(x, y), stratWelch, c.p.PermutationExactMax, c.mc(pl))
		pl.setTest(methodStratified, statWelchT, r, true)
		pl.stratFloor(r, x, y)
	default:
		q := quantileLevel(m.Aggregate)
		pl.setDelta(perTaskStat(x, quantileStat(q)) - perTaskStat(y, quantileStat(q)))
		if !pl.runsTest() {
			return
		}
		if name, n, small := smallTask(c.p.MinTrialsForP90Interval, x, y); m.Aggregate == AggP90 && small {
			pl.res.NoDeltaInterval = noIntervalSmallTask(name, n, c.p.MinTrialsForP90Interval)
		} else {
			iv := stratifiedPercentile(w, valuesOf(x), valuesOf(y), quantileStat(q), c.p.BootstrapResamples, c.p.alpha(), c.gen(pl, procDeltaInterval))
			pl.res.DeltaInterval = &DeltaInterval{Interval: iv}
		}
		bx, by := make([]float64, len(x)), make([]float64, len(y))
		for i := range x {
			bx[i] = hallSheather(len(x[i].vals), q, c.z)
			by[i] = hallSheather(len(y[i].vals), q, c.z)
		}
		r := stratifiedTest(stratify(x, y), stratStudentizedQuantile(q, bx, by), c.p.PermutationExactMax, c.mc(pl))
		pl.setTest(methodStratified, statStudentizedQ, r, true)
		pl.stratFloor(r, x, y)
	}
}

// suiteLabelDelta fills a suite-level label result: per label, the
// difference of the weighted proportions with Agresti and Caffo's
// interval, and the stratified permutation of the label distance.
func (c *computer) suiteLabelDelta(pl *plan, x, y []sideTask, order []string) {
	m := pl.metric
	k := len(order)
	w := weights(x)
	px := weightedProportions(w, valuesOf(x), k)
	py := weightedProportions(w, valuesOf(y), k)
	cx, cy := make([]int, k), make([]int, k)
	for i := range x {
		for _, v := range x[i].vals {
			cx[int(v)]++
		}
		for _, v := range y[i].vals {
			cy[int(v)]++
		}
	}
	delta := map[string]float64{}
	ivs := map[string]Interval{}
	pl.zero = true
	for _, l := range perLabel(m, order, cx, cy) {
		d := px[l] - py[l]
		delta[order[l]] = d
		if d != 0 {
			pl.zero = false
		}
		sx, sy := make([]stratum, len(x)), make([]stratum, len(y))
		for i := range x {
			sx[i] = stratum{w: x[i].w, n: len(x[i].vals)}
			sy[i] = stratum{w: y[i].w, n: len(y[i].vals)}
			for _, v := range x[i].vals {
				if int(v) == l {
					sx[i].k++
				}
			}
			for _, v := range y[i].vals {
				if int(v) == l {
					sy[i].k++
				}
			}
		}
		ivs[order[l]] = agrestiCaffo(sx, sy, c.z)
	}
	pl.res.Delta = &Delta{PerLabel: delta}
	pl.res.DeltaKind = "per_label"
	if !pl.runsTest() {
		return
	}
	pl.res.DeltaInterval = &DeltaInterval{PerLabel: ivs}
	r := stratifiedTest(stratify(x, y), labelDistance(k), c.p.PermutationExactMax, c.mc(pl))
	pl.setTest(methodStratified, statLabelDistance, r, true)
	pl.stratFloor(r, x, y)
}

// candidateSuite is the candidate's own suite-level summary of every
// metric that has one, over its suite's tasks of weight above zero that
// hold at least MIN_TRIALS_PER_TASK_IN_SUITE valued trials, whatever the
// reference holds.
func (c *computer) candidateSuite() map[string]Summary {
	out := map[string]Summary{}
	for _, m := range c.in.Suite.Metrics {
		if m.level() == LevelCandidate {
			continue
		}
		of := c.trialMetric(m)
		var total, held float64
		var side []sideTask
		var all []value
		for _, st := range c.suiteTasks() {
			w := c.in.Suite.weight(st.Digest)
			if w <= 0 {
				continue
			}
			total += w
			r := c.own[st.Name+"\x00"+of.Digest]
			if r == nil {
				continue
			}
			side = append(side, sideTask{name: st.Name, w: w, read: r})
			all = append(all, r.values...)
		}
		var order []string
		if of.Type == TypeLabel {
			order = labelOrder(of, c.taxonomy(of), all)
		}
		var kept []sideTask
		for _, t := range side {
			switch {
			case m.level() == LevelCell:
				t.stat = readCellStat(m, t.read, order)
				t.vals = t.stat.labels
			case m.Type == TypeLabel:
				t.vals = indices(order, t.read.values)
			default:
				t.vals = t.read.nums()
			}
			if len(t.vals) >= c.p.MinTrialsPerTaskInSuite {
				kept = append(kept, t)
				held += t.w
			}
		}
		if total <= 0 {
			continue
		}
		g := newGenerator(ProcedureSeed(c.root, m.Digest, "suite", procSummaryCandidate))
		if m.Type == TypeLabel {
			out[m.Digest] = c.suiteLabelSummary(m, kept, order, held/total)
		} else {
			out[m.Digest] = c.suiteSummary(m, kept, held/total, g)
		}
	}
	return out
}

// taskConfounds adds a paired task's confounds to a result: its tuple
// fields outside the variable, the grader each side was read at for the
// metric, its layer confounds, the trials without a value, the
// self-reported values, the excluded trials and an uncalibrated judge.
// named marks a suite-level result, whose entries name their tasks.
func (c *computer) taskConfounds(pl *plan, pt *pairedTask, named bool) {
	task := ""
	if named {
		task = pt.name
	}
	for _, f := range pt.tuple {
		pl.conf.add(f, task, nil)
	}
	r := c.read(pt, pl.metric)
	if r.x.grader != r.y.grader {
		pl.conf.add(FieldGraderDigests, task, nil)
	}
	for _, name := range layerOrder {
		if n, ok := pt.layers[name]; ok {
			pl.conf.add(name, task, &n)
		}
	}
	miss := sideCount{r.x.missing(), r.y.missing()}
	pl.missingN.x += r.x.n
	pl.missingN.y += r.y.n
	if miss.any() {
		pl.conf.add(confoundMissing, task, &miss)
		pl.missing.x += miss.x
		pl.missing.y += miss.y
	}
	if sr := (sideCount{r.x.selfReported(), r.y.selfReported()}); sr.any() {
		pl.conf.add(confoundSelfReported, task, &sr)
	}
	inc := sideCount{len(pt.xt.incomplete), len(pt.yt.incomplete)}
	pl.rows.x += len(pt.xt.finished) + inc.x
	pl.rows.y += len(pt.yt.finished) + inc.y
	if inc.any() {
		pl.conf.add(confoundIncomplete, task, &inc)
		pl.incomplete.x += inc.x
		pl.incomplete.y += inc.y
	}
	of := c.trialMetric(pl.metric)
	if of.Type == TypeLabel {
		for _, cal := range c.in.Calibrations {
			if cal.Metric == of.Digest && !cal.Calibrated && (cal.Grader == r.x.grader || cal.Grader == r.y.grader) {
				pl.conf.set(Confound{Name: confoundUncalibrated, Grader: cal.Grader})
				pl.uncalibr = true
			}
		}
	}
}

// windowConfound adds window to a result whose two sides' windows, over
// the trials it read, lie more than WINDOW_TOLERANCE apart at their starts
// or their ends.
func (c *computer) windowConfound(pl *plan) {
	tasks := pl.tasks
	if pl.level == LevelTask {
		tasks = []*pairedTask{pl.task}
	}
	var wx, wy window
	var names []string
	for _, pt := range tasks {
		r := c.read(pt, pl.metric)
		wx.merge(r.x.window())
		wy.merge(r.y.window())
		names = append(names, pt.name)
	}
	if !wx.ok || !wy.ok {
		return
	}
	g := windowGap(wx, wy)
	if g <= time.Duration(c.p.WindowTolerance) {
		return
	}
	cf := Confound{Name: confoundWindow, Candidate: *wx.public(), Reference: *wy.public()}
	if pl.level == LevelSuite {
		cf.Tasks = names
	}
	pl.conf.set(cf)
	pl.windowed, pl.windowGap = true, g
}

// correct corrects the family's p-values together by Benjamini and
// Hochberg's procedure; the primary result's q is its p.
func (c *computer) correct() {
	var family []*plan
	var ps []float64
	for _, pl := range c.plans {
		if pl.inFamily && pl.tested {
			family = append(family, pl)
			ps = append(ps, pl.p)
		}
		if pl.primary && pl.tested {
			pl.q = pl.p
		}
	}
	for i, q := range benjaminiHochberg(ps) {
		family[i].q = q
	}
}

// decide gives a result its verdict and its reason.
func (c *computer) decide(pl *plan) {
	m := pl.metric
	res := &pl.res
	fdr := c.p.FalseDiscoveryRate
	fam := c.family
	switch {
	case pl.insufficient:
		res.Verdict = Insufficient
		res.DeltaInterval, res.NoDeltaInterval = nil, ""
		res.Method, res.Statistic, res.Exact, res.Resamples = "", "", nil, nil
		if pl.level == LevelSuite {
			res.Reason = reasonCoverage(pl.coverage, c.p.MinSuiteCoverage, pl.left)
		} else {
			res.Reason = reasonTaskBelow(pl.belowTask, pl.belowN, c.p.MinTrialsForVerdict)
		}
	case pl.level == LevelCandidate:
		res.Reason = reasonCandidate
	case m.Aggregate == AggModeShare:
		res.Reason = reasonModeShare
	case m.Aggregate == AggDistinctCount:
		res.Reason = reasonDistinctCount
	case !pl.tested:
		// A side without a value of a non-descriptive metric cannot reach
		// here: it is insufficient.
		res.Reason = reasonZeroDelta
	case pl.selfReported:
		res.P = ptr(pl.p)
		res.Reason = reasonSelfReported(pl.p)
	default:
		res.P, res.Q = ptr(pl.p), ptr(pl.q)
		res.Verdict = verdict(m.Direction, pl.q, fdr, res.Delta)
		threshold := fdr / float64(max(fam, 1))
		if pl.primary {
			threshold = fdr
		}
		switch {
		case pl.floorOK && pl.floor > threshold:
			res.Reason = reasonFloor(pl.floorWhat, fam, pl.primary, fdr)
		case pl.zero:
			res.Reason = reasonZeroDelta
		case pl.primary:
			res.Reason = reasonPrimary(pl.p)
		case pl.level == LevelSuite && m.Digest == c.in.Suite.PrimaryMetric:
			res.Reason = reasonExploratoryPrimary(pl.p, pl.q, fam, fdr)
		default:
			res.Reason = reasonFamily(pl.p, pl.q, fam, fdr)
		}
	}
	if pl.conf == nil {
		return
	}
	if pl.missing.any() {
		res.Reason += suffixMissing(pl.missing.x, pl.missingN.x, pl.missing.y, pl.missingN.y)
	}
	if pl.incomplete.any() {
		res.Reason += suffixIncomplete(pl.incomplete.x, pl.rows.x, pl.incomplete.y, pl.rows.y)
	}
	if pl.windowed {
		res.Reason += suffixWindow(pl.windowGap)
	}
	if pl.uncalibr {
		res.Reason += suffixUncalibrated
	}
}

// verdict is spec 008's rule for a tested result: changed or within_noise
// for a metric without a direction; improved, regressed or within_noise by
// the sign of the delta otherwise; a zero delta is always within_noise.
func verdict(d Direction, q, fdr float64, delta *Delta) Verdict {
	if delta == nil || q > fdr {
		return WithinNoise
	}
	sign := 0.0
	if delta.PerLabel != nil {
		for _, v := range delta.PerLabel {
			if v != 0 {
				sign = 1
			}
		}
	} else {
		sign = delta.Value
	}
	switch {
	case sign == 0:
		return WithinNoise
	case d == NoDirection || delta.PerLabel != nil:
		return Changed
	case (sign > 0) == (d == HigherIsBetter):
		return Improved
	}
	return Regressed
}
