---
title: "Bars: rules on a comparison that pass or fail, the recorded evaluation, the release check a CI job calls, signed alerts when a bar fails, and Topos's release bar as the first Examen bar"
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
affects: [internal/bar/, internal/events/, cmd/examen/, api/]
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
release decision can be audited, the call and the command a CI job
uses, the signed alert sent when a bar fails, and how Topos's release
bar (Topos spec 025) is expressed as an Examen bar.

In the build order, bars, and with them the first suite moved from
Topos's release bar, follow the console path: the comparisons of spec
008 shown in the console view of spec 010.

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
| `no_regression` | the comparison's results for the metric at its scope | no result read is `regressed`, `insufficient`, or missing values | `regressed`, `insufficient`, `missing` |
| `at_least: v` | the metric's entry in the comparison's `candidate_suite` (spec 008) | its `value` is at least `v` | `below`, `insufficient`, `missing` |
| `at_most: v` | the same | its `value` is at most `v` | `above`, `insufficient`, `missing` |
| `require_single_variable: true` | the comparison's `single_variable` | it is true | `confounded`, listing the comparison's `confounds` |

- A `candidate_suite` entry is **insufficient** when any task of the
  candidate's suite with weight greater than zero has fewer than
  `MIN_TRIALS_FOR_VERDICT` valued trials.
- A result read **misses values** when its `missing` is above zero on
  either side for `no_regression`, or on the candidate side for
  `at_least` and `at_most`. A missing check or judge value is a grader
  that produced nothing, and a rate computed without it would be
  inflated or deflated without anyone seeing it, so a bar fails
  closed. The one exception is a metric whose producing grader is an
  annotation grader, whose missing values are its sampling (spec 006).
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
direction is `none`, which can never regress; when `at_least` or
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
| `no_reference` | no comparison is named and the experiment names no baseline |

The comparison is the one the request names, or else the newest
complete comparison of the candidate against the experiment's
baseline whose inputs are current: no grading has been appended to a
trial it read since. When there is none, Examen computes one (spec
008) and the evaluation waits for it in state `pending`.

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
the comparison's console view (spec 010), from which every figure
leads to the trials and evidence behind it. An evaluation in state
`pending` has no `result` and is read again with `GET
/evaluations/{id}`.

**The command.** The command-line client wraps the call:

```
examen bar check --suite <name> [--suite-owner <owner>] --bar <name|digest>
                 --experiment <id> --candidate <name|digest>
                 [--comparison <id>] [--wait <duration>] [--meta key=value]... [--json]
```

It resolves a candidate's name to the digest of its current version
before the call. With `--wait`, it reads the experiment and the evaluation every
`BAR_POLL_INTERVAL` until the experiment is complete or canceled and
the evaluation is complete, or until the duration has passed. It
prints the result, one line per failed rule with the metric, the
level, the verdict, the delta with its interval and the confounds,
and the console link; `--json` prints the evaluation instead.

| Exit code | Meaning |
|---|---|
| 0 | the bar passed |
| 1 | the bar failed |
| 2 | no evaluation, because of the request: an unknown bar, experiment or candidate, `suite_mismatch`, `no_reference`, `experiment_canceled`, a refusal by the authorizer, or invalid flags. Retrying the same command gives the same answer |
| 3 | no evaluation yet: the service did not answer or answered with a server error, or the experiment did not complete within `--wait`. Retrying may succeed |

A release job fails on any code other than 0. A job applies the
experiment and runs the check in one chain, so the job's status is the
check's (the experiment command is spec 003's; this shows its shape):

```sh
exp="$(examen experiment apply -f release-experiment.yaml --output id)" &&
examen bar check --suite topos-release --bar release --experiment "$exp" \
  --candidate topos-release-candidate --wait 6h --meta commit="$GITHUB_SHA"
```

### Alerts

When an evaluation completes, Examen journals one event (spec 002),
`bar.failed` or `bar.passed`, in the log of the candidate's experiment
and in the same transaction, and delivers it to the installation's
event sink. `bar.passed` lets a sink clear an alert it raised.

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

An event carries ids, names, digests, verdicts and figures. It never
carries a transcript, a final message, a file, a check's output, a
judge's rationale, a task's instruction or a credential.

**The sink.** `EXAMEN_EVENTS_URL` (spec 011) receives a `POST` with
`Content-Type: application/json`, the event as journaled, byte for
byte, and

```
Examen-Signature: t=<unix seconds of this attempt>,v1=<hex HMAC-SHA256 of "<t>.<body>" under the first secret>[,v1=<the same under the second>]
```

The file `EXAMEN_EVENTS_SECRET_FILE` names (spec 011) holds one secret, or two separated by a comma
while a secret is rotated, and then both signatures are sent. The
signature is recomputed on every attempt. A sink verifies by
recomputing the HMAC over the exact bytes, comparing in constant time
against each `v1`, and refusing a `t` more than `SIGNATURE_MAX_AGE`
from its own clock. This is the format Cella and Lux sign their
events with, so one sink verifies all three.

**Delivery** runs on one replica at a time, the holder of a Postgres
advisory lock, and is at least once, in journal order per bar. The
event's `delivery` is `pending` until it is acknowledged or dropped.

| Answer | Then |
|---|---|
| 2xx | acknowledged |
| 408, 429, 5xx, a connection failure, or no answer within `ALERT_TIMEOUT` | retried with exponential backoff from `ALERT_BACKOFF_MIN` to `ALERT_BACKOFF_MAX` |
| 401 | retried the same way and logged at error: the two ends hold different secrets, and dropping would lose every event of a botched rotation |
| any other 4xx | dropped at once and counted: a sink that refuses the body will refuse it again |
| still unacknowledged after `ALERT_RETRY_WINDOW` | dropped, counted and logged |

A URL without a secret, a secret without a URL, and a non-loopback
`http://` URL each stop the start (spec 011). Without a URL, events are
journaled with `delivery` `acknowledged` and nothing is sent.

Examen keeps no per-owner webhook address or secret. The event names
its owner, and an installation serving many owners routes each event
to the owner's chosen destination in its sink.

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
The suite's `primary_metric` is `task_passed`; it also lists the
built-ins `completed`, `cost_usd` and `duration`, which are reported
and do not gate. It lists nothing else, since every metric it lists
is one more result in the suite family (spec 008). Every task weighs
1.

**`task_passed` is false for a run that did not end its turn.** Topos
spec 025 passes a run only when the session ended its turn, spent no
more than its `maxCost`, and the checker passed. In Examen,
`task_passed` is true exactly when Topos's checker passes and the
trial's outcome is `completed`: a trial that ends `timed_out`,
`turn_limit` or `over_budget` has `task_passed` false. Spec 006 runs
checks after every end of the agent's work except an infrastructure
failure or a cancel, so every finished trial has a value. The check
learns how the session ended from the session's log at
`EXAMEN_TRANSCRIPT` (spec 006), as Topos's own checker reads the log
today, and reports false unless the session ended its turn within its
budget. A trial left without a value fails the bar with `missing`.

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
`MIN_TRIALS_FOR_VERDICT`, names `main` as its baseline and one Lux
Budget, and may include other models as further candidates; their
comparisons and reports appear, and the release check evaluates the
bar for the gating candidate only.

| Topos spec 025 | Examen |
|---|---|
| `bar.yaml` names one gating model | the gating candidate's `topos.model`; the bar is evaluated for that candidate only |
| baseline: the mean pass rate of five runs of the suite | the baseline `main`; the reference side's suite-level `task_passed` rate |
| noise: twice the standard deviation of those five pass rates | the test of the suite-level result at `FALSE_DISCOVERY_RATE`, from within-cell spread on both sides, with its interval at `CONFIDENCE_LEVEL` |
| threshold: baseline less noise, rounded down | no threshold figure: `no_regression` fails when the suite-level verdict is `regressed` |
| an incomplete run never passes | a parked cell or a task without enough trials makes the result `insufficient`, which fails |
| other models report and never gate | other candidates are compared and reported; the bar is evaluated for the gating candidate |
| changing the pinned model measures a new baseline first | a release that changes the agent and the model at once differs from `main` in two logical variables, is not single-variable, and fails `require_single_variable` until `main` is moved to an experiment of the new model |
| the CI key's Lux budget and `TOPOS_TASKS_BUDGET` | the experiment's one Lux Budget |
| a task's `maxCost` caps one run | no per-trial cap: the experiment's hard Lux Budget bounds spend, since a cap derived per trial would cut off a dearer trial and bias the cell (spec 005) |
| the release job fails | `examen bar check` exits 1 |
| the rate, the threshold and the report in the release notes | the evaluation's suite-level summaries with their denominators, the delta with its interval, `q`, and the console link |

One difference is deliberate. Topos's threshold measured the noise of
the baseline alone and compared one run of the candidate with it.
Examen's test counts the trial-to-trial spread of both sides, so a
candidate whose own pass rate swings widely needs a larger drop to be
called a regression, and a steady one a smaller drop.

### Limits

The constants of spec 002 this spec holds: `MAX_RULES_PER_BAR`,
`BAR_POLL_INTERVAL`, `ALERT_TIMEOUT`, `ALERT_BACKOFF_MIN`,
`ALERT_BACKOFF_MAX`, `ALERT_RETRY_WINDOW`, `SIGNATURE_MAX_AGE`,
`MIN_TRIALS_FOR_VERDICT`, `FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL`
and `METADATA_MAX_PAIRS`.

## Not in this spec

The statistics a bar reads (008). The routes' envelope and error
shapes (003), and the action vocabulary (004). Converting Topos's task
directories, their `serve`, `search` and `answers` fields included
(spec 001, decision 11, and spec 005). What stays in Topos: the
scripted-solution tier that proves each task and checker without a
model, the replay tier, and the comparison of Topos's IR path with a
provider's own SDK, which compares two builds of Topos rather than
two candidates. An owner-level webhook address; delivery to a chat or
paging service, which a sink does. Bars that block anything other
than the caller's own job.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| `no_regression` at each scope fails exactly on a `regressed`, `insufficient` or value-missing result in its scope and passes otherwise; its default scope is `all` | `bar.TestNoRegressionScopes` |
| `at_least` and `at_most` compare the `candidate_suite` summary with the value, and fail `insufficient` when any weighted task has fewer than `MIN_TRIALS_FOR_VERDICT` valued trials | `bar.TestBounds` |
| Missing values fail a rule, except for a metric an annotation grader produces | `bar.TestMissingFailsClosed` |
| `require_single_variable` fails a confounded comparison and lists its confounds | `bar.TestRequireSingleVariable` |
| Applying a bar with an unknown metric, `no_regression` on a direction-`none` metric, a bound on a label metric, an out-of-range value, or more than `MAX_RULES_PER_BAR` rules is refused naming the rule | `bar.TestApplyRefusals` |
| An evaluation of an experiment that is not complete, canceled, on another suite version, or without a reference is refused with its reason | `bar.TestEvaluationRefusals` |
| An evaluation computes no statistic, and two evaluations of one bar version against one comparison give the same result | `bar.TestEvaluationReadsOnlyTheComparison` |
| A complete evaluation is immutable, its `inputs` and `rules` reproduce its `result`, and it stays readable after its comparison's experiment is deleted | `bar.TestEvaluationIsSelfContained` |
| `POST /suites/{suite}/bars/{ref}/evaluate` answers `201` with `result: failed` for a failing bar and carries absolute links built on `EXAMEN_PUBLIC_URL` | `httpapi.TestBarEvaluationRoute` |
| `examen bar check` exits 0, 1, 2 and 3 in the cases of its table, and with `--wait` polls every `BAR_POLL_INTERVAL` | `cli.TestBarCheckExitCodes`, against a stub server and a fake clock |
| A completed evaluation journals one `bar.failed` or `bar.passed` in its transaction, and the event holds no content field | `events.TestBarEventIsJournaled`, `events.TestNoContentInEvents` with canary strings |
| The signature verifies with the documented formula for each of two secrets, is fresh on every attempt, and fails for a body changed by one byte | `events.TestSignature` |
| Delivery retries, holds a 401, drops another 4xx at once, drops after `ALERT_RETRY_WINDOW`, and runs on one replica | `events.TestDelivery`, under a fake clock |
| A URL without a secret, a secret without a URL, and a non-loopback `http://` URL stop the start | `events.TestSinkStartupRules` |
| On recorded Topos suite results, the release bar fails for a clear drop in the gating model's pass rate and for an incomplete run, passes for a change within the noise, and counts a trial that ended `timed_out` with a passing checker as not passed | `bar.TestToposReleaseBar` |
