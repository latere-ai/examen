// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package calibrate measures how well a judge agrees with people
// (specs/006-grading.md, Calibration): over the trials that hold a judge
// version's current value of a metric and at least one person's, the
// people's value of each trial, Krippendorff's alpha between the judge and
// the people, and whether the judge is calibrated for the metric.
package calibrate

import (
	"cmp"
	"fmt"
	"maps"
	"slices"

	"latere.ai/x/examen/internal/limits"
)

// Pair is one trial's values: the judge's current value and each person's
// current value, a bool, a float64 or a string by the metric's type.
type Pair struct {
	Judge  any
	People []any
}

// Calibration is a judge's agreement with people on one metric: the pairs
// read, Krippendorff's alpha over them, and whether it is calibrated.
type Calibration struct {
	Pairs      int
	Alpha      float64
	Calibrated bool
}

// Of is a judge's calibration on a metric over its pairs: each trial's
// people's value by PeoplesValue, a trial whose people tie dropped, and
// the judge calibrated when at least CALIBRATION_MIN_PAIRS trials remain
// and alpha is at least CALIBRATION_MIN_AGREEMENT. Nominal metrics, binary
// and label, take the nominal distance, and the others the interval one.
// A sample in which alpha is undefined, every value one value, says
// nothing of agreement: its alpha reads 0 and it is not calibrated.
func Of(pairs []Pair, nominal bool) (Calibration, error) {
	var units [][2]any
	for _, p := range pairs {
		people, ok, err := PeoplesValue(p.People, nominal)
		if err != nil {
			return Calibration{}, err
		}
		if ok {
			units = append(units, [2]any{p.Judge, people})
		}
	}
	out := Calibration{Pairs: len(units)}
	alpha, defined, err := Alpha(units, nominal)
	if err != nil {
		return Calibration{}, err
	}
	if defined {
		out.Alpha = alpha
	}
	out.Calibrated = defined && out.Pairs >= limits.CalibrationMinPairs && alpha >= limits.CalibrationMinAgreement
	return out, nil
}

// PeoplesValue is the people's value of one trial: its one person's, or
// with several their consensus, the majority for a nominal metric, a tie
// dropping the trial, and the median for the others. ok is false for a
// trial it drops or one no person answered.
func PeoplesValue(values []any, nominal bool) (any, bool, error) {
	switch {
	case len(values) == 0:
		return nil, false, nil
	case len(values) == 1:
		return values[0], true, nil
	}
	if !nominal {
		nums := make([]float64, len(values))
		for i, v := range values {
			f, ok := v.(float64)
			if !ok {
				return nil, false, fmt.Errorf("calibrate: a value of type %T on an interval metric", v)
			}
			nums[i] = f
		}
		slices.Sort(nums)
		mid := len(nums) / 2
		if len(nums)%2 == 1 {
			return nums[mid], true, nil
		}
		return (nums[mid-1] + nums[mid]) / 2, true, nil
	}
	counts := map[string]int{}
	first := map[string]any{}
	for _, v := range values {
		k := fmt.Sprint(v)
		counts[k]++
		if _, seen := first[k]; !seen {
			first[k] = v
		}
	}
	best, top, tie := "", 0, false
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		switch c := counts[k]; {
		case c > top:
			best, top, tie = k, c, false
		case c == top:
			tie = true
		}
	}
	if tie {
		return nil, false, nil
	}
	return first[best], true, nil
}

// Alpha is Krippendorff's alpha over units of two values each, a judge's
// and the people's: 1 - D_o/D_e with the coincidence of every pairable
// value, the nominal distance (0 for equal values, 1 otherwise) or the
// interval one ((c - k)^2). With two values a unit, the observed
// disagreement is
//
//	D_o = (1/n) Σ_u 2 δ(a_u, b_u)
//
// and the expected one, over all n = 2N values,
//
//	D_e = (1/(n (n - 1))) Σ_{i ≠ j} δ(v_i, v_j)
//
// defined is false when D_e is 0, every value equal, or there is no unit.
// Sums run over the values in a fixed order, the units' in turn and then
// the values sorted, so one sample gives one alpha.
func Alpha(units [][2]any, nominal bool) (float64, bool, error) {
	n := 2 * len(units)
	if n < 2 {
		return 0, false, nil
	}
	delta := func(a, b any) (float64, error) {
		if nominal {
			if fmt.Sprint(a) == fmt.Sprint(b) {
				return 0, nil
			}
			return 1, nil
		}
		x, okx := a.(float64)
		y, oky := b.(float64)
		if !okx || !oky {
			return 0, fmt.Errorf("calibrate: values of types %T and %T on an interval metric", a, b)
		}
		d := x - y
		return float64(d * d), nil
	}
	observed := 0.0
	values := make([]any, 0, n)
	for _, u := range units {
		d, err := delta(u[0], u[1])
		if err != nil {
			return 0, false, err
		}
		observed += 2 * d
		values = append(values, u[0], u[1])
	}
	slices.SortFunc(values, func(a, b any) int { return cmp.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
	expected := 0.0
	for i := range values {
		for j := range values {
			if i == j {
				continue
			}
			d, err := delta(values[i], values[j])
			if err != nil {
				return 0, false, err
			}
			expected += d
		}
	}
	if expected == 0 {
		return 0, false, nil
	}
	do := observed / float64(n)
	de := expected / float64(n*(n-1))
	return 1 - do/de, true, nil
}
