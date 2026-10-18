// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package mathx

import (
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// ulps is how many units in the last place of want got lies from it.
func ulps(got, want float64) float64 {
	if got == want {
		return 0
	}
	u := math.Nextafter(want, math.Inf(1)) - want
	return math.Abs(got-want) / u
}

// Each function agrees with package math's within a few units in the last
// place over its domain. Package math is within one unit for these, so the
// bound is this package's error and package math's together: its exp on
// amd64 is assembly, which lies a unit from its own pure-Go exp.
func TestAgreesWithTheStandardLibrary(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	lgamma := func(x float64) float64 { v, _ := math.Lgamma(x); return v }
	for _, c := range []struct {
		name  string
		f, g  func(float64) float64
		gen   func() float64
		bound float64
	}{
		{"Exp", Exp, math.Exp, func() float64 { return r.Float64()*1450 - 745 }, 2},
		{"Exp near 0", Exp, math.Exp, func() float64 { return r.Float64()*2 - 1 }, 2},
		{"Log", Log, math.Log, func() float64 { return math.Exp(r.Float64()*1400 - 700) }, 1},
		{"Log near 1", Log, math.Log, func() float64 { return 0.5 + r.Float64() }, 1},
		{"Lgamma", Lgamma, lgamma, func() float64 { return 10 + r.Float64()*3000 }, 8},
		{"Erf", Erf, math.Erf, func() float64 { return r.Float64()*8 - 4 }, 8},
		{"Erfc", Erfc, math.Erfc, func() float64 { return r.Float64()*12 - 2 }, 10},
		{"Erfinv", Erfinv, math.Erfinv, func() float64 { return r.Float64()*1.998 - 0.999 }, 10},
	} {
		worst, at := 0.0, 0.0
		for range 50000 {
			x := c.gen()
			if d := ulps(c.f(x), c.g(x)); d > worst {
				worst, at = d, x
			}
		}
		if worst > c.bound {
			t.Errorf("%s is %v ulps from package math at %v; the bound is %v", c.name, worst, at, c.bound)
		}
	}
}

// Near its zeros at 1 and 2 log Γ is small and its error is read as
// absolute: within 1e-14 of package math's, or that relative to larger
// values, over the small arguments, the integers and the half-integers the
// binomial coefficients read.
func TestLgammaNearItsZeros(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	xs := []float64{1e-300, 1e-10, 0.5, 1.5, 2.5}
	for i := 1; i <= 2000; i++ {
		xs = append(xs, float64(i), float64(i)+0.5)
	}
	for range 20000 {
		xs = append(xs, r.Float64()*20)
	}
	for _, x := range xs {
		want, _ := math.Lgamma(x)
		if d := math.Abs(Lgamma(x)-want) / math.Max(1, math.Abs(want)); d > 1e-14 {
			t.Errorf("Lgamma(%v) = %v, package math %v", x, Lgamma(x), want)
		}
	}
}

func TestSpecialValues(t *testing.T) {
	inf, nan := math.Inf(1), math.NaN()
	for _, c := range []struct {
		name      string
		got, want float64
	}{
		{"Exp(0)", Exp(0), 1},
		{"Exp(+Inf)", Exp(inf), inf},
		{"Exp(-Inf)", Exp(-inf), 0},
		{"Exp(710)", Exp(710), inf},
		{"Exp(-746)", Exp(-746), 0},
		{"Exp(1)", Exp(1), math.E},
		{"Log(1)", Log(1), 0},
		{"Log(0)", Log(0), -inf},
		{"Log(+Inf)", Log(inf), inf},
		{"Log(e)", Log(math.E), 1},
		{"Log(2)", Log(2), math.Ln2},
		{"Lgamma(1)", Lgamma(1), 0},
		{"Lgamma(2)", Lgamma(2), 0},
		{"Lgamma(+Inf)", Lgamma(inf), inf},
		{"Lgamma(3)", Lgamma(3), math.Ln2},
		{"LogChoose(5, 0)", LogChoose(5, 0), 0},
		{"LogChoose(5, 5)", LogChoose(5, 5), 0},
		{"LogChoose(5, 6)", LogChoose(5, 6), -inf},
		{"LogChoose(5, -1)", LogChoose(5, -1), -inf},
		{"Pow(2, 10)", Pow(2, 10), 1024},
		{"Pow(0, 2)", Pow(0, 2), 0},
		{"Pow(0, -1)", Pow(0, -1), inf},
		{"Pow(1, 1e300)", Pow(1, 1e300), 1},
		{"Pow(7, 0)", Pow(7, 0), 1},
		{"Erf(0)", Erf(0), 0},
		{"Erf(7)", Erf(7), 1},
		{"Erf(-7)", Erf(-7), -1},
		{"Erfc(28)", Erfc(28), 0},
		{"Erfc(-28)", Erfc(-28), 2},
		{"Erfinv(0)", Erfinv(0), 0},
		{"Erfinv(1)", Erfinv(1), inf},
		{"Erfinv(-1)", Erfinv(-1), -inf},
		{"NormalPDF(0)", NormalPDF(0), 1 / math.Sqrt(2*math.Pi)},
	} {
		if ulps(c.got, c.want) > 1 {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	for name, v := range map[string]float64{
		"Exp(NaN)": Exp(nan), "Log(-1)": Log(-1), "Log(NaN)": Log(nan),
		"Lgamma(0)": Lgamma(0), "Lgamma(-1)": Lgamma(-1), "Lgamma(NaN)": Lgamma(nan),
		"Pow(-1, 2)": Pow(-1, 2), "Pow(NaN, 2)": Pow(nan, 2), "Erf(NaN)": Erf(nan),
		"Erfc(NaN)": Erfc(nan), "Erfinv(2)": Erfinv(2), "Erfinv(NaN)": Erfinv(nan),
	} {
		if !math.IsNaN(v) {
			t.Errorf("%s = %v, want NaN", name, v)
		}
	}
	if got := LogChoose(10, 5); ulps(got, math.Log(252)) > 4 {
		t.Errorf("LogChoose(10, 5) = %v, want log 252", got)
	}
	if LogChoose(30, 7) != LogChoose(30, 23) {
		t.Error("LogChoose is not symmetric")
	}
	if Exp(-740) <= 0 || Exp(-740) >= 1e-300 {
		t.Errorf("Exp(-740) = %v, want a subnormal", Exp(-740))
	}
}

// The figures the analysis reads at its default level, held to their bits
// so that a run on another architecture proves the same arithmetic:
// z = sqrt(2)·erfinv(0.95) prints as 1.959963984540054, spec 008's value.
func TestGoldenBits(t *testing.T) {
	z := math.Sqrt2 * Erfinv(0.95)
	if s := strconv.FormatFloat(z, 'g', 16, 64); s != "1.959963984540054" {
		t.Errorf("z = %s, want 1.959963984540054", s)
	}
	for _, c := range []struct {
		name string
		got  float64
		bits uint64
	}{
		{"z", z, 0x3fff5c0331eeff84},
		{"Exp(0.5)", Exp(0.5), 0x3ffa61298e1e069c},
		{"Log(10)", Log(10), 0x40026bb1bbb55516},
		{"Lgamma(10.5)", Lgamma(10.5), 0x402be199a0f64393},
		{"LogChoose(20, 10)", LogChoose(20, 10), 0x402840eaca8b44cf},
		{"Erfinv(0.8)", Erfinv(0.8), 0x3fecff8a2529bab4},
		{"Pow(5, -1/3)", Pow(5, -1.0/3), 0x3fe2b6b5edf6b54a},
		{"NormalPDF(1.2815515655446004)", NormalPDF(1.2815515655446004), 0x3fc676bab61387db},
	} {
		if math.Float64bits(c.got) != c.bits {
			t.Errorf("%s = %v (%#x), want the bits %#x", c.name, c.got, math.Float64bits(c.got), c.bits)
		}
	}
}

// The package holds no assembly: every function is the Go above on every
// architecture.
func TestNoAssembly(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if ext := filepath.Ext(e.Name()); ext == ".s" || ext == ".S" || strings.HasSuffix(e.Name(), "_amd64.go") || strings.HasSuffix(e.Name(), "_arm64.go") {
			t.Errorf("%s: the package must hold no assembly and no architecture-specific file", e.Name())
		}
	}
}
