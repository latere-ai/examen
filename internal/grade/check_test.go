// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package grade

import (
	"strings"
	"testing"

	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

var (
	passes  = Produced{Digest: "sha256:pass", Metric: objects.Metric{Name: "tests_pass", Type: objects.TypeBinary}}
	size    = Produced{Digest: "sha256:size", Metric: objects.Metric{Name: "size", Type: objects.TypeCount}}
	quality = Produced{Digest: "sha256:qual", Metric: objects.Metric{Name: "quality", Type: objects.TypeScore, Range: []float64{0, 1}}}
	kind    = Produced{Digest: "sha256:kind", Metric: objects.Metric{Name: "kind", Type: objects.TypeLabel, Labels: []string{"game", "tool"}}}
	idea    = Produced{Digest: "sha256:idea", Metric: objects.Metric{Name: "idea", Type: objects.TypeLabel, Vocabulary: objects.VocabularyOpen}}
)

// TestRunStates holds each row of the check run state table
// (specs/006-grading.md): the JSON form, the exit-status form, a check
// that failed itself, one that could not start, and one its bound killed.
func TestRunStates(t *testing.T) {
	two := []Produced{passes, size}
	for _, c := range []struct {
		name     string
		produces []Produced
		exit     Exit
		state    string
		values   int
	}{
		{"the JSON form", two, Exit{Stdout: ` {"metrics": {"tests_pass": true, "size": 12}} `}, objects.CheckOK, 2},
		{"a null is no value", two, Exit{Stdout: `{"metrics": {"tests_pass": null, "size": 3}}`}, objects.CheckOK, 1},
		{"exit 0 with nothing, one binary metric", []Produced{passes}, Exit{}, objects.CheckOK, 1},
		{"exit 1 with nothing, one binary metric", []Produced{passes}, Exit{Code: 1}, objects.CheckOK, 1},
		{"exit 0 with nothing, two metrics", two, Exit{}, objects.CheckMalformed, 0},
		{"exit 1 with nothing, two metrics", two, Exit{Code: 1}, objects.CheckError, 0},
		{"exit 1 with output", two, Exit{Code: 1, Stdout: `{"metrics": {}}`}, objects.CheckError, 0},
		{"exit 2", []Produced{passes}, Exit{Code: 2}, objects.CheckError, 0},
		{"not runnable", two, Exit{NotRunnable: true}, objects.CheckNotRunnable, 0},
		{"timed out", two, Exit{TimedOut: true, Stdout: `{"metrics": {"tests_pass": true, "size": 1}}`}, objects.CheckTimedOut, 0},
	} {
		got := ReadCheck(c.produces, c.exit)
		if got.State != c.state || len(got.Values) != c.values {
			t.Errorf("%s: %s with %d values (%s), want %s with %d", c.name, got.State, len(got.Values), got.Reason, c.state, c.values)
		}
		if got.State != objects.CheckOK && got.Reason == "" {
			t.Errorf("%s: a run that is not ok names no reason", c.name)
		}
	}
	if got := ReadCheck([]Produced{passes}, Exit{Code: 1}); got.Values[0] != (store.Measured{Metric: passes.Digest, Value: false}) {
		t.Errorf("exit 1 is the task's failure, false: %+v", got.Values)
	}
}

// TestTaskFailureIsAValue: a check that reports failing tests gives the
// value false, which analysis reads, never a missing value.
func TestTaskFailureIsAValue(t *testing.T) {
	got := ReadCheck([]Produced{passes}, Exit{Stdout: `{"metrics": {"tests_pass": false}}`})
	if got.State != objects.CheckOK || len(got.Values) != 1 || got.Values[0].Value != false {
		t.Fatalf("failing tests: %+v", got)
	}
}

// TestMalformedYieldsNothing: an output that names a metric wrongly
// yields no value at all, so a check cannot report a convenient subset.
func TestMalformedYieldsNothing(t *testing.T) {
	all := []Produced{passes, size, quality, kind, idea}
	ok := `"tests_pass": true, "size": 1, "quality": 0.5, "kind": "game", "idea": "Life"`
	for name, out := range map[string]string{
		"not JSON":            "passed",
		"two values":          `{"metrics": {` + ok + `}} {}`,
		"an array":            `[1]`,
		"no metrics":          `{"values": {}}`,
		"an extra member":     `{"metrics": {` + ok + `}, "note": "x"}`,
		"metrics not object":  `{"metrics": [1]}`,
		"a missing metric":    `{"metrics": {"tests_pass": true}}`,
		"an unknown metric":   `{"metrics": {` + ok + `, "speed": 3}}`,
		"a mistyped binary":   `{"metrics": {"tests_pass": "yes", "size": 1, "quality": 0.5, "kind": "game", "idea": "x"}}`,
		"a fractional count":  `{"metrics": {"tests_pass": true, "size": 1.5, "quality": 0.5, "kind": "game", "idea": "x"}}`,
		"a negative count":    `{"metrics": {"tests_pass": true, "size": -1, "quality": 0.5, "kind": "game", "idea": "x"}}`,
		"an out-of-range":     `{"metrics": {"tests_pass": true, "size": 1, "quality": 1.4, "kind": "game", "idea": "x"}}`,
		"an unknown label":    `{"metrics": {"tests_pass": true, "size": 1, "quality": 0.5, "kind": "film", "idea": "x"}}`,
		"an open label empty": `{"metrics": {"tests_pass": true, "size": 1, "quality": 0.5, "kind": "game", "idea": "   "}}`,
		"too long":            `{"metrics": {` + ok + `}}` + strings.Repeat(" ", limits.CheckMaxOutputBytes),
	} {
		got := ReadCheck(all, Exit{Stdout: out})
		if got.State != objects.CheckMalformed || len(got.Values) != 0 || got.Reason == "" {
			t.Errorf("%s: %+v", name, got)
		}
	}
	got := ReadCheck(all, Exit{Stdout: `{"metrics": {` + ok + `}}`})
	if got.State != objects.CheckOK || len(got.Values) != 5 || got.Values[4].Value != "life" {
		t.Errorf("a whole answer: %+v", got)
	}
}

// FuzzParse: whatever a check prints, the reading is one of the states
// and gives values only when ok.
func FuzzParse(f *testing.F) {
	f.Add(`{"metrics": {"tests_pass": true, "size": 3}}`, 0)
	f.Add(``, 1)
	f.Add(`{"metrics": {"tests_pass": null, "size": null}}`, 0)
	f.Fuzz(func(t *testing.T, out string, code int) {
		got := ReadCheck([]Produced{passes, size}, Exit{Code: code, Stdout: out})
		switch got.State {
		case objects.CheckOK:
		case objects.CheckMalformed, objects.CheckError:
			if len(got.Values) != 0 {
				t.Fatalf("%s with values %v", got.State, got.Values)
			}
		default:
			t.Fatalf("state %q", got.State)
		}
	})
}

// TestApplying drops a value whose metric does not apply to the trial's
// outcome.
func TestApplying(t *testing.T) {
	metrics := map[string]objects.Metric{"sha256:a": {AppliesTo: []string{objects.OutcomeCompleted}}, "sha256:b": {}}
	values := []store.Measured{{Metric: "sha256:a", Value: true}, {Metric: "sha256:b", Value: 1.0}, {Metric: "sha256:c", Value: "x"}}
	if got := Applying(values, metrics, objects.OutcomeTimedOut); len(got) != 2 || got[0].Metric != "sha256:b" {
		t.Errorf("for a timed-out trial: %+v", got)
	}
	if got := Applying(values, metrics, objects.OutcomeCompleted); len(got) != 3 {
		t.Errorf("for a completed trial: %+v", got)
	}
}
