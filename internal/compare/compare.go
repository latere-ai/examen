// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package compare is the seam between what Examen keeps and the statistics
// of specs/008-analysis.md: the questions a comparison and a grid cell ask
// of the analysis, and where it reads what it needs. Every figure a grid or
// a comparison shows is the analysis's; nothing on this side computes an
// interval, a test or a verdict.
//
// Analysis answers them with internal/analysis. NotComputed answers every
// question with ErrNotComputed, which a comparison reads as its state
// computing with no results and a grid cell as no summary yet: the
// analyzer of a test that exercises what surrounds the analysis.
package compare

import (
	"context"
	"encoding/json/jsontext"
	"errors"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// ErrNotComputed is a question the analysis has not answered: the
// comparison stays computing with no results, and a grid cell carries no
// summary.
var ErrNotComputed = errors.New("compare: the analysis has computed no figure yet")

// Source is where the analysis reads a comparison's sides and a cell's
// trials and current values.
type Source struct {
	Experiments store.Experiments
	Versions    store.Versions
	Baselines   store.Baselines
	Taxonomies  store.Taxonomies
}

// Analyzer answers what the analysis computes.
type Analyzer interface {
	// Compare computes a comparison from what both its sides hold now,
	// and answers its computed members and the measurements it read.
	Compare(ctx context.Context, c objects.Comparison, src Source) (objects.Computed, []string, error)
	// Summaries are spec 002's summaries of one metric over each of an
	// experiment's cells' trials, by cell id, as the analysis renders them.
	Summaries(ctx context.Context, e objects.Experiment, cells []objects.Cell, metric string, src Source) (map[string]jsontext.Value, error)
	// Declared is an experiment's declared analysis, from the newest
	// comparison of each of its declared comparisons.
	Declared(ctx context.Context, e objects.Experiment, family []objects.Comparison, src Source) (objects.DeclaredAnalysis, error)
}

// NotComputed is an analyzer that computes nothing: every question answers
// ErrNotComputed.
type NotComputed struct{}

// Compare answers ErrNotComputed.
func (NotComputed) Compare(context.Context, objects.Comparison, Source) (objects.Computed, []string, error) {
	return objects.Computed{}, nil, ErrNotComputed
}

// Summaries answers ErrNotComputed.
func (NotComputed) Summaries(context.Context, objects.Experiment, []objects.Cell, string, Source) (map[string]jsontext.Value, error) {
	return nil, ErrNotComputed
}

// Declared answers ErrNotComputed.
func (NotComputed) Declared(context.Context, objects.Experiment, []objects.Comparison, Source) (objects.DeclaredAnalysis, error) {
	return objects.DeclaredAnalysis{}, ErrNotComputed
}
