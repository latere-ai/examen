// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package compare

import (
	"errors"
	"testing"

	"latere.ai/x/examen/internal/objects"
)

// Until the analysis is wired, every question answers ErrNotComputed and
// no figure.
func TestNotComputed(t *testing.T) {
	var a Analyzer = NotComputed{}
	computed, inputs, err := a.Compare(t.Context(), objects.Comparison{}, Source{})
	if !errors.Is(err, ErrNotComputed) || computed.Results != nil || inputs != nil {
		t.Fatalf("compare %v %v %v", computed, inputs, err)
	}
	summary, err := a.Summarize(t.Context(), objects.Cell{}, "sha256:x", Source{})
	if !errors.Is(err, ErrNotComputed) || summary != nil {
		t.Fatalf("summarize %s %v", summary, err)
	}
}
