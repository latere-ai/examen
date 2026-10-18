// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"math"
	"slices"
	"strconv"
	"strings"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

// The types of a metric.
const (
	TypeBinary   = "binary"
	TypeScore    = "score"
	TypeCount    = "count"
	TypeDuration = "duration"
	TypeCost     = "cost"
	TypeLabel    = "label"
)

// The directions of a metric.
const (
	HigherIsBetter = "higher_is_better"
	LowerIsBetter  = "lower_is_better"
	DirectionNone  = "none"
)

// The levels of a metric and a grader.
const (
	LevelTrial     = "trial"
	LevelCell      = "cell"
	LevelCandidate = "candidate"
)

// The vocabularies of a label metric.
const (
	VocabularyClosed = "closed"
	VocabularyOpen   = "open"
)

// The aggregates of a metric.
const (
	AggregateRate          = "rate"
	AggregateMean          = "mean"
	AggregateMedian        = "median"
	AggregateP90           = "p90"
	AggregateDistribution  = "distribution"
	AggregateRepeatRate    = "repeat_rate"
	AggregateModeShare     = "mode_share"
	AggregateDistinctCount = "distinct_count"
)

// Metric is a named, typed quantity a suite is measured on.
type Metric struct {
	Name          string    `json:"name"`
	Type          string    `json:"type"`
	Range         []float64 `json:"range,omitempty"`
	Vocabulary    string    `json:"vocabulary,omitempty"`
	Labels        []string  `json:"labels,omitempty"`
	Direction     string    `json:"direction"`
	Level         string    `json:"level,omitempty"`
	AppliesTo     []string  `json:"applies_to,omitempty"`
	Of            string    `json:"of,omitempty"`
	ExcludeLabels *[]string `json:"exclude_labels,omitempty"`
	Aggregate     string    `json:"aggregate,omitempty"`
	Unit          string    `json:"unit,omitempty"`
	Description   string    `json:"description,omitempty"`
}

// LevelOf is the metric's level, trial when it names none.
func (m Metric) LevelOf() string {
	if m.Level == "" {
		return LevelTrial
	}
	return m.Level
}

// Open reports whether the metric is a label metric of open vocabulary.
func (m Metric) Open() bool { return m.Type == TypeLabel && m.Vocabulary == VocabularyOpen }

// Applies reports whether a trial-level metric applies to a finished trial
// of the outcome.
func (m Metric) Applies(outcome string) bool {
	return len(m.AppliesTo) == 0 || slices.Contains(m.AppliesTo, outcome)
}

// Normalize fills nothing and changes nothing but a reference to the
// metric a cell-level metric summarizes, written name@digest, which is
// kept as its digest so two spellings of one content are one version.
func (m *Metric) Normalize() error {
	if m.Of == "" || IsBuiltinMetric(m.Of) {
		return nil
	}
	_, d, err := ParseRef("of", m.Of)
	if err != nil {
		return err
	}
	m.Of = d
	return nil
}

// Validate holds a metric to the rules of specs/002-objects.md that need
// no other object.
func (m Metric) Validate() error {
	switch {
	case !ValidName(KindMetric, m.Name):
		return invalid("name", "a metric's name is [a-z][a-z0-9_]{0,62}")
	case !slices.Contains([]string{TypeBinary, TypeScore, TypeCount, TypeDuration, TypeCost, TypeLabel}, m.Type):
		return invalid("type", "type is binary, score, count, duration, cost or label")
	case !slices.Contains([]string{HigherIsBetter, LowerIsBetter, DirectionNone}, m.Direction):
		return invalid("direction", "direction is higher_is_better, lower_is_better or none")
	case m.Type == TypeLabel && m.Direction != DirectionNone:
		return invalid("direction", "a label metric's direction is none")
	}
	if err := m.validateRange(); err != nil {
		return err
	}
	if err := m.validateLabels(); err != nil {
		return err
	}
	if err := m.validateLevel(); err != nil {
		return err
	}
	return m.validateAggregate()
}

func (m Metric) validateRange() error {
	if m.Type != TypeScore {
		if m.Range != nil {
			return invalid("range", "range is for a score metric alone")
		}
		return nil
	}
	if len(m.Range) != 2 {
		return missing("range", "a score metric declares its range as [min, max]")
	}
	lo, hi := m.Range[0], m.Range[1]
	if math.IsNaN(lo) || math.IsNaN(hi) || math.IsInf(lo, 0) || math.IsInf(hi, 0) || lo >= hi {
		return invalid("range", "range is two finite numbers, the first below the second")
	}
	return nil
}

func (m Metric) validateLabels() error {
	if m.Type != TypeLabel {
		if m.Vocabulary != "" || m.Labels != nil {
			return invalid("labels", "vocabulary and labels are for a label metric alone")
		}
		return nil
	}
	switch m.Vocabulary {
	case "", VocabularyClosed:
		if len(m.Labels) == 0 {
			return missing("labels", "a label metric of closed vocabulary declares its labels")
		}
	case VocabularyOpen:
		if m.Labels != nil {
			return invalid("labels", "a label metric of open vocabulary declares no labels")
		}
		return nil
	default:
		return invalid("vocabulary", "vocabulary is closed or open")
	}
	if len(m.Labels) > limits.MaxLabelsPerMetric {
		return apierr.Exceeds("MAX_LABELS_PER_METRIC", "labels", "a metric declares more than MAX_LABELS_PER_METRIC (64) labels")
	}
	seen := map[string]bool{}
	for i, l := range m.Labels {
		f := "labels[" + itoa(i) + "]"
		switch {
		case l == "":
			return invalid(f, "a label is not empty")
		case len(l) > limits.LabelMaxBytes:
			return apierr.Exceeds("LABEL_MAX_BYTES", f, "a label is longer than LABEL_MAX_BYTES (128)")
		case seen[l]:
			return invalid(f, "a label is declared twice")
		}
		seen[l] = true
	}
	return nil
}

func (m Metric) validateLevel() error {
	level := m.LevelOf()
	if !slices.Contains([]string{LevelTrial, LevelCell, LevelCandidate}, level) {
		return invalid("level", "level is trial, cell or candidate")
	}
	if len(m.AppliesTo) > 0 && level != LevelTrial {
		return invalid("applies_to", "applies_to is for a trial-level metric alone")
	}
	for i, o := range m.AppliesTo {
		if !slices.Contains(ResultOutcomes(), o) {
			return invalid("applies_to["+itoa(i)+"]", "applies_to names completed, declined, timed_out or turn_limit")
		}
	}
	if m.ExcludeLabels != nil && level != LevelCell {
		return invalid("exclude_labels", "exclude_labels is for a cell-level metric alone")
	}
	if m.ExcludeLabels != nil {
		for i, l := range *m.ExcludeLabels {
			if l == "" || len(l) > limits.LabelMaxBytes {
				return invalid("exclude_labels["+itoa(i)+"]", "a label is 1 to LABEL_MAX_BYTES (128) bytes")
			}
		}
	}
	switch {
	case level == LevelCell && m.Of == "":
		return missing("of", "a cell-level metric names the trial-level label metric it summarizes")
	case level != LevelCell && m.Of != "":
		return invalid("of", "of is for a cell-level metric alone")
	case level == LevelCandidate && m.Open():
		return invalid("vocabulary", "a candidate-level metric is not a label of open vocabulary")
	}
	return nil
}

func (m Metric) validateAggregate() error {
	level := m.LevelOf()
	if level == LevelCandidate {
		if m.Aggregate != "" {
			return invalid("aggregate", "a candidate-level metric has no aggregate: its one value is its summary")
		}
		return nil
	}
	allowed := map[string][]string{
		TypeBinary: {AggregateRate}, TypeScore: {AggregateMean, AggregateMedian, AggregateP90},
		TypeCount: {AggregateMean, AggregateMedian, AggregateP90}, TypeDuration: {AggregateMean, AggregateMedian, AggregateP90},
		TypeCost: {AggregateMean, AggregateMedian, AggregateP90}, TypeLabel: {AggregateDistribution},
	}[m.Type]
	if level == LevelCell {
		switch m.Aggregate {
		case AggregateRepeatRate, AggregateModeShare:
			if m.Type != TypeScore || len(m.Range) != 2 || m.Range[0] != 0 || m.Range[1] != 1 {
				return invalid("type", "a repeat_rate or mode_share is of type score with the range [0, 1]")
			}
			return nil
		case AggregateDistinctCount:
			if m.Type != TypeCount {
				return invalid("type", "a distinct_count is of type count")
			}
			return nil
		}
		return invalid("aggregate", "a cell-level metric's aggregate is repeat_rate, mode_share or distinct_count")
	}
	if m.Aggregate == "" {
		return missing("aggregate", "a trial-level metric names how a cell summarizes its trials")
	}
	if !slices.Contains(allowed, m.Aggregate) {
		return invalid("aggregate", "the aggregate "+quote(m.Aggregate)+" does not summarize a "+m.Type+" metric")
	}
	return nil
}

// BuiltinPrefix starts the reserved digest of a built-in metric.
const BuiltinPrefix = "builtin:"

// The built-in metrics (specs/002-objects.md).
const (
	MetricCompleted       = "completed"
	MetricDeclined        = "declined"
	MetricCostUSD         = "cost_usd"
	MetricDuration        = "duration"
	MetricInputTokens     = "input_tokens"
	MetricOutputTokens    = "output_tokens"
	MetricReasoningTokens = "reasoning_tokens"
	MetricTurns           = "turns"
	MetricToolCalls       = "tool_calls"
)

// builtins are the built-in metrics, in the order of the spec's table.
var builtins = []Metric{
	{Name: MetricCompleted, Type: TypeBinary, Aggregate: AggregateRate, Direction: DirectionNone, Level: LevelTrial},
	{Name: MetricDeclined, Type: TypeBinary, Aggregate: AggregateRate, Direction: LowerIsBetter, Level: LevelTrial},
	{Name: MetricCostUSD, Type: TypeCost, Aggregate: AggregateMean, Direction: LowerIsBetter, Level: LevelTrial, Unit: "USD"},
	{Name: MetricDuration, Type: TypeDuration, Aggregate: AggregateMedian, Direction: LowerIsBetter, Level: LevelTrial, Unit: "s"},
	{Name: MetricInputTokens, Type: TypeCount, Aggregate: AggregateMean, Direction: DirectionNone, Level: LevelTrial},
	{Name: MetricOutputTokens, Type: TypeCount, Aggregate: AggregateMean, Direction: DirectionNone, Level: LevelTrial},
	{Name: MetricReasoningTokens, Type: TypeCount, Aggregate: AggregateMean, Direction: DirectionNone, Level: LevelTrial},
	{Name: MetricTurns, Type: TypeCount, Aggregate: AggregateMedian, Direction: DirectionNone, Level: LevelTrial},
	{Name: MetricToolCalls, Type: TypeCount, Aggregate: AggregateMedian, Direction: DirectionNone, Level: LevelTrial},
}

// Builtins lists the built-in metrics, each a fresh copy.
func Builtins() []Metric { return slices.Clone(builtins) }

// Builtin is the built-in metric a reserved digest names, and false for a
// digest that names none.
func Builtin(digest string) (Metric, bool) {
	name, ok := strings.CutPrefix(digest, BuiltinPrefix)
	if !ok {
		return Metric{}, false
	}
	for _, m := range builtins {
		if m.Name == name {
			return m, true
		}
	}
	return Metric{}, false
}

// IsBuiltinMetric reports whether a digest is a built-in metric's.
func IsBuiltinMetric(digest string) bool {
	_, ok := Builtin(digest)
	return ok
}

// BuiltinDigest is the reserved digest of a built-in metric.
func BuiltinDigest(name string) string { return BuiltinPrefix + name }

func itoa(i int) string { return strconv.Itoa(i) }
