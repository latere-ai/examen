// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"math"
	"slices"
	"strings"

	"latere.ai/x/examen/internal/id"
)

// The kinds of a grader.
const (
	GraderCheck      = "check"
	GraderModelJudge = "model_judge"
	GraderAgentJudge = "agent_judge"
	GraderAnnotation = "annotation"
)

// The roles of a model judge.
const (
	RoleGrade        = "grade"
	RoleCanonicalize = "canonicalize"
)

// Grader turns a trial into measurements.
type Grader struct {
	Name       string      `json:"name"`
	Kind       string      `json:"kind"`
	Level      string      `json:"level,omitempty"`
	Produces   []string    `json:"produces"`
	Check      *Check      `json:"check,omitempty"`
	ModelJudge *ModelJudge `json:"model_judge,omitempty"`
	AgentJudge *AgentJudge `json:"agent_judge,omitempty"`
	Annotation *Annotation `json:"annotation,omitempty"`
}

// Check is a check grader's command.
type Check struct {
	Command []string          `json:"command"`
	Workdir string            `json:"workdir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Files   *ArcaRef          `json:"files,omitempty"`
	Image   string            `json:"image,omitempty"`
}

// JudgeInputs is the evidence a judge reads. Checks is the values the
// trial's checks recorded, one block of JSON from each produced metric's
// name to its value, read once every check of the trial ended.
type JudgeInputs struct {
	Transcript   bool     `json:"transcript,omitempty"`
	FinalMessage bool     `json:"final_message,omitempty"`
	Files        []string `json:"files,omitempty"`
	Outputs      []string `json:"outputs,omitempty"`
	Checks       bool     `json:"checks,omitempty"`
}

// ModelJudge is one structured call through Lux with a pinned model.
type ModelJudge struct {
	Model       string      `json:"model"`
	Prompt      string      `json:"prompt"`
	Rubric      string      `json:"rubric"`
	Inputs      JudgeInputs `json:"inputs"`
	Temperature *float64    `json:"temperature,omitempty"`
	Reasoning   string      `json:"reasoning,omitempty"`
	Preamble    string      `json:"preamble,omitempty"`
	Role        string      `json:"role,omitempty"`
}

// AgentJudge is a session of a pinned Topos agent version.
type AgentJudge struct {
	Agent        string      `json:"agent"`
	Inputs       JudgeInputs `json:"inputs"`
	Instructions string      `json:"instructions"`
	Preamble     string      `json:"preamble,omitempty"`
}

// Annotation is a queue of trials for people.
type Annotation struct {
	Instructions string  `json:"instructions"`
	Sample       float64 `json:"sample"`
}

// LevelOf is the grader's level, trial when it names none.
func (g Grader) LevelOf() string {
	if g.Level == "" {
		return LevelTrial
	}
	return g.Level
}

// Canonicalizer reports whether the grader is a model judge of role
// canonicalize.
func (g Grader) Canonicalizer() bool {
	return g.Kind == GraderModelJudge && g.ModelJudge != nil && g.ModelJudge.Role == RoleCanonicalize
}

// Normalize keeps every metric reference as its digest, so two spellings
// of one content are one version.
func (g *Grader) Normalize() error {
	for i, p := range g.Produces {
		if IsBuiltinMetric(p) {
			continue
		}
		_, d, err := ParseRef("produces["+itoa(i)+"]", p)
		if err != nil {
			return err
		}
		g.Produces[i] = d
	}
	return nil
}

// Validate holds a grader to the rules of specs/002-objects.md and
// specs/006-grading.md that need no other object.
func (g Grader) Validate() error {
	if !ValidName(KindGrader, g.Name) {
		return invalid("name", "a name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
	}
	if !slices.Contains([]string{GraderCheck, GraderModelJudge, GraderAgentJudge, GraderAnnotation}, g.Kind) {
		return invalid("kind", "kind is check, model_judge, agent_judge or annotation")
	}
	switch g.LevelOf() {
	case LevelTrial:
	case LevelCandidate:
		if g.Kind != GraderModelJudge {
			return invalid("level", "only a model judge grades at the level candidate")
		}
	default:
		return invalid("level", "a grader's level is trial or candidate")
	}
	if len(g.Produces) == 0 && !g.Canonicalizer() {
		return missing("produces", "a grader declares the metrics it produces")
	}
	seen := map[string]bool{}
	for i, p := range g.Produces {
		f := "produces[" + itoa(i) + "]"
		if !id.ValidDigest(p) && !IsBuiltinMetric(p) {
			return unpinned(f, "a metric is named by its digest or a built-in metric's reserved digest")
		}
		if seen[p] {
			return invalid(f, "a metric is produced twice")
		}
		seen[p] = true
	}
	parts := map[string]bool{
		GraderCheck: g.Check != nil, GraderModelJudge: g.ModelJudge != nil,
		GraderAgentJudge: g.AgentJudge != nil, GraderAnnotation: g.Annotation != nil,
	}
	for kind, set := range parts {
		if set != (kind == g.Kind) {
			if set {
				return invalid(kind, "a grader of kind "+g.Kind+" carries no "+kind+" object")
			}
			return missing(kind, "a grader of kind "+g.Kind+" carries its "+kind+" object")
		}
	}
	switch g.Kind {
	case GraderCheck:
		return g.Check.validate()
	case GraderModelJudge:
		return g.ModelJudge.validate(g)
	case GraderAgentJudge:
		return g.AgentJudge.validate()
	}
	return g.Annotation.validate()
}

func (c *Check) validate() error {
	if len(c.Command) == 0 || c.Command[0] == "" {
		return missing("check.command", "a check names the command it runs")
	}
	if c.Workdir != "" && !relative(c.Workdir) {
		return invalid("check.workdir", "a workdir is relative to the workspace and does not leave it")
	}
	for _, k := range sortedKeys(c.Env) {
		if strings.HasPrefix(k, "EXAMEN_") || k == "" {
			return invalid("check.env."+k, "a check's variable is not empty and does not begin EXAMEN_, which Examen sets")
		}
	}
	if c.Files != nil {
		if err := c.Files.validate("check.files"); err != nil {
			return err
		}
	}
	if c.Image != "" && !PinnedImage(c.Image) {
		return unpinned("check.image", "an image is pinned by its digest, as name@sha256:...")
	}
	return nil
}

func (j *ModelJudge) validate(g Grader) error {
	switch {
	case j.Model == "":
		return missing("model_judge.model", "a model judge names its model")
	case !PinnedModel(j.Model):
		return unpinned("model_judge.model", "a model is named by a pinned id, not an alias")
	case j.Preamble != "" && j.Preamble != JudgePreamble:
		return invalid("model_judge.preamble", "the preamble is Examen's, pinned at apply")
	case j.Temperature != nil && (math.IsNaN(*j.Temperature) || *j.Temperature < 0 || *j.Temperature > 2):
		return invalid("model_judge.temperature", "temperature is a number from 0 to 2")
	}
	if err := j.Inputs.validate("model_judge.inputs"); err != nil {
		return err
	}
	switch j.Role {
	case "", RoleGrade:
		if j.Prompt == "" && j.Rubric == "" {
			return missing("model_judge.prompt", "a model judge carries a prompt or a rubric")
		}
	case RoleCanonicalize:
		if len(g.Produces) != 1 {
			return invalid("produces", "a canonicalizer produces exactly one open-vocabulary metric")
		}
		if g.LevelOf() != LevelTrial {
			return invalid("level", "a canonicalizer has no level of its own")
		}
	default:
		return invalid("model_judge.role", "role is grade or canonicalize")
	}
	return nil
}

func (j *AgentJudge) validate() error {
	switch {
	case j.Agent == "":
		return missing("agent_judge.agent", "an agent judge names its agent version")
	case !pinnedAgent(j.Agent):
		return unpinned("agent_judge.agent", "an agent is named by its version digest")
	case j.Preamble != "":
		return invalid("agent_judge.preamble", "the preamble is Examen's, pinned at apply")
	case j.Instructions == "":
		return missing("agent_judge.instructions", "an agent judge carries its instructions")
	}
	return j.Inputs.validate("agent_judge.inputs")
}

func (a *Annotation) validate() error {
	if math.IsNaN(a.Sample) || a.Sample < 0 || a.Sample > 1 {
		return invalid("annotation.sample", "sample is a fraction from 0 to 1")
	}
	if a.Instructions == "" {
		return missing("annotation.instructions", "an annotation queue carries instructions for the person")
	}
	return nil
}

func (in JudgeInputs) validate(field string) error {
	for i, p := range append(slices.Clone(in.Files), in.Outputs...) {
		if !relative(p) {
			return invalid(field+"["+itoa(i)+"]", "a file or output a judge reads is a relative path that does not leave its root")
		}
	}
	return nil
}

// PinnedImage reports whether an image is pinned by its digest: a name and
// @sha256: with the hex, or a digest alone.
func PinnedImage(s string) bool {
	if id.ValidDigest(s) {
		return true
	}
	name, digest, ok := strings.Cut(s, "@")
	return ok && name != "" && id.ValidDigest(digest)
}

// PinnedModel reports whether a model id has the form of a pinned one: one
// token with no whitespace, and not an alias that moves, such as one that
// ends in latest.
func PinnedModel(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t\n") {
		return false
	}
	return !strings.HasSuffix(strings.ToLower(s), "latest")
}

// pinnedAgent reports whether an agent version is named by its digest.
func pinnedAgent(s string) bool {
	_, _, err := ParseRef("agent", s)
	return err == nil
}
