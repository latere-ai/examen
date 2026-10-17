---
title: "Analysis: per-cell summaries by metric type, the noise floor, exact and permutation tests, suite-level summaries with task weights, the multiple-comparison correction, verdicts, computed confounds, comparisons and trends, all computed deterministically and stored"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/006-grading.md
  - specs/007-measurements-and-evidence.md
affects: [internal/analysis/, internal/store/, api/]
effort: large
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Analysis

## Overview

The analysis turns measurements into the statements Examen makes: what
a cell's trials amount to, how sure that is, and whether a candidate
changed a metric against a reference, per task and over the suite. It
fixes every summary, interval and test by name and formula, the
correction that keeps many metrics and tasks from producing chance
findings, the verdict rule, how confounds are computed from the
provenance tuple and the provenance layers, and how two comparisons
of the same inputs are guaranteed to agree.

Every number a person sees in the console is computed by `examend`
and stored, with the comparison it belongs to or with the cell it
summarizes. The console and every other client render stored figures
and compute no statistic.

## Design

### Inputs

The analysis reads **finished trials** (spec 002): trials whose
outcome is neither `infra_failed` nor `canceled` and that were not
retried. A retried trial is `infra_failed`, so it is never read; its
retry is. The smoke trials are read like any other. Every other
outcome, `declined`, `timed_out`, `turn_limit` and `over_budget`
included, is a result of the agent and is analyzed.

For a cell and a metric:

- `n` is the number of finished trials;
- `valued` is the number of those with a current value for the metric
  (spec 007);
- `missing` is `n - valued`.

These are a summary's `n`, `denominator` and `missing` (spec 002), and
the `k` below is its `numerator`. A summary is computed over the valued
trials. `MIN_TRIALS_FOR_VERDICT` counts finished trials, and only those
holding a current value of the metric: a side has enough for a verdict
when `valued` is at least `MIN_TRIALS_FOR_VERDICT`.

Within a side, values are ordered by trial id, ascending. Every sum in
this spec runs in that order.

### Aggregation per cell

A metric's `aggregate` fits its type: `rate` for `binary`,
`distribution` for `label`, and `mean`, `median` or `p90` for `score`,
`count`, `duration` and `cost`.

| Type | Aggregate | Cell summary | Interval at `CONFIDENCE_LEVEL` |
|---|---|---|---|
| `binary` | `rate` | `k / valued`, `k` the number of true values | Wilson score interval |
| `score`, `count`, `duration`, `cost` | `mean`, `median`, `p90` | the aggregate of the values | bootstrap percentile interval of the aggregate |
| `label` | `distribution` | the count and proportion of each declared label, and the mode (ties broken by the order of `labels`) | Wilson score interval of each label's proportion |

For a numeric metric the summary also carries the mean, the sample
standard deviation (divisor `valued - 1`), the minimum and the
maximum, whatever its aggregate.

**Quantiles.** The median is the quantile 0.5 and `p90` the quantile
0.9. The quantile `q` of sorted values `x(1) <= ... <= x(n)` is the
linear interpolation of Hyndman and Fan's type 7:

```
h = (n - 1) * q + 1
Q(q) = x(floor(h)) + (h - floor(h)) * (x(floor(h) + 1) - x(floor(h)))
```

with `x(n + 1)` read as `x(n)`. Every quantile in this spec, bootstrap
percentiles included, uses this definition.

**Wilson score interval.** With `p = k / n` and
`z = sqrt(2) * erfinv(CONFIDENCE_LEVEL)`:

```
center = (p + z^2 / (2n)) / (1 + z^2 / n)
half   = (z / (1 + z^2 / n)) * sqrt(p (1 - p) / n + z^2 / (4 n^2))
interval = [center - half, center + half]
```

**Bootstrap percentile interval.** `BOOTSTRAP_RESAMPLES` resamples,
each `valued` values drawn with replacement from the cell's values;
the aggregate of each; the interval is `[Q(alpha / 2), Q(1 - alpha /
2)]` of those aggregates, with `alpha = 1 - CONFIDENCE_LEVEL`.

### The noise floor

A cell's provenance tuple is fully pinned, so its trials differ only
by what the agent, the model and the environment do differently from
one attempt to the next. That within-cell spread is the noise: no
difference between two cells can be read as a change of the variable
unless it stands out from it.

- The **noise floor of a cell** for a metric is half the width of its
  summary's interval.
- The **noise floor of a result** is half the width of its delta's
  interval (below).

Both are reported. Neither decides a verdict; the test does.

### Tests per task

A task-level result compares the candidate's cell `x` with the
reference's cell `y` for one task and one metric. Its **delta** is
oriented candidate minus reference.

| Type | `delta` (`delta_kind`) | Delta interval | Test (`method`) |
|---|---|---|---|
| `binary` | `k_x / n_x - k_y / n_y` (`difference`) | Newcombe's hybrid score interval | `fisher_exact`, two-sided |
| `score`, `duration`, `cost` | `A(x) - A(y)`, `A` the metric's aggregate (`difference`) | bootstrap percentile, each side resampled independently | `permutation` of `A(x) - A(y)` |
| `count` | the Hodges and Lehmann shift: the median of the `n_x * n_y` differences `x_i - y_j` (`shift`) | Moses' interval from the test's distribution | `rank_sum`: the Wilcoxon and Mann and Whitney rank-sum test by permutation |
| `label` | the total variation distance `1/2 * sum over labels of abs(p_x(l) - p_y(l))` (`total_variation`) | bootstrap percentile | `chi_square_permutation` |

Here `n_x` and `n_y` are the two sides' `valued`.

**Fisher's exact test.** The table is candidate true, candidate
false, reference true, reference false: `a, b, c, d`, with
`K = a + c`, `n1 = a + b`, `N = a + b + c + d`. With the margins fixed,
`P(i) = C(K, i) * C(N - K, n1 - i) / C(N, n1)`, computed in log space
with the log-gamma function. The two-sided p-value is the sum of
`P(i)` over every `i` with `P(i) <= P(a) * (1 + TIE_TOLERANCE)`.
Barnard's test is rejected: its p-value is a maximum over a nuisance
parameter that has to be found by numeric search, so two
implementations, or two grids, give different p-values for one table.

**Newcombe's hybrid score interval.** With `p1 = k_x / n_x`,
`p2 = k_y / n_y` and their Wilson intervals `[l1, u1]` and `[l2, u2]`:

```
lower = (p1 - p2) - sqrt((p1 - l1)^2 + (u2 - p2)^2)
upper = (p1 - p2) + sqrt((u1 - p1)^2 + (p2 - l2)^2)
```

**Permutation tests.** The pooled values are `x` followed by `y`. A
relabeling chooses which `n_x` of the `n_x + n_y` pooled values form
the candidate side. Each method has a non-negative **discrepancy**
`S`, large when the sides differ:

| Method | Statistic `T` | Discrepancy `S` |
|---|---|---|
| `permutation` | `A(x) - A(y)` | `abs(T)` |
| `rank_sum` | `U - n_x * n_y / 2`, with `U = W - n_x (n_x + 1) / 2` and `W` the sum of the candidate side's midranks in the pooled values (tied values share the mean of their ranks) | `abs(T)` |
| `chi_square_permutation` | none | Pearson's `X^2 = sum (O - E)^2 / E` over the 2 by k table of label counts, labels with no count on either side left out, `E = row total * column total / N` |

- **Exact** when `C(n_x + n_y, n_x)` is at most
  `PERMUTATION_EXACT_MAX`: every relabeling is enumerated and
  `p = #{relabelings with S >= S_obs} / C(n_x + n_y, n_x)`, the
  observed labeling included.
- **Monte Carlo** otherwise: `PERMUTATION_RESAMPLES` relabelings by
  shuffling the pooled values (Determinism, below), the first `n_x`
  forming the candidate side, and
  `p = (1 + #{resamples with S >= S_obs}) / (1 + PERMUTATION_RESAMPLES)`.

`a >= b` in these counts holds when `a >= b - TIE_TOLERANCE * max(1,
abs(b))`, so a relabeling that reproduces the observed discrepancy is
counted even when floating-point rounding puts it a hair below. With
one label observed on both sides, `X^2` is zero and `p` is 1.

**Counts.** Counts of tokens, turns and tool calls are discrete,
heavily tied and skewed by the occasional runaway trial. The rank-sum
test is exact under ties when computed by permutation of midranks,
and one runaway trial moves it by one rank, not by its size. Its
delta is the Hodges and Lehmann shift because the shift and the
rank statistic never have opposite signs: when more differences
`x_i - y_j` are positive than negative, `T` is positive and the median
difference is at least zero, and the converse. Moses' interval: let
`k` be the floor of the quantile `alpha / 2` of the distribution of
`U` over the relabelings the test enumerated or drew; the interval is
`[d(k + 1), d(M - k)]` of the sorted `M = n_x * n_y` differences.

### Within-cell and between-cell variance

Two kinds of spread are kept apart.

- **Within-cell variance** is trial-to-trial spread in one fully
  pinned cell, the noise. For a side over a set of tasks it is pooled:
  `sum_t (n_t - 1) s_t^2 / sum_t (n_t - 1)` over tasks with
  `n_t >= 2`, where `s_t^2` is the sample variance of the cell's
  values (for a binary metric, `k (n - k) / (n (n - 1))`).
- **Between-cell variance** is how much the cells of one candidate
  differ from task to task: `sum_t w_t (s_t - S)^2 / W`, the weighted
  variance of the per-task summaries `s_t` around the suite-level
  summary `S` (below).

Every interval and test in this spec treats the suite's tasks as
fixed: the question is whether the candidate changed what the agent
does on these tasks, so only within-cell variance enters an interval
or a test. Between-cell variance is reported on every suite-level
summary, beside the within-cell variance, because it says how unevenly
the metric sits across tasks and whether more trials (within-cell
dominates) or more tasks (between-cell dominates) would sharpen the
next experiment. A claim about tasks outside the suite needs a model
of how tasks are sampled, which Examen does not make. Label metrics
carry neither figure.

### Suite-level summaries and task weights

A suite gives each task a **weight**, a finite number at least zero,
in `Suite.weights` keyed by task digest; a task it does not list
weighs 1, and at least one task weighs more than zero. The weights are
part of the suite's content, so changing one makes a new suite
version with a new digest. A task of weight 0 is run, summarized and
compared per task, and left out of every suite-level figure. That is
how an owner adds a task without breaking the suite-level comparison
with a baseline that lacks it: add it at weight 0, move the baseline
to an experiment that ran it, then raise its weight.

Over a set of tasks `T` with weights `w_t > 0` and `W = sum w_t`:

| Type | Suite-level summary `S` |
|---|---|
| `binary`, numeric | `S = sum_t w_t s_t / W`, the weighted mean of the per-task summaries: of rates for a rate, of means, medians or 90th percentiles for those aggregates |
| `label` | the weighted mean of the per-task proportion vectors |

So a task with more valued trials does not count for more than its
weight.

- **Interval: stratified bootstrap.** Each of `BOOTSTRAP_RESAMPLES`
  resamples redraws every task's values from that task's cell alone,
  `n_t` with replacement, and recomputes `S`. The interval is the
  percentile interval of those values. For a delta, both sides are
  redrawn this way and the statistic is the difference of the two
  `S`, or their total variation distance for a label metric.
- **Test: stratified permutation** (`method` `stratified_permutation`).
  A relabeling permutes the labels
  within each task separately, keeping each task's `n_x` and `n_y`.
  The statistic is `S_x - S_y`, with discrepancy `abs(S_x - S_y)`,
  or the total variation distance between the weighted distributions
  for a label metric. The test is exact when the product over tasks of
  `C(n_x + n_y, n_x)` is at most `PERMUTATION_EXACT_MAX`, and Monte
  Carlo otherwise, with the p-values defined as per task. A count
  metric uses the same test on the weighted difference of its
  aggregates, and its suite-level `delta_kind` is `difference`.
- The Cochran, Mantel and Haenszel test and van Elteren's stratified
  rank test are rejected: each weighs a task by its number of trials,
  not by the weight the suite declares, so its p-value would test a
  different summary than the one reported.

In a comparison, `T` is the paired tasks (Comparisons, below) of
weight greater than zero, weighted by the candidate's suite. A
candidate's own suite-level summary in one experiment, without a
reference, is over every task of its suite with weight greater than
zero. `coverage` is the share of the total weight of the candidate's
suite that `T` holds with enough valued trials on both sides.

### Verdicts

Each result gets one verdict, by this rule, in this order:

1. `insufficient` when either side's `valued` is below
   `MIN_TRIALS_FOR_VERDICT`. A suite-level result is `insufficient`
   when any task of `T` is, or when its `coverage` is below 1. An
   insufficient result has no p-value and takes no part in the
   correction.
2. For a metric whose `direction` is `none`: `changed` when the
   corrected value `q` is at most `FALSE_DISCOVERY_RATE`, and
   `within_noise` otherwise.
3. For a metric with a direction: when `q` is at most
   `FALSE_DISCOVERY_RATE`, `improved` if the sign of the test's
   statistic is the metric's direction (positive for
   `higher_is_better`, negative for `lower_is_better`) and `regressed`
   if it is the opposite; `within_noise` otherwise.

The statistic's sign is the delta's for every method but the
rank-sum test, whose sign agrees with its delta's whenever both are
non-zero. A statistic of zero gives `p = 1`, so it is always
`within_noise`.

The verdict comes from the corrected test alone. The delta's interval
is descriptive and gives the noise floor; it is not part of the rule,
because it is not corrected for multiple comparisons and is built by a
different method than the test (Newcombe's against Fisher's,
bootstrap against permutation). A row can therefore show `improved`
with an interval that touches zero, or `within_noise` with one that
does not, and the report shows the `q` that decided it.

### Multiple comparisons

Each comparison is corrected in **two families**:

- the **task family**: every task-level result of the comparison, for
  every metric of the suite, that has a p-value;
- the **suite family**: every suite-level result of the comparison
  that has a p-value.

Within a family of `m` p-values sorted ascending,
`p(1) <= ... <= p(m)`, the Benjamini and Hochberg corrected values are

```
q(i) = min over j >= i of min(1, m * p(j) / j)
```

Tied p-values receive the same `q`. A result is significant when its
`q` is at most `FALSE_DISCOVERY_RATE`. Each result keeps both `p` and
`q`, so the uncorrected value is always readable beside the corrected
one, and the comparison's `correction` records the method,
`benjamini_hochberg`, and the size `m` of each family.

Why the family is one comparison, and not one experiment: a comparison
is what a bar reads (spec 009) and what a report headlines (spec 010),
and its verdicts must not move when another candidate is added to the
same experiment. With one family per experiment, adding a candidate
would change every other candidate's corrected values.

Why two families: a suite-level result summarizes the same trials as
the task-level results of its metric and answers a different question,
whether the candidate moved the metric over the suite. Putting the few
suite-level results in one family with the many task-level ones would
let thousands of task tests dilute the handful a release decision
rests on.

Benjamini and Hochberg's procedure controls the false discovery rate
when the p-values are positively dependent, which is the dependence
between metrics measured on the same trials, such as cost, tokens and
duration. Benjamini and Yekutieli's correction, which holds under any
dependence, is rejected: at thousands of task-level results it would
leave almost nothing detectable at small N.

### Confounds and the variable

A comparison declares what it means to vary as one **logical
variable**, and `variable` (spec 002) holds that variable's tuple
fields:

| Logical variable | Tuple fields |
|---|---|
| `agent` | `candidate.kind`, `agent_or_image_digest` |
| `model` | `model` |
| `reasoning` | `reasoning` |
| `tools` | `tools` |
| `environment` | `environment_digest` |
| `workspace` | `workspace_ref` |

`task_hash` and `grader_digests` are never a variable: a change to
either changes what is measured, not the agent. A comparison holds one
logical variable or none. When a request declares none, the variable
is the one logical variable whose fields differ between the two sides;
when none differs it is empty, an A/A comparison whose every
`improved` or `regressed` verdict is a false discovery or drift over
time; when several differ it is empty and every differing field is a
confound.

**Tuple confounds** are computed per paired task by comparing the two
cells' tuples field by field. `tools` is compared as a sorted set, an
absent narrowing as the agent's own tools. `grader_digests` is
compared per metric: the producing grader each side was read at for
that metric (Comparisons, below), so a new judge for one metric
confounds that metric's results only. A field that differs and is not
in `variable` is a confound of that task's results.

**Layer confounds** are computed from the sent and observed layers
(spec 007), because a pinned configuration does not guarantee what
was sent or what served:

| Confound | When |
|---|---|
| `observed.model` | on either side, a request was served by a model other than the one it was sent to |
| `sent.model` | on either side, a trial with at least one request sent none to its configured `model`: the override did not take effect |
| `sent.reasoning` | the set of `reasoning` values sent to the configured model differs between the two sides, and the variable is none of `agent`, `model` and `reasoning` |

A task-level result's `confounds` are its task's tuple and layer
confounds, for its metric. A suite-level result's are the union over
`T`. The comparison's `confounds` are the union over all its results,
and `single_variable` is true exactly when that union is empty
(spec 002). A comparison whose variable is `model` and whose two sides
differ in nothing else is single-variable: comparing two models on one
agent version is the case spec 001's decision 4 exists for.

### Comparisons

A comparison has a candidate side, the candidate's cells in its
experiment, and a reference side, which is exactly one of:

- **a baseline**: the baseline's candidate's cells in the baseline's
  experiment (`baseline` set);
- **another candidate of the same experiment**
  (`reference_candidate` set).

**Pairing.** Cells pair by task name. A task on one side only is
listed in `unpaired_tasks` and has no task-level result. Metrics pair
by digest: a metric of the candidate's suite for which the reference
holds no values has `valued = 0` on the reference side and is
insufficient.

**Which grader each side is read at.** The candidate side is read at
the producing grader its suite names. The reference side is read at
the candidate side's producing grader when every finished reference
trial has a grading by it, which a re-grade of the baseline produces
(spec 006), and at its own suite's producer otherwise, in which case
`grader_digests` is a confound for that metric.

Two candidates of one experiment share the suite, the tasks and the
time window, so their task-level tuple fields are equal by
construction and only candidate fields and layers can differ. A
comparison with a baseline spans two time windows, and a change at a
model provider between them is in no tuple field. The comparison
records both windows; an owner who needs to separate drift from the
variable includes the baseline's candidate as a candidate of the new
experiment and compares the two in one window.

**When comparisons are computed.** When an experiment that names a
baseline reaches `complete`, one comparison per candidate against it.
When a grading is later appended to a trial either side read, a new
comparison of the same pair; the earlier one stays. On request, any
candidate against any baseline the owner may read or against another
candidate of the same experiment, with an optional declared variable.
A comparison is computed by the work queue and is never changed once
complete; recomputing makes a new comparison.

**The comparison record** is spec 002's. Of its fields, the analysis
fills `reference_candidate` or `baseline`, `state`, `unpaired_tasks`,
`candidate_suite` (what a bar's `at_least` and `at_most` read, spec
009), `windows`, `verdict_counts`, `correction` (Multiple comparisons,
above), `analysis_version`, `parameters`, `inputs_digest` and `seed`
(Determinism, below), and `results`. Each result is spec 002's result,
and each of its two sides, like every stored summary below, is spec
002's summary: `n`, `denominator` (`valued`), `missing`, `numerator`
(`k`) for a binary metric, `value`, `interval`, `noise_floor`, `mean`,
`sd`, `min` and `max` for a numeric metric, `distribution` and `mode`
for a label metric, at suite level `tasks`, `coverage`,
`within_variance` and `between_variance`, and `excluded` and `final`.

### The stored analysis

Beside comparisons, `examend` keeps the figures an experiment shows
before any comparison exists, so the console renders an experiment
while it runs and one with no baseline:

- per cell and metric, the cell's summary;
- per cell, its outcome counts (every outcome of spec 002, the
  `infra_failed` trials and their retries included); its spend: the
  cost of every trial of the cell, the smoke trial and failed trials
  included, and of the cell's judge gradings, from Lux's records; its
  judge spend, the part of the spend that judge gradings made; and,
  when the suite's primary metric is binary, its cost per success:
  the cost of its finished trials over the number of true values,
  absent when there are none;
- per experiment, candidate and metric, the candidate's suite-level
  summary, and the same spend figures summed over the candidate's
  cells.

They are recomputed by the work queue when a trial of the cell reaches
`done` or a grading is recorded, and replace the previous figures. A
stored summary is computed by the same procedures as a comparison, its
root seed derived by the same formula from its own inputs document: the
canonical JSON of the cell's or candidate's finished trials, with
the measurement ids and values it read. Recomputing it when nothing
changed is therefore bit-identical. A comparison never reads the
stored analysis: it computes its own side summaries and keeps them, so
a comparison stays reproducible when the stored analysis moves on.

Each comparison also stores its `verdict_counts`, which a headline shows.

### Trends across successive baselines

Moving a baseline makes a new record (spec 002) that names the record
it replaced in `previous`. The records linked this way, newest first,
are the baseline's **lineage**. A trend reads the newest
`MAX_TREND_POINTS` records of a lineage and shows, per metric:

- **points**: each record's reference side as the candidate's
  suite-level summary of its experiment;
- **steps**: for each pair of consecutive records, the comparison of
  the newer record's candidate and experiment against the older
  record, an ordinary comparison computed on request when it does not
  yet exist, with its verdicts and confounds.

A step between records that measured a metric under different digests
is insufficient for that metric, and the series shows the break. An
A/A step, the same candidate measured again later, is how drift at a
model provider becomes visible.

### Determinism

The same inputs give the same results, bit for bit on one
architecture.

**Inputs.** A comparison's inputs document is the canonical JSON of
spec 002 of: `analysis_version`, `parameters`, `variable`, the
candidate suite's weights, and for each side and each paired task the
task name and digest, the cell's tuple, the producing grader read per
metric, and per finished trial its id, outcome, sent and served
models, reasoning values, and every measurement read as
`{metric, grader, generation, value}`. `inputs_digest` is `sha256:`
and the hex of its SHA-256. The measurement ids read are recorded in
`comparison_inputs` (spec 007), from which the document is rebuilt.

**Seeds.** The root seed is `SHA-256("examen.analysis/v1" || 0x00 ||
inputs_digest)`. Each procedure of each result has its own seed,
`SHA-256(root || metric digest || 0x00 || task name or "suite" ||
0x00 || procedure)`, `procedure` one of `summary_interval:candidate`,
`summary_interval:reference`, `delta_interval` and `test`, so no
result's random draws depend on the order in which results are
computed. The root seed and each result's seed are recorded.

**Generator.** The 128-bit permuted congruential generator with DXSM
output that Go's `math/rand/v2` `PCG` implements, seeded with the
first and second 8 bytes of the procedure's seed read as big-endian
unsigned integers. Examen draws from it with two methods of its own
and never through a library helper whose algorithm may change:

- a uniform integer in `[0, n)` by Lemire's method: take a 64-bit
  value `v`, form the 128-bit product `v * n`, and when its low half
  is below `(2^64 - n) mod n` draw again; the result is the high half;
- a shuffle by Fisher and Yates: for `i` from `len - 1` down to `1`,
  swap position `i` with the uniform integer in `[0, i + 1)`.

A bootstrap resample draws its indices in order, for tasks in
ascending bytewise order of name, the candidate side before the
reference side.

**Arithmetic.** IEEE 754 binary64. Sums run in the orders this spec
fixes. Every product that is summed is rounded before the addition
(in Go, an explicit `float64(x * y)`), which forbids the fused
multiply-add Go otherwise emits on arm64. Verdicts are identical
across amd64 and arm64.

**Recomputation** reads the recorded measurement ids and the recorded
parameters, so a comparison recomputed by a later release with
changed defaults gives the results it gave when it was made.

### Built-in metrics

The built-in metrics are analyzed with the types, aggregates and
directions spec 002's table gives them. Tokens, turns and tool calls
have the direction `none`: they describe behavior; more is not worse in
general, so a change in them is `changed`, never `regressed`. An owner
who wants fewer of them declares a metric of their own with a
direction.

### Limits

The constants of spec 002 this spec holds: `MIN_TRIALS_FOR_VERDICT`,
`FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL`, `BOOTSTRAP_RESAMPLES`,
`PERMUTATION_RESAMPLES`, `PERMUTATION_EXACT_MAX`, `TIE_TOLERANCE` and
`MAX_TREND_POINTS`.

## Not in this spec

How measurements are stored and which is current (007). How graders
produce values, judge calibration and agreement statistics (006). Bar
rules (009). How figures are formatted and laid out (010). Sequential
testing and stopping a cell early, which N declared before a cell runs
excludes (spec 001, decision 7). A minimum effect size: a change that
must be large to matter is a bar's `at_least` or `at_most` (009).
Generalizing beyond a suite's tasks.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Wilson intervals, Newcombe intervals, Fisher's two-sided p-values and type 7 quantiles match committed reference values, ties and the extremes `k = 0` and `k = n` included | `analysis.TestWilson`, `analysis.TestNewcombe`, `analysis.TestFisherExact`, `analysis.TestQuantileType7` |
| A permutation test with at most `PERMUTATION_EXACT_MAX` relabelings enumerates them all and matches a hand-computed p-value; above it, it draws `PERMUTATION_RESAMPLES` and uses the `(1 + count) / (1 + B)` estimate | `analysis.TestPermutationExact`, `analysis.TestPermutationMonteCarlo` |
| The rank-sum test on tied counts matches the exact distribution, and the Hodges and Lehmann shift never has the opposite sign of its statistic over a property-based sweep | `analysis.TestRankSumTies`, `analysis.TestShiftSignAgreesWithStatistic` |
| Equal data on both sides give `p = 1` and `within_noise` for every method | `analysis.TestEqualSidesAreWithinNoise` |
| With `MIN_TRIALS_FOR_VERDICT - 1` valued trials on either side, every verdict is `insufficient` and the result is in no family | `analysis.TestInsufficientBelowMinimum` |
| A metric with direction `none` is never `improved` or `regressed`; a significant change is `changed` | `analysis.TestDirectionNone` |
| The verdict follows `q` and the statistic's sign alone, whatever the interval shows | `analysis.TestVerdictRule` |
| Benjamini and Hochberg's values match a reference implementation; the task and suite families are corrected separately; adding a candidate to an experiment leaves another candidate's `q` values unchanged | `analysis.TestBenjaminiHochberg`, `analysis.TestFamiliesAreSeparate`, `analysis.TestAddingACandidateChangesNoOtherVerdict` |
| A suite-level summary is the weighted mean of the per-task summaries; a weight-0 task is compared per task and absent from every suite-level figure; a missing task makes the suite-level result `insufficient` | `analysis.TestSuiteWeights`, `analysis.TestCoverage` |
| Within-cell and between-cell variance match hand-computed values | `analysis.TestVarianceDecomposition` |
| Two models on one agent version give a single-variable comparison; a differing task hash, a request served by another model, and an ignored model override each appear as confounds | `analysis.TestModelComparisonIsSingleVariable`, `analysis.TestConfounds` |
| A declared variable outside the logical variable table, or naming `task_hash` or `grader_digests`, is refused | `analysis.TestVariableIsOneLogicalVariable` |
| A re-graded baseline is read at the candidate's grader and the grader confound disappears; a partly re-graded one is not | `analysis.TestReferenceReadAtCandidatesGrader` |
| A comparison recomputed from its recorded inputs is byte-identical, and its seeds are the recorded ones | `analysis.TestRecomputeFromRecordedInputs` |
| A golden comparison yields identical verdicts and figures agreeing to 12 significant digits on amd64 and arm64 | `analysis.TestGoldenAcrossArchitectures`, run on both in CI |
| Every figure of the console's grid, verdict tables, gap map and cost table is read from a stored comparison or the stored analysis; the console bundle contains no statistical routine | `analysis.TestEveryShownFigureIsStored`, `console.TestNoStatisticsInTheClient` |
| A trend over a lineage of three baselines shows three points and two step comparisons | `analysis.TestTrend` |
