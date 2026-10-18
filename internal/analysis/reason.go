// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The numbers of a reason are written in spec 010's formats: a p-value and
// a corrected value with two significant digits, and below 0.001 as
// "< 0.001"; a share of weight or a level as a percentage, with one
// decimal when it is not whole; a count as an integer with thousands
// separators.

// pValue writes "p = 0.048" or "p < 0.001" for the name given.
func pValue(name string, v float64) string {
	if v < 0.001 {
		return name + " < 0.001"
	}
	return name + " = " + strconv.FormatFloat(v, 'g', 2, 64)
}

// percent writes a share as a percentage: 0.7 is "70%", 0.755 "75.5%".
func percent(v float64) string {
	return strconv.FormatFloat(math.Round(float64(v*1000))/10, 'f', -1, 64) + "%"
}

// count writes an integer with thousands separators: 10000 is "10,000".
func count(n int) string {
	s := strconv.Itoa(n)
	if n < 0 {
		return "-" + count(-n)
	}
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// trials writes "1 trial" or "3 trials".
func trials(n int) string {
	if n == 1 {
		return "1 trial"
	}
	return count(n) + " trials"
}

// days writes a length of time in days, with one decimal when it is not
// whole: "41 days", "7.5 days", "1 day".
func days(d time.Duration) string {
	v := math.Round(float64(d.Hours()/24*10)) / 10
	if v == 1 {
		return "1 day"
	}
	return strconv.FormatFloat(v, 'f', -1, 64) + " days"
}

// quantileName is how a sentence names a quantile aggregate.
func quantileName(a Aggregate) string {
	if a == AggP90 {
		return "a 90th percentile"
	}
	return "a median"
}

// The sentences of a summary or a delta without an interval (spec 008,
// Reasons).
const (
	noIntervalRepeatRate  = "no interval: resampling repeats trials, which inflates a repeat rate"
	noIntervalDescriptive = "descriptive: no interval"
	noIntervalCandidate   = "descriptive: one value per candidate"
	noIntervalNoValue     = "no interval: no trial has a value"
)

func noIntervalP90(min int) string {
	return "no interval below " + count(min) + " values for a 90th percentile"
}

func noIntervalRanks(n int, a Aggregate, level float64) string {
	return fmt.Sprintf("no interval: %s values cannot bound %s at %s", count(n), quantileName(a), percent(level))
}

func noIntervalQuantileDelta(need int, a Aggregate) string {
	return fmt.Sprintf("no interval: each side needs %s values for %s at this level", count(need), quantileName(a))
}

func noIntervalNoSpread(noSpread, b int) string {
	return fmt.Sprintf("no interval: %s of %s resamples had no spread", count(noSpread), count(b))
}

func noIntervalSmallTask(task string, n, min int) string {
	return fmt.Sprintf("no interval: task %s has %s values; a 90th percentile needs %s", task, count(n), count(min))
}

// The reasons of results without a test (spec 008, Reasons).
const (
	reasonCandidate      = "descriptive: one value per candidate, so nothing to test"
	reasonModeShare      = "descriptive: a mode share depends on the number of trials, so it is not tested"
	reasonDistinctCount  = "descriptive: a count of distinct labels depends on the number of trials, so it is not tested"
	reasonZeroDelta      = "no difference between the two sides"
	reasonLabelsNotBuilt = "descriptive: label tests are not built yet"
)

func reasonTaskBelow(task string, valued, need int) string {
	return fmt.Sprintf("task %s has %s with a value; %s needed", task, trials(valued), count(need))
}

func reasonCoverage(coverage, need float64, left []LeftOut) string {
	s := fmt.Sprintf("covers %s of the suite's weight; %s needed", percent(coverage), percent(need))
	if len(left) == 0 {
		return s
	}
	parts := make([]string, len(left))
	for i, l := range left {
		switch l.Why {
		case LeftOutUnpaired:
			parts[i] = fmt.Sprintf("%s (not run by the %s)", l.Task, l.unpairedBy)
		default:
			parts[i] = fmt.Sprintf("%s (%s)", l.Task, trials(min(l.Candidate, l.Reference)))
		}
	}
	return s + "; left out: " + strings.Join(parts, ", ")
}

func reasonPrimary(p float64) string {
	return pValue("p", p) + " for the primary metric, tested alone without correction"
}

func reasonFamily(p, q float64, m int, fdr float64) string {
	return fmt.Sprintf("%s; %s after correcting %s results at %s", pValue("p", p), pValue("q", q), count(m), percent(fdr))
}

func reasonExploratoryPrimary(p, q float64, m int, fdr float64) string {
	return reasonFamily(p, q, m, fdr) + "; exploratory, so the primary metric is corrected with the rest"
}

func reasonSelfReported(p float64) string {
	return pValue("p", p) + " on values the external harness reported itself: not a finding, and outside the corrected family"
}

func reasonPair(p, h float64, m int, level float64) string {
	return fmt.Sprintf("%s unadjusted; %s after Holm's adjustment over %s pairs at %s", pValue("p", p), pValue("h", h), count(m), percent(level))
}

// sizes writes the trials of a power-floor reason: "5 trials per side" or
// "5 and 7 trials".
func sizes(nx, ny int) string {
	if nx == ny {
		return trials(nx) + " per side"
	}
	return count(nx) + " and " + trials(ny)
}

// suiteSizes writes the tasks of a suite-level power-floor reason: "3 tasks
// of 2 trials per side", or "3 tasks of 2 to 4 trials per side".
func suiteSizes(tasks, lo, hi int) string {
	t := count(tasks) + " tasks"
	if tasks == 1 {
		t = "1 task"
	}
	if lo == hi {
		return t + " of " + trials(lo) + " per side"
	}
	return fmt.Sprintf("%s of %s to %s trials per side", t, count(lo), count(hi))
}

// reasonFloor is the reason of a result whose exact test cannot reach its
// threshold: among m results of a family, or tested alone at the level.
func reasonFloor(what string, m int, alone bool, level float64) string {
	if alone {
		return "with " + what + ", no difference can be shown at " + percent(level)
	}
	return "with " + what + ", no difference can be shown among " + count(m) + " results"
}

// reasonIndependence is the reason of an experiment's test of
// independence, tested alone.
func reasonIndependence(p float64) string {
	return pValue("p", p) + " for the experiment's declared test of independence, tested alone without correction"
}

// reasonNoCandidateDifference is the reason of a test of independence
// whose statistic is 0.
const reasonNoCandidateDifference = "no difference between the candidates"

// reasonIndependenceCoverage is the reason of a test of independence below
// MIN_SUITE_COVERAGE, naming each task left out.
func reasonIndependenceCoverage(coverage, need float64, left []IndependenceLeftOut) string {
	s := fmt.Sprintf("covers %s of the suite's weight; %s needed", percent(coverage), percent(need))
	if len(left) == 0 {
		return s
	}
	parts := make([]string, len(left))
	for i, l := range left {
		switch l.Why {
		case LeftOutUnpaired:
			parts[i] = l.Task + " (not run by every candidate)"
		default:
			parts[i] = fmt.Sprintf("%s (%s)", l.Task, trials(slices.Min(l.Counts)))
		}
	}
	return s + "; left out: " + strings.Join(parts, ", ")
}

// candidateSizes writes the tasks of a test of independence's power-floor
// reason: "4 tasks of 5 trials per candidate", or "4 tasks of 3 to 5
// trials per candidate".
func candidateSizes(tasks, lo, hi int) string {
	t := count(tasks) + " tasks"
	if tasks == 1 {
		t = "1 task"
	}
	if lo == hi {
		return t + " of " + trials(lo) + " per candidate"
	}
	return fmt.Sprintf("%s of %s to %s trials per candidate", t, count(lo), count(hi))
}

// independenceSuffix is what a test of independence's reason adds for its
// confounds: the trials without a value, the trials left out of analysis,
// and an uncalibrated judge.
func independenceSuffix(cs []IndependenceConfound) string {
	var out strings.Builder
	for _, c := range cs {
		n := 0
		for _, v := range c.Counts {
			n += v
		}
		switch c.Name {
		case confoundMissing:
			if n == 1 {
				out.WriteString("; 1 trial has no value")
			} else {
				out.WriteString("; " + count(n) + " trials have no value")
			}
		case confoundIncomplete:
			verb := "were"
			if n == 1 {
				verb = "was"
			}
			out.WriteString("; " + trials(n) + " " + verb + " left out: failed to run, canceled or over budget")
		case confoundUncalibrated:
			out.WriteString(suffixUncalibrated)
		}
	}
	return out.String()
}

// reasonFloorPairs is the power-floor reason of a benchmark's pair result.
func reasonFloorPairs(what string, m int) string {
	return "with " + what + ", no difference can be shown among " + count(m) + " pairs"
}

// sideCounts writes per-side counts of a reason's suffix: "2 of 20
// candidate trials" and, when the reference has some, "and 1 of 20
// reference trials".
func sideCounts(cx, nx, cy, ny int) (string, bool) {
	var parts []string
	if cx > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s candidate trials", count(cx), count(nx)))
	}
	if cy > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s reference trials", count(cy), count(ny)))
	}
	return strings.Join(parts, " and "), len(parts) == 1 && cx+cy == 1
}

func suffixMissing(cx, nx, cy, ny int) string {
	s, one := sideCounts(cx, nx, cy, ny)
	if one {
		return "; " + s + " has no value"
	}
	return "; " + s + " have no value"
}

func suffixIncomplete(cx, nx, cy, ny int) string {
	s, one := sideCounts(cx, nx, cy, ny)
	verb := "were"
	if one {
		verb = "was"
	}
	return "; " + s + " " + verb + " left out: failed to run, canceled or over budget"
}

func suffixWindow(gap time.Duration) string {
	return "; the two sides' time windows differ by " + days(gap)
}

const suffixUncalibrated = "; labels from a judge not yet calibrated against people"
