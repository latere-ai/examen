// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package usage

import (
	"testing"

	"latere.ai/x/examen/internal/objects"
)

func call(model, served, status string, cost int64, priced bool) Call {
	return Call{SentModel: model, Served: served, Status: status, Cost: cost, Currency: Currency, Priced: priced,
		Tokens: Tokens{Input: 100, CachedInput: 20, Output: 50, Reasoning: 10}}
}

// TestTheLayersOfATrialsCalls: the sent layer counts every call by the
// model and reasoning it named, the served model counts the calls a model
// served, tokens sum with the cached input, and the cost sums the priced
// calls, unpriced when one call that may cost was not priced, a refused
// call costing nothing and counting toward no model served.
func TestTheLayersOfATrialsCalls(t *testing.T) {
	l := Of([]Call{
		call("anthropic/claude-haiku-5.5", "claude-haiku-5-5", "ok", 1000, true),
		call("anthropic/claude-haiku-5.5", "claude-haiku-5-5", "failed", 20, true),
		call("openai/gpt-5-mini", "gpt-5-mini", "ok", 300, true),
		call("openai/gpt-5-mini", "", "refused", 0, false),
	})
	if l.Sent.Requests != 4 || len(l.Sent.BySetting) != 2 || l.Sent.BySetting[0] != (objects.SentSetting{Model: "anthropic/claude-haiku-5.5", Requests: 2}) ||
		l.Sent.BySetting[1] != (objects.SentSetting{Model: "openai/gpt-5-mini", Requests: 2}) {
		t.Errorf("sent %+v", l.Sent)
	}
	if len(l.Served) != 2 || l.Served[0] != (objects.ModelCount{Model: "claude-haiku-5-5", Requests: 2}) || l.Served[1] != (objects.ModelCount{Model: "gpt-5-mini", Requests: 1}) {
		t.Errorf("served %+v", l.Served)
	}
	if l.CostState != objects.CostKnown || l.CostMicro != 1320 || l.Input != 480 || l.Output != 200 || l.Reasoning != 40 {
		t.Errorf("figures %+v", l)
	}
	o := l.Observe(objects.Observed{CostState: objects.CostUnknown, LuxRequests: []string{"req_1"}})
	if o.CostState != objects.CostKnown || o.CostUSD == nil || *o.CostUSD != 0.00132 || *o.InputTokens != 480 || len(o.LuxRequests) != 1 {
		t.Errorf("observed %+v", o)
	}
	b := l.Builtin()
	if b[objects.BuiltinDigest(objects.MetricCostUSD)] != 0.00132 || b[objects.BuiltinDigest(objects.MetricOutputTokens)] != float64(200) {
		t.Errorf("built-in %v", b)
	}

	unpriced := Of([]Call{call("m", "m", "ok", 10, true), call("n", "n", "ok", 0, false)})
	if unpriced.CostState != objects.CostUnpriced {
		t.Errorf("a call not priced: %s", unpriced.CostState)
	}
	o = unpriced.Observe(objects.Observed{CostState: objects.CostKnown, CostUSD: new(float64)})
	if o.CostState != objects.CostUnpriced || o.CostUSD != nil {
		t.Errorf("an unpriced trial's observed layer %+v", o)
	}
	if _, ok := unpriced.Builtin()[objects.BuiltinDigest(objects.MetricCostUSD)]; ok {
		t.Error("an unpriced trial has a built-in cost")
	}
	other := call("m", "m", "ok", 10, true)
	other.Currency = "EUR"
	if Of([]Call{other}).CostState != objects.CostUnpriced {
		t.Error("a call priced in another currency is summed")
	}

	none := Of(nil)
	if none.CostState != objects.CostUnknown || none.Sent.Requests != 0 || len(none.Builtin()) != 0 {
		t.Errorf("no calls %+v", none)
	}
	kept := objects.Observed{CostState: objects.CostUnknown}
	if got := none.Observe(kept); got.CostState != objects.CostUnknown || got.Served != nil {
		t.Errorf("no calls changed the observed layer: %+v", got)
	}
}
