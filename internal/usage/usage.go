// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package usage turns Lux's records of a trial's model calls into the
// trial's sent and observed layers and its cost (specs/005-execution.md,
// "Cost and the sent and observed layers"; specs/007-measurements-and-evidence.md).
// It is a pure function of the records, so a figure traces to the records
// it was read from.
package usage

import (
	"cmp"
	"slices"

	"latere.ai/x/examen/internal/objects"
)

// Currency is the one currency a cost is summed in: the Budgets' at the
// key source. A call priced in another is counted as unpriced.
const Currency = "USD"

// Tokens are a call's token counts as Lux records them: Input excludes the
// cached input, and Output includes Reasoning.
type Tokens struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CachedInput int64 `json:"cachedInput"`
	CacheWrite  int64 `json:"cacheWrite"`
	Reasoning   int64 `json:"reasoning"`
	Estimated   bool  `json:"estimated"`
}

// Call is one of Lux's records of a call, as the layers read it.
type Call struct {
	// SentModel is the model the request named; "" when it named none Lux
	// holds.
	SentModel string
	// SentReasoning is the reasoning level the request carried, "" when
	// the record carries none, which Lux's records do not yet.
	SentReasoning string
	// Served is the model that served it.
	Served string
	// Status is ok, refused or failed.
	Status   string
	Tokens   Tokens
	Cost     int64
	Currency string
	Priced   bool
}

// refused is a call Lux turned down before any provider served it: it
// cost nothing, and its price is not missing.
func (c Call) refused() bool { return c.Status == "refused" }

// unpriced is a call that may have cost something Lux could not price.
func (c Call) unpriced() bool { return !c.refused() && (!c.Priced || c.Currency != Currency) }

// Layers are what a trial's calls make of it: the sent layer, the observed
// layer's figures, and the cost in micro-USD.
type Layers struct {
	Sent objects.Sent
	// Served counts the calls each model served, sorted by model; a call
	// no model served is not counted.
	Served []objects.ModelCount
	// Input, Output and Reasoning sum the calls' tokens, input with its
	// cached part, as the built-in metrics count them.
	Input, Output, Reasoning int64
	// CostState is known when every call that may cost was priced,
	// unpriced when one was not, and unknown when there are no calls.
	CostState string
	CostMicro int64
}

// Of computes the layers of calls.
func Of(calls []Call) Layers {
	l := Layers{Sent: objects.Sent{BySetting: []objects.SentSetting{}}, CostState: objects.CostUnknown}
	if len(calls) == 0 {
		return l
	}
	l.CostState = objects.CostKnown
	settings := map[[2]string]int64{}
	served := map[string]int64{}
	for _, c := range calls {
		l.Sent.Requests++
		settings[[2]string{c.SentModel, c.SentReasoning}]++
		if c.Served != "" && !c.refused() {
			served[c.Served]++
		}
		l.Input += c.Tokens.Input + c.Tokens.CachedInput
		l.Output += c.Tokens.Output
		l.Reasoning += c.Tokens.Reasoning
		switch {
		case c.unpriced():
			l.CostState = objects.CostUnpriced
		case !c.refused():
			l.CostMicro += c.Cost
		}
	}
	for k, n := range settings {
		l.Sent.BySetting = append(l.Sent.BySetting, objects.SentSetting{Model: k[0], Reasoning: k[1], Requests: n})
	}
	slices.SortFunc(l.Sent.BySetting, func(a, b objects.SentSetting) int {
		return cmp.Or(cmp.Compare(a.Model, b.Model), cmp.Compare(a.Reasoning, b.Reasoning))
	})
	for m, n := range served {
		l.Served = append(l.Served, objects.ModelCount{Model: m, Requests: n})
	}
	slices.SortFunc(l.Served, func(a, b objects.ModelCount) int { return cmp.Compare(a.Model, b.Model) })
	return l
}

// Observe writes the layers' figures into a trial's observed layer: the
// models served, the tokens, the cost state, and the cost in USD only when
// it is known. A trial with no calls keeps the layer as it is, its cost
// unknown.
func (l Layers) Observe(o objects.Observed) objects.Observed {
	if l.Sent.Requests == 0 {
		return o
	}
	o.Served = l.Served
	in, out, reasoning := l.Input, l.Output, l.Reasoning
	o.InputTokens, o.OutputTokens, o.ReasoningTokens = &in, &out, &reasoning
	o.CostState, o.CostUSD = l.CostState, nil
	if l.CostState == objects.CostKnown {
		usd := float64(l.CostMicro) / 1e6
		o.CostUSD = &usd
	}
	return o
}

// Builtin is the built-in grader's values the layers add to a finished
// trial's: the cost when it is known, and the token counts.
func (l Layers) Builtin() map[string]any {
	out := map[string]any{}
	if l.Sent.Requests == 0 {
		return out
	}
	if l.CostState == objects.CostKnown {
		out[objects.BuiltinDigest(objects.MetricCostUSD)] = float64(l.CostMicro) / 1e6
	}
	out[objects.BuiltinDigest(objects.MetricInputTokens)] = float64(l.Input)
	out[objects.BuiltinDigest(objects.MetricOutputTokens)] = float64(l.Output)
	out[objects.BuiltinDigest(objects.MetricReasoningTokens)] = float64(l.Reasoning)
	return out
}
