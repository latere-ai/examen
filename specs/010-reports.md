---
title: "Reports: the live console view of an experiment and its comparisons, the presentation rules every figure obeys, narrative as a linted draft an owner releases, JSON, CSV and static HTML exports, and sharing by explicit grant"
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
updated: 2026-10-17
author: changkun
---

# Reports

## Overview

A report is where measurements become claims a person reads and acts
on, so it carries Examen's strictest presentation rules. The people it
serves first compare agents in a console: they open an experiment,
see the candidate × task grid fill in while it runs, read per-metric
verdicts with their denominators, intervals and confounds, see which
cells were never run and what everything cost, and follow any figure
to the trials and evidence behind it.

The primary surface is therefore the **live console view**, a document
the API serves for an experiment or a comparison, in which every
figure was computed and stored by `examend` (spec 008). A console
renders it and computes no statistic. A **report** is a frozen copy of
that view, which can carry a narrative an owner released, be exported
as JSON, CSV or a static HTML bundle into Arca, and be shared. The
console view is built first; the static exports follow it.

## Design

### The console view

`GET /experiments/{id}/view` and `GET /comparisons/{id}/view` (spec
003) answer a **view document**, asking `experiment.read` of the
experiment, or of the comparison's experiment (spec 004). The experiment view
holds every section below for that experiment; the comparison view
holds the header and one comparison's verdict tables in full.

- **Every figure is stored.** Each number in the document is read from
  a stored comparison or from the stored analysis of spec 008, cost
  per success and the headline's verdict counts included. The view
  builder in `examend` selects and formats stored figures and counts
  stored records, such as the gap map's pairs; it computes no
  figure from other figures. Each figure
  appears twice: its value, and its display string formatted by the
  rules below, so the console, the HTML export and the CSV agree. No
  client computes a summary, an interval, a test or a verdict.
- **Every figure has a source.** Each carries a `source`: a result
  key `{comparison, metric, level, task}`, or a cell summary key
  `{cell, metric}`. From a cell the console opens its trials, each
  with its outcome, its configured, sent and observed layers, its
  measurements with every generation, and its evidence refs (spec
  007). Evidence bytes are opened from Arca with the viewer's own
  identity.
- **Live.** While an experiment runs, the stored analysis is
  recomputed as trials finish and gradings are recorded (spec 008).
  The document carries a `version` that grows with every change and
  answers it as its `ETag`; a console sends `If-None-Match` and gets
  `304` when nothing moved. A console refreshes every
  `VIEW_REFRESH_INTERVAL`. A cell still running shows its progress as
  finished trials over N.

An installation may run a reference console, a client of the API that
ships apart from `examend` (spec 011); any console renders the same
document.

### What a report shows

The experiment view and every report hold these sections, in this
order.

**Header.** The suite's name and digest; the experiment's id, state,
N and windows; each candidate's name and digest; the baseline's name,
record id, experiment, candidate and `pinned_at`; the comparisons'
`analysis_version` and `parameters`, so the reader sees the
`FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL` and
`MIN_TRIALS_FOR_VERDICT` in force; and the Lux Budget's amount, spend
and remainder, read from Lux.

**Headline.** For each single-variable comparison, its variable, and
each suite-level result whose verdict is `improved`, `regressed` or
`changed`, with its delta, interval and `q`; then, per metric, how
many task-level results were `improved`, `regressed`, `changed`,
`within_noise` and `insufficient`. A comparison that is not
single-variable never appears in the headline. It is listed under it
by name with its confounds, "not headlined: confounded by
`<fields>`", and its results appear in the verdict tables.

**The candidate × task grid.** Rows are the suite's tasks in suite
order, then a suite-level row; columns are the candidates, and the
baseline's candidate when the experiment names a baseline. A cell
shows the primary metric's summary with its denominator, such as
`80.0% (4/5)` or `12.3 s median (n = 5)`, and the per-task verdict of
the candidate's comparison against the reference for that metric. A
cell not yet complete shows its state and progress; a parked cell its
parking state and reason. A cell is marked when it has missing values,
when a layer confound applies to it, and when its task weighs 0.

**Verdict tables.** One table per comparison and metric, titled with
the comparison's variable and its two sides. The suite-level row comes
first, then one row per task:

| Column | Shows |
|---|---|
| Task | the task name, or "suite" |
| Candidate | summary, interval, denominator |
| Reference | summary, interval, denominator |
| Delta | delta with its `delta_kind`, and its interval |
| p, q | the uncorrected and the corrected value |
| Verdict | the verdict |
| Confounds | the result's confounds, in the row |
| Calibration | for a metric a judge produces: that judge version's calibration flag, alpha, interval and pair count (spec 006) |

**Outcomes.** Per cell, the count of each outcome of spec 002.
`declined`, `timed_out`, `turn_limit` and `over_budget` are each their
own column and never folded into a failure count; `infra_failed`
trials and their retries are shown apart, as excluded from analysis.

**Provenance differences.** Every cell whose sent or observed layer
differs from its configured layer: the configured model and reasoning
beside the models and reasoning values sent and the models served,
with request counts.

**The gap map.** Rows are the tasks of the experiment's suite
version; columns are every candidate that any of the owner's
experiments on that suite version named. Each pair is one of `run`
(an experiment holds at least `MIN_TRIALS_FOR_VERDICT` finished
trials of it), `partial` (fewer), `parked` (with the parking state and
reason), or `never_run`, with the newest experiment that ran it.
Counts of each state head the map. A gap is shown as a cell, never as
blank space.

**Costs.** Per candidate, and per cell:

| Column | Shows |
|---|---|
| Spend | the cost of every trial, the smoke trial, failed trials and retries included, and of the cell's judge gradings, from Lux's records |
| Cost per trial | the `cost_usd` summary over finished trials, with its interval and denominator |
| Cost per success | when the primary metric is binary: the spend on finished trials over the number of true values; absent when there are none |
| Judge spend | the part of the spend that judge gradings made |

**Trends.** When the experiment's baseline has earlier records, the
trend of spec 008 for the primary metric and every metric with a
direction: each record's suite-level summary with its interval, and
each step's verdict and confounds.

### Presentation rules

The view builder enforces these, each with a test; they are not
style guidance.

1. Only single-variable comparisons are headlined.
2. Every rate shows its `numerator/denominator`, and its missing
   values when there are any: `75.0% (3/4, 1 missing)`. No bare
   percentage appears anywhere.
3. A confounded result names its confounds in its own row or
   sentence, never in a footnote.
4. An `insufficient` result shows the word, its denominators and the
   `MIN_TRIALS_FOR_VERDICT` it needed; it never shows a delta as a
   finding.
5. `within_noise` renders muted; `improved` and `regressed` render
   with their direction; `changed` renders neutral.
6. Behavioral outcomes are their own categories (Outcomes, above).
7. Configured and observed values are never merged: a cell whose
   served model differs from its configured model shows both.
8. Every figure leads to its source.

**Formats.** A rate is a percentage with one decimal; a difference of
rates is in percentage points with one decimal and a sign. A p-value
and a corrected value have two significant digits, and below 0.001
read `< 0.001`. An interval is `[low, high]` in the summary's format.
A duration is in seconds below 120 seconds and in minutes above. A
cost is in US dollars with two decimals from one dollar up and four
significant digits below. A count is an integer.

### Reports

A report freezes a view so that it can be released, exported and
shared. Its fields are spec 002's: its experiment, the comparisons it
renders (complete ones of that experiment, at most
`MAX_COMPARISONS_PER_REPORT`), its `document`, the view document as
built at creation in format `examen.report/v1`, with its
`document_digest`, its `narrative` (below), its `exports` (below) and
`created_at`; its id has the prefix `rep_`.

Examen creates a report when an experiment reaches `complete`, with
each candidate's comparison against the baseline when there is one,
and on request for any set of the experiment's complete comparisons
(`POST /experiments/{id}/reports`, spec 003).
A report's `document` never changes. A later comparison, after a
re-grade, appears in a new report. A report is deleted with its
experiment (spec 007).

### Narrative

Tables, the grid, the gap map and verdicts render without any person.
Prose that interprets them is a claim of a different kind, so it is
released by a person and passes a mechanical linter first.

**Drafting.** An owner asks for a draft naming a pinned model id
(`POST /reports/{id}/draft`, spec 003). The
draft is one structured call through Lux, under the experiment's
budget and with the owner's identity, with a prompt fixed for each
Examen release, `examen.narrative/v1`, whose digest is recorded. The
drafter reads the report's `document` alone: names, figures, verdicts
and confounds. It never reads evidence, a task's instruction or a
judge's rationale. An owner may also write claims without a draft.

**Claims.** A narrative is a list of at most `NARRATIVE_MAX_CLAIMS`
claims, at most `NARRATIVE_MAX_BYTES` in all:

```json
{"section": "headline",
 "text": "Moving from model A to model B raised the suite pass rate by 12.0 pp [5.0, 19.0] (q = 0.003), from 70.0% (140/200) to 82.0% (164/200).",
 "cites": [{"comparison": "cmp_01jab...", "metric": "sha256:...", "level": "suite"}]}
```

**The linter** rejects a claim, with the code and the offending text:

| Code | When |
|---|---|
| `no_citation` | the claim cites no result |
| `unknown_citation` | it cites a result the report does not hold |
| `headline_confounded` | a headline claim cites a result of a comparison that is not single-variable |
| `confound_omitted` | a cited result has a confound the text does not name, by its field name or its display label |
| `number_unsupported` | a number in the text is not, as formatted by this spec, a figure of a cited result: a summary's `value`, an interval bound, a delta, a delta interval bound, a `numerator`, a `denominator`, an `n`, a `p` or a `q` |
| `rate_without_denominator` | a rate in the text lacks its `numerator/denominator` in the same claim |
| `verdict_contradicted` | the text states a direction, with a word from the linter's fixed list of words for increase, decrease, better and worse, for a result whose verdict is `within_noise` or `insufficient`, or the direction opposite to its verdict |
| `insufficient_unstated` | a cited `insufficient` result is not called insufficient in the text |
| `too_long` | the narrative exceeds `NARRATIVE_MAX_CLAIMS` or `NARRATIVE_MAX_BYTES` |

The linter is versioned, its word lists are English, and its version
is recorded with every result it gives.

**Release.** The owner edits claims and their citations (`PUT
/reports/{id}/narrative`, spec 003); the linter runs again on every
edit. A narrative is released only with no finding (`POST
/reports/{id}/release`), by a subject allowed `report.release` (spec
004). Examen
keeps the draft, the released text, a line-per-claim diff between
them, the drafter's model and prompt digest, the linter version,
`released_by` and `released_at`. A report holds at most one released
narrative; releasing another supersedes it and keeps it in the
history. A report without one shows "No released interpretation" where
the narrative would be, and its exports carry the same line.

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
| `results.csv` | `comparison`, `variable`, `single_variable`, `metric`, `metric_name`, `level`, `task`, `candidate_value`, `candidate_low`, `candidate_high`, `candidate_numerator`, `candidate_denominator`, `candidate_n`, `reference_value`, `reference_low`, `reference_high`, `reference_numerator`, `reference_denominator`, `reference_n`, `delta`, `delta_kind`, `delta_low`, `delta_high`, `method`, `p`, `q`, `verdict`, `confounds` |
| `outcomes.csv` | `cell`, `task`, `candidate`, `outcome`, `count` |
| `costs.csv` | `candidate`, `cell`, `task`, `spend_usd`, `finished_trials`, `cost_per_trial`, `cost_low`, `cost_high`, `cost_per_success`, `judge_spend_usd` |
| `gaps.csv` | `task`, `candidate`, `state`, `experiment`, `reason` |
| `trends.csv` | `baseline`, `pinned_at`, `experiment`, `candidate`, `metric`, `value`, `low`, `high`, `step_verdict`, `step_q`, `step_confounds` |

Both are read through the API (`GET /reports/{id}` with a format, and
a table for CSV) and written into the HTML bundle.

**Static HTML bundle into Arca.** An export (`POST
/reports/{id}/exports`, spec 003) writes, with the owner's identity,
under `files/examen/reports/<report id>/<n>/`, `n` counting the
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
own access. Every file is written
create-only. Each export is recorded on the report as `{n, path,
manifest_digest, exported_at, narrative_released}`.

### Sharing

By default only the owner reads an experiment, its comparisons, its
live view and its reports.

A **share** (spec 002) is a grant, by the owner, of read access to one
owner on one report, or on one experiment with its live view and
comparisons. It is created through the API (spec 003), decided by the
authorizer (spec 004), recorded, and revoked by the owner; a revocation
holds from the authorizer's next answer.

A grantee of a report reads its `document` and its released narrative:
names, digests, figures, verdicts and confounds. A draft is never
shared. The links from a figure to trials resolve only for a viewer
allowed to read the experiment, and evidence bytes only for a viewer
Arca allows.

Examen has no anonymous reading and no link that works for whoever
holds it. An HTML bundle in Arca is governed by Arca's grants and
links, which are the owner's to create in Arca; Examen never creates
an Arca grant or link.

### Limits

The constants of spec 002 this spec holds:
`MAX_COMPARISONS_PER_REPORT`, `NARRATIVE_MAX_CLAIMS`,
`NARRATIVE_MAX_BYTES`, `VIEW_REFRESH_INTERVAL`, `FALSE_DISCOVERY_RATE`,
`CONFIDENCE_LEVEL` and `MIN_TRIALS_FOR_VERDICT`.

## Not in this spec

The statistics behind every figure (008). The routes' envelopes and
errors (003), the action vocabulary and how grants are stored (004).
The reference console, which ships apart from `examend` (011). Bar evaluations, which a
report links to (009). Cross-experiment and cross-suite meta-reports,
a public leaderboard, and narrative in languages other than English.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Every number in an experiment view and a comparison view is read from a stored comparison or the stored analysis, or is a count of stored records made by `examend`, and carries its source | `view.TestEveryFigureIsStoredAndSourced` |
| The reference console holds no routine that computes a summary, an interval, a test or a verdict | `console.TestNoStatisticsInTheClient` |
| A view answers `304` to its own `ETag` until a trial finishes or a grading is recorded, and a running cell shows its progress | `view.TestLiveViewVersioning` |
| A confounded comparison never appears in the headline and is listed with its confounds | `view.TestHeadlineIsSingleVariableOnly` |
| No rate in a view, a JSON export, a CSV export or an HTML bundle lacks its denominator | `view.TestEveryRateHasItsDenominator`, over the golden experiment |
| Confounds appear in the row of each confounded result; an insufficient result shows its counts and no delta finding | `view.TestConfoundsInline`, `view.TestInsufficientRendering` |
| A result of a metric a judge produces shows that judge version's calibration flag, alpha, interval and pair count | `view.TestCalibrationShown` |
| Behavioral outcomes are separate columns and `infra_failed` trials are shown as excluded | `view.TestOutcomeColumns` |
| The gap map marks every task and candidate pair of the suite version as `run`, `partial`, `parked` or `never_run`, with counts | `view.TestGapMap` |
| Spend includes smoke, failed and retried trials and judge gradings, and matches the sum of the Lux stub's records | `view.TestCosts`, against the Lux stub |
| Each linter code rejects its fixture claim and a compliant narrative passes; a narrative with any finding cannot be released | `narrative.TestLinter`, `narrative.TestReleaseNeedsACleanLint` |
| The drafter's request holds the report document and no evidence, instruction or rationale | `narrative.TestDrafterReadsOnlyTheDocument`, with canary strings |
| A release stores the draft, the released text, their diff, the model, the prompt digest, the linter version and the releaser; a report without a release says so in every export | `narrative.TestReleaseRecord` |
| A report's `document` matches its digest and never changes; a re-grade yields a new report | `report.TestReportIsFrozen` |
| The CSV files have exactly the columns of their table and read back to the document's values | `report.TestCSVColumnsAndRoundTrip` |
| The HTML bundle is written under `files/examen/reports/<id>/<n>/` create-only with its manifest last, and `index.html` references no other origin and needs no script | `report.TestHTMLBundle`, against the Arca stub |
| A subject without a share is refused a report; a share lets one subject read it; revoking it refuses again; no route serves a report anonymously | `report.TestSharing`, against a test authorizer |
