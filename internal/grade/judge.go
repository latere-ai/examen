// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package grade

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"latere.ai/x/pkg/llmdialect/ir"
	"latere.ai/x/pkg/llmdialect/lux"
	"latere.ai/x/sdk-go/llm/wire"

	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// ToolName is the one tool a model judge is offered and must call.
const ToolName = "record_judgment"

// Block is one input a judge reads: its name and its text.
type Block struct {
	Name string
	Text string
}

// Listed is one file of a directory output that is no block of its own:
// its path in the output, its size and digest, and why, not_text,
// not_kept or over_bound.
type Listed struct {
	Path   string
	Size   int64
	Digest string
	Why    string
}

// The reasons a file of a directory output is listed rather than read.
const (
	WhyNotText   = "not_text"
	WhyNotKept   = "not_kept"
	WhyOverBound = "over_bound"
)

// Output is one output a judge reads: a file, as one block, or a whole
// directory, as its UTF-8 text files in ascending bytewise order of path
// and the listing of the rest.
type Output struct {
	Name    string
	Files   []Block
	Listing []Listed
}

// Evidence is everything a judgment reads, in the order its inputs fill
// the bound: the final message, the check values, the named files, the
// outputs and the transcript. A nil member is an input the grader does
// not name.
type Evidence struct {
	FinalMessage *string
	Checks       *string
	Files        []Block
	Outputs      []Output
	Transcript   *string
}

// Request is a judgment's call as sent: the body, its SHA-256, the
// boundary its evidence blocks are delimited by, and the cuts the bound
// made.
type Request struct {
	Body     []byte
	Digest   string
	Boundary string
	Cuts     []objects.Cut
}

// Assemble is a model judge's call (specs/006-grading.md, "Input
// assembly"): a deterministic function of the grader version and the
// evidence. The inputs are bounded by bound bytes, filled in order and cut
// at their ends; each is one block between a boundary no input holds; the
// system content is Examen's preamble, the grader's prompt and rubric and
// the metrics to fill, and the user turn is the evidence alone.
func Assemble(g objects.Grader, graderDigest, trial string, produces []Produced, ev Evidence, bound int) (Request, error) {
	j := g.ModelJudge
	if j == nil {
		return Request{}, fmt.Errorf("grade: the grader %s is no model judge", graderDigest)
	}
	blocks, cuts := bounded(ev, bound)
	boundary := boundaryFor(graderDigest, trial, blocks)
	var user strings.Builder
	for i, b := range blocks {
		if i > 0 {
			user.WriteString("\n")
		}
		user.WriteString("<<<EVIDENCE " + boundary + " name=" + strconv.Quote(b.Name))
		if b.elided > 0 {
			user.WriteString(" elided=" + strconv.Itoa(b.elided))
		}
		user.WriteString(">>>\n" + b.Text + "\n<<<END " + boundary + ">>>\n")
	}
	if len(blocks) == 0 {
		user.WriteString("<<<EVIDENCE " + boundary + " name=\"none\">>>\n<<<END " + boundary + ">>>\n")
	}
	schema, err := inputSchema(produces)
	if err != nil {
		return Request{}, err
	}
	maxTokens := int64(limits.JudgeMaxOutputTokens)
	req := lux.Request{
		Model:      j.Model,
		System:     []lux.Block{{Type: ir.BlockText, Text: systemContent(*j, produces)}},
		Messages:   []lux.Message{{Role: ir.RoleUser, Blocks: []lux.Block{{Type: ir.BlockText, Text: user.String()}}}},
		Tools:      []lux.Tool{{Name: ToolName, Description: "Record the value and the rationale of every metric of this judgment, once.", InputSchema: schema}},
		ToolChoice: &lux.ToolChoice{Mode: ir.ToolChoiceTool, Name: ToolName},
		MaxTokens:  &maxTokens,
	}
	if j.Temperature != nil {
		t := *j.Temperature
		req.Temperature = &t
	}
	if j.Reasoning != "" {
		req.Reasoning = &lux.Reasoning{Level: wire.ReasoningLevel(j.Reasoning)}
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Request{}, fmt.Errorf("grade: encode the judgment's request: %w", err)
	}
	sum := sha256.Sum256(body)
	return Request{Body: body, Digest: "sha256:" + hex.EncodeToString(sum[:]), Boundary: boundary, Cuts: cuts}, nil
}

// systemContent is the judge's instructions: the preamble pinned on the
// grader, its prompt, its rubric, and each metric with its type, its
// range or labels and its description. No byte of evidence is in it.
func systemContent(j objects.ModelJudge, produces []Produced) string {
	preamble := j.Preamble
	if preamble == "" {
		preamble = objects.JudgePreamble
	}
	parts := []string{preamble}
	if strings.TrimSpace(j.Prompt) != "" {
		parts = append(parts, j.Prompt)
	}
	if strings.TrimSpace(j.Rubric) != "" {
		parts = append(parts, j.Rubric)
	}
	var m strings.Builder
	m.WriteString("Metrics to fill:")
	for _, p := range produces {
		m.WriteString("\n- " + p.Metric.Name + " (" + describeType(p.Metric) + ")")
		if p.Metric.Description != "" {
			m.WriteString(": " + p.Metric.Description)
		}
	}
	parts = append(parts, m.String())
	return strings.Join(parts, "\n\n")
}

func describeType(m objects.Metric) string {
	switch {
	case m.Type == objects.TypeLabel && m.Open():
		return "label, a short free-form name"
	case m.Type == objects.TypeLabel:
		return "label, one of: " + strings.Join(m.Labels, ", ")
	case m.Type == objects.TypeScore && len(m.Range) == 2:
		return "score from " + strconv.FormatFloat(m.Range[0], 'g', -1, 64) + " to " + strconv.FormatFloat(m.Range[1], 'g', -1, 64)
	case m.Type == objects.TypeBinary:
		return "binary, true or false"
	case m.Type == objects.TypeCount:
		return "count, a whole number at least 0"
	}
	return m.Type + ", a number at least 0"
}

// inputSchema is record_judgment's input: one member per metric, all
// required and no other, each {value, rationale}.
func inputSchema(produces []Produced) (json.RawMessage, error) {
	props := map[string]any{}
	var required []string
	for _, p := range produces {
		props[p.Metric.Name] = map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value":     valueSchema(p.Metric),
				"rationale": map[string]any{"type": "string", "description": "One or two sentences on what decided the value."},
			},
			"required":             []string{"value", "rationale"},
			"additionalProperties": false,
		}
		required = append(required, p.Metric.Name)
	}
	raw, err := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	if err != nil {
		return nil, fmt.Errorf("grade: encode the judgment's schema: %w", err)
	}
	return raw, nil
}

func valueSchema(m objects.Metric) map[string]any {
	switch m.Type {
	case objects.TypeBinary:
		return map[string]any{"type": "boolean"}
	case objects.TypeLabel:
		if m.Open() {
			return map[string]any{"type": "string"}
		}
		return map[string]any{"type": "string", "enum": m.Labels}
	case objects.TypeCount:
		return map[string]any{"type": "integer", "minimum": 0}
	case objects.TypeScore:
		if len(m.Range) == 2 {
			return map[string]any{"type": "number", "minimum": m.Range[0], "maximum": m.Range[1]}
		}
	}
	return map[string]any{"type": "number", "minimum": 0}
}

// block is one evidence block as assembled, with the bytes its cut left
// out.
type block struct {
	Block
	elided int
}

// bounded fills the inputs into bound bytes in their order, cutting each
// that does not fit at its end, the transcript keeping its first line and
// as much of its end as fits. Once the bound is reached a directory
// output's files not yet read join its listing as over_bound.
func bounded(ev Evidence, bound int) ([]block, []objects.Cut) {
	left := bound
	var out []block
	var cuts []objects.Cut
	add := func(name, text string, transcript bool) {
		b := block{Name: name}
		switch {
		case len(text) <= left:
			b.Text = text
		case transcript:
			b.Text, b.elided = keepEnds(text, left)
		default:
			b.Text, b.elided = keepHead(text, left)
		}
		if b.elided > 0 {
			b.Text += "\n[examen: " + strconv.Itoa(b.elided) + " bytes elided]"
			cuts = append(cuts, objects.Cut{Name: name, Elided: b.elided})
		}
		left -= len(text) - b.elided
		out = append(out, b)
	}
	if ev.FinalMessage != nil {
		add("final_message", *ev.FinalMessage, false)
	}
	if ev.Checks != nil {
		add("checks", *ev.Checks, false)
	}
	for _, f := range ev.Files {
		add(f.Name, f.Text, false)
	}
	for _, o := range ev.Outputs {
		listing := slices.Clone(o.Listing)
		for _, f := range o.Files {
			if left <= 0 {
				listing = append(listing, Listed{Path: strings.TrimPrefix(f.Name, o.Name), Size: int64(len(f.Text)), Digest: digestOf(f.Text), Why: WhyOverBound})
				continue
			}
			add(f.Name, f.Text, false)
		}
		if len(listing) > 0 {
			var l strings.Builder
			for _, f := range listing {
				l.WriteString(f.Path + " " + strconv.FormatInt(f.Size, 10) + " " + f.Digest + " " + f.Why + "\n")
			}
			add(o.Name, strings.TrimSuffix(l.String(), "\n"), false)
		}
	}
	if ev.Transcript != nil {
		add("transcript", *ev.Transcript, true)
	}
	return out, cuts
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// keepHead keeps the beginning of s within n bytes, on a rune boundary,
// and answers the bytes left out.
func keepHead(s string, n int) (string, int) {
	n = max(n, 0)
	for n > 0 && n < len(s) && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n], len(s) - n
}

// keepEnds keeps the first line of s and as much of its end as fits in n
// bytes, and answers the bytes left out.
func keepEnds(s string, n int) (string, int) {
	first, _, _ := strings.Cut(s, "\n")
	if len(first) >= n {
		return keepHead(s, n)
	}
	tail := n - len(first) - 1
	start := len(s) - tail
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return first + "\n" + s[start:], len(s) - len(first) - (len(s) - start)
}

// boundaryFor is the hex of the first 8 bytes of SHA-256 of the grader's
// digest, the trial's id and a counter, for the least counter whose
// boundary occurs in no block.
func boundaryFor(graderDigest, trial string, blocks []block) string {
	for n := 0; ; n++ {
		sum := sha256.Sum256([]byte(graderDigest + "\x00" + trial + "\x00" + strconv.Itoa(n)))
		b := hex.EncodeToString(sum[:8])
		if !slices.ContainsFunc(blocks, func(x block) bool { return strings.Contains(x.Text, b) || strings.Contains(x.Name, b) }) {
			return b
		}
	}
}

// Answer is what a judgment's answer gives: its state among ok, refused
// and malformed, the values and rationales of an ok answer, and a
// developer's reason for another.
type Answer struct {
	State  string
	Values []store.Measured
	Reason string
}

// ReadAnswer validates a judge's answer whatever the provider enforced
// (specs/006-grading.md, "The output"): exactly one call of
// record_judgment and no other, whose input names every metric and no
// other, each {value, rationale} with a value its metric admits and never
// null, and a rationale of at most JUDGE_MAX_RATIONALE_BYTES.
func ReadAnswer(resp lux.Response, produces []Produced) Answer {
	if resp.StopReason == ir.StopRefusal {
		return Answer{State: objects.JudgmentRefused, Reason: "the response ended with a refusal"}
	}
	var calls []*lux.ToolUse
	for _, b := range resp.Blocks {
		if b.Type == ir.BlockToolUse && b.ToolUse != nil {
			calls = append(calls, b.ToolUse)
		}
	}
	switch {
	case len(calls) == 0:
		return malformed("the answer called no tool")
	case len(calls) > 1:
		return malformed("the answer called " + strconv.Itoa(len(calls)) + " tools")
	case calls[0].Name != ToolName:
		return malformed("the answer called " + calls[0].Name)
	}
	dec := json.NewDecoder(bytes.NewReader(calls[0].Args))
	dec.UseNumber()
	var input map[string]json.RawMessage
	if err := dec.Decode(&input); err != nil || input == nil {
		return malformed("the call's input is not one JSON object")
	}
	names := map[string]bool{}
	for _, p := range produces {
		names[p.Metric.Name] = true
	}
	for k := range input {
		if !names[k] {
			return malformed(k + " is a metric the judge does not produce")
		}
	}
	var values []store.Measured
	for _, p := range produces {
		raw, ok := input[p.Metric.Name]
		if !ok {
			return malformed(p.Metric.Name + " is missing")
		}
		var member struct {
			Value     any     `json:"value"`
			Rationale *string `json:"rationale"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var shape map[string]json.RawMessage
		if err := json.Unmarshal(raw, &shape); err != nil || shape == nil {
			return malformed(p.Metric.Name + " is not {value, rationale}")
		}
		for k := range shape {
			if k != "value" && k != "rationale" {
				return malformed(p.Metric.Name + "." + k + " is no member of {value, rationale}")
			}
		}
		if err := dec.Decode(&member); err != nil {
			return malformed(p.Metric.Name + " is not {value, rationale}")
		}
		switch {
		case member.Value == nil:
			return malformed(p.Metric.Name + ".value is missing or null")
		case member.Rationale == nil:
			return malformed(p.Metric.Name + ".rationale is missing")
		case len(*member.Rationale) > limits.JudgeMaxRationaleBytes:
			return malformed(p.Metric.Name + ".rationale is longer than JUDGE_MAX_RATIONALE_BYTES")
		}
		stored, why := objects.Value(p.Metric, member.Value)
		if why != "" {
			return malformed(p.Metric.Name + ".value: " + why)
		}
		values = append(values, store.Measured{Metric: p.Digest, Value: stored, Rationale: *member.Rationale})
	}
	return Answer{State: objects.JudgmentOK, Values: values}
}

func malformed(reason string) Answer {
	return Answer{State: objects.JudgmentMalformed, Reason: reason}
}
