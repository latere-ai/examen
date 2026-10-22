// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/usage"
)

// The failure codes the executor gives a run that ended for a reason
// outside the agent and names no session.error code of its own.
const (
	CodeSessionFailed        = "session_failed"
	CodeSessionExpired       = "session_expired"
	CodeConfirmationRequired = "confirmation_required"
	CodeQuestionUnanswered   = "question_unanswered"
	CodeMachineWaiting       = "machine_waiting"
)

// The session.error codes an outcome other than infra_failed is read
// from: a stop for repeating a failing call is the agent's own, and a
// Budget that ran out is over_budget.
const (
	errRepeatedCall    = "repeated_call"
	errBudgetExhausted = "budget_exhausted"
	errSpendExceeded   = "spend_exceeded"
)

// infraErrors are the session.error codes that make a run infra_failed
// wherever they occur in its log, though the agent went on and the session
// ended as if the turn were whole: the agent then worked without the
// machine or a service its run needs, so the outcome is not the agent's.
var infraErrors = map[string]bool{
	"machine_lost":                 true,
	objects.CodeMachineUnavailable: true,
	"model_unavailable":            true,
	"model_busy":                   true,
	"model_error":                  true,
	"files_unavailable":            true,
	"authorizer_unavailable":       true,
	"agent_identity_missing":       true,
}

// machineReplaced is the session.machine reason of a new, empty machine in
// the place of a lost one: the working directory the agent built is gone.
const machineReplaced = "replaced"

// Report is what a session's state and log make of its trial
// (specs/005-execution.md, the `topos` outcome table).
type Report struct {
	// Done is whether the trial reached its outcome: the session ended, or
	// stopped idle short of its end, which no person attends to resume.
	Done    bool
	Outcome string
	Failure *objects.Failure
	// End is the reason to end a session that stopped idle, completed or
	// canceled; "" for a session that ended itself.
	End string
	// StartedAt is the turn's start and AgentEndedAt its end.
	StartedAt, AgentEndedAt time.Time
	// Calls are the session's model requests as the layers read them.
	Calls []usage.Call
	// Turns are the agent's responses on the session's own thread, and
	// ToolCalls every call it made, on every thread.
	Turns, ToolCalls int
	// Log is the session's log as JSONL, one event a line, through its
	// last event; LogDigest its SHA-256.
	Log       []byte
	LogDigest string
	// Machines are the machines the session ran on, each once, in the
	// order its session.machine events attached them.
	Machines []objects.Machine
}

// requestRecord is what a model.request carries that the layers read.
type requestRecord struct {
	Model        string `json:"model"`
	Outcome      string `json:"outcome"`
	CostUSDMicro *int64 `json:"cost_usd_micro"`
	Usage        *struct {
		InputTokens  int64  `json:"input_tokens"`
		OutputTokens int64  `json:"output_tokens"`
		CacheRead    *int64 `json:"cache_read_input_tokens"`
		CacheWrite   *int64 `json:"cache_write_input_tokens"`
		Reasoning    int64  `json:"reasoning_tokens"`
	} `json:"usage"`
}

// Read reads a session and its log as a trial's report. A session that
// is running, or idle before its first turn, is not done.
func Read(s Session, events []Event) (Report, error) {
	r := Report{StartedAt: s.CreatedAt, AgentEndedAt: s.UpdatedAt}
	h := sha256.New()
	reasoning := ""
	if s.Model != nil {
		reasoning = s.Model.Reasoning
	}
	// infra is the first code in the log that makes the run infra_failed.
	var lastError, lastDetail, lastStop, infra string
	turned := false
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			return r, fmt.Errorf("topos: encode event %d: %w", e.Seq, err)
		}
		r.Log = append(append(r.Log, line...), '\n')
		switch e.Type {
		case EventSessionStatus:
			var p struct {
				Status     string `json:"status"`
				StopReason string `json:"stop_reason"`
				Detail     string `json:"detail"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return r, fmt.Errorf("topos: decode session.status %d: %w", e.Seq, err)
			}
			if e.Thread != "" {
				continue
			}
			if p.Status == StatusRunning && !turned {
				r.StartedAt, turned = e.Time, true
			}
			if p.Status != StatusRunning {
				r.AgentEndedAt, lastDetail = e.Time, p.Detail
			}
		case EventSessionError:
			var p struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return r, fmt.Errorf("topos: decode session.error %d: %w", e.Seq, err)
			}
			lastError = p.Code
			if infraErrors[p.Code] && infra == "" {
				infra = p.Code
			}
		case EventSessionMachine:
			var p struct {
				Reason  string `json:"reason"`
				Machine struct {
					Kind string `json:"kind"`
					ID   string `json:"id"`
				} `json:"machine"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return r, fmt.Errorf("topos: decode session.machine %d: %w", e.Seq, err)
			}
			if m := (objects.Machine{Kind: p.Machine.Kind, ID: p.Machine.ID}); m.ID != "" && !slices.Contains(r.Machines, m) {
				r.Machines = append(r.Machines, m)
			}
			if p.Reason == machineReplaced && infra == "" {
				infra = machineReplaced
			}
		case EventAgentMessage:
			var p struct {
				StopReason string `json:"stop_reason"`
			}
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return r, fmt.Errorf("topos: decode agent.message %d: %w", e.Seq, err)
			}
			if e.Thread == "" {
				r.Turns++
				lastStop = p.StopReason
			}
		case EventAgentToolUse:
			r.ToolCalls++
		case EventModelRequest:
			var p requestRecord
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return r, fmt.Errorf("topos: decode model.request %d: %w", e.Seq, err)
			}
			r.Calls = append(r.Calls, p.call(reasoning))
		}
	}
	h.Write(r.Log)
	r.LogDigest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	refused := lastDetail == "refusal" || lastStop == "refusal"
	if s.Status == StatusRunning {
		return r, nil
	}
	outcome(&r, s, lastError, refused)
	if infra != "" && r.Done {
		switch r.Outcome {
		case objects.OutcomeCanceled, objects.OutcomeOverBudget, objects.OutcomeInfraFailed:
		default:
			r.Outcome, r.Failure = objects.OutcomeInfraFailed, &objects.Failure{Code: infra, Detail: "the session met " + infra + " and the agent went on without what failed"}
		}
	}
	return r, nil
}

// outcome reads a session that is no longer running as its trial's
// outcome by the stop it made: refused is whether the agent's last answer
// refused, and lastError the last session.error code in its log.
func outcome(r *Report, s Session, lastError string, refused bool) {
	fail := func(code string) {
		if code == "" {
			code = CodeSessionFailed
		}
		r.Outcome, r.Failure = objects.OutcomeInfraFailed, &objects.Failure{Code: code, Detail: "the session stopped " + s.StopReason}
	}
	switch s.Status {
	case StatusEnded:
		r.Done = true
		switch s.StopReason {
		case StopCompleted:
			r.Outcome = objects.OutcomeCompleted
			if refused {
				r.Outcome = objects.OutcomeDeclined
			}
		case StopCanceled:
			r.Outcome, r.Failure = objects.OutcomeCanceled, &objects.Failure{Code: "session_canceled", Detail: "the session was canceled at Topos"}
		case StopExpired:
			fail(CodeSessionExpired)
		default:
			fail(lastError)
		}
		return
	}
	// Idle: the turn stopped short of its end, and no person attends the
	// run to resume it, so the stop is its outcome and the session is
	// ended after it.
	r.Done, r.End = true, StopCompleted
	switch s.StopReason {
	case "":
		r.Done, r.End = false, ""
	case StopEndTurn:
		r.Outcome = objects.OutcomeCompleted
		if refused {
			r.Outcome = objects.OutcomeDeclined
		}
	case StopTurnLimit:
		r.Outcome = objects.OutcomeTimedOut
	case StopOutputLimit:
		r.Outcome = objects.OutcomeTurnLimit
	case StopBudget:
		r.Outcome, r.Failure = objects.OutcomeOverBudget, &objects.Failure{Code: StopBudget, Detail: "the session's budget ran out"}
	case StopInterrupted:
		r.Outcome, r.Failure, r.End = objects.OutcomeCanceled, &objects.Failure{Code: "session_interrupted", Detail: "the session was interrupted at Topos"}, StopCanceled
	case StopToolConfirmation:
		fail(CodeConfirmationRequired)
	case StopQuestion:
		fail(CodeQuestionUnanswered)
	case StopMachineWaiting:
		fail(CodeMachineWaiting)
	case StopError:
		switch lastError {
		case errRepeatedCall:
			r.Outcome = objects.OutcomeTurnLimit
		case errBudgetExhausted, errSpendExceeded:
			r.Outcome, r.Failure = objects.OutcomeOverBudget, &objects.Failure{Code: lastError, Detail: "a Budget the session's Keys draw on ran out"}
		default:
			fail(lastError)
		}
	default:
		fail(s.StopReason)
	}
}

// call is a model request as the layers read it: the model it named,
// served by that model, its tokens, and its cost when Topos priced it. A
// request that failed with no cost recorded spent nothing.
func (p requestRecord) call(reasoning string) usage.Call {
	c := usage.Call{SentModel: p.Model, SentReasoning: reasoning, Served: p.Model, Status: "ok", Currency: usage.Currency}
	if p.Usage != nil {
		c.Tokens = usage.Tokens{Input: p.Usage.InputTokens, Output: p.Usage.OutputTokens, Reasoning: p.Usage.Reasoning}
		if p.Usage.CacheRead != nil {
			c.Tokens.CachedInput = *p.Usage.CacheRead
		}
		if p.Usage.CacheWrite != nil {
			c.Tokens.CacheWrite = *p.Usage.CacheWrite
		}
	}
	switch {
	case p.CostUSDMicro != nil:
		c.Cost, c.Priced = *p.CostUSDMicro, true
		if p.Outcome != "ok" {
			c.Status = "failed"
		}
	case p.Outcome != "ok":
		c.Status = "refused"
	}
	return c
}
