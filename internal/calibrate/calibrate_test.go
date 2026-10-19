// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package calibrate

import (
	"math"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/limits"
)

// units reads two coders' values, one character each.
func units(a, b string) [][2]any {
	out := make([][2]any, len(a))
	for i := range a {
		out[i] = [2]any{string(a[i]), string(b[i])}
	}
	return out
}

// Krippendorff's alpha matches the reference values of his own examples:
// two observers on binary data, 0 1 0 0 0 0 0 0 1 0 against
// 1 1 1 0 0 1 0 0 0 0, alpha = 1 - 19·4/(14·6) = 2/21 = 0.095; two
// observers on nominal data, a a b b d c c c e d d a against
// b a b b b c c c e d d d, alpha = 1 - 23·6/448 = 155/224 = 0.692; and on
// interval data, the units (1, 1), (2, 2), (3, 3), (4, 5), alpha =
// 1 - (2/8)/(222/56) = 104/111. One value throughout leaves alpha
// undefined.
func TestAlpha(t *testing.T) {
	for _, c := range []struct {
		name    string
		units   [][2]any
		nominal bool
		want    float64
	}{
		{"binary", units("0100000010", "1110010000"), true, 2.0 / 21},
		{"nominal", units("aabbdcccedda", "babbbccceddd"), true, 155.0 / 224},
		{"interval", [][2]any{{1.0, 1.0}, {2.0, 2.0}, {3.0, 3.0}, {4.0, 5.0}}, false, 104.0 / 111},
		{"perfect", units("abab", "abab"), true, 1},
	} {
		got, defined, err := Alpha(c.units, c.nominal)
		if err != nil || !defined || math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s: alpha %v (defined %v, %v), want %v", c.name, got, defined, err, c.want)
		}
	}
	if _, defined, err := Alpha(units("aaa", "aaa"), true); defined || err != nil {
		t.Errorf("one value throughout: defined %v %v", defined, err)
	}
	if _, defined, err := Alpha(nil, true); defined || err != nil {
		t.Errorf("no unit: defined %v %v", defined, err)
	}
	if _, _, err := Alpha([][2]any{{1.0, "a"}}, false); err == nil {
		t.Error("a label on an interval metric")
	}
	if _, _, err := Alpha([][2]any{{1.0, 2.0}, {3.0, true}}, false); err == nil {
		t.Error("a bool among the values of an interval metric")
	}
}

// The people's value of a trial is its one person's, the majority of
// several on a nominal metric with a tie dropping the trial, and their
// median on an interval one.
func TestPeoplesValue(t *testing.T) {
	for _, c := range []struct {
		name    string
		values  []any
		nominal bool
		want    any
		ok      bool
	}{
		{"none", nil, true, nil, false},
		{"one", []any{"game"}, true, "game", true},
		{"a majority", []any{"tool", "game", "game"}, true, "game", true},
		{"a tie", []any{"tool", "game"}, true, nil, false},
		{"a majority of bools", []any{true, false, true}, true, true, true},
		{"an odd median", []any{3.0, 1.0, 2.0}, false, 2.0, true},
		{"an even median", []any{4.0, 1.0, 2.0, 3.0}, false, 2.5, true},
	} {
		got, ok, err := PeoplesValue(c.values, c.nominal)
		if err != nil || ok != c.ok || got != c.want {
			t.Errorf("%s: %v %v %v, want %v %v", c.name, got, ok, err, c.want, c.ok)
		}
	}
	if _, _, err := PeoplesValue([]any{1.0, "a"}, false); err == nil {
		t.Error("a label among an interval metric's values")
	}
}

// A judge is calibrated with at least CALIBRATION_MIN_PAIRS trials and an
// alpha of at least CALIBRATION_MIN_AGREEMENT; fewer pairs, a lower alpha
// or one value throughout leave it uncalibrated, and a trial whose people
// tie is no pair.
func TestCalibrated(t *testing.T) {
	agree := func(n int) []Pair {
		out := make([]Pair, n)
		for i := range out {
			l := string("ab"[i%2])
			out[i] = Pair{Judge: l, People: []any{l}}
		}
		return out
	}
	enough := agree(limits.CalibrationMinPairs)
	c, err := Of(enough, true)
	if err != nil || !c.Calibrated || c.Pairs != limits.CalibrationMinPairs || c.Alpha != 1 {
		t.Fatalf("enough agreeing pairs %+v %v", c, err)
	}
	if c, _ := Of(agree(limits.CalibrationMinPairs-1), true); c.Calibrated {
		t.Errorf("too few pairs %+v", c)
	}
	tied := append(agree(limits.CalibrationMinPairs-1), Pair{Judge: "a", People: []any{"a", "b"}})
	if c, _ := Of(tied, true); c.Calibrated || c.Pairs != limits.CalibrationMinPairs-1 {
		t.Errorf("a tied trial counted %+v", c)
	}
	// Half the pairs flipped: alpha far below the threshold.
	flipped := agree(2 * limits.CalibrationMinPairs)
	for i := range limits.CalibrationMinPairs {
		flipped[i].People = []any{strings.Map(func(r rune) rune { return 'a' + 'b' - r }, flipped[i].Judge.(string))}
	}
	if c, err := Of(flipped, true); err != nil || c.Calibrated || c.Alpha >= limits.CalibrationMinAgreement {
		t.Errorf("disagreeing pairs %+v %v", c, err)
	}
	same := make([]Pair, limits.CalibrationMinPairs)
	for i := range same {
		same[i] = Pair{Judge: "a", People: []any{"a"}}
	}
	if c, err := Of(same, true); err != nil || c.Calibrated || c.Alpha != 0 {
		t.Errorf("one value throughout %+v %v", c, err)
	}
	if _, err := Of([]Pair{{Judge: 1.0, People: []any{1.0, "x"}}}, false); err == nil {
		t.Error("a people's value that does not read")
	}
	if _, err := Of([]Pair{{Judge: "x", People: []any{1.0}}, {Judge: 1.0, People: []any{2.0}}}, false); err == nil {
		t.Error("a judge's value that does not read")
	}
}
