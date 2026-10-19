// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/objects"
)

var t0 = time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)

func ev(typ, thread string, at time.Duration, payload string) Event {
	return Event{ID: "evt_" + typ, Type: typ, Thread: thread, Time: t0.Add(at), Payload: json.RawMessage(payload)}
}

// TestOutcomes holds each row of spec 005's topos outcome table: how a
// session that ended, or stopped idle short of its end, reads as its
// trial's outcome and failure, and whether the executor ends it.
func TestOutcomes(t *testing.T) {
	refusal := []Event{ev(EventAgentMessage, "", time.Second, `{"stop_reason":"refusal"}`)}
	errorOf := func(code string) []Event {
		return []Event{ev(EventSessionError, "", time.Second, `{"code":"`+code+`"}`)}
	}
	machine := func(reason string) []Event {
		return []Event{ev(EventSessionMachine, "", time.Second, `{"machine":{"kind":"cella","id":"sbx_1"},"reason":"`+reason+`"}`)}
	}
	// noMachine is a run whose every tool call met a full sandbox
	// ceiling: the agent answered without a machine and ended its turn.
	noMachine := []Event{
		ev(EventSessionError, "", time.Second, `{"code":"machine_unavailable"}`),
		ev(EventSessionError, "thr_1", 2*time.Second, `{"code":"machine_unavailable"}`),
		ev(EventAgentMessage, "", 3*time.Second, `{"stop_reason":"end_turn"}`),
	}
	for _, c := range []struct {
		name, status, stop string
		events             []Event
		done               bool
		outcome, code, end string
	}{
		{"running", StatusRunning, "", nil, false, "", "", ""},
		{"idle before its turn", StatusIdle, "", nil, false, "", "", ""},
		{"completed", StatusEnded, StopCompleted, nil, true, objects.OutcomeCompleted, "", ""},
		{"declined", StatusEnded, StopCompleted, refusal, true, objects.OutcomeDeclined, "", ""},
		{"declined by its status", StatusEnded, StopCompleted, []Event{ev(EventSessionStatus, "", time.Second, `{"status":"ended","stop_reason":"completed","detail":"refusal"}`)}, true, objects.OutcomeDeclined, "", ""},
		{"canceled", StatusEnded, StopCanceled, nil, true, objects.OutcomeCanceled, "session_canceled", ""},
		{"expired", StatusEnded, StopExpired, nil, true, objects.OutcomeInfraFailed, CodeSessionExpired, ""},
		{"failed with a code", StatusEnded, StopFailed, errorOf("machine_lost"), true, objects.OutcomeInfraFailed, "machine_lost", ""},
		{"failed with none", StatusEnded, StopFailed, nil, true, objects.OutcomeInfraFailed, CodeSessionFailed, ""},
		{"turn deadline", StatusIdle, StopTurnLimit, nil, true, objects.OutcomeTimedOut, "", StopCompleted},
		{"output limit", StatusIdle, StopOutputLimit, nil, true, objects.OutcomeTurnLimit, "", StopCompleted},
		{"repeated call", StatusIdle, StopError, errorOf("repeated_call"), true, objects.OutcomeTurnLimit, "", StopCompleted},
		{"budget", StatusIdle, StopBudget, nil, true, objects.OutcomeOverBudget, StopBudget, StopCompleted},
		{"spend refused", StatusIdle, StopError, errorOf("spend_exceeded"), true, objects.OutcomeOverBudget, "spend_exceeded", StopCompleted},
		{"model unavailable", StatusIdle, StopError, errorOf("model_unavailable"), true, objects.OutcomeInfraFailed, "model_unavailable", StopCompleted},
		{"a confirmation", StatusIdle, StopToolConfirmation, nil, true, objects.OutcomeInfraFailed, CodeConfirmationRequired, StopCompleted},
		{"a question", StatusIdle, StopQuestion, nil, true, objects.OutcomeInfraFailed, CodeQuestionUnanswered, StopCompleted},
		{"a computer away", StatusIdle, StopMachineWaiting, nil, true, objects.OutcomeInfraFailed, CodeMachineWaiting, StopCompleted},
		{"interrupted", StatusIdle, StopInterrupted, nil, true, objects.OutcomeCanceled, "session_interrupted", StopCanceled},
		{"end of turn while idle", StatusIdle, StopEndTurn, nil, true, objects.OutcomeCompleted, "", StopCompleted},
		{"declined while idle", StatusIdle, StopEndTurn, refusal, true, objects.OutcomeDeclined, "", StopCompleted},
		{"an unknown stop", StatusIdle, "something_new", nil, true, objects.OutcomeInfraFailed, "something_new", StopCompleted},
		{"no machine to work on", StatusEnded, StopCompleted, noMachine, true, objects.OutcomeInfraFailed, "machine_unavailable", ""},
		{"no machine while idle", StatusIdle, StopEndTurn, noMachine, true, objects.OutcomeInfraFailed, "machine_unavailable", StopCompleted},
		{"a model error the agent went on from", StatusIdle, StopEndTurn, errorOf("model_busy"), true, objects.OutcomeInfraFailed, "model_busy", StopCompleted},
		{"a machine replaced", StatusEnded, StopCompleted, machine("replaced"), true, objects.OutcomeInfraFailed, "replaced", ""},
		{"a machine attached", StatusEnded, StopCompleted, machine("attached"), true, objects.OutcomeCompleted, "", ""},
		{"an error that is the agent's", StatusEnded, StopCompleted, errorOf("repeated_call"), true, objects.OutcomeCompleted, "", ""},
		{"canceled with no machine", StatusEnded, StopCanceled, noMachine, true, objects.OutcomeCanceled, "session_canceled", ""},
		{"over budget with no machine", StatusIdle, StopBudget, noMachine, true, objects.OutcomeOverBudget, StopBudget, StopCompleted},
		{"failed with no machine", StatusEnded, StopFailed, noMachine, true, objects.OutcomeInfraFailed, "machine_unavailable", ""},
		{"running with no machine", StatusRunning, "", noMachine, false, "", "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, err := Read(Session{ID: "ses_1", Status: c.status, StopReason: c.stop}, c.events)
			if err != nil {
				t.Fatal(err)
			}
			code := ""
			if r.Failure != nil {
				code = r.Failure.Code
			}
			if r.Done != c.done || r.Outcome != c.outcome || code != c.code || r.End != c.end {
				t.Fatalf("done %v, outcome %q, code %q, end %q; want %v, %q, %q, %q", r.Done, r.Outcome, code, r.End, c.done, c.outcome, c.code, c.end)
			}
		})
	}
}

// TestReadTheLog: the turn's times are its first running status and its
// last stop, on the session's own thread; turns count the agent's answers
// there and tool calls every thread's; each model request is a call at
// the session's level, priced when Topos priced it and refused when it
// failed with no cost; the log's digest is its JSONL's; and a payload that
// does not parse is an error.
func TestReadTheLog(t *testing.T) {
	events := []Event{
		ev(EventSessionStatus, "", 0, `{"status":"running"}`),
		ev(EventSessionStatus, "thr_1", time.Second, `{"status":"running"}`),
		ev(EventModelRequest, "", 2*time.Second, `{"model":"m","outcome":"ok","cost_usd_micro":300,"usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":6,"cache_write_input_tokens":2,"reasoning_tokens":1}}`),
		ev(EventModelRequest, "thr_1", 3*time.Second, `{"model":"m","outcome":"error"}`),
		ev(EventAgentToolUse, "", 4*time.Second, `{}`),
		ev(EventAgentToolUse, "thr_1", 5*time.Second, `{}`),
		ev(EventAgentMessage, "thr_1", 6*time.Second, `{"stop_reason":"end_turn"}`),
		ev(EventAgentMessage, "", 7*time.Second, `{"stop_reason":"end_turn"}`),
		ev(EventSessionStatus, "", 9*time.Second, `{"status":"ended","stop_reason":"completed"}`),
	}
	r, err := Read(Session{Status: StatusEnded, StopReason: StopCompleted, Model: &Model{Name: "m", Reasoning: "low"}}, events)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case !r.StartedAt.Equal(t0) || !r.AgentEndedAt.Equal(t0.Add(9*time.Second)):
		t.Fatalf("times %v %v", r.StartedAt, r.AgentEndedAt)
	case r.Turns != 1 || r.ToolCalls != 2:
		t.Fatalf("turns %d, tool calls %d", r.Turns, r.ToolCalls)
	case len(r.Calls) != 2 || r.Calls[0].Cost != 300 || !r.Calls[0].Priced || r.Calls[0].SentReasoning != "low" ||
		r.Calls[0].Tokens.CachedInput != 6 || r.Calls[0].Tokens.CacheWrite != 2 || r.Calls[1].Status != "refused":
		t.Fatalf("calls %+v", r.Calls)
	case strings.Count(string(r.Log), "\n") != len(events) || !strings.HasPrefix(r.LogDigest, "sha256:") || len(r.LogDigest) != 71:
		t.Fatalf("log %d lines, digest %q", strings.Count(string(r.Log), "\n"), r.LogDigest)
	}
	failedWithCost := requestRecord{Model: "m", Outcome: "escalated", CostUSDMicro: new(int64(9))}.call("")
	if failedWithCost.Status != "failed" || failedWithCost.Cost != 9 {
		t.Fatalf("a priced request that did not end ok: %+v", failedWithCost)
	}
	for _, typ := range []string{EventSessionStatus, EventSessionError, EventSessionMachine, EventAgentMessage, EventModelRequest} {
		if _, err := Read(Session{Status: StatusEnded}, []Event{ev(typ, "", 0, `[`)}); err == nil {
			t.Fatalf("a %s that does not parse was read", typ)
		}
	}
}
