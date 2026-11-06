// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json/jsontext"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// answered is a comparison as a read answers it: its suite-level and
// candidate-level results, since its rows route pages the task-level ones,
// and final only once its figures are (objects.Comparison.Settled).
func answered(c objects.Comparison) objects.Comparison {
	rows := []objects.ResultRow{}
	for _, r := range c.Results {
		if r.Level != "task" {
			rows = append(rows, r)
		}
	}
	c.Results = rows
	c.Final = c.Settled()
	return c
}

// comparisonFilter reads the filters of a comparison list.
func comparisonFilter(r *http.Request) (store.ComparisonFilter, error) {
	q := r.URL.Query()
	f := store.ComparisonFilter{Candidate: q.Get("candidate"), Baseline: q.Get("baseline"), ReferenceCandidate: q.Get("reference_candidate")}
	var err error
	if f.Automatic, err = boolParam(r, "automatic"); err != nil {
		return f, err
	}
	if f.Declared, err = boolParam(r, "declared"); err != nil {
		return f, err
	}
	if f.Exploratory, err = boolParam(r, "exploratory"); err != nil {
		return f, err
	}
	if f.Final, err = boolParam(r, "final"); err != nil {
		return f, err
	}
	return f, nil
}

func replyComparisons(w http.ResponseWriter, list string, p page, cs []objects.Comparison) {
	for i := range cs {
		cs[i] = answered(cs[i])
	}
	replyPage(w, list, p, cs, func(c objects.Comparison) string { return c.ID })
}

func (s *Server) experimentComparisons(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	list := "comparisons/" + e.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	f, err := comparisonFilter(r)
	if err != nil {
		return err
	}
	f.Experiment = e.ID
	cs, err := s.Store.Experiments().Comparisons(r.Context(), f, store.Page{After: p.After, Limit: p.Limit})
	if err != nil {
		return apierr.Internal(err)
	}
	replyComparisons(w, list, p, cs)
	return nil
}

// declaredAnalysis answers an experiment's declared analysis: the stored
// one, or before any declared comparison is computed, each entry not
// computed yet.
func (s *Server) declaredAnalysis(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	a, found, err := s.Store.Experiments().DeclaredAnalysis(r.Context(), e.ID)
	if err != nil {
		return storeError(err, missing{apierr.CodeExperimentNotFound, r.PathValue("id")})
	}
	if !found {
		src := compare.Source{Experiments: s.Store.Experiments(), Versions: s.Store.Versions(), Baselines: s.Store.Baselines(), Taxonomies: s.Store.Taxonomies()}
		if a, err = compare.Pending(r.Context(), e, src); err != nil {
			return apierr.Internal(err)
		}
	}
	reply(w, http.StatusOK, a)
	return nil
}

func (s *Server) listComparisons(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	p, err := pageOf(r, "comparisons")
	if err != nil {
		return err
	}
	q := r.URL.Query()
	requested := q.Get("owner")
	d, err := s.permitted(r, c, access.Ask(r, authorizer.ActionExperimentList, access.List(authorizer.ActionExperimentList, requested)))
	if err != nil {
		return err
	}
	f, err := comparisonFilter(r)
	if err != nil {
		return err
	}
	if f.SingleVariable, err = boolParam(r, "single_variable"); err != nil {
		return err
	}
	f.Owners, f.Experiment = d.Owners(c, requested), q.Get("experiment")
	if ref := q.Get("suite"); ref != "" {
		if _, digest, err := objects.ParseRef("suite", ref); err == nil {
			f.Suite = digest
		} else {
			f.SuiteName = ref
		}
	}
	var cs []objects.Comparison
	if len(f.Owners) > 0 {
		if cs, err = s.Store.Experiments().Comparisons(r.Context(), f, store.Page{After: p.After, Limit: p.Limit}); err != nil {
			return apierr.Internal(err)
		}
	}
	replyComparisons(w, "comparisons", p, cs)
	return nil
}

// comparisonOf reads a comparison and asks experiment.read of its
// experiment: a missing comparison and a deny are the comparison's one
// 404.
func (s *Server) comparisonOf(r *http.Request, c access.Caller, cid string) (objects.Comparison, objects.Experiment, error) {
	miss := missing{apierr.CodeComparisonNotFound, cid}
	cmp, err := s.Store.Experiments().Comparison(r.Context(), cid)
	if err != nil {
		return cmp, objects.Experiment{}, storeError(err, miss)
	}
	e, err := s.experimentAs(r, c, cmp.Experiment, authorizer.ActionExperimentRead, nil, miss)
	return cmp, e, err
}

func (s *Server) getComparison(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	cmp, _, err := s.comparisonOf(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	reply(w, http.StatusOK, answered(cmp))
	return nil
}

func (s *Server) comparisonRows(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	cmp, e, err := s.comparisonOf(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	q := r.URL.Query()
	list := "rows/" + cmp.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	res, err := s.experimentSuite(r, e)
	if err != nil {
		return err
	}
	metricPos := map[string]int{}
	for i, d := range res.Suite.Metrics {
		metricPos[d] = i
	}
	taskPos := map[string]int{}
	for i, d := range res.Suite.Tasks {
		taskPos[res.Tasks[d].Name] = i
	}
	var rows []objects.ResultRow
	for _, row := range cmp.Results {
		if row.Level != "task" || (q.Get("verdict") != "" && row.Verdict != q.Get("verdict")) ||
			(q.Get("task") != "" && row.Task != q.Get("task")) ||
			(q.Get("metric") != "" && row.Metric != q.Get("metric") && row.MetricName != q.Get("metric")) {
			continue
		}
		rows = append(rows, row)
	}
	slices.SortStableFunc(rows, func(a, b objects.ResultRow) int {
		if d := metricPos[a.Metric] - metricPos[b.Metric]; d != 0 {
			return d
		}
		return taskPos[a.Task] - taskPos[b.Task]
	})
	start := 0
	if p.After != "" {
		if start, err = strconv.Atoi(p.After); err != nil || start < 0 || start > len(rows) {
			return apierr.OnField(apierr.CodeInvalidCursor, "cursor", "the cursor was not issued by this comparison's rows")
		}
	}
	body := listBody[objects.ResultRow]{Items: rows[start:min(start+p.Limit, len(rows))]}
	if start+p.Limit < len(rows) {
		body.NextCursor = cursorFor(list, strconv.Itoa(start+p.Limit))
	}
	if body.Items == nil {
		body.Items = []objects.ResultRow{}
	}
	reply(w, http.StatusOK, body)
	return nil
}

func (s *Server) requestComparison(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	var in struct {
		Experiment         string `json:"experiment"`
		Candidate          string `json:"candidate"`
		Baseline           string `json:"baseline"`
		ReferenceCandidate string `json:"reference_candidate"`
		Variable           string `json:"variable"`
		Task               string `json:"task"`
		ReferenceTask      string `json:"reference_task"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	switch {
	case in.Experiment == "":
		return apierr.OnField(apierr.CodeMissingField, "experiment", "a comparison names the candidate's experiment")
	case in.Candidate == "":
		return apierr.OnField(apierr.CodeMissingField, "candidate", "a comparison names its candidate")
	}
	references := 0
	for _, set := range []bool{in.Baseline != "", in.ReferenceCandidate != "", in.ReferenceTask != ""} {
		if set {
			references++
		}
	}
	_, known := objects.Variables[in.Variable]
	switch {
	case references != 1:
		return apierr.OnField(apierr.CodeInvalidField, "baseline", "a comparison names exactly one reference: baseline, reference_candidate, or a reference_task with the variable task")
	case (in.ReferenceTask != "") != (in.Variable == "task"):
		return apierr.OnField(apierr.CodeInvalidField, "reference_task", "reference_task and the variable task go together")
	case in.Variable == "task" && (in.Task == "" || in.Task == in.ReferenceTask):
		return apierr.OnField(apierr.CodeInvalidField, "task", "a comparison across tasks names two different tasks")
	case in.Variable != "" && !known:
		return apierr.OnField(apierr.CodeInvalidField, "variable", "variable is one logical variable: agent, model, reasoning, tools, environment, workspace or task")
	}
	e, err := s.readExperiment(r, c, in.Experiment)
	if err != nil {
		return err
	}
	_, candidate, err := objects.ParseRef("candidate", in.Candidate)
	if err != nil {
		return err
	}
	if !slices.Contains(e.Candidates, candidate) {
		return apierr.OnField(apierr.CodeNotInExperiment, "candidate", "the candidate is not one of the experiment's")
	}
	cmp := objects.Comparison{ID: id.New(id.Comparison), Owner: e.Owner, Experiment: e.ID, Candidate: candidate, Exploratory: true, State: objects.ComparisonComputing}
	final := e.State == objects.StateComplete
	switch {
	case in.Baseline != "":
		b, err := s.Store.Baselines().ByID(r.Context(), in.Baseline)
		if errors.Is(err, store.ErrNotFound) || (err == nil && b.Owner != e.Owner) {
			return apierr.OnField(apierr.CodeReferenceNotFound, "baseline", "no baseline the caller may use has the id "+in.Baseline)
		}
		if err != nil {
			return apierr.Internal(err)
		}
		if b.Suite.Owner != e.Owner || b.Suite.Name != e.SuiteName {
			return apierr.OnField(apierr.CodeSuiteMismatch, "baseline", "the baseline pins an experiment of another suite")
		}
		cmp.Baseline = b.ID
	case in.ReferenceCandidate != "":
		_, ref, err := objects.ParseRef("reference_candidate", in.ReferenceCandidate)
		if err != nil {
			return err
		}
		if !slices.Contains(e.Candidates, ref) {
			return apierr.OnField(apierr.CodeNotInExperiment, "reference_candidate", "the reference candidate is not one of the experiment's")
		}
		cmp.ReferenceCandidate = ref
	default:
		res, err := s.experimentSuite(r, e)
		if err != nil {
			return err
		}
		cells, err := s.Store.Experiments().AllCells(r.Context(), e.ID)
		if err != nil {
			return apierr.Internal(err)
		}
		for _, f := range []struct{ field, name string }{{"task", in.Task}, {"reference_task", in.ReferenceTask}} {
			d, ok := res.TaskByName(f.name)
			if !ok || !slices.ContainsFunc(cells, func(cl objects.Cell) bool { return cl.Task == d && cl.Candidate == candidate }) {
				return apierr.OnField(apierr.CodeNotInExperiment, f.field, "the candidate holds no cell of the task "+f.name)
			}
			if f.field == "task" {
				cmp.Task = d
			} else {
				cmp.ReferenceTask = d
			}
		}
	}
	if in.Variable != "" {
		cmp.Variable = objects.Variables[in.Variable]
	}
	cmp.Final = final
	if err := s.Store.Experiments().CreateComparison(r.Context(), cmp); err != nil {
		return apierr.Internal(err)
	}
	if s.Engine != nil {
		if err := s.Engine.Compute(r.Context()); err != nil {
			s.log().WarnContext(r.Context(), "comparisons were not computed; the ticker takes them", "error", apierr.LogText(err))
		}
	}
	out, err := s.Store.Experiments().Comparison(r.Context(), cmp.ID)
	if err != nil {
		return apierr.Internal(err)
	}
	w.Header().Set("Location", s.location("/comparisons/"+cmp.ID))
	reply(w, http.StatusCreated, answered(out))
	return nil
}

// gridMetric is the metric a grid shows.
type gridMetric struct {
	Digest    string `json:"digest"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Aggregate string `json:"aggregate,omitempty"`
	Direction string `json:"direction"`
}

// gridCandidate is one column of a grid.
type gridCandidate struct {
	Digest string `json:"digest"`
	Name   string `json:"name"`
}

// gridTrials counts a cell's trials.
type gridTrials struct {
	Declared int            `json:"declared"`
	Pending  int            `json:"pending"`
	Running  int            `json:"running"`
	Checking int            `json:"checking"`
	Done     int            `json:"done"`
	Retried  int            `json:"retried"`
	Outcomes map[string]int `json:"outcomes"`
}

// gridCell is one entry of a grid row.
type gridCell struct {
	Cell         string         `json:"cell"`
	Candidate    string         `json:"candidate"`
	State        string         `json:"state"`
	ParkedReason string         `json:"parked_reason,omitempty"`
	Trials       gridTrials     `json:"trials"`
	Summary      jsontext.Value `json:"summary,omitzero"`
	Verdict      string         `json:"verdict,omitempty"`
	Reason       string         `json:"reason,omitempty"`
	Comparison   string         `json:"comparison,omitempty"`
}

// gridRow is one task of a grid.
type gridRow struct {
	Task             taskName       `json:"task"`
	Cells            []gridCell     `json:"cells"`
	ReferenceSummary jsontext.Value `json:"reference_summary,omitzero"`
}

// gridAnswer is the candidate × task grid of specs/003-api.md.
type gridAnswer struct {
	Experiment string            `json:"experiment"`
	State      string            `json:"state"`
	Reference  objects.Reference `json:"reference"`
	Metric     gridMetric        `json:"metric"`
	Candidates []gridCandidate   `json:"candidates"`
	Items      []gridRow         `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

// columnComparisons are the comparison each candidate's column reads: the
// one the comparison parameter names for it, else its newest automatic
// comparison against the experiment's reference.
func (s *Server) columnComparisons(r *http.Request, e objects.Experiment) (map[string]objects.Comparison, error) {
	named := r.URL.Query()["comparison"]
	out := map[string]objects.Comparison{}
	all, err := s.Store.Experiments().Comparisons(r.Context(), store.ComparisonFilter{Experiment: e.ID}, store.Page{Limit: 1000})
	if err != nil {
		return nil, apierr.Internal(err)
	}
	for _, cid := range named {
		i := slices.IndexFunc(all, func(c objects.Comparison) bool { return c.ID == cid })
		if i < 0 {
			return nil, apierr.OnField(apierr.CodeInvalidField, "comparison", "the comparison "+cid+" is not one of this experiment's")
		}
		out[all[i].Candidate] = all[i]
	}
	for _, c := range all {
		// Newest first, so the first automatic one of a candidate against
		// the current reference is its newest.
		if _, set := out[c.Candidate]; set || !c.Automatic || c.Baseline != e.Reference.Baseline || c.ReferenceCandidate != e.Reference.Candidate {
			continue
		}
		out[c.Candidate] = c
	}
	return out, nil
}

// taskResult is a comparison's task-level result of a metric for a task.
func taskResult(c objects.Comparison, metric, task string) (objects.ResultRow, bool) {
	for _, row := range c.Results {
		if row.Level == "task" && row.Metric == metric && row.Task == task {
			return row, true
		}
	}
	return objects.ResultRow{}, false
}

func (s *Server) grid(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	list := "grid/" + e.ID
	p, err := pageOf(r, list)
	if err != nil {
		return err
	}
	q := r.URL.Query()
	v, err := s.viewOf(r, e)
	if err != nil {
		return err
	}
	metricDigest := v.res.Suite.PrimaryMetric
	if ref := q.Get("metric"); ref != "" {
		d, _, ok := v.res.MetricByRef(ref)
		if !ok {
			return apierr.OnField(apierr.CodeInvalidField, "metric", "the suite lists no metric "+ref)
		}
		metricDigest = d
	}
	m := v.res.Metrics[metricDigest]
	cols, err := s.columnComparisons(r, e)
	if err != nil {
		return err
	}
	counts, err := s.Store.Experiments().Counts(r.Context(), e.ID)
	if err != nil {
		return apierr.Internal(err)
	}
	names, err := s.Store.Versions().Many(r.Context(), e.CandidateIDs)
	if err != nil {
		return apierr.Internal(err)
	}
	out := gridAnswer{Experiment: e.ID, State: e.State, Reference: e.Reference, Items: []gridRow{},
		Metric: gridMetric{Digest: metricDigest, Name: m.Name, Type: m.Type, Aggregate: m.Aggregate, Direction: m.Direction}}
	keep := q["candidate"]
	for i, d := range e.Candidates {
		if len(keep) == 0 || slices.Contains(keep, d) {
			out.Candidates = append(out.Candidates, gridCandidate{Digest: d, Name: names[e.CandidateIDs[i]].Name})
		}
	}
	byTaskCand := map[[2]string]objects.Cell{}
	for _, cl := range v.cells {
		byTaskCand[[2]string{cl.Task, cl.Candidate}] = cl
	}
	start := 0
	if p.After != "" {
		if start, err = strconv.Atoi(p.After); err != nil || start < 0 {
			return apierr.OnField(apierr.CodeInvalidCursor, "cursor", "the cursor was not issued by this grid")
		}
		start++
	}
	tasks, state := q["task"], q.Get("state")
	var shown []objects.Cell
	for pos := start; pos < len(v.res.Suite.Tasks); pos++ {
		td := v.res.Suite.Tasks[pos]
		task := v.res.Tasks[td]
		if len(tasks) > 0 && !slices.Contains(tasks, task.Name) && !slices.Contains(tasks, td) {
			continue
		}
		row := gridRow{Task: taskName{Name: task.Name, Digest: td}, Cells: []gridCell{}}
		holds := state == ""
		var cells []objects.Cell
		for _, col := range out.Candidates {
			cl, ok := byTaskCand[[2]string{td, col.Digest}]
			if !ok {
				continue
			}
			holds = holds || cl.State == state
			ct := counts[cl.ID]
			entry := gridCell{Cell: cl.ID, Candidate: col.Digest, State: cl.State, ParkedReason: cl.ParkedReason, Trials: gridTrials{
				Declared: e.Trials, Pending: ct.States[objects.StatePending], Running: ct.States[objects.StateRunning],
				Checking: ct.States[objects.StateChecking], Done: ct.States[objects.StateDone], Retried: ct.Retried, Outcomes: ct.Outcomes,
			}}
			if entry.Trials.Outcomes == nil {
				entry.Trials.Outcomes = map[string]int{}
			}
			if cmp, ok := cols[col.Digest]; ok {
				entry.Comparison = cmp.ID
				if res, ok := taskResult(cmp, metricDigest, task.Name); ok {
					entry.Verdict, entry.Reason = res.Verdict, res.Reason
					if e.Reference.Baseline != "" && row.ReferenceSummary == nil {
						row.ReferenceSummary = res.Reference
					}
				}
			}
			row.Cells = append(row.Cells, entry)
			cells = append(cells, cl)
		}
		if !holds {
			continue
		}
		if len(out.Items) == p.Limit {
			last := out.Items[len(out.Items)-1].Task.Digest
			out.NextCursor = cursorFor(list, strconv.Itoa(slices.Index(v.res.Suite.Tasks, last)))
			break
		}
		out.Items = append(out.Items, row)
		shown = append(shown, cells...)
	}
	// The page's summaries are computed in one pass over its cells.
	src := compare.Source{Experiments: s.Store.Experiments(), Versions: s.Store.Versions(), Baselines: s.Store.Baselines(), Taxonomies: s.Store.Taxonomies()}
	summaries, err := s.analyzer().Summaries(r.Context(), e, shown, metricDigest, src)
	if err != nil && !errors.Is(err, compare.ErrNotComputed) {
		return apierr.Internal(err)
	}
	for i := range out.Items {
		for j := range out.Items[i].Cells {
			out.Items[i].Cells[j].Summary = summaries[out.Items[i].Cells[j].Cell]
		}
	}
	if out.Candidates == nil {
		out.Candidates = []gridCandidate{}
	}
	reply(w, http.StatusOK, out)
	return nil
}
