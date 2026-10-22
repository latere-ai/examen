// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"latere.ai/x/pkg/llmdialect/lux"
	"latere.ai/x/pkg/otel"

	"latere.ai/x/examen/internal/grade"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
)

// generatePath is Lux's native door under its root.
const generatePath = "/lux/v1/generate"

// luxAnswerLimit bounds one answer of Lux: a judgment's tool call.
const luxAnswerLimit = 4 << 20

// The codes of Lux's refusals a judgment ends on (specs/006-grading.md,
// "Judgment states").
var (
	spendCodes   = []string{"budget_exhausted", "spend_exceeded"}
	gatewayCodes = []string{"model_not_allowed", "model_not_found", "model_unpriced", "key_disabled", "door_not_allowed", "unauthorized", "forbidden"}
)

// luxError is a refusal of Lux: its status, code and request id.
type luxError struct {
	status  int
	code    string
	request string
}

func (e *luxError) Error() string { return fmt.Sprintf("lux: refused %d %s", e.status, e.code) }

// errLuxUnavailable is Lux not answering, or answering an outage.
var errLuxUnavailable = errors.New("lux: unavailable")

// runJudge runs one judgment of a trial (specs/006-grading.md, "Model
// judges"): the evidence its grader names read through the trial's
// evidence and checked, assembled into one structured call through Lux on
// a Key the key source leases for the judge, its answer validated and
// tried again up to JUDGE_MAX_ATTEMPTS when it is malformed.
func (e *Engine) runJudge(ctx context.Context, row objects.Grading) error {
	rec := objects.Judgment{Grader: row.Grader, Attempts: []objects.Attempt{}, Cuts: []objects.Cut{}}
	end := func(state, reason string) store.GradingEnd {
		rec.State, rec.Reason = state, reason
		return store.GradingEnd{ID: row.ID, State: state, Record: &rec}
	}
	wait := func(cause error) error {
		return e.later(ctx, row, func(reason string) store.GradingEnd { return end(objects.CheckNotRun, reason) }, cause)
	}
	r, err := e.runOfTrial(ctx, row.Trial, tokens.PurposeJudge)
	why, ended := signInEnded(err)
	switch {
	case errors.Is(err, errEvidenceChanged):
		return e.Store.Gradings().End(ctx, end(objects.JudgmentEvidenceChanged, err.Error()))
	case ended:
		return e.Store.Gradings().End(ctx, end(objects.CheckNotRun, why))
	case outage(err):
		return wait(err)
	case err != nil:
		return err
	}
	g, gid, err := e.graderOf(ctx, r.exp.Owner, row.Grader)
	if err != nil {
		return err
	}
	var produces []grade.Produced
	for _, p := range produced(g, r.metrics) {
		if p.Metric.Applies(r.trial.Outcome) {
			produces = append(produces, p)
		}
	}
	if len(produces) == 0 || g.ModelJudge == nil {
		return e.Store.Gradings().End(ctx, end(objects.CheckNotRun, "no metric the judge produces applies to a trial of this outcome"))
	}
	ev, err := e.judgeEvidence(ctx, r, g.ModelJudge.Inputs)
	var req grade.Request
	if err == nil {
		// The workspace's files are read as the assembly's bound reaches
		// them, so a file that changed or a store that does not answer is
		// met here.
		req, err = grade.Assemble(g, row.Grader, r.trial.ID, produces, ev, limits.JudgeMaxInputBytes)
	}
	why, ended = signInEnded(err)
	switch {
	case errors.Is(err, errEvidenceChanged):
		return e.Store.Gradings().End(ctx, end(objects.JudgmentEvidenceChanged, err.Error()))
	case ended:
		return e.Store.Gradings().End(ctx, end(objects.CheckNotRun, why))
	case outage(err):
		return wait(err)
	case err != nil:
		return err
	}
	rec.RequestDigest, rec.Boundary, rec.Cuts = req.Digest, req.Boundary, append(rec.Cuts, req.Cuts...)
	lease, err := e.Keys.Lease(ctx, keys.Request{
		Owner: r.exp.Owner, Subject: r.exp.CreatedBy, Experiment: r.exp.ID, Purpose: keys.PurposeJudge,
		Models: []string{g.ModelJudge.Model}, Surface: "lux", TTL: limits.MaxTrialTimeout,
		Labels: map[string]string{keys.LabelExperiment: r.exp.ID, "examen.grader": gid},
	})
	var refused *keys.RefusedError
	switch {
	case errors.Is(err, keys.ErrBudgetExhausted):
		out := end(objects.JudgmentOverBudget, "the key source has no budget left for the judge")
		out.Park, out.ParkReason = objects.StateOverBudget, "budget_exhausted: "+r.trial.ID+": judge "+row.Grader
		return e.Store.Gradings().End(ctx, out)
	case errors.As(err, &refused):
		return e.Store.Gradings().End(ctx, end(objects.JudgmentGatewayRefused, "the key source refused the judge's key: "+refused.Reason))
	case errors.Is(err, keys.ErrUnavailable):
		return wait(err)
	case err != nil:
		return err
	}
	if err := e.Store.Gradings().HoldKey(ctx, r.exp.ID, row.Grader, lease.ID); err != nil {
		return err
	}
	var cost int64
	priced := true
	for range limits.JudgeMaxAttempts {
		resp, requestID, err := e.callJudge(ctx, lease.Value, r.trial.ID, req.Body)
		var lerr *luxError
		switch {
		case errors.Is(err, errLuxUnavailable):
			// An outage counts against no attempt: the judgment is taken
			// again once Lux answers.
			return wait(err)
		case errors.As(err, &lerr) && slices.Contains(spendCodes, lerr.code):
			rec.Attempts = append(rec.Attempts, objects.Attempt{LuxRequest: lerr.request, State: objects.JudgmentOverBudget})
			out := end(objects.JudgmentOverBudget, "Lux refused the call for spend: "+lerr.code)
			out.Park, out.ParkReason = objects.StateOverBudget, "budget_exhausted: "+r.trial.ID+": judge "+row.Grader
			return e.Store.Gradings().End(ctx, out)
		case errors.As(err, &lerr) && (slices.Contains(gatewayCodes, lerr.code) || lerr.status < 500):
			rec.Attempts = append(rec.Attempts, objects.Attempt{LuxRequest: lerr.request, State: objects.JudgmentGatewayRefused})
			return e.Store.Gradings().End(ctx, end(objects.JudgmentGatewayRefused, "Lux refused the call: "+lerr.Error()))
		case err != nil:
			return err
		}
		rec.ServedModel = resp.Model
		rec.Tokens.Input += resp.Usage.InputTokens
		rec.Tokens.Output += resp.Usage.OutputTokens
		if resp.Usage.CostMicroUSD != nil {
			cost += *resp.Usage.CostMicroUSD
		} else {
			priced = false
		}
		if priced {
			c := cost
			rec.Cost = &c
		}
		answer := grade.ReadAnswer(resp, produces)
		rec.Attempts = append(rec.Attempts, objects.Attempt{LuxRequest: requestID, State: answer.State})
		switch answer.State {
		case objects.JudgmentOK:
			out := end(objects.JudgmentOK, "")
			out.Values = answer.Values
			return e.Store.Gradings().End(ctx, out)
		case objects.JudgmentRefused:
			return e.Store.Gradings().End(ctx, end(objects.JudgmentRefused, answer.Reason))
		}
		rec.Reason = answer.Reason
		e.log().InfoContext(ctx, "a judgment's answer was malformed", "trial", r.trial.ID, "grader", row.Grader, "reason", answer.Reason)
	}
	return e.Store.Gradings().End(ctx, end(objects.JudgmentMalformed, rec.Reason))
}

// callJudge sends one judgment's call to Lux's door on the judge's Key,
// labeled with the trial, and answers the response and Lux's request id.
func (e *Engine) callJudge(ctx context.Context, key, trial string, body []byte) (lux.Response, string, error) {
	g := e.Grading
	ctx, cancel := context.WithTimeout(ctx, holdJudge/time.Duration(limits.JudgeMaxAttempts+1))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(g.LuxURL, "/")+generatePath, bytes.NewReader(body))
	if err != nil {
		return lux.Response{}, "", fmt.Errorf("work: build the judgment's call: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Lux-Labels", keys.LabelTrial+"="+trial)
	client := g.HTTP
	if client == nil {
		client = otel.HTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return lux.Response{}, "", fmt.Errorf("%w: %w", errLuxUnavailable, scrubURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, luxAnswerLimit))
	if err != nil {
		return lux.Response{}, "", fmt.Errorf("%w: read the answer: %w", errLuxUnavailable, err)
	}
	requestID := resp.Header.Get("Lux-Request-Id")
	switch {
	case resp.StatusCode == http.StatusOK:
		var out lux.Response
		if err := json.Unmarshal(raw, &out); err != nil {
			return lux.Response{}, requestID, fmt.Errorf("%w: the answer does not parse", errLuxUnavailable)
		}
		if requestID == "" {
			requestID = out.ID
		}
		return out, requestID, nil
	case resp.StatusCode == http.StatusTooManyRequests && !hasCode(raw, spendCodes), resp.StatusCode >= 500:
		return lux.Response{}, requestID, fmt.Errorf("%w: answered %d", errLuxUnavailable, resp.StatusCode)
	}
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				RequestID string `json:"request_id"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &env)
	if requestID == "" {
		requestID = env.Error.Details.RequestID
	}
	return lux.Response{}, requestID, &luxError{status: resp.StatusCode, code: env.Error.Code, request: requestID}
}

// hasCode reports whether an error envelope's code is one of codes.
func hasCode(raw []byte, codes []string) bool {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(raw, &env) == nil && slices.Contains(codes, env.Error.Code)
}

// judgeEvidence reads the inputs a judge names: the final message, the
// transcript, the check values and the outputs. The one output a topos
// trial keeps is workspace/, the kept copy of its working directory:
// named whole, its UTF-8 text files in path order and a listing of the
// rest; named by one file, that file.
func (e *Engine) judgeEvidence(ctx context.Context, r gradedRun, in objects.JudgeInputs) (grade.Evidence, error) {
	var ev grade.Evidence
	if in.FinalMessage {
		s := r.ev.FinalMessage
		ev.FinalMessage = &s
	}
	if in.Transcript {
		s := string(r.ev.Log)
		ev.Transcript = &s
	}
	if in.Checks {
		s, err := e.checkValues(ctx, r)
		if err != nil {
			return ev, err
		}
		ev.Checks = &s
	}
	for _, f := range in.Files {
		ev.Files = append(ev.Files, grade.Block{Name: f, Text: ""})
	}
	if len(in.Outputs) == 0 {
		return ev, nil
	}
	token, err := e.workToken(ctx, r, tokens.PurposeJudge)
	if err != nil {
		return ev, err
	}
	kept := keptWork(r.ev.Work)
	for _, o := range in.Outputs {
		out := grade.Output{Name: o}
		rel, isWork := strings.CutPrefix(o, "workspace/")
		for i, f := range r.ev.Work {
			if !isWork || (rel != "" && f.Path != rel) {
				continue
			}
			digest := "sha256:" + f.SHA256
			if !kept[i] {
				out.Listing = append(out.Listing, grade.Listed{Path: f.Path, Size: f.Size, Digest: digest, Why: grade.WhyNotKept})
				continue
			}
			// The file is read only if the judge's bound reaches it, and
			// only as much of it as the bound has room for is kept; the
			// rest passes through to have its digest checked.
			out.Files = append(out.Files, grade.Block{Name: "workspace/" + f.Path, Size: f.Size, Digest: digest, Load: func(keep int) (string, bool, error) {
				h := &headOf{n: keep}
				if err := e.streamWork(ctx, token, r, f, h); err != nil {
					return "", false, err
				}
				head, isText := h.text(int64(keep) >= f.Size)
				return head, isText, nil
			}})
		}
		ev.Outputs = append(ev.Outputs, out)
	}
	return ev, nil
}

// text reports whether bytes are UTF-8 text: valid UTF-8 with no NUL.
func text(b []byte) bool { return utf8.Valid(b) && !bytes.Contains(b, []byte{0}) }

// checkValues is the values the trial's checks recorded, by metric name,
// as one JSON object: each check's latest generation.
func (e *Engine) checkValues(ctx context.Context, r gradedRun) (string, error) {
	ms, err := e.Store.Experiments().MeasurementsOf(ctx, []string{r.trial.ID})
	if err != nil {
		return "", err
	}
	latest := map[string]int{}
	for _, m := range ms {
		if slices.Contains(r.task.Checks, m.Grader) && m.Annotator == "" {
			latest[m.Grader] = max(latest[m.Grader], m.Generation)
		}
	}
	values := map[string]any{}
	for _, m := range ms {
		if gen, ok := latest[m.Grader]; ok && m.Generation == gen && m.Annotator == "" {
			if metric, ok := r.metrics[m.Metric]; ok {
				values[metric.Name] = m.Value
			}
		}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("work: render the check values: %w", err)
	}
	return string(raw), nil
}

// scrubURL is a transport error without the request's address.
func scrubURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
