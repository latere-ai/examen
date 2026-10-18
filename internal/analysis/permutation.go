// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"slices"

	"latere.ai/x/examen/internal/analysis/mathx"
)

// The statistics a test orders by, as a result's `statistic` names them.
const (
	statFisherP      = "fisher_p"
	statWelchT       = "welch_t"
	statStudentizedQ = "studentized_quantile"
	statDifference   = "difference"
	statTableProb    = "table_probability"
	statPearsonX2    = "pearson_x2"
	statPearsonX2Sum = "pearson_x2_sum"
)

// The tests, as a result's `method` names them.
const (
	methodBoschloo   = "boschloo_exact"
	methodPerm       = "permutation"
	methodStratified = "stratified_permutation"
	methodFFH        = "fisher_freeman_halton"
	methodChiSquare  = "chi_square_permutation"
)

// atLeast reports a >= b as the permutation counts read it: b finite and
// a >= b - tol·max(1, |b|), or both positive infinity, so a relabeling that
// reproduces the observed discrepancy counts even when rounding puts it a
// hair below.
func atLeast(a, b, tol float64) bool {
	if math.IsInf(b, 1) {
		return math.IsInf(a, 1)
	}
	return a >= b-float64(tol*math.Max(1, math.Abs(b)))
}

// choose is C(n, k) as an integer, or limit + 1 when it is larger than
// limit: a count of relabelings is compared with PERMUTATION_EXACT_MAX and
// never needed above it.
func choose(n, k, limit int) int {
	if k < 0 || k > n {
		return 0
	}
	k = min(k, n-k)
	c := 1
	for i := 1; i <= k; i++ {
		c = c * (n - k + i) / i
		if c > limit {
			return limit + 1
		}
	}
	return c
}

// testResult is a test's p-value, whether it enumerated every case, and
// how many relabelings or tables it enumerated or drew.
type testResult struct {
	p         float64
	exact     bool
	resamples int
}

// mcConfig holds what a Monte Carlo permutation test reads: its seed's
// generator, B, the stop rule's exceedances and the tie tolerance.
type mcConfig struct {
	g    *generator
	b    int
	stop int
	tol  float64
}

// sequential runs Besag and Clifford's sequential Monte Carlo test: draw
// relabelings one at a time, count those whose discrepancy is at least
// the observed one, and stop at the stop-th, with p = stop/L at the L-th
// draw; with fewer after B draws, p = (1 + c)/(1 + B).
func sequential(cfg mcConfig, observed float64, next func() float64) testResult {
	c := 0
	for l := 1; l <= cfg.b; l++ {
		if atLeast(next(), observed, cfg.tol) {
			c++
			if c == cfg.stop {
				return testResult{p: float64(cfg.stop) / float64(l), resamples: l}
			}
		}
	}
	return testResult{p: float64(1+c) / float64(1+cfg.b), resamples: cfg.b}
}

// combinations calls visit with every n_x-subset of {0 … n-1} in
// lexicographic order, ascending indices.
func combinations(n, k int, visit func(idx []int)) {
	idx := make([]int, k)
	for i := range idx {
		idx[i] = i
	}
	for {
		visit(idx)
		i := k - 1
		for i >= 0 && idx[i] == n-k+i {
			i--
		}
		if i < 0 {
			return
		}
		idx[i]++
		for j := i + 1; j < k; j++ {
			idx[j] = idx[j-1] + 1
		}
	}
}

// split writes the pooled values a relabeling chooses into x, in ascending
// index order, and the rest into y.
func split(pooled []float64, idx []int, x, y []float64) {
	xi, yi, at := 0, 0, 0
	for i, v := range pooled {
		if at < len(idx) && idx[at] == i {
			x[xi] = v
			xi++
			at++
			continue
		}
		y[yi] = v
		yi++
	}
}

// statistic computes T from a relabeling's two sides. It may reorder the
// values of each side, which a relabeling holds in buffers of its own.
type statistic func(x, y []float64) float64

// permutationTest is the two-sample permutation test of T between x and y
// with the discrepancy S = |T|. The pooled values are x followed by y, and
// a relabeling chooses which n_x of them form the candidate side. It is
// exact when C(N, n_x) is at most exactMax: every relabeling is enumerated
// and p = #{S >= S_obs}/C(N, n_x), the observed labeling included.
// Otherwise it is Monte Carlo with sequential stopping, each relabeling
// drawn by shuffling a fresh copy of the pooled values, the first n_x
// forming the candidate side.
func permutationTest(x, y []float64, t statistic, exactMax int, cfg mcConfig) testResult {
	pooled := slices.Concat(x, y)
	observed := math.Abs(t(slices.Clone(x), slices.Clone(y)))
	bx, by := make([]float64, len(x)), make([]float64, len(y))
	if total := choose(len(pooled), len(x), exactMax); total <= exactMax {
		count := 0
		combinations(len(pooled), len(x), func(idx []int) {
			split(pooled, idx, bx, by)
			if atLeast(math.Abs(t(bx, by)), observed, cfg.tol) {
				count++
			}
		})
		return testResult{p: float64(count) / float64(total), exact: true, resamples: total}
	}
	buf := make([]float64, len(pooled))
	return sequential(cfg, observed, func() float64 {
		copy(buf, pooled)
		cfg.g.shuffle(buf)
		return math.Abs(t(buf[:len(x)], buf[len(x):]))
	})
}

// studentize divides a statistic's numerator by its denominator; a zero
// denominator gives 0 for a zero numerator and plus or minus infinity by
// the numerator's sign otherwise.
func studentize(num, den float64) float64 {
	if den == 0 {
		switch {
		case num > 0:
			return math.Inf(1)
		case num < 0:
			return math.Inf(-1)
		}
		return 0
	}
	return num / den
}

// welchT is Welch's statistic (mean(x) - mean(y))/sqrt(s_x²/n_x +
// s_y²/n_y).
func welchT(x, y []float64) float64 {
	den := math.Sqrt(variance(x)/float64(len(x)) + variance(y)/float64(len(y)))
	return studentize(mean(x)-mean(y), den)
}

// hallSheather is Hall and Sheather's bandwidth for Siddiqui's estimate of
// the sparsity at the quantile q of n values:
//
//	u = sqrt(2)·erfinv(2q - 1)
//	b = n^(-1/3)·z^(2/3)·(1.5·phi(u)² / (2u² + 1))^(1/3)
func hallSheather(n int, q, z float64) float64 {
	u := float64(math.Sqrt(2) * mathx.Erfinv(float64(2*q)-1))
	phi := mathx.NormalPDF(u)
	inner := float64(1.5*float64(phi*phi)) / (float64(2*float64(u*u)) + 1)
	return float64(float64(mathx.Pow(float64(n), -1.0/3)*mathx.Pow(z, 2.0/3)) * mathx.Pow(inner, 1.0/3))
}

// sparsity is Siddiqui's difference quotient of the sorted values s at the
// quantile q with bandwidth b, an estimate of 1/f(F^-1(q)).
func sparsity(s []float64, q, b float64) float64 {
	hi, lo := math.Min(1, q+b), math.Max(0, q-b)
	return (quantile(s, hi) - quantile(s, lo)) / (hi - lo)
}

// studentizedQuantile returns Chung and Romano's studentized difference of
// the quantile q of two sides,
//
//	T = (Q_x(q) - Q_y(q)) / sqrt(q(1 - q)(r_x²/n_x + r_y²/n_y)),
//
// each side's bandwidth fixed by its size. It sorts each side in place.
func studentizedQuantile(q, bx, by float64) statistic {
	return func(x, y []float64) float64 {
		slices.Sort(x)
		slices.Sort(y)
		sx, sy := x, y
		rx, ry := sparsity(sx, q, bx), sparsity(sy, q, by)
		v := float64(rx*rx)/float64(len(x)) + float64(ry*ry)/float64(len(y))
		den := math.Sqrt(float64(float64(q*(1-q)) * v))
		return studentize(quantile(sx, q)-quantile(sy, q), den)
	}
}

// repeatRate is R = Σ_l k_l(k_l - 1)/(d(d - 1)) of labels written as small
// whole numbers, the share of the d(d - 1)/2 pairs of trials that hold one
// label; d is at least 2.
func repeatRate(v []float64) float64 {
	// Labels are positions in a label order; up to 64 of them are counted
	// on the stack, so a permutation test draws without allocating.
	var small [64]int
	counts := small[:]
	top := 0
	for _, x := range v {
		top = max(top, int(x))
	}
	if top >= len(small) {
		counts = make([]int, top+1)
	}
	for _, x := range v {
		counts[int(x)]++
	}
	num := 0
	for _, c := range counts[:top+1] {
		num += c * (c - 1)
	}
	d := len(v)
	return float64(num) / float64(d*(d-1))
}

// repeatRateDiff is the statistic `difference` of a repeat rate,
// R(x) - R(y).
func repeatRateDiff(x, y []float64) float64 { return repeatRate(x) - repeatRate(y) }

// stratum2 is one included task of a stratified test: its weight and both
// sides' values.
type stratum2 struct {
	w    float64
	x, y []float64
}

// stratStatistic computes T from every task's relabeled sides. It may
// reorder the values of each side.
type stratStatistic func(w []float64, xs, ys [][]float64) float64

// stratifiedTest is the stratified permutation test: a relabeling permutes
// the labels within each included task separately, keeping each task's
// n_x and n_y. It is exact when the product over tasks of C(n_x + n_y, n_x)
// is at most exactMax, enumerating every combination of the tasks'
// relabelings with the first task's varying slowest; otherwise Monte Carlo
// with sequential stopping, each draw shuffling a fresh copy of every
// task's pooled values in the tasks' order.
func stratifiedTest(tasks []stratum2, t stratStatistic, exactMax int, cfg mcConfig) testResult {
	k := len(tasks)
	w := make([]float64, k)
	pooled := make([][]float64, k)
	xs, ys := make([][]float64, k), make([][]float64, k)
	obsX, obsY := make([][]float64, k), make([][]float64, k)
	total := 1
	for i, s := range tasks {
		w[i] = s.w
		pooled[i] = slices.Concat(s.x, s.y)
		xs[i], ys[i] = make([]float64, len(s.x)), make([]float64, len(s.y))
		obsX[i], obsY[i] = slices.Clone(s.x), slices.Clone(s.y)
		if total <= exactMax {
			total *= choose(len(pooled[i]), len(s.x), exactMax)
		}
	}
	observed := math.Abs(t(w, obsX, obsY))
	if total <= exactMax {
		// Every task's combinations, enumerated once.
		combos := make([][][]int, k)
		for i := range tasks {
			combinations(len(pooled[i]), len(tasks[i].x), func(idx []int) {
				combos[i] = append(combos[i], slices.Clone(idx))
			})
		}
		at := make([]int, k)
		count := 0
		for {
			for i := range tasks {
				split(pooled[i], combos[i][at[i]], xs[i], ys[i])
			}
			if atLeast(math.Abs(t(w, xs, ys)), observed, cfg.tol) {
				count++
			}
			i := k - 1
			for i >= 0 && at[i] == len(combos[i])-1 {
				at[i] = 0
				i--
			}
			if i < 0 {
				break
			}
			at[i]++
		}
		return testResult{p: float64(count) / float64(total), exact: true, resamples: total}
	}
	bufs := make([][]float64, k)
	for i := range bufs {
		bufs[i] = make([]float64, len(pooled[i]))
	}
	return sequential(cfg, observed, func() float64 {
		for i := range tasks {
			copy(bufs[i], pooled[i])
			cfg.g.shuffle(bufs[i])
			nx := len(tasks[i].x)
			xs[i], ys[i] = bufs[i][:nx], bufs[i][nx:]
		}
		return math.Abs(t(w, xs, ys))
	})
}

// stratDifference is the statistic `difference` of a suite-level figure
// that is a weighted mean of per-task statistics: S_x - S_y.
func stratDifference(stat func([]float64) float64) stratStatistic {
	return func(w []float64, xs, ys [][]float64) float64 {
		sx := make([]float64, len(xs))
		sy := make([]float64, len(ys))
		for i := range xs {
			sx[i], sy[i] = stat(xs[i]), stat(ys[i])
		}
		return weightedMean(w, sx) - weightedMean(w, sy)
	}
}

// stratWelch is the statistic `welch_t` of a suite-level mean:
// (S_x - S_y)/sqrt(se_x² + se_y²), each task's variance recomputed per
// relabeling.
func stratWelch(w []float64, xs, ys [][]float64) float64 {
	sx, sex := stratifiedMean(w, xs)
	sy, sey := stratifiedMean(w, ys)
	return studentize(sx-sy, math.Sqrt(float64(sex*sex)+float64(sey*sey)))
}

// stratStudentizedQuantile is the statistic `studentized_quantile` of a
// suite-level median or p90:
//
//	T = (S_x - S_y) / sqrt(Σ_t (w_t/W)² q(1 - q)(r_xt²/n_xt + r_yt²/n_yt))
//
// with bx and by each task's bandwidths.
func stratStudentizedQuantile(q float64, bx, by []float64) stratStatistic {
	return func(w []float64, xs, ys [][]float64) float64 {
		wsum := 0.0
		for _, v := range w {
			wsum += v
		}
		qx := make([]float64, len(xs))
		qy := make([]float64, len(ys))
		v := 0.0
		for i := range xs {
			slices.Sort(xs[i])
			slices.Sort(ys[i])
			sx, sy := xs[i], ys[i]
			qx[i], qy[i] = quantile(sx, q), quantile(sy, q)
			rx, ry := sparsity(sx, q, bx[i]), sparsity(sy, q, by[i])
			share := w[i] / wsum
			tv := float64(rx*rx)/float64(len(sx)) + float64(ry*ry)/float64(len(sy))
			v += float64(float64(float64(share*share)*float64(q*(1-q))) * tv)
		}
		return studentize(weightedMean(w, qx)-weightedMean(w, qy), math.Sqrt(v))
	}
}
