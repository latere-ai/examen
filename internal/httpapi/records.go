// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// recordRoutes are the baseline and taxonomy routes under a suite.
func recordRoutes() []route {
	return []route{
		{method: http.MethodPut, path: "/suites/{suite}/baselines/{name}", handle: (*Server).pinBaseline},
		{method: http.MethodGet, path: "/suites/{suite}/baselines/{name}", handle: (*Server).currentBaseline},
		{method: http.MethodGet, path: "/suites/{suite}/baselines/{name}/history", handle: (*Server).baselineHistory},
		{method: http.MethodGet, path: "/suites/{suite}/baselines", handle: (*Server).listBaselines},
		{method: http.MethodGet, path: "/baselines/{id}", handle: (*Server).baselineByID},
		{method: http.MethodPut, path: "/suites/{suite}/taxonomies/{metric}", handle: (*Server).appendTaxonomy},
		{method: http.MethodGet, path: "/suites/{suite}/taxonomies/{metric}", handle: (*Server).currentTaxonomy},
		{method: http.MethodGet, path: "/suites/{suite}/taxonomies/{metric}/history", handle: (*Server).taxonomyHistory},
		{method: http.MethodGet, path: "/suites/{suite}/taxonomies/{metric}/unmapped", handle: (*Server).unmappedLabels},
		{method: http.MethodGet, path: "/taxonomies/{id}", handle: (*Server).taxonomyByID},
	}
}

// baselineMembers are the fields a question about a stored baseline sends.
func baselineMembers(b objects.Baseline, c access.Caller) map[string]any {
	m := map[string]any{authorizer.FieldOwner: b.Owner, authorizer.FieldSuite: b.Suite.Name, authorizer.FieldSuiteOwner: b.Suite.Owner, authorizer.FieldName: b.Name}
	if b.Owner != c.Subject {
		m[authorizer.FieldShared] = false
	}
	return m
}

func (s *Server) pinBaseline(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	suiteName, name := r.PathValue("suite"), r.PathValue("name")
	var in struct {
		Experiment string `json:"experiment"`
		Candidate  string `json:"candidate"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	switch {
	case !objects.ValidName(objects.KindSuite, name):
		return apierr.OnField(apierr.CodeInvalidField, "name", "a baseline's name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
	case in.Experiment == "":
		return apierr.OnField(apierr.CodeMissingField, "experiment", "a pin names the experiment it pins")
	case in.Candidate == "":
		return apierr.OnField(apierr.CodeMissingField, "candidate", "a pin names the candidate it pins")
	}
	_, candidate, err := objects.ParseRef("candidate", in.Candidate)
	if err != nil {
		return err
	}
	pre, err := ifMatch(r)
	if err != nil {
		return err
	}
	requested := r.URL.Query().Get("owner")
	d, err := s.permitted(r, c, access.Ask(r, authorizer.ActionBaselinePin, access.Resource(authorizer.ActionBaselinePin, "", map[string]any{
		authorizer.FieldOwner: requested, authorizer.FieldSuite: suiteName, authorizer.FieldSuiteOwner: requested,
		authorizer.FieldName: name, authorizer.FieldExperiment: in.Experiment, authorizer.FieldCandidate: candidate,
	})))
	if err != nil {
		return err
	}
	owner := createOwner(r, c, d)
	suite := objects.SuiteName{Owner: owner, Name: suiteName}
	// The experiment is asked before any state is read, so a pin cannot
	// confirm that another owner's experiment exists.
	e, err := s.namedExperiment(r, c, "experiment", in.Experiment)
	if err != nil {
		return err
	}
	if _, err := s.Store.Versions().Current(r.Context(), objects.KindSuite, owner, suiteName); err != nil {
		return storeError(err, apierr.CodeSuiteNotFound, "the owner has no suite named "+suiteName)
	}
	switch {
	case e.State != objects.StateComplete:
		return &apierr.Error{Code: apierr.CodeExperimentNotComplete, Detail: "pin: the experiment is " + e.State, State: e.State}
	case e.SuiteName != suiteName || e.Owner != owner:
		return apierr.OnField(apierr.CodeSuiteMismatch, "experiment", "pin: the experiment ran a version of another suite")
	case !slices.Contains(e.Candidates, candidate):
		return apierr.OnField(apierr.CodeNotInExperiment, "candidate", "pin: the candidate is not one of the experiment's")
	}
	b, made, err := s.Store.Baselines().Pin(r.Context(), objects.Baseline{
		ID: id.New(id.Baseline), Owner: owner, Name: name, Suite: suite, Experiment: e.ID, Candidate: candidate, CreatedBy: c.Subject,
	}, pre)
	if err != nil {
		return precondition(err)
	}
	w.Header().Set("ETag", `"`+b.ID+`"`)
	w.Header().Set("Location", s.location("/baselines/"+b.ID))
	status := http.StatusOK
	if made {
		status = http.StatusCreated
	}
	reply(w, status, b)
	return nil
}

// namedExperiment reads an experiment another route's body names: asked
// experiment.read before any state of it is read, and a deny or a missing
// one is reference_not_found on the member that names it.
func (s *Server) namedExperiment(r *http.Request, c access.Caller, field, eid string) (objects.Experiment, error) {
	refused := apierr.OnField(apierr.CodeReferenceNotFound, field, "no experiment the caller may read has the id "+eid)
	e, err := s.Store.Experiments().Get(r.Context(), eid)
	if errors.Is(err, store.ErrNotFound) {
		return e, refused
	}
	if err != nil {
		return e, apierr.Internal(err)
	}
	d, err := s.ask(r, c, access.Ask(r, authorizer.ActionExperimentRead, access.Resource(authorizer.ActionExperimentRead, e.ID, experimentMembers(e, c))))
	if err != nil {
		return e, err
	}
	if !d.Allow {
		return e, refused
	}
	return e, nil
}

// readBaseline answers a stored baseline record the caller may read.
func (s *Server) readBaseline(r *http.Request, c access.Caller, b objects.Baseline) error {
	_, err := s.allowed(r, c, authorizer.ActionBaselineRead, b.ID, baselineMembers(b, c), apierr.CodeBaselineNotFound, "no baseline the caller may read answers here")
	return err
}

func (s *Server) currentBaseline(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	owner, err := s.contextOwner(r, c, authorizer.ActionBaselineList)
	if err != nil {
		return err
	}
	suite := objects.SuiteName{Owner: owner, Name: r.PathValue("suite")}
	b, err := s.Store.Baselines().Current(r.Context(), owner, suite, r.PathValue("name"))
	if err != nil {
		return storeError(err, apierr.CodeBaselineNotFound, "the owner has no baseline of this name on the suite")
	}
	if err := s.readBaseline(r, c, b); err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+b.ID+`"`)
	reply(w, http.StatusOK, b)
	return nil
}

func (s *Server) baselineHistory(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	owner, err := s.contextOwner(r, c, authorizer.ActionBaselineList)
	if err != nil {
		return err
	}
	suite := objects.SuiteName{Owner: owner, Name: r.PathValue("suite")}
	list := "baselines/" + suite.Name + "/" + r.PathValue("name")
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	b, err := s.Store.Baselines().Current(r.Context(), owner, suite, r.PathValue("name"))
	if err != nil {
		return storeError(err, apierr.CodeBaselineNotFound, "the owner has no baseline of this name on the suite")
	}
	if err := s.readBaseline(r, c, b); err != nil {
		return err
	}
	records, err := s.Store.Baselines().History(r.Context(), owner, suite, b.Name, store.Page{After: p.After, Limit: p.Limit})
	if err != nil {
		return apierr.Internal(err)
	}
	replyPage(w, list, p, records, func(b objects.Baseline) string { return b.ID })
	return nil
}

func (s *Server) listBaselines(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	suiteName := r.PathValue("suite")
	p, err := pageOf(r, "baselines/"+suiteName)
	if err != nil {
		return err
	}
	requested := r.URL.Query().Get("owner")
	d, err := s.permitted(r, c, access.Ask(r, authorizer.ActionBaselineList, access.Resource(authorizer.ActionBaselineList, "", map[string]any{
		authorizer.FieldOwner: requested, authorizer.FieldSuite: suiteName, authorizer.FieldSuiteOwner: requested,
	})))
	if err != nil {
		return err
	}
	owners := d.Owners(c, requested)
	suiteOwner := createOwner(r, c, d)
	var records []objects.Baseline
	if len(owners) > 0 {
		records, err = s.Store.Baselines().List(r.Context(), owners, objects.SuiteName{Owner: suiteOwner, Name: suiteName}, store.Page{After: p.After, Limit: p.Limit})
		if err != nil {
			return apierr.Internal(err)
		}
	}
	replyPage(w, "baselines/"+suiteName, p, records, func(b objects.Baseline) string { return b.ID })
	return nil
}

func (s *Server) baselineByID(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	bid := r.PathValue("id")
	b, err := s.Store.Baselines().ByID(r.Context(), bid)
	if err != nil {
		return storeError(err, apierr.CodeBaselineNotFound, "no baseline has the id "+bid)
	}
	if err := s.readBaseline(r, c, b); err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+b.ID+`"`)
	reply(w, http.StatusOK, b)
	return nil
}

// openMetric finds the open-vocabulary metric a taxonomy route names, by
// name or digest, among every version of the suite: the suite's owner, and
// the metric's digest.
func (s *Server) openMetric(r *http.Request, c access.Caller) (objects.SuiteName, objects.Version, string, error) {
	suiteName, ref := r.PathValue("suite"), r.PathValue("metric")
	owner, err := s.contextOwner(r, c, authorizer.ActionSuiteList)
	if err != nil {
		return objects.SuiteName{}, objects.Version{}, "", err
	}
	suite := objects.SuiteName{Owner: owner, Name: suiteName}
	missing := apierr.New(apierr.CodeTaxonomyNotFound, "no open-vocabulary metric of a version of the suite answers to "+ref)
	vs := s.Store.Versions()
	current, err := vs.Current(r.Context(), objects.KindSuite, owner, suiteName)
	if err != nil {
		return suite, current, "", storeError(err, apierr.CodeTaxonomyNotFound, "the owner has no suite named "+suiteName)
	}
	versions, err := vs.History(r.Context(), objects.KindSuite, owner, suiteName, store.Page{Limit: limits.MaxPageSize * 10})
	if err != nil {
		return suite, current, "", apierr.Internal(err)
	}
	seen := map[string]bool{}
	for _, v := range versions {
		var su objects.Suite
		if err := json.Unmarshal(v.Body, &su); err != nil {
			return suite, current, "", apierr.Internal(err)
		}
		for _, d := range su.Metrics {
			if seen[d] || objects.IsBuiltinMetric(d) {
				continue
			}
			seen[d] = true
			mv, err := vs.ByDigest(r.Context(), objects.KindMetric, owner, d)
			if err != nil {
				continue
			}
			var m objects.Metric
			if err := json.Unmarshal(mv.Body, &m); err != nil {
				return suite, current, "", apierr.Internal(err)
			}
			if m.Open() && (d == ref || m.Name == ref) {
				return suite, current, d, nil
			}
		}
	}
	return suite, current, "", missing
}

// taxonomyMembers are the fields of a question about a taxonomy.
func taxonomyMembers(suite objects.SuiteName, metric string, version int) map[string]any {
	return map[string]any{authorizer.FieldOwner: suite.Owner, authorizer.FieldName: suite.Name, authorizer.FieldMetric: metric, authorizer.FieldVersion: version}
}

func (s *Server) appendTaxonomy(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var in struct {
		Canonical []string          `json:"canonical"`
		Map       map[string]string `json:"map"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	pre, err := ifMatch(r)
	if err != nil {
		return err
	}
	suite, _, metric, err := s.openMetric(r, c)
	if err != nil {
		return err
	}
	cur, err := s.Store.Taxonomies().Current(r.Context(), suite, metric)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return apierr.Internal(err)
	}
	d, err := s.permitted(r, c, access.Ask(r, authorizer.ActionTaxonomyApply, access.Resource(authorizer.ActionTaxonomyApply, "", taxonomyMembers(suite, metric, cur.Version+1))))
	if err != nil {
		return err
	}
	if d.Limits.Owner != "" && d.Limits.Owner != suite.Owner {
		return apierr.Denied(authorizer.ActionTaxonomyApply, "owner")
	}
	t, err := checkTaxonomy(in.Canonical, in.Map, cur)
	if err != nil {
		return err
	}
	t.ID, t.Owner, t.Suite, t.Metric, t.CreatedBy, t.Source = id.New(id.Taxonomy), suite.Owner, suite, metric, c.Subject, objects.TaxonomySource{Owner: c.Subject}
	out, made, err := s.Store.Taxonomies().Append(r.Context(), t, pre)
	if err != nil {
		return precondition(err)
	}
	w.Header().Set("ETag", `"`+out.ID+`"`)
	w.Header().Set("Location", s.location("/taxonomies/"+out.ID))
	status := http.StatusOK
	if made {
		status = http.StatusCreated
	}
	reply(w, status, out)
	return nil
}

// checkTaxonomy holds a taxonomy body to specs/003-api.md: canonical labels
// and raw labels of 1 to LABEL_MAX_BYTES, each map value one of canonical,
// every raw label the current record maps kept, and the counts within
// their limits. Raw labels are normalized as measurements keep them.
func checkTaxonomy(canonical []string, mapping map[string]string, cur objects.Taxonomy) (objects.Taxonomy, error) {
	if len(canonical) > limits.MaxLabelsOpenVocabulary {
		return objects.Taxonomy{}, apierr.Exceeds("MAX_LABELS_OPEN_VOCABULARY", "canonical", "more canonical labels than MAX_LABELS_OPEN_VOCABULARY (4096)")
	}
	if len(mapping) > limits.MaxTaxonomyRawLabels {
		return objects.Taxonomy{}, apierr.Exceeds("MAX_TAXONOMY_RAW_LABELS", "map", "more raw labels than MAX_TAXONOMY_RAW_LABELS (10000)")
	}
	seen := map[string]bool{}
	for i, l := range canonical {
		if l == "" || len(l) > limits.LabelMaxBytes || seen[l] {
			return objects.Taxonomy{}, apierr.OnField(apierr.CodeInvalidField, "canonical["+itoa(i)+"]", "a canonical label is 1 to LABEL_MAX_BYTES (128) bytes and listed once")
		}
		seen[l] = true
	}
	norm := map[string]string{}
	for raw, to := range mapping {
		n := objects.NormalizeLabel(raw)
		switch {
		case n == "" || len(n) > limits.LabelMaxBytes:
			return objects.Taxonomy{}, apierr.OnField(apierr.CodeInvalidField, "map", "a raw label is 1 to LABEL_MAX_BYTES (128) bytes once normalized")
		case !seen[to]:
			return objects.Taxonomy{}, apierr.OnField(apierr.CodeInvalidField, "map."+raw, "a raw label maps to one of canonical")
		}
		if prev, dup := norm[n]; dup && prev != to {
			return objects.Taxonomy{}, apierr.OnField(apierr.CodeInvalidField, "map."+raw, "two raw labels that normalize to one map to two canonical labels")
		}
		norm[n] = to
	}
	for raw := range cur.Map {
		if _, kept := norm[raw]; !kept {
			return objects.Taxonomy{}, apierr.OnField(apierr.CodeInvalidField, "map", "the raw label "+raw+" that the current record maps is left out")
		}
	}
	if canonical == nil {
		canonical = []string{}
	}
	digest, err := id.Digest(map[string]any{"canonical": canonical, "map": norm})
	if err != nil {
		return objects.Taxonomy{}, apierr.Internal(err)
	}
	return objects.Taxonomy{Canonical: canonical, Map: norm, Digest: digest}, nil
}

// readTaxonomy answers whether the caller may read a taxonomy record.
func (s *Server) readTaxonomy(r *http.Request, c access.Caller, t objects.Taxonomy, objectID string) error {
	m := taxonomyMembers(t.Suite, t.Metric, t.Version)
	if t.Suite.Owner != c.Subject {
		m[authorizer.FieldShared], m[authorizer.FieldPinned] = false, false
	}
	_, err := s.allowed(r, c, authorizer.ActionTaxonomyRead, objectID, m, apierr.CodeTaxonomyNotFound, "no taxonomy the caller may read answers here")
	return err
}

func (s *Server) currentTaxonomy(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	suite, _, metric, err := s.openMetric(r, c)
	if err != nil {
		return err
	}
	t, err := s.Store.Taxonomies().Current(r.Context(), suite, metric)
	if err != nil {
		return storeError(err, apierr.CodeTaxonomyNotFound, "the metric has no taxonomy record yet")
	}
	if err := s.readTaxonomy(r, c, t, t.ID); err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+t.ID+`"`)
	reply(w, http.StatusOK, t)
	return nil
}

func (s *Server) taxonomyHistory(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	suite, _, metric, err := s.openMetric(r, c)
	if err != nil {
		return err
	}
	list := "taxonomies/" + suite.Name + "/" + metric
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	t, err := s.Store.Taxonomies().Current(r.Context(), suite, metric)
	if err != nil {
		return storeError(err, apierr.CodeTaxonomyNotFound, "the metric has no taxonomy record yet")
	}
	if err := s.readTaxonomy(r, c, t, t.ID); err != nil {
		return err
	}
	records, err := s.Store.Taxonomies().History(r.Context(), suite, metric, store.Page{After: p.After, Limit: p.Limit})
	if err != nil {
		return apierr.Internal(err)
	}
	replyPage(w, list, p, records, func(t objects.Taxonomy) string { return t.ID })
	return nil
}

func (s *Server) unmappedLabels(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	suite, current, metric, err := s.openMetric(r, c)
	if err != nil {
		return err
	}
	t, err := s.Store.Taxonomies().Current(r.Context(), suite, metric)
	objectID := t.ID
	switch {
	case errors.Is(err, store.ErrNotFound):
		// With no record yet every raw label is unmapped, and the question
		// is asked of the suite.
		objectID, t = current.ID, objects.Taxonomy{Suite: suite, Metric: metric}
	case err != nil:
		return apierr.Internal(err)
	}
	if err := s.readTaxonomy(r, c, t, objectID); err != nil {
		return err
	}
	counts, err := s.Store.Taxonomies().Labels(r.Context(), suite, metric)
	if err != nil {
		return apierr.Internal(err)
	}
	items := []store.LabelCount{}
	for _, l := range counts {
		if _, mapped := t.Map[l.Label]; !mapped {
			items = append(items, l)
		}
	}
	reply(w, http.StatusOK, listBody[store.LabelCount]{Items: items})
	return nil
}

func (s *Server) taxonomyByID(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	tid := r.PathValue("id")
	t, err := s.Store.Taxonomies().ByID(r.Context(), tid)
	if err != nil {
		return storeError(err, apierr.CodeTaxonomyNotFound, "no taxonomy has the id "+tid)
	}
	if err := s.readTaxonomy(r, c, t, t.ID); err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+t.ID+`"`)
	reply(w, http.StatusOK, t)
	return nil
}
