// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// scanBatch is how many trials a list with a value filter reads at a time
// while it looks for a page of matches.
const scanBatch = 500

// shortMaxBytes bounds a service's name and version, and a unit's name.
const shortMaxBytes = 63

// trialView is what the trials of one experiment are read against: its
// suite, each cell's task and candidate, the producers of each task's
// metrics, and each candidate's harness.
type trialView struct {
	res       objects.Resolved
	cells     map[string]objects.Cell
	producers map[string]map[string]string
	harness   map[string]string
	metrics   []string // trial-level metric digests, in the suite's order
}

// viewOf reads what an experiment's trials are read against.
func (s *Server) viewOf(r *http.Request, e objects.Experiment) (trialView, error) {
	res, err := s.experimentSuite(r, e)
	if err != nil {
		return trialView{}, err
	}
	v := trialView{res: res, cells: map[string]objects.Cell{}, producers: res.Producers(), harness: map[string]string{}}
	cells, err := s.Store.Experiments().AllCells(r.Context(), e.ID)
	if err != nil {
		return v, apierr.Internal(err)
	}
	for _, c := range cells {
		v.cells[c.ID] = c
	}
	versions, err := s.Store.Versions().Many(r.Context(), e.CandidateIDs)
	if err != nil {
		return v, apierr.Internal(err)
	}
	for _, cv := range versions {
		var c objects.Candidate
		if err := json.Unmarshal(cv.Body, &c); err != nil {
			return v, apierr.Internal(err)
		}
		if c.External != nil {
			v.harness[cv.Digest] = c.External.Harness
		}
	}
	for _, d := range res.Suite.Metrics {
		if res.Metrics[d].LevelOf() == objects.LevelTrial {
			v.metrics = append(v.metrics, d)
		}
	}
	return v, nil
}

// values are a trial's current values, by metric digest.
func (v trialView) values(t objects.Trial, ms []objects.Measurement) map[string]objects.Measurement {
	cell := v.cells[t.Cell]
	if !t.Finished() {
		return map[string]objects.Measurement{}
	}
	return objects.Current(ms, v.producers[cell.Task], v.metrics, v.harness[cell.Candidate])
}

// named renders current values by metric name, null for none.
func (v trialView) named(cur map[string]objects.Measurement) map[string]any {
	out := make(map[string]any, len(v.metrics))
	for _, d := range v.metrics {
		out[v.res.Metrics[d].Name] = nil
		if m, ok := cur[d]; ok {
			out[v.res.Metrics[d].Name] = m.Value
		}
	}
	return out
}

// valueMatch reads the value filter of a trial list: the metric's digest,
// and the value it keeps, or missing.
func valueMatch(raw string, m objects.Metric) (any, bool) {
	if raw == "missing" {
		return nil, true
	}
	switch m.Type {
	case objects.TypeBinary:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return raw, false
		}
		return b, false
	case objects.TypeLabel:
		if m.Open() {
			return objects.NormalizeLabel(raw), false
		}
		return raw, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return raw, false
	}
	return f, false
}

func (s *Server) listTrials(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	list := "trials/" + e.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	if (q.Get("metric") == "") != (q.Get("value") == "") {
		return apierr.OnField(apierr.CodeInvalidField, "metric", "metric and value are given together")
	}
	v, err := s.viewOf(r, e)
	if err != nil {
		return err
	}
	f := store.TrialFilter{State: q.Get("state"), Outcome: q.Get("outcome")}
	if f.Imported, err = boolParam(r, "imported"); err != nil {
		return err
	}
	if f.Retried, err = boolParam(r, "retried"); err != nil {
		return err
	}
	if cell, task, cand := q.Get("cell"), q.Get("task"), q.Get("candidate"); cell != "" || task != "" || cand != "" {
		f.Cells = []string{}
		for _, cl := range v.cells {
			if (cell == "" || cl.ID == cell) && (cand == "" || cl.Candidate == cand) &&
				(task == "" || cl.Task == task || v.res.Tasks[cl.Task].Name == task) {
				f.Cells = append(f.Cells, cl.ID)
			}
		}
	}
	var metric string
	var want any
	var missing bool
	if ref := q.Get("metric"); ref != "" {
		d, m, ok := v.res.MetricByRef(ref)
		if !ok || m.LevelOf() != objects.LevelTrial {
			return apierr.OnField(apierr.CodeInvalidField, "metric", "metric names a trial-level metric of the suite")
		}
		metric = d
		want, missing = valueMatch(q.Get("value"), m)
	}
	// A page without a value filter is one read; with one, trials are read
	// a batch at a time until a page of matches is found.
	var items []objects.Trial
	after, limit := p.After, p.Limit
	if metric != "" {
		limit = scanBatch
	}
	for {
		batch, err := s.Store.Experiments().Trials(r.Context(), e.ID, f, store.Page{After: after, Limit: limit})
		if err != nil {
			return apierr.Internal(err)
		}
		more := len(batch) > limit
		if more && metric != "" {
			batch = batch[:limit]
		}
		ids := make([]string, len(batch))
		for i, t := range batch {
			ids[i] = t.ID
		}
		ms, err := s.Store.Experiments().MeasurementsOf(r.Context(), ids)
		if err != nil {
			return apierr.Internal(err)
		}
		byTrial := map[string][]objects.Measurement{}
		for _, m := range ms {
			byTrial[m.Trial] = append(byTrial[m.Trial], m)
		}
		for _, t := range batch {
			cur := v.values(t, byTrial[t.ID])
			if metric != "" {
				got, has := cur[metric]
				if missing == has || (!missing && got.Value != want) {
					continue
				}
			}
			t.Values = v.named(cur)
			items = append(items, t)
		}
		if metric == "" || len(items) > p.Limit || !more {
			break
		}
		after = batch[len(batch)-1].ID
	}
	replyPage(w, list, p, items, func(t objects.Trial) string { return t.ID })
	return nil
}

// trialOf reads a trial and asks experiment.read of its experiment: a
// missing trial and a deny are trial_not_found.
func (s *Server) trialOf(r *http.Request, c access.Caller, tid string) (objects.Trial, objects.Experiment, error) {
	t, err := s.Store.Experiments().Trial(r.Context(), tid)
	if err != nil {
		return t, objects.Experiment{}, storeError(err, apierr.CodeTrialNotFound, "no trial has the id "+tid)
	}
	e, err := s.experimentAs(r, c, t.Experiment, authorizer.ActionExperimentRead, nil, apierr.CodeTrialNotFound)
	return t, e, err
}

func (s *Server) getTrial(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	t, _, err := s.trialOf(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	reply(w, http.StatusOK, t)
	return nil
}

// measurementFilter reads the filters every measurement list shares.
func (s *Server) measurementFilter(r *http.Request, e objects.Experiment) (store.MeasurementFilter, error) {
	q := r.URL.Query()
	f := store.MeasurementFilter{Grader: q.Get("grader"), Generation: q.Get("generation")}
	switch f.Generation {
	case "", "current", "all":
	default:
		if n, err := strconv.Atoi(f.Generation); err != nil || n < 1 {
			return f, apierr.OnField(apierr.CodeInvalidField, "generation", "generation is current, all, or a whole number from 1")
		}
	}
	if ref := q.Get("metric"); ref != "" {
		res, err := s.experimentSuite(r, e)
		if err != nil {
			return f, err
		}
		d, _, ok := res.MetricByRef(ref)
		if !ok {
			d = ref
		}
		f.Metric = d
	}
	return f, nil
}

func (s *Server) trialMeasurements(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	t, e, err := s.trialOf(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	list := "measurements/" + t.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	f, err := s.measurementFilter(r, e)
	if err != nil {
		return err
	}
	f.Trial = t.ID
	ms, err := s.Store.Experiments().Measurements(r.Context(), f, store.Page{After: p.After, Limit: p.Limit})
	if err != nil {
		return apierr.Internal(err)
	}
	replyPage(w, list, p, ms, func(m objects.Measurement) string { return m.ID })
	return nil
}

func (s *Server) experimentMeasurements(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	list := "measurements/" + e.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	f, err := s.measurementFilter(r, e)
	if err != nil {
		return err
	}
	f.Experiment, f.Candidate = e.ID, r.URL.Query().Get("candidate")
	if cell := r.URL.Query().Get("cell"); cell != "" {
		f.Cells = []string{cell}
	}
	ms, err := s.Store.Experiments().Measurements(r.Context(), f, store.Page{After: p.After, Limit: p.Limit})
	if err != nil {
		return apierr.Internal(err)
	}
	replyPage(w, list, p, ms, func(m objects.Measurement) string { return m.ID })
	return nil
}

func (s *Server) getMeasurement(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	mid := r.PathValue("id")
	m, err := s.Store.Experiments().Measurement(r.Context(), mid)
	if err != nil {
		return storeError(err, apierr.CodeMeasurementNotFound, "no measurement has the id "+mid)
	}
	if _, err := s.experimentAs(r, c, m.Experiment, authorizer.ActionExperimentRead, nil, apierr.CodeMeasurementNotFound); err != nil {
		return err
	}
	reply(w, http.StatusOK, m)
	return nil
}

// outputRef is one output an external harness names: a path the task
// names, and the object it wrote into the owner's Arca space. This release
// takes outputs as Arca references alone, recorded as declared and never
// read.
type outputRef struct {
	Path string           `json:"path"`
	Arca *objects.ArcaRef `json:"arca"`
}

// selfValue is one value a harness computed itself.
type selfValue struct {
	Metric    string `json:"metric"`
	Value     any    `json:"value"`
	Rationale string `json:"rationale"`
}

// runFields are what a submit and an import carry of one run.
type runFields struct {
	Outcome         string                          `json:"outcome"`
	Failure         *objects.Failure                `json:"failure"`
	StartedAt       *time.Time                      `json:"started_at"`
	AgentEndedAt    *time.Time                      `json:"agent_ended_at"`
	Outputs         []outputRef                     `json:"outputs"`
	LuxRequests     []string                        `json:"lux_requests"`
	ServiceVersions map[string]string               `json:"service_versions"`
	ServiceUsage    map[string]objects.ServiceUsage `json:"service_usage"`
	Declared        *objects.Declared               `json:"declared"`
	Measurements    []selfValue                     `json:"measurements"`
	Ref             string                          `json:"ref"`
}

// runContext is what a run is checked against: its task, its candidate,
// the suite and the producers of the task's metrics.
type runContext struct {
	owner     string
	task      objects.Task
	taskHash  string
	candidate objects.Candidate
	res       objects.Resolved
}

// checkRun holds a run to the rules of specs/003-api.md and answers what
// the store records of it.
func checkRun(rf runFields, rc runContext) (store.Outcome, error) {
	o := store.Outcome{Outcome: rf.Outcome, Failure: rf.Failure, Ref: rf.Ref, Declared: rf.Declared}
	switch {
	case !slices.Contains(objects.Outcomes(), rf.Outcome) || rf.Outcome == objects.OutcomeCanceled:
		return o, apierr.OnField(apierr.CodeInvalidField, "outcome", "outcome is any outcome but canceled, which only Examen sets")
	case (rf.Outcome == objects.OutcomeInfraFailed || rf.Outcome == objects.OutcomeOverBudget) && rf.Failure == nil:
		return o, apierr.OnField(apierr.CodeMissingField, "failure", "an "+rf.Outcome+" run says why in failure")
	case rf.Failure != nil && rf.Outcome != objects.OutcomeInfraFailed && rf.Outcome != objects.OutcomeOverBudget:
		return o, apierr.OnField(apierr.CodeInvalidField, "failure", "failure is for infra_failed and over_budget alone")
	case rf.Failure != nil && rf.Failure.Code == "":
		return o, apierr.OnField(apierr.CodeMissingField, "failure.code", "a failure names its code")
	case rf.StartedAt == nil:
		return o, apierr.OnField(apierr.CodeMissingField, "started_at", "a run names its first step's time")
	case rf.AgentEndedAt == nil:
		return o, apierr.OnField(apierr.CodeMissingField, "agent_ended_at", "a run names its end's time")
	case rf.AgentEndedAt.Before(*rf.StartedAt):
		return o, apierr.OnField(apierr.CodeInvalidField, "agent_ended_at", "the end is no earlier than the first step")
	case len(rf.Ref) > 255:
		return o, apierr.OnField(apierr.CodeInvalidField, "ref", "ref is at most 255 characters")
	}
	o.StartedAt, o.AgentEndedAt = *rf.StartedAt, *rf.AgentEndedAt
	seen := map[string]bool{}
	for i, out := range rf.Outputs {
		f := "outputs[" + itoa(i) + "]"
		switch {
		case !rc.task.HasOutput(out.Path):
			return o, apierr.OnField(apierr.CodeInvalidField, f+".path", "the task names no output "+out.Path)
		case seen[out.Path]:
			return o, apierr.OnField(apierr.CodeInvalidField, f+".path", "an output is named twice")
		case out.Arca == nil:
			return o, apierr.OnField(apierr.CodeMissingField, f+".arca", "this release takes an output as the object the harness wrote into the owner's Arca space, with its digest")
		case out.Arca.Path == "" || strings.HasPrefix(out.Arca.Path, "/") || slices.Contains(strings.Split(out.Arca.Path, "/"), ".."):
			return o, apierr.OnField(apierr.CodeInvalidField, f+".arca.path", "an Arca path is relative to the owner's space with no ..")
		case !id.ValidDigest(out.Arca.Digest):
			return o, apierr.OnField(apierr.CodeInvalidField, f+".arca.digest", "an Arca object is named with the sha256: digest of its content")
		}
		seen[out.Path] = true
		o.Evidence = append(o.Evidence, objects.EvidenceRef{Kind: objects.EvidenceOutput, Name: out.Path, Owner: rc.owner, Path: out.Arca.Path, Digest: out.Arca.Digest})
	}
	for i, req := range rf.LuxRequests {
		if req == "" || len(req) > 255 {
			return o, apierr.OnField(apierr.CodeInvalidField, "lux_requests["+itoa(i)+"]", "a request id is 1 to 255 characters")
		}
	}
	for k, v := range rf.ServiceVersions {
		if k == "" || len(k) > shortMaxBytes || v == "" || len(v) > shortMaxBytes {
			return o, apierr.OnField(apierr.CodeInvalidField, "service_versions", "a service's name and version are 1 to 63 characters")
		}
	}
	for k, u := range rf.ServiceUsage {
		if k == "" || len(k) > shortMaxBytes || u.CostUSDMicro < 0 {
			return o, apierr.OnField(apierr.CodeInvalidField, "service_usage", "a service's name is 1 to 63 characters and its cost at least 0")
		}
		for unit, n := range u.Units {
			if unit == "" || len(unit) > shortMaxBytes || n < 0 || math.IsNaN(n) || math.IsInf(n, 0) {
				return o, apierr.OnField(apierr.CodeInvalidField, "service_usage."+k+".units", "a unit's name is 1 to 63 characters and its count a finite number at least 0")
			}
		}
	}
	if d := rf.Declared; d != nil {
		switch {
		case len(d.HarnessVersion) > 255 || len(d.Image) > 255:
			return o, apierr.OnField(apierr.CodeInvalidField, "declared", "a declared value is at most 255 characters")
		case d.HarnessVersion != "" && d.HarnessVersion != rc.candidate.External.Version:
			return o, &apierr.Error{Code: apierr.CodeProvenanceMismatch, Field: "declared.harness_version", Fields: []string{"version"},
				Detail: "the run declares the harness version " + d.HarnessVersion + " and the candidate " + rc.candidate.External.Version}
		}
	}
	duration := o.AgentEndedAt.Sub(o.StartedAt).Seconds()
	o.Observed = objects.Observed{
		// Lux's records are read by no path of this release, so what the
		// run cost is unknown, never zero.
		CostState: objects.CostUnknown, Duration: &duration,
		ServiceVersions: rf.ServiceVersions, ServiceUsage: rf.ServiceUsage, LuxRequests: rf.LuxRequests,
	}
	o.Harness = rc.candidate.External.Harness
	o.Declined = rc.res.Suite.Lists(objects.BuiltinDigest(objects.MetricDeclined)) && rc.res.Suite.DeclineJudge == ""
	producers := rc.res.Producers()[rc.taskHash]
	measured := map[string]bool{}
	for i, mv := range rf.Measurements {
		f := "measurements[" + itoa(i) + "]"
		d, m, ok := rc.res.MetricByRef(mv.Metric)
		_, produced := producers[d]
		switch {
		case !ok:
			return o, apierr.OnField(apierr.CodeInvalidField, f+".metric", "the suite lists no metric "+mv.Metric)
		case m.LevelOf() != objects.LevelTrial:
			return o, apierr.OnField(apierr.CodeInvalidField, f+".metric", "a harness reports trial-level metrics alone")
		case produced:
			return o, apierr.OnField(apierr.CodeInvalidField, f+".metric", "a grader produces this metric for the task, so the harness may not report it")
		case measured[d]:
			return o, apierr.OnField(apierr.CodeInvalidField, f+".metric", "a metric is reported twice")
		case objects.Excluded(rf.Outcome):
			return o, apierr.OnField(apierr.CodeInvalidField, f, "a run that ended "+rf.Outcome+" is no finished trial and takes no value")
		case !m.Applies(rf.Outcome):
			return o, apierr.OnField(apierr.CodeInvalidField, f+".metric", "the metric does not apply to a run that ended "+rf.Outcome)
		case len(mv.Rationale) > limits.JudgeMaxRationaleBytes:
			return o, apierr.Exceeds("JUDGE_MAX_RATIONALE_BYTES", f+".rationale", "a rationale is at most JUDGE_MAX_RATIONALE_BYTES (4 KiB)")
		}
		value, why := objects.Value(m, mv.Value)
		if why != "" {
			return o, apierr.OnField(apierr.CodeInvalidField, f+".value", why)
		}
		measured[d] = true
		o.SelfReported = append(o.SelfReported, store.Measured{Metric: d, Value: value, Rationale: mv.Rationale})
	}
	return o, nil
}

// runContextOf reads what a trial's run is checked against.
func (s *Server) runContextOf(r *http.Request, e objects.Experiment, cell objects.Cell) (runContext, error) {
	res, err := s.experimentSuite(r, e)
	if err != nil {
		return runContext{}, err
	}
	cv, err := s.Store.Versions().ByID(r.Context(), objects.KindCandidate, cell.CandidateID)
	if err != nil {
		return runContext{}, apierr.Internal(err)
	}
	rc := runContext{owner: e.Owner, task: res.Tasks[cell.Task], taskHash: cell.Task, res: res}
	if err := json.Unmarshal(cv.Body, &rc.candidate); err != nil {
		return rc, apierr.Internal(err)
	}
	return rc, nil
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	tid := r.PathValue("id")
	t, err := s.Store.Experiments().Trial(r.Context(), tid)
	if err != nil {
		return storeError(err, apierr.CodeTrialNotFound, "no trial has the id "+tid)
	}
	body, err := readBody(r)
	if err != nil {
		return err
	}
	var in struct {
		Lease string `json:"lease"`
		runFields
	}
	if err := decodeBytes(body, &in); err != nil {
		return err
	}
	cell, err := s.Store.Experiments().Cell(r.Context(), t.Cell)
	if err != nil {
		return apierr.Internal(err)
	}
	e, err := s.Store.Experiments().Get(r.Context(), t.Experiment)
	if err != nil {
		return apierr.Internal(err)
	}
	rc, err := s.runContextOf(r, e, cell)
	if err != nil {
		return err
	}
	harness := ""
	if rc.candidate.External != nil {
		harness = rc.candidate.External.Harness
	}
	members := experimentMembers(e, c)
	members[authorizer.FieldCandidate], members[authorizer.FieldHarness] = cell.Candidate, harness
	members[authorizer.FieldTrial], members[authorizer.FieldLease], members[authorizer.FieldOutcome] = t.ID, in.Lease, in.Outcome
	if _, err := s.allowed(r, c, authorizer.ActionTrialSubmit, e.ID, members, apierr.CodeTrialNotFound, "no trial the caller may submit has the id "+tid); err != nil {
		return err
	}
	if in.Lease == "" {
		return apierr.OnField(apierr.CodeMissingField, "lease", "a submit names the lease the trial is held under")
	}
	if rc.candidate.External == nil {
		return apierr.OnField(apierr.CodeLeaseNotHeld, "lease", "the trial is not an external candidate's, and no lease holds it")
	}
	o, err := checkRun(in.runFields, rc)
	if err != nil {
		return err
	}
	out, err := s.Store.Experiments().Submit(r.Context(), store.Submission{Trial: t.ID, Lease: in.Lease, Subject: c.Subject, Digest: bodyDigest(body), Outcome: o})
	if err != nil {
		return storeError(err, apierr.CodeTrialNotFound, "the trial is gone")
	}
	s.advance(r, e.ID)
	reply(w, http.StatusOK, out)
	return nil
}

func (s *Server) importTrial(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var in struct {
		Executor    string     `json:"executor"`
		Task        string     `json:"task"`
		Candidate   string     `json:"candidate"`
		ExecutorRef string     `json:"executor_ref"`
		EndedAt     *time.Time `json:"ended_at"`
		runFields
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	eid := r.PathValue("id")
	extra := map[string]any{authorizer.FieldTask: in.Task, authorizer.FieldCandidate: in.Candidate, authorizer.FieldExecutor: in.Executor, authorizer.FieldExecutorRef: in.ExecutorRef}
	e, err := s.experimentAs(r, c, eid, authorizer.ActionExperimentImport, extra, apierr.CodeExperimentNotFound)
	if err != nil {
		return err
	}
	switch in.Executor {
	case "":
		return apierr.OnField(apierr.CodeMissingField, "executor", "an import names the executor its run came from")
	case objects.ExecutorTopos, objects.ExecutorCella:
		return &apierr.Error{Code: apierr.CodeCoreNotConfigured, Core: in.Executor, Field: "executor", Detail: "reading a finished " + in.Executor + " record needs " + in.Executor + ", which this release does not reach"}
	case objects.ExecutorExternal:
	default:
		return apierr.OnField(apierr.CodeInvalidField, "executor", "executor is external, topos or cella")
	}
	if in.ExecutorRef != "" {
		return apierr.OnField(apierr.CodeInvalidField, "executor_ref", "an external run names itself with ref; executor_ref names a session or sandbox")
	}
	_, taskDigest, err := objects.ParseRef("task", in.Task)
	if err != nil {
		return err
	}
	_, candDigest, err := objects.ParseRef("candidate", in.Candidate)
	if err != nil {
		return err
	}
	res, err := s.experimentSuite(r, e)
	if err != nil {
		return err
	}
	ci := slices.Index(e.Candidates, candDigest)
	if _, ok := res.Tasks[taskDigest]; !ok || ci < 0 {
		return apierr.OnField(apierr.CodeNotInExperiment, "task", "the task or the candidate is not part of the experiment")
	}
	cv, err := s.Store.Versions().ByID(r.Context(), objects.KindCandidate, e.CandidateIDs[ci])
	if err != nil {
		return apierr.Internal(err)
	}
	var cand objects.Candidate
	if err := json.Unmarshal(cv.Body, &cand); err != nil {
		return apierr.Internal(err)
	}
	if cand.Kind != objects.CandidateExternal {
		return apierr.OnField(apierr.CodeInvalidField, "candidate", "an external run is a candidate of kind external's")
	}
	switch {
	case in.EndedAt == nil:
		return apierr.OnField(apierr.CodeMissingField, "ended_at", "an imported run names its end")
	case in.AgentEndedAt != nil && in.EndedAt.Before(*in.AgentEndedAt):
		return apierr.OnField(apierr.CodeInvalidField, "ended_at", "the run's end is no earlier than its system under test's")
	case in.EndedAt.After(time.Now()):
		return apierr.OnField(apierr.CodeInvalidField, "ended_at", "an imported run ended no later than the request")
	case in.Declared == nil || in.Declared.HarnessVersion == "":
		return apierr.OnField(apierr.CodeMissingField, "declared.harness_version", "an imported run declares the harness version it ran")
	}
	rc := runContext{owner: e.Owner, task: res.Tasks[taskDigest], taskHash: taskDigest, candidate: cand, res: res}
	o, err := checkRun(in.runFields, rc)
	if err != nil {
		return err
	}
	o.EndedAt = in.EndedAt
	tuple, err := res.TupleOf(taskDigest, cand)
	if err != nil {
		return apierr.Internal(err)
	}
	cell := objects.Cell{Task: taskDigest, Candidate: candDigest, Tuple: tuple, TaskID: res.TaskIDs[taskDigest], CandidateID: cv.ID,
		TaskPos: slices.Index(res.Suite.Tasks, taskDigest), CandidatePos: ci}
	t, err := s.Store.Experiments().Import(r.Context(), store.Import{Experiment: e.ID, Cell: cell, Outcome: o})
	if err != nil {
		return storeError(err, apierr.CodeExperimentNotFound, "the experiment is gone")
	}
	w.Header().Set("Location", s.location("/trials/"+t.ID))
	reply(w, http.StatusCreated, t)
	return nil
}

// leasedTrial is a trial as a lease hands it to a harness: what it runs,
// and never a reference.
type leasedTrial struct {
	ID          string       `json:"id"`
	Cell        string       `json:"cell"`
	Index       int          `json:"index"`
	Task        taskName     `json:"task"`
	Instruction string       `json:"instruction,omitempty"`
	Inputs      *[]inputLink `json:"inputs,omitempty"`
	Outputs     []string     `json:"outputs"`
	Timeout     string       `json:"timeout"`
	MaxTurns    int          `json:"max_turns,omitempty"`
}

// inputLink is one presigned link to a task's input. This release mints
// none: a task with inputs is refused at an execute experiment's create.
type inputLink struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// taskName is a task by its name and digest.
type taskName struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// leaseAnswer is the answer of a lease or a renewal.
type leaseAnswer struct {
	Lease     string        `json:"lease"`
	ExpiresAt time.Time     `json:"expires_at"`
	Trials    []leasedTrial `json:"trials"`
}

// duration renders a duration in Go's notation without its trailing zero
// units: 30m, 2h, 1h30m.
func duration(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	if s == "" {
		return "0s"
	}
	return s
}

func (s *Server) lease(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var in struct {
		Candidate string `json:"candidate"`
		Max       *int   `json:"max"`
		Renew     string `json:"renew"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	eid := r.PathValue("id")
	switch {
	case in.Renew != "" && (in.Candidate != "" || in.Max != nil):
		return apierr.OnField(apierr.CodeInvalidField, "renew", "a renewal names its lease in place of candidate and max")
	case in.Renew == "" && in.Candidate == "":
		return apierr.OnField(apierr.CodeMissingField, "candidate", "a lease names the external candidate whose trials it takes")
	case in.Max != nil && *in.Max < 1:
		return apierr.OnField(apierr.CodeInvalidField, "max", "max is 1 or more")
	}
	e, err := s.Store.Experiments().Get(r.Context(), eid)
	if err != nil {
		return storeError(err, apierr.CodeExperimentNotFound, "no experiment has the id "+eid)
	}
	candidate := ""
	if in.Candidate != "" {
		if _, candidate, err = objects.ParseRef("candidate", in.Candidate); err != nil {
			return err
		}
	}
	var cand objects.Candidate
	if ci := slices.Index(e.Candidates, candidate); ci >= 0 {
		cv, err := s.Store.Versions().ByID(r.Context(), objects.KindCandidate, e.CandidateIDs[ci])
		if err != nil {
			return apierr.Internal(err)
		}
		if err := json.Unmarshal(cv.Body, &cand); err != nil {
			return apierr.Internal(err)
		}
	}
	harness := ""
	if cand.External != nil {
		harness = cand.External.Harness
	}
	members := experimentMembers(e, c)
	members[authorizer.FieldCandidate], members[authorizer.FieldHarness], members[authorizer.FieldLease] = candidate, harness, in.Renew
	if _, err := s.allowed(r, c, authorizer.ActionTrialLease, e.ID, members, apierr.CodeExperimentNotFound, "no experiment the caller may lease trials of has the id "+eid); err != nil {
		return err
	}
	if in.Renew != "" {
		held, err := s.Store.Experiments().Renew(r.Context(), e.ID, in.Renew, c.Subject)
		if err != nil {
			return storeError(err, apierr.CodeExperimentNotFound, "the experiment is gone")
		}
		return s.replyLease(w, r, held, false)
	}
	switch {
	case !slices.Contains(e.Candidates, candidate):
		return apierr.OnField(apierr.CodeNotInExperiment, "candidate", "the candidate is not one of the experiment's")
	case cand.Kind != objects.CandidateExternal:
		return apierr.OnField(apierr.CodeInvalidField, "candidate", "trials are leased of a candidate of kind external alone")
	case e.Mode == objects.ModeImport:
		return &apierr.Error{Code: apierr.CodeWrongMode, Detail: "lease: the experiment is in mode import, whose trials are imported", State: e.State}
	case e.Finished():
		return &apierr.Error{Code: apierr.CodeExperimentFinished, Detail: "lease: the experiment is " + e.State, State: e.State}
	}
	s.advance(r, e.ID)
	cells, err := s.Store.Experiments().Leasable(r.Context(), e.ID, candidate)
	if err != nil {
		return apierr.Internal(err)
	}
	allowed := make([]string, 0, len(cells))
	if s.Engine != nil {
		if allowed, err = s.Engine.Allowed(r.Context(), e, cells); err != nil {
			return apierr.Internal(err)
		}
	} else {
		for _, cl := range cells {
			allowed = append(allowed, cl.ID)
		}
	}
	req := store.LeaseRequest{Experiment: e.ID, Candidate: candidate, Subject: c.Subject, Cells: allowed}
	if in.Max != nil {
		req.Max = *in.Max
	}
	held, err := s.Store.Experiments().Lease(r.Context(), req)
	if err != nil {
		return storeError(err, apierr.CodeExperimentNotFound, "the experiment is gone")
	}
	if len(held.Trials) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	return s.replyLease(w, r, held, true)
}

// replyLease answers a lease with each trial's task, and on a new lease
// its input links, of which this release delivers none: a task with inputs
// is refused at create.
func (s *Server) replyLease(w http.ResponseWriter, r *http.Request, held store.Leased, links bool) error {
	cells := map[string]objects.Cell{}
	var taskIDs []string
	for _, t := range held.Trials {
		if _, ok := cells[t.Cell]; ok {
			continue
		}
		cl, err := s.Store.Experiments().Cell(r.Context(), t.Cell)
		if err != nil {
			return apierr.Internal(err)
		}
		cells[t.Cell] = cl
		taskIDs = append(taskIDs, cl.TaskID)
	}
	versions, err := s.Store.Versions().Many(r.Context(), taskIDs)
	if err != nil {
		return apierr.Internal(err)
	}
	out := leaseAnswer{Lease: held.Lease, ExpiresAt: held.ExpiresAt, Trials: []leasedTrial{}}
	for _, t := range held.Trials {
		cl := cells[t.Cell]
		var task objects.Task
		if err := json.Unmarshal(versions[cl.TaskID].Body, &task); err != nil {
			return apierr.Internal(err)
		}
		lt := leasedTrial{ID: t.ID, Cell: t.Cell, Index: t.Index, Task: taskName{Name: task.Name, Digest: cl.Task},
			Instruction: task.Instruction, Outputs: task.Outputs, Timeout: duration(task.TimeoutOf()), MaxTurns: task.MaxTurns}
		if lt.Outputs == nil {
			lt.Outputs = []string{}
		}
		if links {
			lt.Inputs = &[]inputLink{}
		}
		out.Trials = append(out.Trials, lt)
	}
	reply(w, http.StatusOK, out)
	return nil
}
