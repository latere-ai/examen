---
title: "Objects: metric, grader, task, suite, candidate, experiment, cell, trial, measurement, baseline, comparison, bar, bar evaluation, share, dataset export, report and event; their fields, states, identifiers and limits"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
affects: [api/, internal/store/, internal/id/]
effort: medium
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Objects

## Overview

Seventeen objects carry everything Examen keeps. This spec fixes each
one's fields, states and identifier, the words the other specs use for
them, and every limit as a named constant. A field written here is the
one place it is defined; the API (003) exposes these shapes and the
other specs refer to them by name.

## Glossary

| Word | Meaning | Not to be confused with |
|---|---|---|
| task | one versioned unit of work an agent attempts, with its checks | a Topos session's goal; Topos spec 025's tasks are the same concept and move here |
| check | a command run in the trial's machine after the agent ends, producing measurements | a CI check |
| suite | an owned set of tasks with the metrics and graders they are measured by | Topos spec 025's suite, which becomes an Examen suite |
| metric | a named, typed quantity with a direction | a telemetry metric |
| grader | what turns a trial into measurements: a check, a model judge, an agent judge, or an annotation queue | |
| judge | a grader that uses a model: a model judge or an agent judge | |
| decline judge | what produces the built-in metric `declined`: the built-in decline judge, `builtin:decline`, or the grader a suite names in `decline_judge` (spec 006) | the outcome `declined`, which the executor sets |
| candidate | one fully pinned agent configuration under evaluation | the authorizer's `subject`, which is the caller |
| experiment | one execution of a suite against candidates | |
| cell | one task × one candidate in an experiment; the unit of comparison | |
| trial | one attempt by a candidate at a task | Topos's retired word "run"; a trial of a Topos agent is one Topos session |
| finished trial | a trial whose outcome is neither `infra_failed` nor `canceled` and that was not retried; the trials analysis counts | a trial in state `done`, which may have failed or been canceled |
| rollout | one submission of some of a cell's trials to its executor: the smoke rollout, the main rollout, retry rollouts, and an agent judge's rollouts (specs 005 and 006) | a deployment rollout, or a rollout identity |
| measurement | one value of one metric for one trial, from one grader generation | |
| baseline | a record that pins one candidate's cells in one complete experiment as the reference; a baseline's name identifies a lineage of records | |
| comparison | the per-metric verdicts of a candidate's cells against a reference | |
| verdict | `improved`, `regressed`, `changed`, `within_noise` or `insufficient`, per metric | |
| changed | the verdict of a significant change in a metric whose direction is `none`, which is neither better nor worse | `improved` and `regressed`, which need a direction |
| bar | a rule on a comparison that passes or fails | a lateregate gate |
| bar evaluation | one bar version applied to one comparison, recorded with what it read | |
| share | an owner's grant to another owner of a suite, a baseline, an experiment or a report | |
| dataset export | trials with their measurements and evidence references, written as records into the owner's Arca space | an export of a report |
| report | a frozen copy of an experiment's view, with the narrative its owner released | the live view, which changes while an experiment runs |
| event | one entry of an experiment's event log | a telemetry event |

## Identifiers

`internal/id` mints prefixed, time-ordered identifiers: a prefix, an
underscore and a ULID in lowercase. A versioned object also has a
**digest**, `sha256:` and the hex of the SHA-256 of its canonical
JSON (keys sorted, no insignificant whitespace), which is what a pinned
reference names.

| Object | Prefix | Versioned by digest |
|---|---|---|
| metric | `met_` | yes |
| grader | `grd_` | yes |
| task | `tsk_` | yes |
| suite | `sui_` | yes |
| candidate | `cnd_` | yes |
| experiment | `exp_` | no |
| cell | `cel_` | no |
| trial | `tri_` | no |
| measurement | `msr_` | no |
| baseline | `bas_` | no |
| comparison | `cmp_` | no |
| bar | `bar_` | yes |
| bar evaluation | `bev_` | no |
| share | `shr_` | no |
| dataset export | `dsx_` | no |
| report | `rep_` | no |
| event | `evt_` | no |

Every object has `id`, `owner`, `created_at` and, when mutable,
`updated_at`. `owner` is the subject that owns it, a person or an
organization, exactly as the authorizer names it (spec 004). Objects of
one owner never reference another owner's objects except through a
share (spec 004): an experiment may run a suite and name a baseline
shared with its owner, and a baseline or a bar may name a suite shared
with its owner.

A versioned object is immutable once applied: applying changed content
makes a new version with a new digest under the same `name`. Each
version has its own id, and `name@digest` also addresses it (spec
003). Every reference to a version pins a digest, never a name alone.
The one reference by name is a baseline's or a bar's `suite`, which
names a suite, every version of it, by its owner and name.

Two grader digests are reserved and name no applied version:
`builtin`, Examen's filling of the built-in metrics from the trial
record, and `builtin:decline`, the built-in decline judge.

**Metadata.** Every object that has `metadata` (task, candidate,
experiment, bar evaluation) holds at most `METADATA_MAX_PAIRS` pairs of
string keys and values. A key beginning `examen.` is reserved for the
labels Examen writes (spec 005) and is refused on any object a client
writes.

## Limits

Every limit is defined here once and referred to by name everywhere
else. The last column names the spec that explains its use.

| Constant | Value | Meaning | Spec |
|---|---|---|---|
| `DEFAULT_TRIALS` | 5 | trials per cell when an experiment names no N | 002 |
| `MAX_TRIALS_PER_CELL` | 1000 | the most trials one cell may declare | 002 |
| `SMOKE_TRIALS` | 1 | trials a cell runs before the rest | 005 |
| `MIN_TRIALS_FOR_VERDICT` | 5 | finished trials, holding a current value of the metric, each side needs before a verdict other than `insufficient` | 008 |
| `MAX_INFRA_RETRIES` | 2 | retries of a trial that failed for an infrastructure reason | 005 |
| `MAX_CELLS_PER_EXPERIMENT` | 2000 | tasks × candidates in one experiment | 003 |
| `MAX_TASKS_PER_SUITE` | 1000 | tasks in one suite version | 002 |
| `MAX_METRICS_PER_SUITE` | 64 | metrics in one suite version | 002 |
| `MAX_CANDIDATES_PER_EXPERIMENT` | 32 | candidates in one experiment | 003 |
| `MAX_ACTIVE_EXPERIMENTS_PER_OWNER` | 16 | experiments of one owner neither `complete` nor `canceled` | 004 |
| `DEFAULT_PARALLELISM` | 8 | trials of one experiment running at once, unless set lower or higher | 005 |
| `MAX_PARALLELISM` | 256 | the highest parallelism an experiment may set | 005 |
| `DEFAULT_TRIAL_TIMEOUT` | 30 minutes | wall time of one trial when a task names none | 005 |
| `MAX_TRIAL_TIMEOUT` | 24 hours | the longest timeout a task may set | 005 |
| `CHECK_TIMEOUT` | 10 minutes | wall time of all of one trial's checks together | 006 |
| `TRIAL_GRACE` | 10 minutes | a trial's time past its `timeout` and `CHECK_TIMEOUT` before examend cancels it as `deadline_passed` | 005 |
| `TRIAL_START_TIMEOUT` | 1 hour | how long a trial may stay pending while its rollout runs fewer trials than its parallelism | 005 |
| `REPORT_INTERVAL` | 15 seconds | how often an open rollout is reported | 005 |
| `USAGE_SETTLE_DELAY` | 2 minutes | the wait after a trial ends before Lux's records of it are read | 005 |
| `COST_ESTIMATE_MARGIN` | 1.5 | the factor a cell's cost estimate is multiplied by at the gate | 005 |
| `ADOPTION_MAX_AGE` | 30 days | the oldest trials a new experiment may adopt instead of re-running | 005 |
| `CHECK_MAX_OUTPUT_BYTES` | 64 KiB | the stdout of one check examend reads, and the stderr tail a check run record keeps; the whole of both stays in evidence | 006 |
| `JUDGE_MAX_INPUT_BYTES` | 512 KiB | evidence one model judge call may read | 006 |
| `JUDGE_MAX_OUTPUT_TOKENS` | 4096 | the output one model judge call may produce | 006 |
| `JUDGE_MAX_ATTEMPTS` | 2 | calls one judgment may make when the answer is malformed | 006 |
| `JUDGE_MAX_RATIONALE_BYTES` | 4 KiB | one rationale, a judge's or a person's | 006 |
| `AGENT_JUDGE_TIMEOUT` | 30 minutes | the turn timeout of one agent judge session | 006 |
| `ANNOTATION_CLAIM_TTL` | 30 minutes | how long a claimed annotation item waits for its answer | 006 |
| `CALIBRATION_MIN_PAIRS` | 30 | judged and annotated trials a judge needs before its agreement is read | 006 |
| `CALIBRATION_MIN_AGREEMENT` | 0.67 | the alpha below which a judge's measurements of a metric are flagged uncalibrated | 006 |
| `EXPORT_RECORDS_PER_FILE` | 1000 | records in one `records-*.jsonl` file of a dataset export | 007 |
| `MAX_EXPORT_RECORDS` | 2,000,000 | records in one dataset export; one experiment at the limits of this spec fits | 007 |
| `MAX_EXPORT_CONDITIONS` | 16 | metric conditions in one dataset export's filter | 007 |
| `MAX_EXPORT_EXPERIMENTS` | 100 | experiments one dataset export's filter names | 007 |
| `FALSE_DISCOVERY_RATE` | 0.05 | the rate the multiple-comparison correction holds | 008 |
| `CONFIDENCE_LEVEL` | 0.95 | the level of every reported interval | 008 |
| `BOOTSTRAP_RESAMPLES` | 10,000 | resamples of every bootstrap interval | 008 |
| `PERMUTATION_RESAMPLES` | 10,000 | relabelings of a Monte Carlo permutation test | 008 |
| `PERMUTATION_EXACT_MAX` | 100,000 | the most relabelings a permutation test enumerates instead of sampling | 008 |
| `TIE_TOLERANCE` | 1e-9 | relative tolerance when a test compares a probability or a discrepancy with the observed one | 008 |
| `MAX_TREND_POINTS` | 100 | baseline records one trend reads | 008 |
| `MAX_RULES_PER_BAR` | 128 | rules in one bar version | 009 |
| `BAR_POLL_INTERVAL` | 15 seconds | how often `examen bar check --wait` reads the experiment and the evaluation | 009 |
| `ALERT_TIMEOUT` | 10 seconds | how long one event delivery attempt waits for the sink | 009 |
| `ALERT_BACKOFF_MIN` | 1 second | the first delivery retry delay | 009 |
| `ALERT_BACKOFF_MAX` | 5 minutes | the longest delivery retry delay | 009 |
| `ALERT_RETRY_WINDOW` | 24 hours | how long an event is retried before it is dropped | 009 |
| `SIGNATURE_MAX_AGE` | 5 minutes | the oldest signature time a sink accepts | 009 |
| `MAX_COMPARISONS_PER_REPORT` | 64 | comparisons one report renders | 010 |
| `NARRATIVE_MAX_CLAIMS` | 40 | claims in one narrative | 010 |
| `NARRATIVE_MAX_BYTES` | 32 KiB | the text of one narrative | 010 |
| `VIEW_REFRESH_INTERVAL` | 10 seconds | how often a console reads a live view again | 010 |
| `DEFAULT_PAGE_SIZE` | 50 | items in a page when `limit` is absent | 003 |
| `MAX_PAGE_SIZE` | 200 | the largest `limit` | 003 |
| `MAX_REQUEST_BYTES` | 4 MiB | the longest request body examend reads | 003 |
| `API_REQUESTS_PER_MINUTE` | 600 | requests one subject may make to one replica in a minute | 012 |
| `IDEMPOTENCY_WINDOW` | 24 hours | how long an `Idempotency-Key` answers its first response | 003 |
| `IDEMPOTENCY_KEY_MAX_BYTES` | 255 | the longest `Idempotency-Key` | 003 |
| `STREAM_POLL_INTERVAL` | 1 second | how often an open stream reads the event log for new entries | 003 |
| `STREAM_KEEPALIVE` | 15 seconds | the longest a stream stays silent before a keepalive comment | 003 |
| `MAX_STREAMS_PER_SUBJECT` | 10 | streams one subject holds open on one replica | 003 |
| `OWNER_TOKEN_MAX_LIFETIME` | 15 minutes | the lifetime Examen asks of the minter, and the longest it uses any owner token after obtaining it | 004 |
| `OWNER_TOKEN_RENEWAL` | 2 minutes | how long before its expiry a cached owner token is replaced, and how long a token file's read is reused | 004 |
| `METADATA_MAX_PAIRS` | 16 | pairs in the `metadata` of any object | 002 |
| `SHUTDOWN_GRACE` | 25 seconds | how long open requests have to finish after the stop signal | 011 |
| `IMAGE_MAX_SIZE` | 40 MiB | the largest the `examend` image may be | 011 |

## Metric

A named, typed quantity a suite is measured on.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner, `[a-z][a-z0-9_]{0,62}` |
| `type` | enum | `binary` (true or false), `score` (a number in a declared range), `count`, `duration`, `cost`, `label` (one of declared values) |
| `range` | [min, max] | for `score`; required |
| `labels` | []string | for `label`; the allowed values |
| `direction` | enum | `higher_is_better`, `lower_is_better`, `none` (a label has `none`) |
| `aggregate` | enum | how a cell summarizes its trials: `rate` (binary), `mean`, `median`, `p90`, `distribution` (label) |
| `unit` | string | for display, such as `s` or `USD` |
| `description` | string | for people |

**Built-in metrics** exist for every owner, with these types,
aggregates and directions:

| Metric | Type | Aggregate | Direction | Value |
|---|---|---|---|---|
| `completed` | `binary` | `rate` | `higher_is_better` | the agent ended its work itself: outcome `completed` or `declined` |
| `declined` | `binary` | `rate` | `lower_is_better` | the agent refused the task, said it would not or could not do it, or stopped without attempting it, as the decline judge decides (spec 006) |
| `cost_usd` | `cost` | `mean` | `lower_is_better` | the trial's cost from Lux's records, in USD |
| `duration` | `duration` | `median` | `lower_is_better` | `agent_ended_at` less `started_at`, in seconds |
| `input_tokens`, `output_tokens`, `reasoning_tokens` | `count` | `mean` | `none` | the sums of Lux's token counts for the trial |
| `turns`, `tool_calls` | `count` | `median` | `none` | the agent's responses and tool calls, from the executor's records |

Examen fills every built-in metric but `declined` from the trial record,
with grader `builtin`, for every trial whose outcome is neither
`infra_failed` nor `canceled` (spec 005). `declined` is a measurement
the decline judge produces (spec 006). A trial's outcome is set once,
by its executor, and is final: the decline judge never changes it.

## Grader

What turns a trial into measurements. A grader declares the metrics it
produces; a measurement for a metric the grader did not declare is
refused.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner |
| `kind` | enum | `check`, `model_judge`, `agent_judge`, `annotation` |
| `produces` | []metric digest | the metrics it fills |
| `check` | object | for `check`: `command` ([]string, run in the trial's machine), `workdir`, `env`, and `files` (optional: an owner's Arca path and its content digest, delivered read-only to the check). Output contract in spec 006 |
| `model_judge` | object | for `model_judge`: `model` (a pinned model id), `prompt` (text), `rubric` (text), `inputs` (which evidence it reads: `transcript`, `final_message`, named `files`), `temperature`, `reasoning` (optional: the reasoning level the call names; the model's default when absent), `zero_retention` (bool: its Lux Key allows only providers that keep nothing), `preamble` (Examen's judge preamble, pinned at apply) |
| `agent_judge` | object | for `agent_judge`: `agent` (a pinned Topos agent version digest), `inputs`, `instructions`, `preamble` (Examen's agent judge preamble, pinned at apply) |
| `annotation` | object | for `annotation`: `instructions` for the person, `sample` (the fraction of trials to queue) |

A grader's digest covers every field above. Changing a judge's model,
prompt, rubric, inputs, temperature, reasoning, preamble or
`zero_retention` therefore makes a new grader version, and its
measurements are a new generation (spec 006).

**One producer per metric.** For each task of a suite version, each
metric the suite lists has at most one producer that is not an
annotation grader: one of the task's `checks`, the suite's `graders` and,
for `declined`, the suite's decline judge; `builtin` for the other
built-in metrics. A suite version that gives one task two such
producers of one metric is refused at apply, naming the metric and both
graders. An annotation grader may produce a metric a judge also
produces, and then calibrates the judge (spec 007).

## Task

One versioned unit of work.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner; a suite version refuses two tasks of one name |
| `instruction` | text | the first message the agent receives |
| `workspace` | object | where the trial starts: `none`, `origo` (a repository and a full commit id), or `arca` (an owner's path and its content digest) |
| `environment` | object | the Cella sandbox template the trial runs in, pinned by image digest, with its resources and network policy |
| `checks` | []grader digest | graders of kind `check`, run in order after the agent ends |
| `timeout` | duration | the trial's wall time, at most `MAX_TRIAL_TIMEOUT`; `DEFAULT_TRIAL_TIMEOUT` when unset |
| `max_turns` | int | optional bound on the model responses of the agent's own thread; the `topos` executor needs a step bound Topos does not have today (spec 005, requirement R7), and the `cella` executor passes it as advice |
| `evidence` | []path | files of the workspace kept as evidence besides the transcript |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

The task's digest is its `task_hash` in the provenance tuple.

## Suite

An owned set of tasks with what they are measured by.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner |
| `tasks` | []task digest | at most `MAX_TASKS_PER_SUITE`, no two with one name |
| `metrics` | []metric digest | at most `MAX_METRICS_PER_SUITE`, built-ins included by reference |
| `graders` | []grader digest | judges and annotation queues applied to every trial, beside each task's checks |
| `primary_metric` | metric digest | the metric a report leads with |
| `weights` | map | task digest to a finite number at least 0; a task it does not list weighs 1, and at least one task weighs more than 0 (spec 008) |
| `decline_judge` | grader digest | optional: a grader of kind `model_judge` that produces only `declined` and reads `final_message` and `transcript`; absent, the built-in decline judge `builtin:decline` (spec 006) |

A suite's digest covers every field above, `weights` and
`decline_judge` included, so changing either makes a new suite version.

## Candidate

One fully pinned agent configuration.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner |
| `kind` | enum | `topos` or `image` |
| `topos` | object | for `topos`: `agent` (a Topos agent version digest), `model` (a pinned model id that overrides the agent's), `reasoning` (the reasoning level, overriding the agent's), `tools` (optional narrowing of the agent's tools) |
| `image` | object | for `image`: `image` (digest-pinned), `command`, `model`, `reasoning`, `env` (no secrets; model access comes from the trial's Lux Key), and `lux` (`door`: `openai`, `anthropic`, `gemini` or `lux`, default `lux`; `key_env`, the variable the Key is mounted under, default `LUX_KEY`; `url_env`, the variable set to the door's base URL, default `LUX_URL`) (spec 005) |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

### The provenance tuple

A cell's identity is everything that conditions its trials:

```
(candidate.kind, agent_or_image_digest, program, model, reasoning,
 tools, task_hash, environment_digest, workspace_ref, grader_digests)
```

`program` is an `image` candidate's `command` and `env`, canonicalized
as in a digest, so two image candidates that differ only in how their
program is started are different cells; a `topos` candidate's is empty.
`grader_digests` holds every grader of the cell's trials: the task's
checks, the suite's graders and the suite's decline judge. Every field
is pinned: an agent named without a version digest, a model alias with
nothing recording what served it, an image by tag, a workspace by
branch, is refused when applied. A comparison's confounds are computed
from this tuple (spec 008).

## Experiment

One execution of a suite against candidates.

| Field | Type | Meaning |
|---|---|---|
| `suite` | suite digest | |
| `candidates` | []candidate digest | at most `MAX_CANDIDATES_PER_EXPERIMENT` |
| `trials` | int | N per cell, declared before any cell runs; `DEFAULT_TRIALS` when unset, at most `MAX_TRIALS_PER_CELL` |
| `budget` | string | the Lux Budget every model call of this experiment is metered against |
| `parallelism` | int | `DEFAULT_PARALLELISM` when unset, at most `MAX_PARALLELISM` |
| `baseline` | baseline id | optional; the baseline record its comparisons use |
| `adopt` | bool | reuse finished trials of identical cells no older than `ADOPTION_MAX_AGE` (spec 005) |
| `mode` | enum | `execute` (the default): Examen runs every cell; `import`: Examen runs no trial, and each cell is made from the resolved provenance tuple of the first trial imported into it (below) |
| `state` | enum | below |
| `created_by` | subject | the subject that created it (spec 004) |
| `progress` | object | computed and never written: `{cells: {<state>: n}, trials: {<state>: n}, outcomes: {<outcome>: n}}` |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

States: `pending` → `running` → `grading` → `complete`, and
`canceled` from any state before `complete`. An experiment is
`complete` when every cell is `complete` or parked.

**Mode `import`.** No cell exists at create. Each imported trial names
a task and a candidate of the experiment; its provenance tuple is
resolved from the records it came from and must equal the tuple that
task and candidate give, and it joins the cell of that tuple, which the
first such import creates (spec 005). A cell holds at most `trials`
trials. The owner closes the experiment's intake (spec 003); the cells
then move on to `grading` with the trials they hold, and the experiment
completes as any does.

## Cell

One task × one candidate in an experiment. Created by Examen, when the
experiment is applied in mode `execute` and at its first import in
mode `import`; never written by a client.

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | |
| `task` | task digest | |
| `candidate` | candidate digest | |
| `tuple` | object | the provenance tuple, resolved |
| `rollouts` | []object | every submission of the cell's trials to its executor, each `{key, kind, executor, handle, parallelism, state}`: `kind` is `smoke`, `main`, `retry` or `judge` (an agent judge's, spec 006); `state` is `queued`, `submitting`, `submitted` or `ended` (spec 005) |
| `adopted` | []trial id | trials of an identical earlier cell that this cell reads as its own (spec 005) |
| `state` | enum | below |
| `parked_reason` | string | when parked |

States: `pending` → `smoking` → `running` → `grading` → `complete`.
Parking states: `blocked` (the smoke trial failed for an
infrastructure or compatibility reason), `over_budget` (the estimate
exceeds the budget's remainder), `canceled`. A cell of an experiment in
mode `import` starts `running` and has no `smoking`.

## Trial

One attempt by a candidate at a task.

| Field | Type | Meaning |
|---|---|---|
| `cell` | cell id | |
| `index` | int | 0 to N-1; the smoke trials take the first indices |
| `executor_ref` | string | the Topos session id or the Cella sandbox name |
| `state` | enum | `pending`, `running`, `checking`, `done` |
| `outcome` | enum | once `done`, below |
| `failure` | object | for `infra_failed` and `canceled`: `{code, detail}`, a stable code and a developer's detail |
| `retry_of` | trial id | when this trial retries an infrastructure failure |
| `sent` | object | what was sent: the model and reasoning settings of each request, from the executor (spec 007) |
| `observed` | object | what happened: the model that served, token usage including reasoning, cost, from the executor's and Lux's records (spec 007) |
| `checks` | []object | one check run record per check of the task, in order: `{grader, state, exit_code, reason, duration, stdout_size, stdout_sha256, stderr_tail}`, `state` one of spec 006's check run states |
| `judgments` | []object | one judgment record per judge grading: `{grader, generation, state, attempts, request_digest, cuts, boundary, served_model, tokens, cost}`, `attempts` each `{lux_request, state}`, `state` one of spec 006's judgment states |
| `evidence` | []evidence ref | spec 007 |
| `imported` | bool | true when the trial was imported rather than executed (spec 005) |
| `started_at`, `agent_ended_at`, `ended_at` | time | the agent's first step; the agent's end; the trial's end after its checks and evidence |

| Outcome | When |
|---|---|
| `completed` | the agent ended its work itself |
| `declined` | the provider ended the agent's last response with a refusal |
| `timed_out` | the task's `timeout` passed while the agent ran |
| `turn_limit` | the harness stopped the agent at a bound other than time or money: the task's `max_turns`, the model's output limit, or a stop for repeating a failing call |
| `over_budget` | Lux refused a request for spend |
| `infra_failed` | the trial could not be run to an outcome for a reason outside the agent |
| `canceled` | the experiment was canceled before the trial ended |

The executor sets the outcome once, and it is final (spec 005). Only
`infra_failed` is retried, up to `MAX_INFRA_RETRIES`. A trial that is
retried, and an `infra_failed` or `canceled` trial, is not a finished
trial: it is excluded from analysis with its lineage kept. Every other
outcome is a result.

## Measurement

One value of one metric for one trial, from one grader generation.
Append-only: a measurement is never updated or deleted while its trial
exists.

| Field | Type | Meaning |
|---|---|---|
| `trial` | trial id | |
| `metric` | metric digest | |
| `grader` | grader digest | or `builtin` for the built-in metrics Examen fills, or `builtin:decline` for the built-in decline judge |
| `generation` | int | 1 for the first grading by this grader version of this trial, counted per annotator for an annotation grader; a re-grade appends the next |
| `value` | number, bool or string | typed by the metric |
| `rationale` | text | a judge's reason, an annotator's note; empty for checks and built-ins |
| `annotator` | string | for an annotation grader: `human:<subject>`, the person who answered; empty otherwise |
| `created_at` | time | |

The current value of a metric for a trial is read from the latest
generation of the metric's producing grader (spec 007). Earlier
generations stay readable.

## Baseline

A pinned set of measurements used as the reference: one record of a
lineage.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner and suite, such as `main`; identifies the lineage |
| `suite` | object | the suite, every version of it: `{owner, name}` |
| `experiment` | experiment id | the complete experiment whose cells it pins; it ran a version of `suite` |
| `candidate` | candidate digest | the candidate in that experiment that is the reference |
| `previous` | baseline id | the record this one replaced in its lineage; absent on the first |
| `created_by` | subject | the subject that pinned it |
| `pinned_at` | time | |

A baseline's owner is the owner that pinned it, which may differ from
its suite's owner when the suite is shared with it (spec 004). Moving a
baseline to another experiment or candidate appends a record whose
`previous` is the record it replaces, and a name's current baseline is
its latest record. Every record stays readable, so every past
comparison can be reproduced. An experiment names one record by its id.

## Comparison

The per-metric verdicts of a candidate's cells against a reference.
Computed by Examen (spec 008); never written by a client, and never
changed once complete: computing again makes a new comparison.

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | the candidate's experiment |
| `candidate` | candidate digest | |
| `baseline` | baseline id | the reference, when it is a baseline |
| `reference_candidate` | candidate digest | the reference, when it is another candidate of the same experiment; exactly one of `baseline` and `reference_candidate` is set |
| `state` | enum | `computing`, `complete`, `failed` |
| `variable` | []tuple field | what the comparison intends to vary |
| `confounds` | []string | every tuple field or layer that differs besides `variable`, computed: the union of its results' |
| `single_variable` | bool | `confounds` is empty |
| `results` | []result | per metric and task, and per metric over the suite; below |
| `unpaired_tasks` | object | `{candidate_only, reference_only}`, task names held by one side alone |
| `candidate_suite` | object | per metric, the candidate's own suite-level summary over every task of its suite with weight greater than zero, whatever the reference holds |
| `windows` | object | for each side, the earliest `started_at` and the latest `ended_at` of the trials it read |
| `verdict_counts` | object | per metric, the number of task-level results with each verdict |
| `correction` | object | `{method, task_family, suite_family}`: the correction applied, `benjamini_hochberg`, and how many p-values each family held |
| `analysis_version` | string | `examen.analysis/v1` |
| `parameters` | object | the values of `FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL`, `MIN_TRIALS_FOR_VERDICT`, `BOOTSTRAP_RESAMPLES`, `PERMUTATION_RESAMPLES`, `PERMUTATION_EXACT_MAX` and `TIE_TOLERANCE` it was computed with |
| `inputs_digest` | string | the digest of its inputs document (spec 008) |
| `seed` | string | the root seed, hex |
| `computed_at` | time | |

A **result** is one row of `results`:

| Member | Meaning |
|---|---|
| `level` | `task`, over one task's cells, or `suite`, over the paired tasks |
| `metric`, `metric_name`, `direction` | the metric's digest, name and direction |
| `task`, `task_digests` | on a `task` result: the task's name, and each side's task digest |
| `candidate`, `reference` | the two sides' summaries, below |
| `delta`, `delta_kind` | the change, candidate less reference, and its kind: `difference`, `shift` or `total_variation` |
| `delta_interval` | the delta's interval at `CONFIDENCE_LEVEL` |
| `noise_floor` | half the width of `delta_interval` |
| `method`, `exact`, `resamples` | the test; whether it enumerated every relabeling; how many relabelings it enumerated or drew |
| `seed` | this result's derived seed, hex |
| `p`, `q` | the uncorrected p-value, and the value after the correction at `FALSE_DISCOVERY_RATE` |
| `verdict` | one of the verdicts of the glossary |
| `confounds` | the tuple fields and layers besides `variable` that differ: on a task result for that task's cells, on a suite result for any paired task's |

An `insufficient` result carries both summaries and no `p`, `q` or
`delta_interval`.

A **summary** describes one metric over one set of trials, a cell or a
candidate's tasks:

| Member | Meaning |
|---|---|
| `aggregate` | the metric's `aggregate` |
| `n` | the finished trials of the set |
| `value` | the rate, mean, median or p90; for `distribution`, an object from each label to its share |
| `numerator` | for `rate`: the finished trials whose value is true |
| `denominator` | the finished trials holding a current value of the metric |
| `missing` | `n` less `denominator` |
| `interval` | `[low, high]` at `CONFIDENCE_LEVEL`; absent when spec 008 computes none |
| `noise_floor` | half the width of `interval` |
| `mean`, `sd`, `min`, `max` | for a numeric metric, whatever its aggregate |
| `distribution`, `mode` | for a label metric: each label's count and proportion, and the mode |
| `tasks`, `coverage`, `within_variance`, `between_variance` | at suite level: the tasks summarized, the share of the suite's weight they hold, and the two variances (spec 008) |
| `excluded` | the trials of the set not counted in `n`, by reason: `retried`, `infra_failed`, `canceled`, `not_done` |
| `final` | true once every declared trial of the set is `done` and graded; false while the figure can still change |

## Bar

A rule on a comparison that passes or fails.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner and suite |
| `suite` | object | the suite, every version of it: `{owner, name}` |
| `rules` | []object | at most `MAX_RULES_PER_BAR`; each: `metric` (a metric digest), exactly one of `no_regression` (true: fails on any `regressed` verdict for the metric in its scope), `at_least` and `at_most` (a value the candidate's suite-level summary must hold), and for `no_regression` a `scope`: `all` (the default), `suite` or `tasks` (spec 009) |
| `require_single_variable` | bool | fails when the comparison is confounded |

A bar's owner is the owner that applied it, which may differ from its
suite's owner when the suite is shared with it (spec 004).

## Bar evaluation

One bar version applied to one comparison (spec 009). Kept whatever its
result, and immutable once complete.

| Field | Type | Meaning |
|---|---|---|
| `bar`, `bar_name` | bar digest, string | the bar version's digest, and its name |
| `experiment`, `candidate` | experiment id, candidate digest | the candidate side |
| `comparison` | comparison id | the comparison read |
| `reference` | object | `{baseline}` or `{candidate}`, as the comparison's |
| `state` | enum | `pending` (its comparison is still being computed), `complete` |
| `result` | enum | `passed` or `failed`, when complete |
| `rules` | []object | per rule, in the bar's order: `{index, metric, metric_name, kind, scope, result, reasons}`, each reason `{reason, results}` with the keys `{metric, level, task}` of the results behind it |
| `single_variable`, `confounds` | | as the comparison's |
| `inputs` | object | a copy of everything the rules read: every result and `candidate_suite` entry, whole, with the comparison's `analysis_version`, `parameters` and `inputs_digest` |
| `metadata` | map | at most `METADATA_MAX_PAIRS`, attached by the caller, such as a commit, a ref and a CI run |
| `created_by` | subject | the subject that asked |
| `completed_at` | time | |
| `links` | object | computed when read and never stored: absolute links to the evaluation, its comparison and the comparison's console view, built on the installation's public address (spec 003) |

Its owner is the bar's owner.

## Share

An owner's grant to another owner (spec 004).

| Field | Type | Meaning |
|---|---|---|
| `object` | enum | `suite`, `baseline`, `experiment` or `report` |
| `suite` | object | for `suite` and `baseline`: the suite, every version of it, `{owner, name}` |
| `baseline` | string | for `baseline`: the baseline's name, which covers every record of its lineage |
| `experiment` | experiment id | for `experiment` |
| `report` | report id | for `report` |
| `grantee` | string | the owner the share names, a rendered subject or organization |
| `created_by` | subject | the subject that created it |

## Dataset export

Trials with their measurements and evidence references, written as
records into the owner's Arca space (spec 007).

| Field | Type | Meaning |
|---|---|---|
| `format` | string | `examen.dataset/v1`, the default |
| `filter` | object | `suites`, `experiments` (at most `MAX_EXPORT_EXPERIMENTS`), `candidates`, `tasks`, `outcomes`, `metrics` (at most `MAX_EXPORT_CONDITIONS` conditions); each optional (spec 007) |
| `include_imported` | bool | include imported trials; default true |
| `all_generations` | bool | also write every earlier generation of each measurement; default false |
| `evidence` | enum | `reference` (the default) or `copy` (spec 007) |
| `state` | enum | `pending`, `writing`, `complete`, `failed` |
| `path` | string | the export's folder: `files/examen/exports/<export id>/` in the owner's space |
| `records` | int | the records written |
| `manifest` | object | `{path, digest}` of its manifest, set at `complete` |
| `error` | object | when `failed`: an error object of spec 003 |
| `created_by` | subject | the subject that created it |
| `completed_at` | time | |

## Report

A frozen copy of an experiment's view (spec 010).

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | |
| `comparisons` | []comparison id | complete comparisons of that experiment, at most `MAX_COMPARISONS_PER_REPORT` |
| `document` | object | the view document as built at creation, format `examen.report/v1`; never changes |
| `document_digest` | string | `sha256:` of its canonical JSON |
| `narrative` | object | `{claims, findings, linter_version, draft, released, history}`: the claims as last edited and the linter's findings on them; the draft with its `model` and `prompt_digest`; the released narrative with its claims, its diff against the draft, `linter_version`, `released_by` and `released_at`; and every narrative released before it |
| `exports` | []object | each static export: `{n, path, manifest_digest, exported_at, narrative_released}` |

## Event

One entry of an experiment's event log (spec 003), written in the same
transaction as the change it reports.

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | the log it belongs to |
| `seq` | int | its position in that log, from 1 |
| `type` | enum | `experiment`, `cell`, `trial`, `graded`, `comparison` (spec 003), `bar.passed`, `bar.failed` (spec 009) |
| `data` | object | what changed, by type (specs 003 and 009) |
| `at` | time | when the change happened, which is the event's `created_at` |
| `delivery` | enum | for `bar.passed` and `bar.failed`: `pending`, `acknowledged` or `dropped` (spec 009) |

Its owner is the experiment's owner, and for a bar event the bar
evaluation's owner.

## Annotation item

A trial queued for a grader of kind `annotation` (spec 006): a queue
entry rather than an object of its own, with no identifier or owner of
its own. The grader's digest and the trial's id name it, and it belongs
to the trial's owner.

| Field | Type | Meaning |
|---|---|---|
| `grader` | grader digest | |
| `trial` | trial id | |
| `state` | enum | `open`, `claimed`, `done` |
| `claimed_by`, `claimed_until` | subject, time | while `claimed` |
| `sampled_at` | time | when it was queued |

## Not in this spec

The API's shapes and errors (003), who may act on what (004), how a
rollout executes (005), how each grader kind runs (006), how evidence
is referenced and exported (007), the statistics (008), bar evaluation
(009) and reports (010).

## Acceptance criteria

- Every identifier is its prefix, an underscore and a lowercase ULID,
  and identifiers of one kind sort by creation time.
- Applying changed content to a versioned object makes a new digest and
  leaves the earlier version readable and unchanged.
- A reference to a version by name alone, a model alias, an image by
  tag or a workspace by branch is refused at apply, naming the field.
- A suite version that pins two tasks of one name, or gives one task
  two producers of one metric other than annotation graders, is refused
  at apply, naming the field.
- A measurement for a metric its grader does not declare is refused.
- A measurement is never updated; a re-grade appends the next
  generation.
- Every limit in this spec is enforced where it applies, with an error
  naming the constant.
- Metadata with more than `METADATA_MAX_PAIRS` pairs, or a key beginning
  `examen.`, is refused on every object that has it.
- An experiment's N cannot change once any of its cells has started.
- The decline judge never changes a trial's outcome.
