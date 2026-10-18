// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"math"
	"slices"
)

// mean is the mean of v, its sum taken in v's order.
func mean(v []float64) float64 {
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// constant reports whether every value of v is equal: such a set has no
// spread, whatever the rounding of its mean.
func constant(v []float64) bool {
	for _, x := range v[1:] {
		if x != v[0] {
			return false
		}
	}
	return true
}

// variance is the sample variance of v, divisor len(v) - 1, by two passes
// in v's order; exactly 0 for a constant set, and 0 for a single value.
func variance(v []float64) float64 {
	if len(v) < 2 || constant(v) {
		return 0
	}
	m := mean(v)
	s := 0.0
	for _, x := range v {
		d := x - m
		s += float64(d * d)
	}
	return s / float64(len(v)-1)
}

// sorted returns a sorted copy of v.
func sorted(v []float64) []float64 {
	s := slices.Clone(v)
	slices.Sort(s)
	return s
}

// quantile is Hyndman and Fan's type 7 quantile q of the sorted values s:
// with h = (n - 1)·q + 1, x(floor h) + (h - floor h)·(x(floor h + 1) -
// x(floor h)), x(n + 1) read as x(n). Of infinite values: when h is whole
// the quantile is x(h); otherwise an infinite value it reads is the
// quantile, and two of opposite signs leave it undefined, NaN.
func quantile(s []float64, q float64) float64 {
	n := len(s)
	h := float64(float64(n-1)*q) + 1
	fl := math.Floor(h)
	frac := h - fl
	i := int(fl)
	lo := s[i-1]
	if frac == 0 {
		return lo
	}
	hi := lo
	if i < n {
		hi = s[i]
	}
	switch {
	case math.IsInf(lo, 0) && math.IsInf(hi, 0) && lo != hi:
		return math.NaN()
	case math.IsInf(lo, 0):
		return lo
	case math.IsInf(hi, 0):
		return hi
	}
	return lo + float64(frac*(hi-lo))
}

// finite reports whether x is neither infinite nor NaN.
func finite(x float64) bool { return !math.IsInf(x, 0) && !math.IsNaN(x) }
