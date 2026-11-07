// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	ownertoken "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/topos"
)

// experimentRoutes are the routes of experiments and what they hold.
func experimentRoutes() []route {
	return []route{
		{method: http.MethodPost, path: "/experiments", idempotent: true, handle: (*Server).createExperiment},
		{method: http.MethodGet, path: "/experiments", handle: (*Server).listExperiments},
		{method: http.MethodGet, path: "/experiments/{id}", handle: (*Server).getExperiment},
		{method: http.MethodGet, path: "/experiments/{id}/grid", handle: (*Server).grid},
		{method: http.MethodPost, path: "/experiments/{id}/cancel", handle: (*Server).cancelExperiment},
		{method: http.MethodPost, path: "/experiments/{id}/close", handle: (*Server).closeExperiment},
		{method: http.MethodGet, path: "/experiments/{id}/events", handle: (*Server).events},
		{method: http.MethodGet, path: "/experiments/{id}/stream", handle: (*Server).stream},
		{method: http.MethodGet, path: "/experiments/{id}/cells", handle: (*Server).listCells},
		{method: http.MethodGet, path: "/cells/{id}", handle: (*Server).getCell},
		{method: http.MethodPost, path: "/cells/{id}/retry", handle: (*Server).retryCell},
		{method: http.MethodGet, path: "/experiments/{id}/trials", handle: (*Server).listTrials},
		{method: http.MethodGet, path: "/trials/{id}", handle: (*Server).getTrial},
		{method: http.MethodPost, path: "/experiments/{id}/imports", idempotent: true, handle: (*Server).importTrial},
		{method: http.MethodPost, path: "/experiments/{id}/work/lease", idempotent: true, handle: (*Server).lease,
			secret: "model_key", refill: (*Server).leaseKey},
		{method: http.MethodPost, path: "/trials/{id}/submit", handle: (*Server).submit},
		{method: http.MethodGet, path: "/trials/{id}/measurements", handle: (*Server).trialMeasurements},
		{method: http.MethodGet, path: "/experiments/{id}/measurements", handle: (*Server).experimentMeasurements},
		{method: http.MethodGet, path: "/measurements/{id}", handle: (*Server).getMeasurement},
		{method: http.MethodGet, path: "/experiments/{id}/comparisons", handle: (*Server).experimentComparisons},
		{method: http.MethodGet, path: "/experiments/{id}/declared", handle: (*Server).declaredAnalysis},
		{method: http.MethodPost, path: "/comparisons", idempotent: true, handle: (*Server).requestComparison},
		{method: http.MethodGet, path: "/comparisons", handle: (*Server).listComparisons},
		{method: http.MethodGet, path: "/comparisons/{id}", handle: (*Server).getComparison},
		{method: http.MethodGet, path: "/comparisons/{id}/rows", handle: (*Server).comparisonRows},
	}
}

// experimentMembers are the fields every question about a stored
// experiment sends.
func experimentMembers(e objects.Experiment, c access.Caller) map[string]any {
	m := map[string]any{authorizer.FieldOwner: e.Owner, authorizer.FieldSuite: e.Suite, authorizer.FieldState: e.State, authorizer.FieldMode: e.Mode}
	if e.Owner != c.Subject {
		m[authorizer.FieldShared] = false
	}
	return m
}

// experimentAs reads an experiment and asks an action of it with extra
// fields: a missing experiment and a deny are both miss, the 404 of what
// the request named, which for a cell, trial, measurement or comparison is
// that object and never its experiment.
func (s *Server) experimentAs(r *http.Request, c access.Caller, eid, action string, extra map[string]any, miss missing) (objects.Experiment, error) {
	e, err := s.Store.Experiments().Get(r.Context(), eid)
	if err != nil {
		return e, storeError(err, miss)
	}
	m := experimentMembers(e, c)
	maps.Copy(m, extra)
	_, err = s.allowed(r, c, action, e.ID, m, miss)
	return e, err
}

// readExperiment reads an experiment the caller may read.
func (s *Server) readExperiment(r *http.Request, c access.Caller, eid string) (objects.Experiment, error) {
	return s.experimentAs(r, c, eid, authorizer.ActionExperimentRead, nil, missing{apierr.CodeExperimentNotFound, eid})
}

// createBody is the body of POST /experiments.
type createBody struct {
	Suite      string   `json:"suite"`
	Candidates []string `json:"candidates"`
	Trials     *int     `json:"trials"`
	Budget     *struct {
		Name        *string `json:"name"`
		CapUSDMicro *int64  `json:"cap_usd_micro"`
	} `json:"budget"`
	Parallelism *int `json:"parallelism"`
	Reference   *struct {
		Baseline  string `json:"baseline"`
		Candidate string `json:"candidate"`
	} `json:"reference"`
	Mode         string            `json:"mode"`
	Scheduling   string            `json:"scheduling"`
	Comparisons  []declaredBody    `json:"comparisons"`
	Independence *independenceBody `json:"independence"`
	Metadata     map[string]string `json:"metadata"`
	// SignIn is an opaque value the client's installation gave it for this
	// create, passed to the authorizer on experiment.create and kept
	// nowhere: not on the experiment, in an answer, an event or a log.
	SignIn *string `json:"sign_in"`
}

// declaredBody is one comparison a create declares: a pair of candidates,
// with an optional variable; one candidate's pair of tasks by name or
// digest; or the general form, a cell variable and its two levels with an
// optional where and by. Each takes an optional metric.
type declaredBody struct {
	Candidate          string            `json:"candidate"`
	ReferenceCandidate string            `json:"reference_candidate"`
	Variable           string            `json:"variable"`
	Task               string            `json:"task"`
	ReferenceTask      string            `json:"reference_task"`
	Levels             []string          `json:"levels"`
	Where              map[string]string `json:"where"`
	By                 []string          `json:"by"`
	Metric             string            `json:"metric"`
}

// independenceBody is a create's test of independence: a label metric
// against a cell variable, stratified by others, over the cells where
// selects; the candidate by task when variable and by are absent.
type independenceBody struct {
	Metric   string            `json:"metric"`
	Variable string            `json:"variable"`
	By       []string          `json:"by"`
	Where    map[string]string `json:"where"`
}

// plan is a create's body, read and resolved in one owner's context.
type plan struct {
	owner       string
	suite       objects.Version
	resolved    objects.Resolved
	candidates  []objects.Version
	candBodies  []objects.Candidate
	suiteDigest string
	candDigests []string
}

// checkCreate holds a create's body to its members' rules.
func checkCreate(in createBody) (suiteDigest string, candDigests []string, err error) {
	switch {
	case in.Suite == "":
		return "", nil, apierr.OnField(apierr.CodeMissingField, "suite", "an experiment names the suite it runs")
	case len(in.Candidates) == 0:
		return "", nil, apierr.OnField(apierr.CodeMissingField, "candidates", "an experiment names at least one candidate")
	case in.Budget == nil:
		return "", nil, apierr.OnField(apierr.CodeMissingField, "budget", "an experiment names its budget, or a cap for one")
	case len(in.Candidates) > limits.MaxCandidatesPerExperiment:
		return "", nil, apierr.Exceeds("MAX_CANDIDATES_PER_EXPERIMENT", "candidates", "more candidates than MAX_CANDIDATES_PER_EXPERIMENT (32)")
	}
	_, suiteDigest, err = objects.ParseRef("suite", in.Suite)
	if err != nil {
		return "", nil, err
	}
	for i, ref := range in.Candidates {
		_, d, err := objects.ParseRef("candidates["+itoa(i)+"]", ref)
		if err != nil {
			return "", nil, err
		}
		if slices.Contains(candDigests, d) {
			return "", nil, apierr.OnField(apierr.CodeInvalidField, "candidates["+itoa(i)+"]", "a candidate is named twice")
		}
		candDigests = append(candDigests, d)
	}
	if in.Trials != nil {
		switch {
		case *in.Trials < 1:
			return "", nil, apierr.OnField(apierr.CodeInvalidField, "trials", "trials is 1 or more")
		case *in.Trials > limits.MaxTrialsPerCell:
			return "", nil, apierr.Exceeds("MAX_TRIALS_PER_CELL", "trials", "more trials than MAX_TRIALS_PER_CELL (1000)")
		}
	}
	b := in.Budget
	switch {
	case (b.Name == nil) == (b.CapUSDMicro == nil):
		return "", nil, apierr.OnField(apierr.CodeInvalidField, "budget", "budget is exactly one of name and cap_usd_micro")
	case b.Name != nil && *b.Name == "":
		return "", nil, apierr.OnField(apierr.CodeInvalidField, "budget.name", "a budget's name is not empty")
	case b.CapUSDMicro != nil && *b.CapUSDMicro < 1:
		return "", nil, apierr.OnField(apierr.CodeInvalidField, "budget.cap_usd_micro", "a cap is at least 1 micro-dollar")
	}
	if in.Parallelism != nil {
		switch {
		case *in.Parallelism < 1:
			return "", nil, apierr.OnField(apierr.CodeInvalidField, "parallelism", "parallelism is 1 or more")
		case *in.Parallelism > limits.MaxParallelism:
			return "", nil, apierr.Exceeds("MAX_PARALLELISM", "parallelism", "parallelism above MAX_PARALLELISM (256)")
		}
	}
	if ref := in.Reference; ref != nil {
		if (ref.Baseline == "") == (ref.Candidate == "") {
			return "", nil, apierr.OnField(apierr.CodeInvalidField, "reference", "a reference is exactly one of baseline and candidate")
		}
		if ref.Candidate != "" {
			_, d, err := objects.ParseRef("reference.candidate", ref.Candidate)
			if err != nil {
				return "", nil, err
			}
			if !slices.Contains(candDigests, d) {
				return "", nil, apierr.OnField(apierr.CodeNotInExperiment, "reference.candidate", "the reference candidate is not one of the candidates")
			}
		}
	}
	switch in.Mode {
	case "", objects.ModeExecute, objects.ModeImport:
	default:
		return "", nil, apierr.OnField(apierr.CodeInvalidField, "mode", "mode is execute or import")
	}
	if in.SignIn != nil && !visibleASCII(*in.SignIn, limits.SignInMaxBytes) {
		return "", nil, apierr.OnField(apierr.CodeInvalidField, "sign_in", "sign_in is 1 to SIGN_IN_MAX_BYTES (255) bytes of visible ASCII")
	}
	switch in.Scheduling {
	case "", objects.SchedulingTaskByTask, objects.SchedulingInterleaved:
	default:
		return "", nil, apierr.OnField(apierr.CodeInvalidField, "scheduling", "scheduling is task_by_task or interleaved")
	}
	if err := checkDeclared(in.Comparisons, candDigests); err != nil {
		return "", nil, err
	}
	if in.Independence != nil && in.Independence.Metric == "" {
		return "", nil, apierr.OnField(apierr.CodeMissingField, "independence.metric", "a test of independence names its label metric")
	}
	return suiteDigest, candDigests, objects.ValidateMetadata("metadata", in.Metadata)
}

// checkDeclared holds a create's declared comparisons to the rules their
// body alone decides: at most MAX_DECLARED_COMPARISONS, each a pair of two
// of the create's candidates with an optional variable other than task, or
// one of its candidates with two different tasks, and no pair of
// candidates twice in either orientation. Tasks are resolved, and pairs of
// tasks held once, against the suite (declaredTasks).
func checkDeclared(list []declaredBody, candDigests []string) error {
	if len(list) > limits.MaxDeclaredComparisons {
		return apierr.Exceeds("MAX_DECLARED_COMPARISONS", "comparisons", "more declared comparisons than MAX_DECLARED_COMPARISONS ("+itoa(limits.MaxDeclaredComparisons)+")")
	}
	for i, d := range list {
		f := "comparisons[" + itoa(i) + "]"
		if d.Levels != nil {
			// The general form names its sides by a variable's levels,
			// which are resolved against the experiment's cells
			// (declarations).
			switch {
			case d.Candidate != "" || d.ReferenceCandidate != "" || d.Task != "" || d.ReferenceTask != "":
				return apierr.OnField(apierr.CodeInvalidField, f, "a declared comparison is in one form: a pair of candidates, a candidate's pair of tasks, or a variable and its levels")
			case d.Variable == "":
				return apierr.OnField(apierr.CodeMissingField, f+".variable", "a declared comparison of the general form names the variable it varies")
			}
			continue
		}
		if d.Where != nil || d.By != nil {
			return apierr.OnField(apierr.CodeInvalidField, f, "where and by belong to a declared comparison of the general form, which names its levels")
		}
		if d.Candidate == "" {
			return apierr.OnField(apierr.CodeMissingField, f+".candidate", "a declared comparison names its candidate")
		}
		_, cand, err := objects.ParseRef(f+".candidate", d.Candidate)
		if err != nil {
			return err
		}
		if !slices.Contains(candDigests, cand) {
			return apierr.OnField(apierr.CodeNotInExperiment, f+".candidate", "the candidate is not one of the experiment's")
		}
		tasks := d.Task != "" || d.ReferenceTask != ""
		_, known := objects.Variables[d.Variable]
		switch {
		case (d.ReferenceCandidate != "") == tasks:
			return apierr.OnField(apierr.CodeInvalidField, f, "a declared comparison names exactly one reference: reference_candidate, or task and reference_task")
		case d.Variable != "" && !known:
			return apierr.OnField(apierr.CodeInvalidField, f+".variable", "variable is one logical variable: agent, model, reasoning, tools, environment, workspace or task")
		case tasks && (d.Task == "" || d.ReferenceTask == ""):
			return apierr.OnField(apierr.CodeMissingField, f+".reference_task", "a declared comparison across tasks names both tasks")
		case tasks && d.Variable != "" && d.Variable != "task":
			return apierr.OnField(apierr.CodeInvalidField, f+".variable", "a comparison across tasks varies the task")
		case !tasks && d.Variable == "task":
			return apierr.OnField(apierr.CodeInvalidField, f+".variable", "a pair of candidates varies a field of the candidate, never the task")
		}
		if tasks {
			continue
		}
		_, ref, err := objects.ParseRef(f+".reference_candidate", d.ReferenceCandidate)
		if err != nil {
			return err
		}
		switch {
		case !slices.Contains(candDigests, ref):
			return apierr.OnField(apierr.CodeNotInExperiment, f+".reference_candidate", "the reference candidate is not one of the experiment's")
		case ref == cand:
			return apierr.OnField(apierr.CodeInvalidField, f+".reference_candidate", "a declared comparison compares two different candidates")
		}
	}
	return nil
}

// declarations resolves the declared comparisons of a create against its
// suite and the cells its tasks and candidates make: each task by name or
// digest, a metric by name or digest, and a declaration of the general
// form's levels and strata. It refuses a task the suite does not pin, one
// task twice, one candidate's pair of tasks declared twice in either
// orientation, a metric the suite does not list at level trial or cell,
// and a general declaration that does not read as one comparison or is
// declared twice; it answers the declarations by digest, with each general
// one's selection.
func declarations(list []declaredBody, res objects.Resolved, design []objects.DesignCell) ([]objects.DeclaredComparison, []objects.Selection, error) {
	out := make([]objects.DeclaredComparison, len(list))
	selections := make([]objects.Selection, len(list))
	seen := map[string]bool{}
	for i, d := range list {
		f := "comparisons[" + itoa(i) + "]"
		metric := ""
		if d.Metric != "" {
			digest, m, ok := res.MetricByRef(d.Metric)
			if !ok || m.LevelOf() == objects.LevelCandidate {
				return nil, nil, apierr.OnField(apierr.CodeInvalidField, f+".metric", "a declared metric is one the suite lists, of level trial or cell")
			}
			metric = digest
		}
		if d.Levels != nil {
			dc := objects.DeclaredComparison{Variable: d.Variable, Levels: slices.Clone(d.Levels), Where: maps.Clone(d.Where), By: slices.Clone(d.By), Metric: metric}
			if err := res.NormalizeDeclaration(&dc, f); err != nil {
				return nil, nil, err
			}
			sel, err := objects.Select(dc, design, f)
			if err != nil {
				return nil, nil, err
			}
			out[i], selections[i] = dc, sel
			continue
		}
		_, cand, _ := objects.ParseRef(f+".candidate", d.Candidate)
		dc := objects.DeclaredComparison{Candidate: cand, Variable: d.Variable, Metric: metric}
		if d.ReferenceCandidate != "" {
			_, dc.ReferenceCandidate, _ = objects.ParseRef(f+".reference_candidate", d.ReferenceCandidate)
			out[i] = dc
			continue
		}
		dc.Task, dc.ReferenceTask = d.Task, d.ReferenceTask
		for _, t := range []struct {
			field, ref string
			into       *string
		}{{f + ".task", dc.Task, &dc.Task}, {f + ".reference_task", dc.ReferenceTask, &dc.ReferenceTask}} {
			digest := t.ref
			_, ok := res.Tasks[t.ref]
			if !ok {
				digest, ok = res.TaskByName(t.ref)
			}
			if !ok {
				return nil, nil, apierr.OnField(apierr.CodeNotInExperiment, t.field, "the suite pins no task "+t.ref)
			}
			*t.into = digest
		}
		if dc.Task == dc.ReferenceTask {
			return nil, nil, apierr.OnField(apierr.CodeInvalidField, f+".reference_task", "a declared comparison across tasks names two different tasks")
		}
		out[i] = dc
	}
	for i, dc := range out {
		key, err := comparisonKey(dc, res.Suite.PrimaryMetric)
		if err != nil {
			return nil, nil, apierr.Internal(err)
		}
		if seen[key] {
			return nil, nil, apierr.OnField(apierr.CodeInvalidField, "comparisons["+itoa(i)+"]", "the comparison is declared twice, in this form or another")
		}
		seen[key] = true
	}
	return out, selections, nil
}

// comparisonKeyJSON writes a comparison's key as it has always been written,
// since a key is compared with the keys already recorded: members of a map
// in a fixed order, a nil slice as null, and <, > and & and the line
// separators escaped.
var comparisonKeyJSON = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	jsontext.EscapeForHTML(true),
	jsontext.EscapeForJS(true),
)

// comparisonKey identifies a declared comparison whatever its form, the
// orientation of its levels and the order of its strata: a pair of
// candidates reads as the candidate compared by task, and a candidate's
// pair of tasks as the task compared within the candidate, at the metric
// of its primary result. Two declarations of one key are one comparison.
func comparisonKey(d objects.DeclaredComparison, primary string) (string, error) {
	variable, levels, where, by := d.Variable, slices.Clone(d.Levels), maps.Clone(d.Where), slices.Clone(d.By)
	switch {
	case d.ReferenceCandidate != "":
		variable, levels, where, by = objects.VarCandidate, []string{d.Candidate, d.ReferenceCandidate}, nil, []string{objects.VarTask}
	case d.ReferenceTask != "":
		variable, levels, where, by = objects.VarTask, []string{d.Task, d.ReferenceTask}, map[string]string{objects.VarCandidate: d.Candidate}, nil
	}
	slices.Sort(levels)
	slices.Sort(by)
	if where == nil {
		where = map[string]string{}
	}
	if by == nil {
		by = []string{}
	}
	b, err := json.Marshal(struct {
		Variable string            `json:"variable"`
		Levels   []string          `json:"levels"`
		Where    map[string]string `json:"where"`
		By       []string          `json:"by"`
		Metric   string            `json:"metric"`
	}{variable, levels, where, by, cmp.Or(d.Metric, primary)}, comparisonKeyJSON)
	return string(b), err
}

// independenceOf resolves a create's test of independence against its
// suite and the cells its tasks and candidates make: a trial-level label
// metric of the suite, and a variable that holds at least two levels among
// the cells it reads, each stratum holding at most one cell of a level.
func independenceOf(in *independenceBody, res objects.Resolved, design []objects.DesignCell, candidates []string) (*objects.Independence, error) {
	if in == nil {
		return nil, nil
	}
	digest, m, ok := res.MetricByRef(in.Metric)
	if !ok || m.Type != objects.TypeLabel || m.LevelOf() != objects.LevelTrial {
		return nil, apierr.OnField(apierr.CodeInvalidField, "independence.metric", "a test of independence reads a trial-level label metric the suite lists")
	}
	out := &objects.Independence{Metric: digest, Variable: in.Variable, By: slices.Clone(in.By), Where: maps.Clone(in.Where)}
	if err := res.NormalizeIndependence(out, "independence"); err != nil {
		return nil, err
	}
	if _, err := objects.SelectIndependence(*out, design, candidates, "independence"); err != nil {
		return nil, err
	}
	return out, nil
}

// generalComparison is the record of a declared comparison of the general
// form: its declaration, its candidate side's one candidate when it has
// one, the tuple fields of its logical variable, and the sides holding an
// external candidate's cell.
func generalComparison(index int, dc objects.DeclaredComparison, sel objects.Selection, kinds map[string]string) objects.Comparison {
	decl := dc
	cmp := objects.Comparison{
		ID: id.New(id.Comparison), Candidate: sel.CandidateOf(), DeclaredIndex: ptr(index), State: objects.ComparisonComputing,
		Declaration: &decl, Variable: objects.Variables[objects.LogicalVariableOf(dc.Variable)],
	}
	if cmp.Variable == nil {
		cmp.Variable = []string{}
	}
	external := [2]bool{}
	for _, st := range sel.Strata {
		for s, c := range []*objects.DesignCell{st.Candidate, st.Reference} {
			if c != nil && kinds[c.Candidate] == objects.CandidateExternal {
				external[s] = true
			}
		}
	}
	if external[0] {
		cmp.DeclaredProvenance = append(cmp.DeclaredProvenance, "candidate")
	}
	if external[1] {
		cmp.DeclaredProvenance = append(cmp.DeclaredProvenance, "reference")
	}
	return cmp
}

// resolvePlan reads a create's suite and candidates in an owner's
// context, with every version the suite pins.
func (s *Server) resolvePlan(r *http.Request, owner string, in createBody, suiteDigest string, candDigests []string) (plan, error) {
	p := plan{owner: owner, suiteDigest: suiteDigest, candDigests: candDigests}
	ctx, vs := r.Context(), s.Store.Versions()
	read := func(kind, field, ref, digest string) (objects.Version, error) {
		v, err := vs.ByDigest(ctx, kind, owner, digest)
		name, _, _ := objects.ParseRef(field, ref)
		if errors.Is(err, store.ErrNotFound) || (err == nil && name != "" && v.Name != name) {
			return v, apierr.OnField(apierr.CodeReferenceNotFound, field, "no "+kind+" the caller may use answers to "+ref)
		}
		if err != nil {
			return v, apierr.Internal(err)
		}
		return v, nil
	}
	var err error
	if p.suite, err = read(objects.KindSuite, "suite", in.Suite, suiteDigest); err != nil {
		return p, err
	}
	var su objects.Suite
	if err := json.Unmarshal(p.suite.Body, &su); err != nil {
		return p, apierr.Internal(err)
	}
	if p.resolved, err = s.resolveSuite(r, owner, su, "suite."); err != nil {
		return p, err
	}
	p.resolved.Digest = suiteDigest
	for i, d := range candDigests {
		v, err := read(objects.KindCandidate, "candidates["+itoa(i)+"]", in.Candidates[i], d)
		if err != nil {
			return p, err
		}
		var c objects.Candidate
		if err := json.Unmarshal(v.Body, &c); err != nil {
			return p, apierr.Internal(err)
		}
		p.candidates, p.candBodies = append(p.candidates, v), append(p.candBodies, c)
	}
	return p, nil
}

// createMembers are the fields of experiment.create: those of the body,
// and once the plan is resolved, what it reads.
func createMembers(r *http.Request, in createBody, suiteDigest string, candDigests []string, p *plan) map[string]any {
	m := map[string]any{
		authorizer.FieldOwner: r.URL.Query().Get("owner"), authorizer.FieldSuite: suiteDigest, authorizer.FieldCandidates: candDigests,
		authorizer.FieldMode: in.Mode,
	}
	trials := limits.DefaultTrials
	if in.Trials != nil {
		trials = *in.Trials
	}
	m[authorizer.FieldTrials] = trials
	if in.Parallelism != nil {
		m[authorizer.FieldParallelism] = *in.Parallelism
	}
	if in.Budget.Name != nil {
		m[authorizer.FieldBudget] = *in.Budget.Name
	} else {
		m[authorizer.FieldBudgetCapUSDMicro] = *in.Budget.CapUSDMicro
	}
	if in.Reference != nil {
		m[authorizer.FieldReferenceCandidate], m[authorizer.FieldBaseline] = in.Reference.Candidate, in.Reference.Baseline
	}
	if in.Mode == "" {
		m[authorizer.FieldMode] = objects.ModeExecute
	}
	if in.SignIn != nil {
		m[authorizer.FieldSignIn] = *in.SignIn
	}
	if p == nil {
		return m
	}
	m[authorizer.FieldSuiteOwner] = p.owner
	var executors, models, judges, audiences []string
	for _, c := range p.candBodies {
		if e := objects.ExecutorOf(c.Kind); !slices.Contains(executors, e) {
			executors = append(executors, e)
		}
		if mo := c.Model(); mo != "" && !slices.Contains(models, mo) {
			models = append(models, mo)
		}
		audiences = append(audiences, c.Audiences()...)
	}
	// Every grader the suite version pins is resolved: each task's checks,
	// the suite's graders, its canonicalizers and its decline judge. An
	// empty set is sent, since it says the suite has none, so an
	// authorizer reads that no grader needs an owner token.
	var kinds []string
	for _, g := range p.resolved.Graders {
		if g.ModelJudge != nil && !slices.Contains(judges, g.ModelJudge.Model) {
			judges = append(judges, g.ModelJudge.Model)
		}
		if !slices.Contains(kinds, g.Kind) {
			kinds = append(kinds, g.Kind)
		}
	}
	slices.Sort(judges)
	slices.Sort(kinds)
	cells := len(p.resolved.Suite.Tasks) * len(candDigests)
	m[authorizer.FieldExecutors], m[authorizer.FieldModels] = executors, strs(models)
	m[authorizer.FieldJudgeModels], m[authorizer.FieldCredentialAudiences] = strs(judges), strs(audiences)
	m[authorizer.FieldGraderKinds] = access.Set(kinds)
	m[authorizer.FieldCells], m[authorizer.FieldTrialsTotal] = cells, cells*trials
	return m
}

// cores are what this server runs: topos trials, an origo workspace's
// clone for them, their checks and model judges, and the reading of an
// origo reference for a check.
type cores struct{ topos, origo, checks, judges, references bool }

// unbuilt refuses what this release cannot run (CHANGELOG): a candidate
// whose executor is not built or not configured, what a topos candidate's
// session cannot carry yet, a grader that needs a core this release does
// not reach or of a kind it does not run, inputs that need Arca to be
// delivered, and a cap that needs a Budget made at Lux when no authorizer
// answered one. Checks and model judges grade topos trials alone: an
// external trial's outputs are recorded unread (specs/005-execution.md).
func unbuilt(in createBody, p plan, budgetAssigned bool, on cores) error {
	toposOn, origoOn := on.topos, on.origo
	core := func(name, field, why string) error {
		return &apierr.Error{Code: apierr.CodeCoreNotConfigured, Core: name, Field: field, Detail: why}
	}
	toposCandidates, otherCandidates := false, false
	for i, c := range p.candBodies {
		f := "candidates[" + itoa(i) + "]"
		switch {
		case c.Kind == objects.CandidateTopos && !toposOn:
			return core("topos", f, "a candidate of kind topos runs as a Topos session, which this installation does not start: it names no Topos address or no minter")
		case c.Kind == objects.CandidateTopos && len(c.Topos.Tools) > 0:
			// Topos's sessions offer every tool of the agent version until
			// a rollout can narrow them (spec 005, R4).
			return apierr.OnField(apierr.CodeExecutorUnavailable, f+".topos.tools", "Topos cannot narrow an agent version's tools for a session yet")
		case c.Kind == objects.CandidateTopos:
			toposCandidates = true
		case c.Kind == objects.CandidateExternal:
			otherCandidates = true
		default:
			return apierr.OnField(apierr.CodeExecutorUnavailable, f, "the "+objects.ExecutorOf(c.Kind)+" executor that runs a candidate of kind "+c.Kind+" is not part of this release")
		}
	}
	su := p.resolved.Suite
	for i, td := range su.Tasks {
		if !toposCandidates {
			break
		}
		t, f := p.resolved.Tasks[td], "suite.tasks["+itoa(i)+"]"
		switch {
		case strings.TrimSpace(t.Instruction) == "":
			return apierr.OnField(apierr.CodeMissingField, f+".instruction", "a task a topos candidate runs gives its instruction, the session's first message")
		case t.Environment != nil:
			return apierr.OnField(apierr.CodeExecutorUnavailable, f+".environment", "a Topos session runs in its agent's machine until a rollout can name another (spec 005, R5)")
		case t.Workspace != nil && t.Workspace.Kind == objects.WorkspaceArca:
			return apierr.OnField(apierr.CodeExecutorUnavailable, f+".workspace", "a Topos session starts in an Arca folder only once a rollout can name one (spec 005, R6)")
		case t.Workspace != nil && t.Workspace.Kind == objects.WorkspaceOrigo && !origoOn:
			return core("origo", f+".workspace", "an origo workspace is cloned from the installation's Origo, which it names no address for")
		}
	}
	// Examen grades a topos trial alone, of an experiment in mode execute.
	gradedHere := toposCandidates && !otherCandidates && in.Mode != objects.ModeImport
	for i, td := range su.Tasks {
		t := p.resolved.Tasks[td]
		f := "suite.tasks[" + itoa(i) + "]"
		switch {
		case len(t.Checks) > 0 && !on.checks:
			return core("cella", f+".checks", "the task's checks run in a check sandbox of Cella over the run's files in Arca, which this installation does not reach")
		case len(t.Checks) > 0 && !gradedHere:
			return apierr.OnField(apierr.CodeExecutorUnavailable, f+".checks", "a check grades an Agent candidate's run alone in this release: an external or imported trial's outputs are recorded unread")
		case len(t.Inputs) > 0 && in.Mode != objects.ModeImport:
			return core("arca", f+".inputs", "the task's inputs reach an external harness through Arca links, which this release does not mint")
		}
		// A reference reaches the task's checks alone, in their sandbox. One
		// of Origo is read there with an owner token, so a task with checks
		// and no Origo to read it from would carry a reference that never
		// arrives.
		for j, ref := range t.Reference {
			if ref.Origo != nil && len(t.Checks) > 0 && !on.references {
				return core("origo", f+".reference["+itoa(j)+"]", "an origo reference is read from the installation's Origo into the check sandbox of the task's checks, which it names no address for")
			}
		}
		for j, d := range t.Graders {
			if err := judged(p, d, f+".graders["+itoa(j)+"]", on, gradedHere); err != nil {
				return err
			}
		}
	}
	for i, d := range su.Graders {
		if err := judged(p, d, "suite.graders["+itoa(i)+"]", on, gradedHere); err != nil {
			return err
		}
	}
	// A canonicalizer and a configured decline judge are model judges this
	// release does not run: where no judge runs, for want of Lux.
	notRun := func(field, why string) error {
		if !on.judges {
			return core("lux", field, "a model judge calls its model through Lux on a Key of the key source, which this installation does not reach")
		}
		return apierr.OnField(apierr.CodeExecutorUnavailable, field, why)
	}
	for i := range su.Canonicalizers {
		if err := notRun("suite.canonicalizers["+itoa(i)+"]", "a canonicalizer does not run in this release; the owner maps raw labels through the taxonomy route"); err != nil {
			return err
		}
	}
	if su.DeclineJudge != "" {
		if err := notRun("suite.decline_judge", "a configured decline judge does not run in this release; the built-in decline judge fills declined"); err != nil {
			return err
		}
	}
	if in.Budget.CapUSDMicro != nil && !budgetAssigned {
		return core("lux", "budget.cap_usd_micro", "a cap is a Budget made at Lux as the owner, which this release does not do; name a Budget, or let the authorizer assign one")
	}
	return nil
}

// judged refuses a grader of the suite or a task that this server does not
// run: a model judge with no Lux or key source, or grading a run this
// release does not grade, and an agent judge.
func judged(p plan, digest, field string, on cores, gradedHere bool) error {
	switch g := p.resolved.Graders[digest]; {
	case g.Kind == objects.GraderModelJudge && g.LevelOf() == objects.LevelCandidate:
		return apierr.OnField(apierr.CodeExecutorUnavailable, field, "a judge of level candidate does not run in this release")
	case g.Kind == objects.GraderModelJudge && !on.judges:
		return &apierr.Error{Code: apierr.CodeCoreNotConfigured, Core: "lux", Field: field, Detail: "a model judge calls its model through Lux on a Key of the key source and reads the run's files in Arca, which this installation does not reach"}
	case g.Kind == objects.GraderModelJudge && !gradedHere:
		return apierr.OnField(apierr.CodeExecutorUnavailable, field, "a model judge grades an Agent candidate's run alone in this release: an external or imported trial's outputs are recorded unread")
	case g.Kind == objects.GraderAgentJudge:
		return &apierr.Error{Code: apierr.CodeCoreNotConfigured, Core: "topos", Field: field, Detail: "an agent judge runs as a Topos session, which this release does not start"}
	}
	return nil
}

// resolveAgents checks, as the caller and with the purpose resolve, that
// Topos holds the agent version each topos candidate pins (spec 003): a
// version it does not hold is unresolved_reference, and a minter that
// keeps no sign-in for the caller refuses the create before the
// experiment exists, so its later work is never unattributed.
func (s *Server) resolveAgents(r *http.Request, c access.Caller, eid string, p plan) error {
	for i, cb := range p.candBodies {
		if cb.Kind != objects.CandidateTopos {
			continue
		}
		f := "candidates[" + itoa(i) + "].topos.agent"
		tp := s.Engine.Topos
		token, err := tp.Tokens.Token(r.Context(), ownertoken.Request{Owner: p.owner, Subject: c.Subject, Audience: tp.Audience, Experiment: eid, Purpose: ownertoken.PurposeResolve})
		var mint *ownertoken.RefusedError
		switch {
		case errors.As(err, &mint) && mint.Reason == ownertoken.ReasonSignInNotKept:
			return &apierr.Error{Code: apierr.CodeSignInNotKept, Detail: "experiment.create: the minter keeps no sign-in of the caller for this experiment"}
		case errors.As(err, &mint):
			return &apierr.Error{Code: apierr.CodeForbidden, Reason: ownertoken.ReasonRefused, Detail: "experiment.create: the minter refused an owner token for Topos"}
		case err != nil:
			return &apierr.Error{Code: apierr.CodeOwnerTokensUnavailable, Detail: "experiment.create: " + err.Error()}
		}
		name, digest, err := objects.ParseRef(f, cb.Topos.Agent)
		if err != nil {
			return err
		}
		_, err = tp.Client.Version(r.Context(), token, name, digest)
		var refused *topos.RefusedError
		switch {
		case errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusForbidden):
			return apierr.OnField(apierr.CodeUnresolvedReference, f, "Topos holds no version "+digest+" of an agent "+name+" the caller may run")
		case errors.As(err, &refused):
			return &apierr.Error{Code: apierr.CodeCoreUnavailable, Core: "topos", Field: f, Detail: "reading the agent's versions: " + refused.Error()}
		case err != nil:
			return &apierr.Error{Code: apierr.CodeCoreUnavailable, Core: "topos", Field: f, Detail: "reading the agent's versions: " + err.Error()}
		}
	}
	return nil
}

// limit is a 403 a limit of the allow made, with its figure.
func limit(action, reason string, figure int, field string) error {
	f := float64(figure)
	return &apierr.Error{Code: apierr.CodeForbidden, Reason: reason, Figure: &f, Field: field, Detail: action + ": the request is above the allow's " + reason}
}

func (s *Server) createExperiment(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var in createBody
	if err := decode(r, &in); err != nil {
		return err
	}
	suiteDigest, candDigests, err := checkCreate(in)
	if err != nil {
		return err
	}
	eid := id.New(id.Experiment)
	ask := func(p *plan) (access.Decision, error) {
		return s.permitted(r, c, access.Ask(r, authorizer.ActionExperimentCreate, access.Resource(authorizer.ActionExperimentCreate, eid, createMembers(r, in, suiteDigest, candDigests, p))))
	}
	// The plan is resolved in the context the request acts in: the owner
	// it names, else the owner the allow of experiment.list names, else
	// the caller. A member of an organization thus resolves the
	// organization's suite and candidates before the create's question,
	// which carries what the plan resolved. A reference that resolves to
	// nothing is answered only after that question, so it cannot probe
	// what another owner holds.
	provisional, err := s.contextOwner(r, c, authorizer.ActionExperimentList)
	if err != nil {
		return err
	}
	p, perr := s.resolvePlan(r, provisional, in, suiteDigest, candDigests)
	if perr != nil {
		if _, err := ask(nil); err != nil {
			return err
		}
		return perr
	}
	d, err := ask(&p)
	if err != nil {
		return err
	}
	if owner := createOwner(r, c, d); owner != provisional {
		if p, err = s.resolvePlan(r, owner, in, suiteDigest, candDigests); err != nil {
			return err
		}
	}
	l := d.Limits
	trials := limits.DefaultTrials
	if in.Trials != nil {
		trials = *in.Trials
	}
	cells := len(p.resolved.Suite.Tasks) * len(candDigests)
	if cells > limits.MaxCellsPerExperiment {
		return apierr.Exceeds("MAX_CELLS_PER_EXPERIMENT", "candidates", "tasks × candidates = "+itoa(cells)+" cells exceed MAX_CELLS_PER_EXPERIMENT (2000)")
	}
	parallelism := limits.DefaultParallelism
	switch {
	case in.Parallelism != nil && l.MaxParallelism > 0 && *in.Parallelism > l.MaxParallelism:
		return limit(authorizer.ActionExperimentCreate, "max_parallelism", l.MaxParallelism, "parallelism")
	case in.Parallelism != nil:
		parallelism = *in.Parallelism
	case l.MaxParallelism > 0:
		parallelism = min(parallelism, l.MaxParallelism)
	}
	if l.MaxTrials > 0 && cells*trials > l.MaxTrials {
		return limit(authorizer.ActionExperimentCreate, "max_trials", l.MaxTrials, "trials")
	}
	for i, cb := range p.candBodies {
		if len(l.Executors) > 0 && !slices.Contains(l.Executors, objects.ExecutorOf(cb.Kind)) {
			return limit(authorizer.ActionExperimentCreate, "executors", len(l.Executors), "candidates["+itoa(i)+"]")
		}
	}
	budget := ""
	switch {
	case l.Budget != "" && in.Budget.Name != nil && *in.Budget.Name != l.Budget:
		return &apierr.Error{Code: apierr.CodeForbidden, Reason: "budget", Field: "budget.name", Detail: "experiment.create: the authorizer assigns another Budget than the one named"}
	case l.Budget != "":
		budget = l.Budget
	case in.Budget.Name != nil:
		budget = *in.Budget.Name
	}
	toposOn := s.Engine != nil && s.Engine.Topos != nil
	on := cores{topos: toposOn, origo: toposOn && s.Engine.Topos.OrigoURL != ""}
	if toposOn && s.Engine.Grading != nil {
		on.checks, on.judges = s.Engine.Grading.Checks(), s.Engine.Grading.Judges() && s.Engine.Keys != nil
		on.references = s.Engine.Grading.References()
	}
	if err := unbuilt(in, p, l.Budget != "", on); err != nil {
		return err
	}
	if err := s.resolveAgents(r, c, eid, p); err != nil {
		return err
	}
	design := p.resolved.Design(candDigests, p.candBodies)
	declared, selections, err := declarations(in.Comparisons, p.resolved, design)
	if err != nil {
		return err
	}
	independence, err := independenceOf(in.Independence, p.resolved, design, candDigests)
	if err != nil {
		return err
	}
	mode := in.Mode
	if mode == "" {
		mode = objects.ModeExecute
	}
	scheduling := in.Scheduling
	if scheduling == "" {
		scheduling = objects.SchedulingTaskByTask
	}
	e := objects.Experiment{
		ID: eid, Owner: p.owner, Suite: suiteDigest, Candidates: candDigests, Trials: trials, Budget: objects.Budget{Name: budget},
		Parallelism: parallelism, Mode: mode, Scheduling: scheduling, Comparisons: declared, Independence: independence, CreatedBy: c.Subject, Metadata: in.Metadata,
		SuiteID: p.suite.ID, SuiteName: p.suite.Name,
	}
	for _, v := range p.candidates {
		e.CandidateIDs = append(e.CandidateIDs, v.ID)
	}
	referenceKind := objects.CandidateExternal
	switch {
	case in.Reference != nil && in.Reference.Baseline != "":
		b, err := s.Store.Baselines().ByID(r.Context(), in.Reference.Baseline)
		if errors.Is(err, store.ErrNotFound) || (err == nil && b.Owner != p.owner) {
			return apierr.OnField(apierr.CodeReferenceNotFound, "reference.baseline", "no baseline record the caller may use has the id "+in.Reference.Baseline)
		}
		if err != nil {
			return apierr.Internal(err)
		}
		if b.Suite.Owner != p.owner || b.Suite.Name != p.suite.Name {
			return apierr.OnField(apierr.CodeSuiteMismatch, "reference.baseline", "the baseline pins an experiment of another suite")
		}
		e.Reference.Baseline = b.ID
		if v, err := s.Store.Versions().ByDigest(r.Context(), objects.KindCandidate, b.Owner, b.Candidate); err == nil {
			var cb objects.Candidate
			if err := json.Unmarshal(v.Body, &cb); err != nil {
				return apierr.Internal(err)
			}
			referenceKind = cb.Kind
		}
	case in.Reference != nil:
		_, e.Reference.Candidate, _ = objects.ParseRef("reference.candidate", in.Reference.Candidate)
	default:
		e.Reference.Candidate = candDigests[0]
	}
	ne := store.NewExperiment{Experiment: e, MaxActive: limits.MaxActiveExperimentsPerOwner}
	if l.MaxActiveExperiments > 0 {
		ne.MaxActive = l.MaxActiveExperiments
	}
	// In mode import no cell exists at create: each is made by the first
	// import of its task and candidate.
	for ti, td := range p.resolved.Suite.Tasks {
		if mode != objects.ModeExecute {
			break
		}
		for ci, cb := range p.candBodies {
			tuple, err := p.resolved.TupleOf(td, cb)
			if err != nil {
				return apierr.Internal(err)
			}
			cell := objects.Cell{
				ID: id.New(id.Cell), Task: td, Candidate: candDigests[ci], Tuple: tuple,
				TaskID: p.resolved.TaskIDs[td], CandidateID: p.candidates[ci].ID, TaskPos: ti, CandidatePos: ci,
			}
			smoke := execute.Smoke(cell.ID, trials, objects.ExecutorOf(cb.Kind))
			nc := store.NewCell{Cell: cell, Smoke: &store.Rollout{Rollout: smoke.Rollout}}
			for _, t := range smoke.Trials {
				nc.Smoke.Trials = append(nc.Smoke.Trials, store.RolloutTrial{Index: t.Index})
			}
			ne.Cells = append(ne.Cells, nc)
		}
	}
	kinds := map[string]string{}
	for ci, d := range candDigests {
		kinds[d] = p.candBodies[ci].Kind
	}
	provenance := func(candidate, reference string) []string {
		var out []string
		if kinds[candidate] == objects.CandidateExternal {
			out = append(out, "candidate")
		}
		if reference == objects.CandidateExternal {
			out = append(out, "reference")
		}
		return out
	}
	automatic := map[string]int{}
	for _, d := range candDigests {
		if d == e.Reference.Candidate {
			continue
		}
		automatic[d] = len(ne.Comparisons)
		ne.Comparisons = append(ne.Comparisons, objects.Comparison{
			ID: id.New(id.Comparison), Candidate: d, Baseline: e.Reference.Baseline, ReferenceCandidate: e.Reference.Candidate,
			Automatic: true, State: objects.ComparisonComputing, DeclaredProvenance: provenance(d, referenceKind),
		})
	}
	// A declared pair that is the automatic comparison's own, in its
	// orientation and with no variable or metric of its own, is that
	// comparison; every other declared comparison is a record of its own.
	for i, dc := range declared {
		if dc.General() {
			ne.Comparisons = append(ne.Comparisons, generalComparison(i, dc, selections[i], kinds))
			continue
		}
		at, isAutomatic := automatic[dc.Candidate]
		if isAutomatic && dc.ReferenceCandidate != "" && dc.ReferenceCandidate == e.Reference.Candidate && dc.Variable == "" && dc.Metric == "" {
			ne.Comparisons[at].DeclaredIndex = ptr(i)
			continue
		}
		cmp := objects.Comparison{
			ID: id.New(id.Comparison), Candidate: dc.Candidate, ReferenceCandidate: dc.ReferenceCandidate, Task: dc.Task, ReferenceTask: dc.ReferenceTask,
			DeclaredIndex: ptr(i), State: objects.ComparisonComputing,
		}
		reference := dc.ReferenceCandidate
		if dc.Task != "" {
			reference = dc.Candidate
			cmp.Variable = objects.Variables["task"]
		} else if dc.Variable != "" {
			cmp.Variable = objects.Variables[dc.Variable]
		}
		cmp.DeclaredProvenance = provenance(dc.Candidate, kinds[reference])
		ne.Comparisons = append(ne.Comparisons, cmp)
	}
	if err := s.Store.Experiments().Create(r.Context(), ne); err != nil {
		if errors.Is(err, store.ErrActiveLimit) {
			if l.MaxActiveExperiments > 0 && l.MaxActiveExperiments < limits.MaxActiveExperimentsPerOwner {
				return limit(authorizer.ActionExperimentCreate, "max_active_experiments", l.MaxActiveExperiments, "")
			}
			return apierr.Exceeds("MAX_ACTIVE_EXPERIMENTS_PER_OWNER", "", "the owner holds MAX_ACTIVE_EXPERIMENTS_PER_OWNER (16) experiments neither complete nor canceled")
		}
		return apierr.Internal(err)
	}
	stored, err := s.Store.Experiments().Get(r.Context(), eid)
	if err != nil {
		return apierr.Internal(err)
	}
	// The experiment's external Key is made now, so its runs' first lease
	// finds it; a key source that does not answer leaves it to that lease.
	var candKinds []string
	for _, cb := range p.candBodies {
		candKinds = append(candKinds, cb.Kind)
	}
	if s.handsKey(stored, candKinds) {
		if _, err := s.externalKey(r.Context(), stored, c.Subject); err != nil {
			s.log().WarnContext(r.Context(), "the key source gave no key at the create; the first lease asks again", "experiment", eid, "error", apierr.LogText(err))
		}
	}
	s.advance(r, eid)
	w.Header().Set("Location", s.location("/experiments/"+eid))
	reply(w, http.StatusCreated, stored)
	return nil
}

func (s *Server) listExperiments(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	p, err := pageOf(r, "experiments")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	requested := q.Get("owner")
	d, err := s.permitted(r, c, access.Ask(r, authorizer.ActionExperimentList, access.List(authorizer.ActionExperimentList, requested)))
	if err != nil {
		return err
	}
	labels, empty, err := labelFilter(r, d)
	if err != nil {
		return err
	}
	f := store.ExperimentFilter{Owners: d.Owners(c, requested), Candidate: q.Get("candidate"), State: q.Get("state"), Mode: q.Get("mode"), Baseline: q.Get("baseline"), Labels: labels}
	if ref := q.Get("suite"); ref != "" {
		if _, digest, err := objects.ParseRef("suite", ref); err == nil {
			f.Suite = digest
		} else {
			f.SuiteName = ref
		}
	}
	var list []objects.Experiment
	if len(f.Owners) > 0 && !empty {
		if list, err = s.Store.Experiments().List(r.Context(), f, store.Page{After: p.After, Limit: p.Limit}); err != nil {
			return apierr.Internal(err)
		}
	}
	replyPage(w, "experiments", p, list, func(e objects.Experiment) string { return e.ID })
	return nil
}

func (s *Server) getExperiment(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	if err := s.figures(r.Context(), &e, c.Subject); err != nil {
		return err
	}
	reply(w, http.StatusOK, e)
	return nil
}

func (s *Server) cancelExperiment(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	miss := missing{apierr.CodeExperimentNotFound, r.PathValue("id")}
	e, err := s.Store.Experiments().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return storeError(err, miss)
	}
	members := map[string]any{authorizer.FieldOwner: e.Owner, authorizer.FieldSuite: e.Suite, authorizer.FieldState: e.State}
	if _, err := s.allowed(r, c, authorizer.ActionExperimentCancel, e.ID, members, miss); err != nil {
		return err
	}
	out, err := s.Store.Experiments().Cancel(r.Context(), e.ID)
	if err != nil {
		return storeError(err, miss)
	}
	s.releaseKey(r, e.ID)
	reply(w, http.StatusOK, out)
	return nil
}

// releaseKey releases an experiment's external Key once it ended, when
// the server takes unattended work; a failure is logged and left to the
// ticker, which releases it within REPORT_INTERVAL.
func (s *Server) releaseKey(r *http.Request, experiment string) {
	if s.Engine == nil || s.Keys == nil {
		return
	}
	s.keyCache().Forget(experiment)
	if err := s.Engine.Release(context.WithoutCancel(r.Context()), experiment); err != nil {
		s.log().WarnContext(r.Context(), "an ended experiment's key was not released; the ticker releases it", "experiment", experiment, "error", apierr.LogText(err))
	}
}

func (s *Server) closeExperiment(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	miss := missing{apierr.CodeExperimentNotFound, r.PathValue("id")}
	e, err := s.experimentAs(r, c, r.PathValue("id"), authorizer.ActionExperimentUpdate, nil, miss)
	if err != nil {
		return err
	}
	out, err := s.Store.Experiments().Close(r.Context(), e.ID)
	if err != nil {
		return storeError(err, miss)
	}
	reply(w, http.StatusOK, out)
	return nil
}

// eventItem is an event as the log answers it.
type eventItem = objects.Event

func (s *Server) events(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	q := r.URL.Query()
	after := int64(0)
	if raw := q.Get("after"); raw != "" {
		if after, err = strconv.ParseInt(raw, 10, 64); err != nil || after < 0 {
			return apierr.OnField(apierr.CodeInvalidCursor, "after", "after is a sequence the log issued")
		}
	}
	p := page{Limit: limits.DefaultPageSize}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > limits.MaxPageSize {
			return apierr.OnField(apierr.CodeInvalidField, "limit", "limit is a whole number from 1 to MAX_PAGE_SIZE (200)")
		}
		p.Limit = n
	}
	list, err := s.Store.Experiments().Events(r.Context(), e.ID, after, p.Limit+1)
	if err != nil {
		return apierr.Internal(err)
	}
	body := listBody[eventItem]{Items: list}
	if len(list) > p.Limit {
		body.Items = list[:p.Limit]
		body.NextCursor = strconv.FormatInt(body.Items[p.Limit-1].Seq, 10)
	}
	if body.Items == nil {
		body.Items = []eventItem{}
	}
	reply(w, http.StatusOK, body)
	return nil
}

// cellOf reads a cell and asks experiment.read of its experiment: a
// missing cell and a deny are the cell's one 404.
func (s *Server) cellOf(r *http.Request, c access.Caller, cid string) (objects.Cell, objects.Experiment, error) {
	miss := missing{apierr.CodeCellNotFound, cid}
	cell, err := s.Store.Experiments().Cell(r.Context(), cid)
	if err != nil {
		return cell, objects.Experiment{}, storeError(err, miss)
	}
	e, err := s.experimentAs(r, c, cell.Experiment, authorizer.ActionExperimentRead, nil, miss)
	return cell, e, err
}

func (s *Server) listCells(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	list := "cells/" + e.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	f := store.CellFilter{State: q.Get("state"), Candidate: q.Get("candidate"), Task: q.Get("task")}
	if f.Task != "" {
		if f.Task, err = s.taskDigest(r, e, f.Task); err != nil {
			return err
		}
	}
	cells, err := s.Store.Experiments().Cells(r.Context(), e.ID, f, store.Page{After: p.After, Limit: p.Limit})
	if err != nil {
		return apierr.Internal(err)
	}
	replyPage(w, list, p, cells, func(c objects.Cell) string { return c.ID })
	return nil
}

// taskDigest is the digest of a task the experiment's suite pins, named by
// its digest or its name; one it does not pin narrows to nothing.
func (s *Server) taskDigest(r *http.Request, e objects.Experiment, ref string) (string, error) {
	res, err := s.experimentSuite(r, e)
	if err != nil {
		return "", err
	}
	if _, ok := res.Tasks[ref]; ok {
		return ref, nil
	}
	if d, ok := res.TaskByName(ref); ok {
		return d, nil
	}
	return "-", nil
}

// experimentSuite reads the suite version an experiment runs, with every
// version it pins.
func (s *Server) experimentSuite(r *http.Request, e objects.Experiment) (objects.Resolved, error) {
	v, err := s.Store.Versions().ByID(r.Context(), objects.KindSuite, e.SuiteID)
	if err != nil {
		return objects.Resolved{}, apierr.Internal(err)
	}
	var su objects.Suite
	if err := json.Unmarshal(v.Body, &su); err != nil {
		return objects.Resolved{}, apierr.Internal(err)
	}
	res, err := s.resolveSuite(r, e.Owner, su, "suite.")
	if err != nil {
		return res, apierr.Internal(err)
	}
	res.Digest = e.Suite
	return res, nil
}

func (s *Server) getCell(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	cell, _, err := s.cellOf(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	reply(w, http.StatusOK, cell)
	return nil
}

func (s *Server) retryCell(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	cid := r.PathValue("id")
	miss := missing{apierr.CodeCellNotFound, cid}
	cell, err := s.Store.Experiments().Cell(r.Context(), cid)
	if err != nil {
		return storeError(err, miss)
	}
	extra := map[string]any{authorizer.FieldCell: cell.ID, authorizer.FieldCellState: cell.State, authorizer.FieldParkedReason: cell.ParkedReason}
	if _, err := s.experimentAs(r, c, cell.Experiment, authorizer.ActionCellRetry, extra, miss); err != nil {
		return err
	}
	if _, err := s.Store.Experiments().RetryCell(r.Context(), cid); err != nil {
		return storeError(err, miss)
	}
	s.advance(r, cell.Experiment)
	out, err := s.Store.Experiments().Cell(r.Context(), cid)
	if err != nil {
		return apierr.Internal(err)
	}
	reply(w, http.StatusOK, out)
	return nil
}
