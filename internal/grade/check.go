// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package grade holds the parts of specs/006-grading.md that read what a
// grader answered and write nothing: the check output contract that turns
// a check run's exit and stdout into its state and values, and a model
// judge's input assembly, its call's tool and the validation of its
// answer. The work package runs the graders and the store appends what
// these decide.
package grade

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// Exit is how a check's command ended, as its executor reported it: its
// exit code, its stdout as kept, whether the run was killed when its bound
// ran out, and whether its command could not start.
type Exit struct {
	Code        int
	Stdout      string
	TimedOut    bool
	NotRunnable bool
}

// Produced are the metrics a grader produces, in its order, each with its
// digest.
type Produced struct {
	Digest string
	Metric objects.Metric
}

// CheckResult is what a check run gives: its state, its values, none for
// a state other than ok, and a developer's reason for a run that is not
// ok.
type CheckResult struct {
	State  string
	Values []store.Measured
	Reason string
}

// ReadCheck reads a check run under the output contract
// (specs/006-grading.md, "The output contract"): the JSON form, one
// object whose one member metrics names every metric the grader produces,
// each typed by its metric or null; or the exit-status form for a grader
// of one binary metric, exit 0 true and exit 1 false with nothing on
// stdout. A run yields all its values or none.
func ReadCheck(produces []Produced, e Exit) CheckResult {
	switch {
	case e.NotRunnable:
		return CheckResult{State: objects.CheckNotRunnable, Reason: "the command could not start"}
	case e.TimedOut:
		return CheckResult{State: objects.CheckTimedOut, Reason: "CHECK_TIMEOUT ran out while the command ran"}
	}
	out := strings.TrimSpace(e.Stdout)
	binary := len(produces) == 1 && produces[0].Metric.Type == objects.TypeBinary
	if out == "" {
		switch {
		case (e.Code == 0 || e.Code == 1) && binary:
			return CheckResult{State: objects.CheckOK, Values: []store.Measured{{Metric: produces[0].Digest, Value: e.Code == 0}}}
		case e.Code == 0:
			return CheckResult{State: objects.CheckMalformed, Reason: "stdout is empty, which answers only a grader of one binary metric"}
		}
		return CheckResult{State: objects.CheckError, Reason: "the command exited " + strconv.Itoa(e.Code)}
	}
	if e.Code != 0 {
		return CheckResult{State: objects.CheckError, Reason: "the command exited " + strconv.Itoa(e.Code) + " with output"}
	}
	if len(e.Stdout) > limits.CheckMaxOutputBytes {
		return CheckResult{State: objects.CheckMalformed, Reason: "stdout is longer than CHECK_MAX_OUTPUT_BYTES"}
	}
	values, reason := readMetrics(produces, out)
	if reason != "" {
		return CheckResult{State: objects.CheckMalformed, Reason: reason}
	}
	return CheckResult{State: objects.CheckOK, Values: values}
}

// foreignJSON reads JSON the service did not write: a check's stdout and a
// model's tool input. A repeated member keeps its last value, bytes that
// are not UTF-8 read as U+FFFD and a member name matches in any letter case.
var foreignJSON = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
	json.MatchCaseInsensitiveNames(true),
)

// readValue reads one metric value; a reason is why the value is not one
// this service reads, such as a number out of the range of a float64.
func readValue(raw jsontext.Value) (any, string) {
	var v any
	if err := json.Unmarshal(raw, &v, foreignJSON); err != nil {
		return nil, "the value is not a number this service reads"
	}
	return v, ""
}

// readMetrics reads the JSON form's one object; a reason is why it is not
// one the grader's metrics admit.
func readMetrics(produces []Produced, out string) ([]store.Measured, string) {
	dec := jsontext.NewDecoder(strings.NewReader(out), foreignJSON)
	var top map[string]jsontext.Value
	if err := json.UnmarshalDecode(dec, &top, foreignJSON); err != nil || top == nil {
		return nil, "stdout is not one JSON object"
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, "stdout holds more than one JSON value"
	}
	for k := range top {
		if k != "metrics" {
			return nil, "the object has a member besides metrics: " + k
		}
	}
	raw, ok := top["metrics"]
	if !ok {
		return nil, "the object has no member metrics"
	}
	var named map[string]jsontext.Value
	if err := json.Unmarshal(raw, &named, foreignJSON); err != nil || named == nil {
		return nil, "metrics is not an object"
	}
	names := make([]string, len(produces))
	for i, p := range produces {
		names[i] = p.Metric.Name
	}
	for k := range named {
		if !slices.Contains(names, k) {
			return nil, "metrics." + k + " is a metric the grader does not produce"
		}
	}
	var values []store.Measured
	for _, p := range produces {
		member, ok := named[p.Metric.Name]
		if !ok {
			return nil, "metrics." + p.Metric.Name + " is missing"
		}
		v, why := readValue(member)
		if why != "" {
			return nil, "metrics." + p.Metric.Name + ": " + why
		}
		if v == nil {
			continue
		}
		stored, why := objects.Value(p.Metric, v)
		if why != "" {
			return nil, "metrics." + p.Metric.Name + ": " + why
		}
		values = append(values, store.Measured{Metric: p.Digest, Value: stored})
	}
	return values, ""
}

// Applying keeps the values of metrics that apply to a trial of the
// outcome (specs/002-objects.md, applies_to), dropping the others.
func Applying(values []store.Measured, metrics map[string]objects.Metric, outcome string) []store.Measured {
	var out []store.Measured
	for _, v := range values {
		if m, ok := metrics[v.Metric]; ok && !m.Applies(outcome) {
			continue
		}
		out = append(out, v)
	}
	return out
}
