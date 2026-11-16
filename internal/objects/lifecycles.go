// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"slices"

	"latere.ai/x/pkg/statemachine"
)

// The lifecycles Examen stores, each a statemachine table. Every writer of
// a lifecycle holds to its table: a write that would take a record along a
// step the table lacks changes nothing and is refused with an error that
// wraps statemachine.ErrIllegalTransition. The tests drive the real writers
// through each table on Postgres, and formal/ renders the tables into the
// Lean and TLA+ models the lean and tla gates check.

// ExperimentState is the state of an experiment.
type ExperimentState string

// The states of an experiment, as experiments.state holds them.
const (
	ExperimentPending  ExperimentState = "pending"
	ExperimentRunning  ExperimentState = "running"
	ExperimentGrading  ExperimentState = "grading"
	ExperimentComplete ExperimentState = "complete"
	ExperimentCanceled ExperimentState = "canceled"
)

// ExperimentTable is the lifecycle of an experiment, held in
// experiments.state. The experiment's own writer, in the transaction that
// holds its row lock, is the one writer of the column: a first rollout or
// an import starts it, the end of its last cell settles it through grading
// to complete, a retried cell reopens a complete experiment to running, and
// a cancel ends one that is not complete. Grading is the state a settling
// experiment passes through inside one transaction; a canceled experiment
// stays canceled.
var ExperimentTable = statemachine.Must(statemachine.New("Experiment", []ExperimentState{ExperimentPending}, map[ExperimentState][]ExperimentState{
	ExperimentPending:  {ExperimentRunning, ExperimentGrading, ExperimentCanceled},
	ExperimentRunning:  {ExperimentGrading, ExperimentCanceled},
	ExperimentGrading:  {ExperimentComplete, ExperimentCanceled},
	ExperimentComplete: {ExperimentRunning},
	ExperimentCanceled: nil,
}))

// CellState is the state of a cell.
type CellState string

// The states of a cell, as cells.state holds them. Blocked, over_budget and
// canceled are the parking states.
const (
	CellPending    CellState = "pending"
	CellSmoking    CellState = "smoking"
	CellRunning    CellState = "running"
	CellGrading    CellState = "grading"
	CellComplete   CellState = "complete"
	CellBlocked    CellState = "blocked"
	CellOverBudget CellState = "over_budget"
	CellCanceled   CellState = "canceled"
)

// Parked reports whether a cell state is one of the parking states.
func Parked(state CellState) bool {
	return state == CellBlocked || state == CellOverBudget || state == CellCanceled
}

// ParseCellState reads a cell state written as text outside the store, such
// as a query parameter, and reports whether CellTable has it.
func ParseCellState(s string) (CellState, bool) {
	st := CellState(s)
	return st, slices.Contains(CellTable.States(), st)
}

// CellTable is the lifecycle of a cell, held in cells.state. The cell's
// writer, in the transaction that holds the experiment's row lock, is the
// one writer of the column. A cell that completes passes through grading in
// the event log, and the stored state moves from where it was to complete
// in one write, so the table has those steps beside grading. An import
// makes its cell running. A retry returns a blocked or over_budget cell to
// smoking or running, or completes it when no trial needs a run; complete
// and canceled are final.
var CellTable = statemachine.Must(statemachine.New("Cell", []CellState{CellPending, CellRunning}, map[CellState][]CellState{
	CellPending:    {CellSmoking, CellBlocked, CellOverBudget, CellCanceled},
	CellSmoking:    {CellRunning, CellGrading, CellComplete, CellBlocked, CellOverBudget, CellCanceled},
	CellRunning:    {CellGrading, CellComplete, CellBlocked, CellOverBudget, CellCanceled},
	CellGrading:    {CellComplete, CellBlocked, CellOverBudget, CellCanceled},
	CellBlocked:    {CellSmoking, CellRunning, CellComplete, CellCanceled},
	CellOverBudget: {CellSmoking, CellRunning, CellComplete, CellCanceled},
	CellComplete:   nil,
	CellCanceled:   nil,
}))

// RolloutState is the state of a rollout.
type RolloutState string

// The states of a rollout, as the state member of a rollout in
// cells.rollouts holds them.
const (
	RolloutQueued    RolloutState = "queued"
	RolloutSubmitted RolloutState = "submitted"
	RolloutEnded     RolloutState = "ended"
)

// RolloutTable is the lifecycle of one rollout of a cell, held in the
// state member of the rollout in cells.rollouts. It moves in the cell's
// transaction: a submit takes a queued rollout to the executor, and an end
// closes a submitted rollout when its trials are done, or a queued or
// submitted one when its cell parks or its experiment is canceled.
var RolloutTable = statemachine.Must(statemachine.New("Rollout", []RolloutState{RolloutQueued}, map[RolloutState][]RolloutState{
	RolloutQueued:    {RolloutSubmitted, RolloutEnded},
	RolloutSubmitted: {RolloutEnded},
	RolloutEnded:     nil,
}))

// TrialState is the state of a trial.
type TrialState string

// The states of a trial, as trials.state holds them. TrialChecking is
// allowed by the column and read by the counts and the submit guard, and no
// writer makes it, so it is outside TrialTable.
const (
	TrialPending  TrialState = "pending"
	TrialRunning  TrialState = "running"
	TrialChecking TrialState = "checking"
	TrialDone     TrialState = "done"
)

// TrialTable is the lifecycle of a trial, held in trials.state. A lease or
// an executor's claim takes a pending trial to running, a lapsed lease
// returns a running trial to pending, and a result, an import or the end of
// its cell or experiment takes a pending or running trial to done. Done is
// final: a retry makes a new trial.
var TrialTable = statemachine.Must(statemachine.New("Trial", []TrialState{TrialPending}, map[TrialState][]TrialState{
	TrialPending: {TrialRunning, TrialDone},
	TrialRunning: {TrialPending, TrialDone},
	TrialDone:    nil,
}))

// GradingState is the state of a grading: pending until its run ends, and
// then the state of the check run or the judgment, the two kinds of grading
// that share the column.
type GradingState string

// The states of a grading, as gradings.state holds them. A check run ends
// ok, malformed, error, not_runnable, timed_out or not_run, and a judgment
// ok, refused, malformed, over_budget, gateway_refused, evidence_changed or
// not_run.
const (
	GradingPending         GradingState = "pending"
	GradingOK              GradingState = "ok"
	GradingMalformed       GradingState = "malformed"
	GradingError           GradingState = "error"
	GradingNotRunnable     GradingState = "not_runnable"
	GradingTimedOut        GradingState = "timed_out"
	GradingNotRun          GradingState = "not_run"
	GradingRefused         GradingState = "refused"
	GradingOverBudget      GradingState = "over_budget"
	GradingGatewayRefused  GradingState = "gateway_refused"
	GradingEvidenceChanged GradingState = "evidence_changed"
)

// GradingTable is the lifecycle of a grading, held in gradings.state. A
// finished trial makes its gradings pending; a run ends one in the state of
// its result, and a cell that parks or an experiment that is canceled ends
// the pending ones not_run. Every end is final. The table is one for both
// kinds: a grading's kind decides which ends its run answers with.
var GradingTable = statemachine.Must(statemachine.New("Grading", []GradingState{GradingPending}, map[GradingState][]GradingState{
	GradingPending: {GradingOK, GradingMalformed, GradingError, GradingNotRunnable, GradingTimedOut, GradingNotRun,
		GradingRefused, GradingOverBudget, GradingGatewayRefused, GradingEvidenceChanged},
	GradingOK:              nil,
	GradingMalformed:       nil,
	GradingError:           nil,
	GradingNotRunnable:     nil,
	GradingTimedOut:        nil,
	GradingNotRun:          nil,
	GradingRefused:         nil,
	GradingOverBudget:      nil,
	GradingGatewayRefused:  nil,
	GradingEvidenceChanged: nil,
}))

// ComparisonState is the state of a comparison.
type ComparisonState string

// The states of a comparison, as comparisons.state holds them.
const (
	ComparisonComputing ComparisonState = "computing"
	ComparisonComplete  ComparisonState = "complete"
	ComparisonFailed    ComparisonState = "failed"
)

// ComparisonTable is the lifecycle of a comparison, held in
// comparisons.state. Every comparison starts computing. A computation
// stores complete or failed, and the next computation of a provisional
// comparison, which a changed input asks for, stores its own result over
// it, so a failed one can complete and a complete one can fail. A
// computation that ends in the state the comparison already holds changes
// no state.
var ComparisonTable = statemachine.Must(statemachine.New("Comparison", []ComparisonState{ComparisonComputing}, map[ComparisonState][]ComparisonState{
	ComparisonComputing: {ComparisonComplete, ComparisonFailed},
	ComparisonComplete:  {ComparisonFailed},
	ComparisonFailed:    {ComparisonComplete},
}))

// UsageStatus is the status Lux gave a call, as usage_records.status holds
// it.
type UsageStatus string

// The statuses of a call Lux recorded.
const (
	UsageOK      UsageStatus = "ok"
	UsageRefused UsageStatus = "refused"
	UsageFailed  UsageStatus = "failed"
)

// ParseUsageStatus reads a status Lux wrote in a record of a call, and
// reports whether UsageStatusTable has it.
func ParseUsageStatus(s string) (UsageStatus, bool) {
	st := UsageStatus(s)
	return st, slices.Contains(UsageStatusTable.States(), st)
}

// UsageStatusTable is the lifecycle of a usage record's status, held in
// usage_records.status. The record mirrors Lux's record of the call by its
// request id, and a read that finds Lux's record changed stores it over the
// earlier one, so a record may start in any status and any status may be
// followed by either of the others.
var UsageStatusTable = statemachine.Must(statemachine.New("UsageStatus", []UsageStatus{UsageOK, UsageRefused, UsageFailed}, map[UsageStatus][]UsageStatus{
	UsageOK:      {UsageRefused, UsageFailed},
	UsageRefused: {UsageOK, UsageFailed},
	UsageFailed:  {UsageOK, UsageRefused},
}))

// KeyState is whether Examen released a Key it held for an experiment.
type KeyState string

// The states of a held Key. No column holds them: the Key is released when
// its released_at is set.
const (
	KeyHeld     KeyState = "held"
	KeyReleased KeyState = "released"
)

// ExperimentKeyTable is the lifecycle of the Key an experiment's external
// runs use, derived from experiment_usage.released_at. The release is the
// one writer: it sets released_at where it is NULL, so a Key is released
// once.
var ExperimentKeyTable = statemachine.Must(statemachine.New("ExperimentKey", []KeyState{KeyHeld}, map[KeyState][]KeyState{
	KeyHeld:     {KeyReleased},
	KeyReleased: nil,
}))

// JudgeKeyTable is the lifecycle of a Key a model judge calls through,
// derived from judge_keys.released_at in the same way.
var JudgeKeyTable = statemachine.Must(statemachine.New("JudgeKey", []KeyState{KeyHeld}, map[KeyState][]KeyState{
	KeyHeld:     {KeyReleased},
	KeyReleased: nil,
}))
