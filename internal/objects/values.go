// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"latere.ai/x/examen/internal/limits"
)

// NormalizeLabel is a raw label as an open-vocabulary metric keeps it: in
// Unicode NFC, trimmed, each run of white space one space, folded to lower
// case (specs/006-grading.md).
func NormalizeLabel(s string) string {
	s = norm.NFC.String(s)
	s = strings.Join(strings.FieldsFunc(s, unicode.IsSpace), " ")
	return strings.ToLower(s)
}

// Value checks a value against its metric's type, range and labels, and
// answers it as it is stored: a bool, a float64 or a string, a raw label
// of an open vocabulary normalized. The detail of a refusal says why for a
// developer.
func Value(m Metric, v any) (any, string) {
	if n, ok := v.(json.Number); ok {
		f, err := n.Float64()
		if err != nil {
			return nil, "the value is not a number this service reads"
		}
		v = f
	}
	switch m.Type {
	case TypeBinary:
		b, ok := v.(bool)
		if !ok {
			return nil, "a binary metric takes true or false"
		}
		return b, ""
	case TypeLabel:
		s, ok := v.(string)
		if !ok {
			return nil, "a label metric takes a string"
		}
		if m.Open() {
			s = NormalizeLabel(s)
			if s == "" || len(s) > limits.LabelMaxBytes {
				return nil, "an open-vocabulary label is 1 to LABEL_MAX_BYTES (128) bytes once normalized"
			}
			return s, ""
		}
		if !slices.Contains(m.Labels, s) {
			return nil, "the label is not one the metric declares"
		}
		return s, ""
	}
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, "a " + m.Type + " metric takes a finite number"
	}
	switch m.Type {
	case TypeScore:
		if f < m.Range[0] || f > m.Range[1] {
			return nil, "the score is outside the metric's range"
		}
	case TypeCount:
		if f < 0 || f != math.Trunc(f) {
			return nil, "a count is a whole number at least 0"
		}
	default:
		if f < 0 {
			return nil, "a " + m.Type + " is at least 0"
		}
	}
	return f, ""
}

// Self is the grader of every value an external harness submitted itself.
func Self(harness string) string { return SelfPrefix + harness }

// Current is, for one trial, each metric's current value
// (specs/007-measurements-and-evidence.md): the measurement of the latest
// generation of the metric's producing grader, over every metric that
// grader produced; for a metric no grader produces and an external trial,
// the latest generation the harness submitted itself; missing otherwise.
// producers maps a metric digest to its producing grader, and harness is
// the trial's external harness, or empty.
func Current(ms []Measurement, producers map[string]string, metrics []string, harness string) map[string]Measurement {
	latest := map[string]int{}
	for _, m := range ms {
		latest[m.Grader] = max(latest[m.Grader], m.Generation)
	}
	out := map[string]Measurement{}
	for _, metric := range metrics {
		g, ok := producers[metric]
		if !ok {
			if harness == "" {
				continue
			}
			g = Self(harness)
		}
		for _, m := range ms {
			if m.Grader == g && m.Metric == metric && m.Generation == latest[g] && m.Annotator == "" {
				out[metric] = m
			}
		}
	}
	return out
}

// BuiltinValues are the measurements of grader builtin a finished trial gets
// when it reaches done: completed from its outcome, and duration from the
// times its executor or harness gave. Lux's records are read by no path of
// this release, so the cost, token, turn and tool-call metrics are not
// filled: an unknown is never written as zero.
func BuiltinValues(t Trial) map[string]any {
	out := map[string]any{
		BuiltinDigest(MetricCompleted): t.Outcome == OutcomeCompleted || t.Outcome == OutcomeDeclined,
	}
	if t.StartedAt != nil && t.AgentEndedAt != nil {
		out[BuiltinDigest(MetricDuration)] = t.AgentEndedAt.Sub(*t.StartedAt).Seconds()
	}
	return out
}

// DeclineValue is the built-in decline judge's value for a finished trial:
// true exactly when its outcome is declined.
func DeclineValue(t Trial) bool { return t.Outcome == OutcomeDeclined }
