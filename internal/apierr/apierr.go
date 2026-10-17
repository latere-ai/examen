// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package apierr is the error envelope of specs/003-api.md. An error has one
// code to branch on, one fixed sentence a person reads in message, the same
// for every occurrence of the code, and the developer detail of its instance
// in details, apart. message never carries what details carries.
//
//	{"error": {"code": "...", "message": "...", "details": {"detail": "...", "retryable": false}}}
//
// An error nobody classified is internal: its text is logged and never
// answered.
package apierr

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"time"

	"latere.ai/x/pkg/httpjson"

	"latere.ai/x/examen/internal/limits"
)

// Status is the status of a code, and 500 for a code the table lacks.
func Status(code string) int {
	if k, ok := table[code]; ok {
		return k.status
	}
	return table[CodeInternal].status
}

// Message is the fixed sentence a person reads for a code, and "" for a code
// the table lacks.
func Message(code string) string { return table[code].message }

// Codes lists every code of the table, in its order.
func Codes() []string { return slices.Clone(order) }

// Error is an error a route answers with: one code of the table, and the
// developer detail of this instance. The sentence a person reads is the
// code's and never built from the instance.
type Error struct {
	Code string
	// Detail is details.detail: the operation, the object, the observed and
	// the expected value, and the error from the layer below, for a
	// developer. Every error but internal carries one.
	Detail string
	// Field is the member, parameter or header the error is about, as a
	// dotted path with indexes, such as candidates[1].
	Field string
	// Constant is the limit of spec 002 a request exceeds, on
	// limit_exceeded.
	Constant string
	// Reason is one snake_case word for this instance: the verifier's word,
	// the authorizer's deny reason, or the limit's member.
	Reason string
	// Figure is the figure a limit holds, on a forbidden a limit made.
	Figure *float64
	// State is the object's state, on a conflict with it.
	State string
	// Fields are the tuple fields that differ or are not pinned, on
	// provenance_mismatch.
	Fields []string
	// Core is the core an error is about: topos, cella, lux, arca or origo.
	Core string
	// Retryable, when set, overrides the code's own.
	Retryable *bool
	// RetryAfter is how long a 429 asks the caller to wait, sent in whole
	// seconds as Retry-After.
	RetryAfter time.Duration
	// cause is kept for the log of an unclassified error and is never
	// rendered.
	cause error
}

func (e *Error) Error() string {
	s := "apierr: " + e.Code
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	if e.Field != "" {
		s += " (" + e.Field + ")"
	}
	return s
}

// Unwrap is the cause.
func (e *Error) Unwrap() error { return e.cause }

// Cause is the error an unclassified error was made from, for its log, and
// e itself otherwise.
func (e *Error) Cause() error {
	if e.cause != nil {
		return e.cause
	}
	return e
}

// New builds an error of a code with its developer detail.
func New(code, detail string) *Error { return &Error{Code: code, Detail: detail} }

// Newf builds an error of a code with a formatted developer detail.
func Newf(code, format string, args ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// OnField builds an error about one member or parameter of the request.
func OnField(code, field, detail string) *Error {
	return &Error{Code: code, Field: field, Detail: detail}
}

// Exceeds builds a limit_exceeded about the constant of spec 002 a request
// exceeds.
func Exceeds(constant, field, detail string) *Error {
	return &Error{Code: CodeLimitExceeded, Constant: constant, Field: field, Detail: detail}
}

// Internal is an error nobody classified. Its text is logged and never
// answered.
func Internal(err error) *Error { return &Error{Code: CodeInternal, cause: err} }

// Denied is the refusal of a deny that names no stored object: 403
// forbidden with the authorizer's reason as details.reason, or, when the
// reason is one of the codes the table answers with 403 other than
// forbidden, that code (specs/004-identity-and-authorization.md). A deny
// about a stored object is the 404 of its kind instead, whatever its
// reason, and is not built here.
func Denied(action, reason string) *Error {
	code := CodeForbidden
	if reason != CodeForbidden && Status(reason) == http.StatusForbidden && Message(reason) != "" {
		code = reason
	}
	return &Error{Code: code, Reason: reason, Detail: action + ": the authorizer denied the request: " + reason}
}

// Envelope renders an error as the status and the body a route answers.
func (e *Error) Envelope() (int, httpjson.Error) {
	k, ok := table[e.Code]
	if !ok || e.Code == CodeInternal {
		k = table[CodeInternal]
		return k.status, httpjson.Error{Code: CodeInternal, Message: k.message, Details: map[string]any{"retryable": k.retryable}}
	}
	details := map[string]any{}
	detail := e.Detail
	if detail == "" {
		// Every error but internal carries a detail; one built without
		// says which code it is, which is still the developer's.
		detail = e.Code
	}
	details["detail"] = detail
	if e.Field != "" {
		details["field"] = e.Field
	}
	if e.Constant != "" {
		details["constant"] = e.Constant
	}
	if e.Reason != "" {
		details["reason"] = e.Reason
	}
	if e.Figure != nil {
		details["figure"] = *e.Figure
	}
	if e.State != "" {
		details["state"] = e.State
	}
	if len(e.Fields) > 0 {
		details["fields"] = slices.Clone(e.Fields)
	}
	if e.Core != "" {
		details["core"] = e.Core
	}
	retryable := k.retryable
	if e.Retryable != nil {
		retryable = *e.Retryable
	}
	details["retryable"] = retryable
	return k.status, httpjson.Error{Code: e.Code, Message: k.message, Details: details}
}

// Classify is the route error of any error.
func Classify(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Internal(err)
}

// Write answers an error in the envelope, with Retry-After in whole
// seconds on every 429, and returns the classified error so the caller
// logs one nobody classified.
func Write(w http.ResponseWriter, err error) *Error {
	e := Classify(err)
	status, body := e.Envelope()
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter(e)))
	}
	httpjson.WriteError(w, status, body)
	return e
}

// retryAfter is the wait a 429 names, in whole seconds rounded up and at
// least one: the instance's, or for too_many_streams STREAM_POLL_INTERVAL,
// since a stream may close at any moment.
func retryAfter(e *Error) int {
	wait := e.RetryAfter
	if wait <= 0 && e.Code == CodeTooManyStreams {
		wait = limits.StreamPollInterval
	}
	return max(1, int(math.Ceil(wait.Seconds())))
}

// The parts of a URL in an error's text that may hold a credential: a
// transport error can hold the whole URL it was asked for, a query with a
// token in it, and a database error the user and password of its address.
var (
	userinfo = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^\s/?#@"']*@`)
	query    = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^\s?#"']*)[?#][^\s"']*`)
)

// CutQueries removes the user part, the query and the fragment of every
// URL in a text, so a logged error keeps the scheme, the host and the path
// alone.
func CutQueries(s string) string {
	return query.ReplaceAllString(userinfo.ReplaceAllString(s, "$1"), "$1")
}

// LogText is the text of an unclassified error as it is logged.
func LogText(err error) string {
	if err == nil {
		return ""
	}
	return CutQueries(err.Error())
}
