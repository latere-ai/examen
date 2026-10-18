// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package mathx holds the elementary functions the analysis computes with:
// exp, log, the log-gamma function, erf, erfc and erfinv, and the powers,
// binomial coefficients and normal density built from them. They are
// Examen's own pure-Go code, written from the series and recurrences named
// on each function, with no assembly and with every product rounded to
// binary64 before it is added (an explicit float64 conversion, which
// forbids the fused multiply-add Go emits on arm64). The standard library's
// exp and log run assembly on amd64 and its pure-Go code is fused on arm64,
// so the same figure could differ in its last bits between two machines;
// these give the same bits on every architecture.
//
// The functions of package math that stay allowed in the analysis are the
// ones IEEE 754 defines exactly, whatever the instruction that computes
// them: Sqrt (correctly rounded), Abs, Floor, Ceil, Trunc, Max, Min,
// Copysign, Signbit, Ldexp, Frexp, Inf, NaN, IsInf, IsNaN, Float64bits and
// Float64frombits. Floor, Max and Min have assembly on some architectures,
// but each returns the one exact answer, so the instruction cannot change
// the bits.
package mathx

import "math"

// The natural logarithm of 2 split in two: ln2Hi carries its leading 32
// significand bits, so that k*ln2Hi is exact for every exponent k of a
// binary64, and ln2Lo the rest.
const (
	ln2Hi = 0x1.62e42feep-1
	ln2Lo = 0x1.a39ef35793c76p-33
	log2e = 1 / math.Ln2
)

// Bounds of Exp's argument: above expMax the result overflows, below expMin
// it is smaller than half the least subnormal.
const (
	expMax = 709.782712893383973096
	expMin = -745.13321910194110842
)

// expTaylor holds 1/(j+2)! for j = 0 … 12: exp(r) = 1 + r + r²·Σ r^j/(j+2)!,
// whose first omitted term is below 2^-58 on |r| <= ln2/2.
var expTaylor = [...]float64{
	1.0 / 2, 1.0 / 6, 1.0 / 24, 1.0 / 120, 1.0 / 720, 1.0 / 5040,
	1.0 / 40320, 1.0 / 362880, 1.0 / 3628800, 1.0 / 39916800,
	1.0 / 479001600, 1.0 / 6227020800, 1.0 / 87178291200,
}

// Exp returns e**x. The argument is reduced to x = k·ln2 + r with |r| at
// most ln2/2, exp(r) is the Taylor series above evaluated by Horner's rule,
// and the result is scaled by 2**k.
func Exp(x float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsInf(x, 1):
		return x
	case x > expMax:
		return math.Inf(1)
	case x < expMin:
		return 0
	}
	k := math.Floor(float64(x*log2e) + 0.5)
	hi := x - float64(k*ln2Hi)
	r := hi - float64(k*ln2Lo)
	p := expTaylor[len(expTaylor)-1]
	for i := len(expTaylor) - 2; i >= 0; i-- {
		p = float64(p*r) + expTaylor[i]
	}
	e := 1 + (r + float64(float64(r*r)*p))
	return math.Ldexp(e, int(k))
}

// logSeries holds 2/(2j+1) for j = 1 … 11, the coefficients of
// R(z) = Σ 2z^j/(2j+1) with z = s², s = f/(2+f), for which
// log(1+f) = 2·atanh(s) = 2s + s·R.
var logSeries = [...]float64{
	2.0 / 3, 2.0 / 5, 2.0 / 7, 2.0 / 9, 2.0 / 11, 2.0 / 13,
	2.0 / 15, 2.0 / 17, 2.0 / 19, 2.0 / 21, 2.0 / 23,
}

// Log returns the natural logarithm of x. With x = 2**k·(1+f) and 1+f in
// [sqrt(2)/2, sqrt(2)), log(1+f) is the series of 2·atanh(f/(2+f)),
// written as f - (f²/2 - s·(f²/2 + R)) so that its leading term, f, is
// exact.
func Log(x float64) float64 {
	switch {
	case math.IsNaN(x) || x < 0:
		return math.NaN()
	case x == 0:
		return math.Inf(-1)
	case math.IsInf(x, 1):
		return x
	}
	m, e := math.Frexp(x)
	if m < math.Sqrt2/2 {
		m = float64(m * 2)
		e--
	}
	f := m - 1
	k := float64(e)
	s := f / (2 + f)
	z := float64(s * s)
	r := logSeries[len(logSeries)-1]
	for i := len(logSeries) - 2; i >= 0; i-- {
		r = float64(r*z) + logSeries[i]
	}
	r = float64(r * z)
	hfsq := float64(0.5 * float64(f*f))
	return float64(k*ln2Hi) - ((hfsq - (float64(s*(hfsq+r)) + float64(k*ln2Lo))) - f)
}

// Pow returns x**y for x at least 0, as exp(y·log x). A negative x has no
// real power for every y, and gives NaN.
func Pow(x, y float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsNaN(y) || x < 0:
		return math.NaN()
	case y == 0 || x == 1:
		return 1
	case x == 0:
		if y > 0 {
			return 0
		}
		return math.Inf(1)
	}
	return Exp(float64(y * Log(x)))
}

// halfLog2Pi is log(2π)/2.
const halfLog2Pi = 0.918938533204672741780329736406

// stirlingShift is the argument below which Lgamma shifts x up by the
// recurrence before it reads Stirling's series.
const stirlingShift = 10

// stirlingSeries holds B_2k / (2k(2k-1)) for k = 1 … 8, the coefficients of
// Stirling's series in 1/x: their first omitted term is below 2^-54 at
// x >= 10, where log Γ is above 12.
var stirlingSeries = [...]float64{
	1.0 / 12, -1.0 / 360, 1.0 / 1260, -1.0 / 1680, 1.0 / 1188,
	-691.0 / 360360, 1.0 / 156, -3617.0 / 122400,
}

// factorialMax is the largest whole argument whose Γ, a factorial, Lgamma
// computes as a product: 170! is the largest factorial below the largest
// binary64, and every factorial up to 18! is exact in binary64.
const factorialMax = 171

// Lgamma returns log Γ(x) for x above 0, the only arguments the analysis
// reads. A whole x up to factorialMax is the logarithm of (x-1)! computed
// as a product, so log Γ(1) and log Γ(2) are exactly 0 and the binomial
// coefficients of the tests are read to the last bit or two; another x
// below stirlingShift is shifted by
// log Γ(x) = log Γ(x+n) - log(x(x+1)…(x+n-1)); and Stirling's series gives
// the rest. Zero, a negative x and NaN give NaN.
func Lgamma(x float64) float64 {
	switch {
	case math.IsNaN(x) || x <= 0:
		return math.NaN()
	case math.IsInf(x, 1):
		return x
	case x <= factorialMax && x == math.Trunc(x):
		f := 1.0
		for i := 2.0; i < x; i++ {
			f = float64(f * i)
		}
		return Log(f)
	case x >= stirlingShift:
		return stirling(x)
	}
	p := x
	y := x + 1
	for y < stirlingShift {
		p = float64(p * y)
		y++
	}
	return stirling(y) - Log(p)
}

// stirling is Stirling's series for log Γ(x), x at least stirlingShift.
func stirling(x float64) float64 {
	w := 1 / x
	w2 := float64(w * w)
	s := stirlingSeries[len(stirlingSeries)-1]
	for i := len(stirlingSeries) - 2; i >= 0; i-- {
		s = float64(s*w2) + stirlingSeries[i]
	}
	s = float64(s * w)
	return float64((x-0.5)*Log(x)) - x + halfLog2Pi + s
}

// LogChoose returns the logarithm of the binomial coefficient C(n, k):
// minus infinity outside 0 <= k <= n, exactly 0 at k = 0 and k = n.
func LogChoose(n, k int) float64 {
	switch {
	case k < 0 || k > n:
		return math.Inf(-1)
	case k == 0 || k == n:
		return 0
	}
	return Lgamma(float64(n+1)) - Lgamma(float64(k+1)) - Lgamma(float64(n-k+1))
}

// twoOverSqrtPi is 2/sqrt(π), and invSqrt2Pi 1/sqrt(2π).
const (
	twoOverSqrtPi = 1.12837916709551257389615890312
	invSqrtPi     = 0.564189583547756286948079451561
	invSqrt2Pi    = 0.398942280401432677939946059934
)

// Where Erf and Erfc change method: erf is its series below erfSplit and 1
// less erfc's continued fraction above it; erfc is 1 less erf's series below
// erfcSplit and its continued fraction above it. Each keeps the function's
// relative precision: erf is near 1 by erfSplit, and erfc is still near 1/2
// at erfcSplit, so neither subtraction loses more than a bit.
const (
	erfSplit  = 1
	erfcSplit = 0.5
)

// expMinusSquare returns exp(-x²) with x² split so that its larger part is
// exact: x = h + l with h carrying 26 significand bits, h² exact, and
// x² - h² = l·(x + h).
func expMinusSquare(x float64) float64 {
	h := math.Float64frombits(math.Float64bits(x) &^ (1<<27 - 1))
	l := x - h
	return float64(Exp(-float64(h*h)) * Exp(-float64(l*(x+h))))
}

// erfSeries is erf(x) for 0 <= x < erfSplit by the series of positive terms
// erf(x) = 2/sqrt(π)·exp(-x²)·Σ 2^n·x^(2n+1)/(1·3·…·(2n+1)).
func erfSeries(x float64) float64 {
	x2 := float64(2 * float64(x*x))
	term := x
	sum := x
	for n := 1; n < 100; n++ {
		term = float64(term*x2) / float64(2*n+1)
		sum += term
		if term <= float64(sum*0x1p-60) {
			break
		}
	}
	return float64(float64(twoOverSqrtPi*expMinusSquare(x)) * sum)
}

// erfcFraction is erfc(x) for x >= erfcSplit by Laplace's continued fraction
// erfc(x) = exp(-x²)/sqrt(π) / (x + (1/2)/(x + (2/2)/(x + (3/2)/(x + …)))),
// evaluated from a fixed depth backward, which damps the rounding of every
// level. The depth covers the convergence of the fraction at x with a
// margin: about 200/x² levels reach binary64 precision.
func erfcFraction(x float64) float64 {
	depth := 40 + int(math.Ceil(300/float64(x*x)))
	t := x
	for k := depth; k >= 1; k-- {
		t = x + float64(k)/2/t
	}
	return float64(expMinusSquare(x)*invSqrtPi) / t
}

// Erf returns the error function of x.
func Erf(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return x
	case x < 0:
		return -Erf(-x)
	case x < erfSplit:
		return erfSeries(x)
	case x > 6:
		return 1
	}
	return 1 - erfcFraction(x)
}

// Erfc returns the complementary error function of x, 1 - erf(x).
func Erfc(x float64) float64 {
	switch {
	case math.IsNaN(x):
		return x
	case x < 0:
		return 2 - Erfc(-x)
	case x < erfcSplit:
		return 1 - erfSeries(x)
	case x > 27:
		return 0
	}
	return erfcFraction(x)
}

// newtonSteps is how many Newton steps Erfinv takes from its starting
// value. Each step doubles the correct digits, so four would do from a
// start good to 2e-3; the fixed count makes the work, and the bits, the
// same for every argument.
const newtonSteps = 6

// Erfinv returns the inverse error function of y, for y in (-1, 1); ±1
// give ±infinity and anything else NaN. It starts from Winitzki's closed
// form and takes Newton steps on erf(x) = y, or, for |y| above 1/2, on
// erfc(x) = 1 - |y|, which the subtraction gives exactly and which keeps
// the precision the tail needs.
func Erfinv(y float64) float64 {
	switch {
	case math.IsNaN(y) || y < -1 || y > 1:
		return math.NaN()
	case y == 1:
		return math.Inf(1)
	case y == -1:
		return math.Inf(-1)
	case y == 0:
		return 0
	case y < 0:
		return -Erfinv(-y)
	}
	const a = 0.147
	ln := Log(float64((1 - y) * (1 + y)))
	t := 2/float64(math.Pi*a) + ln/2
	x := math.Sqrt(math.Sqrt(float64(t*t)-ln/a) - t)
	if y <= 0.5 {
		for range newtonSteps {
			x -= (Erf(x) - y) / float64(twoOverSqrtPi*expMinusSquare(x))
		}
		return x
	}
	c := 1 - y
	for range newtonSteps {
		x += (Erfc(x) - c) / float64(twoOverSqrtPi*expMinusSquare(x))
	}
	return x
}

// NormalPDF returns the standard normal density at u,
// exp(-u²/2)/sqrt(2π).
func NormalPDF(u float64) float64 {
	return float64(Exp(-float64(0.5*float64(u*u))) * invSqrt2Pi)
}
