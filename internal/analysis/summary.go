// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"maps"
	"math"
	"slices"
)

func ptr[T any](v T) *T { return &v }

// quantileLevel is the q of a quantile aggregate.
func quantileLevel(a Aggregate) float64 {
	if a == AggP90 {
		return 0.9
	}
	return 0.5
}

// minRanks is the fewest values whose order statistics bound the quantile q
// at the level: the size a delta of two quantiles names in its sentence.
func minRanks(q, level, tol float64) int {
	for n := 2; n <= 10000; n++ {
		if _, _, _, ok := osRanks(n, q, level, tol); ok {
			return n
		}
	}
	return 0
}

// base fills a summary's counts from a reading.
func base(m Metric, r *sideRead) Summary {
	s := Summary{
		Aggregate:     m.Aggregate,
		N:             r.n,
		NotApplicable: r.notApplicable,
		Denominator:   r.valued(),
		Missing:       r.missing(),
		Excluded:      maps.Clone(r.trials.excluded),
		Final:         r.cell.Final,
	}
	return s
}

// numericFigures fills a numeric summary's mean, sd, min and max.
func numericFigures(s *Summary, v []float64) {
	if len(v) == 0 {
		return
	}
	s.Mean = ptr(mean(v))
	if len(v) > 1 {
		s.SD = ptr(math.Sqrt(variance(v)))
	}
	s.Min = ptr(slices.Min(v))
	s.Max = ptr(slices.Max(v))
}

// cellSummary is a cell's summary of a trial-level metric that is not a
// label: a rate with Wilson's interval, a mean with the bootstrap-t
// interval drawn from g, or a quantile with the order-statistic interval.
func cellSummary(m Metric, r *sideRead, p Parameters, g *generator) Summary {
	s := base(m, r)
	v := r.nums()
	if len(v) == 0 {
		s.NoInterval = noIntervalNoValue
		return s
	}
	switch m.Aggregate {
	case AggRate:
		k := 0
		for _, x := range v {
			if x == 1 {
				k++
			}
		}
		s.Numerator = ptr(k)
		s.Value = float64(k) / float64(len(v))
		s.Interval = ptr(wilsonCount(k, len(v), p.z()))
		return s
	case AggMean:
		numericFigures(&s, v)
		s.Value = mean(v)
		b := bootstrapMean(v, p.BootstrapResamples, p.alpha(), g)
		if b.ok {
			s.Interval = ptr(b.iv)
		} else {
			s.NoInterval = noIntervalNoSpread(b.noSpread, p.BootstrapResamples)
		}
		return s
	}
	numericFigures(&s, v)
	q := quantileLevel(m.Aggregate)
	sv := sorted(v)
	s.Value = quantile(sv, q)
	switch {
	case m.Aggregate == AggP90 && len(v) < p.MinTrialsForP90Interval:
		s.NoInterval = noIntervalP90(p.MinTrialsForP90Interval)
	default:
		if iv, ok := orderStatInterval(sv, q, p.ConfidenceLevel, p.TieTolerance); ok {
			s.Interval = ptr(iv)
		} else {
			s.NoInterval = noIntervalRanks(len(v), m.Aggregate, p.ConfidenceLevel)
		}
	}
	return s
}

// labelCounts counts the labels of values by the order given.
func labelCounts(order []string, vs []value) ([]int, map[string]bool) {
	at := map[string]int{}
	for i, l := range order {
		at[l] = i
	}
	counts := make([]int, len(order))
	unmapped := map[string]bool{}
	for _, v := range vs {
		counts[at[v.label]]++
		if v.unmapped {
			unmapped[v.label] = true
		}
	}
	return counts, unmapped
}

// distribution fills a label summary: each label's count and proportion
// with its Wilson interval, the mode with ties broken by the order, and the
// unmapped trials. A closed vocabulary lists every declared label; an open
// one the labels the side holds.
func distribution(s *Summary, m Metric, order []string, vs []value, z float64) {
	counts, unmapped := labelCounts(order, vs)
	n := len(vs)
	shares := map[string]float64{}
	best := -1
	for i, l := range order {
		c := counts[i]
		if m.Vocabulary == Open && c == 0 {
			continue
		}
		ls := LabelShare{Label: l, Count: c, Unmapped: unmapped[l]}
		if n > 0 {
			ls.Proportion = float64(c) / float64(n)
			ls.Interval = ptr(wilsonCount(c, n, z))
			shares[l] = ls.Proportion
		}
		s.Distribution = append(s.Distribution, ls)
		if c > 0 && (best < 0 || c > counts[best]) {
			best = i
		}
	}
	for _, v := range vs {
		if v.unmapped {
			s.Unmapped++
		}
	}
	if n > 0 {
		s.Value = shares
		s.Mode = order[best]
	} else {
		s.NoInterval = noIntervalNoValue
	}
}

// labelSummary is a cell's summary of a label metric over the order given.
func labelSummary(m Metric, r *sideRead, order []string, z float64) Summary {
	s := base(m, r)
	distribution(&s, m, order, r.values, z)
	return s
}

// cellStat is a cell's reading of a cell-level metric: its of metric's
// reading, the labels left after exclude_labels as whole numbers by the
// order, and the trials left out by label.
type cellStat struct {
	of       *sideRead
	labels   []float64
	excluded map[string]int
}

// readCellStat reads a cell-level metric from its of metric's reading.
func readCellStat(m Metric, of *sideRead, order []string) cellStat {
	at := map[string]int{}
	for i, l := range order {
		at[l] = i
	}
	ex := excludeLabels(m)
	cs := cellStat{of: of}
	for _, v := range of.values {
		if ex[normalizeLabel(v.label)] {
			if cs.excluded == nil {
				cs.excluded = map[string]int{}
			}
			cs.excluded[v.label]++
			continue
		}
		cs.labels = append(cs.labels, float64(at[v.label]))
	}
	return cs
}

// d is the denominator: the valued trials less the excluded ones.
func (c cellStat) d() int { return len(c.labels) }

// has reports whether the statistic has a value: a trial left, two for a
// repeat rate.
func (c cellStat) has(a Aggregate) bool {
	if a == AggRepeatRate {
		return c.d() >= 2
	}
	return c.d() >= 1
}

// modeCount is the count of the most frequent label.
func modeCount(v []float64) int {
	counts := map[float64]int{}
	best := 0
	for _, x := range v {
		counts[x]++
		best = max(best, counts[x])
	}
	return best
}

// distinct is the number of distinct labels.
func distinct(v []float64) int {
	seen := map[float64]bool{}
	for _, x := range v {
		seen[x] = true
	}
	return len(seen)
}

// cellStatistic is a cell-level metric's statistic of labels.
func cellStatistic(a Aggregate) func([]float64) float64 {
	switch a {
	case AggRepeatRate:
		return repeatRate
	case AggModeShare:
		return func(v []float64) float64 { return float64(modeCount(v)) / float64(len(v)) }
	}
	return func(v []float64) float64 { return float64(distinct(v)) }
}

// cellLevelSummary is a cell's summary of a cell-level metric: n and
// missing those of its of metric, the denominator its valued trials less
// those exclude_labels leaves out, the statistic, and for a mode share its
// numerator.
func cellLevelSummary(m Metric, c cellStat) Summary {
	s := Summary{
		Aggregate:      m.Aggregate,
		N:              c.of.n,
		NotApplicable:  c.of.notApplicable,
		Denominator:    c.d(),
		ExcludedLabels: c.excluded,
		Missing:        c.of.missing(),
		Excluded:       maps.Clone(c.of.trials.excluded),
		Final:          c.of.cell.Final,
	}
	if m.Aggregate == AggRepeatRate {
		s.NoInterval = noIntervalRepeatRate
	} else {
		s.NoInterval = noIntervalDescriptive
	}
	if !c.has(m.Aggregate) {
		return s
	}
	s.Value = cellStatistic(m.Aggregate)(c.labels)
	if m.Aggregate == AggModeShare {
		s.Numerator = ptr(modeCount(c.labels))
	}
	return s
}

// candidateValue is a side's current value of a candidate-level metric:
// the latest generation of its producing grader, or of any grader when the
// side names none.
func candidateValue(side *Side, m Metric, producer string) *Measurement {
	var best *Measurement
	for i := range side.Values {
		v := &side.Values[i]
		if v.Metric != m.Digest || (producer != "" && v.Grader != producer) {
			continue
		}
		if best == nil || v.Generation > best.Generation {
			best = v
		}
	}
	return best
}

// candidateSummary is a candidate-level metric's summary: its one value.
func candidateSummary(v *Measurement) Summary {
	s := Summary{NoInterval: noIntervalCandidate, Final: true}
	if v == nil {
		s.Missing = 1
		s.N = 1
		return s
	}
	s.N, s.Denominator = 1, 1
	s.Value = v.Value
	if b, ok := v.Value.(bool); ok {
		k := 0
		if b {
			k = 1
		}
		s.Numerator = ptr(k)
	}
	return s
}
