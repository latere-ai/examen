// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package grade

import (
	"encoding/json/jsontext"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"latere.ai/x/pkg/llmdialect/ir"
	"latere.ai/x/pkg/llmdialect/lux"

	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
)

// jbProduced is n metrics a grader produces, cycling through the five
// shapes a suite uses: a closed label, a score, a binary, a count and an
// open label.
func jbProduced(n int) []Produced {
	out := make([]Produced, n)
	for i := range out {
		name := "metric_" + strconv.Itoa(i)
		var m objects.Metric
		switch i % 5 {
		case 0:
			m = objects.Metric{Name: name, Type: objects.TypeLabel, Labels: []string{"game", "tool", "library", "service"}, Description: "What kind of thing the run made."}
		case 1:
			m = objects.Metric{Name: name, Type: objects.TypeScore, Range: []float64{0, 1}, Description: "How well the result meets the task, from none to fully."}
		case 2:
			m = objects.Metric{Name: name, Type: objects.TypeBinary, Description: "Whether the program starts and runs."}
		case 3:
			m = objects.Metric{Name: name, Type: objects.TypeCount, Description: "How many files the run left behind."}
		default:
			m = objects.Metric{Name: name, Type: objects.TypeLabel, Vocabulary: objects.VocabularyOpen, Description: "A short name for the idea."}
		}
		out[i] = Produced{Digest: fmt.Sprintf("sha256:%064d", i), Metric: m}
	}
	return out
}

// jbText is deterministic prose of about n bytes.
func jbText(n int) string {
	const unit = "The agent read the task, listed the workspace, wrote the program and ran it against the sample input before it answered. "
	var b strings.Builder
	for b.Len() < n {
		b.WriteString(unit)
	}
	return b.String()[:n]
}

// jbEvidence is the evidence of a trial whose transcript is about n bytes.
func jbEvidence(n int) Evidence {
	final, checks := "I made a game of life and ran it.", `{"runs":true,"tests_pass":true,"size":4096}`
	var lines []string
	for i := 1; b2n(lines) < n; i++ {
		lines = append(lines, fmt.Sprintf(`{"id":"evt_%d","seq":%d,"type":"agent.tool_use","payload":{"name":"bash","input":{"command":"go test ./... && echo step %d"}}}`, i, i, i))
	}
	transcript := strings.Join(lines, "\n")
	return Evidence{FinalMessage: &final, Checks: &checks, Transcript: &transcript, Outputs: []Output{{
		Name:    "workspace/",
		Files:   []Block{{Name: "workspace/README.md", Text: "# Life\n" + jbText(300)}, {Name: "workspace/life.py", Text: jbText(1500)}},
		Listing: []Listed{{Path: "logo.png", Size: 10240, Digest: "sha256:aa", Why: WhyNotText}},
	}}}
}

func b2n(lines []string) int {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	return n
}

func jbJudge() objects.Grader {
	zero := 0.0
	return objects.Grader{Name: "artifact", Kind: objects.GraderModelJudge, ModelJudge: &objects.ModelJudge{
		Model: "anthropic/claude-haiku-5.5", Prompt: "Label what the run made.", Rubric: "kind by the first rule that fits",
		Temperature: &zero, Preamble: objects.JudgePreamble, Inputs: objects.JudgeInputs{FinalMessage: true, Outputs: []string{"workspace/"}, Checks: true},
	}}
}

// BenchmarkJSONJudgeRequestMarshal is the call a model judge sends for
// every graded trial: the tool schema, the evidence blocks and the system
// content, rendered as one request body.
func BenchmarkJSONJudgeRequestMarshal(b *testing.B) {
	produces := jbProduced(3)
	for _, c := range []struct {
		name  string
		bytes int
	}{{"small", 0}, {"medium", 10 << 10}, {"large", 200 << 10}} {
		b.Run(c.name, func(b *testing.B) {
			g, ev := jbJudge(), jbEvidence(c.bytes)
			req, err := Assemble(g, "sha256:g", "tri_1", produces, ev, limits.JudgeMaxInputBytes)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(req.Body)))
			for b.Loop() {
				if _, err := Assemble(g, "sha256:g", "tri_1", produces, ev, limits.JudgeMaxInputBytes); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// jbAnswer is a judge's response calling record_judgment for n metrics.
func jbAnswer(produces []Produced, rationale int) lux.Response {
	members := make([]string, len(produces))
	for i, p := range produces {
		var v string
		switch p.Metric.Type {
		case objects.TypeBinary:
			v = "true"
		case objects.TypeScore:
			v = "0.75"
		case objects.TypeCount:
			v = "12"
		case objects.TypeLabel:
			if p.Metric.Open() {
				v = `"snake"`
			} else {
				v = `"game"`
			}
		}
		members[i] = strconv.Quote(p.Metric.Name) + `:{"value":` + v + `,"rationale":` + strconv.Quote(jbText(rationale)) + `}`
	}
	args := "{" + strings.Join(members, ",") + "}"
	return lux.Response{StopReason: ir.StopToolUse, Blocks: []lux.Block{{Type: ir.BlockToolUse, ToolUse: &lux.ToolUse{ID: "toolu_1", Name: ToolName, Args: jsontext.Value(args)}}}}
}

// BenchmarkJSONJudgeAnswerUnmarshal is the validation of a model judge's
// answer: the tool input decoded member by member against its metrics.
func BenchmarkJSONJudgeAnswerUnmarshal(b *testing.B) {
	for _, c := range []struct {
		name      string
		metrics   int
		rationale int
	}{{"small", 3, 60}, {"medium", 20, 400}, {"large", 120, 1500}} {
		b.Run(c.name, func(b *testing.B) {
			produces := jbProduced(c.metrics)
			resp := jbAnswer(produces, c.rationale)
			if got := ReadAnswer(resp, produces); got.State != objects.JudgmentOK {
				b.Fatalf("the fixture answer is %s: %s", got.State, got.Reason)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(resp.Blocks[0].ToolUse.Args)))
			for b.Loop() {
				if got := ReadAnswer(resp, produces); got.State != objects.JudgmentOK {
					b.Fatalf("%s: %s", got.State, got.Reason)
				}
			}
		})
	}
}

// BenchmarkJSONCheckOutputDecode is the reading of a check's standard
// output under the output contract, one object naming each metric.
func BenchmarkJSONCheckOutputDecode(b *testing.B) {
	for _, c := range []struct {
		name    string
		metrics int
	}{{"small", 5}, {"medium", 500}, {"large", 2900}} {
		b.Run(c.name, func(b *testing.B) {
			var produces []Produced
			members := make([]string, c.metrics)
			for i := range members {
				name := "metric_" + strconv.Itoa(i)
				produces = append(produces, Produced{Digest: fmt.Sprintf("sha256:%064d", i), Metric: objects.Metric{Name: name, Type: objects.TypeScore, Range: []float64{0, 1000}}})
				members[i] = strconv.Quote(name) + ":" + strconv.FormatFloat(float64(i%1000)+0.25, 'f', 2, 64)
			}
			out := `{"metrics":{` + strings.Join(members, ",") + `}}`
			if len(out) > limits.CheckMaxOutputBytes {
				b.Fatalf("the fixture is %d bytes, past CHECK_MAX_OUTPUT_BYTES", len(out))
			}
			e := Exit{Stdout: out}
			if got := ReadCheck(produces, e); got.State != objects.CheckOK {
				b.Fatalf("the fixture check is %s: %s", got.State, got.Reason)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(out)))
			for b.Loop() {
				if got := ReadCheck(produces, e); got.State != objects.CheckOK {
					b.Fatalf("%s: %s", got.State, got.Reason)
				}
			}
		})
	}
}
