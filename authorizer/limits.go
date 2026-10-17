// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package authorizer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"latere.ai/x/pkg/authz"

	"latere.ai/x/examen/internal/limits"
)

// The executors an experiment's candidates run on, as Limits.Executors and
// the executors field name them.
const (
	ExecutorTopos    = "topos"
	ExecutorCella    = "cella"
	ExecutorExternal = "external"
)

// Executors lists every executor.
func Executors() []string { return []string{ExecutorTopos, ExecutorCella, ExecutorExternal} }

// AttributionMaxBytes is the longest attribution an allow may carry,
// PUBLICATION_LINE_MAX_BYTES of specs/002-objects.md.
const AttributionMaxBytes = limits.PublicationLineMaxBytes

// Limits are what an allow holds a request to: how an operator's plane
// tells Examen what an owner is held to. Examen enforces what it is told
// and stores no plan. The JSON form has every member optional and exactly
// these keys. An allow narrows the limits of specs/002-objects.md and never
// widens them.
type Limits struct {
	// Owner is whose a created object is; empty is the owner the request
	// named, else the caller's subject.
	Owner string `json:"owner,omitempty"`
	// MaxParallelism is the highest parallelism an experiment may set; 0
	// leaves MAX_PARALLELISM.
	MaxParallelism int `json:"max_parallelism,omitempty"`
	// MaxTrials is cells × trials one experiment may declare; 0 is no cap
	// beyond spec 002's.
	MaxTrials int `json:"max_trials,omitempty"`
	// MaxActiveExperiments is how many of the owner's experiments may be
	// neither complete nor canceled; 0 leaves
	// MAX_ACTIVE_EXPERIMENTS_PER_OWNER.
	MaxActiveExperiments int `json:"max_active_experiments,omitempty"`
	// Executors are the executors an experiment may use; empty is every
	// configured one.
	Executors []string `json:"executors,omitempty"`
	// JudgeModels are globs over the model ids a model judge may call;
	// empty is all.
	JudgeModels []string `json:"judge_models,omitempty"`
	// CredentialAudiences are the audiences a program candidate may be
	// given owner tokens for; empty is none.
	CredentialAudiences []string `json:"credential_audiences,omitempty"`
	// Budget is the Lux Budget the experiment is metered against, assigned
	// or created by the authorizer; empty leaves the one the request named
	// or the cap it gave.
	Budget string `json:"budget,omitempty"`
	// Attribution is the name a publication version shows for its owner;
	// empty leaves the one the request gave.
	Attribution string `json:"attribution,omitempty"`
}

// ErrUnreadableLimits is an allow whose limits cannot be held. A ceiling
// Examen cannot read is not one it can hold, so the answer is no decision
// and the request fails closed.
var ErrUnreadableLimits = errors.New("authorizer: the allow's limits cannot be read")

// wireNames are the keys of the limits object.
var wireNames = []string{
	"owner", "max_parallelism", "max_trials", "max_active_experiments", "executors",
	"judge_models", "credential_audiences", "budget", "attribution",
}

// DecodeLimits reads the limits of a decision. A decision with none is the
// zero Limits. An object that does not parse, a key this version does not
// know, a figure below zero, an empty glob, an empty audience, an
// attribution that is empty or longer than AttributionMaxBytes, and an
// executor that is not topos, cella or external are each
// ErrUnreadableLimits.
func DecodeLimits(d authz.Decision) (Limits, error) {
	var l Limits
	raw := bytes.TrimSpace(d.Limits)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return l, nil
	}
	// The keys are read first and compared by their exact spelling: the
	// decoder below matches a member's name without regard to case, and a
	// key spelled another way is one this version does not know.
	var members map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&members); err != nil {
		return Limits{}, fmt.Errorf("%w: %w", ErrUnreadableLimits, err)
	}
	if dec.More() {
		return Limits{}, fmt.Errorf("%w: more than one object", ErrUnreadableLimits)
	}
	for key := range members {
		if !slices.Contains(wireNames, key) {
			return Limits{}, fmt.Errorf("%w: the key %q is not one this version knows", ErrUnreadableLimits, key)
		}
	}
	if err := json.Unmarshal(raw, &l); err != nil {
		return Limits{}, fmt.Errorf("%w: %w", ErrUnreadableLimits, err)
	}
	// A member written null is one the answer does not set.
	attribution, hasAttribution := members["attribution"]
	hasAttribution = hasAttribution && !bytes.Equal(bytes.TrimSpace(attribution), []byte("null"))
	switch {
	case l.MaxParallelism < 0:
		return Limits{}, fmt.Errorf("%w: max_parallelism is below zero", ErrUnreadableLimits)
	case l.MaxTrials < 0:
		return Limits{}, fmt.Errorf("%w: max_trials is below zero", ErrUnreadableLimits)
	case l.MaxActiveExperiments < 0:
		return Limits{}, fmt.Errorf("%w: max_active_experiments is below zero", ErrUnreadableLimits)
	case slices.Contains(l.JudgeModels, ""):
		return Limits{}, fmt.Errorf("%w: judge_models has an empty glob", ErrUnreadableLimits)
	case slices.Contains(l.CredentialAudiences, ""):
		return Limits{}, fmt.Errorf("%w: credential_audiences has an empty audience", ErrUnreadableLimits)
	case hasAttribution && l.Attribution == "":
		return Limits{}, fmt.Errorf("%w: attribution is empty", ErrUnreadableLimits)
	case len(l.Attribution) > AttributionMaxBytes:
		return Limits{}, fmt.Errorf("%w: attribution is longer than %d bytes", ErrUnreadableLimits, AttributionMaxBytes)
	}
	for _, e := range l.Executors {
		if !slices.Contains(Executors(), e) {
			return Limits{}, fmt.Errorf("%w: executors has %q, which is not topos, cella or external", ErrUnreadableLimits, e)
		}
	}
	return l, nil
}
