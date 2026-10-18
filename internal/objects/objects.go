// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package objects holds the objects of specs/002-objects.md as Go values:
// their fields as the API spells them, the rules an apply holds each to,
// the digest a version is pinned by, the provenance tuple of a cell, and
// the current value of a metric for a trial (specs/007-measurements-and-
// evidence.md). Nothing here reads the database or the network, so every
// rule is tested on its own.
//
// A refusal is an *apierr.Error naming the member it is about, so a
// handler answers it as it comes.
package objects

import (
	"regexp"
	"slices"
	"strings"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
)

// The kinds of versioned objects, as a path names them in the plural and an
// action in the singular.
const (
	KindMetric    = "metric"
	KindGrader    = "grader"
	KindTask      = "task"
	KindSuite     = "suite"
	KindCandidate = "candidate"
)

// Kinds lists every versioned kind.
func Kinds() []string {
	return []string{KindMetric, KindGrader, KindTask, KindSuite, KindCandidate}
}

// Prefix is the identifier prefix of a versioned kind.
func Prefix(kind string) string {
	switch kind {
	case KindMetric:
		return id.Metric
	case KindGrader:
		return id.Grader
	case KindTask:
		return id.Task
	case KindSuite:
		return id.Suite
	case KindCandidate:
		return id.Candidate
	}
	return ""
}

// The outcomes of a trial (specs/002-objects.md).
const (
	OutcomeCompleted   = "completed"
	OutcomeDeclined    = "declined"
	OutcomeTimedOut    = "timed_out"
	OutcomeTurnLimit   = "turn_limit"
	OutcomeOverBudget  = "over_budget"
	OutcomeInfraFailed = "infra_failed"
	OutcomeCanceled    = "canceled"
)

// Outcomes lists every outcome.
func Outcomes() []string {
	return []string{OutcomeCompleted, OutcomeDeclined, OutcomeTimedOut, OutcomeTurnLimit, OutcomeOverBudget, OutcomeInfraFailed, OutcomeCanceled}
}

// ResultOutcomes are the outcomes that are a result of the system under
// test, and the ones a metric's applies_to may name.
func ResultOutcomes() []string {
	return []string{OutcomeCompleted, OutcomeDeclined, OutcomeTimedOut, OutcomeTurnLimit}
}

// Excluded reports whether an outcome leaves its trial out of analysis.
func Excluded(outcome string) bool {
	return outcome == OutcomeOverBudget || outcome == OutcomeInfraFailed || outcome == OutcomeCanceled
}

// The states of an experiment, a cell and a trial.
const (
	StatePending    = "pending"
	StateSmoking    = "smoking"
	StateRunning    = "running"
	StateChecking   = "checking"
	StateGrading    = "grading"
	StateComplete   = "complete"
	StateDone       = "done"
	StateCanceled   = "canceled"
	StateBlocked    = "blocked"
	StateOverBudget = "over_budget"
)

// ExperimentStates, CellStates and TrialStates list each object's states.
func ExperimentStates() []string {
	return []string{StatePending, StateRunning, StateGrading, StateComplete, StateCanceled}
}

// CellStates lists a cell's states, the parking ones last.
func CellStates() []string {
	return []string{StatePending, StateSmoking, StateRunning, StateGrading, StateComplete, StateBlocked, StateOverBudget, StateCanceled}
}

// TrialStates lists a trial's states.
func TrialStates() []string {
	return []string{StatePending, StateRunning, StateChecking, StateDone}
}

// Parked reports whether a cell state is one of the parking states.
func Parked(state string) bool {
	return state == StateBlocked || state == StateOverBudget || state == StateCanceled
}

// The modes of an experiment.
const (
	ModeExecute = "execute"
	ModeImport  = "import"
)

// The schedulings of an experiment (specs/005-execution.md): task by task,
// a task's cells taking turns before the next task's start, or interleaved,
// every cell of every task taking its turn before any takes a second.
const (
	SchedulingTaskByTask  = "task_by_task"
	SchedulingInterleaved = "interleaved"
)

// The reserved grader digests (specs/002-objects.md).
const (
	GraderBuiltin = "builtin"
	GraderDecline = "builtin:decline"
	// SelfPrefix starts the grader of a value an external harness
	// submitted itself: self:<harness>.
	SelfPrefix = "self:"
)

// metricName is a metric's name; nameRule is every other name.
var (
	metricName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	nameRule   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
)

// ValidName reports whether s is a name of the kind: a metric's
// [a-z][a-z0-9_]{0,62}, any other's 1 to 63 characters of [a-z0-9._-]
// starting with a letter or a digit, and never of the form of an
// identifier of its kind.
func ValidName(kind, s string) bool {
	if kind == KindMetric {
		return metricName.MatchString(s)
	}
	return nameRule.MatchString(s) && !id.Valid(Prefix(kind), s)
}

// ValidHarness reports whether s is an external candidate's harness name.
func ValidHarness(s string) bool { return nameRule.MatchString(s) }

// ParseRef reads a reference that pins a version: a digest, or
// name@digest. A name alone is unpinned_reference on field.
func ParseRef(field, s string) (name, digest string, err error) {
	if id.ValidDigest(s) {
		return "", s, nil
	}
	if n, d, ok := strings.Cut(s, "@"); ok && id.ValidDigest(d) && n != "" {
		return n, d, nil
	}
	return "", "", apierr.OnField(apierr.CodeUnpinnedReference, field, "a reference names a digest, or a name and a digest as name@sha256:..., and not "+quote(s))
}

// quote shortens a value for a developer detail.
func quote(s string) string {
	if len(s) > 80 {
		s = s[:80] + "..."
	}
	return `"` + s + `"`
}

// metadata checks a metadata map: at most METADATA_MAX_PAIRS pairs, and no
// key beginning examen., which is reserved for the labels Examen writes.
func metadata(field string, m map[string]string) error {
	if len(m) > limits.MetadataMaxPairs {
		return apierr.Exceeds("METADATA_MAX_PAIRS", field, "metadata holds more than METADATA_MAX_PAIRS (16) pairs")
	}
	for _, k := range sortedKeys(m) {
		if strings.HasPrefix(k, "examen.") {
			return apierr.OnField(apierr.CodeInvalidField, field+"."+k, "a metadata key beginning examen. is reserved for the labels Examen writes")
		}
		if k == "" {
			return apierr.OnField(apierr.CodeInvalidField, field, "a metadata key is empty")
		}
	}
	return nil
}

// ValidateMetadata is the rule of every object that carries metadata.
func ValidateMetadata(field string, m map[string]string) error { return metadata(field, m) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// relative reports whether p is a relative path that never leaves its
// root: not empty, not absolute, and no .. segment.
func relative(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") {
		return false
	}
	return !slices.Contains(strings.Split(strings.TrimSuffix(p, "/"), "/"), "..")
}

// invalid is an invalid_field about one member.
func invalid(field, detail string) *apierr.Error {
	return apierr.OnField(apierr.CodeInvalidField, field, detail)
}

// missing is a missing_field about one member.
func missing(field, detail string) *apierr.Error {
	return apierr.OnField(apierr.CodeMissingField, field, detail)
}

// unpinned is an unpinned_reference about one member.
func unpinned(field, detail string) *apierr.Error {
	return apierr.OnField(apierr.CodeUnpinnedReference, field, detail)
}
