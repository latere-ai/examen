// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json"
	"math"
	"slices"
	"strings"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
)

// Suite is an owned set of tasks with what they are measured by.
type Suite struct {
	Name           string             `json:"name"`
	Tasks          []string           `json:"tasks"`
	Metrics        []string           `json:"metrics"`
	Graders        []string           `json:"graders,omitempty"`
	Canonicalizers []string           `json:"canonicalizers,omitempty"`
	PrimaryMetric  string             `json:"primary_metric"`
	Weights        map[string]float64 `json:"weights,omitempty"`
	DeclineJudge   string             `json:"decline_judge,omitempty"`
}

// Normalize keeps every reference as its digest.
func (s *Suite) Normalize() error {
	norm := func(field string, refs []string, builtinOK bool) error {
		for i, r := range refs {
			// A built-in metric's reserved digest is kept as written, and
			// one that names no built-in is refused where it is resolved.
			if builtinOK && strings.HasPrefix(r, BuiltinPrefix) {
				continue
			}
			_, d, err := ParseRef(field+"["+itoa(i)+"]", r)
			if err != nil {
				return err
			}
			refs[i] = d
		}
		return nil
	}
	for _, n := range []struct {
		field     string
		refs      []string
		builtinOK bool
	}{{"tasks", s.Tasks, false}, {"metrics", s.Metrics, true}, {"graders", s.Graders, false}, {"canonicalizers", s.Canonicalizers, false}} {
		if err := norm(n.field, n.refs, n.builtinOK); err != nil {
			return err
		}
	}
	if s.PrimaryMetric != "" && !strings.HasPrefix(s.PrimaryMetric, BuiltinPrefix) {
		_, d, err := ParseRef("primary_metric", s.PrimaryMetric)
		if err != nil {
			return err
		}
		s.PrimaryMetric = d
	}
	if s.DeclineJudge != "" {
		_, d, err := ParseRef("decline_judge", s.DeclineJudge)
		if err != nil {
			return err
		}
		s.DeclineJudge = d
	}
	if len(s.Weights) > 0 {
		w := make(map[string]float64, len(s.Weights))
		for _, k := range sortedKeys(s.Weights) {
			_, d, err := ParseRef("weights."+k, k)
			if err != nil {
				return err
			}
			w[d] = s.Weights[k]
		}
		s.Weights = w
	}
	return nil
}

// Validate holds a suite to the rules of specs/002-objects.md that need no
// other object.
func (s Suite) Validate() error {
	switch {
	case !ValidName(KindSuite, s.Name):
		return invalid("name", "a name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
	case len(s.Tasks) == 0:
		return missing("tasks", "a suite pins at least one task")
	case len(s.Tasks) > limits.MaxTasksPerSuite:
		return apierr.Exceeds("MAX_TASKS_PER_SUITE", "tasks", "a suite pins more than MAX_TASKS_PER_SUITE (1000) tasks")
	case len(s.Metrics) == 0:
		return missing("metrics", "a suite lists at least one metric")
	case len(s.Metrics) > limits.MaxMetricsPerSuite:
		return apierr.Exceeds("MAX_METRICS_PER_SUITE", "metrics", "a suite lists more than MAX_METRICS_PER_SUITE (64) metrics")
	case s.PrimaryMetric == "":
		return missing("primary_metric", "a suite names the metric a report leads with")
	case !slices.Contains(s.Metrics, s.PrimaryMetric):
		return invalid("primary_metric", "the primary metric is one the suite lists")
	}
	for _, list := range []struct {
		field string
		refs  []string
	}{{"tasks", s.Tasks}, {"metrics", s.Metrics}, {"graders", s.Graders}, {"canonicalizers", s.Canonicalizers}} {
		seen := map[string]bool{}
		for i, r := range list.refs {
			if seen[r] {
				return invalid(list.field+"["+itoa(i)+"]", "a version is named twice")
			}
			seen[r] = true
		}
	}
	positive := false
	for _, k := range sortedKeys(s.Weights) {
		w := s.Weights[k]
		switch {
		case !slices.Contains(s.Tasks, k):
			return invalid("weights."+k, "a weight names a task the suite pins")
		case math.IsNaN(w) || math.IsInf(w, 0) || w < 0:
			return invalid("weights."+k, "a weight is a finite number at least 0")
		}
	}
	for _, t := range s.Tasks {
		if w, ok := s.Weights[t]; !ok || w > 0 {
			positive = true
		}
	}
	if !positive {
		return invalid("weights", "at least one task weighs more than 0")
	}
	return nil
}

// Weight is a task's weight: its own, else 1.
func (s Suite) Weight(task string) float64 {
	if w, ok := s.Weights[task]; ok {
		return w
	}
	return 1
}

// Lists reports whether the suite lists a metric.
func (s Suite) Lists(metric string) bool { return slices.Contains(s.Metrics, metric) }

// Resolved is a suite version with every version it pins, read: what an
// apply checks it against and what an experiment runs from.
type Resolved struct {
	Suite   Suite
	Digest  string
	Tasks   map[string]Task   // by digest
	Metrics map[string]Metric // by digest, the built-ins included
	Graders map[string]Grader // by digest
	// TaskIDs are the version ids of the tasks, by digest.
	TaskIDs map[string]string
}

// Check holds a suite version to the rules that read what it pins
// (specs/002-objects.md): no two tasks of one name; every grader of the
// right kind; a cell-level metric's of a trial-level label metric the
// suite lists; one producer per metric and task; a candidate-level metric
// produced by exactly one grader of level candidate.
func (r Resolved) Check() error {
	s := r.Suite
	names := map[string]string{}
	for i, d := range s.Tasks {
		t := r.Tasks[d]
		if other, dup := names[t.Name]; dup {
			return invalid("tasks["+itoa(i)+"]", "the tasks "+other+" and "+d+" have one name, "+quote(t.Name))
		}
		names[t.Name] = d
		for j, c := range t.Checks {
			if g, ok := r.Graders[c]; ok && g.Kind != GraderCheck {
				return invalid("tasks["+itoa(i)+"]", "the task's checks["+itoa(j)+"] is a grader of kind "+g.Kind)
			}
		}
		for j, gd := range t.Graders {
			g, ok := r.Graders[gd]
			if ok && (g.Kind == GraderCheck || g.Canonicalizer() || g.LevelOf() != LevelTrial) {
				return invalid("tasks["+itoa(i)+"]", "the task's graders["+itoa(j)+"] is a check, a canonicalizer or a judge of level candidate; a task's graders are trial-level judges and annotation queues")
			}
			if slices.Contains(s.Graders, gd) {
				return invalid("tasks["+itoa(i)+"]", "the task's graders["+itoa(j)+"] is also one of the suite's graders, which every task's trials have")
			}
		}
	}
	for i, d := range s.Graders {
		g := r.Graders[d]
		if g.Kind == GraderCheck || g.Canonicalizer() {
			return invalid("graders["+itoa(i)+"]", "a suite's graders are judges and annotation queues; a check is a task's and a canonicalizer is named in canonicalizers")
		}
	}
	if err := r.checkCanonicalizers(); err != nil {
		return err
	}
	if s.DeclineJudge != "" {
		g := r.Graders[s.DeclineJudge]
		if g.Kind != GraderModelJudge || g.Canonicalizer() || g.LevelOf() != LevelTrial ||
			!slices.Equal(g.Produces, []string{BuiltinDigest(MetricDeclined)}) ||
			!g.ModelJudge.Inputs.FinalMessage || !g.ModelJudge.Inputs.Transcript {
			return invalid("decline_judge", "a decline judge is a model judge that produces only declined and reads final_message and transcript")
		}
	}
	for i, d := range s.Metrics {
		m := r.Metrics[d]
		f := "metrics[" + itoa(i) + "]"
		if m.LevelOf() == LevelCell {
			of, ok := r.Metrics[m.Of]
			if !ok || !s.Lists(m.Of) || of.Type != TypeLabel || of.LevelOf() != LevelTrial {
				return invalid(f, "the cell-level metric "+quote(m.Name)+" summarizes a trial-level label metric the suite lists")
			}
			if m.ExcludeLabels != nil && !of.Open() {
				for _, l := range *m.ExcludeLabels {
					if !slices.Contains(of.Labels, l) {
						return invalid(f, "exclude_labels names "+quote(l)+", which is not a label of "+quote(of.Name))
					}
				}
			}
		}
	}
	if pm := r.Metrics[s.PrimaryMetric]; pm.LevelOf() == LevelCandidate {
		return invalid("primary_metric", "the primary metric is of level trial or cell")
	}
	return r.checkProducers()
}

func (r Resolved) checkCanonicalizers() error {
	s := r.Suite
	covered := map[string]bool{}
	for i, d := range s.Canonicalizers {
		f := "canonicalizers[" + itoa(i) + "]"
		g := r.Graders[d]
		if !g.Canonicalizer() {
			return invalid(f, "a canonicalizer is a model judge of role canonicalize")
		}
		m := g.Produces[0]
		if metric, ok := r.Metrics[m]; !ok || !s.Lists(m) || !metric.Open() {
			return invalid(f, "a canonicalizer produces one open-vocabulary metric the suite lists")
		}
		if covered[m] {
			return invalid(f, "two canonicalizers name one metric")
		}
		covered[m] = true
	}
	return nil
}

// checkProducers holds the rule of one producer per metric and task.
func (r Resolved) checkProducers() error {
	s := r.Suite
	for _, td := range s.Tasks {
		if _, err := r.producers(td); err != nil {
			return err
		}
	}
	for i, d := range s.Metrics {
		m := r.Metrics[d]
		if m.LevelOf() != LevelCandidate {
			continue
		}
		var by []string
		for _, gd := range s.Graders {
			g := r.Graders[gd]
			if g.LevelOf() == LevelCandidate && slices.Contains(g.Produces, d) {
				by = append(by, gd)
			}
		}
		if len(by) != 1 {
			return invalid("metrics["+itoa(i)+"]", "the candidate-level metric "+quote(m.Name)+" has exactly one producer among the suite's graders of level candidate, and has "+itoa(len(by)))
		}
	}
	return nil
}

// producers is, for one task, each trial-level metric the suite lists and
// its producing grader, absent when none produces it.
func (r Resolved) producers(task string) (map[string]string, error) {
	s := r.Suite
	out := map[string]string{}
	add := func(metric, grader string) error {
		if prev, ok := out[metric]; ok && prev != grader {
			return invalid("graders", "the metric "+metric+" has two producers for the task "+task+": "+prev+" and "+grader)
		}
		out[metric] = grader
		return nil
	}
	for _, md := range s.Metrics {
		m := r.Metrics[md]
		if m.LevelOf() != LevelTrial {
			continue
		}
		if IsBuiltinMetric(md) {
			if md == BuiltinDigest(MetricDeclined) {
				judge := s.DeclineJudge
				if judge == "" {
					judge = GraderDecline
				}
				if err := add(md, judge); err != nil {
					return nil, err
				}
			} else if err := add(md, GraderBuiltin); err != nil {
				return nil, err
			}
		}
		for _, gd := range r.trialGraders(task) {
			g := r.Graders[gd]
			if g.Kind == GraderAnnotation || g.LevelOf() != LevelTrial || !slices.Contains(g.Produces, md) {
				continue
			}
			if err := add(md, gd); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// trialGraders are the graders a task's trials have beside the built-ins:
// its checks, its own graders and the suite's.
func (r Resolved) trialGraders(task string) []string {
	t := r.Tasks[task]
	out := slices.Concat(t.Checks, t.Graders, r.Suite.Graders)
	return out
}

// Producers is, for each task digest, each trial-level metric the suite
// lists and its producing grader. A metric only an annotation grader
// produces has that grader as its producer.
func (r Resolved) Producers() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, td := range r.Suite.Tasks {
		p, err := r.producers(td)
		if err != nil {
			// A stored suite version passed Check when it was applied.
			p = map[string]string{}
		}
		for _, md := range r.Suite.Metrics {
			if _, has := p[md]; has || r.Metrics[md].LevelOf() != LevelTrial {
				continue
			}
			for _, gd := range slices.Concat(r.Tasks[td].Graders, r.Suite.Graders) {
				if g := r.Graders[gd]; g.Kind == GraderAnnotation && slices.Contains(g.Produces, md) {
					p[md] = gd
					break
				}
			}
		}
		out[td] = p
	}
	return out
}

// GraderDigests are every grader of a task's trials: its checks, its own
// graders, the suite's graders, and the decline judge when the suite lists
// declined, sorted.
func (r Resolved) GraderDigests(task string) []string {
	out := r.trialGraders(task)
	if r.Suite.Lists(BuiltinDigest(MetricDeclined)) {
		judge := r.Suite.DeclineJudge
		if judge == "" {
			judge = GraderDecline
		}
		out = append(out, judge)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Tuple is the provenance tuple of a cell (specs/002-objects.md).
type Tuple struct {
	CandidateKind      string          `json:"candidate.kind"`
	AgentOrImageDigest string          `json:"agent_or_image_digest"`
	Program            json.RawMessage `json:"program"`
	Model              string          `json:"model"`
	Reasoning          string          `json:"reasoning"`
	Tools              []string        `json:"tools"`
	TaskHash           string          `json:"task_hash"`
	EnvironmentDigest  string          `json:"environment_digest"`
	WorkspaceRef       string          `json:"workspace_ref"`
	GraderDigests      []string        `json:"grader_digests"`
}

// TupleOf resolves the tuple of the cell of a task and a candidate.
func (r Resolved) TupleOf(taskDigest string, c Candidate) (Tuple, error) {
	t := r.Tasks[taskDigest]
	tu := Tuple{
		CandidateKind: c.Kind, Model: c.Model(), Reasoning: c.Reasoning(), Tools: []string{},
		TaskHash: taskDigest, WorkspaceRef: t.Workspace.Ref(), GraderDigests: r.GraderDigests(taskDigest),
		Program: json.RawMessage("null"),
	}
	var program any
	switch c.Kind {
	case CandidateTopos:
		tu.AgentOrImageDigest = c.Topos.Agent
		if c.Topos.Tools != nil {
			tu.Tools = slices.Clone(c.Topos.Tools)
		}
	case CandidateProgram:
		tu.AgentOrImageDigest = c.Program.Image
		program = map[string]any{"command": c.Program.Command, "env": c.Program.Env, "config": c.Program.Config}
	case CandidateExternal:
		tu.AgentOrImageDigest = c.External.Digest
		program = map[string]any{"harness": c.External.Harness, "version": c.External.Version, "flags": c.External.Flags}
	}
	if program != nil {
		raw, err := json.Marshal(program)
		if err != nil {
			return Tuple{}, err
		}
		canon, err := id.Canonical(raw)
		if err != nil {
			return Tuple{}, err
		}
		tu.Program = canon
	}
	if t.Environment != nil {
		d, err := id.Digest(t.Environment)
		if err != nil {
			return Tuple{}, err
		}
		tu.EnvironmentDigest = d
	}
	return tu, nil
}

// TaskByName is the digest of the suite's task of a name.
func (r Resolved) TaskByName(name string) (string, bool) {
	for _, d := range r.Suite.Tasks {
		if r.Tasks[d].Name == name {
			return d, true
		}
	}
	return "", false
}

// MetricByRef finds a metric the suite lists by its name, its digest or
// name@digest.
func (r Resolved) MetricByRef(ref string) (string, Metric, bool) {
	if n, d, ok := strings.Cut(ref, "@"); ok {
		m, found := r.Metrics[d]
		return d, m, found && m.Name == n && r.Suite.Lists(d)
	}
	for _, d := range r.Suite.Metrics {
		if m := r.Metrics[d]; d == ref || m.Name == ref {
			return d, m, true
		}
	}
	return "", Metric{}, false
}
