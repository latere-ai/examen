// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package limits

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// constants maps every row of the table of specs/002-objects.md to the
// constant that holds it.
var constants = map[string]any{
	"DEFAULT_TRIALS":                   DefaultTrials,
	"MAX_TRIALS_PER_CELL":              MaxTrialsPerCell,
	"SMOKE_TRIALS":                     SmokeTrials,
	"MIN_TRIALS_FOR_VERDICT":           MinTrialsForVerdict,
	"MIN_TRIALS_PER_TASK_IN_SUITE":     MinTrialsPerTaskInSuite,
	"MIN_SUITE_COVERAGE":               MinSuiteCoverage,
	"MIN_TRIALS_FOR_P90_INTERVAL":      MinTrialsForP90Interval,
	"MAX_INFRA_RETRIES":                MaxInfraRetries,
	"MAX_CELLS_PER_EXPERIMENT":         MaxCellsPerExperiment,
	"MAX_TASKS_PER_SUITE":              MaxTasksPerSuite,
	"MAX_METRICS_PER_SUITE":            MaxMetricsPerSuite,
	"MAX_CANDIDATES_PER_EXPERIMENT":    MaxCandidatesPerExperiment,
	"MAX_DECLARED_COMPARISONS":         MaxDeclaredComparisons,
	"MAX_TASK_FACTORS":                 MaxTaskFactors,
	"MAX_ACTIVE_EXPERIMENTS_PER_OWNER": MaxActiveExperimentsPerOwner,
	"DEFAULT_PARALLELISM":              DefaultParallelism,
	"MAX_PARALLELISM":                  MaxParallelism,
	"DEFAULT_TRIAL_TIMEOUT":            DefaultTrialTimeout,
	"MAX_TRIAL_TIMEOUT":                MaxTrialTimeout,
	"CHECK_TIMEOUT":                    CheckTimeout,
	"TRIAL_GRACE":                      TrialGrace,
	"TRIAL_START_TIMEOUT":              TrialStartTimeout,
	"EXTERNAL_LEASE":                   ExternalLease,
	"REPORT_INTERVAL":                  ReportInterval,
	"USAGE_SETTLE_DELAY":               UsageSettleDelay,
	"USAGE_EVERY":                      UsageEvery,
	"USAGE_LOOKBACK":                   UsageLookback,
	"COST_ESTIMATE_MARGIN":             CostEstimateMargin,
	"ADOPTION_MAX_AGE":                 AdoptionMaxAge,
	"CHECK_MAX_OUTPUT_BYTES":           CheckMaxOutputBytes,
	"JUDGE_MAX_INPUT_BYTES":            JudgeMaxInputBytes,
	"JUDGE_MAX_OUTPUT_TOKENS":          JudgeMaxOutputTokens,
	"JUDGE_MAX_ATTEMPTS":               JudgeMaxAttempts,
	"JUDGE_MAX_RATIONALE_BYTES":        JudgeMaxRationaleBytes,
	"AGENT_JUDGE_TIMEOUT":              AgentJudgeTimeout,
	"ANNOTATION_CLAIM_TTL":             AnnotationClaimTTL,
	"CALIBRATION_MIN_PAIRS":            CalibrationMinPairs,
	"CALIBRATION_MIN_AGREEMENT":        CalibrationMinAgreement,
	"EXPORT_RECORDS_PER_FILE":          ExportRecordsPerFile,
	"MAX_EXPORT_RECORDS":               MaxExportRecords,
	"MAX_EXPORT_CONDITIONS":            MaxExportConditions,
	"MAX_EXPORT_EXPERIMENTS":           MaxExportExperiments,
	"FALSE_DISCOVERY_RATE":             FalseDiscoveryRate,
	"CONFIDENCE_LEVEL":                 ConfidenceLevel,
	"BOOTSTRAP_RESAMPLES":              BootstrapResamples,
	"PERMUTATION_RESAMPLES":            PermutationResamples,
	"PERMUTATION_EXACT_MAX":            PermutationExactMax,
	"PERMUTATION_STOP_EXCEEDANCES":     PermutationStopExceedances,
	"WINDOW_TOLERANCE":                 WindowTolerance,
	"MAX_LABELS_OPEN_VOCABULARY":       MaxLabelsOpenVocabulary,
	"MAX_BENCHMARK_ENTRIES":            MaxBenchmarkEntries,
	"INPUT_LINK_TTL":                   InputLinkTTL,
	"BOSCHLOO_GRID_POINTS":             BoschlooGridPoints,
	"TIE_TOLERANCE":                    TieTolerance,
	"MAX_TREND_POINTS":                 MaxTrendPoints,
	"MAX_RULES_PER_BAR":                MaxRulesPerBar,
	"DELIVERY_TIMEOUT":                 DeliveryTimeout,
	"DELIVERY_BACKOFF_MIN":             DeliveryBackoffMin,
	"DELIVERY_BACKOFF_MAX":             DeliveryBackoffMax,
	"DELIVERY_RETRY_WINDOW":            DeliveryRetryWindow,
	"DELIVERY_LEASE":                   DeliveryLease,
	"SIGNATURE_MAX_AGE":                SignatureMaxAge,
	"MAX_COMPARISONS_PER_REPORT":       MaxComparisonsPerReport,
	"NARRATIVE_MAX_CLAIMS":             NarrativeMaxClaims,
	"NARRATIVE_MAX_BYTES":              NarrativeMaxBytes,
	"DEFAULT_PAGE_SIZE":                DefaultPageSize,
	"MAX_PAGE_SIZE":                    MaxPageSize,
	"MAX_REQUEST_BYTES":                MaxRequestBytes,
	"API_REQUESTS_PER_MINUTE":          APIRequestsPerMinute,
	"IDEMPOTENCY_WINDOW":               IdempotencyWindow,
	"IDEMPOTENCY_KEY_MAX_BYTES":        IdempotencyKeyMaxBytes,
	"SIGN_IN_MAX_BYTES":                SignInMaxBytes,
	"IDEMPOTENCY_RESERVATION":          IdempotencyReservation,
	"REQUEST_DEADLINE":                 RequestDeadline,
	"STREAM_POLL_INTERVAL":             StreamPollInterval,
	"STREAM_KEEPALIVE":                 StreamKeepalive,
	"MAX_STREAMS_PER_SUBJECT":          MaxStreamsPerSubject,
	"OWNER_TOKEN_MAX_LIFETIME":         OwnerTokenMaxLifetime,
	"OWNER_TOKEN_RENEWAL":              OwnerTokenRenewal,
	"METADATA_MAX_PAIRS":               MetadataMaxPairs,
	"MAX_INLINE_INPUT_BYTES":           MaxInlineInputBytes,
	"MAX_OUTPUT_FILES":                 MaxOutputFiles,
	"MAX_OUTPUT_BYTES":                 MaxOutputBytes,
	"OUTPUT_FILE_KEEP_MAX_BYTES":       OutputFileKeepMaxBytes,
	"LABEL_MAX_BYTES":                  LabelMaxBytes,
	"MAX_LABELS_PER_METRIC":            MaxLabelsPerMetric,
	"MAX_TAXONOMY_RAW_LABELS":          MaxTaxonomyRawLabels,
	"BUDGET_STATUS_TTL":                BudgetStatusTTL,
	"PUBLIC_REQUESTS_PER_MINUTE":       PublicRequestsPerMinute,
	"PUBLICATION_LINE_MAX_BYTES":       PublicationLineMaxBytes,
	"PUBLICATION_TEXT_MAX_BYTES":       PublicationTextMaxBytes,
	"PUBLICATION_EVIDENCE_MAX_BYTES":   PublicationEvidenceMaxBytes,
	"SECRET_SCAN_TIMEOUT":              SecretScanTimeout,
	"SHUTDOWN_GRACE":                   ShutdownGrace,
	"IMAGE_MAX_SIZE":                   ImageMaxSize,
}

// rows reads the table of specs/002-objects.md: each constant's name and
// the text of its value.
func rows(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "specs", "002-objects.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	row := regexp.MustCompile("^\\| `([A-Z0-9_]+)` \\| ([^|]+) \\|")
	out := map[string]string{}
	in := false
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := scan.Text()
		if strings.HasPrefix(line, "## ") {
			in = line == "## Limits"
			continue
		}
		if m := row.FindStringSubmatch(line); in && m != nil {
			out[m[1]] = strings.TrimSpace(m[2])
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// units are the words the table writes a quantity in.
var units = map[string]float64{
	"second": float64(time.Second), "seconds": float64(time.Second),
	"minute": float64(time.Minute), "minutes": float64(time.Minute),
	"hour": float64(time.Hour), "hours": float64(time.Hour),
	"day": float64(24 * time.Hour), "days": float64(24 * time.Hour),
	"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30,
}

// quantity reads a value of the table: a number with optional thousands
// separators, and an optional unit.
func quantity(text string) (float64, error) {
	number, unit, _ := strings.Cut(text, " ")
	v, err := strconv.ParseFloat(strings.ReplaceAll(number, ",", ""), 64)
	if err != nil {
		return 0, err
	}
	if unit == "" {
		return v, nil
	}
	scale, ok := units[unit]
	if !ok {
		return 0, fmt.Errorf("the unit %q is not one the test reads", unit)
	}
	return v * scale, nil
}

func numeric(v any) float64 {
	switch x := v.(type) {
	case int:
		return float64(x)
	case float64:
		return x
	case time.Duration:
		return float64(x)
	}
	panic(fmt.Sprintf("a constant of type %T", v))
}

// Every row of the table has its constant with the row's value, and every
// constant has its row.
func TestEveryLimitIsTheSpecs(t *testing.T) {
	table := rows(t)
	if len(table) < 80 {
		t.Fatalf("read %d rows of the table; the reader is wrong", len(table))
	}
	for name, text := range table {
		c, ok := constants[name]
		if !ok {
			t.Errorf("%s is in the table of spec 002 and has no constant", name)
			continue
		}
		if _, isFunc := c.(func(int, float64) int); isFunc {
			// The formula's figures are held by TestPermutationResamples.
			if !strings.HasPrefix(text, "the larger of 10,000 and ceil(20") {
				t.Errorf("%s reads %q, which the formula does not hold", name, text)
			}
			continue
		}
		want, err := quantity(text)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got := numeric(c); math.Abs(got-want) > math.Abs(want)*1e-12 {
			t.Errorf("%s is %v and the table says %s", name, got, text)
		}
	}
	for name := range constants {
		if _, ok := table[name]; !ok {
			t.Errorf("%s has a constant and no row in the table of spec 002", name)
		}
	}
}

// PERMUTATION_RESAMPLES is the larger of 10,000 and ceil(20 · m / the
// family's level): a comparison's at the false discovery rate, a results
// table's at 1 - CONFIDENCE_LEVEL.
func TestPermutationResamples(t *testing.T) {
	for _, c := range []struct {
		m     int
		alpha float64
		want  int
	}{
		{0, FalseDiscoveryRate, 10000}, {1, FalseDiscoveryRate, 10000},
		{25, FalseDiscoveryRate, 10000}, {26, FalseDiscoveryRate, 10400},
		{40, FalseDiscoveryRate, 16000}, {100, FalseDiscoveryRate, 40000},
		{101, FalseDiscoveryRate, 40400}, {500, FalseDiscoveryRate, 200000},
		{3, 1 - ConfidenceLevel, 10000}, {496, 1 - ConfidenceLevel, 198400},
		{26, 0.01, 52000},
	} {
		if got := PermutationResamples(c.m, c.alpha); got != c.want {
			t.Errorf("PermutationResamples(%d, %v) = %d, want %d", c.m, c.alpha, got, c.want)
		}
	}
}
