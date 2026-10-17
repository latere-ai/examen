---
title: "Reports: what a console reads and shows for an experiment and its comparisons, the presentation rules every figure obeys, the frozen report document, narrative as a linted draft an owner releases, and JSON, CSV and static HTML exports"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/006-grading.md
  - specs/007-measurements-and-evidence.md
  - specs/008-analysis.md
  - specs/009-bars.md
affects: [internal/view/, internal/report/, internal/narrative/, api/]
effort: medium
created: 2026-10-17
updated: 2026-10-18
author: changkun
---

# Reports

## Overview

A report is where measurements become claims a person reads and acts on,
so it carries Examen's strictest presentation rules. The people it
serves first compare agents and programs in a console: they open an
experiment, see the candidate × task grid fill in while it runs, read
per-metric verdicts with their denominators, intervals, reasons and
confounds, see which cells parked and what everything cost, and follow
any figure to the trials and evidence behind it.

A console reads three things from the API (spec 003): the grid, paged
by task; a comparison with its rows, paged; and the progress stream,
which says when to read either again. Every figure in them was
computed and stored by `examend` (spec 008), and a console computes no
statistic. A **report** is a frozen document of the same figures, which
can carry a narrative an owner released, be exported as JSON, CSV or a
static HTML bundle into Arca, and be published, which is how a report
is shared (spec 013). The console path is built first; the rest
follows it (Build order, below).

## Design

### What a console reads

- **The grid.** `GET /experiments/{id}/grid`, one page of task rows at
  a time, for the suite's primary metric or another the person picks.
  Each column's verdicts come from the candidate's automatic comparison
  against the experiment's `reference`, or from a comparison the person
  picks (spec 003).
- **A comparison.** `GET /comparisons/{id}` for its suite-level
  results, and `GET /comparisons/{id}/rows`, page by page, for its
  task-level results.
- **The stream.** `GET /experiments/{id}/stream`: a `cell`, `trial`,
  `graded` or `comparison` event says what changed, and the console
  reads the page that holds it again. Nothing is read on a timer.
- **Trials.** `GET /experiments/{id}/trials` with their current
  values, narrowed by a metric's value, such as every trial whose
  `tests_pass` is false; from a trial, its measurements with every
  generation, its configured, sent and observed layers, and its
  evidence refs (spec 007). Evidence bytes are opened from Arca with
  the viewer's own identity.

Three rules hold for everything a console shows:

- **Every figure is stored.** Each number is a member of an API answer,
  read from a stored comparison or from the stored analysis of spec
  008, cost per success and the headline's verdict counts included. A
  console formats figures by the rules below and computes no figure
  from other figures: no summary, interval, test or verdict.
- **Every figure has a source.** Each traces to a result key
  `{comparison, metric, level, task}` or a cell summary key
  `{cell, metric}`, and from a cell the console opens its trials.
- **Live.** While an experiment runs, its automatic comparisons are
  provisional, `final` false, and recomputed as trials finish (spec
  008), so the grid's verdicts appear and move as cells fill. A
  provisional figure is marked as such until it is final. A cell still
  running shows its progress as finished trials over N.

An installation may run a reference console, a client of the API that
ships apart from `examend` (spec 011); any console reads the same
answers.

### What the console and a report show

The console and every report hold these sections, in this order.

**Header.** The suite's name and digest; the experiment's id, state,
N and windows; each candidate's name, kind and digest, and for an
`external` candidate its `harness`, `version`, `model`, `reasoning` and
`flags`, marked as declared by the harness; the reference: the
baseline's name, record id, experiment, candidate and `pinned_at`, or
the reference candidate's name; the comparisons' `analysis_version`
and `parameters`, so the reader sees the `FALSE_DISCOVERY_RATE`,
`CONFIDENCE_LEVEL`, `MIN_TRIALS_FOR_VERDICT` and `MIN_SUITE_COVERAGE`
in force; and the experiment's Budget: its name, cap, spend and
remainder as the experiment's answer carries them, read through the
key source (spec 004).

**Headline.** For each automatic comparison that is not exploratory, in
candidate order:

1. First, always, the primary metric's suite-level result, whatever
   its verdict, with its delta, the delta's interval, both sides'
   summaries with their denominators, and its `reason`: an improvement,
   a regression, a change, "no clear difference" or "insufficient".
2. Then every other suite-level result whose verdict is `improved`,
   `regressed` or `changed`, with its delta, interval and `q`.
3. Then, per metric, how many task-level results were `improved`,
   `regressed`, `changed`, `within_noise` and `insufficient`.

A layer confound of a headlined result, and `incomplete` and `window`,
are named in its line with their tasks and counts or windows; they do
not take the comparison out of the headline. A result with `missing`
among its confounds is not headlined: its line says "not a finding"
with the counts of trials without a value, in place of its verdict; a
result with `self_reported` likewise, with the counts of self-reported
values, and a result with `uncalibrated` likewise, naming the judge
that no person has yet checked. A comparison whose
`declared_provenance` is not empty says in its headline which side's
provenance its harness declared. A comparison that is not
single-variable never appears in the headline. It is listed under it by
name with its tuple confounds, "not headlined: confounded by
`<fields>`", and its results appear in the verdict tables. An
exploratory comparison, computed on request or against a reference set
after the experiment started, never appears in the headline either: it
is listed under it by name, "exploratory, not headlined", and its
results appear in the verdict tables, its primary metric corrected with
the rest (spec 008).

**The candidate × task grid.** Rows are the suite's tasks in suite
order, then a suite-level row; columns are the candidates, and the
baseline's candidate when the reference is a baseline. A cell shows
the primary metric's summary with its denominator, such as
`80.0% (4/5)` or `12.3 s median (n = 5)`, and the per-task verdict of
the candidate's comparison against the reference for that metric. The
suite-level row shows `81.0% weighted over 42 tasks; 680/840 trials`.
A cell not yet complete shows its state and progress; a parked cell
its parking state and reason, and that it can be retried. A cell is
marked when it has missing values, when its values are self-reported,
when trials of it were excluded and not replaced, when a layer confound
applies to it, and when its task weighs 0; the
suite-level row carries, per candidate, the confounds its own side
carries, `missing`, `self_reported`, `observed.model` and `sent.model`,
with their counts, which a benchmark entry copies (spec 013).

**Verdict tables.** One table per comparison and metric, titled with
the comparison's variable and its two sides, and "exploratory" for an
exploratory comparison. The suite-level row comes first, then one row
per task:

| Column | Shows |
|---|---|
| Task | the task name, or "suite" with its coverage and the tasks left out |
| Candidate | summary, interval, denominator |
| Reference | summary, interval, denominator |
| Delta | delta with its `delta_kind`, and its interval |
| p, q | the uncorrected and the corrected value |
| Verdict | the verdict |
| Reason | the result's `reason` |
| Confounds | the result's confounds, in the row, with their tasks and counts |
| Calibration | for a metric a judge produces: that judge version's calibration flag, alpha, interval and pair count (spec 006); a later build step |

A `label` metric's table shows each label's proportions and their
difference with its interval, and, from label tests on, the one
verdict of the two distributions with its test, its `p` and its `q`;
before them, no verdict (spec 008). An open-vocabulary metric's table
names the taxonomy record it was read at, with its version, and shows
each unmapped raw label as a label of its own, marked unmapped, never
among the missing. A repeat rate's table is a numeric one, its
summaries and delta without intervals and each saying why; a mode
share's and a count of distinct labels' are descriptive: both sides'
summaries with their denominators and the delta, marked descriptive,
with no interval, `p`, `q` or verdict. A candidate-level metric has one
row, each side's value and their difference, marked descriptive, with
no verdict.

**Outcomes.** Per cell, the count of each outcome of spec 002.
`declined`, `timed_out` and `turn_limit` are each their own column and
never folded into a failure count; `infra_failed`, `over_budget` and
`canceled` trials and their retries are shown apart, as excluded from
analysis.

**Provenance differences.** Every cell whose sent or observed layer
differs from its configured layer: the configured model and reasoning
beside the models and reasoning values sent and the models served,
with request counts; every cell's service versions, with the tasks
where they differ between the sides of a comparison; and for an
external candidate, the harness versions and images its trials
declared, marked as declared.

**Costs.** Per candidate, and per cell:

| Column | Shows |
|---|---|
| Spend | the cost of every trial, the smoke trial, failed trials and retries included, and of the cell's judge gradings, from Lux's records; with the count of trials whose cost is unpriced or unknown, shown as such and never as zero |
| Cost per trial | the `cost_usd` summary over finished trials, with its interval and denominator |
| Cost per success | when the primary metric is binary: the spend on finished trials over the number of true values; absent when there are none |
| Judge spend | the part of the spend that judge gradings made |
| Service cost | what the APIs a program called reported their own usage cost to be (`service_usage`, spec 005), with their units, labeled as each service's own figure; shown beside Spend and never added into it or into any other column |

**The gap map**, a later build step. Rows are the tasks of the
experiment's suite version; columns are every candidate that any of
the owner's experiments on that suite version named. Each pair is one
of `run` (an experiment holds at least `MIN_TRIALS_FOR_VERDICT`
finished trials of it), `partial` (fewer), `parked` (with the parking
state and reason), or `never_run`, with the newest experiment that ran
it. Counts of each state head the map. A gap is shown as a cell, never
as blank space.

**Trends**, a later build step. When the experiment's reference is a
baseline that has earlier records, the trend of spec 008 for the
primary metric and every metric with a direction: each record's
suite-level summary with its interval, and each step's verdict and
confounds.

### Presentation rules

A console written against this spec, and the report builder, enforce
these, each with a test; they are not style guidance.

1. Only single-variable comparisons that are not exploratory are
   headlined, and a result with missing or self-reported values, or
   from an uncalibrated judge, never is; the declared primary metric's
   suite-level result leads the headline whatever its verdict.
2. Every rate, and every `mode_share`, shows its
   `numerator/denominator`, its missing values when there are any, and
   the trials outside the metric's outcomes or left out by its
   `exclude_labels` when there are any: `75.0% (3/4, 1 missing, 2 not
   applicable)`. A suite-level
   rate shows `<value> weighted over <T> tasks; <k>/<n> trials`, never
   a bare `k/n` beside a weighted value it contradicts. No bare
   percentage appears anywhere.
3. A confounded result names its confounds in its own row or
   sentence, never in a footnote; a layer confound names its tasks and
   request counts.
4. An `insufficient` result shows the word and its `reason`, which
   names the counts and what was needed; it never shows a delta as a
   finding.
5. `within_noise` reads "no clear difference", with the delta's
   interval and both sides' n, and renders muted; `improved` and
   `regressed` render with their direction; `changed` renders neutral.
6. Every verdict shows its `reason`.
7. Behavioral outcomes are their own categories (Outcomes, above).
8. Configured and observed values are never merged: a cell whose
   served model differs from its configured model shows both.
9. A summary or a delta without an interval shows its `no_interval`
   or `no_delta_interval` sentence in the interval's place, and a
   descriptive result says it is descriptive.
10. Every figure leads to its source.

**Formats.** A rate is a percentage with one decimal; a difference of
rates is in percentage points with one decimal and a sign. A p-value
and a corrected value have two significant digits, and below 0.001
read `< 0.001`. An interval is `[low, high]` in the summary's format;
a summary without one shows its `no_interval` sentence, such as "no
interval below 29 values for a 90th percentile" for a `p90` under
`MIN_TRIALS_FOR_P90_INTERVAL`. A duration is in
seconds below 120 seconds and in minutes above. A cost is in US
dollars with two decimals from one dollar up and four significant
digits below. A count is an integer.

### Reports

A report freezes the figures of an experiment so that they can be
released, exported and shared. Its fields are spec 002's: its
experiment, the comparisons it renders (final ones of that experiment,
at most `MAX_COMPARISONS_PER_REPORT`), its `document`, its `narrative`
(below), its `exports` (below) and `created_at`; its id has the prefix
`rep_`. The `document`, format `examen.report/v1`, holds the sections
above as built at creation from the grid and the comparisons it
renders, each figure with its value, its display string formatted by
the rules above and its source, so a report, its HTML export and its
CSV agree with the console; `document_digest` pins it.

Examen creates a report when an experiment reaches `complete`, with
each candidate's final automatic comparison, and on request for any
set of the experiment's final comparisons (`POST
/experiments/{id}/reports`, spec 003). A report's `document` never
changes. A later comparison, after a re-grade, appears in a new report.
A report is deleted with its experiment (spec 007).

### Narrative

Tables, the grid and verdicts render without any person. Prose that
interprets them is a claim of a different kind, so it is released by a
person and passes a mechanical linter first.

**Drafting.** An owner asks for a draft naming a pinned model id
(`POST /reports/{id}/draft`, spec 003). The draft is one structured
call through Lux, on a Key the key source leases with the purpose
`report` (spec 004), under the experiment's budget and with the
owner's identity, with a prompt fixed for each Examen release,
`examen.narrative/v1`, whose digest is recorded. The drafter reads the
report's `document` alone: names, figures, verdicts, reasons and
confounds. It never reads evidence, a task's instruction or a judge's
rationale. An owner may also write claims without a draft.

**Claims.** A narrative is a list of at most `NARRATIVE_MAX_CLAIMS`
claims, at most `NARRATIVE_MAX_BYTES` in all:

```json
{"section": "headline",
 "text": "Moving from model A to model B raised the suite pass rate by 12.0 pp [5.0, 19.0] (p = 0.003), from 70.0% weighted over 20 tasks; 140/200 trials to 82.0% weighted over 20 tasks; 164/200 trials.",
 "cites": [{"comparison": "cmp_01jab...", "metric": "sha256:...", "level": "suite"}]}
```

**The linter** rejects a claim, with the code and the offending text:

| Code | When |
|---|---|
| `no_citation` | the claim cites no result |
| `unknown_citation` | it cites a result the report does not hold |
| `headline_confounded` | a headline claim cites a result of a comparison that is not single-variable or is exploratory, or a result with `missing`, `self_reported` or `uncalibrated` |
| `confound_omitted` | a cited result has a confound the text does not name, by its name or its display label |
| `number_unsupported` | a number in the text is not, as formatted by this spec, a figure of a cited result: a summary's `value`, an interval bound, a delta, a delta interval bound, a `numerator`, a `denominator`, an `n`, a task count, a `p` or a `q` |
| `rate_without_denominator` | a rate in the text lacks its `numerator/denominator`, or for a suite-level rate its tasks and trials, in the same claim |
| `verdict_contradicted` | the text states a direction, with a word from the linter's fixed list of words for increase, decrease, better and worse, for a result whose verdict is `within_noise` or `insufficient`, or the direction opposite to its verdict |
| `insufficient_unstated` | a cited `insufficient` result is not called insufficient in the text |
| `too_long` | the narrative exceeds `NARRATIVE_MAX_CLAIMS` or `NARRATIVE_MAX_BYTES` |

The linter is versioned, its word lists are English, and its version
is recorded with every result it gives.

**Release.** The owner edits claims and their citations (`PUT
/reports/{id}/narrative`, spec 003); the linter runs again on every
edit. A narrative is released only with no finding (`POST
/reports/{id}/release`), by a subject allowed `report.release` (spec
004). Examen keeps the draft, the released text, a line-per-claim diff
between them, the drafter's model and prompt digest, the linter
version, `released_by` and `released_at`. A report holds at most one
released narrative; releasing another supersedes it and keeps it in
the history. A report without one shows "No released interpretation"
where the narrative would be, and its exports carry the same line.

### Exports

**JSON.** The report's `document` with its released narrative, in
canonical JSON, format `examen.report/v1`. Within `v1` fields are only
added.

**CSV.** One file per table, RFC 4180, UTF-8 without a byte order
mark, a header row, `.` as the decimal mark, numbers in the shortest
form that reads back to the same binary64 value, an empty field for a
value that is absent, and multiple values joined with `;`.

| File | Columns |
|---|---|
| `grid.csv` | `task`, `task_digest`, `weight`, `candidate`, `candidate_digest`, `cell`, `state`, `metric`, `value`, `interval_low`, `interval_high`, `numerator`, `denominator`, `n`, `missing`, `verdict` |
| `results.csv` | `comparison`, `variable`, `single_variable`, `exploratory`, `declared_provenance`, `metric`, `metric_name`, `level`, `task`, `candidate_value`, `candidate_low`, `candidate_high`, `candidate_numerator`, `candidate_denominator`, `candidate_n`, `reference_value`, `reference_low`, `reference_high`, `reference_numerator`, `reference_denominator`, `reference_n`, `coverage`, `delta`, `delta_kind`, `delta_low`, `delta_high`, `method`, `p`, `q`, `verdict`, `reason`, `confounds` |
| `outcomes.csv` | `cell`, `task`, `candidate`, `outcome`, `count` |
| `costs.csv` | `candidate`, `cell`, `task`, `spend_usd`, `cost_not_known`, `finished_trials`, `cost_per_trial`, `cost_low`, `cost_high`, `cost_per_success`, `judge_spend_usd`, `service_cost_usd` |
| `gaps.csv` | `task`, `candidate`, `state`, `experiment`, `reason`; once the gap map is built |
| `trends.csv` | `baseline`, `pinned_at`, `experiment`, `candidate`, `metric`, `value`, `low`, `high`, `step_verdict`, `step_q`, `step_confounds`; once trends are built |

Both are read through the API (`GET /reports/{id}` with a format, and
a table for CSV) and written into the HTML bundle.

**Static HTML bundle into Arca.** An export (`POST
/reports/{id}/exports`, spec 003) writes, with the owner's identity,
under `files/evals/reports/<report id>/<n>/`, `n` counting the
report's exports from 1:

```
index.html       the report, self-contained
report.json      the JSON export
csv/<table>.csv  the CSV exports
manifest.json    written last: each file's path, digest and size
```

`index.html` inlines its styles, needs no script to read, and makes no
request to any other origin, so it reads the same offline, from a
download and in Arca's own file view. A figure's source in it is a link
built on the installation's public address, `EXAMEN_PUBLIC_URL` (spec
011), and plain text without one; following a link needs the viewer's
own access. Every file is written create-only. Each export is recorded
on the report as `{n, path, manifest_digest, exported_at,
narrative_released}`.

### Sharing

A report is shared by publishing it, which spec 013 defines with every
other way of sharing: a report publication's version snapshots the
report's `document` and its released narrative, never a draft, at the
evidence level its owner chose, and reaches the people its owner
invites, anyone with its link, or everyone once listed. A figure's
link to trials resolves only for a viewer allowed to read the
experiment. An HTML bundle in Arca is governed by Arca's grants and
links, which are the owner's to create in Arca; Examen creates Arca
links only for published versions (spec 013).

### Limits

The constants of spec 002 this spec holds:
`MAX_COMPARISONS_PER_REPORT`, `NARRATIVE_MAX_CLAIMS`,
`NARRATIVE_MAX_BYTES`, `FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL`,
`MIN_TRIALS_FOR_VERDICT`, `MIN_SUITE_COVERAGE` and
`MIN_TRIALS_FOR_P90_INTERVAL`.

## Build order

| Step | What is built |
|---|---|
| first slice | what a console reads; the header, the headline, the grid, the verdict tables without the calibration column, outcomes, provenance differences and costs; the presentation rules |
| the `external` executor | declared provenance in the header and the headline, and self-reported values marked and kept out of the headline |
| label tests | label verdicts in the verdict tables, open-vocabulary taxonomies, cell-level and candidate-level metrics |
| publishing | reports and their document, which a report publication snapshots (spec 013); the suite-level row's per-candidate confounds |
| agent judges and annotations | the calibration column |
| static report exports | the narrative and its linter, the JSON, CSV and HTML exports |
| the gap map, trends | those two sections and their CSV files |

## Not in this spec

The statistics behind every figure (008). The routes' envelopes and
errors (003), the action vocabulary and how grants are stored (004).
The reference console, which ships apart from `examend` (011). Bar
evaluations, which a report links to (009). Publishing a report, and a
benchmark's results table, with its rank intervals and its comparisons
with the reference entry (013).
Cross-experiment and cross-suite meta-reports, and narrative in
languages other than English.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Every number the console shows for an experiment and a comparison is a member of a grid, comparison, row or trial answer, read from a stored comparison or the stored analysis or a count of stored records made by `examend`, and carries its source | `view.TestEveryFigureIsStoredAndSourced` |
| The reference console holds no routine that computes a summary, an interval, a test or a verdict, and reads a page again on a stream event, never on a timer | `console.TestNoStatisticsInTheClient`, `console.TestReadsOnStreamEvents` |
| The headline leads with the declared primary metric's suite-level result whatever its verdict, with its interval and reason; a confounded comparison and an exploratory one never appear in it and are listed with why; a result with `missing`, `self_reported` or `uncalibrated` is shown as not a finding; a layer confound, `incomplete` or `window` in one task is named in the line and leaves the comparison in the headline; a comparison with an `external` side says its provenance is declared | `view.TestHeadline` |
| No rate in the console, a JSON export, a CSV export or an HTML bundle lacks its denominator, and a suite-level rate reads "weighted over T tasks; k/n trials" | `view.TestEveryRateHasItsDenominator`, over the golden experiment |
| Confounds appear in the row of each confounded result with their tasks and counts; an insufficient result shows its reason and no delta finding; `within_noise` reads "no clear difference" with its delta interval and n | `view.TestConfoundsInline`, `view.TestInsufficientRendering`, `view.TestNoClearDifference` |
| Every verdict is shown with its `reason`, every absent interval with its sentence, and every descriptive result as descriptive | `view.TestReasonsAndAbsentIntervals` |
| Behavioral outcomes are separate columns, and `infra_failed`, `over_budget` and `canceled` trials are shown as excluded | `view.TestOutcomeColumns` |
| Spend includes smoke, failed and retried trials and judge gradings, and matches the sum of the Lux stub's records; service cost is shown beside it, labeled, and is in no other column | `view.TestCosts`, against the Lux stub |
| A report's `document` matches its digest and never changes; a re-grade yields a new report; a report names only final comparisons | `report.TestReportIsFrozen` |
| Later steps: the calibration column shows each judge's flag, alpha, interval and pair count; each linter code rejects its fixture claim and a compliant narrative passes, and a narrative with any finding cannot be released; the drafter reads only the document; a release stores its record; the CSV files have exactly their columns and read back; the HTML bundle is written under `files/evals/reports/<id>/<n>/` create-only with its manifest last and no other origin; the gap map marks every pair; a label table shows its verdict once label tests are built, and a candidate-level row is marked descriptive | `view.TestCalibrationShown`, `narrative.TestLinter`, `narrative.TestReleaseNeedsACleanLint`, `narrative.TestDrafterReadsOnlyTheDocument`, `narrative.TestReleaseRecord`, `report.TestCSVColumnsAndRoundTrip`, `report.TestHTMLBundle`, `view.TestGapMap`, `view.TestLevelsAndLabels` |
