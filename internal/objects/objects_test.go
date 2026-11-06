// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
)

const (
	d1 = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	d2 = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	d3 = "sha256:3333333333333333333333333333333333333333333333333333333333333333"
	d4 = "sha256:4444444444444444444444444444444444444444444444444444444444444444"
	d5 = "sha256:5555555555555555555555555555555555555555555555555555555555555555"
)

// code is the code and the field of an error, and "" for none.
func code(err error) (string, string) {
	var e *apierr.Error
	if err == nil {
		return "", ""
	}
	if !errors.As(err, &e) {
		return "not-apierr", err.Error()
	}
	return e.Code, e.Field
}

func expect(t *testing.T, name string, err error, wantCode, wantField string) {
	t.Helper()
	got, field := code(err)
	if got != wantCode || (wantField != "" && field != wantField) {
		t.Errorf("%s: got %s on %q (%v), want %s on %q", name, got, field, err, wantCode, wantField)
	}
}

func TestNamesAndRefs(t *testing.T) {
	if !ValidName(KindMetric, "tests_pass") || ValidName(KindMetric, "Tests") || ValidName(KindMetric, "a-b") {
		t.Fatal("metric names")
	}
	if !ValidName(KindSuite, "release.v2") || ValidName(KindSuite, "-x") || ValidName(KindSuite, id.New(id.Suite)) {
		t.Fatal("suite names")
	}
	if ValidName("nope", "") {
		t.Fatal("an empty name")
	}
	if n, d, err := ParseRef("f", "release@"+d1); err != nil || n != "release" || d != d1 {
		t.Fatalf("name@digest: %q %q %v", n, d, err)
	}
	if _, d, err := ParseRef("f", d2); err != nil || d != d2 {
		t.Fatal("a digest")
	}
	_, _, err := ParseRef("suite", "release")
	expect(t, "a name alone", err, apierr.CodeUnpinnedReference, "suite")
	_, _, err = ParseRef("suite", strings.Repeat("x", 100))
	if !strings.Contains(err.Error(), "...") {
		t.Fatal("a long value is cut in the detail")
	}
	if Prefix("nope") != "" || Prefix(KindCandidate) != id.Candidate || len(Kinds()) != 5 {
		t.Fatal("prefixes")
	}
	for _, k := range Kinds() {
		if Prefix(k) == "" {
			t.Errorf("%s has no prefix", k)
		}
	}
	if len(Outcomes()) != 7 || len(ExperimentStates()) != 5 || len(CellStates()) != 8 || len(TrialStates()) != 4 {
		t.Fatal("the enumerations")
	}
	if !Excluded(OutcomeCanceled) || Excluded(OutcomeTimedOut) || !Parked(StateBlocked) || Parked(StateRunning) {
		t.Fatal("excluded and parked")
	}
	expect(t, "metadata", ValidateMetadata("metadata", map[string]string{"examen.x": "1"}), apierr.CodeInvalidField, "metadata.examen.x")
	expect(t, "an empty key", ValidateMetadata("metadata", map[string]string{"": "1"}), apierr.CodeInvalidField, "metadata")
	many := map[string]string{}
	for i := range 17 {
		many[strings.Repeat("k", i+1)] = "v"
	}
	expect(t, "many pairs", ValidateMetadata("metadata", many), apierr.CodeLimitExceeded, "metadata")
	if ValidateMetadata("metadata", map[string]string{"team": "core"}) != nil {
		t.Fatal("plain metadata")
	}
}

func TestMetricRules(t *testing.T) {
	score := Metric{Name: "quality", Type: TypeScore, Range: []float64{0, 5}, Direction: HigherIsBetter, Aggregate: AggregateMean}
	label := Metric{Name: "topic", Type: TypeLabel, Labels: []string{"a", "b"}, Direction: DirectionNone, Aggregate: AggregateDistribution}
	open := Metric{Name: "theme", Type: TypeLabel, Vocabulary: VocabularyOpen, Direction: DirectionNone, Aggregate: AggregateDistribution}
	repeat := Metric{Name: "repeat", Type: TypeScore, Range: []float64{0, 1}, Direction: DirectionNone, Level: LevelCell, Of: d1, Aggregate: AggregateRepeatRate}
	distinct := Metric{Name: "distinct", Type: TypeCount, Direction: DirectionNone, Level: LevelCell, Of: d1, Aggregate: AggregateDistinctCount}
	candidate := Metric{Name: "overall", Type: TypeScore, Range: []float64{0, 1}, Direction: HigherIsBetter, Level: LevelCandidate}
	for _, ok := range []Metric{score, label, open, repeat, distinct, candidate,
		{Name: "pass", Type: TypeBinary, Direction: HigherIsBetter, Aggregate: AggregateRate, AppliesTo: []string{OutcomeCompleted}}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%s: %v", ok.Name, err)
		}
	}
	with := func(m Metric, f func(*Metric)) Metric { f(&m); return m }
	tooMany := make([]string, 65)
	for i := range tooMany {
		tooMany[i] = strings.Repeat("l", i+1)
	}
	cases := []struct {
		name  string
		m     Metric
		code  string
		field string
	}{
		{"name", with(score, func(m *Metric) { m.Name = "Bad" }), apierr.CodeInvalidField, "name"},
		{"type", with(score, func(m *Metric) { m.Type = "ratio" }), apierr.CodeInvalidField, "type"},
		{"direction", with(score, func(m *Metric) { m.Direction = "up" }), apierr.CodeInvalidField, "direction"},
		{"label direction", with(label, func(m *Metric) { m.Direction = HigherIsBetter }), apierr.CodeInvalidField, "direction"},
		{"no range", with(score, func(m *Metric) { m.Range = nil }), apierr.CodeMissingField, "range"},
		{"bad range", with(score, func(m *Metric) { m.Range = []float64{5, 0} }), apierr.CodeInvalidField, "range"},
		{"nan range", with(score, func(m *Metric) { m.Range = []float64{math.NaN(), 1} }), apierr.CodeInvalidField, "range"},
		{"range on count", Metric{Name: "n", Type: TypeCount, Range: []float64{0, 1}, Direction: DirectionNone, Aggregate: AggregateMean}, apierr.CodeInvalidField, "range"},
		{"labels on score", with(score, func(m *Metric) { m.Labels = []string{"a"} }), apierr.CodeInvalidField, "labels"},
		{"closed without labels", with(label, func(m *Metric) { m.Labels = nil }), apierr.CodeMissingField, "labels"},
		{"open with labels", with(open, func(m *Metric) { m.Labels = []string{"a"} }), apierr.CodeInvalidField, "labels"},
		{"vocabulary", with(label, func(m *Metric) { m.Vocabulary = "half" }), apierr.CodeInvalidField, "vocabulary"},
		{"too many labels", with(label, func(m *Metric) { m.Labels = tooMany }), apierr.CodeLimitExceeded, "labels"},
		{"empty label", with(label, func(m *Metric) { m.Labels = []string{""} }), apierr.CodeInvalidField, "labels[0]"},
		{"long label", with(label, func(m *Metric) { m.Labels = []string{strings.Repeat("x", 129)} }), apierr.CodeLimitExceeded, "labels[0]"},
		{"twice", with(label, func(m *Metric) { m.Labels = []string{"a", "a"} }), apierr.CodeInvalidField, "labels[1]"},
		{"level", with(score, func(m *Metric) { m.Level = "suite" }), apierr.CodeInvalidField, "level"},
		{"applies_to on a cell", with(repeat, func(m *Metric) { m.AppliesTo = []string{OutcomeCompleted} }), apierr.CodeInvalidField, "applies_to"},
		{"applies_to outcome", with(score, func(m *Metric) { m.AppliesTo = []string{OutcomeCanceled} }), apierr.CodeInvalidField, "applies_to[0]"},
		{"exclude on a trial metric", with(score, func(m *Metric) { m.ExcludeLabels = &[]string{"x"} }), apierr.CodeInvalidField, "exclude_labels"},
		{"empty exclude", with(repeat, func(m *Metric) { m.ExcludeLabels = &[]string{""} }), apierr.CodeInvalidField, "exclude_labels[0]"},
		{"a cell without of", with(repeat, func(m *Metric) { m.Of = "" }), apierr.CodeMissingField, "of"},
		{"of on a trial metric", with(score, func(m *Metric) { m.Of = d1 }), apierr.CodeInvalidField, "of"},
		{"an open candidate", Metric{Name: "x", Type: TypeLabel, Vocabulary: VocabularyOpen, Direction: DirectionNone, Level: LevelCandidate}, apierr.CodeInvalidField, "vocabulary"},
		{"candidate aggregate", with(candidate, func(m *Metric) { m.Aggregate = AggregateMean }), apierr.CodeInvalidField, "aggregate"},
		{"repeat range", with(repeat, func(m *Metric) { m.Range = []float64{0, 2} }), apierr.CodeInvalidField, "type"},
		{"distinct type", with(distinct, func(m *Metric) { m.Type = TypeScore; m.Range = []float64{0, 1} }), apierr.CodeInvalidField, "type"},
		{"cell aggregate", with(repeat, func(m *Metric) { m.Aggregate = AggregateMean }), apierr.CodeInvalidField, "aggregate"},
		{"no aggregate", with(score, func(m *Metric) { m.Aggregate = "" }), apierr.CodeMissingField, "aggregate"},
		{"wrong aggregate", with(score, func(m *Metric) { m.Aggregate = AggregateRate }), apierr.CodeInvalidField, "aggregate"},
	}
	for _, tc := range cases {
		expect(t, tc.name, tc.m.Validate(), tc.code, tc.field)
	}
	m := Metric{Of: "repeat@" + d2}
	if err := m.Normalize(); err != nil || m.Of != d2 {
		t.Fatalf("normalize of: %+v %v", m, err)
	}
	m = Metric{Of: "repeat"}
	expect(t, "an unpinned of", m.Normalize(), apierr.CodeUnpinnedReference, "of")
	if (Metric{}).LevelOf() != LevelTrial || !score.Applies(OutcomeTimedOut) || (Metric{AppliesTo: []string{"completed"}}).Applies(OutcomeDeclined) {
		t.Fatal("levels and applies_to")
	}
	if len(Builtins()) != 9 || IsBuiltinMetric("builtin:nope") || IsBuiltinMetric(d1) {
		t.Fatal("built-ins")
	}
	if b, ok := Builtin(BuiltinDigest(MetricDuration)); !ok || b.Aggregate != AggregateMedian {
		t.Fatal("the duration built-in")
	}
}

func checkGrader() Grader {
	return Grader{Name: "tests", Kind: GraderCheck, Produces: []string{d1}, Check: &Check{Command: []string{"make", "test"}, Workdir: "app", Env: map[string]string{"GOFLAGS": "-mod=mod"}, Files: &ArcaRef{Path: "files/checks", Digest: d2}, Image: "ghcr.io/x/comparators@" + d3}}
}

func judge() Grader {
	return Grader{Name: "judge", Kind: GraderModelJudge, Produces: []string{d1}, ModelJudge: &ModelJudge{Model: "claude-haiku-5-5", Prompt: "p", Inputs: JudgeInputs{FinalMessage: true, Outputs: []string{"a.txt"}}}}
}

func TestGraderRules(t *testing.T) {
	temp := 0.5
	agent := Grader{Name: "agent", Kind: GraderAgentJudge, Produces: []string{d1}, AgentJudge: &AgentJudge{Agent: "reviewer@" + d4, Instructions: "review"}}
	note := Grader{Name: "people", Kind: GraderAnnotation, Produces: []string{d1}, Annotation: &Annotation{Instructions: "rate", Sample: 0.1}}
	canon := Grader{Name: "canon", Kind: GraderModelJudge, Produces: []string{d5}, ModelJudge: &ModelJudge{Model: "m-1", Role: RoleCanonicalize}}
	cand := judge()
	cand.Level, cand.ModelJudge.Temperature = LevelCandidate, &temp
	for _, ok := range []Grader{checkGrader(), judge(), agent, note, canon, cand} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%s: %v", ok.Name, err)
		}
	}
	if !canon.Canonicalizer() || judge().Canonicalizer() || (Grader{}).LevelOf() != LevelTrial {
		t.Fatal("canonicalizer and level")
	}
	with := func(g Grader, f func(*Grader)) Grader { f(&g); return g }
	bad := 3.0
	cases := []struct {
		name  string
		g     Grader
		code  string
		field string
	}{
		{"name", with(judge(), func(g *Grader) { g.Name = "" }), apierr.CodeInvalidField, "name"},
		{"kind", with(judge(), func(g *Grader) { g.Kind = "oracle" }), apierr.CodeInvalidField, "kind"},
		{"check at candidate level", with(checkGrader(), func(g *Grader) { g.Level = LevelCandidate }), apierr.CodeInvalidField, "level"},
		{"cell level", with(judge(), func(g *Grader) { g.Level = LevelCell }), apierr.CodeInvalidField, "level"},
		{"no produces", with(judge(), func(g *Grader) { g.Produces = nil }), apierr.CodeMissingField, "produces"},
		{"unpinned produces", with(judge(), func(g *Grader) { g.Produces = []string{"quality"} }), apierr.CodeUnpinnedReference, "produces[0]"},
		{"twice", with(judge(), func(g *Grader) { g.Produces = []string{d1, d1} }), apierr.CodeInvalidField, "produces[1]"},
		{"two objects", with(judge(), func(g *Grader) { g.Check = &Check{Command: []string{"x"}} }), apierr.CodeInvalidField, "check"},
		{"no object", with(judge(), func(g *Grader) { g.ModelJudge = nil }), apierr.CodeMissingField, "model_judge"},
		{"no command", with(checkGrader(), func(g *Grader) { g.Check = &Check{} }), apierr.CodeMissingField, "check.command"},
		{"workdir", with(checkGrader(), func(g *Grader) { g.Check.Workdir = "../x" }), apierr.CodeInvalidField, "check.workdir"},
		{"env", with(checkGrader(), func(g *Grader) { g.Check.Env = map[string]string{"EXAMEN_TRIAL": "x"} }), apierr.CodeInvalidField, "check.env.EXAMEN_TRIAL"},
		{"files", with(checkGrader(), func(g *Grader) { g.Check.Files = &ArcaRef{Path: "/abs", Digest: d1} }), apierr.CodeInvalidField, "check.files.path"},
		{"image tag", with(checkGrader(), func(g *Grader) { g.Check.Image = "ghcr.io/x/comparators:1" }), apierr.CodeUnpinnedReference, "check.image"},
		{"no model", with(judge(), func(g *Grader) { g.ModelJudge.Model = "" }), apierr.CodeMissingField, "model_judge.model"},
		{"alias", with(judge(), func(g *Grader) { g.ModelJudge.Model = "claude-latest" }), apierr.CodeUnpinnedReference, "model_judge.model"},
		{"preamble", with(judge(), func(g *Grader) { g.ModelJudge.Preamble = "mine" }), apierr.CodeInvalidField, "model_judge.preamble"},
		{"temperature", with(judge(), func(g *Grader) { g.ModelJudge.Temperature = &bad }), apierr.CodeInvalidField, "model_judge.temperature"},
		{"inputs", with(judge(), func(g *Grader) { g.ModelJudge.Inputs.Files = []string{"../secret"} }), apierr.CodeInvalidField, "model_judge.inputs[0]"},
		{"no prompt", with(judge(), func(g *Grader) { g.ModelJudge.Prompt = "" }), apierr.CodeMissingField, "model_judge.prompt"},
		{"role", with(judge(), func(g *Grader) { g.ModelJudge.Role = "boss" }), apierr.CodeInvalidField, "model_judge.role"},
		{"canonicalizer of two", with(canon, func(g *Grader) { g.Produces = []string{d1, d2} }), apierr.CodeInvalidField, "produces"},
		{"canonicalizer level", with(canon, func(g *Grader) { g.Level = LevelCandidate }), apierr.CodeInvalidField, "level"},
		{"no agent", with(agent, func(g *Grader) { g.AgentJudge = &AgentJudge{Instructions: "x"} }), apierr.CodeMissingField, "agent_judge.agent"},
		{"agent by name", with(agent, func(g *Grader) { g.AgentJudge = &AgentJudge{Agent: "reviewer", Instructions: "x"} }), apierr.CodeUnpinnedReference, "agent_judge.agent"},
		{"agent preamble", with(agent, func(g *Grader) { g.AgentJudge = &AgentJudge{Agent: d4, Instructions: "x", Preamble: "p"} }), apierr.CodeInvalidField, "agent_judge.preamble"},
		{"agent instructions", with(agent, func(g *Grader) { g.AgentJudge = &AgentJudge{Agent: d4} }), apierr.CodeMissingField, "agent_judge.instructions"},
		{"sample", with(note, func(g *Grader) { g.Annotation = &Annotation{Instructions: "x", Sample: 2} }), apierr.CodeInvalidField, "annotation.sample"},
		{"annotation instructions", with(note, func(g *Grader) { g.Annotation = &Annotation{Sample: 0.5} }), apierr.CodeMissingField, "annotation.instructions"},
	}
	for _, tc := range cases {
		expect(t, tc.name, tc.g.Validate(), tc.code, tc.field)
	}
	g := Grader{Produces: []string{"quality@" + d1, BuiltinDigest(MetricCompleted)}}
	if err := g.Normalize(); err != nil || !slices.Equal(g.Produces, []string{d1, "builtin:completed"}) {
		t.Fatalf("normalize: %v %v", g.Produces, err)
	}
	g = Grader{Produces: []string{"quality"}}
	expect(t, "normalize a name", g.Normalize(), apierr.CodeUnpinnedReference, "produces[0]")
	if !PinnedImage(d1) || PinnedImage("x@sha256:1") || PinnedModel("") || PinnedModel("a b") {
		t.Fatal("pins")
	}
}

func text(s string) *string { return &s }

func task() Task {
	return Task{
		Name: "fix-flaky", Instruction: "Fix it.",
		Inputs:      []FileSource{{Name: "brief.md", Text: text("hello")}, {Name: "data.csv", Arca: &ArcaRef{Path: "files/data.csv", Digest: d1}}},
		Reference:   []FileSource{{Name: "expected.json", Origo: &OrigoRef{Repository: "acme/app", Commit: strings.Repeat("a", 40), Path: "expected.json"}}},
		Outputs:     []string{"fields.json", "workspace/"},
		Workspace:   &Workspace{Kind: WorkspaceOrigo, Repository: "acme/app", Commit: strings.Repeat("b", 40)},
		Environment: &Environment{Image: "ghcr.io/acme/env@" + d2, Resources: jsontext.Value(`{"cpu":"2"}`)},
		Checks:      []string{d3}, Timeout: "45m", MaxTurns: 40, Evidence: []string{"logs/out.txt"},
		Metadata: map[string]string{"area": "build"},
	}
}

func TestTaskRules(t *testing.T) {
	if err := task().Validate(); err != nil {
		t.Fatal(err)
	}
	if (Task{Name: "bare"}).Validate() != nil {
		t.Fatal("a task without an instruction applies")
	}
	with := func(f func(*Task)) Task { tk := task(); f(&tk); return tk }
	cases := []struct {
		name  string
		t     Task
		code  string
		field string
	}{
		{"name", with(func(t *Task) { t.Name = "" }), apierr.CodeInvalidField, "name"},
		{"input name", with(func(t *Task) { t.Inputs[0].Name = "Bad" }), apierr.CodeInvalidField, "inputs[0].name"},
		{"two inputs", with(func(t *Task) { t.Inputs[1].Name = "brief.md" }), apierr.CodeInvalidField, "inputs[1].name"},
		{"two forms", with(func(t *Task) { t.Inputs[0].Arca = &ArcaRef{Path: "x", Digest: d1} }), apierr.CodeInvalidField, "inputs[0]"},
		{"long text", with(func(t *Task) { t.Inputs[0].Text = text(strings.Repeat("x", 64<<10+1)) }), apierr.CodeLimitExceeded, "inputs[0].text"},
		{"arca path", with(func(t *Task) { t.Inputs[1].Arca.Path = "../x" }), apierr.CodeInvalidField, "inputs[1].arca.path"},
		{"arca digest", with(func(t *Task) { t.Inputs[1].Arca.Digest = "latest" }), apierr.CodeUnpinnedReference, "inputs[1].arca.digest"},
		{"origo repository", with(func(t *Task) { t.Reference[0].Origo.Repository = "" }), apierr.CodeMissingField, "reference[0].origo.repository"},
		{"origo branch", with(func(t *Task) { t.Reference[0].Origo.Commit = "main" }), apierr.CodeUnpinnedReference, "reference[0].origo.commit"},
		{"origo path", with(func(t *Task) { t.Reference[0].Origo.Path = "/etc" }), apierr.CodeInvalidField, "reference[0].origo.path"},
		{"absolute output", with(func(t *Task) { t.Outputs = []string{"/out"} }), apierr.CodeInvalidField, "outputs[0]"},
		{"dotdot output", with(func(t *Task) { t.Outputs = []string{"a/../../b"} }), apierr.CodeInvalidField, "outputs[0]"},
		{"two outputs", with(func(t *Task) { t.Outputs = []string{"a", "a"} }), apierr.CodeInvalidField, "outputs[1]"},
		{"evidence", with(func(t *Task) { t.Evidence = []string{"../x"} }), apierr.CodeInvalidField, "evidence[0]"},
		{"workspace kind", with(func(t *Task) { t.Workspace = &Workspace{Kind: "git"} }), apierr.CodeInvalidField, "workspace.kind"},
		{"none with more", with(func(t *Task) { t.Workspace = &Workspace{Kind: WorkspaceNone, Path: "x"} }), apierr.CodeInvalidField, "workspace"},
		{"origo branch workspace", with(func(t *Task) { t.Workspace.Commit = "main" }), apierr.CodeUnpinnedReference, "workspace.commit"},
		{"origo no repository", with(func(t *Task) { t.Workspace.Repository = "" }), apierr.CodeMissingField, "workspace.repository"},
		{"origo with a path", with(func(t *Task) { t.Workspace.Path = "x" }), apierr.CodeInvalidField, "workspace"},
		{"arca with a commit", with(func(t *Task) { t.Workspace = &Workspace{Kind: WorkspaceArca, Commit: "x"} }), apierr.CodeInvalidField, "workspace"},
		{"arca workspace digest", with(func(t *Task) { t.Workspace = &Workspace{Kind: WorkspaceArca, Path: "files/ws"} }), apierr.CodeUnpinnedReference, "workspace.digest"},
		{"image tag", with(func(t *Task) { t.Environment.Image = "ghcr.io/acme/env:1" }), apierr.CodeUnpinnedReference, "environment.image"},
		{"check", with(func(t *Task) { t.Checks = []string{"tests"} }), apierr.CodeUnpinnedReference, "checks[0]"},
		{"timeout", with(func(t *Task) { t.Timeout = "soon" }), apierr.CodeInvalidField, "timeout"},
		{"long timeout", with(func(t *Task) { t.Timeout = "25h" }), apierr.CodeLimitExceeded, "timeout"},
		{"turns", with(func(t *Task) { t.MaxTurns = -1 }), apierr.CodeInvalidField, "max_turns"},
		{"metadata", with(func(t *Task) { t.Metadata = map[string]string{"examen.trial": "x"} }), apierr.CodeInvalidField, "metadata.examen.trial"},
	}
	for _, tc := range cases {
		expect(t, tc.name, tc.t.Validate(), tc.code, tc.field)
	}
	tk := Task{Checks: []string{"tests@" + d3}}
	if err := tk.Normalize(); err != nil || tk.Checks[0] != d3 {
		t.Fatal("normalize checks")
	}
	tk = Task{Checks: []string{"tests"}}
	expect(t, "normalize a name", tk.Normalize(), apierr.CodeUnpinnedReference, "checks[0]")
	if task().TimeoutOf() != 45*time.Minute || (Task{}).TimeoutOf() != 30*time.Minute {
		t.Fatal("timeouts")
	}
	var none *Workspace
	if none.Ref() != WorkspaceNone || task().Workspace.Ref() != "origo:acme/app@"+strings.Repeat("b", 40) ||
		(&Workspace{Kind: WorkspaceArca, Path: "p", Digest: d1}).Ref() != "arca:p@"+d1 {
		t.Fatal("workspace refs")
	}
	if !OutputDirectory("workspace/") || OutputDirectory("a.txt") || !task().HasOutput("fields.json") {
		t.Fatal("outputs")
	}
}

func external() Candidate {
	return Candidate{Name: "harness-hi", Kind: CandidateExternal, External: &External{Harness: "goalless", Version: "2.1.4", Digest: "git:9f2c", Model: "claude-haiku-5-5", Reasoning: "high", Flags: map[string]string{"retries": "2"}}}
}

func TestCandidateRules(t *testing.T) {
	reserved := []string{"topos", "examen"}
	topos := Candidate{Name: "agent", Kind: CandidateTopos, Topos: &Topos{Agent: d1, Model: "m-1", Reasoning: "high", Tools: []string{"bash"}}}
	program := Candidate{Name: "runner", Kind: CandidateProgram, Program: &Program{Image: "ghcr.io/x/runner@" + d2, Model: "m-2",
		Credentials: []Credential{{Audience: "lectio", Host: "lectio.example.com", Env: "TOKEN"}}, Lux: &ProgramLux{Surface: "lux"}}}
	for _, ok := range []Candidate{topos, program, external()} {
		if err := ok.Validate(reserved); err != nil {
			t.Errorf("%s: %v", ok.Name, err)
		}
	}
	if program.Audiences()[0] != "lectio" || topos.Audiences() != nil {
		t.Fatal("audiences")
	}
	for _, c := range []struct {
		c                Candidate
		model, reasoning string
	}{{topos, "m-1", "high"}, {program, "m-2", ""}, {external(), "claude-haiku-5-5", "high"}, {Candidate{}, "", ""}} {
		if c.c.Model() != c.model || c.c.Reasoning() != c.reasoning {
			t.Errorf("%s: %q %q", c.c.Name, c.c.Model(), c.c.Reasoning())
		}
	}
	with := func(c Candidate, f func(*Candidate)) Candidate {
		raw, _ := json.Marshal(c)
		var out Candidate
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		f(&out)
		return out
	}
	flags := map[string]string{}
	for i := range 17 {
		flags[strings.Repeat("f", i+1)] = "1"
	}
	cases := []struct {
		name  string
		c     Candidate
		code  string
		field string
	}{
		{"name", with(topos, func(c *Candidate) { c.Name = "" }), apierr.CodeInvalidField, "name"},
		{"kind", with(topos, func(c *Candidate) { c.Kind = "vm" }), apierr.CodeInvalidField, "kind"},
		{"two objects", with(topos, func(c *Candidate) { c.External = &External{} }), apierr.CodeInvalidField, "external"},
		{"no object", with(topos, func(c *Candidate) { c.Topos = nil }), apierr.CodeMissingField, "topos"},
		{"no agent", with(topos, func(c *Candidate) { c.Topos.Agent = "" }), apierr.CodeMissingField, "topos.agent"},
		{"agent by name", with(topos, func(c *Candidate) { c.Topos.Agent = "coder" }), apierr.CodeUnpinnedReference, "topos.agent"},
		{"no model", with(topos, func(c *Candidate) { c.Topos.Model = "" }), apierr.CodeMissingField, "topos.model"},
		{"alias", with(topos, func(c *Candidate) { c.Topos.Model = "m-latest" }), apierr.CodeUnpinnedReference, "topos.model"},
		{"no image", with(program, func(c *Candidate) { c.Program.Image = "" }), apierr.CodeMissingField, "program.image"},
		{"image tag", with(program, func(c *Candidate) { c.Program.Image = "runner:1" }), apierr.CodeUnpinnedReference, "program.image"},
		{"program alias", with(program, func(c *Candidate) { c.Program.Model = "x latest" }), apierr.CodeUnpinnedReference, "program.model"},
		{"no audience", with(program, func(c *Candidate) { c.Program.Credentials[0].Audience = "" }), apierr.CodeMissingField, "program.credentials[0].audience"},
		{"reserved", with(program, func(c *Candidate) { c.Program.Credentials[0].Audience = "topos" }), apierr.CodeCredentialAudienceReserved, "program.credentials[0].audience"},
		{"no host", with(program, func(c *Candidate) { c.Program.Credentials[0].Host = "" }), apierr.CodeMissingField, "program.credentials[0].host"},
		{"env and file", with(program, func(c *Candidate) { c.Program.Credentials[0].File = "/run/t" }), apierr.CodeInvalidField, "program.credentials[0]"},
		{"surface", with(program, func(c *Candidate) { c.Program.Lux.Surface = "grpc" }), apierr.CodeInvalidField, "program.lux.surface"},
		{"harness", with(external(), func(c *Candidate) { c.External.Harness = "Bad Name" }), apierr.CodeInvalidField, "external.harness"},
		{"version", with(external(), func(c *Candidate) { c.External.Version = "" }), apierr.CodeMissingField, "external.version"},
		{"long version", with(external(), func(c *Candidate) { c.External.Version = strings.Repeat("v", 256) }), apierr.CodeInvalidField, "external"},
		{"flags", with(external(), func(c *Candidate) { c.External.Flags = flags }), apierr.CodeLimitExceeded, "external.flags"},
		{"empty flag", with(external(), func(c *Candidate) { c.External.Flags = map[string]string{"": "x"} }), apierr.CodeInvalidField, "external.flags"},
		{"metadata", with(external(), func(c *Candidate) { c.Metadata = map[string]string{"examen.x": "y"} }), apierr.CodeInvalidField, "metadata.examen.x"},
	}
	for _, tc := range cases {
		expect(t, tc.name, tc.c.Validate(reserved), tc.code, tc.field)
	}
	if ExecutorOf(CandidateTopos) != ExecutorTopos || ExecutorOf(CandidateProgram) != ExecutorCella || ExecutorOf(CandidateExternal) != ExecutorExternal {
		t.Fatal("executors")
	}
}

// resolved is a small suite with every version it pins.
func resolved() Resolved {
	quality := Metric{Name: "quality", Type: TypeScore, Range: []float64{0, 1}, Direction: HigherIsBetter, Aggregate: AggregateMean}
	theme := Metric{Name: "theme", Type: TypeLabel, Vocabulary: VocabularyOpen, Direction: DirectionNone, Aggregate: AggregateDistribution}
	repeat := Metric{Name: "repeat", Type: TypeScore, Range: []float64{0, 1}, Direction: DirectionNone, Level: LevelCell, Of: d2, Aggregate: AggregateRepeatRate}
	t1, t2 := task(), task()
	t1.Checks, t2.Name, t2.Checks = []string{d3}, "second", nil
	return Resolved{
		Suite: Suite{Name: "release", Tasks: []string{d4, d5}, Metrics: []string{d1, d2, "sha256:6666666666666666666666666666666666666666666666666666666666666666", BuiltinDigest(MetricDeclined), BuiltinDigest(MetricDuration)},
			PrimaryMetric: d1},
		Digest:  "sha256:9999999999999999999999999999999999999999999999999999999999999999",
		Tasks:   map[string]Task{d4: t1, d5: t2},
		Metrics: map[string]Metric{d1: quality, d2: theme, "sha256:6666666666666666666666666666666666666666666666666666666666666666": repeat, BuiltinDigest(MetricDeclined): builtins[1], BuiltinDigest(MetricDuration): builtins[3]},
		Graders: map[string]Grader{d3: checkGrader()},
	}
}

func TestSuiteRules(t *testing.T) {
	s := Suite{Name: "release", Tasks: []string{"a@" + d1, d2}, Metrics: []string{d3, BuiltinDigest(MetricCostUSD)}, Graders: []string{"j@" + d4},
		Canonicalizers: []string{d5}, PrimaryMetric: "q@" + d3, DeclineJudge: "dj@" + d4, Weights: map[string]float64{"a@" + d1: 2}}
	if err := s.Normalize(); err != nil {
		t.Fatal(err)
	}
	if s.Tasks[0] != d1 || s.PrimaryMetric != d3 || s.DeclineJudge != d4 || s.Weights[d1] != 2 || s.Graders[0] != d4 {
		t.Fatalf("%+v", s)
	}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Weight(d1) != 2 || s.Weight(d2) != 1 || !s.Lists(d3) {
		t.Fatal("weights")
	}
	for _, bad := range []struct {
		s     Suite
		field string
	}{
		{Suite{Tasks: []string{"x"}}, "tasks[0]"},
		{Suite{Metrics: []string{"x"}}, "metrics[0]"},
		{Suite{Graders: []string{"x"}}, "graders[0]"},
		{Suite{Canonicalizers: []string{"x"}}, "canonicalizers[0]"},
		{Suite{PrimaryMetric: "x"}, "primary_metric"},
		{Suite{DeclineJudge: "x"}, "decline_judge"},
		{Suite{Weights: map[string]float64{"x": 1}}, "weights.x"},
	} {
		expect(t, "normalize "+bad.field, bad.s.Normalize(), apierr.CodeUnpinnedReference, bad.field)
	}
	many := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = d1
		}
		return out
	}
	with := func(f func(*Suite)) Suite {
		c := Suite{Name: "release", Tasks: []string{d1, d2}, Metrics: []string{d3}, PrimaryMetric: d3}
		f(&c)
		return c
	}
	cases := []struct {
		name  string
		s     Suite
		code  string
		field string
	}{
		{"name", with(func(s *Suite) { s.Name = "" }), apierr.CodeInvalidField, "name"},
		{"no tasks", with(func(s *Suite) { s.Tasks = nil }), apierr.CodeMissingField, "tasks"},
		{"many tasks", with(func(s *Suite) { s.Tasks = many(1001) }), apierr.CodeLimitExceeded, "tasks"},
		{"no metrics", with(func(s *Suite) { s.Metrics = nil }), apierr.CodeMissingField, "metrics"},
		{"many metrics", with(func(s *Suite) { s.Metrics = many(65) }), apierr.CodeLimitExceeded, "metrics"},
		{"no primary", with(func(s *Suite) { s.PrimaryMetric = "" }), apierr.CodeMissingField, "primary_metric"},
		{"primary not listed", with(func(s *Suite) { s.PrimaryMetric = d4 }), apierr.CodeInvalidField, "primary_metric"},
		{"a task twice", with(func(s *Suite) { s.Tasks = []string{d1, d1} }), apierr.CodeInvalidField, "tasks[1]"},
		{"weight of another task", with(func(s *Suite) { s.Weights = map[string]float64{d5: 1} }), apierr.CodeInvalidField, "weights." + d5},
		{"negative weight", with(func(s *Suite) { s.Weights = map[string]float64{d1: -1} }), apierr.CodeInvalidField, "weights." + d1},
		{"all zero", with(func(s *Suite) { s.Weights = map[string]float64{d1: 0, d2: 0} }), apierr.CodeInvalidField, "weights"},
	}
	for _, tc := range cases {
		expect(t, tc.name, tc.s.Validate(), tc.code, tc.field)
	}
}

func TestResolvedChecks(t *testing.T) {
	r := resolved()
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
	p := r.Producers()
	if p[d4][d1] != d3 || p[d4][BuiltinDigest(MetricDeclined)] != GraderDecline || p[d4][BuiltinDigest(MetricDuration)] != GraderBuiltin {
		t.Fatalf("producers of the first task %v", p[d4])
	}
	if _, has := p[d5][d1]; has {
		t.Fatal("the second task has no check, so quality has no producer there")
	}
	if g := r.GraderDigests(d4); !slices.Equal(g, []string{GraderDecline, d3}) {
		t.Fatalf("grader digests %v", g)
	}
	if d, ok := r.TaskByName("second"); !ok || d != d5 {
		t.Fatal("task by name")
	}
	if _, ok := r.TaskByName("nope"); ok {
		t.Fatal("a missing task")
	}
	if d, m, ok := r.MetricByRef("quality"); !ok || d != d1 || m.Name != "quality" {
		t.Fatal("metric by name")
	}
	if _, _, ok := r.MetricByRef("quality@" + d1); !ok {
		t.Fatal("metric by name@digest")
	}
	if _, _, ok := r.MetricByRef("theme@" + d1); ok {
		t.Fatal("a name@digest whose name differs")
	}
	if _, _, ok := r.MetricByRef("nope"); ok {
		t.Fatal("a missing metric")
	}

	// An annotation grader is the producer of a metric only it produces.
	note := Grader{Name: "people", Kind: GraderAnnotation, Produces: []string{d1}, Annotation: &Annotation{Instructions: "x", Sample: 1}}
	r = resolved()
	r.Graders["sha256:7777777777777777777777777777777777777777777777777777777777777777"] = note
	r.Suite.Graders = []string{"sha256:7777777777777777777777777777777777777777777777777777777777777777"}
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
	if got := r.Producers()[d5][d1]; got != "sha256:7777777777777777777777777777777777777777777777777777777777777777" {
		t.Fatalf("the annotation producer %q", got)
	}

	tweak := func(f func(*Resolved)) error { r := resolved(); f(&r); return r.Check() }
	const j = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
	expect(t, "two tasks of one name", tweak(func(r *Resolved) {
		t2 := r.Tasks[d5]
		t2.Name = "fix-flaky"
		r.Tasks[d5] = t2
	}), apierr.CodeInvalidField, "tasks[1]")
	expect(t, "a check that is a judge", tweak(func(r *Resolved) { r.Graders[d3] = judge() }), apierr.CodeInvalidField, "tasks[0]")
	expect(t, "a check in graders", tweak(func(r *Resolved) { r.Suite.Graders = []string{d3} }), apierr.CodeInvalidField, "graders[0]")
	expect(t, "two producers", tweak(func(r *Resolved) {
		r.Graders[j] = judge()
		r.Suite.Graders = []string{j}
	}), apierr.CodeInvalidField, "graders")
	expect(t, "a built-in produced again", tweak(func(r *Resolved) {
		g := judge()
		g.Produces = []string{BuiltinDigest(MetricDuration)}
		r.Graders[j] = g
		r.Suite.Graders = []string{j}
	}), apierr.CodeInvalidField, "graders")
	expect(t, "a bad decline judge", tweak(func(r *Resolved) {
		r.Graders[j] = judge()
		r.Suite.DeclineJudge = j
	}), apierr.CodeInvalidField, "decline_judge")
	goodDecline := judge()
	goodDecline.Produces = []string{BuiltinDigest(MetricDeclined)}
	goodDecline.ModelJudge.Inputs = JudgeInputs{FinalMessage: true, Transcript: true}
	if err := tweak(func(r *Resolved) { r.Graders[j] = goodDecline; r.Suite.DeclineJudge = j }); err != nil {
		t.Fatalf("a decline judge: %v", err)
	}
	expect(t, "a cell metric of a metric not listed", tweak(func(r *Resolved) {
		r.Suite.Metrics = []string{d1, "sha256:6666666666666666666666666666666666666666666666666666666666666666"}
	}), apierr.CodeInvalidField, "metrics[1]")
	expect(t, "exclude labels outside the closed vocabulary", tweak(func(r *Resolved) {
		theme := r.Metrics[d2]
		theme.Vocabulary, theme.Labels = VocabularyClosed, []string{"a"}
		r.Metrics[d2] = theme
		rep := r.Metrics["sha256:6666666666666666666666666666666666666666666666666666666666666666"]
		rep.ExcludeLabels = &[]string{"b"}
		r.Metrics["sha256:6666666666666666666666666666666666666666666666666666666666666666"] = rep
	}), apierr.CodeInvalidField, "metrics[2]")
	expect(t, "a candidate primary metric", tweak(func(r *Resolved) {
		q := r.Metrics[d1]
		q.Level, q.Aggregate = LevelCandidate, ""
		r.Metrics[d1] = q
	}), apierr.CodeInvalidField, "primary_metric")
	expect(t, "a candidate metric without a producer", tweak(func(r *Resolved) {
		r.Metrics[d5] = Metric{Name: "overall", Type: TypeScore, Range: []float64{0, 1}, Direction: HigherIsBetter, Level: LevelCandidate}
		r.Suite.Metrics = append(r.Suite.Metrics, d5)
	}), apierr.CodeInvalidField, "metrics[5]")
	canon := Grader{Name: "canon", Kind: GraderModelJudge, Produces: []string{d2}, ModelJudge: &ModelJudge{Model: "m", Role: RoleCanonicalize}}
	if err := tweak(func(r *Resolved) { r.Graders[j] = canon; r.Suite.Canonicalizers = []string{j} }); err != nil {
		t.Fatalf("a canonicalizer: %v", err)
	}
	expect(t, "a canonicalizer in graders", tweak(func(r *Resolved) { r.Graders[j] = canon; r.Suite.Graders = []string{j} }), apierr.CodeInvalidField, "graders[0]")
	expect(t, "a judge as canonicalizer", tweak(func(r *Resolved) { r.Graders[j] = judge(); r.Suite.Canonicalizers = []string{j} }), apierr.CodeInvalidField, "canonicalizers[0]")
	expect(t, "a canonicalizer of a closed metric", tweak(func(r *Resolved) {
		c := canon
		c.Produces = []string{d1}
		r.Graders[j] = c
		r.Suite.Canonicalizers = []string{j}
	}), apierr.CodeInvalidField, "canonicalizers[0]")
	expect(t, "two canonicalizers of one metric", tweak(func(r *Resolved) {
		r.Graders[j], r.Graders[d5] = canon, canon
		r.Suite.Canonicalizers = []string{j, d5}
	}), apierr.CodeInvalidField, "canonicalizers[1]")
}

func TestTuples(t *testing.T) {
	r := resolved()
	tu, err := r.TupleOf(d4, external())
	if err != nil {
		t.Fatal(err)
	}
	if tu.CandidateKind != CandidateExternal || tu.AgentOrImageDigest != "git:9f2c" || tu.Model != "claude-haiku-5-5" || tu.Reasoning != "high" ||
		tu.TaskHash != d4 || tu.EnvironmentDigest == "" || !strings.HasPrefix(tu.WorkspaceRef, "origo:") {
		t.Fatalf("%+v", tu)
	}
	if string(tu.Program) != `{"flags":{"retries":"2"},"harness":"goalless","version":"2.1.4"}` {
		t.Fatalf("program %s", tu.Program)
	}
	// Two external candidates that differ in reasoning alone give tuples
	// that differ in reasoning alone; ones that differ in flags differ in
	// program.
	low := external()
	low.External.Reasoning = "low"
	tl, err := r.TupleOf(d4, low)
	if err != nil {
		t.Fatal(err)
	}
	tl.Reasoning = tu.Reasoning
	a, _ := json.Marshal(tl)
	b, _ := json.Marshal(tu)
	if string(a) != string(b) {
		t.Fatalf("reasoning alone:\n%s\n%s", a, b)
	}
	flagged := external()
	flagged.External.Flags = map[string]string{"retries": "3"}
	tf, err := r.TupleOf(d4, flagged)
	if err != nil || string(tf.Program) == string(tu.Program) {
		t.Fatal("flags change the program")
	}
	topos := Candidate{Kind: CandidateTopos, Topos: &Topos{Agent: d1, Model: "m", Tools: []string{"bash"}}}
	tt, err := r.TupleOf(d5, topos)
	if err != nil || tt.AgentOrImageDigest != d1 || string(tt.Program) != "null" || tt.Tools[0] != "bash" {
		t.Fatalf("topos %+v %v", tt, err)
	}
	program := Candidate{Kind: CandidateProgram, Program: &Program{Image: "x@" + d2, Command: []string{"run"}}}
	tp, err := r.TupleOf(d5, program)
	if err != nil || tp.AgentOrImageDigest != "x@"+d2 || !strings.Contains(string(tp.Program), `"command":["run"]`) {
		t.Fatalf("program %+v %v", tp, err)
	}
	r.Tasks[d5] = Task{Name: "plain"}
	if tn, err := r.TupleOf(d5, external()); err != nil || tn.EnvironmentDigest != "" || tn.WorkspaceRef != WorkspaceNone {
		t.Fatalf("a plain task %+v %v", tn, err)
	}
}

func TestValuesAndCurrent(t *testing.T) {
	score := Metric{Type: TypeScore, Range: []float64{0, 1}}
	closed := Metric{Type: TypeLabel, Labels: []string{"yes"}}
	open := Metric{Type: TypeLabel, Vocabulary: VocabularyOpen}
	cases := []struct {
		m    Metric
		in   any
		want any
		ok   bool
	}{
		{Metric{Type: TypeBinary}, true, true, true},
		{Metric{Type: TypeBinary}, 1.0, nil, false},
		{score, 0.5, 0.5, true},
		{score, 2.0, nil, false},
		{score, math.NaN(), nil, false},
		{score, "high", nil, false},
		{Metric{Type: TypeCount}, 3.0, 3.0, true},
		{Metric{Type: TypeCount}, 2.5, nil, false},
		{Metric{Type: TypeDuration}, -1.0, nil, false},
		{Metric{Type: TypeCost}, 0.25, 0.25, true},
		{closed, "yes", "yes", true},
		{closed, "no", nil, false},
		{closed, 1.0, nil, false},
		{open, "  Fixes   the\tBug ", "fixes the bug", true},
		{open, "   ", nil, false},
	}
	for i, c := range cases {
		got, why := Value(c.m, c.in)
		if (why == "") != c.ok || got != c.want {
			t.Errorf("case %d: %v %q", i, got, why)
		}
	}
	if NormalizeLabel("Café") != "café" {
		t.Fatal("NFC")
	}
	ms := []Measurement{
		{Metric: d1, Grader: d3, Generation: 1, Value: 0.2},
		{Metric: d1, Grader: d3, Generation: 2, Value: 0.4},
		{Metric: d2, Grader: d3, Generation: 1, Value: "a"},
		{Metric: d4, Grader: Self("goalless"), Generation: 1, Value: 1.0, SelfReported: true},
		{Metric: d5, Grader: d4, Generation: 1, Annotator: "human:x", Value: 1.0},
	}
	cur := Current(ms, map[string]string{d1: d3, d2: d3, d5: d4}, []string{d1, d2, d4, d5}, "goalless")
	if cur[d1].Value != 0.4 {
		t.Fatalf("the latest generation: %v", cur[d1])
	}
	if _, has := cur[d2]; has {
		t.Fatal("generation 2 produced no value for the second metric, which is missing, never generation 1's")
	}
	if !cur[d4].SelfReported {
		t.Fatal("a metric with no producer reads the harness's own value")
	}
	if _, has := cur[d5]; has {
		t.Fatal("an annotation is no current value of a judge's metric")
	}
	if cur := Current(ms, map[string]string{}, []string{d4}, ""); len(cur) != 0 {
		t.Fatal("a trial with no harness has no self-reported value")
	}
	start := time.Date(2026, 3, 2, 14, 1, 0, 0, time.UTC)
	end := start.Add(492 * time.Second)
	tr := Trial{Outcome: OutcomeDeclined, StartedAt: &start, AgentEndedAt: &end}
	b := BuiltinValues(tr)
	if b[BuiltinDigest(MetricCompleted)] != true || b[BuiltinDigest(MetricDuration)] != 492.0 || !DeclineValue(tr) {
		t.Fatalf("built-ins %v", b)
	}
	if b := BuiltinValues(Trial{Outcome: OutcomeTimedOut}); b[BuiltinDigest(MetricCompleted)] != false || len(b) != 1 {
		t.Fatalf("a trial with no times %v", b)
	}
	if !(Trial{State: StateDone, Outcome: OutcomeCompleted}).Finished() || (Trial{State: StateDone, Outcome: OutcomeCompleted, Retried: true}).Finished() {
		t.Fatal("finished")
	}
	if !Sink(EventCellParked) || Sink(EventTrial) || !(Experiment{State: StateCanceled}).Finished() {
		t.Fatal("sink and finished")
	}
}

func TestVersionAnswer(t *testing.T) {
	v := Version{ID: "met_1", Kind: KindMetric, Owner: "o", Name: "q", Digest: d1, Current: true, Body: jsontext.Value(`{"name":"q","type":"binary"}`)}
	a, err := v.Answer()
	if err != nil || a["type"] != "binary" || a["id"] != "met_1" || a["current"] != true {
		t.Fatalf("%v %v", a, err)
	}
	if _, err := (Version{Body: jsontext.Value(`[`)}).Answer(); err == nil {
		t.Fatal("a body that does not parse")
	}
}

// TestTaskGraders: a task's own graders are pinned by digest, named once,
// judges and annotation queues of level trial that are not the suite's,
// produce for that task alone, and are among its cells' grader digests.
func TestTaskGraders(t *testing.T) {
	const j = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
	tk := task()
	tk.Graders = []string{"artifact@" + j}
	if err := tk.Normalize(); err != nil || tk.Graders[0] != j {
		t.Fatalf("normalize: %v %v", tk.Graders, err)
	}
	if err := tk.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := task()
	bad.Graders = []string{"artifact"}
	expect(t, "an unpinned task grader", bad.Normalize(), "unpinned_reference", "graders[0]")
	bad.Graders = []string{"tooshort"}
	expect(t, "a task grader that is no digest", bad.Validate(), "unpinned_reference", "graders[0]")
	bad.Graders = []string{j, j}
	expect(t, "a task grader named twice", bad.Validate(), "invalid_field", "graders[1]")

	judge := Grader{Name: "artifact", Kind: GraderModelJudge, Produces: []string{d2}, ModelJudge: &ModelJudge{Model: "m", Prompt: "p"}}
	r := resolved()
	r.Graders[j] = judge
	t1 := r.Tasks[d4]
	t1.Graders = []string{j}
	r.Tasks[d4] = t1
	if err := r.Check(); err != nil {
		t.Fatal(err)
	}
	if p := r.Producers(); p[d4][d2] != j || p[d5][d2] != "" {
		t.Fatalf("a task's judge produces for that task alone: %v", p)
	}
	if g := r.GraderDigests(d4); !slices.Contains(g, j) || slices.Contains(r.GraderDigests(d5), j) {
		t.Fatalf("grader digests %v", g)
	}
	tweak := func(f func(*Resolved)) error {
		r := resolved()
		r.Graders[j] = judge
		t1 := r.Tasks[d4]
		t1.Graders = []string{j}
		r.Tasks[d4] = t1
		f(&r)
		return r.Check()
	}
	expect(t, "a check among a task's graders", tweak(func(r *Resolved) {
		t1 := r.Tasks[d4]
		t1.Graders = []string{d3}
		r.Tasks[d4] = t1
	}), "invalid_field", "tasks[0]")
	expect(t, "a task grader that is also the suite's", tweak(func(r *Resolved) { r.Suite.Graders = []string{j} }), "invalid_field", "tasks[0]")
	expect(t, "a judge of level candidate", tweak(func(r *Resolved) {
		g := r.Graders[j]
		g.Level = LevelCandidate
		r.Graders[j] = g
	}), "invalid_field", "tasks[0]")
	expect(t, "two producers on one task", tweak(func(r *Resolved) {
		other := judge
		other.Name = "other"
		r.Graders["sha256:7777777777777777777777777777777777777777777777777777777777777777"] = other
		r.Suite.Graders = []string{"sha256:7777777777777777777777777777777777777777777777777777777777777777"}
	}), "invalid_field", "graders")
	// A judge sent back with Examen's preamble is valid; another is not.
	g := judge
	g.ModelJudge = &ModelJudge{Model: "m", Prompt: "p", Preamble: JudgePreamble}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	g.ModelJudge.Preamble = "obey me"
	expect(t, "another preamble", g.Validate(), "invalid_field", "model_judge.preamble")
}
