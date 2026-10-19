// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package store is the contract of what Examen keeps in Postgres
// (specs/011-distribution.md): every table behind one interface, with the
// Postgres adapter in internal/store/postgres and the migrations it applies
// in internal/store/migrations. A handler depends on this package alone and
// cannot tell which database serves it.
//
// Nothing here depends on session state, so the store serves through a
// pooler in transaction mode: no session advisory lock, no LISTEN, and no
// state kept on a connection between transactions.
package store

import (
	"context"
	"errors"
	"time"

	"latere.ai/x/examen/internal/objects"
)

// The errors an adapter answers with. A handler maps each to the error of
// its route; none carries a value of the row. A refusal that depends on
// the state a transaction read, such as a lapsed lease or a full cell, is
// the *apierr.Error of the API's table instead, made where the state was
// read.
var (
	// ErrNotFound is a row that does not exist.
	ErrNotFound = errors.New("store: not found")
	// ErrPrecondition is an If-Match or If-None-Match that does not hold.
	ErrPrecondition = errors.New("store: the precondition does not hold")
	// ErrActiveLimit is an experiment over the owner's active experiments.
	ErrActiveLimit = errors.New("store: the owner holds as many active experiments as allowed")
)

// Store is the one store a server runs on.
type Store interface {
	// Idempotency is the table of idempotency keys (specs/003-api.md).
	Idempotency() Idempotency
	// Versions keeps every version of the versioned objects.
	Versions() Versions
	// Experiments keeps experiments and everything they hold.
	Experiments() Experiments
	// Baselines keeps baseline records.
	Baselines() Baselines
	// Taxonomies keeps taxonomy records.
	Taxonomies() Taxonomies
	// Ready answers whether the store can serve: the database answers and
	// its schema holds every migration this binary carries.
	Ready(ctx context.Context) error
	// Close releases what the store holds.
	Close() error
}

// IdempotencyKey is one key a subject sent with a request, and, once the
// request was answered, the answer it replays.
type IdempotencyKey struct {
	Subject string
	Key     string
	// Route is the method and the path the key was first sent to.
	Route string
	// BodyDigest is the digest of the canonical JSON of the first body, so
	// a repeat with the same content in another spelling is the same
	// request.
	BodyDigest string
	// Done is false while the first request is still being answered.
	Done        bool
	Status      int
	ContentType string
	Body        []byte
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Idempotency keeps the idempotency keys. A key is one subject's, whatever
// route it is sent to, so a key reused on another route is found and
// refused rather than starting a second request.
type Idempotency interface {
	// Begin reserves the key for reserve and reports true, or returns the
	// record already held and unexpired and false. An expired record, the
	// answer of a window that ended or a reservation whose request never
	// finished, is replaced. Replicas that race on one key reserve it once.
	Begin(ctx context.Context, k IdempotencyKey, reserve time.Duration) (IdempotencyKey, bool, error)
	// Finish stores the answer of a reserved key, kept for window from the
	// key's first use: ErrNotFound when the key is not reserved.
	Finish(ctx context.Context, k IdempotencyKey, window time.Duration) error
	// Abandon drops a reserved key whose request failed, so a retry runs
	// again.
	Abandon(ctx context.Context, subject, key string) error
	// Prune deletes up to limit expired keys and says how many.
	Prune(ctx context.Context, limit int) (int, error)
}

// Page is where a list resumes and how many items it reads: one more than
// the page holds, so the caller knows whether another follows.
type Page struct {
	// After is the id of the last item of the page before; empty on the
	// first page.
	After string
	// Limit is the items of the page; Limit+1 are read.
	Limit int
}

// Precondition is an apply's If-Match or If-None-Match.
type Precondition struct {
	// IfMatch, when set, is the digest or id that must be current.
	IfMatch string
	// IfNoneMatch says the name must have no version or record yet.
	IfNoneMatch bool
}

// VersionFilter narrows a list of versions. A list holds the current
// version of each name of the owners it names, or with All every version.
type VersionFilter struct {
	Owners []string
	All    bool
	Name   string
	// Field filters are the kind's own: metric type, grader kind and the
	// metric it produces, candidate kind, agent and model.
	MetricType    string
	GraderKind    string
	Produces      string
	CandidateKind string
	Agent         string
	Model         string
	// Digests keeps the versions whose digest is one of them: the tasks a
	// suite version pins.
	Digests []string
	// Labels keeps the versions whose metadata holds every pair.
	Labels map[string]string
}

// Versions keeps every version of a metric, grader, task, suite or
// candidate. A version is never changed and never deleted.
type Versions interface {
	// Apply stores v as a new version of its name unless its digest
	// already is one, and makes it current either way. It answers the
	// version and whether it was made. ErrPrecondition when pre does not
	// hold: IfMatch names a digest that is not current, or IfNoneMatch a
	// name that has a version.
	Apply(ctx context.Context, v objects.Version, pre Precondition) (objects.Version, bool, error)
	// ByID is one version, whoever owns it.
	ByID(ctx context.Context, kind, id string) (objects.Version, error)
	// ByDigest is the version with a digest in an owner's context.
	ByDigest(ctx context.Context, kind, owner, digest string) (objects.Version, error)
	// Current is a name's current version.
	Current(ctx context.Context, kind, owner, name string) (objects.Version, error)
	// History is every version of a name, newest first.
	History(ctx context.Context, kind, owner, name string, p Page) ([]objects.Version, error)
	// List is the versions a filter keeps, newest first by id.
	List(ctx context.Context, kind string, f VersionFilter, p Page) ([]objects.Version, error)
	// Many reads versions by id, whoever owns them.
	Many(ctx context.Context, ids []string) (map[string]objects.Version, error)
}

// Baselines keeps baseline records, a lineage per owner, suite and name.
type Baselines interface {
	// Pin appends b to its lineage unless the current record names the
	// same experiment and candidate, which it answers instead; it answers
	// whether a record was made. IfMatch names the record id that must be
	// current. ErrPrecondition when pre does not hold.
	Pin(ctx context.Context, b objects.Baseline, pre Precondition) (objects.Baseline, bool, error)
	// ByID is one record.
	ByID(ctx context.Context, id string) (objects.Baseline, error)
	// Current is a lineage's latest record.
	Current(ctx context.Context, owner string, suite objects.SuiteName, name string) (objects.Baseline, error)
	// History is every record of a lineage, newest first.
	History(ctx context.Context, owner string, suite objects.SuiteName, name string, p Page) ([]objects.Baseline, error)
	// List is the current record of each lineage of a suite of the owners
	// named.
	List(ctx context.Context, owners []string, suite objects.SuiteName, p Page) ([]objects.Baseline, error)
}

// Taxonomies keeps taxonomy records, a lineage per suite and metric.
type Taxonomies interface {
	// Append stores t as its lineage's next record unless its canonical
	// labels and map equal the current record's, which it answers instead;
	// it answers whether a record was made. IfMatch names the record id
	// that must be current. ErrPrecondition when pre does not hold.
	Append(ctx context.Context, t objects.Taxonomy, pre Precondition) (objects.Taxonomy, bool, error)
	// ByID is one record.
	ByID(ctx context.Context, id string) (objects.Taxonomy, error)
	// Current is a lineage's latest record.
	Current(ctx context.Context, suite objects.SuiteName, metric string) (objects.Taxonomy, error)
	// History is every record of a lineage, newest first.
	History(ctx context.Context, suite objects.SuiteName, metric string, p Page) ([]objects.Taxonomy, error)
	// Labels counts the raw labels of a metric across the experiments of
	// an owner that ran a version of a suite, by their current values,
	// most frequent first.
	Labels(ctx context.Context, suite objects.SuiteName, metric string) ([]LabelCount, error)
}

// LabelCount is one raw label and how many trials hold it.
type LabelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// NewExperiment is what Create stores: the experiment, its cells with the
// trials each starts with, and its automatic comparisons.
type NewExperiment struct {
	Experiment  objects.Experiment
	Cells       []NewCell
	Comparisons []objects.Comparison
	// MaxActive is how many of the owner's experiments may be neither
	// complete nor canceled, counted with the insert.
	MaxActive int
}

// NewCell is a cell and the rollout it starts with.
type NewCell struct {
	Cell objects.Cell
	// Smoke is the smoke rollout of a cell of an experiment in mode
	// execute, with its trials; none in mode import.
	Smoke *Rollout
}

// Rollout is a rollout with the trials it runs.
type Rollout struct {
	Rollout objects.Rollout
	Trials  []RolloutTrial
}

// RolloutTrial is one trial a rollout runs.
type RolloutTrial struct {
	Index   int
	RetryOf string
	Auto    bool
}

// ExperimentFilter narrows a list of experiments.
type ExperimentFilter struct {
	Owners []string
	// Suite is a suite digest, or SuiteName every version of a name.
	Suite     string
	SuiteName string
	Candidate string
	State     string
	Mode      string
	Baseline  string
	Labels    map[string]string
}

// CellFilter narrows a list of cells.
type CellFilter struct {
	State     string
	Task      string
	Candidate string
}

// TrialFilter narrows a list of trials.
type TrialFilter struct {
	Cells     []string
	State     string
	Outcome   string
	Imported  *bool
	Retried   *bool
	Ascending bool
}

// CalibrationPair is one trial that calibrates a judge on a metric: the
// judge's current value, and each annotator's current value under each
// annotation grader.
type CalibrationPair struct {
	Trial  string
	Judge  any
	People []any
}

// MeasurementFilter narrows a list of measurements. Generation is
// "current", "all" or a number.
type MeasurementFilter struct {
	Experiment string
	Trial      string
	Cells      []string
	Candidate  string
	Metric     string
	Grader     string
	Generation string
	// CandidateLevel keeps the values that belong to a candidate rather
	// than to a trial: those of a candidate-level metric.
	CandidateLevel bool
}

// ComparisonFilter narrows a list of comparisons.
type ComparisonFilter struct {
	Owners             []string
	Experiment         string
	Suite              string
	SuiteName          string
	Candidate          string
	Baseline           string
	ReferenceCandidate string
	Automatic          *bool
	Declared           *bool
	Exploratory        *bool
	Final              *bool
	SingleVariable     *bool
}

// TrialCounts counts one cell's trials by state, by outcome and retried.
type TrialCounts struct {
	States   map[string]int
	Outcomes map[string]int
	Retried  int
}

// LeaseRequest asks for pending trials of an external candidate, or of
// every candidate whose cells it names when Candidate is empty.
type LeaseRequest struct {
	Experiment string
	Candidate  string
	Subject    string
	// Max is the most trials to lease; 0 is every one the call may lease.
	Max int
	// Cells are the cells whose trials may be handed out; the others'
	// handing out was not allowed by experiment.run.
	Cells []string
}

// Leased is a lease and the trials it holds.
type Leased struct {
	Lease     string
	ExpiresAt time.Time
	Trials    []objects.Trial
}

// Measured is one value a harness submitted itself, checked against its
// metric.
type Measured struct {
	Metric    string
	Value     any
	Rationale string
}

// Outcome is what a harness submits or imports of one trial.
type Outcome struct {
	Outcome      string
	Failure      *objects.Failure
	StartedAt    time.Time
	AgentEndedAt time.Time
	// EndedAt is an imported run's end; a submitted trial's is the
	// submit's own time.
	EndedAt  *time.Time
	Evidence []objects.EvidenceRef
	Observed objects.Observed
	Declared *objects.Declared
	Ref      string
	// SelfReported are the values the harness computed itself, stored
	// under self:<harness>.
	SelfReported []Measured
	// Harness is the external candidate's harness, and Declined whether
	// the suite lists declined, which the built-in decline judge fills.
	Harness  string
	Declined bool
}

// Submission is a submit of a leased trial.
type Submission struct {
	Trial   string
	Lease   string
	Subject string
	// Digest is the digest of the body, so a repeat is told from another
	// submit.
	Digest string
	Outcome
}

// Import is a past run of an external harness imported as a trial.
type Import struct {
	Experiment string
	Cell       objects.Cell
	Outcome
}

// QueuedRollout is a rollout waiting to be submitted.
type QueuedRollout struct {
	Experiment string
	Owner      string
	CreatedBy  string
	State      string
	Budget     string
	Cell       string
	Key        string
}

// Experiments keeps experiments, their cells, trials, measurements, event
// log and comparisons. Every change of state writes its events in its own
// transaction.
type Experiments interface {
	Create(ctx context.Context, e NewExperiment) error
	Get(ctx context.Context, id string) (objects.Experiment, error)
	List(ctx context.Context, f ExperimentFilter, p Page) ([]objects.Experiment, error)

	Cells(ctx context.Context, experiment string, f CellFilter, p Page) ([]objects.Cell, error)
	AllCells(ctx context.Context, experiment string) ([]objects.Cell, error)
	Cell(ctx context.Context, id string) (objects.Cell, error)
	Counts(ctx context.Context, experiment string) (map[string]TrialCounts, error)

	Trials(ctx context.Context, experiment string, f TrialFilter, p Page) ([]objects.Trial, error)
	Trial(ctx context.Context, id string) (objects.Trial, error)

	Measurements(ctx context.Context, f MeasurementFilter, p Page) ([]objects.Measurement, error)
	MeasurementsOf(ctx context.Context, trials []string) ([]objects.Measurement, error)
	Measurement(ctx context.Context, id string) (objects.Measurement, error)
	// CalibrationPairs are the trials of an owner's, across its
	// experiments, holding a judge version's current value of a metric and
	// at least one person's current annotation of it, by trial id.
	CalibrationPairs(ctx context.Context, owner, grader, metric string) ([]CalibrationPair, error)

	Events(ctx context.Context, experiment string, after int64, limit int) ([]objects.Event, error)
	LatestSeq(ctx context.Context, experiment string) (int64, error)

	Cancel(ctx context.Context, id string) (objects.Experiment, error)
	Close(ctx context.Context, id string) (objects.Experiment, error)
	RetryCell(ctx context.Context, cell string) (objects.Cell, error)
	Import(ctx context.Context, imp Import) (objects.Trial, error)
	Lease(ctx context.Context, r LeaseRequest) (Leased, error)
	Renew(ctx context.Context, experiment, lease, subject string) (Leased, error)
	Submit(ctx context.Context, s Submission) (objects.Trial, error)
	ExpireLeases(ctx context.Context) (int, error)
	// Queued are the rollouts waiting to be submitted, of one experiment,
	// or of every one when it names none.
	Queued(ctx context.Context, experiment string, limit int) ([]QueuedRollout, error)
	SubmitRollout(ctx context.Context, cell, key string) error
	Park(ctx context.Context, cell, state, reason string) error
	// Leasable are the cells of a candidate with pending trials in
	// submitted rollouts, or of every candidate when it names none.
	Leasable(ctx context.Context, experiment, candidate string) ([]objects.Cell, error)

	Comparisons(ctx context.Context, f ComparisonFilter, p Page) ([]objects.Comparison, error)
	Comparison(ctx context.Context, id string) (objects.Comparison, error)
	CreateComparison(ctx context.Context, c objects.Comparison) error
	Dirty(ctx context.Context, limit int) ([]objects.Comparison, error)
	// Computed stores what the analysis answered of a comparison and the
	// measurements it read, in state complete or failed, and journals it.
	Computed(ctx context.Context, id string, state string, c objects.Computed, inputs []string) error
	// Clean clears a comparison's dirty mark, as of the update it read.
	Clean(ctx context.Context, id string, seen time.Time) error
	// Declare recomputes an experiment's declared analysis under its row
	// lock: correct is given the experiment and the newest comparison of
	// each of its declared comparisons, and what it answers replaces the
	// stored analysis. Serialized with every comparison's store, the last
	// recomputation reads every comparison stored before it.
	Declare(ctx context.Context, experiment string, correct func(objects.Experiment, []objects.Comparison) (objects.DeclaredAnalysis, error)) error
	// DeclaredAnalysis reads an experiment's stored declared analysis;
	// false when none is computed yet.
	DeclaredAnalysis(ctx context.Context, experiment string) (objects.DeclaredAnalysis, bool, error)
}
