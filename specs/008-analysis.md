---
title: "Analysis: per-cell summaries and their intervals by metric type, Boschloo's exact test and permutation tests, suite-level summaries with task weights and coverage, the primary result and the one corrected family, verdicts and their reasons, computed confounds, automatic and progressive comparisons, and trends, all computed deterministically and stored"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/005-execution.md
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
findings, the verdict rule and the plain reason each verdict carries,
how confounds are computed from the provenance tuple and the
provenance layers, and how two comparisons of the same inputs are
guaranteed to agree.

Every number a person sees in the console is computed by `examend`
and stored, with the comparison it belongs to or with the cell it
summarizes. The console and every other client render stored figures
and compute no statistic.

## Design

### Inputs

The analysis reads **finished trials** (spec 002): trials whose
outcome is none of `infra_failed`, `over_budget` and `canceled` and
that were not retried. A retried trial is never read; its retry is.
The smoke trials are read like any other. `declined`, `timed_out` and
`turn_limit` are results of the agent and are analyzed. An
`over_budget` trial says how much money was left, not what the agent
does, so it is excluded like an infrastructure failure, and its cell
parks until it is retried (spec 005).

For a cell and a metric:

- `n` is the number of finished trials;
- `valued` is the number of those with a current value for the metric
  (spec 007);
- `missing` is `n - valued`.

These are a summary's `n`, `denominator` and `missing` (spec 002), and
the `k` below is its `numerator`. A summary is computed over the valued
trials. A side of a task-level result has enough for a verdict when
`valued` is at least `MIN_TRIALS_FOR_VERDICT`.

Within a side, values are ordered by trial id, ascending. Every sum in
this spec runs in that order.

### Aggregation per cell

A metric's `aggregate` fits its type: `rate` for `binary`,
`distribution` for `label`, and `mean`, `median` or `p90` for `score`,
`count`, `duration` and `cost`.

| Type | Aggregate | Cell summary | Interval at `CONFIDENCE_LEVEL` |
|---|---|---|---|
| `binary` | `rate` | `k / valued`, `k` the number of true values | Wilson score interval |
| `score`, `count`, `duration`, `cost` | `mean` | the mean of the values | bootstrap-t interval |
| `score`, `count`, `duration`, `cost` | `median`, `p90` | the quantile of the values | order-statistic interval; none for a `p90` of fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values |
| `label` | `distribution` | the count and proportion of each declared label, and the mode (ties broken by the order of `labels`) | Wilson score interval of each label's proportion |

For a numeric metric the summary also carries the mean, the sample
standard deviation `s` (divisor `valued - 1`), the minimum and the
maximum, whatever its aggregate. Throughout, `alpha = 1 -
CONFIDENCE_LEVEL`.

**Quantiles.** The median is the quantile 0.5 and `p90` the quantile
0.9. The quantile `q` of sorted values `x(1) <= ... <= x(n)` is the
linear interpolation of Hyndman and Fan's type 7:

```
h = (n - 1) * q + 1
Q(q) = x(floor(h)) + (h - floor(h)) * (x(floor(h) + 1) - x(floor(h)))
```

with `x(n + 1)` read as `x(n)`. Every point estimate of a quantile, and
every quantile of resampled statistics in this spec, uses this
definition.

**Wilson score interval.** With `p = k / n` and
`z = sqrt(2) * erfinv(CONFIDENCE_LEVEL)`:

```
center = (p + z^2 / (2n)) / (1 + z^2 / n)
half   = (z / (1 + z^2 / n)) * sqrt(p (1 - p) / n + z^2 / (4 n^2))
interval = [center - half, center + half]
```

**Bootstrap-t interval of a mean.** With `m` the mean of the `n`
valued values and `se = s / sqrt(n)`: each of `BOOTSTRAP_RESAMPLES`
resamples draws `n` values with replacement, and has its own mean
`m*`, its own `se*` and `t* = (m* - m) / se*`. A resample whose `se*`
is 0 is drawn again, so every one of the resamples has a `t*`. With
`Q*` the quantile of the `t*` values:

```
interval = [m - Q*(1 - alpha / 2) * se, m - Q*(alpha / 2) * se]
```

When every value is equal, `se` is 0 and the interval is `[m, m]`,
with no resampling. The studentized interval is used because a cell is
small and its costs, durations and counts are skewed by the occasional
runaway trial, where a percentile interval of the mean is too narrow.

**Order-statistic interval of a quantile.** Let `B` be the number of
values below the true quantile `q`, which follows the binomial
distribution of `n` trials and probability `q` whatever the values'
own distribution. The interval `[x(l), x(u)]` then covers the true
quantile with probability at least

```
cover(l, u) = sum over i from l to u - 1 of C(n, i) * q^i * (1 - q)^(n - i)
```

The ranks are the pair `1 <= l < u <= n` with `cover(l, u)` at least
`CONFIDENCE_LEVEL` and the smallest `u - l`; among equal pairs, the
one whose two tails, `P(B < l)` and `P(B >= u)`, differ least; then
the smallest `l`. When no pair reaches `CONFIDENCE_LEVEL`, as for a
median of 5 values at 0.95, the summary has no interval. A `p90` shows
none below `MIN_TRIALS_FOR_P90_INTERVAL` values whatever the pair
search finds: below it, the chance that every value lies under the
true 90th percentile, `0.9^n`, is above about 2.5%, so the interval's
top could only be the largest value seen. Tied values make the
coverage larger, never smaller.

### Tests per task

A task-level result compares the candidate's cell `x` with the
reference's cell `y` for one task and one metric. Its **delta** is
oriented candidate minus reference. Here `n_x` and `n_y` are the two
sides' `valued`.

| Type | `delta` (`delta_kind`) | Delta interval | Test (`method`) |
|---|---|---|---|
| `binary` | `k_x / n_x - k_y / n_y` (`difference`) | Newcombe's hybrid score interval | `boschloo_exact`, two-sided |
| `score`, `count`, `duration`, `cost` with aggregate `mean` | `mean(x) - mean(y)` (`difference`) | bootstrap-t interval of the difference | `permutation` of the difference of means |
| `score`, `count`, `duration`, `cost` with aggregate `median` or `p90` | `Q(x) - Q(y)` (`difference`) | the difference of two order-statistic intervals; none when either side has none, or for a `p90` when either side has fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values | `permutation` of the difference of quantiles |
| `label` | per label `l`, `p_x(l) - p_y(l)` (`per_label`) | Newcombe's hybrid score interval per label | none in the first slice (Label metrics, below) |

**Boschloo's exact unconditional test.** The table is candidate true,
candidate false, reference true, reference false: `a, b, c, d`, with
`n_x = a + b` and `n_y = c + d`. Under the null hypothesis both sides
share one unknown rate `pi`, the nuisance parameter. The test orders
every possible table `(i, j)`, `i` of `n_x` and `j` of `n_y` true, by
Fisher's two-sided p-value `F(i, j)` given its margins, and its p-value
is the largest, over `pi`, of the probability under `pi` of a table at
least as extreme as the one observed:

```
p = max over pi in the grid of
      sum over (i, j) with F(i, j) <= F(a, c) * (1 + TIE_TOLERANCE) of
        C(n_x, i) pi^i (1 - pi)^(n_x - i) * C(n_y, j) pi^j (1 - pi)^(n_y - j)
```

`F(i, j)` is computed with `K = i + j` and `N = n_x + n_y`, the margins
held fixed: `P(t) = C(K, t) * C(N - K, n_x - t) / C(N, n_x)`, and
`F(i, j)` is the sum of `P(t)` over every `t` with
`P(t) <= P(i) * (1 + TIE_TOLERANCE)`. Every probability is computed in
log space with the log-gamma function. The grid is fixed:
`pi_g = (g - 1/2) / BOSCHLOO_GRID_POINTS` for `g` from 1 to
`BOSCHLOO_GRID_POINTS`, so every implementation takes its maximum over
the same points and answers one p-value for one table. The test holds
its level at every rate, which Fisher's test, conditioning on a total
of successes the experiment never fixed, does only by being
conservative; ordered by Fisher's p-value, Boschloo's test is
uniformly more powerful than Fisher's. Fisher's p-value is its ordering
statistic and is reported nowhere as a test of its own. Barnard's test
with a numeric search for the maximum is rejected: two searches can
stop at different points and answer different p-values for one table.

**Newcombe's hybrid score interval.** With `p1 = k_x / n_x`,
`p2 = k_y / n_y` and their Wilson intervals `[l1, u1]` and `[l2, u2]`:

```
lower = (p1 - p2) - sqrt((p1 - l1)^2 + (u2 - p2)^2)
upper = (p1 - p2) + sqrt((u1 - p1)^2 + (p2 - l2)^2)
```

**Bootstrap-t interval of a difference of means.** With
`d = mean(x) - mean(y)` and `se = sqrt(s_x^2 / n_x + s_y^2 / n_y)`:
each of `BOOTSTRAP_RESAMPLES` resamples redraws each side from its own
values, `n_x` and `n_y` with replacement, the candidate side first, and
has its own `d*`, `se*` and `t* = (d* - d) / se*`; a resample whose
`se*` is 0 is drawn again; the interval is
`[d - Q*(1 - alpha / 2) * se, d - Q*(alpha / 2) * se]`. When every
value of both sides is equal, the interval is `[d, d]`.

**Difference of two quantiles.** Each side's order-statistic interval
is taken at the level `sqrt(CONFIDENCE_LEVEL)`, `[l_x, u_x]` and
`[l_y, u_y]`. The sides are independent, so both cover together with
probability at least `CONFIDENCE_LEVEL`, and then the difference of the
true quantiles lies in `[l_x - u_y, u_x - l_y]`, which is the delta's
interval. It is distribution-free and errs wide.

**Permutation tests.** The pooled values are `x` followed by `y`. A
relabeling chooses which `n_x` of the `n_x + n_y` pooled values form
the candidate side. The statistic is `T = A(x) - A(y)`, `A` the
metric's aggregate, and the discrepancy `S = abs(T)`.

- **Exact** when `C(n_x + n_y, n_x)` is at most
  `PERMUTATION_EXACT_MAX`: every relabeling is enumerated and
  `p = #{relabelings with S >= S_obs} / C(n_x + n_y, n_x)`, the
  observed labeling included.
- **Monte Carlo** otherwise: `B` relabelings by shuffling the pooled
  values (Determinism, below), the first `n_x` forming the candidate
  side, and `p = (1 + #{resamples with S >= S_obs}) / (1 + B)`.

`a >= b` in these counts holds when `a >= b - TIE_TOLERANCE * max(1,
abs(b))`, so a relabeling that reproduces the observed discrepancy is
counted even when floating-point rounding puts it a hair below.

**How many relabelings.** `B` is `PERMUTATION_RESAMPLES`: the larger of
10,000 and `ceil(20 * m / FALSE_DISCOVERY_RATE)`, where `m` is the
number of p-values in the test's family, the corrected family's size
for every result in it and 1 for the primary metric's suite-level
result (spec 002). A Monte Carlo p-value is at least `1 / (1 + B)`, and
a lone discovery in a family of `m` passes Benjamini and Hochberg's
procedure only with `p` at most `FALSE_DISCOVERY_RATE / m`. At this `B`
the smallest p-value a test can give is about a twentieth of that, so
the number of draws never decides whether a real difference can be
found. `m` is known before any test runs, since whether a result enters
the family depends on its counts alone.

**Counts.** Counts of tokens, turns and tool calls are tested like any
numeric metric: by permutation of the aggregate the metric declares,
so the test asks about the same figure the summary shows. A runaway
trial moves a mean; a metric that should not be moved by one declares
`median`.

### Label metrics

In the first slice a label result is descriptive. Per label it carries
the difference of the two sides' proportions with Newcombe's interval,
and at suite level the difference of the weighted proportions with the
stratified bootstrap interval (below). It carries no `p`, `q` or
`verdict` and takes no part in the family. Label tests are a later
build step, whose design is kept here: `chi_square_permutation`, a
permutation test whose discrepancy is Pearson's
`X^2 = sum (O - E)^2 / E` over the 2 by k table of label counts, labels
with no count on either side left out, `E = row total * column total /
N`, and stratified by task at suite level. With label tests, a
significant change in a label metric, whose direction is `none`, is
`changed`.

### The advice on trials and tasks

Two kinds of spread are kept apart.

- **Within-cell variance** is trial-to-trial spread in one fully
  pinned cell, the noise. For a side over its included tasks it is
  pooled: `V_w = sum_t (n_t - 1) s_t^2 / sum_t (n_t - 1)` over tasks
  with `n_t >= 2`, where `s_t^2` is the sample variance of the cell's
  values (for a binary metric, `k (n - k) / (n (n - 1))`).
- **Between-cell variance** is how much the cells of one candidate
  differ from task to task: `V_b = sum_t w_t (s_t - S)^2 / W`, the
  weighted variance of the per-task summaries `s_t` around the
  suite-level summary `S` (below).

Every interval and test in this spec treats the suite's tasks as
fixed: the question is whether the candidate changed what the agent
does on these tasks, so only within-cell variance enters an interval
or a test. A claim about tasks outside the suite needs a model of how
tasks are sampled, which Examen does not make. The two variances are
not shown as figures. They decide the suite-level summary's `advice`,
one sentence for the person planning the next experiment: with `n_bar`
the mean `n_t` of the included tasks, when `V_w / n_bar` is at least
`V_b` the advice is "More trials per task would narrow this interval
more than more tasks would.", and otherwise "More tasks would narrow
this interval more than more trials per task would." A label metric
carries no advice.

### Suite-level summaries, task weights and coverage

A suite gives each task a **weight**, a finite number at least zero,
in `Suite.weights` keyed by task digest; a task it does not list
weighs 1, and at least one task weighs more than zero. The weights are
part of the suite's content, so changing one makes a new suite
version with a new digest. A task of weight 0 is run, summarized and
compared per task, and left out of every suite-level figure. That is
how an owner adds a task without breaking the suite-level comparison
with a baseline that lacks it: add it at weight 0, move the baseline
to an experiment that ran it, then raise its weight.

**Inclusion and coverage.** A task of weight above zero is included in
a suite-level figure when it has at least `MIN_TRIALS_PER_TASK_IN_SUITE`
valued trials, on both sides and paired for a comparison's result. Two
per side is the least for which a task's own variance, which the
studentized interval needs, exists; the stratified test holds its
level with a task of few trials, which adds little and biases nothing,
so the stricter `MIN_TRIALS_FOR_VERDICT` guards only a task's own
verdict. `coverage` is the share of the total weight of the
candidate's suite tasks of weight above zero that the included tasks
hold. Every task of weight above zero that is not included is listed in
the result's `left_out`, as `unpaired` or `too_few_trials` with each
side's valued trials.

Over the included tasks `T`, with weights `w_t` and `W = sum w_t`:

| Type | Suite-level summary `S` |
|---|---|
| `binary`, numeric | `S = sum_t w_t s_t / W`, the weighted mean of the per-task summaries: of rates for a rate, of means, medians or 90th percentiles for those aggregates |
| `label` | the weighted mean of the per-task proportion vectors |

So a task with more valued trials does not count for more than its
weight.

- **Interval: stratified bootstrap.** Each of `BOOTSTRAP_RESAMPLES`
  resamples redraws every included task's values from that task's cell
  alone, `n_t` with replacement, and recomputes `S`. For a `rate` or a
  `mean`, `S` is a weighted mean and its interval is studentized as
  above, with `se = sqrt(sum_t w_t^2 v_t / n_t) / W`, `v_t` the task's
  sample variance, and a resample whose `se*` is 0 drawn again. For a
  `median` or a `p90`, no distribution-free interval exists for a
  weighted sum of quantiles, and the interval is the percentile
  interval `[Q(alpha / 2), Q(1 - alpha / 2)]` of the resampled `S`; a
  `p90` has none when any included task has fewer than
  `MIN_TRIALS_FOR_P90_INTERVAL` valued trials. For a delta, both sides
  are redrawn this way, the candidate side first, and the statistic is
  the difference of the two `S`, studentized for a rate or a mean with
  `se = sqrt(se_x^2 + se_y^2)`; for a label metric, per label, the
  difference of the weighted proportions with its percentile interval.
- **Test: stratified permutation** (`method` `stratified_permutation`).
  A relabeling permutes the labels within each included task
  separately, keeping each task's `n_x` and `n_y`. The statistic is
  `S_x - S_y`, with discrepancy `abs(S_x - S_y)`. The test is exact
  when the product over tasks of `C(n_x + n_y, n_x)` is at most
  `PERMUTATION_EXACT_MAX`, and Monte Carlo otherwise, with the p-values
  defined as per task. A binary or count metric uses the same test, and
  every suite-level `delta_kind` of a numeric or binary metric is
  `difference`.
- The Cochran, Mantel and Haenszel test and van Elteren's stratified
  rank test are rejected: each weighs a task by its number of trials,
  not by the weight the suite declares, so its p-value would test a
  different summary than the one reported.

In a comparison, the tasks considered are the paired tasks
(Comparisons, below) of weight greater than zero, weighted by the
candidate's suite. A candidate's own suite-level summary in one
experiment, without a reference, is over its suite's tasks with weight
greater than zero that it holds enough valued trials of.

### Verdicts

Each result of a metric with a verdict gets one, by this rule, in this
order:

1. `insufficient` when a task-level result has a side whose `valued`
   is below `MIN_TRIALS_FOR_VERDICT`, or a suite-level result's
   `coverage` is below `MIN_SUITE_COVERAGE`. An insufficient result has
   no p-value and takes no part in the family. A suite-level result of
   enough coverage is tested over the tasks it includes, and names the
   others in `left_out`.
2. For a metric whose `direction` is `none`: `changed` when the
   corrected value `q` is at most `FALSE_DISCOVERY_RATE`, and
   `within_noise` otherwise.
3. For a metric with a direction: when `q` is at most
   `FALSE_DISCOVERY_RATE`, `improved` if the sign of the delta is the
   metric's direction (positive for `higher_is_better`, negative for
   `lower_is_better`) and `regressed` if it is the opposite;
   `within_noise` otherwise.

A delta of zero has no direction and is always `within_noise`. A
`label` result has no verdict in the first slice (Label metrics,
above).

The verdict comes from the test alone. The delta's interval is
descriptive: it is not corrected for multiple comparisons and is built
by a different method than the test (Newcombe's against Boschloo's,
bootstrap-t and order statistics against permutation). A row can
therefore show `improved` with an interval that touches zero, or
`within_noise` with one that does not, and its `reason` says what
decided it. A console shows `within_noise` as "no clear difference",
with the delta's interval and the counts (spec 010).

### Reasons

Every result stores `reason`, one plain sentence that says why its
verdict is what it is, built from fixed templates with numbers in spec
010's formats:

| Case | `reason` |
|---|---|
| a task-level side below the minimum | "task t3 has 3 trials with a value; 5 needed" |
| a suite-level result below coverage | "covers 70% of the suite's weight; 80% needed; left out: t3 (3 trials), t7 (not run by the reference)" |
| the primary metric's suite-level result | "p < 0.001 for the primary metric, tested alone without correction" |
| a result of the family | "p = 0.048; q = 0.19 after correcting 40 results at 5%" |
| a delta of zero | "no difference between the two sides" |
| a label result | "descriptive: label tests are not built yet" |

A result with `missing` among its confounds adds the counts, such as
"; 2 of 20 candidate trials have no value". The console shows `reason`
beside every verdict.

### Multiple comparisons

A comparison's p-values are read in two parts:

- **The primary result.** The suite-level result of the suite's
  `primary_metric` is one pre-declared test: the suite names the metric
  before the experiment runs, and the comparison exists to answer it.
  It is tested alone at `FALSE_DISCOVERY_RATE` and reported without
  correction, so its `q` equals its `p`.
- **The family.** Every other result of the comparison that has a
  p-value, the task-level results of every metric, the primary
  metric's included, and the suite-level results of every other
  metric, is one family, corrected together.

Within the family of `m` p-values sorted ascending,
`p(1) <= ... <= p(m)`, the Benjamini and Hochberg corrected values are

```
q(i) = min over j >= i of min(1, m * p(j) / j)
```

Tied p-values receive the same `q`. A result is significant when its
`q` is at most `FALSE_DISCOVERY_RATE`. Each result keeps both `p` and
`q`, so the uncorrected value is always readable beside the corrected
one, and the comparison's `correction` records the method,
`benjamini_hochberg`, the primary metric, and `m`.

Why the family is one comparison, and not one experiment: a comparison
is what a bar reads (spec 009) and what a report headlines (spec 010),
and its verdicts must not move when another candidate is added to the
same experiment. With one family per experiment, adding a candidate
would change every other candidate's corrected values.

Why the primary result stands alone: it is the figure a person reads
first and a release bar usually rests on (spec 009), and it was
declared before any trial ran, so testing it alone is one planned test,
not a pick among many. Correcting it together with hundreds of
task-level results would make the result a release rests on depend on
how many tasks and metrics the suite happens to list.

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

A result's `confounds` are spec 002's confound entries, of four kinds.

**Tuple confounds** are computed per paired task by comparing the two
cells' tuples field by field. `tools` is compared as a sorted set, an
absent narrowing as the agent's own tools. `grader_digests` is
compared per metric: the producing grader each side was read at for
that metric (Comparisons, below), so a new judge for one metric
confounds that metric's results only. A field that differs and is not
in `variable` is a confound of that task's results, and of the
suite-level result, naming the task. `single_variable` is true exactly
when no tuple confound exists on any paired task: the comparison varies
its variable and nothing else it was configured with.

**Layer confounds** are computed from the sent and observed layers
(spec 007), because a pinned configuration does not guarantee what
was sent or what served. Each is computed per task and attaches only
to the results of the tasks where it occurs, with the requests it
covers counted per side:

| Confound | When, on either side | Requests counted |
|---|---|---|
| `observed.model` | a request was served by a model other than the one it was sent to | the requests served by another model |
| `sent.model` | a trial with at least one request sent none to its configured `model`: the override did not take effect | the requests of such trials |
| `sent.reasoning` | the set of `reasoning` values sent to the configured model differs between the two sides, and the variable is none of `agent`, `model` and `reasoning` | the requests carrying a value the other side never sent |

A suite-level result carries a layer confound with the tasks it
occurs in and the summed counts. A layer confound leaves
`single_variable` as it is: one fallback in one task names that task's
results, and does not take the comparison out of the headline (spec
010).

**`missing`.** A result whose sides hold finished trials without a
value of its metric carries `missing`, with the count per side. A
result with `missing` is never headlined (spec 010), since a figure
computed without some trials' values is inflated or deflated where no
one can see it.

**`window`.** Once adoption is built (spec 005), a task whose cells
read adopted trials carries `window`, since those trials ran in an
earlier time window than the experiment's own.

The comparison's `confounds` are the union of the names of its results'
confounds.

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

Two candidates of one experiment share the suite, the tasks and, since
the work queue deals a task's cells their trials round robin (spec
005), the time window, so their task-level tuple fields are equal by
construction and only candidate fields and layers can differ. A
comparison with a baseline spans two time windows, and a change at a
model provider between them is in no tuple field. The comparison
records both windows; an owner who needs to separate drift from the
variable includes the baseline's candidate as a candidate of the new
experiment, makes it the reference, and compares the two in one
window.

**When comparisons are computed.**

- **Automatic.** For every candidate of an experiment other than its
  reference candidate, against the experiment's `reference` (spec 002),
  with `automatic` true. It is computed when the candidate's first
  trials finish, and recomputed in place, keeping its id, with `final`
  false, each time a trial of either side reaches `done` or a grading
  of one is recorded. Once both sides are final it is computed a last
  time with `final` true and never changes again. A change of the
  experiment's `reference` starts new automatic comparisons; the
  earlier ones stay.
- **After a later grading.** A grading appended to a trial that a final
  comparison read makes a new comparison of the same pair, final and
  with the same `automatic`; the earlier one stays.
- **On request.** Any candidate against any baseline the owner may
  read or against another candidate of the same experiment, with an
  optional declared variable, computed once from what both sides hold
  then (spec 003).

A provisional comparison is computed by the same rules over the
finished trials so far, so a cell below `MIN_TRIALS_FOR_VERDICT` is
`insufficient` and verdicts appear as cells fill. It is a view of a
running experiment, not a stopping rule: N is declared before a cell
runs, and a bar or a report reads only a final comparison (specs 009
and 010). Every comparison is computed by the work queue.

**The comparison record** is spec 002's. Of its fields, the analysis
fills `reference_candidate` or `baseline`, `automatic`, `state`,
`final`, `single_variable`, `confounds`, `unpaired_tasks`,
`candidate_suite` (what a bar's `at_least` and `at_most` read, spec
009), `windows`, `verdict_counts`, `correction` (Multiple comparisons,
above), `analysis_version`, `parameters`, `inputs_digest` and `seed`
(Determinism, below), and `results`. Each result is spec 002's result,
with its `reason` and, on a suite-level result, its `left_out`; each
of its two sides, like every stored summary below, is spec 002's
summary: `n`, `denominator` (`valued`), `missing`, `numerator` (`k`)
for a binary metric, `value`, `interval`, `mean`, `sd`, `min` and
`max` for a numeric metric, `distribution` and `mode` for a label
metric, at suite level `tasks`, `coverage` and `advice`, and
`excluded` and `final`.

### The stored analysis

Beside comparisons, `examend` keeps the figures an experiment shows
before any comparison exists, so the console renders an experiment
from its first trial:

- per cell and metric, the cell's summary;
- per cell, its outcome counts (every outcome of spec 002, the
  `infra_failed` and `over_budget` trials and their retries included);
  its spend: the cost of every trial of the cell, the smoke trial and
  failed trials included, and of the cell's judge gradings, from Lux's
  records; its judge spend, the part of the spend that judge gradings
  made; and, when the suite's primary metric is binary, its cost per
  success: the cost of its finished trials over the number of true
  values, absent when there are none;
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

Trends are a later build step than the console path; the design is
kept here. Moving a baseline makes a new record (spec 002) that names
the record it replaced in `previous`. The records linked this way,
newest first, are the baseline's **lineage**. A trend reads the newest
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
candidate suite's weights and primary metric, and for each side and
each paired task the task name and digest, the cell's tuple, the
producing grader read per metric, and per finished trial its id,
outcome, sent and served models, reasoning values, and every
measurement read as `{metric, grader, generation, value}`.
`inputs_digest` is `sha256:` and the hex of its SHA-256. The
measurement ids read are recorded in `comparison_inputs` (spec 007),
from which the document is rebuilt.

**Seeds.** The root seed is `SHA-256("examen.analysis/v1" || 0x00 ||
inputs_digest)`. Each procedure of each result has its own seed,
`SHA-256(root || metric digest || 0x00 || task name or "suite" ||
0x00 || procedure)`, `procedure` one of `summary_interval:candidate`,
`summary_interval:reference`, `delta_interval` and `test`, so no
result's random draws depend on the order in which results are
computed. Boschloo's test and the order-statistic intervals draw
nothing. The root seed and each result's seed are recorded.

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
reference side; a resample drawn again because its `se*` is 0 takes
its draws from the same generator, in turn.

**Arithmetic.** IEEE 754 binary64. Sums run in the orders this spec
fixes. Every product that is summed is rounded before the addition
(in Go, an explicit `float64(x * y)`), which forbids the fused
multiply-add Go otherwise emits on arm64, so verdicts are identical
across amd64 and arm64.

**Recomputation** reads the recorded measurement ids and the recorded
parameters, so a comparison recomputed by a later release with
changed defaults gives the results it gave when it was made.

### Built-in metrics

The built-in metrics are analyzed with the types, aggregates and
directions spec 002's table gives them. `completed` has the direction
`none`: the agent ending on its own, declining included, is neither
better nor worse, and whether it succeeded is a check metric's to say.
Tokens, turns and tool calls have the direction `none` too: they
describe behavior, and more is not worse in general. A change in any
of them is `changed`, never `regressed`. An owner who wants fewer of
them declares a metric of their own with a direction.

### Limits

The constants of spec 002 this spec holds: `MIN_TRIALS_FOR_VERDICT`,
`MIN_TRIALS_PER_TASK_IN_SUITE`, `MIN_SUITE_COVERAGE`,
`MIN_TRIALS_FOR_P90_INTERVAL`, `FALSE_DISCOVERY_RATE`,
`CONFIDENCE_LEVEL`, `BOOTSTRAP_RESAMPLES`, `PERMUTATION_RESAMPLES`,
`PERMUTATION_EXACT_MAX`, `BOSCHLOO_GRID_POINTS`, `TIE_TOLERANCE` and
`MAX_TREND_POINTS`.

## Not in this spec

How measurements are stored and which is current (007). How graders
produce values, judge calibration and agreement statistics (006). Bar
rules (009). How figures are formatted and laid out (010). Sequential
testing and stopping a cell early, which N declared before a cell runs
excludes (spec 001, decision 7). A minimum effect size: a change that
must be large to matter is a bar's `at_least` or `at_most` (009).
Generalizing beyond a suite's tasks.

## Build order

The first slice builds everything above but label tests, trends and
the cross-architecture run of the golden comparison, which follow as
later steps (specs/README.md).

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Wilson intervals, Newcombe intervals, Boschloo's p-values on the fixed grid and type 7 quantiles match committed reference values, ties and the extremes `k = 0` and `k = n` included, and Boschloo's p-value is never above Fisher's for the same table | `analysis.TestWilson`, `analysis.TestNewcombe`, `analysis.TestBoschloo`, `analysis.TestQuantileType7` |
| A bootstrap-t interval matches a reference implementation on skewed samples, is `[m, m]` for equal values, and redraws a resample whose standard error is 0 | `analysis.TestBootstrapT` |
| An order-statistic interval's ranks match the rule and its binomial coverage is at least `CONFIDENCE_LEVEL`; a median of 5 values and a `p90` of fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values carry no interval | `analysis.TestOrderStatisticInterval` |
| A permutation test with at most `PERMUTATION_EXACT_MAX` relabelings enumerates them all and matches a hand-computed p-value; above it, it draws `PERMUTATION_RESAMPLES` as resolved for its family and uses the `(1 + count) / (1 + B)` estimate | `analysis.TestPermutationExact`, `analysis.TestPermutationMonteCarlo` |
| `PERMUTATION_RESAMPLES` resolves to 10,000 for small families and to `ceil(20 · m / FALSE_DISCOVERY_RATE)` above them, and a lone true discovery in a family of 500 can pass | `analysis.TestResamplesGrowWithTheFamily` |
| A count metric is tested by permutation of its declared aggregate | `analysis.TestCountsUseTheirAggregate` |
| Equal data on both sides give `within_noise` for every method | `analysis.TestEqualSidesAreWithinNoise` |
| With `MIN_TRIALS_FOR_VERDICT - 1` valued trials on either side, every task-level verdict is `insufficient` and the result is in no family | `analysis.TestInsufficientBelowMinimum` |
| A suite-level result includes exactly the tasks with at least `MIN_TRIALS_PER_TASK_IN_SUITE` valued trials per side, is `insufficient` only below `MIN_SUITE_COVERAGE`, and lists every task left out with why | `analysis.TestCoverage` |
| A metric with direction `none` is never `improved` or `regressed`; a significant change is `changed`; a label result carries no verdict, `p` or `q` | `analysis.TestDirectionNone`, `analysis.TestLabelsAreDescriptive` |
| The verdict follows `q` and the delta's sign alone, whatever the interval shows, and every result carries a `reason` from the templates | `analysis.TestVerdictRule`, `analysis.TestReasons` |
| The primary metric's suite-level result has `q` equal to `p`; every other result is corrected in one family whose Benjamini and Hochberg values match a reference implementation; adding a candidate to an experiment leaves another candidate's `q` values unchanged | `analysis.TestPrimaryStandsAlone`, `analysis.TestBenjaminiHochberg`, `analysis.TestAddingACandidateChangesNoOtherVerdict` |
| A suite-level summary is the weighted mean of the per-task summaries over its included tasks; a weight-0 task is compared per task and absent from every suite-level figure | `analysis.TestSuiteWeights` |
| The advice sentence follows the comparison of within-cell and between-cell variance, which match hand-computed values | `analysis.TestAdvice` |
| Two models on one agent version give a single-variable comparison; a differing task hash is a tuple confound; a request served by another model in one task is a layer confound on that task's results with its request counts and leaves `single_variable` true; trials without a value add `missing` with counts | `analysis.TestModelComparisonIsSingleVariable`, `analysis.TestConfounds` |
| A declared variable outside the logical variable table, or naming `task_hash` or `grader_digests`, is refused | `analysis.TestVariableIsOneLogicalVariable` |
| A re-graded baseline is read at the candidate's grader and the grader confound disappears; a partly re-graded one is not | `analysis.TestReferenceReadAtCandidatesGrader` |
| The automatic comparison is recomputed in place with `final` false as trials finish, keeps its id, becomes `final` true once both sides are final, and never changes after | `analysis.TestAutomaticComparisonIsProgressive` |
| A final comparison recomputed from its recorded inputs is byte-identical, and its seeds are the recorded ones | `analysis.TestRecomputeFromRecordedInputs` |
| Every figure of the console's grid, verdict tables and cost table is read from a stored comparison or the stored analysis; the console bundle contains no statistical routine | `analysis.TestEveryShownFigureIsStored`, `console.TestNoStatisticsInTheClient` |
| Later steps: a golden comparison yields identical verdicts and figures agreeing to 12 significant digits on amd64 and arm64; a trend over a lineage of three baselines shows three points and two step comparisons; label tests | `analysis.TestGoldenAcrossArchitectures`, run on both in CI; `analysis.TestTrend` |
