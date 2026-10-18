// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"

	"latere.ai/x/examen/internal/analysis/mathx"
)

// countTables counts the 2 by k tables with column totals cols and a first
// row summing to nx, by a recurrence over the columns, before any is
// enumerated: ways[s] is the number of partial first rows summing to s.
// Counts above limit are held at limit + 1.
func countTables(cols []int, nx, limit int) int {
	ways := make([]int, nx+1)
	ways[0] = 1
	for _, c := range cols {
		next := make([]int, nx+1)
		for s := range next {
			sum := 0
			for a := 0; a <= min(c, s); a++ {
				sum = min(sum+ways[s-a], limit+1)
			}
			next[s] = sum
		}
		ways = next
	}
	return ways[nx]
}

// freemanHalton is Fisher, Freeman and Halton's exact test of a 2 by k
// table of label counts, x the candidate's row and y the reference's, over
// labels with a count on either side. Given the margins, a table whose
// candidate row is a has the multivariate hypergeometric probability
// P(a) = Π_l C(c_l, a_l)/C(N, n_x), and the p-value is the sum of P(a) over
// every table with P(a) <= P(observed)·(1 + tol), the tables enumerated
// with their rows in lexicographic order. ok is false when the tables
// number more than limit.
func freemanHalton(x, y []int, limit int, tol float64) (testResult, bool) {
	cols := make([]int, len(x))
	nx, n := 0, 0
	for i := range x {
		cols[i] = x[i] + y[i]
		nx += x[i]
		n += cols[i]
	}
	tables := countTables(cols, nx, limit)
	if tables > limit {
		return testResult{}, false
	}
	logTotal := mathx.LogChoose(n, nx)
	logP := func(row []int) float64 {
		s := -logTotal
		for i, a := range row {
			s += mathx.LogChoose(cols[i], a)
		}
		return s
	}
	observed := mathx.Exp(logP(x))
	bound := float64(observed * (1 + tol))
	// capacity[i] is what the columns from i on can hold.
	capacity := make([]int, len(cols)+1)
	for i := len(cols) - 1; i >= 0; i-- {
		capacity[i] = capacity[i+1] + cols[i]
	}
	row := make([]int, len(cols))
	p := 0.0
	var walk func(i, left int)
	walk = func(i, left int) {
		if i == len(cols) {
			if pa := mathx.Exp(logP(row)); pa <= bound {
				p += pa
			}
			return
		}
		for a := max(0, left-capacity[i+1]); a <= min(cols[i], left); a++ {
			row[i] = a
			walk(i+1, left-a)
		}
	}
	walk(0, nx)
	return testResult{p: min(p, 1), exact: true, resamples: tables}, true
}

// pearsonX2 is Pearson's X² = Σ (O - E)²/E over the cells of the 2 by k
// table of two sides' labels, written as whole numbers 0 … k-1, with
// E = row total · column total / N.
func pearsonX2(k int) statistic {
	cx, cy := make([]int, k), make([]int, k)
	return func(x, y []float64) float64 {
		clear(cx)
		clear(cy)
		for _, v := range x {
			cx[int(v)]++
		}
		for _, v := range y {
			cy[int(v)]++
		}
		nx, ny := float64(len(x)), float64(len(y))
		n := nx + ny
		s := 0.0
		for l := range k {
			col := float64(cx[l] + cy[l])
			if col == 0 {
				continue
			}
			ex := float64(nx*col) / n
			ey := float64(ny*col) / n
			dx, dy := float64(cx[l])-ex, float64(cy[l])-ey
			s += float64(dx*dx)/ex + float64(dy*dy)/ey
		}
		return s
	}
}

// proportions is the share of each label 0 … k-1 among values written as
// whole numbers.
func proportions(v []float64, k int) []float64 {
	out := make([]float64, k)
	for _, x := range v {
		out[int(x)]++
	}
	for i := range out {
		out[i] /= float64(len(v))
	}
	return out
}

// weightedProportions is S(l) = Σ_t w_t p_t(l) / W over the tasks.
func weightedProportions(w []float64, sides [][]float64, k int) []float64 {
	out := make([]float64, k)
	wsum := 0.0
	for i, v := range sides {
		p := proportions(v, k)
		for l := range out {
			out[l] += float64(w[i] * p[l])
		}
		wsum += w[i]
	}
	for l := range out {
		out[l] /= wsum
	}
	return out
}

// labelDistance is the statistic `label_distance` of a suite-level label
// result: D = Σ_l (S_x(l) - S_y(l))²/P(l), P(l) = (S_x(l) + S_y(l))/2, over
// labels with P(l) above 0.
func labelDistance(k int) stratStatistic {
	return func(w []float64, xs, ys [][]float64) float64 {
		sx := weightedProportions(w, xs, k)
		sy := weightedProportions(w, ys, k)
		d := 0.0
		for l := range k {
			p := (sx[l] + sy[l]) / 2
			if p == 0 {
				continue
			}
			diff := sx[l] - sy[l]
			d += float64(diff*diff) / p
		}
		return d
	}
}

// labelTest is the task-level test of a label metric: Fisher, Freeman and
// Halton's exact test when its tables number at most exactMax, and the
// permutation of Pearson's X² otherwise; a table of fewer than two labels
// has p = 1. x and y are the sides' labels as whole numbers 0 … k-1.
func labelTest(x, y []float64, k, exactMax int, cfg mcConfig) (testResult, string, string) {
	cx, cy := make([]int, k), make([]int, k)
	for _, v := range x {
		cx[int(v)]++
	}
	for _, v := range y {
		cy[int(v)]++
	}
	var rx, ry []int
	for l := range k {
		if cx[l]+cy[l] > 0 {
			rx = append(rx, cx[l])
			ry = append(ry, cy[l])
		}
	}
	if len(rx) < 2 {
		return testResult{p: 1, exact: true, resamples: 1}, methodFFH, statTableProb
	}
	if r, ok := freemanHalton(rx, ry, exactMax, cfg.tol); ok {
		return r, methodFFH, statTableProb
	}
	stat := pearsonX2(k)
	observed := math.Abs(stat(x, y))
	pooled := append(append([]float64{}, x...), y...)
	buf := make([]float64, len(pooled))
	r := sequential(cfg, observed, func() float64 {
		copy(buf, pooled)
		cfg.g.shuffle(buf)
		return stat(buf[:len(x)], buf[len(x):])
	})
	return r, methodChiSquare, statPearsonX2
}
