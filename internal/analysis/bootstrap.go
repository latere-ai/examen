// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"slices"
)

// tStar is a resample's studentized statistic: (est* - est)/se* when the
// resample has spread, and when it has none, plus or minus infinity by the
// sign of est* - est, or 0 when they are equal. A resample without spread
// is kept, never drawn again: it is how the resamples show that the few
// large values were absent.
func tStar(estStar, est, seStar float64) float64 {
	switch {
	case seStar > 0:
		return (estStar - est) / seStar
	case estStar > est:
		return math.Inf(1)
	case estStar < est:
		return math.Inf(-1)
	}
	return 0
}

// bootResult is a bootstrap interval when one exists, and how many of its
// resamples had no spread.
type bootResult struct {
	iv       Interval
	ok       bool
	noSpread int
}

// studentized closes a bootstrap-t interval: [est - Q*(1 - α/2)·se,
// est - Q*(α/2)·se], absent when a quantile it needs is infinite or
// undefined.
func studentized(t []float64, est, se, alpha float64, noSpread int) bootResult {
	slices.Sort(t)
	lo, hi := quantile(t, alpha/2), quantile(t, 1-alpha/2)
	if !finite(lo) || !finite(hi) {
		return bootResult{noSpread: noSpread}
	}
	return bootResult{iv: Interval{est - float64(hi*se), est - float64(lo*se)}, ok: true, noSpread: noSpread}
}

// draw fills buf with len(buf) values of v drawn with replacement, in
// order.
func draw(g *generator, v, buf []float64) {
	for i := range buf {
		buf[i] = v[g.below(len(v))]
	}
}

// bootstrapMean is the bootstrap-t interval of the mean of v: B resamples
// of n values drawn with replacement, each with its own mean m*, standard
// error se* and t*. A constant v has se = 0 and the interval [m, m], with
// no resampling.
func bootstrapMean(v []float64, b int, alpha float64, g *generator) bootResult {
	m := mean(v)
	if constant(v) {
		return bootResult{iv: Interval{m, m}, ok: true}
	}
	n := float64(len(v))
	se := math.Sqrt(variance(v) / n)
	buf := make([]float64, len(v))
	t := make([]float64, b)
	noSpread := 0
	for i := range t {
		draw(g, v, buf)
		seStar := math.Sqrt(variance(buf) / n)
		if seStar == 0 {
			noSpread++
		}
		t[i] = tStar(mean(buf), m, seStar)
	}
	return studentized(t, m, se, alpha, noSpread)
}

// bootstrapMeanDiff is the bootstrap-t interval of mean(x) - mean(y): each
// resample redraws each side from its own values, the candidate side
// first, with se = sqrt(s_x²/n_x + s_y²/n_y). Both sides constant give
// se = 0 and the interval [d, d].
func bootstrapMeanDiff(x, y []float64, b int, alpha float64, g *generator) bootResult {
	d := mean(x) - mean(y)
	nx, ny := float64(len(x)), float64(len(y))
	se := math.Sqrt(variance(x)/nx + variance(y)/ny)
	if se == 0 {
		return bootResult{iv: Interval{d, d}, ok: true}
	}
	bx, by := make([]float64, len(x)), make([]float64, len(y))
	t := make([]float64, b)
	noSpread := 0
	for i := range t {
		draw(g, x, bx)
		draw(g, y, by)
		seStar := math.Sqrt(variance(bx)/nx + variance(by)/ny)
		if seStar == 0 {
			noSpread++
		}
		t[i] = tStar(mean(bx)-mean(by), d, seStar)
	}
	return studentized(t, d, se, alpha, noSpread)
}

// weightedMean is S = Σ w_t s_t / W over per-task summaries s.
func weightedMean(w, s []float64) float64 {
	var num, den float64
	for i := range w {
		num += float64(w[i] * s[i])
		den += w[i]
	}
	return num / den
}

// stratifiedSE is sqrt(Σ w_t² v_t / n_t) / W over the tasks' values.
func stratifiedSE(w []float64, sides [][]float64) float64 {
	var s, den float64
	for i, v := range sides {
		s += float64(float64(w[i]*w[i]) * (variance(v) / float64(len(v))))
		den += w[i]
	}
	return math.Sqrt(s) / den
}

// stratifiedMean is the weighted mean of the tasks' means and its standard
// error.
func stratifiedMean(w []float64, sides [][]float64) (s, se float64) {
	means := make([]float64, len(sides))
	for i, v := range sides {
		means[i] = mean(v)
	}
	return weightedMean(w, means), stratifiedSE(w, sides)
}

// resampleTasks draws every task's values from that task's cell alone, in
// the tasks' order, into bufs.
func resampleTasks(g *generator, sides, bufs [][]float64) {
	for i, v := range sides {
		draw(g, v, bufs[i])
	}
}

// buffers returns one buffer per side of each side's length.
func buffers(sides [][]float64) [][]float64 {
	out := make([][]float64, len(sides))
	for i, v := range sides {
		out[i] = make([]float64, len(v))
	}
	return out
}

// stratifiedBootstrapMean is the stratified bootstrap-t interval of the
// weighted mean of the tasks' means: each resample redraws every task from
// its own cell and recomputes S and se; with ys given, the statistic is
// S_x - S_y with se = sqrt(se_x² + se_y²), the candidate side's tasks
// drawn before the reference side's.
func stratifiedBootstrapMean(w []float64, xs, ys [][]float64, b int, alpha float64, g *generator) bootResult {
	est, se := stratifiedMean(w, xs)
	if ys != nil {
		sy, sey := stratifiedMean(w, ys)
		est -= sy
		se = math.Sqrt(float64(se*se) + float64(sey*sey))
	}
	if se == 0 {
		return bootResult{iv: Interval{est, est}, ok: true}
	}
	bx := buffers(xs)
	var by [][]float64
	if ys != nil {
		by = buffers(ys)
	}
	t := make([]float64, b)
	noSpread := 0
	for i := range t {
		resampleTasks(g, xs, bx)
		e, s := stratifiedMean(w, bx)
		if ys != nil {
			resampleTasks(g, ys, by)
			ey, sy := stratifiedMean(w, by)
			e -= ey
			s = math.Sqrt(float64(s*s) + float64(sy*sy))
		}
		if s == 0 {
			noSpread++
		}
		t[i] = tStar(e, est, s)
	}
	return studentized(t, est, se, alpha, noSpread)
}

// stratifiedPercentile is the percentile interval [Q(α/2), Q(1 - α/2)] of
// the weighted mean of the tasks' statistics, each recomputed from a
// stratified resample; with ys given, of S_x - S_y, the candidate side's
// tasks drawn first.
func stratifiedPercentile(w []float64, xs, ys [][]float64, stat func([]float64) float64, b int, alpha float64, g *generator) Interval {
	weighted := func(sides [][]float64) float64 {
		s := make([]float64, len(sides))
		for i, v := range sides {
			s[i] = stat(v)
		}
		return weightedMean(w, s)
	}
	bx := buffers(xs)
	var by [][]float64
	if ys != nil {
		by = buffers(ys)
	}
	vals := make([]float64, b)
	for i := range vals {
		resampleTasks(g, xs, bx)
		v := weighted(bx)
		if ys != nil {
			resampleTasks(g, ys, by)
			v -= weighted(by)
		}
		vals[i] = v
	}
	slices.Sort(vals)
	return Interval{quantile(vals, alpha/2), quantile(vals, 1-alpha/2)}
}
