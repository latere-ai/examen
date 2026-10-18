// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"slices"
	"testing"
)

func seedGen(label string) *generator {
	return newGenerator(ProcedureSeed(RootSeed("sha256:test"), "metric", label, procTest))
}

func mc(label string, b int) mcConfig {
	p := Defaults()
	return mcConfig{g: seedGen(label), b: b, stop: p.PermutationStopExceedances, tol: p.TieTolerance}
}

func TestBootstrapT(t *testing.T) {
	p := Defaults()
	alpha := p.alpha()
	// Equal values: [m, m] with no resampling.
	r := bootstrapMean([]float64{4, 4, 4}, p.BootstrapResamples, alpha, seedGen("equal"))
	if !r.ok || r.iv != (Interval{4, 4}) || r.noSpread != 0 {
		t.Errorf("equal values: %+v, want [4, 4]", r)
	}
	// 0, 0, 0, 0, 10: m = 2, se = 2; about 1024/3125 of the resamples are all
	// zeros, t* = -infinity, far more than make Q*(α/2) infinite, so the
	// interval is not shown, and the resamples without spread are counted.
	v := []float64{0, 0, 0, 0, 10}
	if m, se := mean(v), math.Sqrt(variance(v)/5); m != 2 || se != 2 {
		t.Errorf("m = %v, se = %v, want 2 and 2", m, se)
	}
	r = bootstrapMean(v, p.BootstrapResamples, alpha, seedGen("skew"))
	if r.ok {
		t.Errorf("0, 0, 0, 0, 10 has the interval %v", r.iv)
	}
	if r.noSpread < 3000 || r.noSpread > 3600 {
		t.Errorf("%d of 10,000 resamples had no spread, want about 3,277 and the all-tens", r.noSpread)
	}
	// A skewed sample with spread in every likely resample: the interval
	// holds the mean, is wider above it than below it, and repeats bit for
	// bit from its seed.
	sk := []float64{1.2, 0.8, 1.1, 0.9, 1.0, 1.3, 0.7, 1.05, 0.95, 6.5, 1.15, 0.85}
	a := bootstrapMean(sk, p.BootstrapResamples, alpha, seedGen("sk"))
	b := bootstrapMean(sk, p.BootstrapResamples, alpha, seedGen("sk"))
	m := mean(sk)
	if !a.ok || a.iv[0] > m || a.iv[1] < m || a.iv != b.iv {
		t.Errorf("skewed: %+v and %+v around %v", a, b, m)
	}
	if a.iv[1]-m <= m-a.iv[0] {
		t.Errorf("skewed: %v is not wider above the mean %v", a.iv, m)
	}
	// The difference of means: equal sides give [d, d]; otherwise the
	// interval holds d.
	d := bootstrapMeanDiff([]float64{3, 3}, []float64{1, 1, 1}, p.BootstrapResamples, alpha, seedGen("d0"))
	if !d.ok || d.iv != (Interval{2, 2}) {
		t.Errorf("constant sides: %+v, want [2, 2]", d)
	}
	x := []float64{5.1, 4.8, 6.2, 5.5, 4.9, 5.7, 6.0, 5.3}
	y := []float64{4.2, 4.6, 3.9, 4.4, 5.0, 4.1, 4.3}
	d = bootstrapMeanDiff(x, y, p.BootstrapResamples, alpha, seedGen("d1"))
	dd := mean(x) - mean(y)
	if !d.ok || d.iv[0] > dd || d.iv[1] < dd {
		t.Errorf("difference: %+v around %v", d, dd)
	}
	// Two values each, all resamples from one point mass likely: the delta
	// interval is absent and counts its resamples without spread.
	d = bootstrapMeanDiff([]float64{0, 10}, []float64{0, 10}, p.BootstrapResamples, alpha, seedGen("d2"))
	if d.ok || d.noSpread == 0 {
		t.Errorf("0, 10 against 0, 10: %+v, want no interval", d)
	}
}

func TestStratifiedBootstrap(t *testing.T) {
	p := Defaults()
	alpha := p.alpha()
	w := []float64{1, 2}
	xs := [][]float64{{1, 2, 3, 4, 5, 6}, {2, 2.5, 3, 3.5, 4, 9}}
	ys := [][]float64{{1, 1.5, 2, 2.5, 3, 3.5}, {2, 2, 3, 3, 4, 4}}
	s, _ := stratifiedMean(w, xs)
	r := stratifiedBootstrapMean(w, xs, nil, p.BootstrapResamples, alpha, seedGen("s1"))
	if !r.ok || r.iv[0] > s || r.iv[1] < s {
		t.Errorf("suite mean %v: %+v", s, r)
	}
	sy, _ := stratifiedMean(w, ys)
	r = stratifiedBootstrapMean(w, xs, ys, p.BootstrapResamples, alpha, seedGen("s2"))
	if !r.ok || r.iv[0] > s-sy || r.iv[1] < s-sy {
		t.Errorf("suite delta %v: %+v", s-sy, r)
	}
	c := stratifiedBootstrapMean(w, [][]float64{{1, 1}, {2, 2}}, nil, 100, alpha, seedGen("s3"))
	if !c.ok || c.iv[0] != c.iv[1] {
		t.Errorf("constant tasks: %+v", c)
	}
	median := func(v []float64) float64 { return quantile(sorted(v), 0.5) }
	iv := stratifiedPercentile(w, xs, nil, median, p.BootstrapResamples, alpha, seedGen("s4"))
	sm := weightedMean(w, []float64{median(xs[0]), median(xs[1])})
	if iv[0] > sm || iv[1] < sm {
		t.Errorf("suite median %v: %v", sm, iv)
	}
	iv = stratifiedPercentile(w, xs, ys, median, p.BootstrapResamples, alpha, seedGen("s5"))
	if iv[0] > iv[1] {
		t.Errorf("suite median delta: %v", iv)
	}
}

func TestBoschloo(t *testing.T) {
	p := Defaults()
	g, tol := p.BoschlooGridPoints, p.TieTolerance
	// Fisher's p-value of 5/5 against 1/5 is 12/252.
	if f := fisherP(5, 5, 1, 5, tol); math.Abs(f-12.0/252) > 1e-15 {
		t.Errorf("Fisher 5/5 against 1/5 = %v, want 12/252", f)
	}
	// The power floor of 5 against 5: every candidate trial true, every
	// reference trial false, gives 2·(0.4995·0.5005)^5 on the fixed grid.
	b, f := boschloo(5, 5, 0, 5, g, tol)
	want := 2 * math.Pow(0.4995*0.5005, 5)
	if math.Abs(b-want) > 1e-15 || !near(b, 0.0019531, 7) {
		t.Errorf("Boschloo 5/5 against 0/5 = %v, want %v", b, want)
	}
	if math.Abs(f-2.0/252) > 1e-15 {
		t.Errorf("Fisher 5/5 against 0/5 = %v, want 2/252", f)
	}
	// Equal tables give 1; a table and its mirror the same p-value.
	if b, _ := boschloo(3, 5, 3, 5, g, tol); math.Abs(b-1) > 1e-12 {
		t.Errorf("equal tables: %v, want 1", b)
	}
	b1, _ := boschloo(1, 6, 5, 7, g, tol)
	b2, _ := boschloo(5, 7, 1, 6, g, tol)
	b3, _ := boschloo(5, 6, 2, 7, g, tol)
	if math.Abs(b1-b2) > 1e-12 || math.Abs(b1-b3) > 1e-12 {
		t.Errorf("mirrored tables: %v, %v, %v", b1, b2, b3)
	}
	// Boschloo's p-value is never above Fisher's for the same table, the
	// extremes k = 0 and k = n included.
	for nx := 1; nx <= 8; nx++ {
		for ny := 1; ny <= 8; ny++ {
			for a := 0; a <= nx; a++ {
				for c := 0; c <= ny; c++ {
					b, f := boschloo(a, nx, c, ny, 100, tol)
					if b > f+1e-12 || b <= 0 || b > 1 {
						t.Fatalf("%d/%d against %d/%d: Boschloo %v, Fisher %v", a, nx, c, ny, b, f)
					}
				}
			}
		}
	}
	// A known value: 7/12 against 1/10.
	b, _ = boschloo(7, 12, 1, 10, g, tol)
	if b <= 0.005 || b >= 0.05 {
		t.Errorf("7/12 against 1/10: %v", b)
	}
}

// Spec 008's worked examples of an exact permutation test.
func TestPermutationExact(t *testing.T) {
	p := Defaults()
	x := []float64{3, 5, 4, 6, 12}
	y := []float64{2, 3, 2, 3, 2, 3}
	if tw := welchT(x, y); !near(tw, 2.1918, 4) {
		t.Errorf("Welch's T = %v, want 2.1918", tw)
	}
	r := permutationTest(x, y, welchT, p.PermutationExactMax, mc("w", 10000))
	if !r.exact || r.resamples != 462 || r.p != 10.0/462 {
		t.Errorf("Welch: %+v, want 10/462 of 462 exact", r)
	}
	diff := func(a, b []float64) float64 { return mean(a) - mean(b) }
	if r := permutationTest(x, y, diff, p.PermutationExactMax, mc("d", 10000)); r.p != 4.0/462 {
		t.Errorf("bare difference: %v, want 4/462", r.p)
	}
	// Repeat rates: five equal labels against five distinct.
	same := []float64{0, 0, 0, 0, 0}
	distinct := []float64{0, 1, 2, 3, 4}
	if d := repeatRateDiff(same, distinct); d != 1 {
		t.Errorf("T = %v, want 1", d)
	}
	r = permutationTest(same, distinct, repeatRateDiff, p.PermutationExactMax, mc("r", 10000))
	if !r.exact || r.resamples != 252 || r.p != 12.0/252 {
		t.Errorf("repeat rate: %+v, want 12/252 of 252", r)
	}
	// Equal sides give p = 1.
	r = permutationTest([]float64{1, 2, 3}, []float64{1, 2, 3}, welchT, p.PermutationExactMax, mc("e", 10000))
	if r.p != 1 {
		t.Errorf("equal sides: %v, want 1", r.p)
	}
	// C(20, 10) = 184,756 is enumerated under PERMUTATION_EXACT_MAX.
	if c := choose(20, 10, p.PermutationExactMax); c != 184756 {
		t.Errorf("C(20, 10) = %d", c)
	}
	if c := choose(22, 11, p.PermutationExactMax); c != p.PermutationExactMax+1 {
		t.Errorf("C(22, 11) = %d, want the cap", c)
	}
	if c := choose(3, 4, 10); c != 0 {
		t.Errorf("C(3, 4) = %d", c)
	}
}

// Above PERMUTATION_EXACT_MAX the test draws relabelings one at a time and
// stops at the PERMUTATION_STOP_EXCEEDANCES-th exceedance.
func TestPermutationSequential(t *testing.T) {
	// The 200th exceedance at the 800th draw gives 200/800.
	draws := 0
	r := sequential(mcConfig{b: 10000, stop: 200, tol: 1e-9}, 1, func() float64 {
		draws++
		if draws%4 == 0 {
			return 1
		}
		return 0
	})
	if r.p != 0.25 || r.resamples != 800 {
		t.Errorf("%+v, want p = 0.25 at 800", r)
	}
	// Without enough exceedances, all B draws and (1 + c)/(1 + B).
	r = sequential(mcConfig{b: 999, stop: 200, tol: 1e-9}, 1, func() float64 { return 0 })
	if r.p != 1.0/1000 || r.resamples != 999 {
		t.Errorf("%+v, want 1/1000 at 999", r)
	}
	// Two cells of 15: C(30, 15) is above the exact bound; equal halves are
	// far from significant and stop early, a clear difference draws all.
	p := Defaults()
	var x, y []float64
	for i := range 15 {
		x = append(x, float64(i))
		y = append(y, float64(i)+0.5)
	}
	r = permutationTest(x, y, welchT, p.PermutationExactMax, mc("near", 16000))
	if r.exact || r.resamples >= 16000 || r.p < 0.5 {
		t.Errorf("near-equal sides: %+v, want an early stop", r)
	}
	again := permutationTest(x, y, welchT, p.PermutationExactMax, mc("near", 16000))
	if again != r {
		t.Errorf("the same seed gave %+v and %+v", r, again)
	}
	for i := range y {
		y[i] += 20
	}
	r = permutationTest(x, y, welchT, p.PermutationExactMax, mc("far", 16000))
	if r.exact || r.resamples != 16000 || r.p != 1.0/16001 {
		t.Errorf("far sides: %+v, want every draw and 1/16001", r)
	}
}

// Welch's statistic and the studentized quantile statistic: Hall and
// Sheather's bandwidth matches the worked examples.
func TestStudentizedStatistics(t *testing.T) {
	if b := hallSheather(5, 0.5, z95); !near(b, 0.5682, 4) {
		t.Errorf("bandwidth n = 5, q = 0.5: %v, want 0.5682", b)
	}
	if b := hallSheather(35, 0.9, z95); !near(b, 0.1058, 4) {
		t.Errorf("bandwidth n = 35, q = 0.9: %v, want 0.1058", b)
	}
	// Zero denominators: equal constant sides give 0, unequal ones infinity
	// by the sign.
	if v := welchT([]float64{1, 1}, []float64{1, 1}); v != 0 {
		t.Errorf("Welch of equal constants: %v", v)
	}
	if v := welchT([]float64{2, 2}, []float64{1, 1}); !math.IsInf(v, 1) {
		t.Errorf("Welch of 2s against 1s: %v", v)
	}
	if v := welchT([]float64{1, 1}, []float64{2, 2}); !math.IsInf(v, -1) {
		t.Errorf("Welch of 1s against 2s: %v", v)
	}
	sq := studentizedQuantile(0.5, hallSheather(6, 0.5, z95), hallSheather(6, 0.5, z95))
	x := []float64{1, 2, 3, 4, 5, 6}
	if v := sq(x, x); v != 0 {
		t.Errorf("studentized median of equal sides: %v", v)
	}
	if v := sq(x, []float64{0, 1, 2, 3, 4, 5}); v <= 0 {
		t.Errorf("studentized median of a shift: %v", v)
	}
	if v := sq([]float64{2, 2, 2}, []float64{1, 1, 1}); !math.IsInf(v, 1) {
		t.Errorf("studentized median of constants: %v", v)
	}
}

func TestRepeatRate(t *testing.T) {
	// a, a, a, b, c: 6/20; five equal labels: 1; five distinct: 0.
	for _, c := range []struct {
		v    []float64
		want float64
	}{
		{[]float64{0, 0, 0, 1, 2}, 0.3},
		{[]float64{3, 3, 3, 3, 3}, 1},
		{[]float64{0, 1, 2, 3, 4}, 0},
		{[]float64{1, 0}, 0},
	} {
		if got := repeatRate(c.v); got != c.want {
			t.Errorf("R(%v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestBenjaminiHochberg(t *testing.T) {
	// A reference computation: m = 5.
	p := []float64{0.01, 0.04, 0.03, 0.005, 0.2}
	want := []float64{0.025, 0.05, 0.05, 0.025, 0.2}
	got := benjaminiHochberg(p)
	for i := range p {
		if math.Abs(got[i]-want[i]) > 1e-15 {
			t.Errorf("q(%v) = %v, want %v", p[i], got[i], want[i])
		}
	}
	// Ties receive the same q, and q never exceeds 1.
	got = benjaminiHochberg([]float64{0.02, 0.02, 0.9, 0.95})
	if got[0] != got[1] || got[0] != 0.04 || got[2] != 0.95 || got[3] != 0.95 {
		t.Errorf("ties: %v", got)
	}
	if q := benjaminiHochberg([]float64{0.8}); q[0] != 0.8 {
		t.Errorf("one p-value: %v", q)
	}
	// q is never below p and keeps p's order.
	ps := []float64{0.001, 0.2, 0.013, 0.04, 0.3, 0.0005, 0.07, 0.04}
	qs := benjaminiHochberg(ps)
	for i := range ps {
		if qs[i] < ps[i] {
			t.Errorf("q %v below p %v", qs[i], ps[i])
		}
		for j := range ps {
			if ps[i] < ps[j] && qs[i] > qs[j] {
				t.Errorf("order broken: p %v q %v, p %v q %v", ps[i], qs[i], ps[j], qs[j])
			}
		}
	}
}

func TestHolm(t *testing.T) {
	h := holm([]float64{0.003, 0.03, 0.4}, []bool{true, true, true})
	want := []float64{0.009, 0.06, 0.4}
	for i := range h {
		if math.Abs(h[i]-want[i]) > 1e-15 {
			t.Errorf("h = %v, want %v", h, want)
		}
	}
	// An insufficient pair counts in m and is adjusted to nothing.
	h = holm([]float64{0.01, math.NaN(), 0.02}, []bool{true, false, true})
	if math.Abs(h[0]-0.03) > 1e-15 || math.Abs(h[2]-0.04) > 1e-15 {
		t.Errorf("with an insufficient pair: %v, want 0.03 and 0.04", h)
	}
	// Ties receive the same h.
	h = holm([]float64{0.01, 0.01, 0.5}, []bool{true, true, true})
	if h[0] != h[1] || h[0] != 0.03 {
		t.Errorf("ties: %v", h)
	}
}

func TestFisherFreemanHalton(t *testing.T) {
	p := Defaults()
	// A 2 by 2 table is Fisher's exact test: 5/5 against 1/5.
	r, ok := freemanHalton([]int{5, 0}, []int{1, 4}, p.PermutationExactMax, p.TieTolerance)
	if !ok || math.Abs(r.p-12.0/252) > 1e-12 || !r.exact {
		t.Errorf("2 by 2: %+v, want 12/252", r)
	}
	// A 2 by 3 table against a reference value enumerated by hand in exact
	// rationals: rows (3, 1, 1) and (0, 2, 3) give 2/7.
	r, ok = freemanHalton([]int{3, 1, 1}, []int{0, 2, 3}, p.PermutationExactMax, p.TieTolerance)
	if !ok || math.Abs(r.p-2.0/7) > 1e-12 {
		t.Errorf("2 by 3: %+v, want 2/7", r)
	}
	// Equal rows give 1.
	r, _ = freemanHalton([]int{2, 2, 1}, []int{2, 2, 1}, p.PermutationExactMax, p.TieTolerance)
	if math.Abs(r.p-1) > 1e-12 {
		t.Errorf("equal rows: %v", r.p)
	}
	// Too many tables leave the exact test.
	if _, ok := freemanHalton([]int{10, 10, 10}, []int{10, 10, 10}, 50, p.TieTolerance); ok {
		t.Error("an exact test above its limit")
	}
	if n := countTables([]int{2, 2}, 2, 100); n != 3 {
		t.Errorf("tables of columns 2, 2 and a row of 2: %d, want 3", n)
	}
	// A one-label table has p = 1.
	tr, method, stat := labelTest([]float64{0, 0}, []float64{0, 0, 0}, 3, p.PermutationExactMax, mc("one", 10000))
	if tr.p != 1 || method != methodFFH || stat != statTableProb {
		t.Errorf("one label: %+v %s %s", tr, method, stat)
	}
	tr, method, _ = labelTest([]float64{0, 0, 0, 1}, []float64{1, 1, 2, 2}, 3, p.PermutationExactMax, mc("two", 10000))
	if method != methodFFH || !tr.exact || tr.p <= 0 || tr.p > 1 {
		t.Errorf("small table: %+v %s", tr, method)
	}
}

func TestChiSquarePermutation(t *testing.T) {
	p := Defaults()
	var x, y []float64
	for i := range 30 {
		x = append(x, float64(i%3))
		y = append(y, float64((i%5)%3))
	}
	a, method, stat := labelTest(x, y, 3, 20, mc("chi", 10000))
	b, _, _ := labelTest(x, y, 3, 20, mc("chi", 10000))
	if method != methodChiSquare || stat != statPearsonX2 || a.exact {
		t.Errorf("method %s statistic %s exact %v", method, stat, a.exact)
	}
	if a != b {
		t.Errorf("the same seed gave %+v and %+v", a, b)
	}
	if a.p <= 0 || a.p > 1 {
		t.Errorf("p = %v", a.p)
	}
	// Pearson's X² of a known table: rows (10, 0) and (0, 10) is 20.
	if v := pearsonX2(2)(slices.Repeat([]float64{0}, 10), slices.Repeat([]float64{1}, 10)); math.Abs(v-20) > 1e-12 {
		t.Errorf("X² = %v, want 20", v)
	}
	_ = p
}
