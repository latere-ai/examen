// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"

	"latere.ai/x/examen/internal/analysis/mathx"
)

// Interval is [low, high], written in JSON as a two-element array.
type Interval [2]float64

// Low and High are the interval's bounds.
func (iv Interval) Low() float64  { return iv[0] }
func (iv Interval) High() float64 { return iv[1] }

// wilsonRaw is Wilson's score interval of the proportion p over n trials
// before the clamp:
//
//	center = (p + z²/(2n)) / (1 + z²/n)
//	half   = (z / (1 + z²/n)) · sqrt(p(1 - p)/n + z²/(4n²))
func wilsonRaw(p, n, z float64) Interval {
	z2 := float64(z * z)
	den := 1 + z2/n
	center := (p + z2/float64(2*n)) / den
	half := float64((z / den) * math.Sqrt(float64(p*(1-p))/n+z2/float64(float64(4*n)*n)))
	return Interval{center - half, center + half}
}

// wilson is Wilson's interval clamped to [0, 1]: the bounds lie in it in
// exact arithmetic, and the clamp removes binary64's rounding, which puts
// the lower bound of 0 of n a hair below 0.
func wilson(p, n, z float64) Interval {
	iv := wilsonRaw(p, n, z)
	return Interval{math.Max(0, iv[0]), math.Min(1, iv[1])}
}

// wilsonCount is Wilson's clamped interval of k of n.
func wilsonCount(k, n int, z float64) Interval {
	return wilson(float64(k)/float64(n), float64(n), z)
}

// newcombe is Newcombe's hybrid score interval of k_x/n_x - k_y/n_y, from
// the two sides' clamped Wilson intervals, clamped to [-1, 1]:
//
//	lower = (p1 - p2) - sqrt((p1 - l1)² + (u2 - p2)²)
//	upper = (p1 - p2) + sqrt((u1 - p1)² + (p2 - l2)²)
func newcombe(kx, nx, ky, ny int, z float64) Interval {
	p1 := float64(kx) / float64(nx)
	p2 := float64(ky) / float64(ny)
	w1 := wilsonCount(kx, nx, z)
	w2 := wilsonCount(ky, ny, z)
	d := p1 - p2
	a, b := p1-w1[0], w2[1]-p2
	c, e := w1[1]-p1, p2-w2[0]
	lower := d - math.Sqrt(float64(a*a)+float64(b*b))
	upper := d + math.Sqrt(float64(c*c)+float64(e*e))
	return Interval{math.Max(-1, lower), math.Min(1, upper)}
}

// stratum is one included task of a suite-level figure of a rate: its
// weight, its true values and its valued trials.
type stratum struct {
	w    float64
	k, n int
}

// kish returns the weighted rate S = Σ w_t (k_t/n_t) / W and Kish's
// effective sample size n_eff = W² / Σ (w_t²/n_t), the number of equally
// weighted trials whose rate would be as precise as S.
func kish(tasks []stratum) (s, neff float64) {
	var num, w, den float64
	for _, t := range tasks {
		num += float64(t.w * (float64(t.k) / float64(t.n)))
		w += t.w
		den += float64(t.w*t.w) / float64(t.n)
	}
	return num / w, float64(w*w) / den
}

// agrestiCaffo is Agresti and Caffo's interval of a weighted difference of
// rates over paired tasks, which adds one success and one failure to each
// side of each task:
//
//	p~_t = (k_t + 1) / (n_t + 2)
//	D~   = Σ w_t (p~_xt - p~_yt) / W
//	V~   = Σ w_t² (p~_xt (1 - p~_xt)/(n_xt + 2) + p~_yt (1 - p~_yt)/(n_yt + 2)) / W²
//
// clamped to [-1, 1] and centered at the adjusted D~.
func agrestiCaffo(x, y []stratum, z float64) Interval {
	var d, v, w float64
	for i := range x {
		px := float64(x[i].k+1) / float64(x[i].n+2)
		py := float64(y[i].k+1) / float64(y[i].n+2)
		wt := x[i].w
		d += float64(wt * (px - py))
		vt := float64(px*(1-px))/float64(x[i].n+2) + float64(py*(1-py))/float64(y[i].n+2)
		v += float64(float64(wt*wt) * vt)
		w += wt
	}
	d /= w
	v /= float64(w * w)
	half := float64(z * math.Sqrt(v))
	return Interval{math.Max(-1, d-half), math.Min(1, d+half)}
}

// osRanks is the pair of ranks 1 <= l < u <= n of the order-statistic
// interval [x(l), x(u)] of the quantile q at the level asked. B, the
// number of values below the true quantile, is binomial with n trials and
// probability q, and the interval covers with probability at least
//
//	cover(l, u) = Σ_{i = l}^{u - 1} C(n, i) q^i (1 - q)^(n - i)
//
// The pair is the one of the smallest u - l whose coverage reaches the
// level; among equal widths, the one whose tails P(B < l) and P(B >= u)
// differ least, two differences within tol of each other read as equal;
// then the smallest l. ok is false when no pair reaches the level.
func osRanks(n int, q, level, tol float64) (l, u int, cover float64, ok bool) {
	if n < 2 {
		return 0, 0, 0, false
	}
	lq, l1q := mathx.Log(q), mathx.Log(1-q)
	pmf := make([]float64, n+1)
	for i := range pmf {
		pmf[i] = mathx.Exp(mathx.LogChoose(n, i) + float64(float64(i)*lq) + float64(float64(n-i)*l1q))
	}
	// below[i] is P(B < i), summed upward; above[i] is P(B >= i), summed
	// downward, so each tail is read from its own small end.
	below := make([]float64, n+2)
	for i := 1; i <= n+1; i++ {
		below[i] = below[i-1] + pmf[i-1]
	}
	above := make([]float64, n+2)
	for i := n; i >= 0; i-- {
		above[i] = above[i+1] + pmf[i]
	}
	for width := 1; width < n; width++ {
		best, bestDiff := 0, 0.0
		for lo := 1; lo+width <= n; lo++ {
			c := below[lo+width] - below[lo]
			if c < level {
				continue
			}
			diff := math.Abs(below[lo] - above[lo+width])
			if best == 0 || diff < bestDiff-tol {
				best, bestDiff = lo, diff
			}
		}
		if best != 0 {
			return best, best + width, below[best+width] - below[best], true
		}
	}
	return 0, 0, 0, false
}

// orderStatInterval is the order-statistic interval of the quantile q of
// the sorted values s at the level, when one exists.
func orderStatInterval(s []float64, q, level, tol float64) (Interval, bool) {
	l, u, _, ok := osRanks(len(s), q, level, tol)
	if !ok {
		return Interval{}, false
	}
	return Interval{s[l-1], s[u-1]}, true
}
