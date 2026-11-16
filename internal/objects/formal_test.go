// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"flag"
	"slices"
	"testing"

	"latere.ai/x/pkg/statemachine/statemachinetest"
)

var update = flag.Bool("update", false, "rewrite the formal models under formal/")

// TestFormalModels holds formal/, which the lean and tla gates check, to the
// lifecycle tables.
func TestFormalModels(t *testing.T) {
	statemachinetest.Models(t, "../../formal", *update,
		ExperimentTable.Graph(), CellTable.Graph(), RolloutTable.Graph(), TrialTable.Graph(),
		GradingTable.Graph(), ComparisonTable.Graph(), UsageStatusTable.Graph(),
		ExperimentKeyTable.Graph(), JudgeKeyTable.Graph())
}

// TestEveryStatusIsInItsTable keeps each status type and its table to one
// set of states, so a status added to one is added to the other.
func TestEveryStatusIsInItsTable(t *testing.T) {
	check(t, "Experiment", ExperimentTable.States(), ExperimentPending, ExperimentRunning, ExperimentGrading, ExperimentComplete, ExperimentCanceled)
	check(t, "Cell", CellTable.States(), CellPending, CellSmoking, CellRunning, CellGrading, CellComplete, CellBlocked, CellOverBudget, CellCanceled)
	check(t, "Rollout", RolloutTable.States(), RolloutQueued, RolloutSubmitted, RolloutEnded)
	// TrialChecking is allowed by the column and made by no writer.
	check(t, "Trial", TrialTable.States(), TrialPending, TrialRunning, TrialDone)
	check(t, "Grading", GradingTable.States(), GradingPending, GradingOK, GradingMalformed, GradingError, GradingNotRunnable,
		GradingTimedOut, GradingNotRun, GradingRefused, GradingOverBudget, GradingGatewayRefused, GradingEvidenceChanged)
	check(t, "Comparison", ComparisonTable.States(), ComparisonComputing, ComparisonComplete, ComparisonFailed)
	check(t, "UsageStatus", UsageStatusTable.States(), UsageOK, UsageRefused, UsageFailed)
	check(t, "ExperimentKey", ExperimentKeyTable.States(), KeyHeld, KeyReleased)
	check(t, "JudgeKey", JudgeKeyTable.States(), KeyHeld, KeyReleased)
}

func check[S ~string](t *testing.T, name string, states []S, consts ...S) {
	t.Helper()
	want := slices.Sorted(slices.Values(consts))
	if !slices.Equal(states, want) {
		t.Errorf("%s table states %v, constants %v", name, states, want)
	}
}

func TestParsers(t *testing.T) {
	if s, ok := ParseUsageStatus("refused"); !ok || s != UsageRefused {
		t.Errorf(`ParseUsageStatus("refused") = %q, %v`, s, ok)
	}
	if _, ok := ParseUsageStatus("refuse"); ok {
		t.Error(`ParseUsageStatus("refuse") reports a status`)
	}
	if s, ok := ParseCellState("over_budget"); !ok || s != CellOverBudget {
		t.Errorf(`ParseCellState("over_budget") = %q, %v`, s, ok)
	}
	if _, ok := ParseCellState("done"); ok {
		t.Error(`ParseCellState("done") reports a cell state`)
	}
}
