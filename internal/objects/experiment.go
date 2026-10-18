// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json"
	"time"
)

// Version is one stored version of a versioned object: its record, and its
// kind's fields as applied, name included.
type Version struct {
	ID        string
	Kind      string
	Owner     string
	Name      string
	Digest    string
	Current   bool
	CreatedAt time.Time
	Body      json.RawMessage
}

// Answer is the version as the API answers it: the kind's fields, and
// beside them its id, owner, name, digest, whether it is current, and when
// it was made.
func (v Version) Answer() (map[string]any, error) {
	out := map[string]any{}
	if err := json.Unmarshal(v.Body, &out); err != nil {
		return nil, err
	}
	out["id"], out["owner"], out["name"], out["digest"] = v.ID, v.Owner, v.Name, v.Digest
	out["current"], out["created_at"] = v.Current, v.CreatedAt.UTC()
	return out, nil
}

// Budget is the Lux Budget an experiment is metered against.
type Budget struct {
	Name string `json:"name"`
}

// Reference is what every other candidate of an experiment is compared
// with: a baseline record, or one of its candidates.
type Reference struct {
	Baseline  string `json:"baseline,omitempty"`
	Candidate string `json:"candidate,omitempty"`
}

// Progress is an experiment's progress, computed and never written.
type Progress struct {
	Cells    map[string]int `json:"cells"`
	Trials   map[string]int `json:"trials"`
	Outcomes map[string]int `json:"outcomes"`
}

// Experiment is one execution of a suite against candidates.
type Experiment struct {
	ID          string            `json:"id"`
	Owner       string            `json:"owner"`
	Suite       string            `json:"suite"`
	Candidates  []string          `json:"candidates"`
	Trials      int               `json:"trials"`
	Budget      Budget            `json:"budget"`
	Parallelism int               `json:"parallelism"`
	Reference   Reference         `json:"reference"`
	Mode        string            `json:"mode"`
	State       string            `json:"state"`
	CreatedBy   string            `json:"created_by"`
	Judgments   []json.RawMessage `json:"judgments"`
	Progress    *Progress         `json:"progress,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`

	// SuiteID and CandidateIDs are the version ids the digests resolved
	// to in the owner's context, and SuiteName the suite's name.
	SuiteID      string   `json:"-"`
	SuiteName    string   `json:"-"`
	CandidateIDs []string `json:"-"`
	// IntakeClosed says the owner closed an import experiment's intake.
	IntakeClosed bool `json:"-"`
}

// Finished reports whether the experiment is complete or canceled.
func (e Experiment) Finished() bool { return e.State == StateComplete || e.State == StateCanceled }

// Rollout is one submission of some of a cell's trials to its executor.
type Rollout struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Executor    string `json:"executor"`
	Handle      string `json:"handle,omitempty"`
	Parallelism int    `json:"parallelism"`
	KeyID       string `json:"key_id,omitempty"`
	State       string `json:"state"`
}

// The kinds and states of a rollout.
const (
	RolloutSmoke = "smoke"
	RolloutMain  = "main"
	RolloutRetry = "retry"

	RolloutQueued    = "queued"
	RolloutSubmitted = "submitted"
	RolloutEnded     = "ended"
)

// Cell is one task and one candidate in an experiment.
type Cell struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	Experiment   string    `json:"experiment"`
	Task         string    `json:"task"`
	Candidate    string    `json:"candidate"`
	Tuple        Tuple     `json:"tuple"`
	Rollouts     []Rollout `json:"rollouts"`
	Adopted      []string  `json:"adopted"`
	State        string    `json:"state"`
	ParkedReason string    `json:"parked_reason,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// TaskID and CandidateID are the version ids; TaskPos and CandidatePos
	// the task's place in the suite and the candidate's in the experiment.
	TaskID       string `json:"-"`
	CandidateID  string `json:"-"`
	TaskPos      int    `json:"-"`
	CandidatePos int    `json:"-"`
}

// Lease is an external harness's hold on a trial.
type Lease struct {
	ID        string    `json:"id"`
	Subject   string    `json:"subject"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Failure is why a trial ended infra_failed, over_budget or canceled.
type Failure struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// ServiceUsage is what an API reported its own usage of a trial to be.
type ServiceUsage struct {
	CostUSDMicro int64              `json:"cost_usd_micro"`
	Units        map[string]float64 `json:"units,omitempty"`
}

// The cost states of a trial's observed layer.
const (
	CostKnown    = "known"
	CostUnpriced = "unpriced"
	CostUnknown  = "unknown"
)

// Observed is what happened in a trial: from the executor's and Lux's
// records, and what a harness submitted about the services it called.
type Observed struct {
	CostState       string                  `json:"cost_state,omitempty"`
	CostUSD         *float64                `json:"cost_usd,omitempty"`
	Duration        *float64                `json:"duration,omitempty"`
	ServiceVersions map[string]string       `json:"service_versions,omitempty"`
	ServiceUsage    map[string]ServiceUsage `json:"service_usage,omitempty"`
	// LuxRequests are the Lux request ids an external harness submitted
	// for the trial's model calls, kept as given.
	LuxRequests []string `json:"lux_requests,omitempty"`
}

// Declared is what an external harness declared about its own run.
type Declared struct {
	HarnessVersion string `json:"harness_version,omitempty"`
	Image          string `json:"image,omitempty"`
}

// The kinds of an evidence reference this release writes.
const EvidenceOutput = "output"

// EvidenceRef is one item of evidence in the owner's Arca space
// (specs/007-measurements-and-evidence.md).
type EvidenceRef struct {
	Kind   string `json:"kind"`
	Name   string `json:"name,omitempty"`
	Owner  string `json:"owner"`
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   *int64 `json:"size,omitempty"`
}

// OutputsSummary summarizes a trial's outputs.
type OutputsSummary struct {
	Files int `json:"files"`
}

// Trial is one attempt by a candidate at a task.
type Trial struct {
	ID           string            `json:"id"`
	Owner        string            `json:"owner"`
	Experiment   string            `json:"experiment"`
	Cell         string            `json:"cell"`
	Index        int               `json:"index"`
	ExecutorRef  string            `json:"executor_ref,omitempty"`
	Lease        *Lease            `json:"lease,omitempty"`
	State        string            `json:"state"`
	Outcome      string            `json:"outcome,omitempty"`
	Failure      *Failure          `json:"failure,omitempty"`
	RetryOf      string            `json:"retry_of,omitempty"`
	Observed     Observed          `json:"observed"`
	Declared     *Declared         `json:"declared,omitempty"`
	Checks       []json.RawMessage `json:"checks"`
	Judgments    []json.RawMessage `json:"judgments"`
	Evidence     []EvidenceRef     `json:"evidence"`
	Outputs      *OutputsSummary   `json:"outputs,omitempty"`
	Imported     bool              `json:"imported"`
	StartedAt    *time.Time        `json:"started_at,omitempty"`
	AgentEndedAt *time.Time        `json:"agent_ended_at,omitempty"`
	EndedAt      *time.Time        `json:"ended_at,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	// Values is each trial-level metric's current value, by name, on a
	// list's items alone.
	Values map[string]any `json:"values,omitempty"`

	// Rollout is the key of the rollout it belongs to; Retried says a
	// later trial retries it; AutoRetry says it is an automatic retry of
	// an infrastructure failure, which MAX_INFRA_RETRIES counts.
	Rollout   string `json:"-"`
	Retried   bool   `json:"-"`
	AutoRetry bool   `json:"-"`
}

// Finished reports whether the trial is a finished trial: done, with an
// outcome that is a result, and not retried.
func (t Trial) Finished() bool {
	return t.State == StateDone && t.Outcome != "" && !Excluded(t.Outcome) && !t.Retried
}

// Measurement is one value of one metric for one trial, or for a
// candidate-level metric for one candidate, from one grader generation.
type Measurement struct {
	ID           string    `json:"id"`
	Owner        string    `json:"owner"`
	Experiment   string    `json:"experiment"`
	Trial        string    `json:"trial,omitempty"`
	Candidate    string    `json:"candidate,omitempty"`
	Metric       string    `json:"metric"`
	Grader       string    `json:"grader"`
	SelfReported bool      `json:"self_reported"`
	Generation   int       `json:"generation"`
	Value        any       `json:"value"`
	Rationale    string    `json:"rationale"`
	Annotator    string    `json:"annotator"`
	CreatedAt    time.Time `json:"created_at"`

	// Cell is the trial's cell.
	Cell string `json:"-"`
}

// The types of an event.
const (
	EventExperiment          = "experiment"
	EventCell                = "cell"
	EventTrial               = "trial"
	EventGraded              = "graded"
	EventComparison          = "comparison"
	EventExperimentCompleted = "experiment.completed"
	EventExperimentCanceled  = "experiment.canceled"
	EventCellParked          = "cell.parked"
)

// Event is one entry of an experiment's event log.
type Event struct {
	ID         string          `json:"id"`
	Experiment string          `json:"-"`
	Seq        int64           `json:"seq"`
	Type       string          `json:"type"`
	Data       json.RawMessage `json:"data"`
	At         time.Time       `json:"at"`
}

// Sink reports whether an event type is a sink event.
func Sink(eventType string) bool {
	switch eventType {
	case EventExperimentCompleted, EventExperimentCanceled, EventCellParked:
		return true
	}
	return false
}

// SuiteName names a suite, every version of it, by its owner and name.
type SuiteName struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// Baseline is one record of a baseline's lineage.
type Baseline struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Name       string    `json:"name"`
	Suite      SuiteName `json:"suite"`
	Experiment string    `json:"experiment"`
	Candidate  string    `json:"candidate"`
	Previous   string    `json:"previous,omitempty"`
	CreatedBy  string    `json:"created_by"`
	PinnedAt   time.Time `json:"pinned_at"`
	CreatedAt  time.Time `json:"created_at"`
}

// TaxonomySource is who appended a taxonomy record.
type TaxonomySource struct {
	Owner         string `json:"owner,omitempty"`
	Canonicalizer string `json:"canonicalizer,omitempty"`
	Experiment    string `json:"experiment,omitempty"`
	Generation    int    `json:"generation,omitempty"`
}

// Taxonomy is one version of a suite's mapping of an open-vocabulary
// metric's raw labels to canonical ones.
type Taxonomy struct {
	ID        string            `json:"id"`
	Owner     string            `json:"owner"`
	Suite     SuiteName         `json:"suite"`
	Metric    string            `json:"metric"`
	Version   int               `json:"version"`
	Canonical []string          `json:"canonical"`
	Map       map[string]string `json:"map"`
	Source    TaxonomySource    `json:"source"`
	Previous  string            `json:"previous,omitempty"`
	Digest    string            `json:"digest"`
	CreatedBy string            `json:"created_by"`
	CreatedAt time.Time         `json:"created_at"`
}

// The states of a comparison.
const (
	ComparisonComputing = "computing"
	ComparisonComplete  = "complete"
	ComparisonFailed    = "failed"
)

// ResultRow is one result of a comparison (specs/002-objects.md). The
// summaries and the members only the analysis reads are kept as it
// rendered them.
type ResultRow struct {
	Level           string          `json:"level"`
	Metric          string          `json:"metric"`
	MetricName      string          `json:"metric_name"`
	Direction       string          `json:"direction"`
	Task            string          `json:"task,omitempty"`
	TaskDigests     json.RawMessage `json:"task_digests,omitempty"`
	Candidate       json.RawMessage `json:"candidate,omitempty"`
	Reference       json.RawMessage `json:"reference,omitempty"`
	Delta           json.RawMessage `json:"delta,omitempty"`
	DeltaKind       string          `json:"delta_kind,omitempty"`
	DeltaInterval   json.RawMessage `json:"delta_interval,omitempty"`
	NoDeltaInterval string          `json:"no_delta_interval,omitempty"`
	Method          string          `json:"method,omitempty"`
	Statistic       string          `json:"statistic,omitempty"`
	Exact           *bool           `json:"exact,omitempty"`
	Resamples       *int            `json:"resamples,omitempty"`
	Seed            string          `json:"seed,omitempty"`
	P               *float64        `json:"p,omitempty"`
	Q               *float64        `json:"q,omitempty"`
	H               *float64        `json:"h,omitempty"`
	Verdict         string          `json:"verdict,omitempty"`
	Reason          string          `json:"reason,omitempty"`
	LeftOut         json.RawMessage `json:"left_out,omitempty"`
	Confounds       json.RawMessage `json:"confounds,omitempty"`
}

// Computed are the members of a comparison the analysis fills.
type Computed struct {
	SingleVariable  *bool           `json:"single_variable,omitempty"`
	Confounds       []string        `json:"confounds"`
	Results         []ResultRow     `json:"results"`
	Taxonomies      json.RawMessage `json:"taxonomies,omitempty"`
	UnpairedTasks   json.RawMessage `json:"unpaired_tasks,omitempty"`
	CandidateSuite  json.RawMessage `json:"candidate_suite,omitempty"`
	Windows         json.RawMessage `json:"windows,omitempty"`
	VerdictCounts   json.RawMessage `json:"verdict_counts,omitempty"`
	Correction      json.RawMessage `json:"correction,omitempty"`
	AnalysisVersion string          `json:"analysis_version,omitempty"`
	Parameters      json.RawMessage `json:"parameters,omitempty"`
	InputsDigest    string          `json:"inputs_digest,omitempty"`
	Seed            string          `json:"seed,omitempty"`
	ComputedAt      *time.Time      `json:"computed_at,omitempty"`
}

// Comparison is the per-metric results of a candidate's cells against a
// reference.
type Comparison struct {
	ID                 string   `json:"id"`
	Owner              string   `json:"owner"`
	Experiment         string   `json:"experiment"`
	Candidate          string   `json:"candidate"`
	Baseline           string   `json:"baseline,omitempty"`
	ReferenceCandidate string   `json:"reference_candidate,omitempty"`
	Task               string   `json:"task,omitempty"`
	ReferenceTask      string   `json:"reference_task,omitempty"`
	Automatic          bool     `json:"automatic"`
	Exploratory        bool     `json:"exploratory"`
	State              string   `json:"state"`
	Final              bool     `json:"final"`
	Variable           []string `json:"variable"`
	DeclaredProvenance []string `json:"declared_provenance"`
	Computed
	CreatedAt time.Time `json:"created_at"`
}

// Variables maps each logical variable of specs/008-analysis.md to its
// tuple fields.
var Variables = map[string][]string{
	"agent":       {"candidate.kind", "agent_or_image_digest", "program"},
	"model":       {"model"},
	"reasoning":   {"reasoning"},
	"tools":       {"tools"},
	"environment": {"environment_digest"},
	"workspace":   {"workspace_ref"},
	"task":        {"task_hash"},
}
