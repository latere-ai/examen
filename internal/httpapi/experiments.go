// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"slices"
	"strconv"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
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
		{method: http.MethodPost, path: "/experiments/{id}/work/lease", idempotent: true, handle: (*Server).lease},
		{method: http.MethodPost, path: "/trials/{id}/submit", handle: (*Server).submit},
		{method: http.MethodGet, path: "/trials/{id}/measurements", handle: (*Server).trialMeasurements},
		{method: http.MethodGet, path: "/experiments/{id}/measurements", handle: (*Server).experimentMeasurements},
		{method: http.MethodGet, path: "/measurements/{id}", handle: (*Server).getMeasurement},
		{method: http.MethodGet, path: "/experiments/{id}/comparisons", handle: (*Server).experimentComparisons},
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
// fields: a missing experiment and a deny are the 404 code names.
func (s *Server) experimentAs(r *http.Request, c access.Caller, eid, action string, extra map[string]any, code string) (objects.Experiment, error) {
	e, err := s.Store.Experiments().Get(r.Context(), eid)
	if err != nil {
		return e, storeError(err, code, "no experiment has the id "+eid)
	}
	m := experimentMembers(e, c)
	maps.Copy(m, extra)
	_, err = s.allowed(r, c, action, e.ID, m, code, "no experiment the caller may see has the id "+eid)
	return e, err
}

// readExperiment reads an experiment the caller may read.
func (s *Server) readExperiment(r *http.Request, c access.Caller, eid string) (objects.Experiment, error) {
	return s.experimentAs(r, c, eid, authorizer.ActionExperimentRead, nil, apierr.CodeExperimentNotFound)
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
	Mode     string            `json:"mode"`
	Metadata map[string]string `json:"metadata"`
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
	return suiteDigest, candDigests, objects.ValidateMetadata("metadata", in.Metadata)
}

// resolvePlan reads a create's suite and candidates in an owner's
// context, with every version the suite pins.
func (s *Server) resolvePlan(r *http.Request, owner string, in createBody, suiteDigest string, candDigests []string) (plan, error) {
	p := plan{owner: owner, suiteDigest: suiteDigest, candDigests: candDigests}
	vs := s.Store.Versions()
	read := func(kind, field, ref, digest string) (objects.Version, error) {
		v, err := vs.ByDigest(r.Context(), kind, owner, digest)
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
	for _, g := range p.resolved.Graders {
		if g.ModelJudge != nil && !slices.Contains(judges, g.ModelJudge.Model) {
			judges = append(judges, g.ModelJudge.Model)
		}
	}
	slices.Sort(judges)
	cells := len(p.resolved.Suite.Tasks) * len(candDigests)
	m[authorizer.FieldExecutors], m[authorizer.FieldModels] = executors, strs(models)
	m[authorizer.FieldJudgeModels], m[authorizer.FieldCredentialAudiences] = strs(judges), strs(audiences)
	m[authorizer.FieldCells], m[authorizer.FieldTrialsTotal] = cells, cells*trials
	return m
}

// unbuilt refuses what this release cannot run (CHANGELOG): a candidate
// whose executor is not built, a grader that needs a core this release
// does not reach, inputs that need Arca to be delivered, and a cap that
// needs a Budget made at Lux when no authorizer answered one.
func unbuilt(in createBody, p plan, budgetAssigned bool) error {
	for i, c := range p.candBodies {
		if c.Kind != objects.CandidateExternal {
			return apierr.OnField(apierr.CodeExecutorUnavailable, "candidates["+itoa(i)+"]", "the "+objects.ExecutorOf(c.Kind)+" executor that runs a candidate of kind "+c.Kind+" is not part of this release")
		}
	}
	core := func(name, field, why string) error {
		return &apierr.Error{Code: apierr.CodeCoreNotConfigured, Core: name, Field: field, Detail: why}
	}
	su := p.resolved.Suite
	for i, td := range su.Tasks {
		t := p.resolved.Tasks[td]
		if len(t.Checks) > 0 {
			return core("cella", "suite.tasks["+itoa(i)+"].checks", "the task's checks run in a check sandbox of Cella, which this release does not reach")
		}
		if len(t.Inputs) > 0 && in.Mode != objects.ModeImport {
			return core("arca", "suite.tasks["+itoa(i)+"].inputs", "the task's inputs reach an external harness through Arca links, which this release does not mint")
		}
	}
	judged := func(field, digest string) error {
		switch g := p.resolved.Graders[digest]; g.Kind {
		case objects.GraderModelJudge:
			return core("lux", field, "a model judge calls its model through Lux on a Key this release does not lease")
		case objects.GraderAgentJudge:
			return core("topos", field, "an agent judge runs as a Topos session, which this release does not start")
		}
		return nil
	}
	for i, d := range su.Graders {
		if err := judged("suite.graders["+itoa(i)+"]", d); err != nil {
			return err
		}
	}
	for i, d := range su.Canonicalizers {
		if err := judged("suite.canonicalizers["+itoa(i)+"]", d); err != nil {
			return err
		}
	}
	if su.DeclineJudge != "" {
		if err := judged("suite.decline_judge", su.DeclineJudge); err != nil {
			return err
		}
	}
	if in.Budget.CapUSDMicro != nil && !budgetAssigned {
		return core("lux", "budget.cap_usd_micro", "a cap is a Budget made at Lux as the owner, which this release does not do; name a Budget, or let the authorizer assign one")
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
	// The plan is resolved in the owner the request names, or the caller;
	// a reference that resolves to nothing is answered only after the
	// question, so it cannot probe what another owner holds.
	provisional := r.URL.Query().Get("owner")
	if provisional == "" {
		provisional = c.Subject
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
	if err := unbuilt(in, p, l.Budget != ""); err != nil {
		return err
	}
	mode := in.Mode
	if mode == "" {
		mode = objects.ModeExecute
	}
	e := objects.Experiment{
		ID: eid, Owner: p.owner, Suite: suiteDigest, Candidates: candDigests, Trials: trials, Budget: objects.Budget{Name: budget},
		Parallelism: parallelism, Mode: mode, CreatedBy: c.Subject, Metadata: in.Metadata,
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
			smoke := execute.Smoke(cell.ID, trials)
			nc := store.NewCell{Cell: cell, Smoke: &store.Rollout{Rollout: smoke.Rollout}}
			for _, t := range smoke.Trials {
				nc.Smoke.Trials = append(nc.Smoke.Trials, store.RolloutTrial{Index: t.Index})
			}
			ne.Cells = append(ne.Cells, nc)
		}
	}
	for ci, d := range candDigests {
		if d == e.Reference.Candidate {
			continue
		}
		cmp := objects.Comparison{
			ID: id.New(id.Comparison), Candidate: d, Baseline: e.Reference.Baseline, ReferenceCandidate: e.Reference.Candidate,
			Automatic: true, State: objects.ComparisonComputing,
		}
		if p.candBodies[ci].Kind == objects.CandidateExternal {
			cmp.DeclaredProvenance = append(cmp.DeclaredProvenance, "candidate")
		}
		if referenceKind == objects.CandidateExternal {
			cmp.DeclaredProvenance = append(cmp.DeclaredProvenance, "reference")
		}
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
	reply(w, http.StatusOK, e)
	return nil
}

func (s *Server) cancelExperiment(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.Store.Experiments().Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return storeError(err, apierr.CodeExperimentNotFound, "no experiment has the id "+r.PathValue("id"))
	}
	members := map[string]any{authorizer.FieldOwner: e.Owner, authorizer.FieldSuite: e.Suite, authorizer.FieldState: e.State}
	if _, err := s.allowed(r, c, authorizer.ActionExperimentCancel, e.ID, members, apierr.CodeExperimentNotFound, "no experiment the caller may cancel has this id"); err != nil {
		return err
	}
	out, err := s.Store.Experiments().Cancel(r.Context(), e.ID)
	if err != nil {
		return storeError(err, apierr.CodeExperimentNotFound, "the experiment is gone")
	}
	reply(w, http.StatusOK, out)
	return nil
}

func (s *Server) closeExperiment(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.experimentAs(r, c, r.PathValue("id"), authorizer.ActionExperimentUpdate, nil, apierr.CodeExperimentNotFound)
	if err != nil {
		return err
	}
	out, err := s.Store.Experiments().Close(r.Context(), e.ID)
	if err != nil {
		return storeError(err, apierr.CodeExperimentNotFound, "the experiment is gone")
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
// missing cell and a deny are cell_not_found.
func (s *Server) cellOf(r *http.Request, c access.Caller, cid string) (objects.Cell, objects.Experiment, error) {
	cell, err := s.Store.Experiments().Cell(r.Context(), cid)
	if err != nil {
		return cell, objects.Experiment{}, storeError(err, apierr.CodeCellNotFound, "no cell has the id "+cid)
	}
	e, err := s.experimentAs(r, c, cell.Experiment, authorizer.ActionExperimentRead, nil, apierr.CodeCellNotFound)
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
	cell, err := s.Store.Experiments().Cell(r.Context(), cid)
	if err != nil {
		return storeError(err, apierr.CodeCellNotFound, "no cell has the id "+cid)
	}
	extra := map[string]any{authorizer.FieldCell: cell.ID, authorizer.FieldCellState: cell.State, authorizer.FieldParkedReason: cell.ParkedReason}
	if _, err := s.experimentAs(r, c, cell.Experiment, authorizer.ActionCellRetry, extra, apierr.CodeCellNotFound); err != nil {
		return err
	}
	out, err := s.Store.Experiments().RetryCell(r.Context(), cid)
	if err != nil {
		return storeError(err, apierr.CodeCellNotFound, "the cell is gone")
	}
	s.advance(r, cell.Experiment)
	if out, err = s.Store.Experiments().Cell(r.Context(), cid); err != nil {
		return apierr.Internal(err)
	}
	reply(w, http.StatusOK, out)
	return nil
}
