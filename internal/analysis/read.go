// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"cmp"
	"encoding/json"
	"slices"
	"strings"
)

// Why a trial of a cell is not counted in a summary's n (spec 002).
const (
	excludedRetried     = "retried"
	excludedInfraFailed = "infra_failed"
	excludedOverBudget  = "over_budget"
	excludedCanceled    = "canceled"
	excludedNotDone     = "not_done"
)

// cellTrials is a cell's trials sorted for analysis: the finished ones in
// ascending order of id, and the counts of the others by why they are not
// read.
type cellTrials struct {
	finished []*Trial
	excluded map[string]int
	// incomplete holds the trials excluded from analysis that no finished
	// retry replaced: done, of an excluded outcome, and not retried.
	incomplete []*Trial
}

func sortTrials(c *Cell) cellTrials {
	out := cellTrials{excluded: map[string]int{}}
	for i := range c.Trials {
		t := &c.Trials[i]
		switch {
		case t.Retried:
			out.excluded[excludedRetried]++
		case t.Outcome == "":
			out.excluded[excludedNotDone]++
		case t.Outcome.excluded():
			out.excluded[string(t.Outcome)]++
			out.incomplete = append(out.incomplete, t)
		default:
			out.finished = append(out.finished, t)
		}
	}
	slices.SortFunc(out.finished, func(a, b *Trial) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(out.incomplete, func(a, b *Trial) int { return cmp.Compare(a.ID, b.ID) })
	if len(out.excluded) == 0 {
		out.excluded = nil
	}
	return out
}

// applies reports whether a trial-level metric applies to a trial of the
// outcome: every finished outcome when its applies_to is empty.
func applies(m Metric, o Outcome) bool {
	return len(m.AppliesTo) == 0 || slices.Contains(m.AppliesTo, o)
}

// value is one trial's read value of a metric: the measurement read, its
// value as the analysis computes with it, and for a label its label.
type value struct {
	trial *Trial
	m     *Measurement
	num   float64 // a number, or 1 and 0 for true and false
	label string
	// unmapped marks an open-vocabulary raw label the taxonomy record does
	// not map, which stands as a label of its own.
	unmapped bool
}

// sideRead is one cell's reading of one trial-level metric.
type sideRead struct {
	cell          *Cell
	trials        cellTrials
	grader        string // the grader the side is read at
	n             int    // finished trials the metric applies to
	notApplicable int
	values        []value // the valued trials, in trial id order
}

// valued is the number of trials with a current value.
func (r *sideRead) valued() int { return len(r.values) }

// missing is n less the valued trials.
func (r *sideRead) missing() int { return r.n - len(r.values) }

// selfReported counts the values read from self-reported measurements.
func (r *sideRead) selfReported() int {
	c := 0
	for _, v := range r.values {
		if v.m.SelfReported {
			c++
		}
	}
	return c
}

// window is the time window of the valued trials.
func (r *sideRead) window() window {
	var w window
	for _, v := range r.values {
		w.add(v.trial.StartedAt, v.trial.EndedAt)
	}
	return w
}

// nums are the values as numbers, in trial id order.
func (r *sideRead) nums() []float64 {
	out := make([]float64, len(r.values))
	for i, v := range r.values {
		out[i] = v.num
	}
	return out
}

// current returns a trial's current measurement of a metric at a grader:
// the latest generation by that grader; with no grader, for an external
// candidate, the latest self-reported value.
func current(t *Trial, metric, grader string, external bool) *Measurement {
	var best *Measurement
	for i := range t.Measurements {
		m := &t.Measurements[i]
		if m.Metric != metric {
			continue
		}
		switch {
		case grader != "" && m.Grader != grader:
			continue
		case grader == "" && (!external || !m.SelfReported):
			continue
		}
		if best == nil || m.Generation > best.Generation {
			best = m
		}
	}
	return best
}

// hasGrading reports whether every finished trial the metric applies to
// holds a measurement of it by the grader.
func hasGrading(ct cellTrials, m Metric, grader string) bool {
	for _, t := range ct.finished {
		if applies(m, t.Outcome) && current(t, m.Digest, grader, false) == nil {
			return false
		}
	}
	return true
}

// toFloat reads a measurement's value as a number.
func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

// readMetric reads a cell's values of a trial-level metric at a grader:
// for each finished trial in id order the metric applies to, its current
// value, converted by the metric's type; an open-vocabulary label is read
// as the canonical label the taxonomy maps it to, or as itself, marked,
// when the record does not map it.
func readMetric(c *Cell, ct cellTrials, m Metric, grader string, tax *Taxonomy) *sideRead {
	r := &sideRead{cell: c, trials: ct, grader: grader}
	external := c.Tuple.CandidateKind == "external"
	for _, t := range ct.finished {
		if !applies(m, t.Outcome) {
			r.notApplicable++
			continue
		}
		r.n++
		ms := current(t, m.Digest, grader, external)
		if ms == nil {
			continue
		}
		v := value{trial: t, m: ms}
		ok := true
		switch m.Type {
		case TypeBinary:
			switch b := ms.Value.(type) {
			case bool:
				if b {
					v.num = 1
				}
			default:
				f, isNum := toFloat(b)
				ok = isNum
				if f != 0 {
					v.num = 1
				}
			}
		case TypeLabel:
			s, isString := ms.Value.(string)
			ok = isString
			v.label = s
			if isString && m.Vocabulary == Open {
				if canon, mapped := taxMap(tax, s); mapped {
					v.label = canon
				} else {
					v.unmapped = true
				}
			}
		default:
			v.num, ok = toFloat(ms.Value)
		}
		if ok {
			r.values = append(r.values, v)
		}
	}
	return r
}

// taxMap is the canonical label a record maps a raw label to.
func taxMap(tax *Taxonomy, raw string) (string, bool) {
	if tax == nil {
		return "", false
	}
	c, ok := tax.Map[raw]
	return c, ok
}

// normalizeLabel folds a label as spec 006 normalizes one before it is
// compared: trimmed, each run of white space one space, lower case. Values
// are stored normalized, Unicode NFC included; this reads a metric's
// exclude_labels the same way.
func normalizeLabel(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// excludeLabels is a cell-level metric's exclude_labels, normalized:
// declined and none when the metric names none.
func excludeLabels(m Metric) map[string]bool {
	labels := m.ExcludeLabels
	if labels == nil {
		labels = []string{"declined", "none"}
	}
	out := map[string]bool{}
	for _, l := range labels {
		out[normalizeLabel(l)] = true
	}
	return out
}

// labelOrder is the order a label metric's results list its labels in: a
// closed vocabulary's declared labels, or the record's canonical labels,
// then any other label read, the unmapped raw labels among them, bytewise.
func labelOrder(m Metric, tax *Taxonomy, reads ...[]value) []string {
	var base []string
	if m.Vocabulary == Open {
		if tax != nil {
			base = tax.Canonical
		}
	} else {
		base = m.Labels
	}
	known := map[string]bool{}
	for _, l := range base {
		known[l] = true
	}
	var extra []string
	for _, vs := range reads {
		for _, v := range vs {
			if !known[v.label] {
				known[v.label] = true
				extra = append(extra, v.label)
			}
		}
	}
	slices.Sort(extra)
	return slices.Concat(base, extra)
}
