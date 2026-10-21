// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package work is the unattended work of an experiment
// (specs/005-execution.md): submitting each queued rollout once
// experiment.run allows it, asking it again before a cell's external
// trials are handed out, returning trials whose lease lapsed to pending,
// and computing the comparisons whose inputs changed. Every step is
// idempotent, so the request that made the work, the ticker of any
// replica, or both, may take it.
package work

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/compare"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// batch bounds what one tick reads of each kind of work.
const batch = 200

// ReasonRunDenied is the parked reason of a cell whose run the authorizer
// denied.
const ReasonRunDenied = "run_denied"

// Engine takes an experiment's unattended work.
type Engine struct {
	Store    store.Store
	Authz    access.Authorizer
	Analyzer compare.Analyzer
	// Keys is the key source an ended experiment's Key is released at and
	// its Key's records are read from. Nil releases and reads nothing.
	Keys keys.Source
	// Topos runs the trials of topos candidates; nil runs none.
	Topos *Topos
	// Grading runs the checks and model judges of finished trials; nil
	// runs none.
	Grading *Grading
	Log     *slog.Logger
	// Now is the clock of the usage reads; time.Now when nil.
	Now func() time.Time
}

func (e *Engine) log() *slog.Logger {
	if e.Log != nil {
		return e.Log
	}
	return slog.Default()
}

// run asks experiment.run for one step of a cell's work on its executor,
// as the experiment's created_by with no claims: true for an allow, false
// for a deny, and access.ErrUnavailable when no decision came.
func (e *Engine) run(ctx context.Context, exp, owner, createdBy, state, budget, cell, executor string) (bool, error) {
	res := access.Resource(authorizer.ActionExperimentRun, exp, map[string]any{
		authorizer.FieldOwner: owner, authorizer.FieldCreatedBy: createdBy, authorizer.FieldState: state,
		authorizer.FieldPurpose: authorizer.PurposeTrial, authorizer.FieldCell: cell,
		authorizer.FieldExecutor: executor, authorizer.FieldBudget: budget,
	})
	d, err := e.Authz.Authorize(ctx, access.Unattended(createdBy), access.Question{Action: authorizer.ActionExperimentRun, Resource: res})
	if err != nil {
		return false, err
	}
	return d.Allow, nil
}

// Advance submits the queued rollouts of one experiment, or of every
// experiment when it names none: each once experiment.run allows it. A
// deny parks the cell canceled with run_denied; an outage leaves the
// rollout queued for the next tick.
func (e *Engine) Advance(ctx context.Context, experiment string) error {
	queued, err := e.Store.Experiments().Queued(ctx, experiment, batch)
	if err != nil {
		return err
	}
	var errs []error
	for _, q := range queued {
		allow, err := e.run(ctx, q.Experiment, q.Owner, q.CreatedBy, q.State, q.Budget, q.Cell, q.Executor)
		switch {
		case errors.Is(err, access.ErrUnavailable):
			e.log().WarnContext(ctx, "the authorizer gave no decision on a rollout; it waits", "experiment", q.Experiment, "cell", q.Cell, "error", apierr.LogText(err))
			continue
		case err != nil:
			errs = append(errs, err)
			continue
		case !allow:
			errs = append(errs, e.Store.Experiments().Park(ctx, q.Cell, objects.StateCanceled, ReasonRunDenied))
			continue
		}
		errs = append(errs, e.Store.Experiments().SubmitRollout(ctx, q.Cell, q.Key))
	}
	return errors.Join(errs...)
}

// Allowed asks experiment.run for each cell before its external trials
// are handed out, and answers the cells it allows. A denied cell parks
// canceled with run_denied; a cell the authorizer gave no decision on is
// left out, and its trials wait.
func (e *Engine) Allowed(ctx context.Context, exp objects.Experiment, cells []objects.Cell) ([]string, error) {
	return e.allowed(ctx, exp, cells, objects.ExecutorExternal)
}

// Compute computes the comparisons whose inputs changed. A comparison the
// analysis has not computed stays computing; one it failed on is failed.
func (e *Engine) Compute(ctx context.Context) error {
	x := e.Store.Experiments()
	dirty, err := x.Dirty(ctx, batch)
	if err != nil {
		return err
	}
	src := compare.Source{Experiments: x, Versions: e.Store.Versions(), Baselines: e.Store.Baselines(), Taxonomies: e.Store.Taxonomies()}
	var errs []error
	var declared []string
	for _, c := range dirty {
		computed, inputs, err := e.Analyzer.Compare(ctx, c, src)
		switch {
		case errors.Is(err, compare.ErrNotComputed):
			errs = append(errs, x.Clean(ctx, c.ID, c.UpdatedAt))
			continue
		case err != nil:
			e.log().ErrorContext(ctx, "a comparison failed", "comparison", c.ID, "error", apierr.LogText(err))
			errs = append(errs, x.Computed(ctx, c.ID, objects.ComparisonFailed, objects.Computed{}, nil))
		default:
			errs = append(errs, x.Computed(ctx, c.ID, objects.ComparisonComplete, computed, inputs))
		}
		if c.DeclaredIndex != nil && !slices.Contains(declared, c.Experiment) {
			declared = append(declared, c.Experiment)
		}
	}
	// A test of independence reads every cell, so an experiment that
	// declares one is marked whenever a trial of it reaches done or it
	// ends, whatever comparisons it keeps.
	marked, err := x.DeclaredDirty(ctx, batch)
	if err != nil {
		errs = append(errs, err)
	}
	for _, eid := range marked {
		if !slices.Contains(declared, eid) {
			declared = append(declared, eid)
		}
	}
	// A declared comparison's primary result is corrected with every
	// other declared comparison's, so each experiment whose declared
	// comparisons changed, or whose test of independence is marked, has
	// its declared analysis recomputed, under its row lock, from what they
	// store and its cells hold now.
	for _, eid := range declared {
		err := x.Declare(ctx, eid, func(exp objects.Experiment, family []objects.Comparison) (objects.DeclaredAnalysis, error) {
			return e.Analyzer.Declared(ctx, exp, family, src)
		})
		if !errors.Is(err, compare.ErrNotComputed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Tick takes one round of every kind of work: lapsed leases back to
// pending, queued rollouts submitted, the sessions of topos trials
// started, followed and stopped, the records of the experiments' Keys read
// and their trials' cost settled, comparisons computed, and the Keys of
// ended experiments released, their judges' among them. Grading runs in
// its own loop (GradeLoop).
func (e *Engine) Tick(ctx context.Context) error {
	_, err := e.Store.Experiments().ExpireLeases(ctx)
	return errors.Join(err, e.Advance(ctx, ""), e.RunTopos(ctx), e.Usage(ctx), e.Compute(ctx), e.Release(ctx, ""), e.ReleaseJudgeKeys(ctx))
}

// Loop ticks every interval until ctx ends, logging a round that failed;
// the next tick takes what it left.
func (e *Engine) Loop(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := e.Tick(ctx); err != nil && ctx.Err() == nil {
				e.log().WarnContext(ctx, "a round of unattended work failed; the next tick takes what it left", "error", apierr.LogText(err))
			}
		}
	}
}
