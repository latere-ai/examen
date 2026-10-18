// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"strconv"
	"testing"
)

// near reports whether got rounds to want at the decimals want is written
// with: within half a unit of its last decimal.
func near(got, want float64, decimals int) bool {
	return math.Abs(got-want) <= 0.5*math.Pow(10, -float64(decimals))+1e-15
}

func checkInterval(t *testing.T, name string, got Interval, lo, hi float64) {
	t.Helper()
	if !near(got[0], lo, 4) || !near(got[1], hi, 4) {
		t.Errorf("%s = [%.6f, %.6f], want [%.4f, %.4f]", name, got[0], got[1], lo, hi)
	}
}

var z95 = Defaults().z()

// Spec 008's worked examples of z, Wilson's interval and its clamp.
func TestWilson(t *testing.T) {
	if s := strconv.FormatFloat(z95, 'g', 16, 64); s != "1.959963984540054" {
		t.Errorf("z = %s, want 1.959963984540054", s)
	}
	zero := wilsonCount(0, 5, z95)
	if zero[0] != 0 || math.Signbit(zero[0]) {
		t.Errorf("the lower bound of 0 of 5 is %v, want exactly 0", zero[0])
	}
	if raw := wilsonRaw(0, 5, z95); raw[0] > 0 || raw[0] < -1e-16 {
		t.Errorf("the unclamped lower bound of 0 of 5 is %v, want within rounding of 0 and not above it", raw[0])
	}
	checkInterval(t, "0 of 5", zero, 0, 0.4345)
	for _, c := range []struct {
		k, n   int
		lo, hi float64
	}{
		{5, 5, 0.5655, 1}, {11, 49, 0.1302, 0.3588}, {7, 50, 0.0695, 0.2619}, {12, 50, 0.1430, 0.3741},
	} {
		iv := wilsonCount(c.k, c.n, z95)
		checkInterval(t, strconv.Itoa(c.k)+" of "+strconv.Itoa(c.n), iv, c.lo, c.hi)
		if p := float64(c.k) / float64(c.n); iv[0] > p || iv[1] < p {
			t.Errorf("%d of %d: %v does not hold the estimate", c.k, c.n, iv)
		}
	}
	if iv := wilsonCount(5, 5, z95); iv[1] != 1 {
		t.Errorf("the upper bound of 5 of 5 is %v, want exactly 1", iv[1])
	}
	// Every interval lies in [0, 1], holds its estimate within binary64's
	// rounding (the clamp removes a bound rounded outside [0, 1], not one
	// rounded inside it: 0 of 7 has the lower bound 2.8e-17 and 57 of 57 the upper
	// bound 1 - 2.2e-16), and k of n
	// mirrors n - k of n.
	for n := 1; n <= 60; n++ {
		for k := 0; k <= n; k++ {
			iv := wilsonCount(k, n, z95)
			m := wilsonCount(n-k, n, z95)
			p := float64(k) / float64(n)
			if iv[0] < 0 || iv[1] > 1 || iv[0] > p+1e-15 || iv[1] < p-1e-15 {
				t.Fatalf("%d of %d: %v", k, n, iv)
			}
			if math.Abs(iv[0]-(1-m[1])) > 1e-12 || math.Abs(iv[1]-(1-m[0])) > 1e-12 {
				t.Fatalf("%d of %d is %v and %d of %d is %v: not mirrored", k, n, iv, n-k, n, m)
			}
		}
	}
}

func TestNewcombe(t *testing.T) {
	for _, c := range []struct {
		kx, nx, ky, ny int
		delta, lo, hi  float64
	}{
		{0, 5, 2, 5, -0.4000, -0.7693, 0.1182},
		{7, 50, 11, 49, -0.0845, -0.2362, 0.0696},
		{12, 50, 11, 49, 0.0155, -0.1502, 0.1794},
		{12, 50, 7, 50, 0.1000, -0.0558, 0.2515},
	} {
		iv := newcombe(c.kx, c.nx, c.ky, c.ny, z95)
		d := float64(c.kx)/float64(c.nx) - float64(c.ky)/float64(c.ny)
		if !near(d, c.delta, 4) {
			t.Errorf("delta %v, want %v", d, c.delta)
		}
		checkInterval(t, "Newcombe", iv, c.lo, c.hi)
		// Swapping the sides negates the interval.
		sw := newcombe(c.ky, c.ny, c.kx, c.nx, z95)
		if math.Abs(sw[0]+iv[1]) > 1e-12 || math.Abs(sw[1]+iv[0]) > 1e-12 {
			t.Errorf("swapped %v is not the negation of %v", sw, iv)
		}
	}
	for _, c := range [][4]int{{5, 5, 0, 5}, {0, 5, 5, 5}, {20, 20, 0, 20}} {
		iv := newcombe(c[0], c[1], c[2], c[3], z95)
		if iv[0] < -1 || iv[1] > 1 {
			t.Errorf("%v: %v leaves [-1, 1]", c, iv)
		}
	}
}

// Kish's effective sample size with Wilson's interval for a suite-level
// rate, and Agresti and Caffo's interval for a suite-level difference.
func TestSuiteRateIntervals(t *testing.T) {
	ten := make([]stratum, 10)
	for i := range ten {
		ten[i] = stratum{w: 1, k: 5, n: 5}
	}
	s, neff := kish(ten)
	if s != 1 || math.Abs(neff-50) > 1e-12 {
		t.Errorf("S = %v, n_eff = %v, want 1 and 50", s, neff)
	}
	checkInterval(t, "ten tasks at 5 of 5", wilson(s, neff, z95), 0.9287, 1)

	s, neff = kish([]stratum{{2, 4, 5}, {1, 3, 10}, {1, 9, 10}})
	if math.Abs(s-0.7) > 1e-15 || math.Abs(neff-16) > 1e-12 {
		t.Errorf("S = %v, n_eff = %v, want 7/10 and 16", s, neff)
	}
	checkInterval(t, "weights 2, 1, 1", wilson(s, neff, z95), 0.4560, 0.8666)

	ref := make([]stratum, 10)
	for i := range ref {
		ref[i] = stratum{w: 1, k: 5, n: 5}
	}
	ref[8].k, ref[9].k = 4, 4
	sx, _ := kish(ten)
	sy, _ := kish(ref)
	if d := sx - sy; math.Abs(d-0.04) > 1e-15 {
		t.Errorf("delta %v, want 1/25", d)
	}
	ac := agrestiCaffo(ten, ref, z95)
	checkInterval(t, "Agresti and Caffo", ac, -0.0912, 0.1483)
	center := (ac[0] + ac[1]) / 2
	if math.Abs(center-1.0/35) > 1e-12 {
		t.Errorf("the interval is centered at %v, want D~ = 1/35", center)
	}
	if half := (ac[1] - ac[0]) / 2; math.Abs(half-z95*math.Sqrt(32.0/8575)) > 1e-12 {
		t.Errorf("half width %v, want z·sqrt(32/8575)", half)
	}
}

func TestQuantileType7(t *testing.T) {
	inf := math.Inf(1)
	for _, c := range []struct {
		v    []float64
		q    float64
		want float64
	}{
		{[]float64{1, 2, 3, 4, 5}, 0.5, 3},
		{[]float64{1, 2, 3, 4}, 0.5, 2.5},
		{[]float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 0.9, 9.1},
		{[]float64{3, 3, 3, 7}, 0.5, 3},
		{[]float64{2}, 0.9, 2},
		{[]float64{1, 2}, 1, 2},
		{[]float64{1, 2}, 0, 1},
		{[]float64{-inf, -inf, 1}, 0.5, -inf},
		{[]float64{-inf, 1, 2}, 0.25, -inf},
		{[]float64{-inf, 1, 2}, 0.5, 1},
		{[]float64{1, 2, inf}, 0.75, inf},
	} {
		if got := quantile(c.v, c.q); got != c.want {
			t.Errorf("Q(%v) of %v = %v, want %v", c.q, c.v, got, c.want)
		}
	}
	if got := quantile([]float64{-inf, inf}, 0.5); !math.IsNaN(got) {
		t.Errorf("Q(0.5) of -inf, +inf = %v, want undefined", got)
	}
}

// The ranks of the order-statistic interval follow the rule and the worked
// examples, and its binomial coverage reaches its level.
func TestOrderStatisticInterval(t *testing.T) {
	p := Defaults()
	level, tol := p.ConfidenceLevel, p.TieTolerance
	delta := math.Sqrt(level)
	if !near(delta, 0.974679, 6) {
		t.Errorf("sqrt(0.95) = %v", delta)
	}
	for _, c := range []struct {
		n     int
		q, lv float64
		l, u  int
		cover float64
	}{
		{5, 0.5, level, 0, 0, 0},
		{6, 0.5, level, 1, 6, 31.0 / 32},
		{6, 0.5, delta, 0, 0, 0},
		{7, 0.5, delta, 1, 7, 63.0 / 64},
		{28, 0.9, level, 0, 0, 0},
		{29, 0.9, level, 21, 29, 0.9513},
		{34, 0.9, delta, 0, 0, 0},
		{35, 0.9, delta, 24, 35, 0.9749},
	} {
		l, u, cover, ok := osRanks(c.n, c.q, c.lv, tol)
		if c.l == 0 {
			if ok {
				t.Errorf("n = %d, q = %v at %v: ranks [%d, %d], want none", c.n, c.q, c.lv, l, u)
			}
			continue
		}
		if !ok || l != c.l || u != c.u || !near(cover, c.cover, 4) {
			t.Errorf("n = %d, q = %v at %v: [x(%d), x(%d)] cover %v, want [x(%d), x(%d)] cover %v", c.n, c.q, c.lv, l, u, cover, c.l, c.u, c.cover)
		}
	}
	// MIN_TRIALS_FOR_P90_INTERVAL is the pair rule's own threshold at the
	// default level.
	if _, _, _, ok := osRanks(p.MinTrialsForP90Interval-1, 0.9, level, tol); ok {
		t.Error("a p90 below MIN_TRIALS_FOR_P90_INTERVAL found ranks")
	}
	if _, _, _, ok := osRanks(p.MinTrialsForP90Interval, 0.9, level, tol); !ok {
		t.Error("a p90 at MIN_TRIALS_FOR_P90_INTERVAL found no ranks")
	}
	// Every found pair covers at its level, for n up to 200.
	for n := 2; n <= 200; n++ {
		for _, q := range []float64{0.5, 0.9} {
			l, u, cover, ok := osRanks(n, q, level, tol)
			if ok && (cover < level || l < 1 || u > n || l >= u) {
				t.Fatalf("n = %d, q = %v: [%d, %d] covers %v", n, q, l, u, cover)
			}
		}
	}
	if _, ok := orderStatInterval([]float64{1, 2, 3, 4, 5}, 0.5, level, tol); ok {
		t.Error("a median of 5 values has an interval")
	}
	iv, ok := orderStatInterval([]float64{1, 2, 3, 4, 5, 6}, 0.5, level, tol)
	if !ok || iv != (Interval{1, 6}) {
		t.Errorf("median of 1 … 6: %v %v, want [1, 6]", iv, ok)
	}
	if _, _, _, ok := osRanks(1, 0.5, level, tol); ok {
		t.Error("one value has ranks")
	}
}
