// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"slices"
	"sort"

	"latere.ai/x/examen/internal/analysis/mathx"
)

// fisherTable holds, for one total of successes K = i + j over two sides of
// n_x and n_y trials, the hypergeometric probabilities of the candidate
// side's count t given the margins,
//
//	P(t) = C(K, t)·C(N - K, n_x - t) / C(N, n_x),
//
// and Fisher's two-sided p-value of each t: the sum of P(s) over every s
// with P(s) <= P(t)·(1 + tol), taken in ascending order of P(s).
type fisherTable struct {
	lo int       // the smallest t the margins allow
	p  []float64 // Fisher's two-sided p-value of t = lo + index
}

func newFisherTable(k, nx, ny int, tol float64) fisherTable {
	n := nx + ny
	lo, hi := max(0, k-ny), min(k, nx)
	logTotal := mathx.LogChoose(n, nx)
	probs := make([]float64, hi-lo+1)
	for t := lo; t <= hi; t++ {
		probs[t-lo] = mathx.Exp(mathx.LogChoose(k, t) + mathx.LogChoose(n-k, nx-t) - logTotal)
	}
	order := slices.Clone(probs)
	slices.Sort(order)
	cum := make([]float64, len(order)+1)
	for i, v := range order {
		cum[i+1] = cum[i] + v
	}
	f := make([]float64, len(probs))
	for i, pt := range probs {
		limit := float64(pt * (1 + tol))
		at := sort.Search(len(order), func(j int) bool { return order[j] > limit })
		f[i] = cum[at]
	}
	return fisherTable{lo: lo, p: f}
}

// fisherP is Fisher's two-sided p-value of a of n_x and c of n_y true: the
// statistic Boschloo's test orders tables by, reported nowhere as a test
// of its own.
func fisherP(a, nx, c, ny int, tol float64) float64 {
	ft := newFisherTable(a+c, nx, ny, tol)
	return ft.p[a-ft.lo]
}

// boschloo is Boschloo's exact unconditional test of a of n_x against c of
// n_y true, two-sided, and Fisher's p-value of the table, which orders it.
// Every table (i, j) at least as extreme as the observed one, F(i, j) <=
// F(a, c)·(1 + tol), enters the sum, and the p-value is its largest
// probability over the fixed grid pi_g = (g - 1/2)/G, g = 1 … G, of the
// one rate both sides share under the null hypothesis:
//
//	p = max_g Σ_{(i, j) extreme} C(n_x, i) pi^i (1 - pi)^(n_x - i) · C(n_y, j) pi^j (1 - pi)^(n_y - j)
//
// The sum runs over i ascending, and for each i over its extreme j
// ascending, as px(i)·Σ_j py(j); every probability is computed in log
// space.
func boschloo(a, nx, c, ny, grid int, tol float64) (p, fisher float64) {
	n := nx + ny
	tables := make([]fisherTable, n+1)
	for k := range tables {
		tables[k] = newFisherTable(k, nx, ny, tol)
	}
	fisher = tables[a+c].p[a-tables[a+c].lo]
	limit := float64(fisher * (1 + tol))
	extreme := make([][]int, nx+1)
	for i := range extreme {
		for j := 0; j <= ny; j++ {
			ft := tables[i+j]
			if ft.p[i-ft.lo] <= limit {
				extreme[i] = append(extreme[i], j)
			}
		}
	}
	lcx := make([]float64, nx+1)
	for i := range lcx {
		lcx[i] = mathx.LogChoose(nx, i)
	}
	lcy := make([]float64, ny+1)
	for j := range lcy {
		lcy[j] = mathx.LogChoose(ny, j)
	}
	py := make([]float64, ny+1)
	for g := 1; g <= grid; g++ {
		pi := (float64(g) - 0.5) / float64(grid)
		lp, l1p := mathx.Log(pi), mathx.Log(1-pi)
		for j := range py {
			py[j] = mathx.Exp(lcy[j] + float64(float64(j)*lp) + float64(float64(ny-j)*l1p))
		}
		total := 0.0
		for i, js := range extreme {
			if len(js) == 0 {
				continue
			}
			inner := 0.0
			for _, j := range js {
				inner += py[j]
			}
			px := mathx.Exp(lcx[i] + float64(float64(i)*lp) + float64(float64(nx-i)*l1p))
			total += float64(px * inner)
		}
		p = max(p, total)
	}
	return min(p, 1), fisher
}
