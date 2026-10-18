// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package compare is the seam between what Examen keeps and the statistics
// of specs/008-analysis.md: the questions a comparison and a grid cell ask
// of the analysis, and where it reads what it needs. Every figure a grid or
// a comparison shows is the analysis's; nothing on this side computes an
// interval, a test or a verdict.
//
// NotComputed answers every question with ErrNotComputed, which a
// comparison reads as its state computing with no results and a grid cell
// as no summary yet. It stands in until the analysis is wired behind
// Analyzer.
package compare

import (
	"context"
	"encoding/json"
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
	// Summarize is spec 002's summary of one metric over one cell's
	// trials, as the analysis renders it.
	Summarize(ctx context.Context, cell objects.Cell, metric string, src Source) (json.RawMessage, error)
}

// NotComputed is the analyzer of a build without the analysis: every
// question answers ErrNotComputed.
type NotComputed struct{}

// Compare answers ErrNotComputed.
func (NotComputed) Compare(context.Context, objects.Comparison, Source) (objects.Computed, []string, error) {
	return objects.Computed{}, nil, ErrNotComputed
}

// Summarize answers ErrNotComputed.
func (NotComputed) Summarize(context.Context, objects.Cell, string, Source) (json.RawMessage, error) {
	return nil, ErrNotComputed
}
