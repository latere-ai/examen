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
	CodeInvalidRequest             = "invalid_request"
	CodeUnknownField               = "unknown_field"
	CodeMissingField               = "missing_field"
	CodeInvalidField               = "invalid_field"
	CodeInvalidCursor              = "invalid_cursor"
	CodeMissingToken               = "missing_token"
	CodeInvalidToken               = "invalid_token"
	CodeForbidden                  = "forbidden"
	CodeSignInNotKept              = "sign_in_not_kept"
	CodeNotFound                   = "not_found"
	CodeMetricNotFound             = "metric_not_found"
	CodeGraderNotFound             = "grader_not_found"
	CodeTaskNotFound               = "task_not_found"
	CodeSuiteNotFound              = "suite_not_found"
	CodeCandidateNotFound          = "candidate_not_found"
	CodeExperimentNotFound         = "experiment_not_found"
	CodeCellNotFound               = "cell_not_found"
	CodeTrialNotFound              = "trial_not_found"
	CodeMeasurementNotFound        = "measurement_not_found"
	CodeBaselineNotFound           = "baseline_not_found"
	CodeComparisonNotFound         = "comparison_not_found"
	CodeTaxonomyNotFound           = "taxonomy_not_found"
	CodeExperimentFinished         = "experiment_finished"
	CodeExperimentNotComplete      = "experiment_not_complete"
	CodeCellNotParked              = "cell_not_parked"
	CodeWrongMode                  = "wrong_mode"
	CodeCellFull                   = "cell_full"
	CodeAlreadyImported            = "already_imported"
	CodeLeaseExpired               = "lease_expired"
	CodeLeaseNotHeld               = "lease_not_held"
	CodeIdempotencyKeyReused       = "idempotency_key_reused"
	CodeRequestInProgress          = "request_in_progress"
	CodePreconditionFailed         = "precondition_failed"
	CodeRequestTooLarge            = "request_too_large"
	CodeUnpinnedReference          = "unpinned_reference"
	CodeReferenceNotFound          = "reference_not_found"
	CodeLimitExceeded              = "limit_exceeded"
	CodeSuiteMismatch              = "suite_mismatch"
	CodeNotInExperiment            = "not_in_experiment"
	CodeProvenanceMismatch         = "provenance_mismatch"
	CodeBudgetRefused              = "budget_refused"
	CodeCoreNotConfigured          = "core_not_configured"
	CodeCredentialAudienceReserved = "credential_audience_reserved"
	CodeUnresolvedReference        = "unresolved_reference"
	CodeExecutorUnavailable        = "executor_unavailable"
	CodeTooManyStreams             = "too_many_streams"
	CodeRateLimited                = "rate_limited"
	CodeInternal                   = "internal"
	CodeCoreUnavailable            = "core_unavailable"
	CodeAuthorizerUnavailable      = "authorizer_unavailable"
	CodeOwnerTokensUnavailable     = "owner_tokens_unavailable"
	CodeUnavailable                = "unavailable"
)

// table is the error table of specs/003-api.md: each code's status,
// the one sentence a person reads for it, and whether the same request
// may succeed later unchanged unless the instance says otherwise.
var table = map[string]kind{
	CodeInvalidRequest:             {http.StatusBadRequest, "The request could not be read. Send a JSON body that matches the API reference.", false},
	CodeUnknownField:               {http.StatusBadRequest, "The request contains a field this service does not accept.", false},
	CodeMissingField:               {http.StatusBadRequest, "The request is missing a required field.", false},
	CodeInvalidField:               {http.StatusBadRequest, "A value in the request is not allowed.", false},
	CodeInvalidCursor:              {http.StatusBadRequest, "This page link is no longer valid. Start again from the first page.", false},
	CodeMissingToken:               {http.StatusUnauthorized, "Sign in to continue.", false},
	CodeInvalidToken:               {http.StatusUnauthorized, "Your sign-in has expired or is not valid. Sign in again.", false},
	CodeForbidden:                  {http.StatusForbidden, "You do not have permission to do this.", false},
	CodeSignInNotKept:              {http.StatusForbidden, "Your sign-in cannot be kept for work that runs after you leave. Start this from a client that allows it.", false},
	CodeNotFound:                   {http.StatusNotFound, "There is nothing at this address.", false},
	CodeMetricNotFound:             {http.StatusNotFound, "This metric does not exist, or you do not have access to it.", false},
	CodeGraderNotFound:             {http.StatusNotFound, "This grader does not exist, or you do not have access to it.", false},
	CodeTaskNotFound:               {http.StatusNotFound, "This task does not exist, or you do not have access to it.", false},
	CodeSuiteNotFound:              {http.StatusNotFound, "This suite does not exist, or you do not have access to it.", false},
	CodeCandidateNotFound:          {http.StatusNotFound, "This candidate does not exist, or you do not have access to it.", false},
	CodeExperimentNotFound:         {http.StatusNotFound, "This experiment does not exist, or you do not have access to it.", false},
	CodeCellNotFound:               {http.StatusNotFound, "This cell does not exist, or you do not have access to it.", false},
	CodeTrialNotFound:              {http.StatusNotFound, "This trial does not exist, or you do not have access to it.", false},
	CodeMeasurementNotFound:        {http.StatusNotFound, "This measurement does not exist, or you do not have access to it.", false},
	CodeBaselineNotFound:           {http.StatusNotFound, "This baseline does not exist, or you do not have access to it.", false},
	CodeComparisonNotFound:         {http.StatusNotFound, "This comparison does not exist, or you do not have access to it.", false},
	CodeTaxonomyNotFound:           {http.StatusNotFound, "This taxonomy does not exist, or you do not have access to it.", false},
	CodeExperimentFinished:         {http.StatusConflict, "This experiment has already finished.", false},
	CodeExperimentNotComplete:      {http.StatusConflict, "This experiment has not completed yet.", true},
	CodeCellNotParked:              {http.StatusConflict, "This cell is not waiting to be retried.", false},
	CodeWrongMode:                  {http.StatusConflict, "This experiment's mode does not allow this request.", false},
	CodeCellFull:                   {http.StatusConflict, "This cell already has all of its trials.", false},
	CodeAlreadyImported:            {http.StatusConflict, "This session or sandbox is already part of this experiment.", false},
	CodeLeaseExpired:               {http.StatusConflict, "This lease has expired. Lease the trial again.", false},
	CodeLeaseNotHeld:               {http.StatusConflict, "This trial is not held under the lease given.", false},
	CodeIdempotencyKeyReused:       {http.StatusConflict, "This idempotency key was already used for a different request.", false},
	CodeRequestInProgress:          {http.StatusConflict, "The same request is still being processed. Try again shortly.", true},
	CodePreconditionFailed:         {http.StatusPreconditionFailed, "This changed since you last read it. Read it again and retry.", false},
	CodeRequestTooLarge:            {http.StatusRequestEntityTooLarge, "The request is too large.", false},
	CodeUnpinnedReference:          {http.StatusUnprocessableEntity, "A reference must name an exact version.", false},
	CodeReferenceNotFound:          {http.StatusUnprocessableEntity, "Something this request refers to does not exist, or you do not have access to it.", false},
	CodeLimitExceeded:              {http.StatusUnprocessableEntity, "This request is larger than this service allows.", false},
	CodeSuiteMismatch:              {http.StatusUnprocessableEntity, "These objects belong to different suites.", false},
	CodeNotInExperiment:            {http.StatusUnprocessableEntity, "The task or candidate named is not part of this experiment.", false},
	CodeProvenanceMismatch:         {http.StatusUnprocessableEntity, "The imported trial was not run with this cell's configuration.", false},
	CodeBudgetRefused:              {http.StatusUnprocessableEntity, "The budget named cannot be used for this experiment.", false},
	CodeCoreNotConfigured:          {http.StatusUnprocessableEntity, "This installation is not set up to run this kind of work.", false},
	CodeCredentialAudienceReserved: {http.StatusUnprocessableEntity, "A program cannot be given a credential for a service that runs or grades it.", false},
	CodeUnresolvedReference:        {http.StatusUnprocessableEntity, "Something this experiment uses could not be found.", false},
	CodeExecutorUnavailable:        {http.StatusUnprocessableEntity, "This kind of candidate cannot run here yet.", false},
	CodeTooManyStreams:             {http.StatusTooManyRequests, "Too many progress streams are open. Close one and try again.", true},
	CodeRateLimited:                {http.StatusTooManyRequests, "Too many requests. Try again shortly.", true},
	CodeInternal:                   {http.StatusInternalServerError, "Something went wrong. Try again later.", true},
	CodeCoreUnavailable:            {http.StatusBadGateway, "A service this one depends on is not responding. Try again later.", true},
	CodeAuthorizerUnavailable:      {http.StatusServiceUnavailable, "Permissions cannot be checked right now. Try again later.", true},
	CodeOwnerTokensUnavailable:     {http.StatusServiceUnavailable, "This service cannot act on your behalf right now.", true},
	CodeUnavailable:                {http.StatusServiceUnavailable, "The service is temporarily unavailable. Try again later.", true},
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
	CodeTaxonomyNotFound,
	CodeExperimentFinished,
	CodeExperimentNotComplete,
	CodeCellNotParked,
	CodeWrongMode,
	CodeCellFull,
	CodeAlreadyImported,
	CodeLeaseExpired,
	CodeLeaseNotHeld,
	CodeIdempotencyKeyReused,
	CodeRequestInProgress,
	CodePreconditionFailed,
	CodeRequestTooLarge,
	CodeUnpinnedReference,
	CodeReferenceNotFound,
	CodeLimitExceeded,
	CodeSuiteMismatch,
	CodeNotInExperiment,
	CodeProvenanceMismatch,
	CodeBudgetRefused,
	CodeExecutorUnavailable,
	CodeCoreNotConfigured,
	CodeCredentialAudienceReserved,
	CodeUnresolvedReference,
	CodeTooManyStreams,
	CodeRateLimited,
	CodeInternal,
	CodeCoreUnavailable,
	CodeAuthorizerUnavailable,
	CodeOwnerTokensUnavailable,
	CodeUnavailable,
}
