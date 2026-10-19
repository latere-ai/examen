// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package limits holds every limit of specs/002-objects.md, each defined
// once here and referred to by name everywhere else. The name of each
// constant is the spec's, in Go's spelling: DEFAULT_TRIALS is
// DefaultTrials. A test reads the spec's table and holds every constant
// to its row, so a change to a limit is a change to the spec first.
package limits

import (
	"math"
	"time"
)

// Trials, cells and the shape of an experiment.
const (
	DefaultTrials                 = 10
	MaxTrialsPerCell              = 1000
	SmokeTrials                   = 1
	MinTrialsForVerdict           = 5
	MinTrialsPerTaskInSuite       = 2
	MinSuiteCoverage              = 0.8
	MinTrialsForP90Interval       = 29
	MaxInfraRetries               = 2
	MaxMachineWaits               = 8
	MaxCellsPerExperiment         = 2000
	MaxTasksPerSuite              = 1000
	MaxMetricsPerSuite            = 64
	MaxCandidatesPerExperiment    = 32
	MaxDeclaredComparisons        = 128
	MaxActiveExperimentsPerOwner  = 16
	DefaultParallelism            = 8
	MaxParallelism                = 256
	CostEstimateMargin            = 1.5
	MaxOutputFiles                = 10000
	MaxOutputBytes                = 1 << 30
	OutputFileKeepMaxBytes        = 16 << 20
	MaxInlineInputBytes           = 64 << 10
	MetadataMaxPairs              = 16
	MaxTaskFactors                = 16
	LabelMaxBytes                 = 128
	MaxLabelsPerMetric            = 64
	MaxTaxonomyRawLabels          = 10000
	MaxRulesPerBar                = 128
	MaxComparisonsPerReport       = 64
	MaxTrendPoints                = 100
	NarrativeMaxClaims            = 40
	NarrativeMaxBytes             = 32 << 10
	PublicationLineMaxBytes       = 200
	PublicationTextMaxBytes       = 16 << 10
	PublicationEvidenceMaxBytes   = 10 << 30
	ExportRecordsPerFile          = 1000
	MaxExportRecords              = 2000000
	MaxExportConditions           = 16
	MaxExportExperiments          = 100
	CheckMaxOutputBytes           = 64 << 10
	JudgeMaxInputBytes            = 512 << 10
	JudgeMaxOutputTokens          = 4096
	JudgeMaxAttempts              = 2
	JudgeMaxRationaleBytes        = 4 << 10
	CalibrationMinPairs           = 30
	CalibrationMinAgreement       = 0.67
	ImageMaxSize                  = 40 << 20
	DeliveryRetryWindow           = 24 * time.Hour
	IdempotencyWindow             = 24 * time.Hour
	IdempotencyKeyMaxBytes        = 255
	SignInMaxBytes                = 255
	IdempotencyReservation        = 5 * time.Minute
	RequestDeadline               = time.Minute
	MaxStreamsPerSubject          = 10
	APIRequestsPerMinute          = 600
	PublicRequestsPerMinute       = 120
	DefaultPageSize               = 50
	MaxPageSize                   = 200
	MaxRequestBytes               = 4 << 20
	DefaultTrialTimeout           = 30 * time.Minute
	MaxTrialTimeout               = 24 * time.Hour
	CheckTimeout                  = 10 * time.Minute
	TrialGrace                    = 10 * time.Minute
	TrialStartTimeout             = time.Hour
	ExternalLease                 = 10 * time.Minute
	MachineWait                   = 30 * time.Second
	MachineWaitMax                = 5 * time.Minute
	ReportInterval                = 15 * time.Second
	UsageSettleDelay              = 2 * time.Minute
	UsageEvery                    = time.Minute
	UsageLookback                 = 30 * time.Minute
	AdoptionMaxAge                = 30 * 24 * time.Hour
	AgentJudgeTimeout             = 30 * time.Minute
	AnnotationClaimTTL            = 30 * time.Minute
	DeliveryTimeout               = 10 * time.Second
	DeliveryBackoffMin            = time.Second
	DeliveryBackoffMax            = 5 * time.Minute
	DeliveryLease                 = 30 * time.Second
	SignatureMaxAge               = 5 * time.Minute
	StreamPollInterval            = time.Second
	StreamKeepalive               = 15 * time.Second
	OwnerTokenMaxLifetime         = 15 * time.Minute
	OwnerTokenRenewal             = 2 * time.Minute
	BudgetStatusTTL               = 15 * time.Second
	SecretScanTimeout             = 30 * time.Minute
	ShutdownGrace                 = 25 * time.Second
	FalseDiscoveryRate            = 0.05
	ConfidenceLevel               = 0.95
	BootstrapResamples            = 10000
	PermutationExactMax           = 200000
	PermutationStopExceedances    = 200
	MaxLabelsOpenVocabulary       = 4096
	MaxBenchmarkEntries           = 32
	WindowTolerance               = 7 * 24 * time.Hour
	InputLinkTTL                  = time.Hour
	BoschlooGridPoints            = 1000
	TieTolerance                  = 1e-9
	permutationResamplesFloor     = 10000
	permutationResamplesPerPValue = 20
)

// PermutationResamples is PERMUTATION_RESAMPLES: the relabelings of a
// Monte Carlo permutation test whose family holds m p-values and is held
// at the level alpha, the larger of 10,000 and ceil(20 · m / alpha). A
// comparison's family is held at FalseDiscoveryRate, and its primary
// metric's suite-level result is a family of one; a benchmark's results
// table is held at 1 - ConfidenceLevel with m its number of pairs.
func PermutationResamples(m int, alpha float64) int {
	x := permutationResamplesPerPValue * float64(max(m, 1)) / alpha
	// The quotient is a whole number whenever the level divides it, and the
	// float division may land a hair above it; within TieTolerance of a
	// whole number it is that number.
	n := int(math.Ceil(x - x*TieTolerance))
	return max(permutationResamplesFloor, n)
}
