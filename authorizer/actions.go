// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package authorizer

import (
	"slices"

	"latere.ai/x/pkg/authz"
)

// Core is the name the shared contract carries this vocabulary under. A
// grant on a personal access token qualifies an action with it:
// "examen:trial.submit".
const Core = "examen"

// The resource kinds. An action acts on exactly one.
const (
	KindMetric     = "Metric"
	KindGrader     = "Grader"
	KindTask       = "Task"
	KindSuite      = "Suite"
	KindCandidate  = "Candidate"
	KindExperiment = "Experiment"
	KindBaseline   = "Baseline"
)

// The actions: every question examend asks an authorizer in this release,
// in the order of the table of specs/004-identity-and-authorization.md.
const (
	ActionMetricApply = "metric.apply"
	ActionMetricRead  = "metric.read"
	ActionMetricList  = "metric.list"

	ActionGraderApply = "grader.apply"
	ActionGraderRead  = "grader.read"
	ActionGraderList  = "grader.list"

	ActionTaskApply = "task.apply"
	ActionTaskRead  = "task.read"
	ActionTaskList  = "task.list"

	ActionSuiteApply = "suite.apply"
	ActionSuiteRead  = "suite.read"
	ActionSuiteList  = "suite.list"
	// ActionTaxonomyApply and ActionTaxonomyRead act on the suite whose
	// open-vocabulary metric the taxonomy maps.
	ActionTaxonomyApply = "taxonomy.apply"
	ActionTaxonomyRead  = "taxonomy.read"

	ActionCandidateApply = "candidate.apply"
	ActionCandidateRead  = "candidate.read"
	ActionCandidateList  = "candidate.list"

	// ActionExperimentCreate sends the id minted before the question, so an
	// authorizer can keep a record of the experiment it allows. Its allow
	// carries the limits the experiment is held to.
	ActionExperimentCreate = "experiment.create"
	// ActionExperimentRead is asked by every read of an experiment and of
	// what it holds: its cells, trials, measurements and comparisons.
	ActionExperimentRead   = "experiment.read"
	ActionExperimentList   = "experiment.list"
	ActionExperimentUpdate = "experiment.update"
	// ActionCellRetry resumes a cell parked blocked or over_budget. It acts
	// on the experiment and is decided as experiment.update is.
	ActionCellRetry        = "cell.retry"
	ActionExperimentCancel = "experiment.cancel"
	ActionExperimentImport = "experiment.import"
	// ActionTrialLease and ActionTrialSubmit are an external harness's work
	// on an experiment's trials. They are actions of their own, so a grant
	// can name them alone, and are decided as experiment.update is.
	ActionTrialLease  = "trial.lease"
	ActionTrialSubmit = "trial.submit"
	// ActionExperimentRun is asked by Examen itself, with no caller
	// present, before each step of an experiment's unattended work: a
	// rollout's submission, and handing out a cell's external trials. Its
	// subject is the experiment's created_by and its claims are empty.
	ActionExperimentRun = "experiment.run"

	ActionBaselinePin  = "baseline.pin"
	ActionBaselineRead = "baseline.read"
	ActionBaselineList = "baseline.list"
)

// The fields a resource carries beside its kind and id, as
// specs/004-identity-and-authorization.md defines each.
const (
	// FieldOwner is the object's owner; on an apply, a create, a pin or a
	// list, the owner the request names, left out when it names none. On a
	// taxonomy action it is the suite's owner.
	FieldOwner = "owner"
	// FieldName and FieldDigest are the object's name and digest; on a
	// baseline, FieldSuite is the suite's name.
	FieldName   = "name"
	FieldDigest = "digest"
	// FieldShared says, on a read of another owner's object, whether a
	// share names the context owner for it.
	FieldShared = "shared"
	// FieldPinned says, on a read of another owner's task, metric, grader
	// or suite, whether an experiment of the context owner pins it.
	FieldPinned = "pinned"
	// FieldMetricType, FieldGraderKind, FieldCandidateKind and
	// FieldWorkspaceKind are an object's own type or kind.
	FieldMetricType    = "metric_type"
	FieldGraderKind    = "grader_kind"
	FieldCandidateKind = "candidate_kind"
	FieldWorkspaceKind = "workspace_kind"
	// FieldModel, FieldAgent, FieldImage and FieldReasoning are a grader's
	// judge model or agent, or a candidate's pinned agent version, image,
	// model and reasoning level; FieldImage on a task is its environment's
	// image digest.
	FieldModel     = "model"
	FieldAgent     = "agent"
	FieldImage     = "image"
	FieldReasoning = "reasoning"
	// FieldHarness is an external candidate's harness.
	FieldHarness = "harness"
	// FieldCredentialAudiences are the audiences a program candidate's
	// credentials name; on experiment.create, those of every candidate.
	FieldCredentialAudiences = "credential_audiences"
	// FieldRepository, FieldCommit and FieldPath are a task workspace's
	// Origo repository and commit, or Arca path.
	FieldRepository = "repository"
	FieldCommit     = "commit"
	FieldPath       = "path"
	// FieldTaskCount and FieldMetricCount are how many tasks and metrics a
	// suite version pins.
	FieldTaskCount   = "task_count"
	FieldMetricCount = "metric_count"
	// FieldMetric and FieldVersion are, on a taxonomy action, the
	// open-vocabulary metric's digest and the record's version.
	FieldMetric  = "metric"
	FieldVersion = "version"
	// FieldSuite and FieldSuiteOwner are an experiment's suite digest and
	// its owner, or a baseline's suite name and its owner.
	FieldSuite      = "suite"
	FieldSuiteOwner = "suite_owner"
	// FieldCandidates, FieldModels, FieldJudgeModels and FieldExecutors
	// are an experiment's candidate digests, every model they name, every
	// model the suite's model judges name, and the executors the
	// candidates need.
	FieldCandidates  = "candidates"
	FieldModels      = "models"
	FieldJudgeModels = "judge_models"
	FieldExecutors   = "executors"
	// FieldGraderKinds are the kinds of the graders an experiment's suite
	// version names, its task checks, decline judge and canonicalizers
	// included, each once and sorted; the built-in decline judge is none.
	FieldGraderKinds = "grader_kinds"
	// FieldCells, FieldTrials and FieldTrialsTotal are tasks × candidates,
	// N, and cells × N.
	FieldCells       = "cells"
	FieldTrials      = "trials"
	FieldTrialsTotal = "trials_total"
	FieldParallelism = "parallelism"
	// FieldBudget and FieldBudgetCapUSDMicro are the Lux Budget a create
	// names, or the cap it gives in micro-dollars in its place.
	FieldBudget            = "budget"
	FieldBudgetCapUSDMicro = "budget_cap_usd_micro"
	// FieldReferenceCandidate is the candidate digest an experiment's
	// reference names, when it names a candidate; FieldBaseline and
	// FieldBaselineOwner the baseline record's id and owner when it names
	// one.
	FieldReferenceCandidate = "reference_candidate"
	FieldBaseline           = "baseline"
	FieldBaselineOwner      = "baseline_owner"
	// FieldMode is an experiment's mode, execute or import, and FieldState
	// a stored experiment's state.
	FieldMode  = "mode"
	FieldState = "state"
	// FieldTask, FieldCandidate, FieldExecutor and FieldExecutorRef are an
	// import's: the task and candidate digests of its cell, and the
	// executor and the reference of what it imports.
	FieldTask        = "task"
	FieldCandidate   = "candidate"
	FieldExecutor    = "executor"
	FieldExecutorRef = "executor_ref"
	// FieldTrial, FieldLease and FieldOutcome are a lease's and a submit's:
	// the trial's id, the lease's id on a renewal or a submit, and the
	// outcome submitted.
	FieldTrial   = "trial"
	FieldLease   = "lease"
	FieldOutcome = "outcome"
	// FieldExperiment is, on a baseline pin, the experiment it pins.
	FieldExperiment = "experiment"
	// FieldCreatedBy, FieldPurpose and FieldCell are, on experiment.run,
	// the subject that created the experiment, the work about to be done,
	// and the cell it is for; FieldCell is also the cell cell.retry names.
	FieldCreatedBy = "created_by"
	FieldPurpose   = "purpose"
	FieldCell      = "cell"
	// FieldCellState and FieldParkedReason are, on cell.retry, the cell's
	// parked state and its parked_reason.
	FieldCellState    = "cell_state"
	FieldParkedReason = "parked_reason"
)

// PurposeTrial is the purpose experiment.run names for a cell's rollouts
// and for handing out its external trials.
const PurposeTrial = "trial"

// action is one row of the vocabulary table: its name, its kind, the
// fields it sends, and whether it sends an id.
type action struct {
	name, kind string
	fields     []string
	id         bool
}

// stored are the fields of a read of a versioned object.
var stored = []string{FieldOwner, FieldName, FieldDigest, FieldShared, FieldPinned}

// experiment are the fields every action on a stored experiment sends.
var experiment = []string{FieldOwner, FieldSuite, FieldState, FieldMode}

// table is the vocabulary of specs/004-identity-and-authorization.md for
// this release, in its order, with the fields each action sends.
var table = []action{
	{ActionMetricApply, KindMetric, []string{FieldOwner, FieldName, FieldDigest, FieldMetricType}, false},
	{ActionMetricRead, KindMetric, stored, true},
	{ActionMetricList, KindMetric, []string{FieldOwner}, false},
	{ActionGraderApply, KindGrader, []string{FieldOwner, FieldName, FieldDigest, FieldGraderKind, FieldModel, FieldAgent}, false},
	{ActionGraderRead, KindGrader, []string{FieldOwner, FieldName, FieldDigest, FieldGraderKind, FieldShared, FieldPinned}, true},
	{ActionGraderList, KindGrader, []string{FieldOwner}, false},
	{ActionTaskApply, KindTask, []string{FieldOwner, FieldName, FieldDigest, FieldWorkspaceKind, FieldRepository, FieldCommit, FieldPath, FieldImage}, false},
	{ActionTaskRead, KindTask, stored, true},
	{ActionTaskList, KindTask, []string{FieldOwner}, false},
	{ActionSuiteApply, KindSuite, []string{FieldOwner, FieldName, FieldDigest, FieldTaskCount, FieldMetricCount}, false},
	{ActionSuiteRead, KindSuite, stored, true},
	{ActionSuiteList, KindSuite, []string{FieldOwner}, false},
	{ActionTaxonomyApply, KindSuite, []string{FieldOwner, FieldName, FieldMetric, FieldVersion}, false},
	{ActionTaxonomyRead, KindSuite, []string{FieldOwner, FieldName, FieldMetric, FieldVersion, FieldShared, FieldPinned}, true},
	{ActionCandidateApply, KindCandidate, []string{FieldOwner, FieldName, FieldDigest, FieldCandidateKind, FieldAgent, FieldImage, FieldModel, FieldReasoning, FieldHarness, FieldCredentialAudiences}, false},
	{ActionCandidateRead, KindCandidate, []string{FieldOwner, FieldName, FieldDigest, FieldCandidateKind}, true},
	{ActionCandidateList, KindCandidate, []string{FieldOwner}, false},
	{ActionExperimentCreate, KindExperiment, []string{
		FieldOwner, FieldSuite, FieldSuiteOwner, FieldCandidates, FieldExecutors, FieldModels, FieldJudgeModels, FieldGraderKinds, FieldCredentialAudiences,
		FieldCells, FieldTrials, FieldTrialsTotal, FieldParallelism, FieldBudget, FieldBudgetCapUSDMicro, FieldReferenceCandidate,
		FieldBaseline, FieldBaselineOwner, FieldMode,
	}, true},
	{ActionExperimentRead, KindExperiment, []string{FieldOwner, FieldSuite, FieldState, FieldMode, FieldShared}, true},
	{ActionExperimentList, KindExperiment, []string{FieldOwner}, false},
	{ActionExperimentUpdate, KindExperiment, append(slices.Clone(experiment), FieldTrials, FieldTrialsTotal, FieldParallelism, FieldReferenceCandidate, FieldBaseline, FieldBaselineOwner), true},
	{ActionCellRetry, KindExperiment, append(slices.Clone(experiment), FieldCell, FieldCellState, FieldParkedReason), true},
	{ActionExperimentCancel, KindExperiment, []string{FieldOwner, FieldSuite, FieldState}, true},
	{ActionExperimentImport, KindExperiment, append(slices.Clone(experiment), FieldTask, FieldCandidate, FieldExecutor, FieldExecutorRef), true},
	{ActionTrialLease, KindExperiment, append(slices.Clone(experiment), FieldCandidate, FieldHarness, FieldLease), true},
	{ActionTrialSubmit, KindExperiment, append(slices.Clone(experiment), FieldCandidate, FieldHarness, FieldTrial, FieldLease, FieldOutcome), true},
	{ActionExperimentRun, KindExperiment, []string{FieldOwner, FieldCreatedBy, FieldState, FieldPurpose, FieldCell, FieldExecutor, FieldBudget}, true},
	{ActionBaselinePin, KindBaseline, []string{FieldOwner, FieldSuite, FieldSuiteOwner, FieldName, FieldExperiment, FieldCandidate}, false},
	{ActionBaselineRead, KindBaseline, []string{FieldOwner, FieldSuite, FieldSuiteOwner, FieldName, FieldShared}, true},
	{ActionBaselineList, KindBaseline, []string{FieldOwner, FieldSuite, FieldSuiteOwner}, false},
}

// vocabulary is the table as the shared contract reads it, built once. A
// table that does not build is a mistake in this file, which the package's
// tests find.
var vocabulary = func() authz.Vocabulary {
	actions := make([]authz.Action, len(table))
	for i, a := range table {
		actions[i] = authz.Action{Name: a.name, Kind: a.kind}
	}
	v, err := authz.NewVocabulary(Core, actions...)
	if err != nil {
		panic(err)
	}
	return v.WithLabels(map[string]string{
		KindMetric: "Metrics", KindGrader: "Graders", KindTask: "Tasks", KindSuite: "Suites",
		KindCandidate: "Candidates", KindExperiment: "Experiments", KindBaseline: "Baselines",
	})
}()

// Vocabulary is the whole action table, for an authorizer to validate
// against and for the conformance suite to drive a case per row from.
func Vocabulary() authz.Vocabulary { return vocabulary }

// Actions lists every action in the order of the table.
func Actions() []string {
	out := make([]string, len(table))
	for i, a := range table {
		out[i] = a.name
	}
	return out
}

// Kind is the resource kind an action acts on, and "" for a string that is
// no action of the table.
func Kind(name string) string {
	kind, _ := vocabulary.Kind(name)
	return kind
}

// Known reports whether name is an action of the table.
func Known(name string) bool { return vocabulary.Known(name) }

// Fields lists the fields an action's resource may carry beside its kind
// and id, each sent when it is known. The value is a fresh copy.
func Fields(name string) []string {
	for _, a := range table {
		if a.name == name {
			return slices.Clone(a.fields)
		}
	}
	return nil
}

// SendsID reports whether an action's resource carries an id, as
// resource.id: the id of the stored object it is about, or, on
// experiment.create, the id minted for the experiment before the question.
func SendsID(name string) bool {
	for _, a := range table {
		if a.name == name {
			return a.id
		}
	}
	return false
}

// ReasonSignInNotKept is the one reason of a deny that Examen answers with
// a code of its own rather than forbidden: an installation that keeps no
// sign-in of the caller it could act from for an experiment's later work
// refuses experiment.create with it, and the caller is answered 403
// sign_in_not_kept. A deny about a stored object is that object's 404
// whatever its reason.
const ReasonSignInNotKept = "sign_in_not_kept"
