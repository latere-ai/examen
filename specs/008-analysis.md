---
title: "Analysis: per-cell summaries and their intervals by metric type and level, Boschloo's exact test, studentized permutation tests with sequential stopping and the label tests, the stratified label test, the repeat rate, suite-level summaries with task weights and coverage, score intervals for weighted rates, the primary result, exploratory comparisons and the one corrected family, declared comparisons whose primary results are corrected together, the experiment-level test of independence, verdicts, reasons and the power floor, computed confounds and declared provenance, automatic and progressive comparisons, a benchmark's pair results over common tasks and simultaneous rank intervals, and trends, all computed deterministically and stored"
status: in-progress
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/005-execution.md
  - specs/006-grading.md
  - specs/007-measurements-and-evidence.md
affects: [internal/analysis/, internal/analysis/mathx/, internal/store/, api/]
effort: large
created: 2026-10-17
updated: 2026-10-19
author: changkun
---

# Analysis

## Overview

The analysis turns measurements into the statements Examen makes: what
a cell's trials amount to, how sure that is, and whether a candidate
changed a metric against a reference, per task and over the suite. It
fixes every summary, interval and test by name and formula, for
metrics of every type and of each level, `trial`, `cell` and
`candidate`, the correction that keeps many metrics and tasks from
producing chance findings, the comparisons and the test of
independence an experiment declares before it runs and how their
planned tests are corrected, the verdict rule and the plain reason each
verdict carries, how confounds are computed from the provenance tuple,
the provenance layers and the trials' times, and how two comparisons
of the same inputs are guaranteed to agree bit for bit.

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
parks until it is retried (spec 005). Excluded trials are counted and
named on every result they touch (`incomplete`, Confounds, below).

For a cell and a metric:

- `n` is the number of finished trials whose outcome the metric's
  `applies_to` names (spec 002); a finished trial of another outcome
  has no value of the metric, is counted as `not_applicable`, and is
  neither in `n` nor missing, so a line count asked only of completed
  trials is not inflated or deflated by the others;
- `valued` is the number of those with a current value for the metric
  (spec 007);
- `missing` is `n - valued`.

These are a summary's `n`, `denominator` and `missing` (spec 002), and
the `k` below is its `numerator`. An open-vocabulary metric is read as
the canonical label its raw label maps to at one taxonomy record (spec
006): the suite's current record when the figure is computed, or, for
a comparison a bar evaluation asks for, the record the bar pins (spec
009); the record is named in the comparison's `taxonomies` and in its
inputs document. A raw label the record does not map counts as a label
of its own: its normalized raw label stands in the distribution beside
the canonical labels, marked unmapped, and the summary's `unmapped`
counts its trials. It is never missing: a label the taxonomy has not
named yet is still an answer the judge gave, and reading it as missing
would drop exactly the answers nobody expected. A summary is computed
over the valued trials. A side of a task-level result has enough for a
verdict when `valued` is at least `MIN_TRIALS_FOR_VERDICT`.

Within a side, values are ordered by trial id, ascending. Every sum in
this spec runs in that order.

### Aggregation per cell

A metric's `aggregate` fits its type: `rate` for `binary`,
`distribution` for `label`, and `mean`, `median` or `p90` for `score`,
`count`, `duration` and `cost`; a cell-level metric's is its statistic,
and a candidate-level metric has none (Cell-level and candidate-level
metrics, below).

| Type | Aggregate | Cell summary | Interval at `CONFIDENCE_LEVEL` |
|---|---|---|---|
| `binary` | `rate` | `k / valued`, `k` the number of true values | Wilson score interval, clamped to [0, 1] |
| `score`, `count`, `duration`, `cost` | `mean` | the mean of the values | bootstrap-t interval; none when a quantile it needs is infinite |
| `score`, `count`, `duration`, `cost` | `median`, `p90` | the quantile of the values | order-statistic interval; none when no pair of ranks reaches the level, and none for a `p90` of fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values |
| `label` | `distribution` | the count and proportion of each declared label, or for an open vocabulary each canonical label and each unmapped raw label, and the mode (ties broken by the order of `labels`, or of the record's `canonical` and then of the raw labels bytewise) | Wilson score interval of each label's proportion |
| `score` of level `cell` | `repeat_rate` | the share of pairs of the cell's trials that hold one label, of its `of` metric | none (Cell-level and candidate-level metrics, below) |
| `score` of level `cell` | `mode_share` | the mode's count over the denominator, of its `of` metric | none: descriptive |
| `count` of level `cell` | `distinct_count` | the number of distinct labels among the cell's values of its `of` metric | none: descriptive |

For a numeric metric the summary also carries the mean, the sample
standard deviation `s` (divisor `valued - 1`), the minimum and the
maximum, whatever its aggregate. Throughout, `alpha = 1 -
CONFIDENCE_LEVEL` and `z = sqrt(2) * erfinv(CONFIDENCE_LEVEL)`,
1.959963984540054 at 0.95. A summary without an interval carries
`no_interval`, one sentence from the templates of Reasons, below.

**Quantiles.** The median is the quantile 0.5 and `p90` the quantile
0.9. The quantile `q` of sorted values `x(1) <= ... <= x(n)` is the
linear interpolation of Hyndman and Fan's type 7:

```
h = (n - 1) * q + 1
Q(q) = x(floor(h)) + (h - floor(h)) * (x(floor(h) + 1) - x(floor(h)))
```

with `x(n + 1)` read as `x(n)`. Every point estimate of a quantile, and
every quantile of resampled statistics in this spec, uses this
definition. Resampled statistics may be infinite (bootstrap-t, below):
when `h - floor(h)` is 0, `Q(q)` is `x(floor(h))`; otherwise, when
either of the two values it reads is infinite, `Q(q)` is that infinite
value, and when they are infinite with opposite signs `Q(q)` is
undefined. An interval that needs an infinite or undefined quantile is
not shown.

**Wilson score interval.** With `p = k / n`:

```
center = (p + z^2 / (2n)) / (1 + z^2 / n)
half   = (z / (1 + z^2 / n)) * sqrt(p (1 - p) / n + z^2 / (4 n^2))
interval = [max(0, center - half), min(1, center + half)]
```

The bounds lie in [0, 1] in exact arithmetic. In binary64 the lower
bound of `k = 0` may round to a tiny value on either side of 0 (0 of 5
gives exactly 0, 0 of 7 about +2.8e-17), and the upper bound of `k = n`
likewise around 1: the clamp removes a rounding outside [0, 1], and a
bound of `k = 0` or `k = n` is set to exactly 0 or 1.

**Bootstrap-t interval of a mean.** With `m` the mean of the `n`
valued values and `se = s / sqrt(n)`: each of `BOOTSTRAP_RESAMPLES`
resamples draws `n` values with replacement, and has its own mean
`m*`, its own `se*` and its own `t*`:

```
t* = (m* - m) / se*      when se* > 0
t* = +infinity           when se* = 0 and m* > m
t* = -infinity           when se* = 0 and m* < m
t* = 0                   when se* = 0 and m* = m
```

A resample is never drawn again. One whose values are all equal has no
spread, and keeping it, with an infinite `t*`, keeps the resamples
that show how often the cell's few large values are absent; drawing it
again would leave them out and narrow the interval exactly where it
knows least. With `Q*` the quantile of the `t*` values:

```
interval = [m - Q*(1 - alpha / 2) * se, m - Q*(alpha / 2) * se]
```

When a quantile it needs is infinite or undefined, the summary has no
interval, and its `no_interval` gives the count of resamples without
spread. When every value is equal, `se` is 0 and the interval is
`[m, m]`, with no resampling. The studentized interval is used because
a cell is small and its costs, durations and counts are skewed by the
occasional runaway trial, where a percentile interval of the mean is
too narrow.

**Order-statistic interval of a quantile.** Let `B` be the number of
values below the true quantile `q`, which follows the binomial
distribution of `n` trials and probability `q` whatever the values'
own distribution. The interval `[x(l), x(u)]` then covers the true
quantile with probability at least

```
cover(l, u) = sum over i from l to u - 1 of C(n, i) * q^i * (1 - q)^(n - i)
```

The ranks are the pair `1 <= l < u <= n` with `cover(l, u)` at least
the level asked, `CONFIDENCE_LEVEL` for a summary, and the smallest
`u - l`; among equal pairs, the one whose two tails, `P(B < l)` and
`P(B >= u)`, differ least; then the smallest `l`. When no pair reaches
the level the summary has no interval: at 0.95 a median needs 6
values, where the rule picks `[x(1), x(6)]` with coverage 31/32. A
`p90` shows none below `MIN_TRIALS_FOR_P90_INTERVAL` values: even
`[x(1), x(n)]` misses the true 90th percentile whenever every value
lies below it, with probability `0.9^n`, which is 0.052 at 28 values
and 0.047 at 29, where the rule picks `[x(21), x(29)]` with coverage
0.9513. At the default `CONFIDENCE_LEVEL` the constant states the pair
rule's own threshold, so that a console can name it and the suite-level
rule can read it (below). Tied values make the coverage larger, never
smaller.

### Tests per task

A task-level result compares the candidate's cell `x` with the
reference's cell `y` for one task and one metric. Its **delta** is
oriented candidate minus reference. Here `n_x` and `n_y` are the two
sides' `valued`, and `N = n_x + n_y`.

| Type | `delta` (`delta_kind`) | Delta interval | Test (`method`, `statistic`) |
|---|---|---|---|
| `binary` | `k_x / n_x - k_y / n_y` (`difference`) | Newcombe's hybrid score interval, clamped to [-1, 1] | `boschloo_exact`, `fisher_p`; two-sided |
| `score`, `count`, `duration`, `cost` with aggregate `mean` | `mean(x) - mean(y)` (`difference`) | bootstrap-t interval of the difference; none when a quantile it needs is infinite | `permutation`, `welch_t` |
| `score`, `count`, `duration`, `cost` with aggregate `median` or `p90` | `Q(x) - Q(y)` (`difference`) | the difference of two order-statistic intervals at the level `sqrt(CONFIDENCE_LEVEL)`; none when either side has none at that level | `permutation`, `studentized_quantile` |
| `label` | per label `l`, `p_x(l) - p_y(l)` (`per_label`) | Newcombe's hybrid score interval per label | `fisher_freeman_halton` with `table_probability`, or `chi_square_permutation` with `pearson_x2` when its tables are too many (Label metrics, below) |
| `repeat_rate` (level `cell`) | `R(x) - R(y)` (`difference`) | none | `permutation`, `difference` |
| `mode_share`, `distinct_count` (level `cell`) | `A(x) - A(y)` (`difference`), `A` the statistic | none | none: descriptive (Cell-level and candidate-level metrics, below) |

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
statistic, `fisher_p`, and is reported nowhere as a test of its own.
Barnard's test with a numeric search for the maximum is rejected: two
searches can stop at different points and answer different p-values
for one table.

**Newcombe's hybrid score interval.** With `p1 = k_x / n_x`,
`p2 = k_y / n_y` and their clamped Wilson intervals `[l1, u1]` and
`[l2, u2]`:

```
lower = max(-1, (p1 - p2) - sqrt((p1 - l1)^2 + (u2 - p2)^2))
upper = min( 1, (p1 - p2) + sqrt((u1 - p1)^2 + (p2 - l2)^2))
```

**Bootstrap-t interval of a difference of means.** With
`d = mean(x) - mean(y)` and `se = sqrt(s_x^2 / n_x + s_y^2 / n_y)`:
each of `BOOTSTRAP_RESAMPLES` resamples redraws each side from its own
values, `n_x` and `n_y` with replacement, the candidate side first, and
has its own `d*`, `se*` and `t*`, by the rule of the bootstrap-t
interval of a mean with `d*` and `d` in place of `m*` and `m`; no
resample is drawn again. The interval is
`[d - Q*(1 - alpha / 2) * se, d - Q*(alpha / 2) * se]`, absent when a
quantile it needs is infinite or undefined. When every value of both
sides is equal, the interval is `[d, d]`.

**Difference of two quantiles.** Each side's order-statistic interval
is taken at the level `sqrt(CONFIDENCE_LEVEL)`, 0.974679 at the
default, `[l_x, u_x]` and `[l_y, u_y]`. The sides are independent, so
both cover together with probability at least `CONFIDENCE_LEVEL`, and
then the difference of the true quantiles lies in
`[l_x - u_y, u_x - l_y]`, which is the delta's interval. It is
distribution-free and errs wide. At the default level a side needs 7
values for a median, where `[x(1), x(7)]` covers with probability
63/64, and 35 for a `p90`, where the rule picks `[x(24), x(35)]` with
coverage 0.9749; with fewer on either side the delta has no interval.

**Permutation tests.** The pooled values are `x` followed by `y`. A
relabeling chooses which `n_x` of the `N` pooled values form the
candidate side. Each relabeling recomputes the statistic `T` from the
relabeled sides, and the discrepancy is `S = abs(T)`. The statistic
fits the aggregate, so that the test asks about the figure the summary
shows, and so that unequal trial counts and spreads on the two sides
keep the test at its level:

- **`welch_t`, for a mean.** Welch's statistic

  ```
  T = (mean(x) - mean(y)) / sqrt(s_x^2 / n_x + s_y^2 / n_y)
  ```

  with `s^2` the sample variance, divisor `n - 1`. A permutation test
  of the bare difference of means assumes the two sides spread alike:
  with 50 trials of spread 1 against 5 of spread 10 and equal means, it
  calls a difference at 5% in about half of such experiments. Welch's
  statistic holds the level asymptotically whatever the spreads, and at
  those sizes brings the rate to about 6.5%.
- **`studentized_quantile`, for a median or a `p90`.** Chung and
  Romano's studentized difference of quantiles at `q`, 0.5 or 0.9:

  ```
  T = (Q_x(q) - Q_y(q)) / sqrt(q (1 - q) (r_x^2 / n_x + r_y^2 / n_y))
  ```

  where `r` estimates a side's sparsity `1 / f(F^-1(q))`, the
  reciprocal of its density at the quantile, by Siddiqui's difference
  quotient with Hall and Sheather's bandwidth:

  ```
  u = sqrt(2) * erfinv(2q - 1)
  b = n^(-1/3) * z^(2/3) * (1.5 * phi(u)^2 / (2 u^2 + 1))^(1/3)
  r = (Q(min(1, q + b)) - Q(max(0, q - b))) / (min(1, q + b) - max(0, q - b))
  ```

  `phi(u) = exp(-u^2 / 2) / sqrt(2 pi)` is the standard normal density
  and `Q` the type 7 quantile of the side's values. `b` depends on `n`
  and `q` alone, so each side's is computed once; `Q` and `r` are
  recomputed per relabeling. A permutation test studentized by any
  consistent estimate of each side's variance holds its level
  asymptotically when the two sides' distributions differ in spread
  (Chung and Romano, 2013); this estimate reads order statistics alone
  and draws nothing.
- **`difference`, for a repeat rate.** `T = R(x) - R(y)`, the
  relabeled values being the cells' labels after `exclude_labels`. The
  test asks whether the two cells' label distributions differ, under
  which hypothesis every relabeling is equally likely and the test is
  exact; how often a cell's trials repeat each other is how such a
  difference shows in this statistic.
- **A zero denominator.** When the denominator of a studentized
  statistic is 0, as when each relabeled side holds one distinct
  value, `T` is 0 when its numerator is 0, and plus or minus infinity
  by the numerator's sign otherwise.

The p-value:

- **Exact** when `C(N, n_x)` is at most `PERMUTATION_EXACT_MAX`: every
  relabeling is enumerated and
  `p = #{relabelings with S >= S_obs} / C(N, n_x)`, the observed
  labeling included.
- **Monte Carlo** otherwise, with Besag and Clifford's sequential
  stopping: relabelings are drawn one at a time by shuffling the pooled
  values (Determinism, below), the first `n_x` forming the candidate
  side, and `c` counts the draws with `S >= S_obs`. When `c` reaches
  `PERMUTATION_STOP_EXCEEDANCES` at the `L`-th draw, drawing stops and
  `p = PERMUTATION_STOP_EXCEEDANCES / L`; when `B` draws pass with `c`
  below it, `p = (1 + c) / (1 + B)`. Either p-value is valid: the
  chance that it is at most `u` is at most `u` at every value it can
  take. A result far from significant stops after about
  `PERMUTATION_STOP_EXCEEDANCES / p` draws, and one that may be
  significant draws all `B`, so stopping changes how long a test takes
  and never what it can find. `resamples` records `L` or `B`.

`a >= b` in these counts holds when `b` is finite and
`a >= b - TIE_TOLERANCE * max(1, abs(b))`, or when `b` and `a` are both
positive infinity, so a relabeling that reproduces the observed
discrepancy is counted even when floating-point rounding puts it a hair
below.

**How many relabelings.** `B` is `PERMUTATION_RESAMPLES`: the larger of
10,000 and `ceil(20 * m / FALSE_DISCOVERY_RATE)`, where `m` is the
number of p-values in the test's family: the corrected family's size
for every result in it, and 1 for the primary metric's suite-level
result of a comparison that is not exploratory (spec 002); a
benchmark's results table resolves it with its own family and level
(Benchmark entries, below). A Monte Carlo p-value is at least
`1 / (1 + B)`, and a lone discovery in a family of `m` passes Benjamini
and Hochberg's procedure only with `p` at most
`FALSE_DISCOVERY_RATE / m`. At this `B` the smallest p-value a test can
give is about a twentieth of that, so the number of draws never decides
whether a real difference can be found. `m` is known before any test
runs, since whether a result enters the family depends on its counts,
its values' provenance and its comparison alone.

**The power floor.** A result can be significant only with `p` at most
its threshold: `FALSE_DISCOVERY_RATE / m` in a family of `m`, the most
Benjamini and Hochberg's procedure grants a lone finding;
`FALSE_DISCOVERY_RATE` for a primary result tested alone; and
`(1 - CONFIDENCE_LEVEL) / m` for a benchmark's pair result, Holm's
first threshold. An exact test has a smallest p-value its sizes allow,
whatever the data:

- Boschloo's test: the p-value of the table `a = n_x`, `c = 0`, every
  candidate trial true and every reference trial false, on the fixed
  grid;
- an exact permutation test: `1 / C(N, n_x)`, and `2 / C(N, n_x)` when
  `n_x = n_y`, since swapping the two sides gives the same `S`;
- an exact stratified permutation test: `1 / prod_t C(n_xt + n_yt,
  n_xt)`, doubled when every task's two sides are of one size; for the
  stratified label test of more than two sides, `1 / prod_t M_t` times
  the orderings of the sides that keep every task's sizes (The
  stratified label test, below).

When that floor is above the result's threshold, no outcome of its
trials could be a finding, and its `reason` is the power-floor template
(Reasons, below) whatever its `p`. Its verdict still follows the rule:
Benjamini and Hochberg's procedure grants the `i`-th smallest p-value
`FALSE_DISCOVERY_RATE * i / m`, more than the lone finding's threshold,
so a result whose floor exceeds `FALSE_DISCOVERY_RATE / m` can still be
a finding when several results of its family are small together; the
reason then states both, the floor and the rank that made it a finding. A Monte Carlo test's floor, `1 / (1 + B)`, is below
every threshold by the choice of `B`.

**Counts.** Counts of tokens, turns and tool calls are tested like any
numeric metric: by the permutation test of the statistic of the
aggregate the metric declares, so the test asks about the same figure
the summary shows. A runaway trial moves a mean; a metric that should
not be moved by one declares `median`.

### Label metrics

A label result carries, per label, the difference of the two sides'
proportions with Newcombe's interval, and at suite level the difference
of the weighted proportions with Agresti and Caffo's interval (below).
These per-label figures are descriptive: the verdict is one test of
whether the two distributions differ, so many labels never make many
chances at a finding. Label tests are built from v0.5.0, and every
comparison computed since carries the tests below, a provisional one of
an earlier release included when it is next computed; a comparison
final before then keeps its label results descriptive, with no `p`,
`q` or `verdict` and no part in the family. A significant difference in
a label metric, whose direction is `none`, is `changed`.

**The task-level test.** The table is the 2 by k table of label
counts, the candidate's row and the reference's, over the labels with a
count on either side, an unmapped raw label among them as a label of
its own; `n_x` and `n_y` are its row totals, `c_l` its column totals,
`N = n_x + n_y`. Under the null hypothesis both sides share one
distribution, and given the margins the probability of a table whose
candidate row is `(a_1 … a_k)` is the multivariate hypergeometric
`P(a) = prod_l C(c_l, a_l) / C(N, n_x)`, in log space with the
log-gamma function.

- **Fisher, Freeman and Halton's exact test** (`method`
  `fisher_freeman_halton`, `statistic` `table_probability`): every
  table with the observed margins is enumerated, and
  `p = sum over tables a with P(a) <= P(observed) * (1 + TIE_TOLERANCE) of P(a)`.
  It is used when the number of such tables, counted before any is
  enumerated by a recurrence over the columns, is at most
  `PERMUTATION_EXACT_MAX`; `exact` is true and `resamples` is that
  number.
- **Otherwise, by permutation of the chi-square statistic**
  (`chi_square_permutation`, `pearson_x2`): the discrepancy is
  Pearson's `X^2 = sum (O - E)^2 / E` over the table's cells,
  `E = row total * column total / N`, and the p-value is the Monte
  Carlo one of a permutation test (above), with sequential stopping,
  drawn from the result's seed, so the same inputs always give the same
  p-value. Its ordering of tables is Pearson's, not the probability the
  exact test orders by, and the `method` says which ran.
- A table with fewer than two labels, every trial of both sides holding
  one label, has `p = 1` and is `within_noise`.

**The suite-level test** is the stratified label test, below, of the
two sides over the included tasks (Suite-level summaries, below). When
the primary metric is a label metric, it is the primary result of a
comparison.

### The stratified label test

One test of whether a label metric depends on which side a trial is
on, over several tasks at once, for any number of sides `K` of at
least two: the two sides of a comparison's suite-level label result
(`K = 2`), and every candidate of an experiment for the experiment's
test of independence (below). Its `method` is
`stratified_permutation` and its `statistic` `pearson_x2_sum`.

For each included task `t`, the table is the `K` by `k_t` table of
label counts over the labels with a count on any side in that task, an
unmapped raw label among them as a label of its own: `O_tsl` the
trials of side `s` holding label `l`, `n_ts` its row totals, `c_tl` its
column totals, `N_t` its trials. The statistic is

```
X^2   = sum over the included tasks t of X^2_t
X^2_t = sum over sides s and labels l of (O_tsl - E_tsl)^2 / E_tsl,
        E_tsl = n_ts * c_tl / N_t
```

summed in a fixed order: tasks in ascending bytewise order of name,
within a task side by side in the sides' order (a comparison's
candidate side first; an experiment's candidates in its order), and
within a side label by label in the result's label order, each squared
difference rounded before its division. A task whose trials all hold one label adds 0 under every
relabeling. The discrepancy is `X^2`.

A relabeling permutes each included task's trials among its sides
separately, keeping every side's `n_ts`. Under the null hypothesis
that on every included task the sides share one distribution of the
label, each task's trials are exchangeable, so every relabeling is
equally likely and the test is exact whatever the statistic.

- **Exact** when the product over tasks of `M_t`, the number of a
  task's relabelings, `N_t! / prod_s n_ts!`, is at most
  `PERMUTATION_EXACT_MAX`: every combination of the tasks' relabelings
  is enumerated, a task's relabelings by choosing the first side's
  trials among all of the task's in lexicographic order of position,
  then the second side's among the rest, and so on, and the first
  task's varying slowest, and `p` is the share of them whose `X^2` is at
  least the observed one, the observed labeling included.
- **Monte Carlo** otherwise, with sequential stopping as for any
  permutation test (Tests per task, above), drawn from the result's
  seed: each draw shuffles, for each task in the order above, a fresh
  copy of the task's pooled values, the sides' values in the sides'
  order and each side's in ascending order of trial id, and gives the
  first `n_t1` to the first side, the next `n_t2` to the second, and so
  on. `B` is `PERMUTATION_RESAMPLES` resolved for the result's family.
- Its smallest p-value, when exact, is `1 / prod_t M_t` times the
  number of orderings of the sides that leave every side's size the
  same in every task: `2 / prod_t M_t` for two sides of one size in
  every task, since swapping them gives the same `X^2` (The power floor,
  above).

**Why the summed chi-square.** The statistic sums what each task shows,
so it has power against a difference on any one task, and against
differences in opposite directions on two tasks, which cancel in the
weighted proportions a label result reports: a candidate that answers
`a` on one task where the reference answers `b`, and `b` on another
where the reference answers `a`, has the reference's weighted
distribution exactly, and a test of that distribution's difference
would find nothing (Worked examples, below). The price is that a
task's `X^2_t` grows with its trials, so unlike every other
suite-level test this one does not weigh a task by the suite's weight:
a weight decides only whether a task enters, by being above zero.
That departs from the rule that fixes the other suite-level tests
(Suite-level summaries, below) for a reason: a rate, a mean or a
quantile reports one weighted figure, and its test asks about that
figure; a label result reports per-label differences, which are
descriptive, and its verdict is one test of whether the distributions
differ at all, so there is no single weighted figure for it to match.
When every included task holds as many valued trials per side, as a
declared N gives unless trials fail, and every included task weighs
the same, the summed chi-square is a fixed multiple of the
weight-normalized one, and both give the same p-value. The stratified
permutation of a distance between the two sides' weighted distributions
is rejected: it tests the weighted figures and so finds nothing where
differences cancel.

### Cell-level and candidate-level metrics

**A cell-level metric** is a statistic of a cell's values of its `of`
label metric, computed by Examen and never measured (spec 002). Each
leaves out the trials whose label is one of the metric's
`exclude_labels`, `declined` and `none` unless it names others, so a
cell whose trials declined is not read as agreeing on one answer; the
summary counts them in `excluded_labels`. `n` and `missing` are those of
the `of` metric, and `denominator`, `d` here, is its valued trials less
the excluded ones. A cell left with no trial, or for a repeat rate with
fewer than two, has no value: it is missing.

- **`repeat_rate`**, the tested one. With `k_l` the trials holding
  label `l`:

  ```
  R = sum over labels l of k_l (k_l - 1) / (d (d - 1))
  ```

  the share of the cell's `d (d - 1) / 2` pairs of trials that hold one
  label. It is an unbiased estimate of the probability that two of the
  cell's trials agree, at every `d`, so cells of different sizes, a
  baseline of 5 trials against a candidate of 50 among them, compare
  one quantity. **Test:** the permutation test above with the statistic
  `difference`, exact or Monte Carlo as there. **Interval:** none, for
  the summary and for the delta: a bootstrap resample draws trials with
  replacement, and a trial drawn twice agrees with its own copy, so a
  resampled repeat rate is biased upward, and no distribution-free
  interval for it is fixed here; `no_interval` and `no_delta_interval`
  say so. **Suite level:** the weighted mean of the per-task repeat
  rates, tested by the stratified permutation with the statistic
  `difference`, with no interval. A repeat-rate result has a verdict by
  the metric's direction like any other, and is in the family.
- **`mode_share`** and **`distinct_count`**, descriptive only.
  `mode_share` is the trials holding the cell's most frequent label
  over `d`, reported as its `numerator` over its `denominator` as a
  rate is; a tie for the mode leaves it unchanged. `distinct_count` is
  the number of distinct labels among the `d` trials. Both depend on
  `d`: of trials spread evenly over 20 labels, the expected mode share
  is 0.2885 at 5 trials and 0.1176 at 49, while the expected repeat
  rate is 0.05 at both. So neither has an interval, a test or a
  verdict. A comparison holds their task-level and suite-level results
  with both sides' summaries, their denominators and the delta, as
  descriptive results with no `p`, `q` or `verdict`, outside the
  family, `verdict_counts` and a headline. Their suite-level summary is
  the weighted mean of the per-task statistics, descriptive too.

**A candidate-level metric** has one value per candidate of an
experiment, from a candidate-level judge (spec 006). One value has no
spread to test against, so its result is descriptive: one `candidate`
result per comparison carrying both sides' values, the `delta` of a
numeric metric, and no interval, `p`, `q` or `verdict`; it takes no
part in the family, in `verdict_counts` or in a headline.

### Tasks are fixed

Every interval and test in this spec treats the suite's tasks as fixed:
the question is whether the candidate changed what the agent does on
these tasks, so only within-cell variance, the trial-to-trial spread in
one fully pinned cell, enters an interval or a test. A claim about
tasks outside the suite needs a model of how tasks are sampled, which
Examen does not make. No figure or sentence of the analysis weighs more
trials against more tasks.

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
studentized statistics need, and a repeat rate exist; the stratified
test holds its level with a task of few trials, which adds little and
biases nothing, so the stricter `MIN_TRIALS_FOR_VERDICT` guards only a
task's own verdict. `coverage` is the share of the total weight of the
candidate's suite tasks of weight above zero that the included tasks
hold. Every task of weight above zero that is not included is listed in
the result's `left_out`, as `unpaired` or `too_few_trials` with each
side's valued trials.

Over the included tasks `T`, with weights `w_t`, valued trials `n_t`
and `W = sum w_t`:

| Type | Suite-level summary `S` |
|---|---|
| `binary`, numeric, and every cell-level metric | `S = sum_t w_t s_t / W`, the weighted mean of the per-task summaries: of rates for a rate, of means, medians or 90th percentiles for those aggregates, and of the statistic for a cell-level metric |
| `label` | the weighted mean of the per-task proportion vectors |

So a task with more valued trials does not count for more than its
weight.

**The interval of `S`:**

- **A rate: Wilson's interval on Kish's effective sample size.**

  ```
  n_eff = W^2 / sum_t (w_t^2 / n_t)
  interval = the clamped Wilson interval with p = S and n = n_eff
  ```

  Each trial of task `t` carries the weight `w_t / n_t` in `S`, and
  Kish's `n_eff`, the square of the weights' sum over the sum of their
  squares, is the number of equally weighted trials whose rate would be
  as precise as `S`; it is the number of trials when every task weighs
  the same and holds as many. A rate never uses the bootstrap-t: at a
  rate of 0 or 1 every resample of a task has no spread, and a
  stratified bootstrap-t interval of a candidate at 50 of 50 over ten
  tasks is `[1, 1]`, where Wilson's is `[0.9287, 1]`.
- **A mean: the stratified bootstrap-t.** Each of `BOOTSTRAP_RESAMPLES`
  resamples redraws every included task's values from that task's cell
  alone, `n_t` with replacement, and recomputes `S` and
  `se = sqrt(sum_t w_t^2 v_t / n_t) / W`, `v_t` the task's sample
  variance; `t*` follows the rule of the bootstrap-t interval of a mean,
  a resample whose `se*` is 0 kept with an infinite `t*`, and the
  interval is absent when a quantile it needs is infinite or undefined.
- **A median or a `p90`:** the percentile interval
  `[Q(alpha / 2), Q(1 - alpha / 2)]` of the stratified-resampled `S`,
  since no distribution-free interval exists for a weighted sum of
  quantiles; a `p90` has none when any included task has fewer than
  `MIN_TRIALS_FOR_P90_INTERVAL` valued trials.
- **A label metric:** per label, Wilson's interval on `n_eff` of that
  label's weighted proportion.
- **A cell-level metric:** none.

**The interval of a suite-level delta**, the candidate side first in
every resample:

- **A rate: Agresti and Caffo's interval**, which adds one success and
  one failure to each side of each task's cell:

  ```
  p~_t     = (k_t + 1) / (n_t + 2)                  per side and task
  D~       = sum_t w_t (p~_xt - p~_yt) / W
  V~       = sum_t w_t^2 (p~_xt (1 - p~_xt) / (n_xt + 2)
                        + p~_yt (1 - p~_yt) / (n_yt + 2)) / W^2
  interval = [max(-1, D~ - z sqrt(V~)), min(1, D~ + z sqrt(V~))]
  ```

  The delta reported is `S_x - S_y`; its interval is centered at the
  adjusted `D~`, as Agresti and Caffo's interval is.
- **A mean:** the stratified bootstrap-t of `S_x - S_y`, with
  `se = sqrt(se_x^2 + se_y^2)`.
- **A median or a `p90`:** the percentile interval of the resampled
  difference.
- **A label metric:** per label, Agresti and Caffo's interval of that
  label's weighted proportions.
- **A cell-level metric:** none.

**The test: stratified permutation** (`method`
`stratified_permutation`). A relabeling permutes the labels within each
included task separately, keeping each task's `n_x` and `n_y`. The
statistic:

- `binary` and `repeat_rate`: `difference`, `T = S_x - S_y`. For a rate
  the test is exact under the hypothesis that each task's two cells
  share one rate, under which a task's trials are exchangeable;
- `mean`: `welch_t`, `T = (S_x - S_y) / sqrt(se_x^2 + se_y^2)`, with
  each task's variance recomputed per relabeling;
- `median`, `p90`: `studentized_quantile`,
  `T = (S_x - S_y) / sqrt(sum_t (w_t / W)^2 q (1 - q) (r_xt^2 / n_xt + r_yt^2 / n_yt))`,
  each `r` as per task;
- `label`: `pearson_x2_sum`, the stratified label test (above), whose
  relabelings, enumeration and draws are as defined there.

The discrepancy is `abs(T)`. The test is exact when the product over
tasks of `C(n_x + n_y, n_x)` is at most `PERMUTATION_EXACT_MAX`, and
Monte Carlo with sequential stopping otherwise, with the p-values
defined as per task. Every suite-level `delta_kind` of a numeric or
binary metric is `difference`. For a rate, a mean and a quantile, the
Cochran, Mantel and Haenszel test and van Elteren's stratified rank
test are rejected: each weighs a task by its number of trials, not by
the weight the suite declares, so its p-value would test a different
summary than the one reported. A label result reports no single
weighted summary for its test to match, and its stratified label test
sums each task's chi-square as the task holds it (The stratified label
test, above).

In a comparison, the tasks considered are the paired tasks
(Comparisons, below) of weight greater than zero, weighted by the
candidate's suite. A candidate's own suite-level summary in one
experiment, without a reference, is over its suite's tasks with weight
greater than zero that it holds enough valued trials of.

### Verdicts

Each result gets one by this rule, in this order:

1. `insufficient` when a task-level result has a side whose `valued`
   is below `MIN_TRIALS_FOR_VERDICT`, or a suite-level result's
   `coverage` is below `MIN_SUITE_COVERAGE`. An insufficient result has
   no p-value and takes no part in the family. A suite-level result of
   enough coverage is tested over the tasks it includes, and names the
   others in `left_out`.
2. No verdict on a descriptive result: a `candidate` result, a
   `mode_share` or `distinct_count` result, a result that reads
   self-reported values (Multiple comparisons, below), and a `label`
   result before label tests are built. A declared comparison's primary
   result has no verdict in the comparison: the experiment's declared
   analysis gives it one, by this rule, from its `q` there (Multiple
   comparisons, below).
3. For a metric whose `direction` is `none`: `changed` when the
   corrected value `q` is at most `FALSE_DISCOVERY_RATE`, and
   `within_noise` otherwise.
4. For a metric with a direction: when `q` is at most
   `FALSE_DISCOVERY_RATE`, `improved` if the sign of the delta is the
   metric's direction (positive for `higher_is_better`, negative for
   `lower_is_better`) and `regressed` if it is the opposite;
   `within_noise` otherwise.

A delta of zero has no direction and is always `within_noise`, except
on a label result: its test reads each task's table, and two sides can
differ on every task while their weighted per-label differences are all
zero (The stratified label test, above), so a label result is
`changed` whenever its `q` is at most `FALSE_DISCOVERY_RATE`, and its
reason says no difference only when every table it reads gives the two
sides the same proportions.

The verdict comes from the test alone. The delta's interval is
descriptive: it is not corrected for multiple comparisons and is built
by a different method than the test (Newcombe's and Agresti and
Caffo's against Boschloo's and the permutation, bootstrap-t and order
statistics against studentized permutation). A row can therefore show
`improved` with an interval that touches zero, or `within_noise` with
one that does not, and its `reason` says what decided it. A console
shows `within_noise` as "no clear difference", with the delta's
interval and the counts (spec 010).

### Reasons

Every result stores `reason`, one plain sentence that says why its
verdict is what it is, built from fixed templates with numbers in spec
010's formats:

| Case | `reason` |
|---|---|
| a task-level side below the minimum | "task t3 has 3 trials with a value; 5 needed" |
| a suite-level result below coverage | "covers 70% of the suite's weight; 80% needed; left out: t3 (3 trials), t7 (not run by the reference)" |
| the primary metric's suite-level result | "p < 0.001 for the primary metric, tested alone without correction" |
| the primary metric's suite-level result of an exploratory comparison | "p = 0.012; q = 0.31 after correcting 41 results at 5%; exploratory, so the primary metric is corrected with the rest" |
| a result of the family | "p = 0.048; q = 0.19 after correcting 40 results at 5%" |
| a result whose exact test cannot reach its threshold | "with 5 trials per side, no difference can be shown among 40 results"; with sides of two sizes, "with 5 and 7 trials, no difference can be shown among 40 results"; tested alone, "with 2 trials per side, no difference can be shown at 5%"; at suite level, "with 3 tasks of 2 trials per side, no difference can be shown among 40 results" |
| a delta of zero | "no difference between the two sides" |
| a result that reads self-reported values | "p = 0.03 on values the external harness reported itself: not a finding, and outside the corrected family" |
| a declared comparison's primary result | "p = 0.012 for the primary metric of a declared comparison; corrected with the primary results of 11 declared comparisons in the experiment's declared analysis" |
| a declared comparison's primary result whose exact test cannot reach `FALSE_DISCOVERY_RATE / D` | "with 5 trials per side, no difference can be shown among 11 declared comparisons" |
| a declared comparison in the declared analysis | "p = 0.012; q = 0.044 after correcting the primary results of 11 declared comparisons at 5%"; one not computed yet, "not computed yet: the candidate has no finished trial"; one whose newest comparison failed, "not computed: the comparison could not be computed"; otherwise its comparison's own reason |
| the experiment's test of independence | "p = 0.004 for the experiment's declared test of independence, tested alone without correction"; with a power floor, "with 4 tasks of 5 trials per candidate, no difference can be shown at 5%"; a statistic of 0, "no difference between the candidates"; before its first computation, "not computed yet: no cell has a finished trial" |
| a mode share | "descriptive: a mode share depends on the number of trials, so it is not tested" |
| a count of distinct labels | "descriptive: a count of distinct labels depends on the number of trials, so it is not tested" |
| a label result, before label tests are built | "descriptive: label tests are not built yet" |
| a candidate-level result | "descriptive: one value per candidate, so nothing to test" |
| a pair result of two benchmark entries | "p = 0.03 unadjusted; h = 0.06 after Holm's adjustment over 3 pairs at 5%" |
| a pair result with an unranked entry | "entry sbm_01jg… is not ranked: covers 60% of the suite's weight; 80% needed" |

A result whose confounds hold `missing` adds the counts, such as
"; 2 of 20 candidate trials have no value"; `incomplete` adds them,
such as "; 2 of 20 candidate trials were left out: failed to run,
canceled or over budget"; `window` adds the gap, such as "; the two
sides' time windows differ by 41 days"; and `uncalibrated` adds
"; labels from a judge not yet calibrated against people". The console
shows `reason` beside every verdict.

A summary without an interval stores `no_interval`, and a result
without a delta interval, unless insufficient, `no_delta_interval`:

| Case | Sentence |
|---|---|
| a `p90` below the minimum | "no interval below 29 values for a 90th percentile" |
| a quantile whose ranks reach no pair | "no interval: 5 values cannot bound a median at 95%" |
| a delta of two quantiles | "no interval: each side needs 7 values for a median at this level" (35 for a 90th percentile) |
| a bootstrap-t quantile that is infinite | "no interval: 3,301 of 10,000 resamples had no spread" |
| a suite-level `p90` with a small task | "no interval: task t3 has 12 values; a 90th percentile needs 29" |
| a repeat rate | "no interval: resampling repeats trials, which inflates a repeat rate" |
| a mode share or a count of distinct labels | "descriptive: no interval" |
| a candidate-level result | "descriptive: one value per candidate" |

### Multiple comparisons

A comparison's p-values are read in two parts:

- **The primary result.** In a comparison that is not exploratory, the
  suite-level result of the suite's `primary_metric` is one
  pre-declared test: the suite names the metric and the experiment its
  reference before any trial runs, and the comparison exists to answer
  it. It is tested alone at `FALSE_DISCOVERY_RATE` and reported without
  correction, so its `q` equals its `p`. A declared comparison's
  primary result is corrected with the other declared comparisons'
  instead (Declared comparisons, below).
- **The family.** Every other result of the comparison that has a
  p-value, the task-level results of every metric, the primary
  metric's included, and the suite-level results of every other
  metric, is one family, corrected together. In an exploratory
  comparison the primary metric's suite-level result is in the family
  too, and `correction.primary` is absent.

A result that reads self-reported values keeps its `p`, carries no `q`
and no verdict, and stays outside the family: the external harness's own claim
is not a measurement Examen made, and it must not change what the
correction allows any other result.

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

**Exploratory comparisons.** A comparison computed on request, and an
automatic comparison against a reference the experiment set after a
cell left `pending`, were not declared before their trials ran: whoever
asked may have looked at the results first, and a primary result chosen
after looking is a pick among many. Such a comparison is `exploratory`:
its primary metric is corrected with the rest, and it is never
headlined (spec 010). Only the automatic comparison against the
reference declared before the experiment ran carries the uncorrected
primary result.

Why the family is one comparison, and not one experiment: a comparison
is what a bar reads (spec 009) and what a report headlines (spec 010),
and its verdicts must not move when another candidate is added to the
same experiment. With one family per experiment, adding a candidate
would change every other candidate's corrected values.

Why the declared primary result stands alone: it is the figure a person
reads first and a release bar usually rests on (spec 009), and it was
declared before any trial ran, so testing it alone is one planned test,
not a pick among many. Correcting it together with hundreds of
task-level results would make the result a release rests on depend on
how many tasks and metrics the suite happens to list.

**Declared comparisons.** An experiment may declare, when it is
created, the comparisons it will make (spec 002, `comparisons`): pairs
of its candidates, each optionally with a variable, pairs of tasks for
one of its candidates, the comparison across tasks of Confounds and the
variable, below, and comparisons of the general form, which name a cell
variable and two of its levels (Comparisons over cell variables,
below). Each is computed without being asked, as the automatic
comparison is, progressively and a last time when both sides are final
(Comparisons, below), with `declared` its position in the experiment's
list and `exploratory` false. Each may name its own `metric`, whose
result is then its primary result in place of the suite's primary
metric's. A declared comparison is read like any other, with one
difference: its primary result, the suite-level result of its metric
for a pair of candidates and for the general form with strata, and the
task-level result of its metric for a pair of tasks and for the general
form without strata, is in no comparison's family. The primary results of all the experiment's
declared comparisons form one family of their own, corrected together
by Benjamini and Hochberg's procedure at `FALSE_DISCOVERY_RATE`, and
every other result of a declared comparison is in its own comparison's
family, as in any comparison that is not exploratory.

- **The family's size** is `D`, the number of comparisons the
  experiment declared, known at create. Every declared comparison
  counts in it: a primary result without a usable p-value, because it
  is insufficient, descriptive, reads self-reported values or is not
  computed yet, counts as `p = 1`, so it is never a finding and never
  makes the correction grant another result more than `D` p-values
  allow. `PERMUTATION_RESAMPLES` for a declared primary result resolves
  with `m = D`, and its power floor's threshold is
  `FALSE_DISCOVERY_RATE / D`.
- **Where the corrected values live.** A declared comparison's primary
  result stores its `p`, its reason, which names the declared family or
  states its power floor, and no `q` or verdict, since its `q` depends
  on the other declared comparisons' p-values. The experiment's
  declared analysis (The stored analysis, below) stores, for each
  declared comparison, its primary result's `p`, `q`, verdict and
  reason, computed from what the declared comparisons store; a primary
  result that is insufficient keeps that verdict there. A bar or a
  report that reads a declared comparison's primary verdict reads it
  there.
- **The automatic pair.** A declared pair of candidates that is the
  automatic comparison's pair, the same candidate against the
  experiment's reference candidate, in that orientation, with no
  `variable` and no `metric` of its own, is that comparison: one
  record, `automatic` true and `declared` set, whose primary result is
  in the declared family and not tested alone. In the other
  orientation, with a `metric` of its own, or in the general form, it
  is a comparison of its own.

Why one family across declared comparisons, when the family is
otherwise one comparison: the family is one comparison so that adding a
candidate to an experiment never moves another candidate's verdicts.
The declared set is closed when the experiment is created, before any
trial is leased, submitted or imported (spec 003), so no candidate or
comparison added later can join it and move its corrected values; a
comparison asked for later is made on request, and is exploratory. And
correcting the planned tests together is what bounds the share of
false findings among them at `FALSE_DISCOVERY_RATE`: tested alone at
5% each, ten independent planned tests find at least one difference in
40% of experiments where nothing differs. Correcting them with
every task-level result instead would bury the planned tests under
hundreds of others, so each comparison's other results keep their own
family.

**The experiment's test of independence.** An experiment may also
declare, when it is created, one label metric whose dependence on a
cell variable, the candidate unless it names another, it tests over
every level of that variable at once (spec 002, `independence`): the
experiment-level test of independence (below). It
is one planned test, tested alone at `FALSE_DISCOVERY_RATE`, its `q`
equal to its `p`, and in no family, for the reason the declared
primary result of a comparison that is not exploratory stands alone.

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
| `agent` | `candidate.kind`, `agent_or_image_digest`, `program` |
| `model` | `model` |
| `reasoning` | `reasoning` |
| `tools` | `tools` |
| `environment` | `environment_digest` |
| `workspace` | `workspace_ref` |
| `task` | `task_hash` |

`agent` is the system under test itself: its kind, its agent version or
image or declared digest, and how its program is started and configured.
So two configurations of one program, such as two HTTP runner configs
for one API, or two versions of one external harness, differ in `agent`
alone and are a single-variable comparison; so are two `external`
candidates that declare one external harness and model and differ only in their
declared `reasoning`, on `reasoning`. Every tuple field but
`grader_digests` belongs to one logical variable; a change of grader
changes what is measured, and is never a variable. A comparison holds
one logical variable or none. When a request declares none, the
variable is the one logical variable whose fields differ between the
two sides; when none differs it is empty, an A/A comparison whose every
`improved` or `regressed` verdict is a false discovery or drift over
time; when several differ it is empty and every differing field is a
confound.

**The pairing unit is everything but the variable.** Two cells are
compared when their tuples agree on every field outside the variable,
or name in `confounds` each field where they do not. For every variable
but `task`, the two sides are two candidates' cells and the unit is the
task: cells pair by task name (Comparisons, below). For `task`, which a
comparison declares only on request (spec 003), the two sides are one
candidate's cells of two tasks of one experiment, such as one prompt
against another, and the unit is that candidate: the comparison holds
one task-level result per metric, for that pair, and no suite-level
result. Such a comparison is made on request and is therefore
exploratory: its primary metric's result is corrected in the family
with every other result. Its tuple confounds are computed over the two
cells as for a paired task, so two tasks that also differ in
environment or workspace name it.

A comparison's results' `confounds` are spec 002's confound entries, of
eight kinds; a pair result of two benchmark entries adds `declared` and
`taxonomy` (Benchmark entries, below).

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

| Confound | When, on either side | Counted |
|---|---|---|
| `observed.model` | a request was served by a model other than the one it was sent to | the requests served by another model |
| `sent.model` | a trial with at least one request sent none to its configured `model`: the override did not take effect | the requests of such trials |
| `sent.reasoning` | the set of `reasoning` values sent to the configured model differs between the two sides, and the variable is none of `agent`, `model` and `reasoning` | the requests carrying a value the other side never sent |
| `observed.service_versions` | a service named in the `service_versions` of trials on both sides has a version on one side that no trial of the other side recorded: the service changed between the trials compared | the finished trials carrying a version the other side never recorded |
| `declared.image` | the set of `declared.image` values of the external trials differs between the two sides, and the variable is not `agent`: the external harness ran in another image | the finished trials carrying an image the other side never declared |

A suite-level result carries a layer confound with the tasks it
occurs in and the summed counts. A layer confound leaves
`single_variable` as it is: one fallback in one task names that task's
results, and does not take the comparison out of the headline (spec
010). `observed.service_versions` is how a change on a service's side,
such as a new release of an API both candidates called, never reads as
a difference between the candidates: the tasks it touches name it.

**`missing`.** A result whose sides hold finished trials without a
value of its metric carries `missing`, with the count per side. A
result with `missing` is never headlined (spec 010), since a figure
computed without some trials' values is inflated or deflated where no
one can see it.

**`self_reported`.** A result that reads a self-reported value, which
an external harness computed and submitted itself (spec 007), carries
`self_reported`, with the number of finished trials per side whose
value is self-reported. Such a result has no verdict, stays outside the
family (Multiple comparisons, above) and is never headlined (spec
010): it is the external harness's claim, not a measurement Examen made.

**`incomplete`.** A result whose sides' cells hold trials excluded from
analysis that no finished retry replaced carries `incomplete`, with the
count per side: trials canceled when a Budget ran out or their cell
parked, `over_budget` trials, `infra_failed` trials whose retries are
spent, an external harness's declared infrastructure failure, and an
imported run that failed. Nothing about the agent excluded them, and
yet they are not a random draw: a cell whose dear trials ran out of
money keeps its cheap ones. It names the tasks it occurs in, leaves
`single_variable` as it is, and fails a bar whose `allow_partial` is
false (spec 009).

**`window`.** Each side's window is the earliest `started_at` and the
latest `ended_at` of the trials the result reads. A result carries
`window` when the two sides' windows' starts, or their ends, lie more
than `WINDOW_TOLERANCE` apart, with each side's window: a change at a
model provider between them is in no tuple field. It applies to every
reference alike: a baseline's experiment, adopted trials, an
experiment in mode `import` whose cells hold runs of different times,
and two benchmark entries. Two candidates of one experiment are dealt
trials round robin (spec 005) and share one window unless a cell ran
again later. Two tasks of one experiment share one window only under
`interleaved` scheduling (spec 005), which deals every cell of every
task in turn: under `task_by_task`, the default, a task's cells run
before the next task's, so a comparison across tasks compares two
times and carries `window` once they lie more than `WINDOW_TOLERANCE`
apart. It names the tasks it occurs in and leaves
`single_variable` as it is.

**`uncalibrated`.** A result of a label metric, or of a cell-level
metric of one, whose producing grader is a judge that spec 006 reports
uncalibrated for that metric when the result is computed carries
`uncalibrated`, naming the judge version. The calibration read is part
of the inputs document. Such a result is never headlined (spec 010): a
label no person has checked the judge on is a reading of the judge, not
yet a measurement of the agent. It changes no verdict and leaves
`single_variable` as it is.

The comparison's `confounds` are the union of the names of its results'
confounds.

**Declared provenance.** A side whose candidate is of kind `external`
is listed in the comparison's `declared_provenance`: its tuple, outcome,
times and outputs are what its external harness declared, which Examen cannot
observe (spec 005). In a comparison it changes no verdict and no
confound. A console and a report show it beside the comparison (spec
010).

### Comparisons over cell variables

A declared comparison of the general form (spec 002, `comparisons`)
names what it varies as a cell variable (spec 002, Cell) and two of its
levels, so one shape holds a pair of candidates, two tasks of one
candidate, two levels of a task factor, of a model or of a reasoning
level, within or across whatever else the experiment varies:

```
{ "variable": "object", "levels": ["build", "do"],
  "where": { "candidate": "sha256:41ab…" }, "by": ["volition"] }
```

- **Sides.** The cells `where` keeps are those carrying each of its
  variables at the level it names; every cell when it names none. Of
  them, the cells carrying `variable` at the first of `levels` are the
  candidate side and those at the second the reference side; a cell at
  neither level is on neither side. The delta is the candidate side
  less the reference side, as in every comparison.
- **Strata.** `by` names the cell variables the comparison is
  stratified by. A stratum is one combination of their levels, and
  each side holds at most one cell of it, which is held at create:
  two cells of one side in one stratum differ in something the
  declaration names neither as its variable nor as a stratum, and a
  test that permutes within the stratum would read that difference as
  the variable's, so the variable that separates them belongs in `by`
  or `where`. Every cell a side holds carries every variable of `by`,
  and the two cells of a stratum differ in nothing but the variable and
  what it implies: across a variable of the candidate, `candidate`,
  `model` or `reasoning`, they are of one task; across a factor, they
  are of one candidate and hold every other factor at one level; across
  the task, they are of one candidate. A stratum whose cells also differ
  in something else is refused at create, so a factor neither held in
  `where` nor named in `by`, such as a seeded workspace paired with an
  empty one under `object`, never reads as the variable; a candidate's
  own fields that also differ, a model across a reasoning level, are
  tuple fields and are named as confounds (Confounds and the variable,
  above).
  The strata are the comparison's paired tasks: a stratum both sides
  hold gives one task-level result per metric, a stratum one side holds
  alone is listed in `unpaired_tasks`, and the suite-level result tests
  and summarizes over the strata by Suite-level summaries, above, with
  each stratum weighted as its candidate side's cell's task is, and
  `left_out` and `coverage` read over the strata.
- **A stratum's name** is its task's name when `by` is `["task"]`, and
  otherwise `name=level` for each variable of `by` in its order, joined
  by `,`, a candidate's level written as its digest: `volition=want`,
  `model=m-1,task=build`. It is the result's `task`, its seed scope is
  `"task:"` and the name (Determinism, below), and a declaration whose
  two strata would take one name is refused at create. A name of spec
  003 holds no `=` or `,`, so no stratum named by levels takes a task's
  name.
- **Without `by`.** Each side is one cell, and the comparison has the
  shape of a comparison across tasks (Confounds and the variable,
  above): one task-level result per metric, named by the two cells'
  task names joined by ` vs `, or by the one task's name when both
  cells are of it, and no suite-level result.
- **Its logical variable** follows from its cell variable: `model` and
  `reasoning` are their own; `task` and every task factor are `task`,
  whose field is `task_hash`; and `candidate` is the one logical
  variable whose fields differ on the paired strata, or none, as for a
  pair of candidates that declares none. So two tasks that differ in a
  factor and also in environment or workspace name the field that also
  differs, as a comparison across tasks does.
- **Its primary result** is the suite-level result of its `metric`, or
  without `by` that metric's one task-level result, corrected in the
  declared family (Multiple comparisons, above).

Examples, for a study whose tasks declare the factors `object`,
`volition` and `workspace`: object at one volition for one candidate,
`{variable: object, levels: [build, do], where: {candidate: C,
volition: none}}`, one cell per side; object stratified by volition
within one candidate, the declaration above; one model's two harnesses,
`{variable: candidate, levels: [C1, C2], by: [task]}`, which reads as
the pair `{candidate: C1, reference_candidate: C2}` does; the seeded
workspace against the empty one, `{variable: workspace, levels:
[seeded, empty], where: {candidate: C, object: do, volition: want}}`;
and a low against a high reasoning level on every model,
`{variable: reasoning, levels: [low, high], by: [model, task]}`.

Why one cell per side per stratum, rather than pooling: two cells of
one side in one stratum are two configurations or two tasks the
declaration did not name, and pooling them would put their difference
on the scale of the variable's. Holding each stratum to a single pair
keeps every task-level result a single-variable pair whose confounds
are computed as any paired task's are.

### Comparisons

A comparison has a candidate side, the candidate's cells in its
experiment, and a reference side, which is exactly one of:

- **a baseline**: the baseline's candidate's cells in the baseline's
  experiment (`baseline` set);
- **another candidate of the same experiment**
  (`reference_candidate` set);
- **the same candidate's cell of another task of the same experiment**,
  for the variable `task` (`task` and `reference_task` set);
- **the cells of the second level** of a declared comparison of the
  general form, whose candidate side is the cells of its first level
  (`declaration` set; Comparisons over cell variables, above).

**Pairing.** Cells pair by task name, and in the general form by
stratum. A task on one side only is listed in `unpaired_tasks` and has
no task-level result. Metrics pair
by digest: a metric of the candidate's suite for which the reference
holds no values has `valued = 0` on the reference side and is
insufficient. A candidate-level metric pairs the two sides'
candidates' values, and an open-vocabulary metric reads both sides at
one taxonomy record, so a change of taxonomy never sits between the
sides of one comparison.

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
model provider between them is in no tuple field: the comparison
records both windows, and its results carry `window` where they lie
more than `WINDOW_TOLERANCE` apart. An owner who needs to separate
drift from the variable includes the baseline's candidate as a
candidate of the new experiment, makes it the reference, and compares
the two in one window.

**When comparisons are computed.**

- **Automatic.** For every candidate of an experiment other than its
  reference candidate, against the experiment's `reference` (spec 002),
  with `automatic` true, and `exploratory` false when that reference was
  set before any cell left `pending`. It is computed when the
  candidate's first trials finish, and recomputed in place, keeping its
  id, with `final` false, each time a trial of either side reaches
  `done` or a grading of one is recorded. Once both sides are final it
  is computed a last time with `final` true and never changes again. A
  change of the experiment's `reference` starts new automatic
  comparisons, `exploratory` when the change came after a cell left
  `pending`; the earlier ones stay.
- **Declared.** For every comparison the experiment declared (Multiple
  comparisons, above), with `declared` its position in the
  experiment's `comparisons` and `exploratory` false, computed and
  recomputed as an automatic comparison is, from its candidate's first
  finished trial to its last computation once both sides are final. A
  declared comparison across tasks needs both of its cells; until both
  exist, as in mode `import` before the first trial of each, it waits.
- **After a later grading.** A grading appended to a trial that a final
  comparison read, or a taxonomy record appended for an open-vocabulary
  metric it read (spec 006), makes a new comparison of the same pair,
  final and with the same `automatic`, `declared` and `exploratory`;
  the earlier one stays, and the declared analysis reads the newest.
- **For a bar evaluation.** When a bar pins a taxonomy record that no
  final comparison of the pair it reads was read at, a comparison of
  that pair read at the bar's records, with the `automatic` and
  `exploratory` of the pair's automatic comparison (spec 009).
- **On request.** Any candidate against any baseline the owner may
  read or against another candidate of the same experiment, with an
  optional declared variable, computed once from what both sides hold
  then (spec 003), with `exploratory` true.

A provisional comparison is computed by the same rules over the
finished trials so far, so a cell below `MIN_TRIALS_FOR_VERDICT` is
`insufficient` and verdicts appear as cells fill. It is a view of a
running experiment, not a stopping rule: N is declared before a cell
runs, and a bar or a report reads only a final comparison (specs 009
and 010). Every comparison is computed by the work queue.

**The comparison record** is spec 002's. Of its fields, the analysis
fills `reference_candidate` or `baseline`, `automatic`, `declared`, `exploratory`,
`state`, `final`, `single_variable`, `confounds`,
`declared_provenance`, `unpaired_tasks`, `candidate_suite` (what a
bar's `at_least` and `at_most` read, spec 009), `windows`,
`verdict_counts`, `correction` (Multiple comparisons, above),
`taxonomies`, `analysis_version`, `parameters`, `inputs_digest` and
`seed` (Determinism, below), and `results`. Each result is spec 002's
result, with its `statistic`, its `reason`, its `no_delta_interval`
when it has one and, on a suite-level result, its `left_out`; each of
its two sides, like every stored summary below, is spec 002's summary:
`n`, `denominator` (`valued`), `missing`, `numerator` (`k`) for a
binary metric, `value`, `interval` or `no_interval`, `mean`, `sd`,
`min` and `max` for a numeric metric, `distribution`, `mode` and
`unmapped` for a label metric, at suite level `tasks` and `coverage`,
and `excluded` and `final`.

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
  made; the number of its trials whose cost is `unpriced` or `unknown`
  (spec 007), which the spend leaves out and states, never reading them
  as zero; its service cost: the sum of the `service_usage` costs its
  trials' APIs reported for themselves, kept apart from the spend and
  never added into it (spec 005); and, when the suite's primary metric
  is binary, its cost per success: the cost of its finished trials over
  the number of true values, absent when there are none or when any
  finished trial's cost is not known;
- per experiment, candidate and metric, the candidate's suite-level
  summary, and the same spend figures summed over the candidate's
  cells.

They are recomputed by the work queue when a trial of the cell reaches
`done` or a grading is recorded, and replace the previous figures. A
stored summary is computed by the same procedures as a comparison, its
root seed derived by the same formula from its own inputs document: the
canonical JSON of the cell's or candidate's finished trials, with the
measurement ids and values it read. Recomputing it when nothing changed
is therefore bit-identical. A comparison never reads the stored
analysis: it computes its own side summaries and keeps them, so a
comparison stays reproducible when the stored analysis moves on.

Each comparison also stores its `verdict_counts`, which a headline shows.

**The declared analysis.** For an experiment that declared comparisons
or a test of independence, `examend` keeps one more record (spec 002):

- the declared family: its method, `benjamini_hochberg`, its size `D`
  and its level, `FALSE_DISCOVERY_RATE` as the declared comparisons
  recorded it;
- for each declared comparison, in the experiment's order, the newest
  comparison of it, its primary metric, its primary result's `p`, the
  `q` Benjamini and Hochberg's procedure gives it among the `D`
  p-values, each missing one counted as 1 (Multiple comparisons,
  above), its verdict by the rule of Verdicts, below, from that `q`,
  its reason, and whether that comparison is final;
- the experiment's test of independence when it declared one (below).

It is recomputed by the work queue each time a declared comparison is
computed, and replaces the record before. The newest record of each
declared comparison is read under the experiment's row lock, the
correction computes with no transaction held, and the record is stored
under the lock again only when those records are still the newest and
no other recomputation stored one meanwhile; otherwise, as when a cell
of the test of independence changed meanwhile, the experiment stays
marked and the next tick recomputes it; it computes no test, only the correction of the p-values the
comparisons store, so recomputing it from the same comparisons gives
the same record bit for bit. It is `final` once every declared
comparison's newest record is final, and the test of independence's
cells are final.

### The experiment-level test of independence

An experiment that declares `independence` (spec 002) names one
trial-level label metric of its suite and a cell variable, `candidate`
unless it names another, and the test asks whether that metric depends
on the variable, stratified by the cell variables of `by`, over the
cells `where` keeps, every cell unless it names some. Unless it names
them, the strata are the task's for a variable of the candidate and the
candidate's for the task or a factor; an empty `by` makes one stratum,
named `all`. It is the stratified label test (above) with one side
per level of the variable, `K` of them, at least two: for `candidate`
the experiment's candidates in its order, and for any other variable
its levels among the cells in ascending bytewise order. Its strata are
the combinations of `by`'s levels, named as a declared comparison's
strata are (Comparisons over cell variables, above), each side holds at
most one cell of a stratum, the cells of a stratum differ in nothing but
the variable and what it implies, as a declared comparison's, and some
stratum holds two levels, which is held at create. With the defaults it
is the test of the metric against the candidate stratified by task,
over every cell.

- **Strata.** A stratum's weight is the weight of the task of its cell
  on the first side that holds one. A stratum of weight above zero is
  included when every side's cell of it holds at least
  `MIN_TRIALS_PER_TASK_IN_SUITE` valued trials of the metric.
  `coverage` is the included strata's share of the weight of the
  strata of weight above zero, and every stratum left out is listed in
  `left_out` as `unpaired`, when a side holds no cell of it, or
  `too_few_trials`, with each side's valued trials. Below
  `MIN_SUITE_COVERAGE` the result is `insufficient`, with no p-value.
  The strata are those of every cell the experiment's tasks and
  candidates make, so a cell not yet made, in mode `import`, is a
  stratum's missing side rather than a stratum fewer.
- **Reading.** Each cell is read at the producing grader the suite
  names, an open-vocabulary metric at the suite's current taxonomy
  record, as a comparison reads its candidate side (Comparisons,
  above).
- **Summaries.** Each side's suite-level summary of the metric over the
  included strata, as a comparison's suite-level label result carries
  it.
- **Verdict.** `changed` when `p` is at most `FALSE_DISCOVERY_RATE`,
  and `within_noise` otherwise; `q` equals `p`. It has no verdict, and
  keeps its `p`, when any value it reads is self-reported.
  `insufficient` comes first, by the coverage rule above.
- **Confounds.** `missing`, `self_reported`, `incomplete` and
  `uncalibrated`, as a comparison's results carry them, with their
  counts per side and the strata they occur in. Its sides differ by
  design, so no tuple field is a confound, and the layer confounds are
  a pair's and are read in the declared comparisons.
- **Seeds.** Its inputs document is the canonical JSON of
  `analysis_version`, `parameters`, `label_tests`, the metric's
  digest, its declaration, `{variable, by, where}`, the suite's
  weights, the taxonomy record and calibration read, and for each side
  in order, per cell of a stratum, the stratum's name, the task's
  digest and per finished trial its id and outcome and the measurement
  read, as a comparison's document holds them, with each side's
  excluded trials. Its root seed derives from that document's digest
  as a comparison's does, and its test's seed from the scope
  `"experiment"` (Determinism, below).
- **When.** It is computed with the declared analysis, from the first
  finished trials, and recomputed as trials finish and gradings
  arrive; it is final once every cell it reads is final, and is then
  computed a last time. Before its first computation the declared
  analysis answers it with its metric, its variable and its levels, no
  `p`, and the reason "not computed yet: no cell has a finished
  trial".

The result is spec 002's independence result: the metric, its
`variable`, `by` and `where`, its `levels` in order, their summaries,
`tasks` (the strata included), `coverage` and `left_out`, the test's
`method`, `statistic`, `exact`, `resamples` and `seed`, `p`, `q`, the
verdict and its reason, the confounds, `final`, and the
`analysis_version`, `parameters` and `inputs_digest` it was computed
with.

### Comparisons across experiments: batches and anchors

Comparisons across experiments come after the general form of declared
comparisons, as a later build step; the design is kept here. An owner
who runs pilots at a small N, then a batch, then a new model or a new
program version months later, compares experiments, not candidates of
one experiment. The cell variable `batch` (spec 002) is the experiment
a cell belongs to, so a comparison across batches is the general form
over the cells of several experiments of one suite name of the owner's,
with `batch` as its variable or among its strata, and its confounds are
stated from provenance as a baseline's are: `window` with each batch's
time window; every tuple field that differs within a stratum, a
changed task's `task_hash`, a changed judge's `grader_digests`, an
external harness's version in `program`; `declared.image`; and
`observed.service_versions`.

**The anchor rule.** Whatever is constant within each batch and
differs between them, the model provider's state in each window, a
harness or image version every run of a batch shared, the judge
version a batch was graded with, moves every cell of the batch
together, so no test of the batches' cells can tell it apart from the
variable compared across them. Such a confound can be ruled out only
through an anchor: a configuration, one candidate digest, that ran in
both batches. With an anchor, the result carries the anchor's own
comparison across the two batches, an A/A comparison over time, with
its delta, its interval and its `p`, as the drift the batches' other
differences produced, and a difference between batches is read against
it. Without one, the result says so, and its reason ends "; no
configuration ran in both batches, so drift between them cannot be
ruled out". A comparison across batches is never `single_variable`.

What is served before that step: a baseline (spec 002) pins one
candidate of an earlier experiment of the suite, and a later
experiment that names it as its `reference` compares each of its
candidates with it automatically, with `window` and the tuple
confounds named, or, on request, any candidate with any baseline,
exploratory. An owner who runs the baseline's candidate again in the
later experiment makes it an anchor by hand: the comparison of that
candidate in the later experiment with its own baseline, on request,
is the A/A comparison across the two windows.

### Benchmark entries

Benchmark entries and their rank intervals come after v0.1, with
submissions to a published benchmark (spec 013); the design is kept
here. A benchmark's results table (spec 013) compares entries of different
experiments, often of different owners, through the `values` each
entry copied at submission (spec 002): per task, the task's weight,
the trials' values of the benchmark's primary metric, the cells' tuple
fields outside the candidate and the layer counts. It makes no
comparison record; it holds the figures below.

**Ranked entries and the common tasks.** An accepted entry is
unranked, with why, when:

- a finished trial of a task of weight above zero in its `values` has
  no value of the primary metric, or a self-reported one: a table
  orders by figures Examen measured, and a missing value moves an entry
  where no one can see it; or
- its covered tasks, those of weight above zero that hold at least
  `MIN_TRIALS_PER_TASK_IN_SUITE` valued trials, hold less than
  `MIN_SUITE_COVERAGE` of the suite's weight.

Declared provenance alone, an external harness whose outputs Examen
graded, is ranked and marked. The remaining entries are taken in
order, the version's reference entry first when it is one of them, then
by ascending submission id. The **common tasks** `C` start as the first
one's covered tasks; each next entry is ranked when `C` and its covered
tasks still share at least `MIN_SUITE_COVERAGE` of the suite's weight,
and `C` becomes their intersection; otherwise it is unranked: "covering
it would leave the table's common tasks at 70% of the suite's weight;
80% needed; it lacks t3". The order, the pair results and the rank
intervals of the ranked entries all read `C` alone, so the order and
the tests rank one set of tasks, and the table's `common` names `C`
with the share of the weight it holds.

**The order** (spec 013) is by each ranked entry's suite-level summary
of the primary metric over `C`, which the table computes and stores
beside the summary the entry copied.

**A pair result.** For two ranked entries `a` and `b` of one benchmark
version, `a` the one of the lower submission id, the pair result is the
primary metric's suite-level result over `C`, computed as for a
comparison with `a` as the candidate side and `b` as the reference
side: tasks pair by name and carry the suite's weights, and every task
of `C` holds at least `MIN_TRIALS_PER_TASK_IN_SUITE` valued trials of
both by construction. The delta is `S_a - S_b` with the suite-level
delta interval of the metric's type, Agresti and Caffo's for a rate,
and the test is the stratified permutation with the metric's statistic:
the tasks are fixed, weighted as declared, and only the labels within
each task are relabeled. `left_out` names the tasks outside `C`. Its
p-value is adjusted with the table's other pairs to `h` (Rank
intervals, below), and its verdict follows the verdict rule with `h`
in place of `q`, at the level `1 - CONFIDENCE_LEVEL`: an entry is
`improved`, `regressed` or `changed` against another only when the
table holds the two apart. The unadjusted `p` is shown in the reason as
text and never decides a verdict. Read the other way, `b` against `a`,
the delta and its interval are negated, `p` and `h` are the same, and
each confound's counts change sides. A pair result is stored as spec
002's result of level `suite`, naming `a` and `b`, with `h` in place of
`q`; it belongs to no comparison's family. A pair with an unranked
entry is `insufficient`, with the unranked entry's why, and has no `p`.

**Its confounds.** A pair varies the candidate: the tuple fields of the
logical variables `agent`, `model`, `reasoning` and `tools`. Over `C`
it names:

- each tuple field outside the candidate that differs between the two
  entries' copied fields: `task_hash`, `environment_digest`,
  `workspace_ref` or `grader_digests`;
- the layer confounds (Confounds and the variable, above), from the
  copied counts, with the variable read as the logical variables whose
  fields differ between the two candidates: `declared.image` is named
  only for two candidates that agree on `agent`, and `sent.reasoning`
  only for two that agree on `agent`, `model` and `reasoning`;
- `incomplete`, from the copied counts of excluded trials;
- `declared`, with the finished trials of each side whose candidate is
  `external`, which a comparison keeps in `declared_provenance`
  instead;
- `window`, when the two entries' windows (spec 002) lie more than
  `WINDOW_TOLERANCE` apart at their starts or their ends; and
- `taxonomy`, when the two entries' values were read at different
  taxonomy records, naming each: the same raw label may be one
  canonical label on one side and another on the other.

`missing` and `self_reported` never sit on a pair: such an entry is
unranked.

**Rank intervals.** A table's K ranked entries give
`m = K(K - 1) / 2` pair results (a pair with an unranked entry is
outside the family and does not count toward `m`), whose p-values are adjusted together
by Holm's step-down procedure at the family-wise level
`1 - CONFIDENCE_LEVEL`. With the p-values sorted ascending, ties in the
order of the pairs' submission ids, `p(1) <= ... <= p(m)`, the adjusted
values are

```
h(i) = max over j <= i of min(1, (m - j + 1) * p(j))
```

Tied p-values receive the same `h`. An insufficient pair has no
p-value, still counts in `m`, and is never apart. A pair is apart when
its `h` is at most `1 - CONFIDENCE_LEVEL` and its delta is not zero;
the entry its delta favors in the metric's direction, the higher one
for direction `none`, is then significantly better than the other. An
entry's rank interval is

```
[1 + B, K - W]
```

`B` the number of entries significantly better than it and `W` the
number significantly worse. An entry apart from neither of two
neighbors that are apart from each other keeps an interval that spans
both.

What the intervals hold together: Holm's procedure keeps at most
`1 - CONFIDENCE_LEVEL` the chance that any pair whose two entries do not
differ is called apart, under any dependence between the pairs, so the
K intervals hold together against false separations. Calling the wrong
one of two entries that do differ the better, a directional error, is
held at that level only under conditions close to independence between
the pairs' tests, which pairs that share an entry do not strictly meet;
the table states the first guarantee and not the second.

A rank interval states every pair at once, and one false separation
moves two intervals, so the family is held to no false separation at
all, not to a share of them as Benjamini and Hochberg's procedure holds
a comparison's family. Holm's procedure rejects whatever Bonferroni's
rejects and often more. Grouping entries whose intervals overlap is
rejected: overlap is not a test, since two 95% intervals stop
overlapping only near 3.9 standard errors apart while a difference is
significant at about 2.8; it does not chain, since A may overlap B and
B overlap C while A and C differ; it ignores that two entries ran the
same tasks, which the task-stratified test accounts for; and it ignores
how many pairs a table compares.

**Resamples.** The table's pair tests are one family held at
`1 - CONFIDENCE_LEVEL`, so `PERMUTATION_RESAMPLES` resolves with its
`m` and that level, and the smallest p-value a pair test can give stays
below Holm's first threshold, `(1 - CONFIDENCE_LEVEL) / m`. Sequential
stopping applies as in any Monte Carlo test. `MAX_BENCHMARK_ENTRIES`
bounds `m`. The table's comparison of each entry with the reference and
a comparison of two entries on demand read the stored pair result, with
its `h` and its verdict, so they agree with the table.

**The stored table.** The table's inputs document is the canonical JSON
of `analysis_version`, `parameters`, the suite's weights and primary
metric, the version's `reference`, and each accepted entry's submission
id and `values`, ascending by submission id. Its `inputs_digest` and
root seed follow Determinism (below), and a pair's procedure seed is
that formula's with the scope `"pair:" || a || ":" || b`, `a` and `b`
its two submission ids ascending, and the procedure `delta_interval`
or `test`. The work queue computes the table when the version's
accepted entries or its reference change, and stores the ranked and
unranked entries, `C` and a pair result for every two accepted entries.
No figure of the table, of its comparisons with the reference or of a
comparison on demand is computed when it is read. Recomputing a table
from the same inputs is bit-identical; after a change, every pair is
drawn again from the new root seed.

### Trends across successive baselines

Trends come after v0.1, a later build step than the console path; the
design is kept here. Moving a baseline makes a new record (spec 002) that names
the record it replaced in `previous`. The records linked this way,
newest first, are the baseline's **lineage**. A trend reads the newest
`MAX_TREND_POINTS` records of a lineage and shows, per metric:

- **points**: each record's reference side as the candidate's
  suite-level summary of its experiment;
- **steps**: for each pair of consecutive records, the comparison of
  the newer record's candidate and experiment against the older
  record, an ordinary comparison computed on request, and so
  exploratory, when it does not yet exist, with its verdicts and
  confounds.

A step between records that measured a metric under different digests
is insufficient for that metric, and the series shows the break. An
A/A step, the same candidate measured again later, is how drift at a
model provider becomes visible.

### Determinism

The same inputs and parameters give bit-for-bit identical results on
every machine and architecture.

**Inputs.** A comparison's inputs document is the canonical JSON of
spec 002 of: `analysis_version`, `parameters`, `variable`,
`exploratory`, for a declared comparison `declared`, the size `D` of
its experiment's declared family, which resolves its primary result's
`PERMUTATION_RESAMPLES`, and for one of the general form its
declaration, `{variable, levels, where, by, metric}`, the candidate
suite's weights and primary metric, a declared comparison's own
`metric` in its place, and
for each side and each paired task, a stratum in the general form, the task's name, or in the general form the stratum's name, and the task's digest, the
cell's tuple, the producing grader read per metric, each taxonomy
record read with its digest and whether a bar pinned it, for each label
metric a judge produces the calibration read, `{grader, pairs, alpha,
calibrated}`, and per finished trial its id, outcome, `started_at` and
`ended_at`, sent and served models, reasoning values, service versions,
declared image, and every measurement read as `{metric, grader,
generation, value, self_reported}`, with the canonical label an
open-vocabulary value maps to, or none when it maps to none; per side,
the id and outcome of each trial excluded from analysis that no
finished retry replaced; and for each side's candidate, every
candidate-level measurement read. `inputs_digest` is `sha256:` and the
hex of its SHA-256. The measurement ids read are recorded in
`comparison_inputs` (spec 007), from which the document is rebuilt.

**Seeds.** The root seed is `SHA-256("examen.analysis/v1" || 0x00 ||
inputs_digest)`. Each procedure of each result has its own seed,
`SHA-256(root || metric digest || 0x00 || scope || 0x00 ||
procedure)`. `scope` is `"task:"` and the task's name for a task-level
result (for a comparison across tasks, `"task:"` and both names joined
by `" vs "`), `"suite"` for a suite-level result, `"experiment"` for
the experiment-level test of independence, whose root seed comes from
its own inputs document, and `"pair:"` with
two submission ids for a pair result; a name of spec 003 holds no `:`
and no space, so no task's scope equals another scope. `procedure` is
one of `summary_interval:candidate`, `summary_interval:reference`,
`delta_interval` and `test`, so no result's random draws depend on the
order in which results are computed. Boschloo's test, the
order-statistic intervals and the score intervals draw nothing. The
root seed and each result's seed are recorded.

**Draw order.** A resampling procedure draws in one fixed order: the
candidate side's tasks first, then the reference side's, each side's
tasks in ascending task-id order, and within a task its trials'
resampled indices in sequence; one replicate is complete before the
next begins. Every implementation that draws in this order from the
generator below reproduces the same replicates.

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
reference side, and is never drawn again. A Monte Carlo test draws its
relabelings in turn from its procedure's generator until it stops.

**Arithmetic.** IEEE 754 binary64. Sums run in the orders this spec
fixes. Every product that is summed is rounded before the addition
(in Go, an explicit `float64(x * y)`), which forbids the fused
multiply-add Go otherwise emits on arm64. Every elementary function the
analysis calls, `exp`, `log`, `lgamma` and `erfinv`, and the powers and
the normal density built from them, is Examen's own pure-Go code in
`internal/analysis/mathx`, vendored with no assembly and no fused
multiply-add, in place of the standard library's, whose `exp` and `log`
use assembly on amd64. Square roots, the four operations and the
conversions are correctly rounded by IEEE 754 on every platform. So
results are bit-for-bit identical across machines and architectures,
and the golden comparison is held to byte equality on amd64 and arm64.

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

### Worked examples

The reference values the tests commit, computed from the formulas
above with exact rational arithmetic where the value is rational, and
in binary64 otherwise, at `CONFIDENCE_LEVEL` 0.95:

| Case | Inputs | Value |
|---|---|---|
| `z` | 0.95 | 1.959963984540054 |
| Wilson, clamped | 0 of 5 | [0, 0.4345]: the lower bound of `k = 0` is set to exactly 0 |
| Wilson | 5 of 5; 11 of 49; 7 of 50; 12 of 50 | [0.5655, 1]; [0.1302, 0.3588]; [0.0695, 0.2619]; [0.1430, 0.3741] |
| Newcombe | 0/5 against 2/5 | -0.4000 [-0.7693, 0.1182] |
| Newcombe | 7/50 against 11/49 | -0.0845 [-0.2362, 0.0696] |
| Newcombe | 12/50 against 11/49 | +0.0155 [-0.1502, 0.1794] |
| Newcombe | 12/50 against 7/50 | +0.1000 [-0.0558, 0.2515] |
| Kish and Wilson | ten tasks of weight 1, 5 of 5 in each | `n_eff` = 50, `S` = 1, [0.9287, 1] |
| Kish and Wilson | weights 2, 1, 1 with 4/5, 3/10, 9/10 | `S` = 7/10, `n_eff` = 16 / (4/5 + 1/10 + 1/10) = 16, [0.4560, 0.8666] |
| Agresti and Caffo | ten tasks of weight 1: candidate 5/5 in each; reference 5/5 in eight and 4/5 in two | delta 1/25 = 0.04; `D~` = 1/35 = 0.028571; `V~` = 32/8575; interval [-0.0912, 0.1483] |
| bootstrap-t, no spread | 0, 0, 0, 0, 10 | `m` = 2, `se` = 2; a resample is all zeros with probability (4/5)^5 = 1024/3125, so about 3,277 of 10,000 resamples have `t*` = -infinity, far more than the 250 that make `Q*(alpha / 2)` infinite, and the interval is not shown |
| median ranks | 5 values; 6 values; at 0.974679, 6 and 7 values | none; [x(1), x(6)], coverage 31/32; none, and [x(1), x(7)], 63/64 |
| `p90` ranks | 28 values; 29 values; at 0.974679, 34 and 35 values | none; [x(21), x(29)], 0.9513; none, and [x(24), x(35)], 0.9749 |
| Hall and Sheather's bandwidth | `n` = 5, `q` = 0.5; `n` = 35, `q` = 0.9 | 0.5682; 0.1058 |
| Welch permutation, exact | x = 3, 5, 4, 6, 12; y = 2, 3, 2, 3, 2, 3 | 462 relabelings; T = 2.1918; p = 10/462 = 5/231 = 0.0216, where the bare difference of means would give 4/462 = 0.0087 |
| repeat rate | a, a, a, b, c; five equal labels; five distinct | 6/20 = 0.3; 1; 0 |
| repeat rate permutation, exact | a, a, a, a, a against a, b, c, d, e | 252 relabelings; T = 1; p = 12/252 = 1/21 = 0.0476 |
| mode share against repeat rate | trials spread evenly over 20 labels | expected mode share 0.2885 at 5 trials and 0.1176 at 49; expected repeat rate 0.05 at both |
| power floor, Boschloo | 5 trials against 5 | smallest p 2 (0.4995 * 0.5005)^5 = 0.0019531, above 0.05 / m from m = 26; in a family of 40, whose threshold is 0.00125, the reason is "with 5 trials per side, no difference can be shown among 40 results" |
| power floor, permutation | 5 against 5; 10 against 10 | 2/252 = 0.0079, a floor from m = 7; 2/184,756 = 1.08e-5, exact under `PERMUTATION_EXACT_MAX`, a floor from m = 4,619 |
| resamples | m = 40; m = 500 | `B` = 16,000; `B` = 200,000 |
| sequential stopping | the 200th exceedance at the 800th draw | p = 200/800 = 0.25, `resamples` 800 |
| Holm | p = 0.003, 0.03, 0.4 over three pairs | h = 0.009, 0.06, 0.4: only the first pair is apart at 0.05, and the pair of p = 0.03 is `within_noise` |
| stratified label test, two sides | t1: a, a, a, b against b, b, b, a; t2: a, a, b against a, b, b | X^2 = 2 + 2/3 = 8/3; 70 × 20 = 1,400 relabelings; p = 752/1,400 = 94/175 = 0.5371 |
| stratified label test, opposite shifts | t1: a, a, a, a against b, b, b, b; t2: b, b, b, b against a, a, a, a | the two weighted distributions are equal, so a test of them finds nothing; X^2 = 8 + 8 = 16; 4,900 relabelings; p = 4/4,900 = 1/1,225 = 0.00082 |
| stratified label test, three sides | t1: a, a, b; a, b, b; b, b, b; t2: a, a; a, b; b, b | X^2 = 3 + 4 = 7; 1,680 × 90 = 151,200 relabelings; p = 63,720/151,200 = 59/140 = 0.4214 |
| stratified label test, one label per task | every trial of t1 holds a, every trial of t2 holds b | X^2 = 0 under every relabeling; p = 1 |
| the declared family | D = 4: primary p-values 0.01, 0.04, an insufficient result, 0.03 | the insufficient result counts as 1: q = 0.04, 0.0533, none, 0.0533, one finding at 5%; corrected over the three p-values alone, q would be 0.03, 0.04 and 0.04, three findings |
| resamples of a declared primary result | D = 33 | `B` = 13,200 |
| rank intervals | three entries, the first and the third apart | [1, 2], [1, 3], [2, 3] |

### Limits

The constants of spec 002 this spec holds: `MIN_TRIALS_FOR_VERDICT`,
`MIN_TRIALS_PER_TASK_IN_SUITE`, `MIN_SUITE_COVERAGE`,
`MIN_TRIALS_FOR_P90_INTERVAL`, `FALSE_DISCOVERY_RATE`,
`CONFIDENCE_LEVEL`, `BOOTSTRAP_RESAMPLES`, `PERMUTATION_RESAMPLES`,
`PERMUTATION_EXACT_MAX`, `PERMUTATION_STOP_EXCEEDANCES`,
`BOSCHLOO_GRID_POINTS`, `TIE_TOLERANCE`, `WINDOW_TOLERANCE`,
`MAX_TREND_POINTS` and `MAX_BENCHMARK_ENTRIES`.

## Not in this spec

How measurements are stored and which is current (007). How graders
produce values, judge calibration and agreement statistics (006). Bar
rules (009). How figures are formatted and laid out (010). Sequential
testing and stopping a cell early, which N declared before a cell runs
excludes (spec 001, decision 7); the sequential rule above stops
drawing relabelings, never trials. A minimum effect size: a change that
must be large to matter is a bar's `at_least` or `at_most` (009).
Generalizing beyond a suite's tasks, and advice on more trials against
more tasks. An interval for a repeat rate. Held-out grading of a
benchmark (spec 013).

## Build order

Declared comparisons, their declared family and the declared analysis
follow v0.1 in the release after it. Label tests, the general form of
declared comparisons and the experiment's test of independence are
built in v0.5.0, ahead of the built-in comparators; comparisons across
batches follow as a later step. The first release, v0.1, builds everything above but label tests, open
vocabularies, cell-level and candidate-level metrics and the
`uncalibrated` confound, benchmark entries, trends and the
cross-architecture run of the golden comparison, which follow as later
steps (specs/README.md). Its comparisons read `external` and imported
trials, so declared provenance, comparisons across tasks and the
`self_reported`, `declared.image` and `window` confounds are in it; the
`window` confound reads adopted trials once adoption is built. Open
vocabularies, the two levels, label tests and `uncalibrated` are served
by v0.5.0; until annotations are built every judge is uncalibrated
(spec 006). Benchmark entries come with submissions, after publishing
(spec 013).

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Wilson intervals, clamped to [0, 1], Newcombe intervals, clamped to [-1, 1], Boschloo's p-values on the fixed grid and type 7 quantiles match the worked examples and committed reference values, ties and the extremes `k = 0` and `k = n` included; the lower Wilson bound of 0 of 5 is exactly 0; Boschloo's p-value is never above Fisher's for the same table | `analysis.TestWilson`, `analysis.TestNewcombe`, `analysis.TestBoschloo`, `analysis.TestQuantileType7` |
| A bootstrap-t interval matches a reference implementation on skewed samples and is `[m, m]` for equal values; a resample whose standard error is 0 is kept with an infinite `t*` and never drawn again; 0, 0, 0, 0, 10 has no interval and its `no_interval` gives the resamples without spread | `analysis.TestBootstrapT` |
| An order-statistic interval's ranks match the rule and the worked examples, and its binomial coverage is at least its level; a median of 5 values and a `p90` of fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values carry no interval; a quantile delta has none with fewer than 7 values per side for a median or 35 for a `p90` | `analysis.TestOrderStatisticInterval` |
| Welch's statistic and the studentized quantile statistic match the worked examples; with equal means and equal medians, 50 normal trials of spread 1 against 5 of spread 10, the Welch permutation test rejects at 5% in at most 10% of 2,000 simulated experiments and the studentized median test in at most 12%, where the bare differences reject in more than 40% | `analysis.TestStudentizedStatistics`, `analysis.TestStudentizedLevel` |
| A permutation test with at most `PERMUTATION_EXACT_MAX` relabelings enumerates them all and matches the worked examples; above it, it stops at `PERMUTATION_STOP_EXCEEDANCES` exceedances with `p = PERMUTATION_STOP_EXCEEDANCES / L`, or draws `PERMUTATION_RESAMPLES` as resolved for its family with the `(1 + count) / (1 + B)` estimate, and records the draws in `resamples` | `analysis.TestPermutationExact`, `analysis.TestPermutationSequential` |
| `PERMUTATION_RESAMPLES` resolves to 10,000 for small families and to `ceil(20 · m / FALSE_DISCOVERY_RATE)` above them, and a lone true discovery in a family of 500 can pass | `analysis.TestResamplesGrowWithTheFamily` |
| A result whose exact test's smallest p-value is above its threshold carries the power-floor reason; Boschloo's 5 against 5 in a family of 40 reads "with 5 trials per side, no difference can be shown among 40 results" | `analysis.TestPowerFloor` |
| A count metric is tested by the permutation of its declared aggregate's statistic | `analysis.TestCountsUseTheirAggregate` |
| Equal data on both sides give `within_noise` for every method | `analysis.TestEqualSidesAreWithinNoise` |
| With `MIN_TRIALS_FOR_VERDICT - 1` valued trials on either side, every task-level verdict is `insufficient` and the result is in no family | `analysis.TestInsufficientBelowMinimum` |
| A suite-level result includes exactly the tasks with at least `MIN_TRIALS_PER_TASK_IN_SUITE` valued trials per side, is `insufficient` only below `MIN_SUITE_COVERAGE`, and lists every task left out with why | `analysis.TestCoverage` |
| A suite-level rate's interval is Wilson's on Kish's effective sample size and a suite-level rate difference's is Agresti and Caffo's, matching the worked examples; no rate or rate difference uses the bootstrap-t | `analysis.TestSuiteRateIntervals` |
| A metric with direction `none` is never `improved` or `regressed`; a significant change is `changed`; before label tests are built a label result carries no verdict, `p` or `q` | `analysis.TestDirectionNone`, `analysis.TestLabelsAreDescriptive` |
| Label tests: Fisher, Freeman and Halton's p-value matches reference values for 2 by k tables, ties and a one-label table included; above `PERMUTATION_EXACT_MAX` tables the chi-square permutation draws from the result's seed with sequential stopping and repeats bit for bit; a significant difference is `changed`; per-label intervals are Newcombe's per task and Agresti and Caffo's over the suite | `analysis.TestFisherFreemanHalton`, `analysis.TestChiSquarePermutation` |
| The stratified label test matches the worked examples' exact p-values for two and three sides; a task of one label adds nothing and every task of one label gives p = 1; above `PERMUTATION_EXACT_MAX` relabelings it draws from its seed with sequential stopping, repeats bit for bit and lands near the exact p-value; a comparison's suite-level label result is its two-sided form, with the statistic `pearson_x2_sum`, and finds the opposite shifts a test of the weighted distributions misses | `analysis.TestStratifiedLabelTest`, `analysis.TestLabelResults`, `analysis.TestOppositeShiftsAreFound` |
| A declared comparison's primary result is in no comparison's family, carries its `p` and no `q` or verdict, draws `PERMUTATION_RESAMPLES` resolved with `m = D` and reads its power floor at `FALSE_DISCOVERY_RATE / D`; a declared comparison across tasks is not exploratory and its primary result is the primary metric's task-level result; its inputs document carries `D` and one that is not declared is unchanged | `analysis.TestDeclaredComparison` |
| The declared family corrects every declared comparison's primary p-value together over `D`, a missing one counted as 1, and matches the worked example; an insufficient primary result keeps its verdict and no `q`; a self-reported one keeps its `p` and no verdict; the declared analysis recomputed from the same comparisons is identical | `analysis.TestDeclaredFamily` |
| The experiment's test of independence over three candidates matches the worked example; it includes the strata every level holds enough trials of and is `insufficient` below `MIN_SUITE_COVERAGE`, is tested alone with `q` equal to `p`, is `changed` or `within_noise`, has no verdict on self-reported values and no `p` before label tests are built, states its power floor, and recomputes bit for bit from its inputs document with the seed scope `experiment`; its sides are the levels of its cell variable, the candidates in the experiment's order by default, and its strata the combinations of `by`'s levels, task by default, each weighted as its first side's task on every side; it is recomputed whenever a trial of its experiment reaches done, whatever comparisons the experiment keeps; before any cell holds a finished trial it answers its declaration with no figure | `analysis.TestIndependence`, `objects.TestSelectIndependence`, `compare.TestDeclaredIndependence`, `httpapi.TestGeneralDeclarationsAndIndependence` |
| A repeat rate matches the worked examples, is tested by the exact permutation of its difference (12/252 for five equal labels against five distinct), has no interval and says why, and its suite-level figure is the weighted mean; `mode_share` and `distinct_count` carry no interval, `p`, `q` or verdict, and stay out of the family, `verdict_counts` and the headline | `analysis.TestRepeatRate`, `analysis.TestDescriptiveCellMeasures` |
| A candidate-level result carries both values and no interval, `p`, `q` or verdict, and stays out of the family | `analysis.TestCandidateLevelIsDescriptive` |
| An open-vocabulary metric is read at one taxonomy record on both sides, the suite's or the one a bar pins; an unmapped raw label counts as a label of its own, marked, and never as missing; a new record after a final comparison makes a new comparison | `analysis.TestTaxonomyReads` |
| Declared images that differ between the sides in one task are `declared.image` on that task's results | `analysis.TestDeclaredImage` |
| A trial outside a metric's `applies_to` is in `not_applicable`, not in `n` or `missing` | `analysis.TestAppliesTo` |
| A cell-level metric leaves out the trials of its `exclude_labels`, reports a mode share's numerator over the remaining denominator, and a cell of declines has no value rather than a share of 1 or a repeat rate of 1 | `analysis.TestCellMeasuresExcludeLabels` |
| A comparison across tasks pairs one candidate's two cells, holds one result per metric, is exploratory with its primary result corrected in the family, and names an environment or workspace that also differs as a confound | `analysis.TestComparisonAcrossTasks` |
| Two `external` candidates that differ only in declared reasoning give a single-variable comparison on `reasoning` | `analysis.TestDeclaredReasoningIsSingleVariable` |
| A trial of `unknown` cost is counted apart in the spend and never as zero | `analysis.TestUnknownCostNeverZero` |
| The verdict follows `q` and the delta's sign alone, whatever the interval shows, and every result carries a `reason` from the templates, and every absent interval its sentence | `analysis.TestVerdictRule`, `analysis.TestReasons` |
| The declared primary metric's suite-level result has `q` equal to `p`; in an exploratory comparison, on request or against a reference set after a cell left `pending`, it is in the family and `correction.primary` is absent; the family's Benjamini and Hochberg values match a reference implementation; adding a candidate to an experiment leaves another candidate's `q` values unchanged | `analysis.TestPrimaryStandsAlone`, `analysis.TestExploratory`, `analysis.TestBenjaminiHochberg`, `analysis.TestAddingACandidateChangesNoOtherVerdict` |
| A result that reads a self-reported value carries `self_reported`, keeps its `p`, has no `q` or verdict, and is not counted in the family's `m` | `analysis.TestSelfReported` |
| A suite-level summary is the weighted mean of the per-task summaries over its included tasks; a weight-0 task is compared per task and absent from every suite-level figure | `analysis.TestSuiteWeights` |
| Two models on one agent version give a single-variable comparison; a differing task hash is a tuple confound; a request served by another model in one task is a layer confound on that task's results with its request counts and leaves `single_variable` true; trials without a value add `missing` with counts | `analysis.TestModelComparisonIsSingleVariable`, `analysis.TestConfounds` |
| Excluded trials no finished retry replaced add `incomplete` with per-side counts; sides whose windows' starts or ends lie more than `WINDOW_TOLERANCE` apart add `window` with both windows, for a baseline, adopted trials, an import experiment and benchmark entries; a label result from a judge uncalibrated for it adds `uncalibrated`; none of them changes `single_variable` | `analysis.TestIncomplete`, `analysis.TestWindow`, `analysis.TestUncalibrated` |
| Two `program` candidates that differ only in `config` give a single-variable comparison with the variable `agent`; a service version that differs between the sides in one task is `observed.service_versions` on that task's results with trial counts and leaves `single_variable` true; a comparison with an `external` side lists it in `declared_provenance` | `analysis.TestProgramConfigIsSingleVariable`, `analysis.TestServiceVersions`, `analysis.TestDeclaredProvenance` |
| A declared variable outside the logical variable table, or naming `task_hash` or `grader_digests`, is refused | `analysis.TestVariableIsOneLogicalVariable` |
| A re-graded baseline is read at the candidate's grader and the grader confound disappears; a partly re-graded one is not | `analysis.TestReferenceReadAtCandidatesGrader` |
| The automatic comparison is recomputed in place with `final` false as trials finish, keeps its id, becomes `final` true once both sides are final, and never changes after; one against a reference set after a cell left `pending` is exploratory | `analysis.TestAutomaticComparisonIsProgressive` |
| A final comparison recomputed from its recorded inputs is byte-identical, and its seeds are the recorded ones; a task named `suite` and the suite-level result draw from different seeds | `analysis.TestRecomputeFromRecordedInputs`, `analysis.TestSeedScopes` |
| A benchmark entry with a missing or self-reported primary value, or below `MIN_SUITE_COVERAGE`, is unranked with why, and a declared one is ranked and marked; the common tasks follow the rule's order; the order, every pair result and the rank intervals read the common tasks alone | `analysis.TestEntryRanking` |
| A pair result of two ranked entries is the stratified permutation over their copied values on the common tasks, with the metric's statistic and the suite-level delta interval of its type; its verdict follows `h`, not `p`; read the other way it negates the delta and its interval and keeps `p` and `h`; it names a differing tuple field outside the candidate, the layer confounds by the variable rule, `incomplete`, `declared`, `window` and `taxonomy` | `analysis.TestEntryPairs`, `analysis.TestEntryPairConfounds` |
| Holm's adjusted values match a reference implementation and the worked example, ties included; an insufficient pair counts in `m` and is never apart; `PERMUTATION_RESAMPLES` for a table resolves with its `m` at `1 - CONFIDENCE_LEVEL` | `analysis.TestHolm` |
| Three entries where the first and the third are apart and the second is apart from neither give the intervals `[1, 2]`, `[1, 3]` and `[2, 3]`; every interval is `[1 + B, K - W]` | `analysis.TestRankIntervals` |
| A results table recomputed from its recorded inputs is byte-identical, and its pairs' seeds are the recorded ones | `analysis.TestRecomputeResultsTable` |
| Every figure of the console's grid, verdict tables and cost table is read from a stored comparison or the stored analysis; the console bundle contains no statistical routine | `analysis.TestEveryShownFigureIsStored`, `console.TestNoStatisticsInTheClient` |
| The analysis calls no `exp`, `log`, `lgamma` or `erfinv` but `internal/analysis/mathx`'s, which holds no assembly | `analysis.TestUsesVendoredMath` |
| Later steps: the golden comparison is byte-identical on amd64 and arm64; a trend over a lineage of three baselines shows three points and two step comparisons | `analysis.TestGoldenAcrossArchitectures`, run on both in CI; `analysis.TestTrend` |
| A declared comparison of the general form takes as its sides the cells `where` keeps at each of its variable's two levels, pairs them by stratum, names each stratum by its task or by its levels, gives one task-level result per stratum and the stratified suite-level result over the strata, weighted as their candidate sides' tasks, and matches the stratified label test's worked example over two strata; without `by` it is one pair whose task-level result is its primary result; its logical variable follows its cell variable; a declaration whose variable or `where` or `by` no cell carries, whose level selects no cell, whose stratum holds two cells of one side or two cells that also differ in a variable it neither holds nor names, that pairs no stratum or names two strata alike, is refused at create naming the member; a comparison of no declaration keeps its inputs document | `objects.TestSelect`, `objects.TestSelectRefusals`, `analysis.TestGeneralDeclaration`, `analysis.TestGeneralDeclarationWithoutStrata`, `analysis.TestTaskVariableNeedsADeclaration`, `compare.TestGeneralInput`, `compare.TestGeneralInputWaitsAndFails`, `httpapi.TestGeneralDeclarationsRefused`, `httpapi.TestGeneralDeclarationsAndIndependence` |
| A declaration's `metric` is its primary result's metric, in the comparison and in the declared family, and a pair of candidates that names one is a record of its own beside the automatic comparison | `compare.TestGeneralInput`, `httpapi.TestGeneralDeclarationsRefused` |
| A comparison across batches names its confounds from the batches' provenance and is never `single_variable`; with a configuration that ran in both batches it carries that anchor's comparison across them, delta, interval and `p`; without one its reason says drift between the batches cannot be ruled out | not built |

## Outcome

In progress. v0.1.0 computes every served comparison with the analysis
of this spec: per-task and suite-level summaries with their intervals
by metric level, Boschloo's test and the studentized and sequential
permutation tests, the Benjamini and Hochberg correction of a family
beside the primary result tested alone, verdicts, reasons and the
power floor, coverage and task weights, the confounds of the
provenance tuple, declared provenance and declared images, comparisons
across tasks, self-reported values, and the inputs digest, seeds and
parameters that make a comparison reproduce bit for bit on vendored
math. v0.3.0 adds declared comparisons and their family, served at
`GET /experiments/{id}/declared`. v0.5.0 serves label tests in every
comparison, task-level and stratified; the general form of declared
comparisons over cell variables with their strata and a declaration's
own metric; the experiment's test of independence of a label metric
against a cell variable, in the declared analysis; the `uncalibrated`
confound from each judge's calibration; and candidate-level values.

Built in `internal/analysis` and not yet served: the layer confounds
read from Lux's records; the stored analysis's spend and outcomes; and
a benchmark's results table with Holm's correction and rank intervals.
Comparisons across batches are designed and not built. A grid's cell
summaries are computed by the same procedures when the grid is read,
and are not stored.

Every row below shipped in v0.1.0 unless it names another release.

| # | Verdict | Held by |
|---|---|---|
| 1 | Met | `analysis.TestWilson`, `analysis.TestNewcombe`, `analysis.TestQuantileType7`, `analysis.TestBoschloo` |
| 2 | Partly met: the interval `[m, m]`, an infinite `t*` kept, and the `no_interval` count; the skewed sample is checked for its shape and its repeat, not against a reference implementation | `analysis.TestBootstrapT`, `analysis.TestMeanResults` |
| 3 | Met | `analysis.TestOrderStatisticInterval`, `analysis.TestReasons` |
| 4 | Met | `analysis.TestStudentizedStatistics`, `analysis.TestPermutationExact`, `analysis.TestStudentizedLevel` |
| 5 | Met | `analysis.TestPermutationExact`, `analysis.TestPermutationSequential`, `analysis.TestResamplesGrowWithTheFamily` |
| 6 | Met | `analysis.TestResamplesGrowWithTheFamily` |
| 7 | Met | `analysis.TestPowerFloor`, `analysis.TestPowerFloorThresholds` |
| 8 | Met | `analysis.TestCountsUseTheirAggregate` |
| 9 | Met | `analysis.TestEqualSidesAreWithinNoise` |
| 10 | Met | `analysis.TestInsufficientBelowMinimum` |
| 11 | Met | `analysis.TestCoverage` |
| 12 | Partly met: Kish's, Wilson's and Agresti and Caffo's intervals match the worked examples; no test shows a comparison's suite-level rate uses them rather than the bootstrap-t | `analysis.TestSuiteRateIntervals` |
| 13 | Met; from v0.5.0 a label result is tested, and one computed before keeps its descriptive form | `analysis.TestDirectionNone`, `analysis.TestLabelsAreDescriptive`, `compare.TestServedLabelTestsAndCandidateValues` |
| 14 | Partly met, served from v0.5.0: the Fisher, Freeman and Halton and the chi-square p-values; the chi-square's sequential stop, and the methods of the per-label intervals, are untested | `analysis.TestFisherFreemanHalton`, `analysis.TestChiSquarePermutation`, `analysis.TestLabelResults`, `compare.TestServedLabelTestsAndCandidateValues` |
| 15 | Met (v0.3.0), served from v0.5.0 | `analysis.TestStratifiedLabelTest`, `analysis.TestOppositeShiftsAreFound`, `analysis.TestLabelResults`, `compare.TestServedLabelTestsAndCandidateValues` |
| 16 | Met (v0.3.0) | `analysis.TestDeclaredComparison`, `httpapi.TestDeclaredComparisons`, `compare.TestDeclaredInput` |
| 17 | Met (v0.3.0) | `analysis.TestDeclaredFamily`, `httpapi.TestDeclaredComparisons`, `work.TestComputeDeclares`, `postgres.TestDeclare` |
| 18 | Met (v0.5.0) | `analysis.TestIndependence`, `analysis.TestIndependenceWeighsStrata`, `objects.TestSelectIndependence`, `objects.TestStrataDifferInTheVariableAlone`, `compare.TestDeclaredIndependence`, `httpapi.TestGeneralDeclarationsAndIndependence`, `work.TestComputeDeclaresIndependence` |
| 19 | Met | `analysis.TestRepeatRate`, `analysis.TestDescriptiveCellMeasures`, `analysis.TestModeShareAgainstRepeatRate` |
| 20 | Met; served from v0.5.0, though nothing writes a candidate-level value yet | `analysis.TestCandidateLevelIsDescriptive`, `compare.TestServedLabelTestsAndCandidateValues`, `compare.TestCandidateValuesPage`, `postgres.TestDeclarationsAndCandidateValues` |
| 21 | Partly met: one taxonomy record is read on both sides, and an unmapped raw label counts as a label of its own; a record a bar pins waits on bars, and a record appended after a final comparison makes no new comparison | `analysis.TestTaxonomyReads` |
| 22 | Met | `analysis.TestDeclaredImage` |
| 23 | Met | `analysis.TestAppliesTo` |
| 24 | Met | `analysis.TestCellMeasuresExcludeLabels` |
| 25 | Met | `analysis.TestComparisonAcrossTasks`, `httpapi.TestAnalyzedImport`, `httpapi.TestComparisons` |
| 26 | Met | `analysis.TestDeclaredReasoningIsSingleVariable`, `httpapi.TestAnalyzedImport` |
| 27 | Met; the stored analysis's spend is not served | `analysis.TestUnknownCostNeverZero`, `compare.TestCostOf` |
| 28 | Met | `analysis.TestVerdictRule`, `analysis.TestReasons` |
| 29 | Partly met: `q` equals `p` for the primary result, an exploratory comparison corrects it within the family, and Benjamini and Hochberg match a reference; a reference changed, or a candidate added, after the start waits on changing an experiment | `analysis.TestPrimaryStandsAlone`, `analysis.TestExploratory`, `analysis.TestBenjaminiHochberg` |
| 30 | Met | `analysis.TestSelfReported`, `httpapi.TestAnalyzedImport` |
| 31 | Met | `analysis.TestSuiteWeights` |
| 32 | Met; the `observed.model` layer confound needs Lux's records, which no served comparison reads | `analysis.TestModelComparisonIsSingleVariable`, `analysis.TestConfounds` |
| 33 | Partly met: `incomplete`, `window` and, from v0.5.0, `uncalibrated` are served; `window` for adopted trials waits on adoption | `analysis.TestIncomplete`, `analysis.TestWindow`, `analysis.TestUncalibrated`, `compare.TestServedCalibration` |
| 34 | Met; `program` candidates are refused at create, so their half is computed in the analysis package alone | `analysis.TestProgramConfigIsSingleVariable`, `analysis.TestServiceVersions`, `analysis.TestDeclaredProvenance` |
| 35 | Met | `analysis.TestVariableIsOneLogicalVariable`, `httpapi.TestComparisonRequestRefusals` |
| 36 | Met in the analysis package; re-grading is not built | `analysis.TestReferenceReadAtCandidatesGrader` |
| 37 | Partly met: computed at the first finished trial, recomputed in place with `final` false, final at completion, and a retry adds a comparison beside the final one; a reference set after a cell left `pending` waits on changing an experiment | `httpapi.TestAnalyzedImport`, `httpapi.TestOverBudgetParksAndRetryReopens` |
| 38 | Met | `analysis.TestRecomputeFromRecordedInputs`, `analysis.TestSeedScopes` |
| 39 | Partly met, not served: entry ranking; that every pair result reads the common tasks alone is untested | `analysis.TestEntryRanking` |
| 40 | Partly met, not served: pair results and their confounds; that the verdict follows `h` is untested, since two entries give `h` equal to `p` | `analysis.TestEntryPairs`, `analysis.TestEntryPairConfounds` |
| 41 | Met, not served | `analysis.TestHolm`, `analysis.TestTableResamples` |
| 42 | Met, not served | `analysis.TestRankIntervals` |
| 43 | Met, not served | `analysis.TestRecomputeResultsTable` |
| 45 | Met | `analysis.TestUsesVendoredMath`, `mathx.TestNoAssembly` |
| 46 | Partly met: the golden comparison is held to a committed digest on whichever architecture runs it; no CI job runs it on both amd64 and arm64, and trends are not built | `analysis.TestGoldenAcrossArchitectures` |
| 47 | Met (v0.5.0) | `objects.TestSelect`, `objects.TestSelectRefusals`, `objects.TestStrataDifferInTheVariableAlone`, `analysis.TestGeneralDeclaration`, `analysis.TestGeneralDeclarationWithoutStrata`, `analysis.TestTaskVariableNeedsADeclaration`, `compare.TestGeneralInput`, `compare.TestGeneralInputWaitsAndFails`, `httpapi.TestGeneralDeclarationsRefused`, `httpapi.TestGeneralDeclarationsAndIndependence` |
| 48 | Met (v0.5.0) | `compare.TestGeneralInput`, `httpapi.TestGeneralDeclarationsRefused` |

Not built: 44, since the grid's figures are computed when it is read
and there is no cost table; 49, comparisons across batches; and trends.

Two behaviors differ from the text above: an automatic or declared
comparison turns `final` when its experiment completes, rather than
once both its sides are final; and cell-level metrics are accepted,
pinned and computed in served comparisons, ahead of the label tests
step of the build order, as are open vocabularies and their
taxonomies.

From v0.7.1 a comparison reads the values of a task's own graders, the
model judge a task names beside its checks among them, as it reads the
suite's (`compare.TestATaskGraderProducesItsValues`); v0.7.0 resolved a
task's checks alone, so a comparison of a judge's metric that only a
task named read no values. And the declared analysis is computed with no
transaction held (`postgres.TestDeclareReadsWithNoTransactionHeld`):
v0.7.0 computed it under the experiment's row lock while the analysis
read through the pool, and with a pooler in transaction mode of one
server connection per replica, two replicas recomputing one experiment
held every connection until the pooler's wait timed out.
