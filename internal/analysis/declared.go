// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

// DeclaredPrimary is one declared comparison's primary result as its
// newest comparison stores it, which the declared family reads.
type DeclaredPrimary struct {
	// Comparison is the id of the declared comparison's newest record;
	// empty when none is computed yet.
	Comparison string
	Metric     string
	Direction  Direction
	// P is the result's p-value; nil when it has none, being
	// insufficient, descriptive or not computed.
	P     *float64
	Delta *Delta
	// Verdict is insufficient for an insufficient result, and empty
	// otherwise: a declared primary result carries no other verdict.
	Verdict Verdict
	// SelfReported marks a result that reads self-reported values, which
	// keeps its p and is no finding.
	SelfReported bool
	// Reason is the comparison's own reason for the result.
	Reason string
	Final  bool
}

// DeclaredFamily is the correction of an experiment's declared
// comparisons' primary results together (spec 008): its method, its size,
// every declared comparison, and its level.
type DeclaredFamily struct {
	Method      string          `json:"method"`
	Size        int             `json:"size"`
	Level       float64         `json:"level"`
	Final       bool            `json:"final"`
	Comparisons []DeclaredEntry `json:"comparisons"`
}

// DeclaredEntry is one declared comparison in its family.
type DeclaredEntry struct {
	Index      int      `json:"index"`
	Comparison string   `json:"comparison,omitempty"`
	Metric     string   `json:"metric"`
	P          *float64 `json:"p,omitempty"`
	Q          *float64 `json:"q,omitempty"`
	Verdict    Verdict  `json:"verdict,omitempty"`
	Reason     string   `json:"reason"`
	Final      bool     `json:"final"`
}

// CorrectDeclared corrects the primary results of an experiment's declared
// comparisons, given in the experiment's order, together by Benjamini and
// Hochberg's procedure at the level fdr. The family's size is the number
// of declared comparisons, every one of which counts: a result without a
// usable p-value, being insufficient, descriptive, self-reported or not
// computed, counts as p = 1, so it is never a finding and never lets the
// correction grant another result more than the declared number allows.
// A result with a usable p-value gets its q and the verdict spec 008's
// rule gives from it.
func CorrectDeclared(primaries []DeclaredPrimary, fdr float64) DeclaredFamily {
	out := DeclaredFamily{Method: "benjamini_hochberg", Size: len(primaries), Level: fdr, Final: true, Comparisons: make([]DeclaredEntry, len(primaries))}
	ps := make([]float64, len(primaries))
	usable := make([]bool, len(primaries))
	for i, p := range primaries {
		ps[i] = 1
		if p.P != nil && !p.SelfReported && p.Verdict != Insufficient {
			ps[i], usable[i] = *p.P, true
		}
	}
	qs := benjaminiHochberg(ps)
	for i, p := range primaries {
		e := DeclaredEntry{Index: i, Comparison: p.Comparison, Metric: p.Metric, P: p.P, Verdict: p.Verdict, Reason: p.Reason, Final: p.Final}
		switch {
		case p.Comparison == "":
			e.Reason = reasonNotComputed
		case usable[i]:
			e.Q = ptr(qs[i])
			e.Verdict = verdict(p.Direction, qs[i], fdr, p.Delta)
			e.Reason = reasonDeclaredFamily(*p.P, qs[i], len(primaries), fdr)
		}
		out.Final = out.Final && e.Final
		out.Comparisons[i] = e
	}
	return out
}
