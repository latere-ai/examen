// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json/jsontext"
	"slices"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

// The kinds of a candidate.
const (
	CandidateTopos    = "topos"
	CandidateProgram  = "program"
	CandidateExternal = "external"
)

// The executors, by the candidate kind each runs.
const (
	ExecutorTopos    = "topos"
	ExecutorCella    = "cella"
	ExecutorExternal = "external"
)

// ExecutorOf is the executor a candidate kind runs on.
func ExecutorOf(kind string) string {
	switch kind {
	case CandidateTopos:
		return ExecutorTopos
	case CandidateProgram:
		return ExecutorCella
	}
	return ExecutorExternal
}

// declaredMaxBytes bounds a string an external harness declares about
// itself.
const declaredMaxBytes = 255

// Candidate is one fully pinned system under test.
type Candidate struct {
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Topos    *Topos            `json:"topos,omitzero"`
	Program  *Program          `json:"program,omitzero"`
	External *External         `json:"external,omitzero"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Topos is a Topos agent version with a model, reasoning and tools
// override.
type Topos struct {
	Agent     string   `json:"agent"`
	Model     string   `json:"model"`
	Reasoning string   `json:"reasoning,omitempty"`
	Tools     []string `json:"tools,omitempty"`
}

// Program is a program in an image, pinned by digest.
type Program struct {
	Image       string            `json:"image"`
	Command     []string          `json:"command,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Config      jsontext.Value    `json:"config,omitzero"`
	Model       string            `json:"model,omitempty"`
	Reasoning   string            `json:"reasoning,omitempty"`
	Credentials []Credential      `json:"credentials,omitempty"`
	Lux         *ProgramLux       `json:"lux,omitzero"`
}

// Credential is an owner token for an audience, delivered as a Cella
// Secret scoped to a host.
type Credential struct {
	Audience string `json:"audience"`
	Host     string `json:"host"`
	Env      string `json:"env,omitempty"`
	File     string `json:"file,omitempty"`
}

// ProgramLux is the Lux surface a program speaks and where its Key is
// mounted.
type ProgramLux struct {
	Surface string `json:"surface,omitempty"`
	KeyEnv  string `json:"key_env,omitempty"`
	URLEnv  string `json:"url_env,omitempty"`
}

// External is a configuration of the owner's own harness, declared by the
// harness and never observed.
type External struct {
	Harness   string            `json:"harness"`
	Version   string            `json:"version"`
	Digest    string            `json:"digest,omitempty"`
	Model     string            `json:"model,omitempty"`
	Reasoning string            `json:"reasoning,omitempty"`
	Flags     map[string]string `json:"flags,omitempty"`
}

// Validate holds a candidate to the rules of specs/002-objects.md that need
// no other object. reserved are the audiences Examen acts through, which no
// program credential may name.
func (c Candidate) Validate(reserved []string) error {
	if !ValidName(KindCandidate, c.Name) {
		return invalid("name", "a name is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
	}
	parts := map[string]bool{CandidateTopos: c.Topos != nil, CandidateProgram: c.Program != nil, CandidateExternal: c.External != nil}
	if _, known := parts[c.Kind]; !known {
		return invalid("kind", "kind is topos, program or external")
	}
	for kind, set := range parts {
		if set != (kind == c.Kind) {
			if set {
				return invalid(kind, "a candidate of kind "+c.Kind+" carries no "+kind+" object")
			}
			return missing(kind, "a candidate of kind "+c.Kind+" carries its "+kind+" object")
		}
	}
	var err error
	switch c.Kind {
	case CandidateTopos:
		err = c.Topos.validate()
	case CandidateProgram:
		err = c.Program.validate(reserved)
	default:
		err = c.External.validate()
	}
	if err != nil {
		return err
	}
	return metadata("metadata", c.Metadata)
}

func (t *Topos) validate() error {
	switch {
	case t.Agent == "":
		return missing("topos.agent", "a topos candidate names its agent version")
	case !pinnedAgent(t.Agent):
		return unpinned("topos.agent", "an agent is named by its version digest")
	case t.Model == "":
		return missing("topos.model", "a topos candidate names its model")
	case !PinnedModel(t.Model):
		return unpinned("topos.model", "a model is named by a pinned id, not an alias")
	}
	return nil
}

func (p *Program) validate(reserved []string) error {
	switch {
	case p.Image == "":
		return missing("program.image", "a program candidate names its image")
	case !PinnedImage(p.Image):
		return unpinned("program.image", "an image is pinned by its digest, as name@sha256:...")
	case p.Model != "" && !PinnedModel(p.Model):
		return unpinned("program.model", "a model is named by a pinned id, not an alias")
	}
	for i, c := range p.Credentials {
		f := "program.credentials[" + itoa(i) + "]"
		switch {
		case c.Audience == "":
			return missing(f+".audience", "a credential names its audience")
		case slices.Contains(reserved, c.Audience):
			return apierr.OnField(apierr.CodeCredentialAudienceReserved, f+".audience", "the audience "+quote(c.Audience)+" is one Examen acts through")
		case c.Host == "":
			return missing(f+".host", "a credential names the host it is scoped to")
		case (c.Env == "") == (c.File == ""):
			return invalid(f, "a credential names exactly one of env and file")
		}
	}
	if p.Lux != nil && p.Lux.Surface != "" && !slices.Contains([]string{"anthropic_messages", "openai_responses", "openai_chat_completions", "gemini", "lux"}, p.Lux.Surface) {
		return invalid("program.lux.surface", "surface is anthropic_messages, openai_responses, openai_chat_completions, gemini or lux")
	}
	return nil
}

func (e *External) validate() error {
	switch {
	case !ValidHarness(e.Harness):
		return invalid("external.harness", "a harness is 1 to 63 characters of [a-z0-9._-], starting with a letter or a digit")
	case e.Version == "":
		return missing("external.version", "an external candidate declares its harness's version")
	case len(e.Version) > declaredMaxBytes || len(e.Digest) > declaredMaxBytes || len(e.Model) > declaredMaxBytes || len(e.Reasoning) > declaredMaxBytes:
		return invalid("external", "a declared value is at most 255 bytes")
	case len(e.Flags) > limits.MetadataMaxPairs:
		return apierr.Exceeds("METADATA_MAX_PAIRS", "external.flags", "flags hold more than METADATA_MAX_PAIRS (16) pairs")
	}
	if slices.Contains(sortedKeys(e.Flags), "") {
		return invalid("external.flags", "a flag's name is not empty")
	}
	return nil
}

// Model is the model a candidate names or declares, and Reasoning its
// reasoning level.
func (c Candidate) Model() string {
	switch {
	case c.Topos != nil:
		return c.Topos.Model
	case c.Program != nil:
		return c.Program.Model
	case c.External != nil:
		return c.External.Model
	}
	return ""
}

// Reasoning is the reasoning level a candidate names or declares.
func (c Candidate) Reasoning() string {
	switch {
	case c.Topos != nil:
		return c.Topos.Reasoning
	case c.Program != nil:
		return c.Program.Reasoning
	case c.External != nil:
		return c.External.Reasoning
	}
	return ""
}

// Audiences are the audiences a program candidate's credentials name.
func (c Candidate) Audiences() []string {
	if c.Program == nil {
		return nil
	}
	var out []string
	for _, cr := range c.Program.Credentials {
		out = append(out, cr.Audience)
	}
	return out
}
