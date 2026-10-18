// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package analysis is the statistics engine of specs/008-analysis.md: it
// turns the values of finished trials into per-cell and suite-level
// summaries with their intervals, compares a candidate's cells with a
// reference's by the test each metric's type and aggregate names, corrects
// a comparison's p-values as one family beside its primary result, gives
// every result a verdict and a plain reason, computes the confounds of the
// provenance tuple, the layers and the trials' times, and ranks a
// benchmark's entries by simultaneous rank intervals. It reads plain
// values and returns plain records: it knows no store, no request and no
// clock, and the same inputs and parameters give the same bits on every
// machine and architecture.
//
// Every elementary function it computes with is internal/analysis/mathx's,
// and every product that is summed is rounded first, an explicit float64
// conversion, so no fused multiply-add changes a figure between amd64 and
// arm64.
package analysis

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"latere.ai/x/examen/internal/analysis/mathx"
	"latere.ai/x/examen/internal/limits"
)

// Version is the analysis_version every comparison records.
const Version = "examen.analysis/v1"

// Parameters are the values a computation reads of the constants of spec
// 002 that spec 008 holds. A comparison records them, and a recomputation
// reads the recorded ones, so a later release with changed defaults gives
// the results a comparison gave when it was made. No computation of this
// package reads a constant of internal/limits for these.
type Parameters struct {
	FalseDiscoveryRate         float64  `json:"false_discovery_rate"`
	ConfidenceLevel            float64  `json:"confidence_level"`
	MinTrialsForVerdict        int      `json:"min_trials_for_verdict"`
	MinTrialsPerTaskInSuite    int      `json:"min_trials_per_task_in_suite"`
	MinSuiteCoverage           float64  `json:"min_suite_coverage"`
	MinTrialsForP90Interval    int      `json:"min_trials_for_p90_interval"`
	BootstrapResamples         int      `json:"bootstrap_resamples"`
	PermutationExactMax        int      `json:"permutation_exact_max"`
	PermutationStopExceedances int      `json:"permutation_stop_exceedances"`
	BoschlooGridPoints         int      `json:"boschloo_grid_points"`
	TieTolerance               float64  `json:"tie_tolerance"`
	WindowTolerance            Duration `json:"window_tolerance"`
}

// Defaults are the parameters of spec 002's table.
func Defaults() Parameters {
	return Parameters{
		FalseDiscoveryRate:         limits.FalseDiscoveryRate,
		ConfidenceLevel:            limits.ConfidenceLevel,
		MinTrialsForVerdict:        limits.MinTrialsForVerdict,
		MinTrialsPerTaskInSuite:    limits.MinTrialsPerTaskInSuite,
		MinSuiteCoverage:           limits.MinSuiteCoverage,
		MinTrialsForP90Interval:    limits.MinTrialsForP90Interval,
		BootstrapResamples:         limits.BootstrapResamples,
		PermutationExactMax:        limits.PermutationExactMax,
		PermutationStopExceedances: limits.PermutationStopExceedances,
		BoschlooGridPoints:         limits.BoschlooGridPoints,
		TieTolerance:               limits.TieTolerance,
		WindowTolerance:            Duration(limits.WindowTolerance),
	}
}

// ErrParameters is the error of a parameter outside its domain.
var ErrParameters = errors.New("analysis: a parameter is outside its domain")

// Validate reports the first parameter outside its domain.
func (p Parameters) Validate() error {
	bad := func(name string, v any) error { return fmt.Errorf("%w: %s is %v", ErrParameters, name, v) }
	switch {
	case !(p.FalseDiscoveryRate > 0 && p.FalseDiscoveryRate < 1):
		return bad("false_discovery_rate", p.FalseDiscoveryRate)
	case !(p.ConfidenceLevel > 0 && p.ConfidenceLevel < 1):
		return bad("confidence_level", p.ConfidenceLevel)
	case p.MinTrialsForVerdict < 2:
		return bad("min_trials_for_verdict", p.MinTrialsForVerdict)
	case p.MinTrialsPerTaskInSuite < 2:
		return bad("min_trials_per_task_in_suite", p.MinTrialsPerTaskInSuite)
	case !(p.MinSuiteCoverage > 0 && p.MinSuiteCoverage <= 1):
		return bad("min_suite_coverage", p.MinSuiteCoverage)
	case p.MinTrialsForP90Interval < 2:
		return bad("min_trials_for_p90_interval", p.MinTrialsForP90Interval)
	case p.BootstrapResamples < 1:
		return bad("bootstrap_resamples", p.BootstrapResamples)
	case p.PermutationExactMax < 1:
		return bad("permutation_exact_max", p.PermutationExactMax)
	case p.PermutationStopExceedances < 1:
		return bad("permutation_stop_exceedances", p.PermutationStopExceedances)
	case p.BoschlooGridPoints < 1:
		return bad("boschloo_grid_points", p.BoschlooGridPoints)
	case !(p.TieTolerance >= 0 && p.TieTolerance < 1):
		return bad("tie_tolerance", p.TieTolerance)
	case p.WindowTolerance < 0:
		return bad("window_tolerance", p.WindowTolerance)
	}
	return nil
}

// alpha is 1 - CONFIDENCE_LEVEL.
func (p Parameters) alpha() float64 { return 1 - p.ConfidenceLevel }

// z is sqrt(2)·erfinv(CONFIDENCE_LEVEL), the normal quantile every score
// interval and Hall and Sheather's bandwidth read: 1.959963984540054 at
// 0.95.
func (p Parameters) z() float64 {
	return float64(math.Sqrt(2) * mathx.Erfinv(p.ConfidenceLevel))
}

// resamples is PERMUTATION_RESAMPLES for a family of m p-values held at
// the level alpha.
func resamples(m int, alpha float64) int { return limits.PermutationResamples(m, alpha) }

// Duration is a length of time written in JSON as Go writes a duration,
// such as "168h0m0s".
type Duration time.Duration

// MarshalJSON writes the duration as a string.
func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

// UnmarshalJSON reads a duration written as a string.
func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("analysis: a duration is a string: %w", err)
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("analysis: reading the duration %q: %w", s, err)
	}
	*d = Duration(v)
	return nil
}
