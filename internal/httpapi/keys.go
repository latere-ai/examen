// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"latere.ai/x/examen/internal/apierr"
	keysrc "latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
)

// The external Key of an experiment (specs/005-execution.md, "Attribution
// and cost"): the one Key the key source keeps for an experiment whose
// runs a person's own setup does, which Examen asks for at the create and
// hands on with every lease of the experiment's runs, beside the request
// label each run's calls carry, so the experiment's Budget binds them and
// Lux's records of them are the run's cost. A setup that calls models with
// a Key of its own may: its runs' cost then reads unknown.

// keyCacheTTL is how long a replica answers leases from the Key the key
// source last answered, so a burst of leases asks it once.
const keyCacheTTL = 30 * time.Second

// LabelHeader is the request header a run's model calls carry their label
// in, and trialLabel the label's value for one trial.
const LabelHeader = "Lux-Labels"

func trialLabel(trial string) string { return keysrc.LabelTrial + "=" + trial }

// keySource is the server's key source and the caches over it.
type keySource struct {
	once    sync.Once
	cache   *keysrc.Cache
	mu      sync.Mutex
	budgets map[string]cachedBudget
}

type cachedBudget struct {
	b  keysrc.Budget
	at time.Time
}

func (s *Server) keyCache() *keysrc.Cache {
	s.keyState.once.Do(func() {
		s.keyState.cache = &keysrc.Cache{Source: s.Keys, TTL: keyCacheTTL}
		s.keyState.budgets = map[string]cachedBudget{}
	})
	return s.keyState.cache
}

// handsKey reports whether an experiment's runs are handed a Key: a key
// source is configured, Examen executes the experiment, and a candidate of
// it is one a person's own setup runs.
func (s *Server) handsKey(e objects.Experiment, kinds []string) bool {
	return s.Keys != nil && e.Mode == objects.ModeExecute && slices.Contains(kinds, objects.CandidateExternal)
}

// externalKey asks the key source for the experiment's external Key on
// behalf of subject, and records its id, never its value.
func (s *Server) externalKey(ctx context.Context, e objects.Experiment, subject string) (keysrc.Lease, error) {
	l, err := s.keyCache().External(ctx, keysrc.Request{
		Owner: e.Owner, Subject: subject, Experiment: e.ID, Purpose: keysrc.PurposeExternal,
		Labels: map[string]string{keysrc.LabelExperiment: e.ID},
	})
	if err != nil {
		return keysrc.Lease{}, err
	}
	if err := s.Store.Usage().Hold(ctx, e.ID, e.Owner, l.ID); err != nil {
		return keysrc.Lease{}, apierr.Internal(err)
	}
	return l, nil
}

// keyError is a key source's failure as a request's refusal (specs/004-identity-and-authorization.md,
// "The key endpoint"). Nothing of the endpoint or of a Key is in it.
func keyError(err error) error {
	var refused *keysrc.RefusedError
	switch {
	case errors.As(err, &refused):
		return &apierr.Error{Code: apierr.CodeForbidden, Reason: refused.Reason, Detail: "the key source refused the experiment's key: " + refused.Reason}
	case errors.Is(err, keysrc.ErrBudgetExhausted):
		return &apierr.Error{Code: apierr.CodeBudgetRefused, Field: "budget", Detail: "no budget the experiment's key would draw on has money left"}
	case errors.Is(err, keysrc.ErrUnavailable), errors.Is(err, keysrc.ErrNoKey):
		return &apierr.Error{Code: apierr.CodeOwnerTokensUnavailable, Reason: "key_source", Detail: "the key source did not answer the experiment's key"}
	}
	var ae *apierr.Error
	if errors.As(err, &ae) {
		return ae
	}
	return apierr.Internal(err)
}

// budgetFigures fills an experiment's Budget's figures from the key
// source, reused for BUDGET_STATUS_TTL. Any failure leaves them out.
func (s *Server) budgetFigures(ctx context.Context, e *objects.Experiment, subject string) {
	if s.Keys == nil || e.Budget.Name == "" {
		return
	}
	s.keyCache()
	now := time.Now()
	s.keyState.mu.Lock()
	held, ok := s.keyState.budgets[e.ID]
	s.keyState.mu.Unlock()
	if !ok || now.Sub(held.at) >= limits.BudgetStatusTTL {
		b, err := s.Keys.Budget(ctx, keysrc.Request{Owner: e.Owner, Subject: subject, Experiment: e.ID, Budget: e.Budget.Name})
		if err != nil {
			if !errors.Is(err, keysrc.ErrNoKey) {
				s.log().WarnContext(ctx, "the key source gave no budget figures; the answer carries none", "experiment", e.ID, "error", err.Error())
			}
			return
		}
		held = cachedBudget{b: b, at: now}
		s.keyState.mu.Lock()
		s.keyState.budgets[e.ID] = held
		s.keyState.mu.Unlock()
	}
	if held.b.Currency != "USD" {
		e.Budget.Currency = held.b.Currency
		return
	}
	capUSD, spent, remaining := held.b.Cap, held.b.Spent, held.b.Remaining
	e.Budget.CapUSDMicro, e.Budget.SpentUSDMicro, e.Budget.RemainingUSDMicro = &capUSD, &spent, &remaining
}

// figures fills what an experiment's answer computes when it is read: its
// Budget's figures and its usage.
func (s *Server) figures(ctx context.Context, e *objects.Experiment, subject string) error {
	s.budgetFigures(ctx, e, subject)
	u, err := s.Store.Usage().Summary(ctx, e.ID)
	if err != nil {
		return apierr.Internal(err)
	}
	e.Usage = u
	return nil
}
