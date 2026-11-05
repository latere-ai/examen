// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/objects"
)

// jbSizes are the three sizes a payload is measured at.
var jbSizes = []string{"small", "medium", "large"}

// jbWriter is a response writer that keeps nothing, so a benchmark times
// the encoding and not a recorder's copy.
type jbWriter struct{ h http.Header }

func newJBWriter() *jbWriter                    { return &jbWriter{h: http.Header{}} }
func (w *jbWriter) Header() http.Header         { return w.h }
func (w *jbWriter) WriteHeader(int)             {}
func (w *jbWriter) Write(p []byte) (int, error) { return len(p), nil }

// jbCountWriter counts the bytes of an answer.
type jbCountWriter struct {
	jbWriter
	n int
}

func (w *jbCountWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p), nil }

var jbAt = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func jbDigest(i int) string { return fmt.Sprintf("sha256:%064x", i+1) }

// jbCandidates is n candidate references, each a name pinned to a digest.
func jbCandidates(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("agent-%04d@%s", i, jbDigest(i))
	}
	return out
}

// jbCreateBody is the body of an experiment create with n candidates.
func jbCreateBody(n int) []byte {
	cap := int64(25_000_000)
	body := map[string]any{
		"suite":      "regression-suite@" + jbDigest(0),
		"candidates": jbCandidates(n),
		"trials":     5,
		"budget":     map[string]any{"name": "team-budget", "cap_usd_micro": cap},
		"reference":  map[string]any{"baseline": "main", "candidate": "agent-0000"},
		"mode":       "managed",
		"scheduling": "interleaved",
		"comparisons": []map[string]any{
			{"candidate": "agent-0001", "reference_candidate": "agent-0000", "metric": "tests_pass"},
			{"variable": "model", "levels": []string{"haiku", "sonnet"}, "by": []string{"task"}},
		},
		"metadata": map[string]string{"branch": "feature/json-v2", "commit": strings.Repeat("a", 40), "ci": "run-48211"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	return raw
}

// BenchmarkJSONCreateExperimentDecode is the decoding of a create
// request's body by the handler's codec: one object, no member the
// contract does not describe, nothing after it.
func BenchmarkJSONCreateExperimentDecode(b *testing.B) {
	for i, n := range []int{1, 110, 2500} {
		body := jbCreateBody(n)
		b.Run(jbSizes[i], func(b *testing.B) {
			var in createBody
			if err := decode(&http.Request{Body: io.NopCloser(bytes.NewReader(body))}, &in); err != nil || len(in.Candidates) != n {
				b.Fatalf("the fixture does not decode: %v", err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				var in createBody
				if err := decode(&http.Request{Body: io.NopCloser(bytes.NewReader(body))}, &in); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONPinBaselineDecode is the decoding of a small write, a
// baseline pin, which most writes of the API are.
func BenchmarkJSONPinBaselineDecode(b *testing.B) {
	body := []byte(`{"experiment":"exp_01JC8M2Q4V7Y","candidate":"agent-0000@` + jbDigest(0) + `"}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		var in struct {
			Experiment string `json:"experiment"`
			Candidate  string `json:"candidate"`
		}
		if err := decode(&http.Request{Body: io.NopCloser(bytes.NewReader(body))}, &in); err != nil {
			b.Fatal(err)
		}
		if in.Experiment == "" {
			b.Fatal("the pin was not read")
		}
	}
}

// jbTrial is a finished trial as a list answers it.
func jbTrial(i int) objects.Trial {
	started, ended := jbAt.Add(time.Duration(i)*time.Minute), jbAt.Add(time.Duration(i)*time.Minute+4*time.Minute)
	cost, in, out, dur := 0.0184, int64(41200), int64(3100), 212.5
	sz := int64(2048)
	return objects.Trial{
		ID: fmt.Sprintf("tri_%016x", i), Owner: "usr_01JC8M2Q4V7Y", Experiment: "exp_01JC8M2Q4V7Y", Cell: fmt.Sprintf("cel_%016x", i/5), Index: i % 5,
		State: objects.StateDone, Outcome: objects.OutcomeCompleted,
		Sent:     &objects.Sent{Requests: 14, BySetting: []objects.SentSetting{{Model: "anthropic/claude-haiku-5.5", Reasoning: "low", Requests: 14}}},
		Observed: objects.Observed{CostState: objects.CostKnown, CostUSD: &cost, InputTokens: &in, OutputTokens: &out, Duration: &dur, Served: []objects.ModelCount{{Model: "anthropic/claude-haiku-5.5", Requests: 14}}},
		Checks:   []json.RawMessage{json.RawMessage(`{"grader":"sha256:aa","state":"ok"}`)}, Judgments: []json.RawMessage{},
		Evidence:  []objects.EvidenceRef{{Kind: objects.EvidenceOutput, Name: "workspace/", Owner: "usr_01JC8M2Q4V7Y", Path: fmt.Sprintf("trials/%d/workspace/", i), Digest: jbDigest(i), Size: &sz}},
		StartedAt: &started, AgentEndedAt: &ended, EndedAt: &ended, CreatedAt: started,
		Values: map[string]any{"tests_pass": true, "quality": 0.75},
	}
}

// BenchmarkJSONTrialListEncode is the answer of a list of trials: the page
// envelope and its items rendered through the route's reply, from one
// item up to MAX_PAGE_SIZE.
func BenchmarkJSONTrialListEncode(b *testing.B) {
	for i, n := range []int{1, 12, 200} {
		items := make([]objects.Trial, n+1)
		for j := range items {
			items[j] = jbTrial(j)
		}
		b.Run(jbSizes[i], func(b *testing.B) {
			p := page{Limit: n}
			var probe jbCountWriter
			probe.h = http.Header{}
			replyPage(&probe, "trials", p, items, func(t objects.Trial) string { return t.ID })
			w := newJBWriter()
			b.ReportAllocs()
			b.SetBytes(int64(probe.n))
			for b.Loop() {
				replyPage(w, "trials", p, items, func(t objects.Trial) string { return t.ID })
			}
		})
	}
}

// jbExperiment is an experiment with n candidates as the stream's
// snapshot frame carries it.
func jbExperiment(n int) objects.Experiment {
	cap, spent := int64(25_000_000), int64(9_400_000)
	remaining := cap - spent
	trials := 5
	return objects.Experiment{
		ID: "exp_01JC8M2Q4V7Y", Owner: "usr_01JC8M2Q4V7Y", Suite: "regression-suite@" + jbDigest(0), Candidates: jbCandidates(n), Trials: trials,
		Budget:      objects.Budget{Name: "team-budget", CapUSDMicro: &cap, SpentUSDMicro: &spent, RemainingUSDMicro: &remaining},
		Parallelism: 8, Reference: objects.Reference{Baseline: "main", Candidate: "agent-0000"}, Mode: "managed", Scheduling: "interleaved",
		Comparisons: []objects.DeclaredComparison{{Candidate: "agent-0001", ReferenceCandidate: "agent-0000", Metric: "tests_pass"}},
		State:       "running", CreatedBy: "usr_01JC8M2Q4V7Y", Judgments: []json.RawMessage{},
		Progress:  &objects.Progress{Cells: map[string]int{"pending": 12, "running": 8, "done": 40 * n}, Trials: map[string]int{"pending": 60, "running": 8, "done": 200 * n}, Outcomes: map[string]int{"completed": 190 * n, "timed_out": 6 * n, "declined": 4 * n}},
		Usage:     &objects.Usage{Requests: 2800 * int64(n), CostUSDMicro: spent},
		Metadata:  map[string]string{"branch": "feature/json-v2", "commit": strings.Repeat("a", 40)},
		CreatedAt: jbAt, UpdatedAt: jbAt.Add(time.Hour),
	}
}

// BenchmarkJSONStreamSnapshotMarshal is the first frame of an experiment's
// event stream: the experiment rendered as the snapshot event.
func BenchmarkJSONStreamSnapshotMarshal(b *testing.B) {
	for i, n := range []int{1, 110, 2500} {
		e := jbExperiment(n)
		b.Run(jbSizes[i], func(b *testing.B) {
			var probe jbCountWriter
			snapshot, err := json.Marshal(e)
			if err != nil || frame(&probe, 1, "snapshot", snapshot) != nil {
				b.Fatal("the snapshot does not render")
			}
			b.ReportAllocs()
			b.SetBytes(int64(probe.n))
			w := newJBWriter()
			for b.Loop() {
				snapshot, err := json.Marshal(e)
				if err != nil {
					b.Fatal(err)
				}
				if err := frame(w, 1, "snapshot", snapshot); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONIdempotencyReplayRoundTrip is the answer kept under an
// idempotency key being given its credential afresh: the kept object
// decoded, one member set and the object encoded again.
func BenchmarkJSONIdempotencyReplayRoundTrip(b *testing.B) {
	for i, n := range []int{1, 110, 2500} {
		kept, err := json.Marshal(jbExperiment(n))
		if err != nil {
			b.Fatal(err)
		}
		secret := map[string]string{"value": "lx_" + strings.Repeat("k", 40), "expires_at": jbAt.Format(time.RFC3339)}
		b.Run(jbSizes[i], func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(kept)))
			for b.Loop() {
				if _, err := withMember(kept, "key", secret); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
