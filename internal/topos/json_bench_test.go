// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"time"
)

var jbStart = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func jbEvent(seq uint64, typ, thread string, payload any) Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return Event{ID: fmt.Sprintf("evt_%08d", seq), Seq: seq, Type: typ, Thread: thread, Time: jbStart.Add(time.Duration(seq) * time.Second), Payload: raw}
}

func jbProse(n int) string {
	const unit = "I read the task, listed the workspace and wrote the program, then ran it against the sample input. "
	return strings.Repeat(unit, n/len(unit)+1)[:n]
}

// jbLog is a finished session's log of about n encoded bytes: a status
// event, then turns of a model request, a tool call and an agent message,
// a save of the working directory after every third turn, and the end.
func jbLog(n int) (Session, []Event) {
	var events []Event
	size := 0
	add := func(typ, thread string, payload any) {
		e := jbEvent(uint64(len(events)+1), typ, thread, payload)
		line, err := json.Marshal(e, logJSON)
		if err != nil {
			panic(err)
		}
		size += len(line) + 1
		events = append(events, e)
	}
	add(EventSessionStatus, "", map[string]any{"status": StatusRunning})
	for turn := 0; size < n; turn++ {
		add(EventModelRequest, "", map[string]any{
			"model": "anthropic/claude-haiku-5.5", "outcome": "ok", "cost_usd_micro": 1840 + turn,
			"usage": map[string]any{"input_tokens": 4200 + turn, "output_tokens": 310, "cache_read_input_tokens": 3900, "cache_write_input_tokens": 0, "reasoning_tokens": 0},
		})
		add(EventAgentToolUse, "", map[string]any{"id": fmt.Sprintf("toolu_%d", turn), "name": "bash", "input": map[string]any{"command": "go test ./... && go vet ./..."}})
		add(EventAgentMessage, "", map[string]any{
			"stop_reason": "tool_use",
			"message":     map[string]any{"blocks": []map[string]any{{"type": "text", "text": jbProse(240)}, {"type": "tool_use", "id": fmt.Sprintf("toolu_%d", turn)}}},
		})
		if turn%3 == 2 {
			written := make([]map[string]any, 4)
			for i := range written {
				written[i] = map[string]any{"path": fmt.Sprintf("src/file_%d_%d.go", turn, i), "size": 2048 + i, "mode": 420, "sha256": strings.Repeat("ab", 32)}
			}
			add(EventWorkSaved, "", map[string]any{"whole": turn == 2, "written": written, "removed": []string{}, "unsaved": []string{}})
		}
	}
	add(EventAgentMessage, "", map[string]any{"stop_reason": "end_turn", "message": map[string]any{"blocks": []map[string]any{{"type": "text", "text": "Done. The program runs and the tests pass."}}}})
	add(EventSessionStatus, "", map[string]any{"status": StatusEnded, "stop_reason": StopCompleted})
	s := Session{
		ID: "ses_00000001", Status: StatusEnded, StopReason: StopCompleted, LastSeq: uint64(len(events)), Rollout: "ro_1",
		Model: &Model{Name: "anthropic/claude-haiku-5.5", Via: "lux", Reasoning: "low"}, CreatedAt: jbStart, UpdatedAt: jbStart.Add(time.Duration(len(events)) * time.Second),
	}
	return s, events
}

var jbSizes = []struct {
	name  string
	bytes int
}{{"small", 512}, {"medium", 10 << 10}, {"large", 200 << 10}}

// BenchmarkJSONSessionReadRoundTrip is a trial's report read from its
// session's log: each event rendered as a line of the kept log, and the
// payloads of the events a report reads decoded.
func BenchmarkJSONSessionReadRoundTrip(b *testing.B) {
	for _, c := range jbSizes {
		b.Run(c.name, func(b *testing.B) {
			s, events := jbLog(c.bytes)
			r, err := Read(s, events)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(r.Log)))
			for b.Loop() {
				if _, err := Read(s, events); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONEvidenceOfRoundTrip is a trial's evidence read from the
// same log: the log as JSONL, the final message and the folded saves.
func BenchmarkJSONEvidenceOfRoundTrip(b *testing.B) {
	for _, c := range jbSizes {
		b.Run(c.name, func(b *testing.B) {
			_, events := jbLog(c.bytes)
			ev, err := EvidenceOf(events)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(ev.Log)))
			for b.Loop() {
				if _, err := EvidenceOf(events); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONEventPageUnmarshal is a page of a session's log as the
// executor reads it from the session service.
func BenchmarkJSONEventPageUnmarshal(b *testing.B) {
	for _, c := range jbSizes {
		b.Run(c.name, func(b *testing.B) {
			_, events := jbLog(c.bytes)
			raw, err := json.Marshal(map[string]any{"items": events, "next_cursor": "cur_0001"})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				var page struct {
					Items      []Event `json:"items"`
					NextCursor string  `json:"next_cursor"`
				}
				if err := json.Unmarshal(raw, &page, foreignJSON); err != nil {
					b.Fatal(err)
				}
				if len(page.Items) != len(events) {
					b.Fatalf("%d events, want %d", len(page.Items), len(events))
				}
			}
		})
	}
}

// BenchmarkJSONSessionUnmarshal is a session as the executor reads it
// when it polls for the end of a run.
func BenchmarkJSONSessionUnmarshal(b *testing.B) {
	s, _ := jbLog(512)
	raw, err := json.Marshal(s)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	for b.Loop() {
		var got Session
		if err := json.Unmarshal(raw, &got, foreignJSON); err != nil {
			b.Fatal(err)
		}
		if got.ID != s.ID {
			b.Fatal("the session was not read")
		}
	}
}
