// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"encoding/json"
	"time"
)

// Type is a metric's type (spec 002).
type Type string

// The metric types.
const (
	TypeBinary   Type = "binary"
	TypeScore    Type = "score"
	TypeCount    Type = "count"
	TypeDuration Type = "duration"
	TypeCost     Type = "cost"
	TypeLabel    Type = "label"
)

// Aggregate is how a cell summarizes its trials (spec 002).
type Aggregate string

// The aggregates.
const (
	AggRate          Aggregate = "rate"
	AggMean          Aggregate = "mean"
	AggMedian        Aggregate = "median"
	AggP90           Aggregate = "p90"
	AggDistribution  Aggregate = "distribution"
	AggRepeatRate    Aggregate = "repeat_rate"
	AggModeShare     Aggregate = "mode_share"
	AggDistinctCount Aggregate = "distinct_count"
)

// Direction is which way a metric is better (spec 002).
type Direction string

// The directions.
const (
	HigherIsBetter Direction = "higher_is_better"
	LowerIsBetter  Direction = "lower_is_better"
	NoDirection    Direction = "none"
)

// Level is where a metric's values belong, and where a result reads.
type Level string

// The levels of a metric (trial, cell, candidate) and of a result (task,
// suite, candidate).
const (
	LevelTrial     Level = "trial"
	LevelCell      Level = "cell"
	LevelCandidate Level = "candidate"
	LevelTask      Level = "task"
	LevelSuite     Level = "suite"
)

// Vocabulary is a label metric's vocabulary.
type Vocabulary string

// The vocabularies.
const (
	Closed Vocabulary = "closed"
	Open   Vocabulary = "open"
)

// Outcome is a trial's outcome (spec 002).
type Outcome string

// The outcomes.
const (
	OutcomeCompleted   Outcome = "completed"
	OutcomeDeclined    Outcome = "declined"
	OutcomeTimedOut    Outcome = "timed_out"
	OutcomeTurnLimit   Outcome = "turn_limit"
	OutcomeOverBudget  Outcome = "over_budget"
	OutcomeInfraFailed Outcome = "infra_failed"
	OutcomeCanceled    Outcome = "canceled"
)

// excluded reports whether a trial of this outcome is excluded from
// analysis though done.
func (o Outcome) excluded() bool {
	return o == OutcomeOverBudget || o == OutcomeInfraFailed || o == OutcomeCanceled
}

// Metric is the part of a metric version the analysis reads.
type Metric struct {
	Digest     string     `json:"digest"`
	Name       string     `json:"name"`
	Type       Type       `json:"type"`
	Aggregate  Aggregate  `json:"aggregate,omitempty"`
	Direction  Direction  `json:"direction"`
	Level      Level      `json:"level,omitempty"`
	Vocabulary Vocabulary `json:"vocabulary,omitempty"`
	// Labels are a closed vocabulary's labels, in the order results list
	// them.
	Labels []string `json:"labels,omitempty"`
	// AppliesTo are the outcomes of the trials a trial-level metric applies
	// to; every finished outcome when empty.
	AppliesTo []Outcome `json:"applies_to,omitempty"`
	// Of is a cell-level metric's trial-level label metric, by digest.
	Of string `json:"of,omitempty"`
	// ExcludeLabels are the labels of Of a cell-level metric leaves out;
	// nil reads as spec 002's default, declined and none, and an empty
	// list leaves none out.
	ExcludeLabels []string `json:"exclude_labels,omitempty"`
}

// level is the metric's level, trial when unset.
func (m Metric) level() Level {
	if m.Level == "" {
		return LevelTrial
	}
	return m.Level
}

// numeric reports whether the metric's values are numbers.
func (m Metric) numeric() bool {
	switch m.Type {
	case TypeScore, TypeCount, TypeDuration, TypeCost:
		return true
	}
	return false
}

// Suite is the part of the candidate's suite version a comparison reads:
// its metrics, its task weights by task digest, and its primary metric.
type Suite struct {
	// Tasks are the suite version's tasks; when empty, the candidate side's
	// cells name them.
	Tasks   []SuiteTask `json:"tasks,omitempty"`
	Metrics []Metric    `json:"metrics"`
	// Weights maps a task digest to its weight; a task it does not list
	// weighs 1.
	Weights       map[string]float64 `json:"weights,omitempty"`
	PrimaryMetric string             `json:"primary_metric"`
}

// SuiteTask is one task of a suite version: its name and digest.
type SuiteTask struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// weight is a task's weight.
func (s Suite) weight(taskDigest string) float64 {
	if w, ok := s.Weights[taskDigest]; ok {
		return w
	}
	return 1
}

// Tuple is a cell's provenance tuple (spec 002). Tools is the tool set the
// trials ran with, the agent's own when the candidate narrows none, which
// the caller resolves; it is compared as a sorted set. Program is the
// canonical JSON of how the program is started and configured.
type Tuple struct {
	CandidateKind      string   `json:"candidate_kind"`
	AgentOrImageDigest string   `json:"agent_or_image_digest"`
	Program            string   `json:"program"`
	Model              string   `json:"model"`
	Reasoning          string   `json:"reasoning"`
	Tools              []string `json:"tools"`
	TaskHash           string   `json:"task_hash"`
	EnvironmentDigest  string   `json:"environment_digest"`
	WorkspaceRef       string   `json:"workspace_ref"`
	GraderDigests      []string `json:"grader_digests"`
}

// The tuple fields, as a comparison's `variable` and a confound name them.
const (
	FieldCandidateKind      = "candidate.kind"
	FieldAgentOrImageDigest = "agent_or_image_digest"
	FieldProgram            = "program"
	FieldModel              = "model"
	FieldReasoning          = "reasoning"
	FieldTools              = "tools"
	FieldTaskHash           = "task_hash"
	FieldEnvironmentDigest  = "environment_digest"
	FieldWorkspaceRef       = "workspace_ref"
	FieldGraderDigests      = "grader_digests"
)

// Request is one model request of a trial's sent and observed layers: the
// model it was sent to, the reasoning value it carried, empty for none,
// and the model that served it, empty when the record names none.
type Request struct {
	Model     string `json:"model"`
	Reasoning string `json:"reasoning,omitempty"`
	Served    string `json:"served,omitempty"`
}

// Measurement is one value of one metric, as read: for a trial-level
// metric a trial's, for a candidate-level metric a candidate's. Value is a
// bool, a float64 or a string by the metric's type; for an open
// vocabulary, the normalized raw label.
type Measurement struct {
	ID           string `json:"id"`
	Metric       string `json:"metric"`
	Grader       string `json:"grader"`
	Generation   int    `json:"generation"`
	Value        any    `json:"value"`
	SelfReported bool   `json:"self_reported"`
}

// Trial is one trial of a cell with what the analysis reads of it.
type Trial struct {
	ID string `json:"id"`
	// Outcome is empty while the trial is not done.
	Outcome Outcome `json:"outcome,omitempty"`
	// Retried is true when a later trial retries this one, which is then
	// never read.
	Retried         bool              `json:"retried,omitempty"`
	StartedAt       time.Time         `json:"started_at"`
	EndedAt         time.Time         `json:"ended_at"`
	Requests        []Request         `json:"requests,omitempty"`
	ServiceVersions map[string]string `json:"service_versions,omitempty"`
	DeclaredImage   string            `json:"declared_image,omitempty"`
	Measurements    []Measurement     `json:"measurements,omitempty"`
	// Cost is what the stored analysis's spend reads.
	Cost Cost `json:"cost"`
}

// Cell is one cell of a side: its task, its tuple, the producing grader of
// each metric at its task, and its trials.
type Cell struct {
	Task       string `json:"task"`
	TaskDigest string `json:"task_digest"`
	Tuple      Tuple  `json:"tuple"`
	// Producers maps a metric digest to its producing grader at this task:
	// a check, a judge, the decline judge or builtin. A metric without one
	// is read from a self-reported value, for an external candidate.
	Producers map[string]string `json:"producers,omitempty"`
	Trials    []Trial           `json:"trials"`
	// Final is true once every declared trial is done and graded.
	Final bool `json:"final"`
	// Judges holds the cell's judge gradings' costs, for the spend.
	Judges []Cost `json:"judges,omitempty"`
}

// Side is one side of a comparison: a candidate's cells in one experiment
// and its candidate-level measurements.
type Side struct {
	Candidate string `json:"candidate"`
	Cells     []Cell `json:"cells"`
	// Values are the candidate-level measurements of the side's candidate,
	// and Producers each candidate-level metric's producing grader.
	Values    []Measurement     `json:"values,omitempty"`
	Producers map[string]string `json:"producers,omitempty"`
}

// Taxonomy is one taxonomy record of an open-vocabulary metric.
type Taxonomy struct {
	ID        string            `json:"id"`
	Digest    string            `json:"digest"`
	Canonical []string          `json:"canonical"`
	Map       map[string]string `json:"map"`
	// PinnedByBar is true when a bar evaluation asked for this record.
	PinnedByBar bool `json:"pinned_by_bar,omitempty"`
}

// Calibration is the calibration of one judge for one label metric, as
// spec 006 reports it when the comparison is computed.
type Calibration struct {
	Grader     string  `json:"grader"`
	Metric     string  `json:"metric"`
	Pairs      int     `json:"pairs"`
	Alpha      float64 `json:"alpha"`
	Calibrated bool    `json:"calibrated"`
}

// Request kinds of a comparison: how its reference was chosen.
const (
	ReferenceBaseline  = "baseline"
	ReferenceCandidate = "candidate"
	ReferenceTask      = "task"
)

// Input is everything one comparison reads.
type Input struct {
	Parameters Parameters `json:"parameters"`
	// Suite is the candidate's suite version.
	Suite     Suite `json:"suite"`
	Candidate Side  `json:"candidate"`
	Reference Side  `json:"reference"`
	// Variable is the logical variable the request declared: agent, model,
	// reasoning, tools, environment, workspace or task; empty when it
	// declared none.
	Variable string `json:"variable,omitempty"`
	// AcrossTasks is true for a comparison whose variable is task: each side
	// holds one cell of one candidate, and they are two tasks.
	AcrossTasks bool `json:"across_tasks,omitempty"`
	// Declaration is, for a declared comparison of the general form, its
	// declaration (spec 008, Comparisons over cell variables): each side's
	// cells are named by their strata, which pair them, and the suite's
	// tasks are the strata, each with its candidate side's task digest.
	// Without strata each side holds one cell and the comparison has the
	// shape of one across tasks. Variable may then be task.
	Declaration *Declaration `json:"declaration,omitempty"`
	Exploratory bool         `json:"exploratory"`
	// Declared is, for a comparison its experiment declared, D: the number
	// of comparisons the experiment declared, whose primary results are
	// corrected together. Its primary result is then in no comparison's
	// family, draws PERMUTATION_RESAMPLES resolved with m = D, and carries
	// its p alone. 0 for any other comparison.
	Declared int `json:"declared,omitempty"`
	// LabelTests is true once label tests are built (spec 008, Build
	// order): a label result is then tested and can be changed; until then
	// it carries its per-label figures and no p, q or verdict, outside the
	// family. The first release builds no label tests.
	LabelTests bool `json:"label_tests,omitempty"`
	// Taxonomies maps an open-vocabulary metric's digest to the record it
	// is read at, on both sides.
	Taxonomies   map[string]Taxonomy `json:"taxonomies,omitempty"`
	Calibrations []Calibration       `json:"calibrations,omitempty"`
}

// Declaration is a declared comparison of the general form, as its
// inputs document records it: the cell variable it varies, its two
// levels, the candidate side's first, the cell variables Where holds to
// one level, those By stratifies, and the metric of its primary result.
type Declaration struct {
	Variable string            `json:"variable"`
	Levels   []string          `json:"levels"`
	Where    map[string]string `json:"where"`
	By       []string          `json:"by"`
	Metric   string            `json:"metric"`
}

// Summary is spec 002's summary of one metric over one set of trials.
type Summary struct {
	Aggregate     Aggregate `json:"aggregate,omitempty"`
	N             int       `json:"n"`
	NotApplicable int       `json:"not_applicable"`
	// Value is the rate, mean, median, p90 or cell-level statistic, or a
	// candidate-level metric's one value; nil when nothing is valued.
	Value          any            `json:"value,omitempty"`
	Numerator      *int           `json:"numerator,omitempty"`
	Denominator    int            `json:"denominator"`
	ExcludedLabels map[string]int `json:"excluded_labels,omitempty"`
	Missing        int            `json:"missing"`
	Interval       *Interval      `json:"interval,omitempty"`
	NoInterval     string         `json:"no_interval,omitempty"`
	Mean           *float64       `json:"mean,omitempty"`
	SD             *float64       `json:"sd,omitempty"`
	Min            *float64       `json:"min,omitempty"`
	Max            *float64       `json:"max,omitempty"`
	Distribution   []LabelShare   `json:"distribution,omitempty"`
	Mode           string         `json:"mode,omitempty"`
	Unmapped       int            `json:"unmapped,omitempty"`
	Tasks          *int           `json:"tasks,omitempty"`
	Coverage       *float64       `json:"coverage,omitempty"`
	Excluded       map[string]int `json:"excluded,omitempty"`
	Final          bool           `json:"final"`
}

// Number returns the summary's value as a number, and whether it is one.
func (s Summary) Number() (float64, bool) {
	v, ok := s.Value.(float64)
	return v, ok
}

// LabelShare is one label's count and proportion in a distribution, with
// the proportion's Wilson interval; Unmapped marks a raw label of an open
// vocabulary the taxonomy record does not map.
type LabelShare struct {
	Label      string    `json:"label"`
	Count      int       `json:"count"`
	Proportion float64   `json:"proportion"`
	Interval   *Interval `json:"interval,omitempty"`
	Unmapped   bool      `json:"unmapped,omitempty"`
}

// Delta is a result's delta: a number, or for a label metric an object
// from each label to the difference of its proportions.
type Delta struct {
	Value    float64
	PerLabel map[string]float64
}

// MarshalJSON writes the number or the object.
func (d Delta) MarshalJSON() ([]byte, error) {
	if d.PerLabel != nil {
		return json.Marshal(d.PerLabel)
	}
	return json.Marshal(d.Value)
}

// DeltaInterval is a delta's interval, or for a label metric an object from
// each label to its interval.
type DeltaInterval struct {
	Interval Interval
	PerLabel map[string]Interval
}

// MarshalJSON writes the interval or the object.
func (d DeltaInterval) MarshalJSON() ([]byte, error) {
	if d.PerLabel != nil {
		return json.Marshal(d.PerLabel)
	}
	return json.Marshal(d.Interval)
}

// TaskDigests are each side's task digest of a task-level result.
type TaskDigests struct {
	Candidate string `json:"candidate"`
	Reference string `json:"reference"`
}

// LeftOut is a task of weight above zero a suite-level result does not
// include: unpaired, or with too few valued trials on a side.
type LeftOut struct {
	Task      string `json:"task"`
	Why       string `json:"why"`
	Candidate int    `json:"candidate"`
	Reference int    `json:"reference"`
	// unpairedBy names the side that did not run an unpaired task.
	unpairedBy string
}

// Why a task is left out.
const (
	LeftOutUnpaired     = "unpaired"
	LeftOutTooFewTrials = "too_few_trials"
)

// Window is a side's time window: the earliest started_at and the latest
// ended_at of the trials read.
type Window struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// Confound is spec 002's confound entry. Candidate and Reference hold a
// count, a Window or a taxonomy record id by the confound's name.
type Confound struct {
	Name      string   `json:"name"`
	Tasks     []string `json:"tasks,omitempty"`
	Candidate any      `json:"candidate,omitempty"`
	Reference any      `json:"reference,omitempty"`
	Grader    string   `json:"grader,omitempty"`
}

// Verdict is a result's verdict.
type Verdict string

// The verdicts.
const (
	Improved     Verdict = "improved"
	Regressed    Verdict = "regressed"
	Changed      Verdict = "changed"
	WithinNoise  Verdict = "within_noise"
	Insufficient Verdict = "insufficient"
)

// Result is spec 002's result: one row of a comparison's results, or a
// benchmark table's pair result.
type Result struct {
	Level           Level          `json:"level"`
	Metric          string         `json:"metric"`
	MetricName      string         `json:"metric_name"`
	Direction       Direction      `json:"direction"`
	Task            string         `json:"task,omitempty"`
	TaskDigests     *TaskDigests   `json:"task_digests,omitempty"`
	A               string         `json:"a,omitempty"`
	B               string         `json:"b,omitempty"`
	Candidate       Summary        `json:"candidate"`
	Reference       Summary        `json:"reference"`
	Delta           *Delta         `json:"delta,omitempty"`
	DeltaKind       string         `json:"delta_kind,omitempty"`
	DeltaInterval   *DeltaInterval `json:"delta_interval,omitempty"`
	NoDeltaInterval string         `json:"no_delta_interval,omitempty"`
	Method          string         `json:"method,omitempty"`
	Statistic       string         `json:"statistic,omitempty"`
	Exact           *bool          `json:"exact,omitempty"`
	Resamples       *int           `json:"resamples,omitempty"`
	Seed            string         `json:"seed,omitempty"`
	P               *float64       `json:"p,omitempty"`
	Q               *float64       `json:"q,omitempty"`
	H               *float64       `json:"h,omitempty"`
	Verdict         Verdict        `json:"verdict,omitempty"`
	Reason          string         `json:"reason"`
	LeftOut         []LeftOut      `json:"left_out,omitempty"`
	Confounds       []Confound     `json:"confounds,omitempty"`
}

// UnpairedTasks are the task names one side holds alone.
type UnpairedTasks struct {
	CandidateOnly []string `json:"candidate_only"`
	ReferenceOnly []string `json:"reference_only"`
}

// Windows are the two sides' windows over every trial a comparison read.
type Windows struct {
	Candidate *Window `json:"candidate,omitempty"`
	Reference *Window `json:"reference,omitempty"`
}

// Correction is a comparison's record of its correction: the method, the
// primary metric tested alone (absent on an exploratory comparison) and
// how many p-values the one corrected family held.
type Correction struct {
	Method  string `json:"method"`
	Primary string `json:"primary,omitempty"`
	Family  int    `json:"family"`
	// Declared is, on a declared comparison, the size of the declared
	// family its primary result is corrected in.
	Declared int `json:"declared,omitempty"`
}

// Resolved are the values of PERMUTATION_RESAMPLES a comparison resolved:
// for its primary result and for its family.
type Resolved struct {
	Primary int `json:"primary"`
	Family  int `json:"family"`
}

// Comparison is the part of spec 002's comparison the analysis fills.
type Comparison struct {
	Variable           []string                   `json:"variable"`
	Exploratory        bool                       `json:"exploratory"`
	LabelTests         bool                       `json:"label_tests"`
	Final              bool                       `json:"final"`
	SingleVariable     bool                       `json:"single_variable"`
	DeclaredProvenance []string                   `json:"declared_provenance"`
	Confounds          []string                   `json:"confounds"`
	Results            []Result                   `json:"results"`
	Taxonomies         map[string]string          `json:"taxonomies,omitempty"`
	UnpairedTasks      UnpairedTasks              `json:"unpaired_tasks"`
	CandidateSuite     map[string]Summary         `json:"candidate_suite"`
	Windows            Windows                    `json:"windows"`
	VerdictCounts      map[string]map[Verdict]int `json:"verdict_counts"`
	Correction         Correction                 `json:"correction"`
	AnalysisVersion    string                     `json:"analysis_version"`
	Parameters         Parameters                 `json:"parameters"`
	Resamples          Resolved                   `json:"permutation_resamples"`
	InputsDigest       string                     `json:"inputs_digest"`
	Seed               string                     `json:"seed"`
}

// window is a side's time window over the trials a result reads, empty
// when it read none.
type window struct {
	from, to time.Time
	ok       bool
}

// add widens the window to hold a trial's times.
func (w *window) add(from, to time.Time) {
	if !w.ok {
		w.from, w.to, w.ok = from, to, true
		return
	}
	if from.Before(w.from) {
		w.from = from
	}
	if to.After(w.to) {
		w.to = to
	}
}

// merge widens the window to hold another.
func (w *window) merge(o window) {
	if o.ok {
		w.add(o.from, o.to)
	}
}

// public is the window as a record writes it.
func (w window) public() *Window {
	if !w.ok {
		return nil
	}
	return &Window{From: w.from, To: w.to}
}

// CostStatus is how a trial's or a grading's cost is known (spec 007).
type CostStatus string

// The cost statuses: priced, or left out of a spend and counted apart.
const (
	CostPriced   CostStatus = "priced"
	CostUnpriced CostStatus = "unpriced"
	CostUnknown  CostStatus = "unknown"
)

// Cost is one trial's or one judge grading's cost: USD when priced, and
// the costs its trial's APIs reported for themselves, which are never
// added into the spend.
type Cost struct {
	USD     float64    `json:"usd"`
	Status  CostStatus `json:"status"`
	Service float64    `json:"service_usd,omitempty"`
}
