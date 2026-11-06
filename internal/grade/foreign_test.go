// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package grade

import (
	"encoding/json/v2"
	"testing"

	"latere.ai/x/pkg/llmdialect/ir"
	"latere.ai/x/sdk-go/llm/wire"

	"latere.ai/x/examen/internal/objects"
)

// A check's stdout is written by a process and a judge's answer by a model:
// a repeated member keeps its last value and a number out of the range of
// a float64 is a reason, not a failure to read the object.
func TestCheckOutputFromAProcess(t *testing.T) {
	got := ReadCheck([]Produced{passes, size}, Exit{Stdout: `{"metrics": {"size": 1, "tests_pass": true, "size": 2}}`})
	if got.State != objects.CheckOK || len(got.Values) != 2 || got.Values[1].Value != 2.0 {
		t.Fatalf("a repeated member: %+v", got)
	}
	got = ReadCheck([]Produced{size}, Exit{Stdout: `{"metrics": {"size": 1e999}}`})
	if got.State != objects.CheckMalformed || got.Reason != "metrics.size: the value is not a number this service reads" {
		t.Fatalf("a number out of range: %+v", got)
	}
}

func TestJudgeAnswerFromAModel(t *testing.T) {
	got := ReadAnswer(answer("{\"kind\": {\"value\": \"game\", \"rationale\": \"a\"}, \"idea\": {\"value\": \"snake\", \"rationale\": \"x\xffy\", \"rationale\": \"b\xfe\"}}", ir.StopToolUse), []Produced{kind, idea})
	if got.State != objects.JudgmentOK || len(got.Values) != 2 || got.Values[1].Rationale != "b�" {
		t.Fatalf("a repeated member and bytes that are not UTF-8: %+v", got)
	}
}

// A request to a provider has the same bytes whenever it is built, and a
// nil list is null.
func TestRequestBytes(t *testing.T) {
	a, err := inputSchema([]Produced{kind, idea})
	if err != nil {
		t.Fatal(err)
	}
	for range 20 {
		b, err := inputSchema([]Produced{kind, idea})
		if err != nil || string(a) != string(b) {
			t.Fatalf("the schema differs: %s %s %v", a, b, err)
		}
	}
	want := `{"additionalProperties":false,"properties":{"idea":{"additionalProperties":false,"properties":{"rationale":{"description":"One or two sentences on what decided the value.","type":"string"},"value":{"type":"string"}},"required":["value","rationale"],"type":"object"},"kind":{"additionalProperties":false,"properties":{"rationale":{"description":"One or two sentences on what decided the value.","type":"string"},"value":{"enum":["game","tool"],"type":"string"}},"required":["value","rationale"],"type":"object"}},"required":["kind","idea"],"type":"object"}`
	if string(a) != want {
		t.Errorf("schema %s", a)
	}
	empty, err := inputSchema(nil)
	if err != nil || string(empty) != `{"additionalProperties":false,"properties":{},"required":null,"type":"object"}` {
		t.Errorf("empty schema %s %v", empty, err)
	}
}

// A provider request leaves out a flag or a count that is zero, so the same
// request has the same bytes and digest.
func TestRequestOmitsZeroFlags(t *testing.T) {
	zero := 0.0
	b, err := json.Marshal(wire.Request{
		Model:       "m",
		System:      []wire.Block{{Type: ir.BlockText, Text: "a<b"}},
		Messages:    []wire.Message{{Role: ir.RoleUser, Blocks: []wire.Block{{Type: ir.BlockText, Text: "x"}}}},
		ToolChoice:  &wire.ToolChoice{Mode: ir.ToolChoiceTool, Name: "t"},
		Temperature: &zero,
		Reasoning:   &wire.Reasoning{Level: "low"},
	}, providerJSON)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"m","system":[{"type":"text","text":"a<b"}],"messages":[{"role":"user","blocks":[{"type":"text","text":"x"}]}],"tool_choice":{"mode":"tool","name":"t"},"temperature":0,"reasoning":{"effort":"low"}}`
	if string(b) != want {
		t.Errorf("request %s", b)
	}
}
