// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apierr

import "net/http"

// kind is one row of the error table: the status of a code, the one
// sentence a person reads for it, and whether the same request may succeed
// later unchanged unless the instance says otherwise.
type kind struct {
	status    int
	message   string
	retryable bool
}

// The codes of specs/003-api.md, in the order of its table.
const (
	CodeInvalidRequest         = "invalid_request"
	CodeUnknownField           = "unknown_field"
	CodeMissingField           = "missing_field"
	CodeInvalidField           = "invalid_field"
	CodeInvalidCursor          = "invalid_cursor"
	CodeMissingToken           = "missing_token"
	CodeInvalidToken           = "invalid_token"
	CodeForbidden              = "forbidden"
	CodeSignInNotKept          = "sign_in_not_kept"
	CodeNotFound               = "not_found"
	CodeMetricNotFound         = "metric_not_found"
	CodeGraderNotFound         = "grader_not_found"
	CodeTaskNotFound           = "task_not_found"
	CodeSuiteNotFound          = "suite_not_found"
	CodeCandidateNotFound      = "candidate_not_found"
	CodeExperimentNotFound     = "experiment_not_found"
	CodeCellNotFound           = "cell_not_found"
	CodeTrialNotFound          = "trial_not_found"
	CodeMeasurementNotFound    = "measurement_not_found"
	CodeBaselineNotFound       = "baseline_not_found"
	CodeComparisonNotFound     = "comparison_not_found"
	CodeBarNotFound            = "bar_not_found"
	CodeEvaluationNotFound     = "evaluation_not_found"
	CodeExportNotFound         = "export_not_found"
	CodeShareNotFound          = "share_not_found"
	CodeReportNotFound         = "report_not_found"
	CodeTaxonomyNotFound       = "taxonomy_not_found"
	CodePublicationNotFound    = "publication_not_found"
	CodeSubmissionNotFound     = "submission_not_found"
	CodeAnnotationNotFound     = "annotation_not_found"
	CodeImmutableField         = "immutable_field"
	CodeExperimentFinished     = "experiment_finished"
	CodeExperimentNotComplete  = "experiment_not_complete"
	CodeExperimentCanceled     = "experiment_canceled"
	CodeExperimentNotFinished  = "experiment_not_finished"
	CodeExperimentIsBaseline   = "experiment_is_baseline"
	CodeExperimentAdoptedFrom  = "experiment_adopted_from"
	CodeCellNotParked          = "cell_not_parked"
	CodeComparisonNotComplete  = "comparison_not_complete"
	CodeNarrativeHasFindings   = "narrative_has_findings"
	CodeWrongMode              = "wrong_mode"
	CodeCellFull               = "cell_full"
	CodeSourceNotFinished      = "source_not_finished"
	CodeAlreadyImported        = "already_imported"
	CodeAlreadyAnnotated       = "already_annotated"
	CodeLeaseExpired           = "lease_expired"
	CodeLeaseNotHeld           = "lease_not_held"
	CodePublicationExists      = "publication_exists"
	CodeVersionNotPublished    = "version_not_published"
	CodePublicationListed      = "publication_listed"
	CodeSubmissionsClosed      = "submissions_closed"
	CodeReportNotShared        = "report_not_shared"
	CodeSubmissionDecided      = "submission_decided"
	CodeIdempotencyKeyReused   = "idempotency_key_reused"
	CodeRequestInProgress      = "request_in_progress"
	CodePreconditionFailed     = "precondition_failed"
	CodePublicationWithdrawn   = "publication_withdrawn"
	CodePublicationPaused      = "publication_paused"
	CodeRequestTooLarge        = "request_too_large"
	CodeUnpinnedReference      = "unpinned_reference"
	CodeReferenceNotFound      = "reference_not_found"
	CodeLimitExceeded          = "limit_exceeded"
	CodeSuiteMismatch          = "suite_mismatch"
	CodeNoReference            = "no_reference"
	CodeNotInExperiment        = "not_in_experiment"
	CodeProvenanceMismatch     = "provenance_mismatch"
	CodeMetricNotDeclared      = "metric_not_declared"
	CodeBudgetRefused          = "budget_refused"
	CodeCoreNotConfigured      = "core_not_configured"
	CodeImageNotAdmitted       = "image_not_admitted"
	CodeInstructionRequired    = "instruction_required"
	CodeUnresolvedReference    = "unresolved_reference"
	CodeTooManyStreams         = "too_many_streams"
	CodeRateLimited            = "rate_limited"
	CodeInternal               = "internal"
	CodeCoreUnavailable        = "core_unavailable"
	CodeAuthorizerUnavailable  = "authorizer_unavailable"
	CodeOwnerTokensUnavailable = "owner_tokens_unavailable"
	CodeUnavailable            = "unavailable"
	CodeCoreTimeout            = "core_timeout"
)

// table is the error table of specs/003-api.md: each code's status,
// the one sentence a person reads for it, and whether the same request
// may succeed later unchanged unless the instance says otherwise.
var table = map[string]kind{
	CodeInvalidRequest:         {http.StatusBadRequest, "The request could not be read. Send a JSON body that matches the API reference.", false},
	CodeUnknownField:           {http.StatusBadRequest, "The request contains a field this service does not accept.", false},
	CodeMissingField:           {http.StatusBadRequest, "The request is missing a required field.", false},
	CodeInvalidField:           {http.StatusBadRequest, "A value in the request is not allowed.", false},
	CodeInvalidCursor:          {http.StatusBadRequest, "This page link is no longer valid. Start again from the first page.", false},
	CodeMissingToken:           {http.StatusUnauthorized, "Sign in to continue.", false},
	CodeInvalidToken:           {http.StatusUnauthorized, "Your sign-in has expired or is not valid. Sign in again.", false},
	CodeForbidden:              {http.StatusForbidden, "You do not have permission to do this.", false},
	CodeSignInNotKept:          {http.StatusForbidden, "Your sign-in cannot be kept for work that runs after you leave. Start this from a client that allows it.", false},
	CodeNotFound:               {http.StatusNotFound, "There is nothing at this address.", false},
	CodeMetricNotFound:         {http.StatusNotFound, "This metric does not exist, or you do not have access to it.", false},
	CodeGraderNotFound:         {http.StatusNotFound, "This grader does not exist, or you do not have access to it.", false},
	CodeTaskNotFound:           {http.StatusNotFound, "This task does not exist, or you do not have access to it.", false},
	CodeSuiteNotFound:          {http.StatusNotFound, "This suite does not exist, or you do not have access to it.", false},
	CodeCandidateNotFound:      {http.StatusNotFound, "This candidate does not exist, or you do not have access to it.", false},
	CodeExperimentNotFound:     {http.StatusNotFound, "This experiment does not exist, or you do not have access to it.", false},
	CodeCellNotFound:           {http.StatusNotFound, "This cell does not exist, or you do not have access to it.", false},
	CodeTrialNotFound:          {http.StatusNotFound, "This trial does not exist, or you do not have access to it.", false},
	CodeMeasurementNotFound:    {http.StatusNotFound, "This measurement does not exist, or you do not have access to it.", false},
	CodeBaselineNotFound:       {http.StatusNotFound, "This baseline does not exist, or you do not have access to it.", false},
	CodeComparisonNotFound:     {http.StatusNotFound, "This comparison does not exist, or you do not have access to it.", false},
	CodeBarNotFound:            {http.StatusNotFound, "This bar does not exist, or you do not have access to it.", false},
	CodeEvaluationNotFound:     {http.StatusNotFound, "This evaluation does not exist, or you do not have access to it.", false},
	CodeExportNotFound:         {http.StatusNotFound, "This export does not exist, or you do not have access to it.", false},
	CodeShareNotFound:          {http.StatusNotFound, "This share does not exist, or you do not have access to it.", false},
	CodeReportNotFound:         {http.StatusNotFound, "This report does not exist, or you do not have access to it.", false},
	CodeTaxonomyNotFound:       {http.StatusNotFound, "This taxonomy does not exist, or you do not have access to it.", false},
	CodePublicationNotFound:    {http.StatusNotFound, "This item does not exist, or you do not have access to it.", false},
	CodeSubmissionNotFound:     {http.StatusNotFound, "This submission does not exist, or you do not have access to it.", false},
	CodeAnnotationNotFound:     {http.StatusNotFound, "This item is not in the annotation queue, or you do not have access to it.", false},
	CodeImmutableField:         {http.StatusConflict, "This field can no longer be changed.", false},
	CodeExperimentFinished:     {http.StatusConflict, "This experiment has already finished.", false},
	CodeExperimentNotComplete:  {http.StatusConflict, "This experiment has not completed yet.", true},
	CodeExperimentCanceled:     {http.StatusConflict, "This experiment was canceled.", false},
	CodeExperimentNotFinished:  {http.StatusConflict, "This experiment is still running. Cancel it first.", false},
	CodeExperimentIsBaseline:   {http.StatusConflict, "This experiment is a baseline. Move or delete the baseline first.", false},
	CodeExperimentAdoptedFrom:  {http.StatusConflict, "Another experiment uses trials of this one.", false},
	CodeCellNotParked:          {http.StatusConflict, "This cell is not waiting to be retried.", false},
	CodeComparisonNotComplete:  {http.StatusConflict, "This comparison is not ready yet. Try again after it is computed.", true},
	CodeNarrativeHasFindings:   {http.StatusConflict, "This narrative has problems to fix before it can be released.", false},
	CodeWrongMode:              {http.StatusConflict, "This experiment's mode does not allow this request.", false},
	CodeCellFull:               {http.StatusConflict, "This cell already has all of its trials.", false},
	CodeSourceNotFinished:      {http.StatusConflict, "This session or sandbox has not finished. Import it after it ends.", true},
	CodeAlreadyImported:        {http.StatusConflict, "This session or sandbox is already part of this experiment.", false},
	CodeAlreadyAnnotated:       {http.StatusConflict, "You have already annotated this trial.", false},
	CodeLeaseExpired:           {http.StatusConflict, "This lease has expired. Lease the trial again.", false},
	CodeLeaseNotHeld:           {http.StatusConflict, "This trial is not held under the lease given.", false},
	CodePublicationExists:      {http.StatusConflict, "A publication with this name already exists.", false},
	CodeVersionNotPublished:    {http.StatusConflict, "This version is not published.", false},
	CodePublicationListed:      {http.StatusConflict, "This item is listed. End its listing first.", false},
	CodeSubmissionsClosed:      {http.StatusConflict, "This benchmark does not take submissions from others.", false},
	CodeReportNotShared:        {http.StatusConflict, "Share the report by link before submitting it.", false},
	CodeSubmissionDecided:      {http.StatusConflict, "This submission was already decided.", false},
	CodeIdempotencyKeyReused:   {http.StatusConflict, "This idempotency key was already used for a different request.", false},
	CodeRequestInProgress:      {http.StatusConflict, "The same request is still being processed. Try again shortly.", true},
	CodePreconditionFailed:     {http.StatusPreconditionFailed, "This changed since you last read it. Read it again and retry.", false},
	CodePublicationWithdrawn:   {http.StatusGone, "This item was withdrawn by its owner.", false},
	CodePublicationPaused:      {http.StatusGone, "This item is not available right now.", false},
	CodeRequestTooLarge:        {http.StatusRequestEntityTooLarge, "The request is too large.", false},
	CodeUnpinnedReference:      {http.StatusUnprocessableEntity, "A reference must name an exact version.", false},
	CodeReferenceNotFound:      {http.StatusUnprocessableEntity, "Something this request refers to does not exist, or you do not have access to it.", false},
	CodeLimitExceeded:          {http.StatusUnprocessableEntity, "This request is larger than this service allows.", false},
	CodeSuiteMismatch:          {http.StatusUnprocessableEntity, "These objects belong to different suites.", false},
	CodeNoReference:            {http.StatusUnprocessableEntity, "There is nothing to compare this candidate with.", false},
	CodeNotInExperiment:        {http.StatusUnprocessableEntity, "The task or candidate named is not part of this experiment.", false},
	CodeProvenanceMismatch:     {http.StatusUnprocessableEntity, "The imported trial was not run with this cell's configuration.", false},
	CodeMetricNotDeclared:      {http.StatusUnprocessableEntity, "This grader does not produce one of the metrics given.", false},
	CodeBudgetRefused:          {http.StatusUnprocessableEntity, "The budget named cannot be used for this experiment.", false},
	CodeCoreNotConfigured:      {http.StatusUnprocessableEntity, "This installation is not set up to run this kind of work.", false},
	CodeImageNotAdmitted:       {http.StatusUnprocessableEntity, "This image is not allowed to run here.", false},
	CodeInstructionRequired:    {http.StatusUnprocessableEntity, "An agent candidate needs every task to have an instruction.", false},
	CodeUnresolvedReference:    {http.StatusUnprocessableEntity, "Something this experiment uses could not be found.", false},
	CodeTooManyStreams:         {http.StatusTooManyRequests, "Too many progress streams are open. Close one and try again.", true},
	CodeRateLimited:            {http.StatusTooManyRequests, "Too many requests. Try again shortly.", true},
	CodeInternal:               {http.StatusInternalServerError, "Something went wrong. Try again later.", true},
	CodeCoreUnavailable:        {http.StatusBadGateway, "A service this one depends on is not responding. Try again later.", true},
	CodeAuthorizerUnavailable:  {http.StatusServiceUnavailable, "Permissions cannot be checked right now. Try again later.", true},
	CodeOwnerTokensUnavailable: {http.StatusServiceUnavailable, "This service cannot act on your behalf right now.", true},
	CodeUnavailable:            {http.StatusServiceUnavailable, "The service is temporarily unavailable. Try again later.", true},
	CodeCoreTimeout:            {http.StatusGatewayTimeout, "A service this one depends on took too long to respond. Try again later.", true},
}

// order is the codes in the order of the table.
var order = []string{
	CodeInvalidRequest,
	CodeUnknownField,
	CodeMissingField,
	CodeInvalidField,
	CodeInvalidCursor,
	CodeMissingToken,
	CodeInvalidToken,
	CodeForbidden,
	CodeSignInNotKept,
	CodeNotFound,
	CodeMetricNotFound,
	CodeGraderNotFound,
	CodeTaskNotFound,
	CodeSuiteNotFound,
	CodeCandidateNotFound,
	CodeExperimentNotFound,
	CodeCellNotFound,
	CodeTrialNotFound,
	CodeMeasurementNotFound,
	CodeBaselineNotFound,
	CodeComparisonNotFound,
	CodeBarNotFound,
	CodeEvaluationNotFound,
	CodeExportNotFound,
	CodeShareNotFound,
	CodeReportNotFound,
	CodeTaxonomyNotFound,
	CodePublicationNotFound,
	CodeSubmissionNotFound,
	CodeAnnotationNotFound,
	CodeImmutableField,
	CodeExperimentFinished,
	CodeExperimentNotComplete,
	CodeExperimentCanceled,
	CodeExperimentNotFinished,
	CodeExperimentIsBaseline,
	CodeExperimentAdoptedFrom,
	CodeCellNotParked,
	CodeComparisonNotComplete,
	CodeNarrativeHasFindings,
	CodeWrongMode,
	CodeCellFull,
	CodeSourceNotFinished,
	CodeAlreadyImported,
	CodeAlreadyAnnotated,
	CodeLeaseExpired,
	CodeLeaseNotHeld,
	CodePublicationExists,
	CodeVersionNotPublished,
	CodePublicationListed,
	CodeSubmissionsClosed,
	CodeReportNotShared,
	CodeSubmissionDecided,
	CodeIdempotencyKeyReused,
	CodeRequestInProgress,
	CodePreconditionFailed,
	CodePublicationWithdrawn,
	CodePublicationPaused,
	CodeRequestTooLarge,
	CodeUnpinnedReference,
	CodeReferenceNotFound,
	CodeLimitExceeded,
	CodeSuiteMismatch,
	CodeNoReference,
	CodeNotInExperiment,
	CodeProvenanceMismatch,
	CodeMetricNotDeclared,
	CodeBudgetRefused,
	CodeCoreNotConfigured,
	CodeImageNotAdmitted,
	CodeInstructionRequired,
	CodeUnresolvedReference,
	CodeTooManyStreams,
	CodeRateLimited,
	CodeInternal,
	CodeCoreUnavailable,
	CodeAuthorizerUnavailable,
	CodeOwnerTokensUnavailable,
	CodeUnavailable,
	CodeCoreTimeout,
}
