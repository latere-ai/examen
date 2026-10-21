// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
)

// The kinds of a task's workspace.
const (
	WorkspaceNone  = "none"
	WorkspaceOrigo = "origo"
	WorkspaceArca  = "arca"
)

// Task is one versioned unit of work.
type Task struct {
	Name        string       `json:"name"`
	Instruction string       `json:"instruction,omitempty"`
	Inputs      []FileSource `json:"inputs,omitempty"`
	Reference   []FileSource `json:"reference,omitempty"`
	Outputs     []string     `json:"outputs,omitempty"`
	Workspace   *Workspace   `json:"workspace,omitempty"`
	Environment *Environment `json:"environment,omitempty"`
	Checks      []string     `json:"checks,omitempty"`
	// Graders are judges and annotation queues applied to this task's
	// trials alone, beside the suite's graders: a judge whose prompt is
	// the task's own, such as one that reads what a run did with the
	// task's seed project.
	Graders  []string `json:"graders,omitempty"`
	Timeout  string   `json:"timeout,omitempty"`
	MaxTurns int      `json:"max_turns,omitempty"`
	Evidence []string `json:"evidence,omitempty"`
	// Factors place the task in a design: each factor's name to the
	// task's level of it. A declared comparison or a test of independence
	// selects and stratifies cells by them.
	Factors  map[string]string `json:"factors,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// FileSource is one named input or reference of a task, in one of three
// forms: an Arca path and its digest, an Origo path at a full commit, or
// inline text.
type FileSource struct {
	Name  string    `json:"name"`
	Arca  *ArcaRef  `json:"arca,omitempty"`
	Origo *OrigoRef `json:"origo,omitempty"`
	Text  *string   `json:"text,omitempty"`
}

// ArcaRef is a path in an owner's Arca space and the digest of what it
// holds.
type ArcaRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// OrigoRef is a path of an Origo repository at a full commit id.
type OrigoRef struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	Path       string `json:"path"`
}

// Workspace is where a trial starts.
type Workspace struct {
	Kind       string `json:"kind"`
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Path       string `json:"path,omitempty"`
	Digest     string `json:"digest,omitempty"`
}

// Environment is the Cella sandbox template a trial runs in.
type Environment struct {
	Image     string          `json:"image"`
	Resources json.RawMessage `json:"resources,omitempty"`
	Network   json.RawMessage `json:"network,omitempty"`
}

var (
	fileName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
	commitID = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)
)

// TimeoutOf is the task's wall time: its own, else DEFAULT_TRIAL_TIMEOUT.
func (t Task) TimeoutOf() time.Duration {
	if d, err := time.ParseDuration(t.Timeout); err == nil && t.Timeout != "" {
		return d
	}
	return limits.DefaultTrialTimeout
}

// Normalize keeps every check and grader reference as its digest.
func (t *Task) Normalize() error {
	for i, c := range t.Checks {
		_, d, err := ParseRef("checks["+itoa(i)+"]", c)
		if err != nil {
			return err
		}
		t.Checks[i] = d
	}
	for i, g := range t.Graders {
		_, d, err := ParseRef("graders["+itoa(i)+"]", g)
		if err != nil {
			return err
		}
		t.Graders[i] = d
	}
	return nil
}

// Validate holds a task to the rules of specs/002-objects.md that need no
// other object. Every pin is checked in form; that it exists is checked
// where an experiment uses it.
func (t Task) Validate() error {
	if !ValidName(KindTask, t.Name) {
		return invalid("name", "a name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
	}
	if err := files("inputs", t.Inputs); err != nil {
		return err
	}
	if err := files("reference", t.Reference); err != nil {
		return err
	}
	seen := map[string]bool{}
	for i, o := range t.Outputs {
		f := "outputs[" + itoa(i) + "]"
		if !relative(o) {
			return invalid(f, "an output is a path relative to /examen/outputs/ with no ..")
		}
		if seen[o] {
			return invalid(f, "an output is named twice")
		}
		seen[o] = true
	}
	for i, e := range t.Evidence {
		if !relative(e) {
			return invalid("evidence["+itoa(i)+"]", "an evidence path is relative to the workspace with no ..")
		}
	}
	if t.Workspace != nil {
		if err := t.Workspace.validate(); err != nil {
			return err
		}
	}
	if t.Environment != nil && !PinnedImage(t.Environment.Image) {
		return unpinned("environment.image", "an environment's image is pinned by its digest, as name@sha256:...")
	}
	for i, c := range t.Checks {
		if !id.ValidDigest(c) {
			return unpinned("checks["+itoa(i)+"]", "a check is named by its grader digest")
		}
	}
	named := map[string]bool{}
	for i, g := range t.Graders {
		f := "graders[" + itoa(i) + "]"
		switch {
		case !id.ValidDigest(g):
			return unpinned(f, "a grader is named by its digest")
		case named[g]:
			return invalid(f, "a grader is named twice")
		}
		named[g] = true
	}
	if t.Timeout != "" {
		d, err := time.ParseDuration(t.Timeout)
		switch {
		case err != nil || d <= 0:
			return invalid("timeout", "timeout is a duration above zero in Go's notation, such as 45m")
		case d > limits.MaxTrialTimeout:
			return apierr.Exceeds("MAX_TRIAL_TIMEOUT", "timeout", "timeout is longer than MAX_TRIAL_TIMEOUT (24h)")
		}
	}
	if t.MaxTurns < 0 {
		return invalid("max_turns", "max_turns is a whole number above zero")
	}
	if err := factors(t.Factors); err != nil {
		return err
	}
	return metadata("metadata", t.Metadata)
}

// factors checks a task's factors: at most MAX_TASK_FACTORS, each name and
// level under the name rule, and no name a cell variable Examen defines
// itself.
func factors(f map[string]string) error {
	if len(f) > limits.MaxTaskFactors {
		return apierr.Exceeds("MAX_TASK_FACTORS", "factors", "more factors than MAX_TASK_FACTORS ("+itoa(limits.MaxTaskFactors)+")")
	}
	for _, name := range slices.Sorted(maps.Keys(f)) {
		switch {
		case !fileName.MatchString(name):
			return invalid("factors", "a factor's name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
		case slices.Contains(ReservedVariables, name):
			return invalid("factors."+name, "a factor is not named for a cell variable Examen defines itself: "+strings.Join(ReservedVariables, ", "))
		case !fileName.MatchString(f[name]):
			return invalid("factors."+name, "a factor's level is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
		}
	}
	return nil
}

// files checks a list of inputs or references.
func files(field string, list []FileSource) error {
	seen := map[string]bool{}
	for i, f := range list {
		at := field + "[" + itoa(i) + "]"
		if !fileName.MatchString(f.Name) {
			return invalid(at+".name", "a name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
		}
		if seen[f.Name] {
			return invalid(at+".name", "two of "+field+" have one name")
		}
		seen[f.Name] = true
		forms := 0
		for _, set := range []bool{f.Arca != nil, f.Origo != nil, f.Text != nil} {
			if set {
				forms++
			}
		}
		if forms != 1 {
			return invalid(at, "an entry is exactly one of arca, origo and text")
		}
		switch {
		case f.Arca != nil:
			if err := f.Arca.validate(at + ".arca"); err != nil {
				return err
			}
		case f.Origo != nil:
			if err := f.Origo.validate(at + ".origo"); err != nil {
				return err
			}
		case len(*f.Text) > limits.MaxInlineInputBytes:
			return apierr.Exceeds("MAX_INLINE_INPUT_BYTES", at+".text", "inline text is longer than MAX_INLINE_INPUT_BYTES (64 KiB)")
		}
	}
	return nil
}

func (a *ArcaRef) validate(field string) error {
	if !relative(a.Path) {
		return invalid(field+".path", "an Arca path is relative to the owner's space with no ..")
	}
	if !id.ValidDigest(a.Digest) {
		return unpinned(field+".digest", "an Arca path is pinned by the digest of its content")
	}
	return nil
}

func (o *OrigoRef) validate(field string) error {
	switch {
	case o.Repository == "":
		return missing(field+".repository", "an Origo source names its repository")
	case !commitID.MatchString(o.Commit):
		return unpinned(field+".commit", "an Origo source is pinned by a full commit id, never a branch")
	case !relative(o.Path):
		return invalid(field+".path", "an Origo path is relative with no ..")
	}
	return nil
}

func (w *Workspace) validate() error {
	switch w.Kind {
	case WorkspaceNone:
		if w.Repository != "" || w.Commit != "" || w.Path != "" || w.Digest != "" {
			return invalid("workspace", "a workspace of kind none names nothing else")
		}
	case WorkspaceOrigo:
		switch {
		case w.Repository == "":
			return missing("workspace.repository", "an origo workspace names its repository")
		case !commitID.MatchString(w.Commit):
			return unpinned("workspace.commit", "an origo workspace is pinned by a full commit id, never a branch")
		case w.Path != "" || w.Digest != "":
			return invalid("workspace", "an origo workspace names a repository and a commit alone")
		}
	case WorkspaceArca:
		if w.Repository != "" || w.Commit != "" {
			return invalid("workspace", "an arca workspace names a path and a digest alone")
		}
		return (&ArcaRef{Path: w.Path, Digest: w.Digest}).validate("workspace")
	default:
		return invalid("workspace.kind", "a workspace is none, origo or arca")
	}
	return nil
}

// Ref is the workspace as the provenance tuple holds it.
func (w *Workspace) Ref() string {
	switch {
	case w == nil || w.Kind == WorkspaceNone:
		return WorkspaceNone
	case w.Kind == WorkspaceOrigo:
		return "origo:" + w.Repository + "@" + w.Commit
	}
	return "arca:" + w.Path + "@" + w.Digest
}

// OutputDirectory reports whether a task's output names a directory.
func OutputDirectory(o string) bool { return strings.HasSuffix(o, "/") }

// HasOutput reports whether the task names an output.
func (t Task) HasOutput(o string) bool { return slices.Contains(t.Outputs, o) }
