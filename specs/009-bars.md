---
title: "Bars: rules on a comparison that pass or fail, the recorded evaluation, the release check a CI job calls, the bar events sent to the installation's sink, and Topos's release bar as the first Examen bar"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
  - specs/006-grading.md
  - specs/007-measurements-and-evidence.md
  - specs/008-analysis.md
affects: [internal/bar/, internal/events/, api/]
effort: medium
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Bars

## Overview

A bar turns a comparison into a decision: pass or fail. A team puts
one in front of a release, so that a change which makes an agent
measurably worse on the tasks that matter stops the release, and a
change within the noise does not. This spec fixes what each rule
reads and when it fails, how an evaluation is recorded so that a past
release decision can be audited, the call a CI job makes and the
result a client maps to its exit, the bar events the installation's event sink receives (spec
003), and how Topos's release bar (Topos spec 025) is expressed as an
Examen bar.

In the build order, bars, and with them the first suite moved from
Topos's release bar, follow the console path: the comparisons of spec
008 shown in a console as spec 010 fixes.

## Design

### Rules

A bar (spec 002) names a suite by owner and name, which covers every
version of it, a list of rules and `require_single_variable`. Its owner
is the owner that applied it, the suite's or one the suite is shared
with (spec 004). Each rule names a metric of that suite and one of
three kinds. `no_regression` takes a `scope` besides:

| `scope` | The results it reads |
|---|---|
| `all` (the default) | the metric's suite-level result and every task-level result |
| `suite` | the metric's suite-level result |
| `tasks` | every task-level result of the metric |

`all` is the default because spec 002 defines `no_regression` as
failing on any `regressed` verdict for its metric.

| Rule | Reads | Passes when | Fails with reason |
|---|---|---|---|
| `no_regression` | the comparison's results for the metric at its scope | no result read is `regressed`, `insufficient`, missing values, or self-reported | `regressed`, `insufficient`, `missing`, `self_reported` |
| `at_least: v` | the metric's entry in the comparison's `candidate_suite` (spec 008) | its `value` is at least `v` | `below`, `insufficient`, `missing`, `self_reported` |
| `at_most: v` | the same | its `value` is at most `v` | `above`, `insufficient`, `missing`, `self_reported` |
| `require_single_variable: true` | the comparison's `single_variable` | it is true | `confounded`, listing the tuple confounds that make it false; a layer confound or `missing` leaves `single_variable` true, and the other rules fail on missing values themselves |
| `allow_partial: false` (the default) | the suite-level results the other rules read | none leaves a task of the suite out | `incomplete`, naming each task left out and why |

- A `candidate_suite` entry is **insufficient** when the tasks it
  includes, those with at least `MIN_TRIALS_PER_TASK_IN_SUITE` valued
  trials, hold less than `MIN_SUITE_COVERAGE` of the suite's weight,
  as a suite-level result is (spec 008).
- A result read **misses values** when its `missing` is above zero on
  either side for `no_regression`, or on the candidate side for
  `at_least` and `at_most`. A missing check or judge value is a grader
  that produced nothing, and a rate computed without it would be
  inflated or deflated without anyone seeing it, so a bar fails
  closed. The one exception is a metric whose producing grader is an
  annotation grader, whose missing values are its sampling (spec 006).
- A result read is **self-reported** when its confounds hold
  `self_reported` (spec 008); for `at_least` and `at_most`, the
  metric's suite-level result is read for it. A rule that reads one
  fails: a release decision does not rest on a value an external
  harness claimed for itself. Spec 008 keeps such a value out of the
  headline; a bar fails closed on it, as it does on `missing`.
- `insufficient` fails a rule: a release decision that cannot be made
  is not a pass, as Topos spec 025 holds that an incomplete run never
  passes.
- A rule whose metric the suite version the experiment ran does not
  list reads no result, and fails with `missing`.
- A rule that fails for several reasons reports each, with the keys
  (metric, level, task) of the results behind each.

A bar **passes** when every rule passes and, if it sets
`require_single_variable`, the comparison is single-variable.
Otherwise it **fails**.

**Applying a bar** is refused, naming the rule, when a rule's metric
is not in the current version of the bar's suite; when `no_regression` names a metric whose
direction is `none`, which can never regress; when any rule names a
candidate-level metric, whose result has no verdict and no summary to
bound (spec 008); when `at_least` or
`at_most` names a `label` metric, which has no scalar summary, or a
value outside what the metric can take (`[0, 1]` for a rate, the
metric's `range` for a score, at least zero for a count, a duration
and a cost); and when the bar has more than `MAX_RULES_PER_BAR`
rules.

### Evaluation

An evaluation applies one bar version to one comparison. It is
refused with the error of spec 003 of the reason's name when:

| Reason | When |
|---|---|
| `experiment_not_complete` | the candidate's experiment is not `complete` |
| `experiment_canceled` | the candidate's experiment was canceled |
| `suite_mismatch` | the candidate's experiment ran no version of the bar's suite |
| `no_reference` | no comparison is named and the candidate is the experiment's reference candidate, which nothing is compared with |

The comparison is the one the request names, or else the newest final
comparison of the candidate against the experiment's `reference` whose
inputs are current: no grading has been appended to a trial it read
since. When there is none, Examen computes one (spec 008) and the
evaluation waits for it in state `pending`. A bar reads only a
comparison with `final` true: a provisional one can still change.

The evaluation reads only the comparison: the verdicts, summaries and
corrected values were computed and stored there, and the bar
computes no statistic. The same bar version applied to the same
comparison always gives the same result.

### The recorded evaluation

Every evaluation is kept, whatever its result, as a bar evaluation
(spec 002), immutable once complete: the bar version and its name, the
candidate side and the comparison read, its `state` and `result`, each
rule's result with its reasons, `single_variable` and `confounds`, the
`metadata` the caller attached, such as a commit, a ref and a CI run,
`created_by`, and `inputs`, a copy of everything the rules read: every
result and `candidate_suite` entry, whole, with the comparison's
`analysis_version`, `parameters` and `inputs_digest`.

`inputs` makes an evaluation self-contained: it stays readable, and
explains itself, after its comparison is deleted with an experiment
(spec 007). Reading `inputs` and `rules` alone reproduces `result`.

### The release check from CI

**The call.** `POST /suites/{suite}/bars/{ref}/evaluate` (spec 003)
asks `bar.evaluate` on the bar and `experiment.read` on the experiment
(spec 004). `{ref}` is the bar's name, which reads its current version,
or a pinned version; the body names the experiment and the candidate's
digest, or a comparison:

```json
{"experiment": "exp_01jab...",
 "candidate": "sha256:...",
 "metadata": {"commit": "9f2c1e7", "ref": "refs/tags/v1.4.0"}}
```

The evaluation records the bar digest it used. The answer is `201`
with the evaluation and its links:

```json
{"id": "bev_01jab...", "state": "complete", "result": "failed",
 "rules": [{"index": 0, "metric": "sha256:...", "metric_name": "task_passed",
            "kind": "no_regression", "scope": "suite", "result": "failed",
            "reasons": [{"reason": "regressed",
                         "results": [{"metric": "sha256:...", "level": "suite"}]}]}],
 "single_variable": true, "confounds": [],
 "links": {"evaluation": "https://examen.example/v1/evaluations/bev_01jab...",
           "comparison": "https://examen.example/v1/comparisons/cmp_01jab...",
           "console": "https://examen.example/comparisons/cmp_01jab..."}}
```

A failed bar is a `201` with `result: failed`, never an HTTP error.
The links are absolute, built on the installation's public address,
`EXAMEN_PUBLIC_URL` (spec 011), and absent without it; `console` opens
the comparison's page in a console (spec 010), from which every figure
leads to the trials and evidence behind it. An evaluation in state
`pending` has no `result` and is read again with `GET
/evaluations/{id}`.

**The result a client maps.** Examen ships no command line: the bar
check is this call and its documented result, and a client of any kind
holds a release to it. A release job creates the experiment (`POST
/experiments`, spec 003), follows its progress stream until a
`comparison` event with `final` true names its candidate, or until a
deadline of the job's own, evaluates the bar, and reads the answer. The
outcomes a client tells apart, and the exit code a command-line client
maps each to, are:

| Outcome | What the API answered | Exit code a command-line client maps it to |
|---|---|---|
| passed | `201` or `200`, `state` `complete`, `result` `passed` | 0 |
| failed | `201` or `200`, `state` `complete`, `result` `failed` | 1 |
| no evaluation, because of the request | a `4xx` other than `429`, `409 experiment_not_complete` and `409 request_in_progress`: an unknown bar, experiment or candidate (`404`, `422 reference_not_found`), `422 suite_mismatch`, `422 no_reference`, `409 experiment_canceled`, `403 forbidden`, `400` of any code; repeating the same request gives the same answer | 2 |
| no evaluation yet | `429`, a `5xx` or no answer; `409 experiment_not_complete` or `409 request_in_progress`; or an evaluation still `pending` at the job's deadline; repeating may succeed | 3 |

A release job fails on any outcome but passed. An evaluation answered
in state `pending` is read again with `GET /evaluations/{id}`, or when
the stream carries `bar.passed` or `bar.failed` for it. A client that
prints the result shows one line per failed rule with the metric, the
level, the verdict, the delta with its interval and the confounds, and
the `console` link.

### Bar events

When an evaluation completes, Examen journals one sink event (spec
002), `bar.failed` or `bar.passed`, in the log of the candidate's
experiment and in the same transaction, and delivers it to the
installation's event sink, signed and retried as spec 003 fixes for
every sink event. `bar.passed` lets a sink clear an alert it raised.

```json
{"id": "evt_01jab...", "experiment": "exp_01jab...", "seq": 4190,
 "type": "bar.failed", "at": "2026-10-17T10:41:00Z",
 "owner": "https://issuer.example|9ab3",
 "data": {"bar": {"name": "release", "digest": "sha256:..."},
          "evaluation": "bev_01jab...", "comparison": "cmp_01jab...",
          "candidate": {"name": "topos-release-candidate", "digest": "sha256:..."},
          "reference": {"baseline": "bas_01jab..."},
          "result": "failed",
          "failed_rules": [{"metric_name": "task_passed", "kind": "no_regression",
                            "reason": "regressed", "level": "suite",
                            "delta": -0.12, "delta_interval": [-0.19, -0.04], "q": 0.003}],
          "metadata": {"commit": "9f2c1e7"},
          "links": {"console": "https://examen.example/comparisons/cmp_01jab..."}}}
```

This `data` is the bar evaluation's summary that spec 003's stream and
sink carry. Like every sink event it carries ids, names, digests,
verdicts and figures, and never a transcript, a final message, a file,
a check's output, a judge's rationale, a task's instruction or a
credential.

### Topos's release bar as an Examen bar

Topos spec 025 gates every Topos release on one pinned model: the
release fails when that model's pass rate on Topos's task suite is
below a threshold, the baseline less the measured noise, or when the
run is incomplete. Other models are reported and never gate. Spec
001 moves this onto Examen.

**The suite.** Topos's task directories become an Examen suite,
`topos-release`, through the task import of spec 001 (decision 11).
Each task's checks are one check grader that runs Topos's checker and
produces `task_passed`, binary, `higher_is_better`, aggregate `rate`.
The suite's `primary_metric` is `task_passed`, so its suite-level
result is the one pre-declared test of every comparison, uncorrected
(spec 008); the suite also lists the built-ins `builtin:completed`,
`builtin:cost_usd` and `builtin:duration`, which are reported and do
not gate. Every task weighs 1.

**`task_passed` is false for a run that did not end its turn.** Topos
spec 025 passes a run only when the session ended its turn, spent no
more than its `maxCost`, and the checker passed. In Examen,
`task_passed` is true exactly when Topos's checker passes and the
trial's outcome is `completed`: a trial that ends `timed_out` or
`turn_limit` has `task_passed` false. A trial stopped because its
Budget ran out ends `over_budget`, is excluded, and parks its cell
until the cell is retried (spec 005). Spec 006 runs checks after every
end of the agent's work except an infrastructure failure, a spend
refusal or a cancel, so every finished trial has a value. The check
learns how the session ended from the session's log at
`EXAMEN_TRANSCRIPT` (spec 006), as Topos's own checker reads the log
today, and reports false unless the session ended its turn. A trial
left without a value fails the bar with `missing`.

**The candidate, the baseline and the bar.**

```yaml
# candidate: the release under test, on the gating model
name: topos-release-candidate
kind: topos
topos:
  agent: sha256:...        # the Topos agent version the release ships
  model: <the pinned gating model id>
  reasoning: <as Topos sets it>
---
# bar
name: release
suite: {owner: <the suite's owner>, name: topos-release}
rules:
  - metric: sha256:...     # task_passed
    no_regression: true
    scope: suite
require_single_variable: true
```

The baseline `main` pins the experiment and candidate of the last
accepted release on the same model. The release experiment runs the
suite against the release candidate with `trials` at least
`MIN_TRIALS_FOR_VERDICT`, names `main` as its `reference` and one Lux
Budget, and may include other models as further candidates; their
comparisons and reports appear, and the release check evaluates the
bar for the gating candidate only.

| Topos spec 025 | Examen |
|---|---|
| `bar.yaml` names one gating model | the gating candidate's `topos.model`; the bar is evaluated for that candidate only |
| baseline: the mean pass rate of five runs of the suite | the baseline `main`; the reference side's suite-level `task_passed` rate |
| noise: twice the standard deviation of those five pass rates | the primary metric's suite-level test at `FALSE_DISCOVERY_RATE`, uncorrected, from within-cell spread on both sides, with its interval at `CONFIDENCE_LEVEL` |
| threshold: baseline less noise, rounded down | no threshold figure: `no_regression` fails when the suite-level verdict is `regressed` |
| an incomplete run never passes | `allow_partial` is false, its default: a suite-level result that leaves any task out fails `incomplete`, and the evaluation's `inputs` list the tasks left out and why |
| other models report and never gate | other candidates are compared and reported; the bar is evaluated for the gating candidate |
| changing the pinned model measures a new baseline first | a release that changes the agent and the model at once differs from `main` in two logical variables, is not single-variable, and fails `require_single_variable` until `main` is moved to an experiment of the new model |
| the CI key's Lux budget and `TOPOS_TASKS_BUDGET` | the experiment's one Lux Budget |
| a task's `maxCost` caps one run | no per-trial cap: the experiment's hard Lux Budget bounds spend, since a cap derived per trial would cut off a dearer trial and bias the cell (spec 005) |
| the release job fails | the evaluation's `result` is `failed`, which a release job's client maps to a failing exit |
| the rate, the threshold and the report in the release notes | the evaluation's suite-level summaries with their denominators, the delta with its interval, `q`, and the console link |

Two differences are deliberate. Topos's threshold measured the noise
of the baseline alone and compared one run of the candidate with it.
Examen's test counts the trial-to-trial spread of both sides, so a
candidate whose own pass rate swings widely needs a larger drop to be
called a regression, and a steady one a smaller drop.

A release bar keeps Topos's rule that an incomplete run never passes:
`allow_partial` is false by default, so a parked cell fails the bar
`incomplete` until the cell is retried. A bar that sets `allow_partial`
reads a suite-level result that covers at least `MIN_SUITE_COVERAGE` of
the suite, with the tasks left out named beside it, which suits a
nightly comparison more than a release.

### Limits

The constants of spec 002 this spec holds: `MAX_RULES_PER_BAR`,
`MIN_TRIALS_FOR_VERDICT`,
`MIN_TRIALS_PER_TASK_IN_SUITE`, `MIN_SUITE_COVERAGE`,
`FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL` and `METADATA_MAX_PAIRS`.

## Not in this spec

The statistics a bar reads (008). The routes' envelope and error
shapes (003), and the action vocabulary (004). Converting Topos's task
directories, their `serve`, `search` and `answers` fields included
(spec 001, decision 11, and spec 005). What stays in Topos: the
scripted-solution tier that proves each task and checker without a
model, the replay tier, and the comparison of Topos's IR path with a
provider's own SDK, which compares two builds of Topos rather than
two candidates. Signing and delivering events to the sink (spec 003).
An owner-level webhook address; delivery to a chat or paging service,
which a sink does. Bars that block anything other than the caller's
own job. A command-line client, which Examen does not ship.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| `no_regression` at each scope fails exactly on a `regressed`, `insufficient` or value-missing result in its scope and passes otherwise; its default scope is `all` | `bar.TestNoRegressionScopes` |
| `at_least` and `at_most` compare the `candidate_suite` summary with the value, and fail `insufficient` when its coverage is below `MIN_SUITE_COVERAGE` | `bar.TestBounds` |
| With `allow_partial` false, any rule reading a suite-level result that leaves a task out fails `incomplete` and names the task; with it true, the result stands at `MIN_SUITE_COVERAGE` | `bar.TestIncompleteRun` |
| Missing values fail a rule, except for a metric an annotation grader produces | `bar.TestMissingFailsClosed` |
| A rule that reads a result with the confound `self_reported` fails with `self_reported` | `bar.TestSelfReportedFailsClosed` |
| `require_single_variable` fails a comparison with a tuple confound and lists it, and passes one whose only confounds are layer confounds | `bar.TestRequireSingleVariable` |
| Applying a bar with an unknown metric, `no_regression` on a direction-`none` metric, a rule on a candidate-level metric, a bound on a label metric, an out-of-range value, or more than `MAX_RULES_PER_BAR` rules is refused naming the rule | `bar.TestApplyRefusals` |
| An evaluation of an experiment that is not complete, canceled, on another suite version, or for its reference candidate is refused with its reason; an evaluation reads only a final comparison | `bar.TestEvaluationRefusals` |
| An evaluation computes no statistic, and two evaluations of one bar version against one comparison give the same result | `bar.TestEvaluationReadsOnlyTheComparison` |
| A complete evaluation is immutable, its `inputs` and `rules` reproduce its `result`, and it stays readable after its comparison's experiment is deleted | `bar.TestEvaluationIsSelfContained` |
| `POST /suites/{suite}/bars/{ref}/evaluate` answers `201` with `result: failed` for a failing bar and carries absolute links built on `EXAMEN_PUBLIC_URL` | `httpapi.TestBarEvaluationRoute` |
| Each case of the outcome table is answered as the table names it, so a client maps it to its exit code without reading anything else | `httpapi.TestBarCheckOutcomes` |
| A completed evaluation journals one `bar.failed` or `bar.passed` in its transaction, delivered as spec 003's sink events are, and the event holds no content field | `events.TestBarEventIsJournaled`, `events.TestNoContentInEvents` with canary strings |
| On recorded Topos suite results, the release bar fails for a clear drop in the gating model's pass rate and for a run covering less than `MIN_SUITE_COVERAGE`, passes for a change within the noise, and counts a trial that ended `timed_out` with a passing checker as not passed | `bar.TestToposReleaseBar` |
