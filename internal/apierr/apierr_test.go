// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package apierr

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// specRow is one row of the error table of specs/003-api.md.
type specRow struct {
	status  int
	code    string
	message string
}

func specTable(t *testing.T) []specRow {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "specs", "003-api.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	var out []specRow
	in := false
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		switch {
		case strings.HasPrefix(line, "These are every code a client branches on"):
			in = true
		case in && strings.HasPrefix(line, "| ") && line[2] >= '1' && line[2] <= '5':
			cells := strings.Split(strings.Trim(line, "|"), "|")
			status, err := strconv.Atoi(strings.TrimSpace(cells[0]))
			if err != nil {
				t.Fatalf("a status that is not a number: %q", line)
			}
			out = append(out, specRow{status, strings.Trim(strings.TrimSpace(cells[1]), "`"), strings.TrimSpace(cells[3])})
		case in && line != "" && !strings.HasPrefix(line, "|"):
			in = false
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The table is spec 003's, row for row: the code, its status and its fixed
// sentence, in the spec's order.
func TestTableIsTheSpecs(t *testing.T) {
	rows := specTable(t)
	if len(rows) < 70 {
		t.Fatalf("read %d rows of the table; the reader is wrong", len(rows))
	}
	var codes []string
	for _, r := range rows {
		codes = append(codes, r.code)
		if Status(r.code) != r.status || Message(r.code) != r.message {
			t.Errorf("%s: the table has %d %q, the spec %d %q", r.code, Status(r.code), Message(r.code), r.status, r.message)
		}
	}
	if !slices.Equal(codes, Codes()) {
		t.Fatalf("the spec lists\n  %v\nthe package\n  %v", codes, Codes())
	}
	if len(table) != len(order) {
		t.Fatalf("the table holds %d codes and the order %d", len(table), len(order))
	}
	if Status("no_such_code") != http.StatusInternalServerError || Message("no_such_code") != "" {
		t.Fatal("a code the table lacks")
	}
}

type body struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

func write(t *testing.T, err error) (*httptest.ResponseRecorder, body, *Error) {
	t.Helper()
	rec := httptest.NewRecorder()
	e := Write(rec, err)
	var b body
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return rec, b, e
}

// An error answers its code with the code's sentence and the instance's
// details apart, every member of details in its place.
func TestEnvelope(t *testing.T) {
	figure := 8.0
	no := false
	e := &Error{
		Code: CodeForbidden, Detail: "experiment create: parallelism 16 above the allow's 8", Field: "parallelism",
		Reason: "max_parallelism", Figure: &figure, State: "running", Fields: []string{"model"}, Core: "lux",
		Constant: "MAX_PARALLELISM", Retryable: &no,
	}
	rec, b, _ := write(t, e)
	d := b.Error.Details
	switch {
	case rec.Code != http.StatusForbidden, b.Error.Code != CodeForbidden, b.Error.Message != Message(CodeForbidden):
		t.Fatalf("%d %+v", rec.Code, b)
	case d["detail"] != e.Detail, d["field"] != "parallelism", d["reason"] != "max_parallelism", d["figure"] != 8.0,
		d["state"] != "running", d["core"] != "lux", d["constant"] != "MAX_PARALLELISM", d["retryable"] != false:
		t.Fatalf("details %v", d)
	case fmt.Sprint(d["fields"]) != "[model]":
		t.Fatalf("fields %v", d["fields"])
	case strings.Contains(b.Error.Message, "parallelism"):
		t.Fatal("the sentence carries the instance")
	}
	if rec.Header().Get("Retry-After") != "" {
		t.Fatal("a 403 carries Retry-After")
	}
	// A code's own retryable, and a detail every error but internal has.
	_, b, _ = write(t, &Error{Code: CodeAuthorizerUnavailable})
	if b.Error.Details["retryable"] != true || b.Error.Details["detail"] != CodeAuthorizerUnavailable {
		t.Fatalf("defaults %v", b.Error.Details)
	}
	_, b, _ = write(t, Exceeds("MAX_CELLS_PER_EXPERIMENT", "candidates", "2400 cells"))
	if b.Error.Code != CodeLimitExceeded || b.Error.Details["constant"] != "MAX_CELLS_PER_EXPERIMENT" || b.Error.Details["field"] != "candidates" {
		t.Fatalf("limit %+v", b)
	}
	if got := Newf(CodeInvalidField, "limit %d above %d", 300, 200).Detail; got != "limit 300 above 200" {
		t.Fatalf("Newf %q", got)
	}
}

// An error nobody classified is internal, with no detail, its cause kept
// for the log alone; an error of a code the table lacks is the same.
func TestInternal(t *testing.T) {
	cause := errors.New("a cause with https://api.example.com/x?token=t in it")
	rec, b, e := write(t, fmt.Errorf("wrapped: %w", cause))
	if rec.Code != http.StatusInternalServerError || b.Error.Code != CodeInternal || !errors.Is(e.Cause(), cause) || !errors.Is(e, cause) {
		t.Fatalf("%d %+v %v", rec.Code, b, e)
	}
	if _, has := b.Error.Details["detail"]; has || b.Error.Details["retryable"] != true || strings.Contains(rec.Body.String(), "token=t") {
		t.Fatalf("internal answered %s", rec.Body.String())
	}
	if LogText(e.Cause()) != "wrapped: a cause with https://api.example.com/x in it" || LogText(nil) != "" {
		t.Fatalf("log text %q", LogText(e.Cause()))
	}
	for in, want := range map[string]string{
		"dial postgres://examen:pw@db.example.com:5432/x?sslmode=disable: refused": "dial postgres://db.example.com:5432/x refused",
		"get https://user@api.example.com/a#frag":                                  "get https://api.example.com/a",
		"no url here, user@example.com":                                            "no url here, user@example.com",
	} {
		if got := CutQueries(in); got != want {
			t.Errorf("CutQueries(%q) = %q, want %q", in, got, want)
		}
	}
	_, b, _ = write(t, &Error{Code: "no_such_code", Detail: "x"})
	if b.Error.Code != CodeInternal {
		t.Fatalf("an unknown code answered %+v", b)
	}
	known := New(CodeNotFound, "no route")
	if !errors.Is(known.Cause(), known) {
		t.Fatal("a classified error is not its own cause")
	}
	if got := Classify(fmt.Errorf("wrapped: %w", known)); got.Code != CodeNotFound || got.Detail != "no route" {
		t.Fatalf("a wrapped classified error is %+v", got)
	}
	if got := OnField(CodeInvalidField, "limit", "above 200").Error(); got != "apierr: invalid_field: above 200 (limit)" {
		t.Fatalf("%q", got)
	}
}

// Every 429 carries Retry-After in whole seconds, rounded up, at least one;
// too_many_streams waits STREAM_POLL_INTERVAL when the instance names
// nothing.
func TestRetryAfter(t *testing.T) {
	for _, c := range []struct {
		e    *Error
		want string
	}{
		{&Error{Code: CodeRateLimited, RetryAfter: 1500 * time.Millisecond}, "2"},
		{&Error{Code: CodeRateLimited, RetryAfter: 59 * time.Second}, "59"},
		{&Error{Code: CodeRateLimited}, "1"},
		{&Error{Code: CodeTooManyStreams}, "1"},
		{&Error{Code: CodeTooManyStreams, RetryAfter: 3 * time.Second}, "3"},
	} {
		rec, _, _ := write(t, c.e)
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != c.want {
			t.Errorf("%s %v: %d Retry-After %q, want %q", c.e.Code, c.e.RetryAfter, rec.Code, rec.Header().Get("Retry-After"), c.want)
		}
	}
}

// A deny reason that names a code the table answers with 403 is that code;
// any other reason is forbidden with the reason in details.
func TestDenied(t *testing.T) {
	for _, c := range []struct{ reason, code string }{
		{CodeSignInNotKept, CodeSignInNotKept},
		{"not_owner", CodeForbidden},
		{CodeForbidden, CodeForbidden},
		{CodeExperimentNotFound, CodeForbidden},
		{CodeInternal, CodeForbidden},
		{"", CodeForbidden},
	} {
		e := Denied("experiment.create", c.reason)
		if e.Code != c.code || e.Reason != c.reason || !strings.Contains(e.Detail, "experiment.create") {
			t.Errorf("Denied(%q) = %+v, want %s", c.reason, e, c.code)
		}
	}
}
