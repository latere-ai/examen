// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"math"
	"slices"

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

// x2Scratch holds the counts one task's X² is computed from, so a
// permutation test draws without allocating.
type x2Scratch struct {
	counts [][]int
	cols   []int
}

func newX2Scratch(sides, k int) *x2Scratch {
	s := &x2Scratch{counts: make([][]int, sides), cols: make([]int, k)}
	for i := range s.counts {
		s.counts[i] = make([]int, k)
	}
	return s
}

// labelX2 is Pearson's X² of one task's K by k table of label counts, the
// sides' labels written as whole numbers 0 … k-1:
//
//	X² = Σ_s Σ_l (O_sl - E_sl)²/E_sl,  E_sl = n_s·c_l/N
//
// over the labels with a count on any side, summed side by side and,
// within a side, label by label, each squared difference rounded before
// its division.
func labelX2(sides [][]float64, sc *x2Scratch) float64 {
	clear(sc.cols)
	n := 0
	for s, vs := range sides {
		row := sc.counts[s]
		clear(row)
		for _, v := range vs {
			row[int(v)]++
			sc.cols[int(v)]++
		}
		n += len(vs)
	}
	x := 0.0
	for s, vs := range sides {
		for l, c := range sc.cols {
			if c == 0 {
				continue
			}
			e := float64(len(vs)*c) / float64(n)
			d := float64(sc.counts[s][l]) - e
			x += float64(d*d) / e
		}
	}
	return x
}

// relabelings is the number of ways to deal a task's pooled trials to
// sides of the sizes given, N!/Π_s n_s!, or limit + 1 when it is larger.
func relabelings(sizes []int, limit int) int {
	n := 0
	for _, s := range sizes {
		n += s
	}
	total := 1
	for _, s := range sizes {
		c := choose(n, s, limit)
		if c > limit || total > limit/c {
			return limit + 1
		}
		total *= c
		n -= s
	}
	return total
}

// splits calls visit with every way to deal n positions to sides of the
// sizes given: the first side's positions chosen among all of them in
// lexicographic order, then the second side's among the rest, and so on.
// side[i] is the side position i is dealt to.
func splits(n int, sizes []int, visit func(side []int)) {
	side := make([]int, n)
	var deal func(s int, free []int)
	deal = func(s int, free []int) {
		if s == len(sizes)-1 {
			for _, i := range free {
				side[i] = s
			}
			visit(side)
			return
		}
		rest := make([]int, 0, len(free)-sizes[s])
		combinations(len(free), sizes[s], func(idx []int) {
			rest = rest[:0]
			at := 0
			for j, i := range free {
				if at < len(idx) && idx[at] == j {
					side[i] = s
					at++
					continue
				}
				rest = append(rest, i)
			}
			deal(s+1, slices.Clone(rest))
		})
	}
	all := make([]int, n)
	for i := range all {
		all[i] = i
	}
	deal(0, all)
}

// sideSymmetries is the number of orderings of the sides that give every
// side the sizes it has in every task: such an ordering of a relabeling
// gives the same X², so an exact test's smallest p-value is this many
// over its relabelings.
func sideSymmetries(tasks [][][]float64) int {
	groups := map[string]int{}
	for s := range tasks[0] {
		var key []byte
		for _, t := range tasks {
			key = fmt.Appendf(key, "%d,", len(t[s]))
		}
		groups[string(key)]++
	}
	out := 1
	for _, g := range groups {
		for i := 2; i <= g; i++ {
			out *= i
		}
	}
	return out
}

// stratifiedLabelTest is the stratified label test (spec 008) of K sides
// over the included tasks: tasks[t][s] are side s's labels of task t as
// whole numbers 0 … k-1, each side's in ascending order of trial id and
// the tasks in ascending order of name. The statistic, pearson_x2_sum, is
// Σ_t X²_t summed in task order. A relabeling deals each task's pooled
// labels to its sides anew, keeping every side's size. The test is exact
// when the product of the tasks' relabelings is at most exactMax, every
// combination enumerated with the first task's relabelings varying
// slowest; otherwise Monte Carlo with sequential stopping, each draw
// shuffling a fresh copy of every task's pooled labels, the sides' labels
// in the sides' order, in task order, and dealing the first n_t1 to the
// first side, the next n_t2 to the second, and so on. floor is the
// smallest p-value the exact test can give, and 0 when it is not exact.
func stratifiedLabelTest(tasks [][][]float64, k, exactMax int, cfg mcConfig) (r testResult, floor float64) {
	sides := len(tasks[0])
	sc := newX2Scratch(sides, k)
	pooled := make([][]float64, len(tasks))
	sizes := make([][]int, len(tasks))
	observed := 0.0
	total := 1
	for t, task := range tasks {
		observed += labelX2(task, sc)
		pooled[t] = slices.Concat(task...)
		for _, v := range task {
			sizes[t] = append(sizes[t], len(v))
		}
		if m := relabelings(sizes[t], exactMax); total <= exactMax {
			if m > exactMax || total > exactMax/m {
				total = exactMax + 1
			} else {
				total *= m
			}
		}
	}
	view := func(t int, buf []float64, into [][]float64) {
		at := 0
		for s, n := range sizes[t] {
			into[s] = buf[at : at+n]
			at += n
		}
	}
	if total <= exactMax {
		// Each task's relabelings' X², in enumeration order; the
		// statistic of a combination sums them in task order.
		per := make([][]float64, len(tasks))
		for t := range tasks {
			parts := make([][]float64, sides)
			buf := make([]float64, len(pooled[t]))
			splits(len(pooled[t]), sizes[t], func(side []int) {
				at := 0
				for s := range sides {
					for i, v := range pooled[t] {
						if side[i] == s {
							buf[at] = v
							at++
						}
					}
				}
				view(t, buf, parts)
				per[t] = append(per[t], labelX2(parts, sc))
			})
		}
		at := make([]int, len(tasks))
		count := 0
		for {
			x := 0.0
			for t := range tasks {
				x += per[t][at[t]]
			}
			if atLeast(x, observed, cfg.tol) {
				count++
			}
			i := len(tasks) - 1
			for i >= 0 && at[i] == len(per[i])-1 {
				at[i] = 0
				i--
			}
			if i < 0 {
				break
			}
			at[i]++
		}
		return testResult{p: float64(count) / float64(total), exact: true, resamples: total}, float64(sideSymmetries(tasks)) / float64(total)
	}
	bufs := make([][]float64, len(tasks))
	for t := range bufs {
		bufs[t] = make([]float64, len(pooled[t]))
	}
	parts := make([][]float64, sides)
	return sequential(cfg, observed, func() float64 {
		x := 0.0
		for t := range tasks {
			copy(bufs[t], pooled[t])
			cfg.g.shuffle(bufs[t])
			view(t, bufs[t], parts)
			x += labelX2(parts, sc)
		}
		return x
	}), 0
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
