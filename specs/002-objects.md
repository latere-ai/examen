---
title: "Objects: metric, grader, task, suite, candidate, experiment, cell, trial, measurement, taxonomy, baseline, comparison, bar, bar evaluation, share, dataset export, report, publication, submission and event; their fields, states, identifiers and limits"
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

Twenty objects carry everything Examen keeps. This spec fixes each
one's fields, states and identifier, the words the other specs use for
them, and every limit as a named constant. A field written here is the
one place it is defined; the API (003) exposes these shapes and the
other specs refer to them by name.

## Glossary

| Word | Meaning | Not to be confused with |
|---|---|---|
| task | one versioned unit of work a system under test attempts, with its inputs, its reference outputs and its checks | a Topos session's goal; Topos spec 025's tasks are the same concept and move here |
| check | a command run after the system under test ends, in the trial's machine or, for an external trial, in the check sandbox, producing measurements | a CI check |
| input | a named file a task gives the system under test at `/examen/inputs/<name>` (spec 005) | |
| reference (of a task) | a named expected output a task holds, delivered only to its checks and never to the system under test (spec 005) | an experiment's `reference`, what a candidate is compared with |
| output | a file, or a directory with every file in it, that the system under test writes under `/examen/outputs/`, which a task names, keeps as evidence and its checks read; a directory output is carried as an archive with an output manifest (spec 005) | |
| comparator | one of the scoring programs of Examen's comparator image, run by a `check` grader to compare an output with a reference (spec 006) | a model judge |
| suite | an owned set of tasks with the metrics and graders they are measured by | Topos spec 025's suite, which becomes an Examen suite |
| metric | a named, typed quantity with a direction | a telemetry metric |
| metric level | where a metric's values belong: `trial`, one per finished trial; `cell`, computed by Examen from a cell's trial values; `candidate`, one per candidate of an experiment, from a candidate-level judge (specs 006 and 008) | a result's `level` |
| open vocabulary | a `label` metric that declares no labels: a judge proposes a raw label for each trial, and the suite's taxonomy maps it to a canonical label (spec 006) | a closed vocabulary, whose labels the metric declares |
| taxonomy | one version of a suite's mapping from the raw labels of one open-vocabulary metric to its canonical labels (spec 006) | |
| canonicalizer | a model judge that proposes the next taxonomy version, mapping raw labels the current one does not map (spec 006) | a grader that produces measurements |
| grader | what turns a trial into measurements: a check, a model judge, an agent judge, or an annotation queue | |
| judge | a grader that uses a model: a model judge or an agent judge | |
| decline judge | what produces the built-in metric `declined`: the built-in decline judge, `builtin:decline`, or the grader a suite names in `decline_judge` (spec 006) | the outcome `declined`, which the executor sets |
| candidate | one fully pinned system under test: an agent, a program, or an external harness's configuration | the authorizer's `subject`, which is the caller |
| system under test | another word for a candidate: the agent, program or harness configuration a trial exercises | |
| program | a program in an image, pinned by digest, that a candidate of kind `program` runs in a Cella sandbox: an agent command-line tool of any vendor, Examen's HTTP runner that calls an API, or any other (spec 005) | the tuple field `program`, which holds how it is started and configured |
| external harness | the owner's own program that runs the trials of a candidate of kind `external` wherever it likes, leasing them from Examen and submitting their outcomes and outputs (spec 005) | an executor, which is Examen's |
| lease (of external trials) | an external harness's hold on pending trials for `EXTERNAL_LEASE`, renewable, after which they return to pending (spec 005) | a Key leased from the key source, the delivery lease, and a work queue job's lease |
| check sandbox | a fresh Cella sandbox from a task's environment template in which the checks of an external trial run, with its outputs and references delivered (spec 005) | the trial's machine, in which a `topos` or `cella` trial's checks run |
| experiment | one execution of a suite against candidates | |
| cell | one task × one candidate in an experiment; the unit of comparison | |
| trial | one attempt by a candidate at a task | Topos's retired word "run"; a trial of a Topos agent is one Topos session |
| finished trial | a trial whose outcome is none of `infra_failed`, `over_budget` and `canceled` and that was not retried; the trials analysis counts | a trial in state `done`, which may have failed, run out of budget or been canceled |
| rollout | one submission of some of a cell's trials to its executor: the smoke rollout, the main rollout, retry rollouts, and an agent judge's rollouts (specs 005 and 006) | a deployment rollout, or a rollout identity |
| measurement | one value of one metric for one trial, or for a candidate-level metric for one candidate of an experiment, from one grader generation | |
| self-reported measurement | a measurement an external harness computed and submitted itself, under the grader `self:<harness>` and flagged `self_reported`; never headlined (spec 008) | a measurement a grader produced |
| service version | the version of a service a program called, which the program records in `/examen/observed.json` and an external harness submits (spec 005) | an agent version |
| baseline | a record that pins one candidate's cells in one complete experiment as a reference; a baseline's name identifies a lineage of records | |
| reference | what a candidate's cells are compared with: a baseline, or another candidate of the same experiment; an experiment's `reference` is its first candidate unless it names another | a task's `reference`, its expected outputs |
| comparison | the per-metric verdicts of a candidate's cells against a reference | |
| verdict | `improved`, `regressed`, `changed`, `within_noise` or `insufficient`, per metric | |
| changed | the verdict of a significant change in a metric whose direction is `none`, which is neither better nor worse | `improved` and `regressed`, which need a direction |
| bar | a rule on a comparison that passes or fails | a lateregate gate |
| bar evaluation | one bar version applied to one comparison, recorded with what it read | |
| share | an owner's grant to another owner of a suite, a baseline, an experiment or a publication: the "Only people invited" way of sharing (spec 013); a word of Examen's own | an Arca share of files |
| dataset export | trials with their measurements and evidence references, written as records into the owner's Arca space | an export of a report |
| report | a frozen document of an experiment's grid and comparisons, with the narrative its owner released | the API's answers, which change while an experiment runs |
| publication | a report, a benchmark or a dataset its owner shares as immutable versions, each a snapshot, at a visibility (spec 013) | a share, which grants reading of live objects to one owner |
| visibility | who may read a publication: `private`, its owner and the owners it invites; `unlisted`, anyone with its link; `public`, also listed for everyone (spec 013) | |
| link token | the capability in a publication's link: whoever holds it reads the publication's published versions without signing in, while it is `unlisted` or `public` (spec 013) | an Arca link's token, which addresses files |
| evidence level | how much trial content a publication version carries: `aggregates`, `outputs` or `full` (spec 013) | |
| benchmark | a publication of a suite version that others may run, with the results submitted to it (spec 013) | a public benchmark's content, imported as ordinary tasks (spec 001) |
| submission | one candidate of a report publication version entered in a benchmark's results table (spec 013) | an external trial's submit |
| event | one entry of an experiment's event log; a sink event is also delivered to the installation's event sink (spec 003) | a telemetry event |
| key source | what leases the Lux Keys that model judges, `cella` rollouts and narrative drafts call through: `lux` or `endpoint` (spec 004) | the Keys Topos issues its own sessions |

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
| taxonomy | `tax_` | no |
| baseline | `bas_` | no |
| comparison | `cmp_` | no |
| bar | `bar_` | yes |
| bar evaluation | `bev_` | no |
| share | `shr_` | no |
| dataset export | `dsx_` | no |
| report | `rep_` | no |
| publication | `pub_` | no |
| submission | `sbm_` | no |
| event | `evt_` | no |

A lease of external trials (spec 005) is named by an identifier of the
same form with the prefix `lse_`; it is a hold on trials, not an
object, and has no owner or row of its own.

Every object has `id`, `owner`, `created_at` and, when mutable,
`updated_at`. `owner` is the subject that owns it, a person or an
organization, exactly as the authorizer names it (spec 004). Objects of
one owner never reference another owner's objects except through a
share or a published benchmark (spec 013): an experiment may run a
suite and name a baseline shared with its owner, or run the suite of a
benchmark published to it; a baseline or a bar may name a suite shared
with its owner; and a submission names another owner's benchmark.

A versioned object is immutable once applied: applying changed content
makes a new version with a new digest under the same `name`. Each
version has its own id, and `name@digest` also addresses it (spec
003). Every reference to a version pins a digest, never a name alone.
The one reference by name is a baseline's or a bar's `suite`, which
names a suite, every version of it, by its owner and name.

Three grader digests are reserved and name no applied version:
`builtin`, Examen's filling of the built-in metrics from the trial
record; `builtin:decline`, the built-in decline judge; and
`self:<harness>`, with `<harness>` an external candidate's `harness`,
the grader of every measurement that harness submitted itself (spec
005). Each built-in metric (below) has the reserved metric digest
`builtin:<name>`, such as `builtin:cost_usd`, which also names no
applied version: a suite names it in `metrics` to include that metric,
and every reference to a built-in metric uses it. Metric and grader
digests are read in their own places, so `builtin:declined`, a metric,
and `builtin:decline`, a grader, never meet.

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
| `DEFAULT_TRIALS` | 10 | trials per cell when an experiment names no N | 002 |
| `MAX_TRIALS_PER_CELL` | 1000 | the most trials one cell may declare | 002 |
| `SMOKE_TRIALS` | 1 | trials a cell runs before the rest | 005 |
| `MIN_TRIALS_FOR_VERDICT` | 5 | finished trials, holding a current value of the metric, each side of a task-level result needs before a verdict other than `insufficient` | 008 |
| `MIN_TRIALS_PER_TASK_IN_SUITE` | 2 | finished trials, holding a current value of the metric, each side needs for a task to be included in a suite-level result | 008 |
| `MIN_SUITE_COVERAGE` | 0.8 | the share of the suite's weight a suite-level result must include before a verdict other than `insufficient` | 008 |
| `MIN_TRIALS_FOR_P90_INTERVAL` | 35 | valued trials below which a `p90` summary and its delta carry no interval | 008 |
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
| `EXTERNAL_LEASE` | 10 minutes | how long one lease of an external trial, or one renewal of it, holds the trial before it returns to pending | 005 |
| `REPORT_INTERVAL` | 15 seconds | how often an open rollout is reported | 005 |
| `USAGE_SETTLE_DELAY` | 2 minutes | the wait after a trial ends before Lux's records of it are read | 005 |
| `COST_ESTIMATE_MARGIN` | 1.5 | the factor a cell's cost estimate is multiplied by at the cost check | 005 |
| `ADOPTION_MAX_AGE` | 30 days | the oldest trials a new experiment may adopt instead of re-running, once adoption is built | 005 |
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
| `PERMUTATION_RESAMPLES` | the larger of 10,000 and ceil(20 · m / `FALSE_DISCOVERY_RATE`), with m the number of p-values in the test's family, 1 for the primary metric's suite-level result | relabelings of a Monte Carlo permutation test | 008 |
| `PERMUTATION_EXACT_MAX` | 100,000 | the most relabelings a permutation test, or tables Freeman and Halton's test, enumerates instead of sampling | 008 |
| `BOSCHLOO_GRID_POINTS` | 1000 | points of the fixed grid of nuisance values over which Boschloo's test takes its largest p-value | 008 |
| `TIE_TOLERANCE` | 1e-9 | relative tolerance when a test compares a probability or a discrepancy with the observed one | 008 |
| `MAX_TREND_POINTS` | 100 | baseline records one trend reads, once trends are built | 008 |
| `MAX_RULES_PER_BAR` | 128 | rules in one bar version | 009 |
| `DELIVERY_TIMEOUT` | 10 seconds | how long one sink event delivery attempt waits for the sink | 003 |
| `DELIVERY_BACKOFF_MIN` | 1 second | the first delivery retry delay | 003 |
| `DELIVERY_BACKOFF_MAX` | 5 minutes | the longest delivery retry delay | 003 |
| `DELIVERY_RETRY_WINDOW` | 24 hours | how long a sink event is retried before it is dropped | 003 |
| `DELIVERY_LEASE` | 30 seconds | how long a replica holds the delivery lease row before another may take it | 003 |
| `SIGNATURE_MAX_AGE` | 5 minutes | the oldest signature time a sink accepts | 003 |
| `MAX_COMPARISONS_PER_REPORT` | 64 | comparisons one report renders | 010 |
| `NARRATIVE_MAX_CLAIMS` | 40 | claims in one narrative | 010 |
| `NARRATIVE_MAX_BYTES` | 32 KiB | the text of one narrative | 010 |
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
| `METADATA_MAX_PAIRS` | 16 | pairs in the `metadata` of any object, and in an external candidate's `flags` | 002 |
| `MAX_INLINE_INPUT_BYTES` | 64 KiB | the text of one inline input or reference of a task | 002 |
| `MAX_OUTPUT_FILES` | 10,000 | files one trial's outputs may hold, every file of a directory output counted | 005 |
| `MAX_OUTPUT_BYTES` | 1 GiB | bytes of the output files one trial keeps | 005 |
| `OUTPUT_FILE_KEEP_MAX_BYTES` | 16 MiB | the largest output file kept; a larger one is kept by its digest and size alone, listed in the trial's output manifest | 005 |
| `LABEL_MAX_BYTES` | 128 | one label: declared by a metric, proposed by a judge for an open-vocabulary metric, or canonical in a taxonomy | 002 |
| `MAX_LABELS_PER_METRIC` | 64 | the labels a closed-vocabulary metric declares, and the canonical labels of one taxonomy version | 006 |
| `MAX_TAXONOMY_RAW_LABELS` | 10,000 | the raw labels one taxonomy version maps | 006 |
| `BUDGET_STATUS_TTL` | 15 seconds | how long an experiment's budget figures, read through the key source, are reused for its answers | 004 |
| `PUBLIC_REQUESTS_PER_MINUTE` | 120 | requests one client address may make to the public routes of one replica in a minute | 013 |
| `PUBLICATION_LINE_MAX_BYTES` | 200 | a publication's title, and the attribution a version shows | 013 |
| `PUBLICATION_TEXT_MAX_BYTES` | 16 KiB | a publication's description, and a dataset's citation as canonical JSON | 013 |
| `PUBLICATION_EVIDENCE_MAX_BYTES` | 10 GiB | the evidence and files one publication version copies into its folder | 013 |
| `SECRET_SCAN_TIMEOUT` | 30 minutes | wall time of one publication version's secret scan | 013 |
| `SHUTDOWN_GRACE` | 25 seconds | how long open requests have to finish after the stop signal | 011 |
| `IMAGE_MAX_SIZE` | 40 MiB | the largest the `examend` image may be | 011 |

## Metric

A named, typed quantity a suite is measured on.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner, `[a-z][a-z0-9_]{0,62}` |
| `type` | enum | `binary` (true or false), `score` (a number in a declared range), `count`, `duration`, `cost`, `label` (a string: one of the declared values, or for an open vocabulary any value a judge proposes) |
| `range` | [min, max] | for `score`; required |
| `vocabulary` | enum | for `label`: `closed` (the default), whose values are among `labels`; or `open`, whose values are raw labels a judge proposes, each at most `LABEL_MAX_BYTES`, read as the canonical labels the suite's taxonomy maps them to (spec 006) |
| `labels` | []string | for a `label` metric of closed vocabulary: the allowed values, at most `MAX_LABELS_PER_METRIC`, each at most `LABEL_MAX_BYTES`; absent for an open vocabulary |
| `direction` | enum | `higher_is_better`, `lower_is_better`, `none` (a label has `none`) |
| `level` | enum | `trial` (the default): one value per finished trial, from a grader; `cell`: one value per cell, computed by Examen from the cell's current values of `of` (spec 008); `candidate`: one value per candidate of an experiment, from a grader of level `candidate` (spec 006) |
| `applies_to` | []outcome | for a trial-level metric: the outcomes of the trials it applies to, among `completed`, `declined`, `timed_out` and `turn_limit`; every one of them when absent. A finished trial of another outcome has no value of the metric and is not missing it (spec 008) |
| `of` | metric digest | for `level: cell`: the trial-level `label` metric whose values it summarizes |
| `exclude_labels` | []string | for `level: cell`: labels of `of` whose trials the statistic leaves out, so a cell of declines does not read as fixed on one answer; `["declined", "none"]` when absent, `[]` to leave none out. Each given entry is one of the `of` metric's `labels` when its vocabulary is closed, and any label of at most `LABEL_MAX_BYTES` otherwise, matched after normalization against canonical labels (spec 008) |
| `aggregate` | enum | how a cell summarizes its trials: `rate` (binary), `mean`, `median`, `p90`, `distribution` (label); for a cell-level metric, `mode_share` (the share of the cell's valued trials that hold its most frequent label; type `score`, range [0, 1]) or `distinct_count` (the number of distinct labels among them; type `count`); absent for a candidate-level metric, whose one value is its summary |
| `unit` | string | for display, such as `s` or `USD` |
| `description` | string | for people |

A cell-level metric is never measured: a measurement of one is
refused, and Examen computes its value wherever a summary is read
(spec 008). A metric of level `trial` has a type of every kind above; a
candidate-level metric any type but `label` of open vocabulary.

**Built-in metrics** exist for every owner under the reserved digests
`builtin:<name>` (Identifiers, above), with these types, aggregates
and directions:

| Metric | Type | Aggregate | Direction | Value |
|---|---|---|---|---|
| `completed` | `binary` | `rate` | `none` | the agent ended its work itself: outcome `completed` or `declined`. Ending is not success, and declining is ending, so it has no direction; a task's success is a check metric |
| `declined` | `binary` | `rate` | `lower_is_better` | the agent refused the task, said it would not or could not do it, or stopped without attempting it, as the decline judge decides (spec 006) |
| `cost_usd` | `cost` | `mean` | `lower_is_better` | the trial's cost from Lux's records, in USD |
| `duration` | `duration` | `median` | `lower_is_better` | `agent_ended_at` less `started_at`, in seconds |
| `input_tokens`, `output_tokens`, `reasoning_tokens` | `count` | `mean` | `none` | the sums of Lux's token counts for the trial |
| `turns`, `tool_calls` | `count` | `median` | `none` | the agent's responses and tool calls, from the executor's records |

Every built-in metric is of level `trial`. Examen fills every built-in
metric but `declined` from the trial record, with grader `builtin`, for
every finished trial (spec 005). `declined` is a measurement
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
| `level` | enum | `trial` (the default): grades one trial at a time; `candidate`: for a `model_judge` alone, grades once per candidate of an experiment over all the candidate's finished trials, and produces only candidate-level metrics (spec 006) |
| `produces` | []metric digest | the metrics it fills |
| `check` | object | for `check`: `command` ([]string, run in the trial's machine or the check sandbox), `workdir`, `env`, `files` (optional: an owner's Arca path and its content digest, delivered read-only to the check), and `image` (optional: an image pinned by digest, mounted read-only while the check runs, from which `command[0]` runs; Examen's comparator image is one). Output contract and the comparators in spec 006 |
| `model_judge` | object | for `model_judge`: `model` (a pinned model id), `prompt` (text), `rubric` (text), `inputs` (which evidence it reads: `transcript`, `final_message`, named `files`, named `outputs`), `temperature`, `reasoning` (optional: the reasoning level the call names; the model's default when absent), `preamble` (Examen's judge preamble, pinned at apply), and `role`: `grade` (the default), or `canonicalize` for a canonicalizer, which produces exactly one open-vocabulary metric, reads that metric's raw labels and no evidence, and answers taxonomy versions instead of measurements (spec 006). Which providers may keep what a judge sends is the key source's, not the grader's (spec 004) |
| `agent_judge` | object | for `agent_judge`: `agent` (a pinned Topos agent version digest), `inputs`, `instructions`, `preamble` (Examen's agent judge preamble, pinned at apply) |
| `annotation` | object | for `annotation`: `instructions` for the person, `sample` (the fraction of trials to queue) |

A grader's digest covers every field above. Changing a judge's model,
prompt, rubric, inputs, temperature, reasoning, preamble, role or level therefore
makes a new grader version, and its measurements are a new generation
(spec 006).

**One producer per metric.** For each task of a suite version, each
trial-level metric the suite lists has at most one producer that is not
an annotation grader: one of the task's `checks`, the suite's `graders`
and, for `declined`, the suite's decline judge; `builtin` for the other
built-in metrics. A candidate-level metric has exactly one producer,
one of the suite's `graders` of level `candidate`. A cell-level metric
has none, since Examen computes it, and a canonicalizer produces no
measurement and is no metric's producer. A suite version that gives one task two such producers
of one metric is refused at apply, naming the metric and both graders.
An annotation grader may produce a metric a judge also produces, and
then calibrates the judge (spec 007). A metric the suite lists that no
grader produces for a task has no producer there; an external harness
may submit its value itself, and only for such a metric (spec 005).

## Task

One versioned unit of work.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner; a suite version refuses two tasks of one name |
| `instruction` | text | the first message the system under test receives; required when a `topos` candidate runs the task, optional for a `program` or `external` one (spec 003) |
| `inputs` | []object | named files given to the system under test, each `{name, arca: {path, digest}}`, `{name, origo: {repository, commit, path}}` with a full commit id, or `{name, text}` with at most `MAX_INLINE_INPUT_BYTES` of text; delivered at `/examen/inputs/<name>`, and into the working directory of a `topos` trial (spec 005) |
| `reference` | []object | named expected outputs, in the same three forms as `inputs`; delivered only to the task's checks, never to the system under test (spec 005) |
| `outputs` | []path | what the system under test writes under `/examen/outputs/`, each relative to it: a file, or, for an entry ending in `/`, a directory, such as an agent's whole workspace, whose every file is an output; kept as evidence and read by the checks (spec 005) |
| `workspace` | object | where the trial starts: `none`, `origo` (a repository and a full commit id), or `arca` (an owner's path and its content digest) |
| `environment` | object | the Cella sandbox template the trial runs in, pinned by image digest, with its resources and network policy; the image must be one Cella's admission accepts, checked when an experiment that uses the task is created (spec 003) |
| `checks` | []grader digest | graders of kind `check`, run in order after the agent ends |
| `timeout` | duration | the trial's wall time, at most `MAX_TRIAL_TIMEOUT`; `DEFAULT_TRIAL_TIMEOUT` when unset |
| `max_turns` | int | optional bound on the model responses of the agent's own thread; the `topos` executor needs a step bound Topos does not have today (spec 005, requirement R7), and the `cella` and `external` executors pass it on as advice |
| `evidence` | []path | files of the workspace kept as evidence besides the transcript |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

An input or reference name is 1 to 63 characters of `[a-z0-9._-]`,
starting with a letter or a digit, unique within its list; an `outputs`
path and an Origo `path` are relative and contain no `..`. The task's
digest covers its inputs and references as declared, a pinned digest or
commit and inline text included, and is its `task_hash` in the
provenance tuple.

## Suite

An owned set of tasks with what they are measured by.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner |
| `tasks` | []task digest | at most `MAX_TASKS_PER_SUITE`, no two with one name |
| `metrics` | []metric digest | at most `MAX_METRICS_PER_SUITE`; a built-in metric is included by naming its reserved digest `builtin:<name>` |
| `graders` | []grader digest | judges and annotation queues applied to every trial, beside each task's checks, and judges of level `candidate` applied to each candidate |
| `canonicalizers` | []grader digest | optional: model judges of role `canonicalize`, at most one per open-vocabulary metric the suite lists (spec 006) |
| `primary_metric` | metric digest | the metric a report leads with, of level `trial` or `cell`; its suite-level result is one pre-declared test, outside the corrected family (spec 008) |
| `weights` | map | task digest to a finite number at least 0; a task it does not list weighs 1, and at least one task weighs more than 0 (spec 008) |
| `decline_judge` | grader digest | optional: a grader of kind `model_judge` that produces only `declined` and reads `final_message` and `transcript`; absent, the built-in decline judge `builtin:decline` (spec 006) |

A suite's digest covers every field above, `weights`,
`decline_judge` and `canonicalizers` included, so changing any of them
makes a new suite version. A cell-level metric's `of` is a trial-level
`label` metric the suite also lists, or the suite version is refused at
apply naming the metric. Each open-vocabulary metric the suite lists is
read through the suite's taxonomy of it (Taxonomy, below), which is the
suite's by owner and name and not part of its digest.

## Candidate

One fully pinned system under test.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner |
| `kind` | enum | `topos`, `program` or `external` |
| `topos` | object | for `topos`: `agent` (a Topos agent version digest), `model` (a pinned model id that overrides the agent's), `reasoning` (the reasoning level, overriding the agent's), `tools` (optional narrowing of the agent's tools) |
| `program` | object | for `program`: `image` (digest-pinned), `command`, `env` (no secrets), `config` (an object the program defines and reads, such as the HTTP runner's request, spec 005), `model` and `reasoning` (optional: the model and reasoning level the program is told to use; model access comes from the trial's Lux Key), `credentials` (optional: each `{audience, host, env}` or `{audience, host, file}`, an owner token for `audience` delivered as a Cella Secret scoped to `host`, the API's host name, with its placeholder under the variable `env` or in the file `file`), and `lux` (`surface`: the Lux API the program speaks, `anthropic_messages`, `openai_responses`, `openai_chat_completions`, `gemini` or `lux`, default `lux`, for which its Key is scoped; `key_env`, the variable the Key is mounted under, default `LUX_KEY`; `url_env`, the variable set to the surface's base URL, default `LUX_URL`) (spec 005) |
| `external` | object | for `external`: `harness` (the harness's name, 1 to 63 characters of `[a-z0-9._-]`), `version` (a string the harness declares for itself), `digest` (optional: a digest the harness declares for its build), `model` and `reasoning` (optional: the pinned model id and the reasoning level the harness runs its system with), and `flags` (optional: an object from a setting's name to its value, each a string, at most `METADATA_MAX_PAIRS` pairs: the harness's own settings that change what it runs); declared by the harness, never observed (spec 005) |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

### The provenance tuple

A cell's identity is everything that conditions its trials:

```
(candidate.kind, agent_or_image_digest, program, model, reasoning,
 tools, task_hash, environment_digest, workspace_ref, grader_digests)
```

`agent_or_image_digest` is a `topos` candidate's agent version digest,
a `program` candidate's image digest, or an `external` candidate's
declared `digest`, empty when it declares none. `program` is a
`program` candidate's `command`, `env` and `config`, and an `external`
candidate's `harness`, `version` and `flags`, canonicalized as in a
digest, so two candidates that differ only in how their program is
started or configured are different cells; a `topos` candidate's is
empty. `model` and `reasoning` are a candidate's own, and for an
`external` candidate what it declares, so two external candidates that
declare one harness and model and differ only in `reasoning` are
compared on `reasoning` alone.
`grader_digests` holds every grader of the cell's trials: the task's
checks, the suite's graders and the suite's decline judge. Every field
is pinned: an agent named without a version digest, a model alias with
nothing recording what served it, an image by tag, a workspace by
branch, is refused when applied. An `external` candidate's fields are
pinned by its harness's declaration, which Examen cannot observe, so a
comparison that reads one says so in `declared_provenance` (spec 008).
A comparison's confounds are computed from this tuple (spec 008).

## Experiment

One execution of a suite against candidates.

| Field | Type | Meaning |
|---|---|---|
| `suite` | suite digest | |
| `candidates` | []candidate digest | at most `MAX_CANDIDATES_PER_EXPERIMENT` |
| `trials` | int | N per cell, declared before any cell runs; `DEFAULT_TRIALS` when unset, at most `MAX_TRIALS_PER_CELL` |
| `budget` | object | `{name}`: the Lux Budget every model call of this experiment is metered against, as resolved at create: the Budget the request named, the one an authorizer's `budget` limit assigned, or the one created for the cap the request gave (specs 003 and 004). Read with `cap_usd_micro`, `spent_usd_micro` and `remaining_usd_micro` beside `name`, computed through the key source when read, at most `BUDGET_STATUS_TTL` old, and never stored |
| `benchmark` | object | `{publication, version}`: the published benchmark version through which the experiment runs another owner's suite (spec 013); absent otherwise |
| `parallelism` | int | `DEFAULT_PARALLELISM` when unset, at most `MAX_PARALLELISM` |
| `reference` | object | what every other candidate is compared with: `{baseline}`, a baseline record's id, or `{candidate}`, the digest of one of the experiment's candidates; `{candidate}` naming the first candidate when unset |
| `adopt` | bool | reuse finished trials of identical cells no older than `ADOPTION_MAX_AGE`; a later build step (spec 005) |
| `mode` | enum | `execute` (the default): Examen runs every cell; `import`: Examen runs no trial, and each cell is made from the resolved provenance tuple of the first trial imported into it (below) |
| `state` | enum | below |
| `created_by` | subject | the subject that created it (spec 004) |
| `judgments` | []object | one judgment record per grading that belongs to no trial: a candidate-level judge's, with `candidate` set, and a canonicalizer's, with `metric` set; each otherwise as a trial's `judgments` entry (spec 006) |
| `progress` | object | computed and never written: `{cells: {<state>: n}, trials: {<state>: n}, outcomes: {<outcome>: n}}` |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

States: `pending` → `running` → `grading` → `complete`, and
`canceled` from any state before `complete`. An experiment is
`complete` when every cell is `complete` or parked and every grading
that belongs to no trial has ended (spec 006).

**The automatic comparison.** Every candidate other than the reference
is compared with the reference without being asked: progressively,
while trials finish and gradings arrive, as a comparison with `final`
false, and once more at completion with `final` true (spec 008). A
first experiment with no baseline therefore has comparisons, a
headline and grid verdicts from its first candidate.

**Mode `import`.** No cell exists at create. Each imported trial, a
finished session or sandbox or a past run an external harness reports,
names a task and a candidate of the experiment; its provenance tuple is
resolved from the records it came from, or for an external run from
its candidate's declaration, and must equal the tuple that
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
| `rollouts` | []object | every submission of the cell's trials to its executor, each `{key, kind, executor, handle, parallelism, key_id, state}`: `kind` is `smoke`, `main`, `retry` or `judge` (an agent judge's, spec 006); `key_id` the id of a `cella` rollout's leased Lux Key, never its value; `state` is `queued`, `submitting`, `submitted` or `ended` (spec 005) |
| `adopted` | []trial id | trials of an identical earlier cell that this cell reads as its own, once adoption is built (spec 005) |
| `state` | enum | below |
| `parked_reason` | string | when parked: a stable code, such as `owner_token_refused`, `sign_in_not_kept`, `run_denied`, `owner_mismatch`, `model_unpriced` or `input_changed`, with the figures or the trial behind it |

States: `pending` → `smoking` → `running` → `grading` → `complete`.
Parking states: `blocked` (a smoke trial failed for an infrastructure
or compatibility reason, or a core, the minter or the attribution
check refused the cell's work), `over_budget` (the cost estimate
exceeds the smallest remainder the key source reports, or a trial or a
judgment of the cell was refused because a Budget it draws on ran out),
and `canceled`. A cell parked `blocked` or `over_budget` resumes only
through a retry (`cell.retry`, spec 003); a `canceled` cell never
resumes. A cell of an experiment in mode `import` starts `running` and
has no `smoking`.

## Trial

One attempt by a candidate at a task.

| Field | Type | Meaning |
|---|---|---|
| `cell` | cell id | |
| `index` | int | 0 to N-1; the smoke trials take the first indices |
| `executor_ref` | string | the Topos session id, the Cella sandbox name, or for an `external` trial the run reference its harness submitted, when it submitted one |
| `lease` | object | for an `external` trial while it is leased: `{id, subject, expires_at}`, the lease's `lse_` id, the subject that holds it, and when it lapses unless renewed (spec 005) |
| `state` | enum | `pending`, `running`, `checking`, `done` |
| `outcome` | enum | once `done`, below |
| `failure` | object | for `infra_failed`, `over_budget` and `canceled`: `{code, detail}`, a stable code and a developer's detail |
| `retry_of` | trial id | when this trial retries an earlier one: an infrastructure failure, or an index a cell's retry runs again (spec 005) |
| `sent` | object | what was sent: the model and reasoning settings of each request, from the executor (spec 007) |
| `observed` | object | what happened: the model that served, token usage including reasoning, cost, from the executor's and Lux's records, and the service versions and the usage figures of the APIs it called that a program recorded or a harness submitted (spec 007) |
| `declared` | object | for an `external` trial: what its harness declared about its own run that Examen cannot observe, `harness_version` and `image`, a tag or a digest as the harness names it (spec 005) |
| `checks` | []object | one check run record per check of the task, in order: `{grader, state, exit_code, reason, duration, stdout_size, stdout_sha256, stderr_tail}`, `state` one of spec 006's check run states |
| `judgments` | []object | one judgment record per judge grading: `{grader, generation, state, attempts, request_digest, cuts, boundary, served_model, tokens, cost}`, `attempts` each `{lux_request, state}`, `state` one of spec 006's judgment states |
| `evidence` | []evidence ref | spec 007 |
| `outputs` | object | a summary of the trial's output manifest (spec 005): `{files, bytes, kept_bytes, not_kept, over_limit}`, the files and bytes its outputs held, the bytes kept, the files kept by digest and size alone, and, when its outputs passed `MAX_OUTPUT_FILES` or `MAX_OUTPUT_BYTES`, the constant they passed |
| `imported` | bool | true when the trial was imported rather than executed: a finished session or sandbox, or a past run of an external harness (spec 005) |
| `started_at`, `agent_ended_at`, `ended_at` | time | the system under test's first step; its end; the trial's end after its checks and evidence. For an `external` trial the first two are what its harness submitted, and for an imported external run all three (spec 003) |

| Outcome | When |
|---|---|
| `completed` | the agent ended its work itself |
| `declined` | the provider ended the agent's last response with a refusal |
| `timed_out` | the task's `timeout` passed while the agent ran |
| `turn_limit` | the harness stopped the agent at a bound other than time or money: the task's `max_turns`, the model's output limit, or a stop for repeating a failing call |
| `over_budget` | Lux refused a request for spend: a Budget the trial's Key draws on, or the payer's wallet behind it, ran out |
| `infra_failed` | the trial could not be run to an outcome for a reason outside the agent |
| `canceled` | the experiment was canceled, or the trial's cell parked, before the trial ended |

The executor sets the outcome once, and it is final (spec 005). Only
`infra_failed` is retried automatically, up to `MAX_INFRA_RETRIES`. An
`over_budget` trial is not retried automatically: it parks its cell
`over_budget`, and a retry of the cell runs its index again (spec 005).
A trial that is retried, and an `infra_failed`, `over_budget` or
`canceled` trial, is not a finished trial: it is excluded from analysis
with its lineage kept, since an outcome that says how much money was
left says nothing about the agent. Every other outcome is a result.

## Measurement

One value of one metric for one trial, or for a candidate-level metric
for one candidate of an experiment, from one grader generation.
Append-only: a measurement is never updated or deleted while its trial,
or its experiment, exists.

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | |
| `trial` | trial id | for a metric of level `trial`; absent for a candidate-level metric |
| `candidate` | candidate digest | for a candidate-level metric: the candidate of `experiment` it describes; absent otherwise |
| `metric` | metric digest | |
| `grader` | grader digest | or `builtin` for the built-in metrics Examen fills, `builtin:decline` for the built-in decline judge, or `self:<harness>` for a value an external harness submitted itself |
| `self_reported` | bool | true exactly when `grader` is `self:<harness>`: the value is the harness's claim, never headlined, and every result that reads it carries the confound `self_reported` (spec 008) |
| `generation` | int | 1 for the first grading by this grader version of this trial, or of this candidate for a candidate-level grader, counted per annotator for an annotation grader; a re-grade appends the next |
| `value` | number, bool or string | typed by the metric; for an open-vocabulary metric, the raw label as proposed, normalized (spec 006) |
| `rationale` | text | a judge's reason, an annotator's note; empty for checks and built-ins |
| `annotator` | string | for an annotation grader: `human:<subject>`, the person who answered; empty otherwise |
| `created_at` | time | |

The current value of a metric for a trial is read from the latest
generation of the metric's producing grader, and for an `external`
trial and a metric with no producer on its task, from the latest
self-reported value; a candidate-level metric's, from its grader's
latest generation for that experiment and candidate (spec 007). An
open-vocabulary value is read at a taxonomy version as its canonical
label (spec 006). Earlier generations stay readable.

## Taxonomy

One version of a suite's mapping from the raw labels of one
open-vocabulary metric to its canonical labels (spec 006): one record
of a lineage named by the suite and the metric, as a baseline record is
of its name. Its owner is the suite's owner.

| Field | Type | Meaning |
|---|---|---|
| `suite` | object | the suite, every version of it: `{owner, name}` |
| `metric` | metric digest | an open-vocabulary `label` metric some version of the suite lists |
| `version` | int | 1 for the lineage's first record, then the next |
| `canonical` | []string | the canonical labels in the order results list them, at most `MAX_LABELS_PER_METRIC`, each at most `LABEL_MAX_BYTES` |
| `map` | object | each raw label it maps to one of `canonical`, at most `MAX_TAXONOMY_RAW_LABELS` entries |
| `source` | object | `{owner}`, applied by a person, or `{canonicalizer, experiment, generation}`, proposed by a canonicalizer's run for an experiment, whose judgment record the experiment keeps |
| `previous` | taxonomy id | the record this one follows; absent on the first |
| `digest` | string | `sha256:` of the canonical JSON of `canonical` and `map` |
| `created_by` | subject | the subject that applied it, or the experiment's `created_by` for a proposed record |

A record never changes. A change to the mapping appends the next
version, which maps every raw label the one before it mapped, and the
lineage's current record is its latest.

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

The per-metric results of a candidate's cells against a reference.
Computed by Examen (spec 008); never written by a client. A comparison
whose `final` is false is provisional: Examen recomputes it in place,
keeping its id, as trials finish and gradings arrive. Once `final` is
true it never changes, and computing again makes a new comparison.

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | the candidate's experiment |
| `candidate` | candidate digest | |
| `baseline` | baseline id | the reference, when it is a baseline |
| `reference_candidate` | candidate digest | the reference, when it is another candidate of the same experiment |
| `task`, `reference_task` | task digest | for a comparison whose variable is `task`: the candidate's task, and the task of the same experiment whose cell of the same candidate is the reference (spec 008), whose `unpaired_tasks` and `candidate_suite` are then empty; exactly one of `baseline`, `reference_candidate` and `reference_task` is set |
| `automatic` | bool | true for the comparison Examen keeps of a candidate against its experiment's `reference`; false for one computed on request (spec 003) |
| `state` | enum | `computing`, `complete`, `failed` |
| `final` | bool | true once both sides are final: every declared trial of each side is `done` and graded; false while a figure can still change |
| `variable` | []tuple field | what the comparison intends to vary: the tuple fields of one logical variable, `task_hash` for a comparison across tasks (spec 008) |
| `single_variable` | bool | no tuple field besides `variable` differs on any paired task (spec 008); layer confounds, missing values and self-reported values are scoped to the results they affect and leave it unchanged |
| `declared_provenance` | []string | the sides, `candidate` and `reference`, whose candidate is of kind `external`, so whose tuple its harness declared rather than an executor observed; empty otherwise (spec 008) |
| `confounds` | []string | the name of every confound of its results, computed: the union of its results' `confounds` names |
| `results` | []result | per metric and task, and per metric over the suite; below |
| `taxonomies` | object | for each open-vocabulary metric it reads: the id of the taxonomy record its canonical labels were read at (spec 006) |
| `unpaired_tasks` | object | `{candidate_only, reference_only}`, task names held by one side alone |
| `candidate_suite` | object | per metric, the candidate's own suite-level summary over the tasks of its suite with weight greater than zero, whatever the reference holds |
| `windows` | object | for each side, the earliest `started_at` and the latest `ended_at` of the trials it read |
| `verdict_counts` | object | per metric, the number of task-level results with each verdict |
| `correction` | object | `{method, primary, family}`: the correction applied, `benjamini_hochberg`; `primary`, the digest of the metric whose suite-level result is tested alone and reported uncorrected; and `family`, how many p-values the one corrected family held (spec 008) |
| `analysis_version` | string | `examen.analysis/v1` |
| `parameters` | object | the values it was computed with of `FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL`, `MIN_TRIALS_FOR_VERDICT`, `MIN_TRIALS_PER_TASK_IN_SUITE`, `MIN_SUITE_COVERAGE`, `MIN_TRIALS_FOR_P90_INTERVAL`, `BOOTSTRAP_RESAMPLES`, `PERMUTATION_EXACT_MAX`, `BOSCHLOO_GRID_POINTS` and `TIE_TOLERANCE`, and `PERMUTATION_RESAMPLES` as resolved for the primary result and for the family |
| `inputs_digest` | string | the digest of its inputs document (spec 008) |
| `seed` | string | the root seed, hex |
| `computed_at` | time | |

A **result** is one row of `results`:

| Member | Meaning |
|---|---|
| `level` | `task`, over one task's cells; `suite`, over the paired tasks it includes; or `candidate`, a candidate-level metric's one value per side, which is descriptive (spec 008) |
| `metric`, `metric_name`, `direction` | the metric's digest, name and direction |
| `task`, `task_digests` | on a `task` result: the task's name, and each side's task digest; for a comparison across tasks, both tasks' names joined by ` vs ` |
| `candidate`, `reference` | the two sides' summaries, below |
| `delta`, `delta_kind` | the change, candidate less reference, and its kind: `difference`, or for a `label` metric `per_label`, an object from each label to the difference of its proportions |
| `delta_interval` | the delta's interval at `CONFIDENCE_LEVEL`, for `per_label` an object from each label to its interval; absent where spec 008 computes none |
| `method`, `exact`, `resamples` | the test: `boschloo_exact`, `permutation`, `stratified_permutation`, and once label tests are built `fisher_freeman_halton` or, when its tables are too many to enumerate, `chi_square_permutation` (spec 008); whether it enumerated every case; how many relabelings or tables it enumerated or drew, absent for `boschloo_exact`; all three absent on a `candidate` result |
| `seed` | this result's derived seed, hex |
| `p`, `q` | the uncorrected p-value, and the value after the correction at `FALSE_DISCOVERY_RATE`; on the primary metric's suite-level result, which is not corrected, `q` equals `p` |
| `verdict` | one of the verdicts of the glossary; absent on a `candidate` result, and on a result of a `label` metric until label tests are built (spec 008) |
| `reason` | one plain sentence saying why the verdict is what it is, such as "p = 0.048; q = 0.19 after correcting 40 results at 5%" or "task t3 has 3 trials; 5 needed" (spec 008) |
| `left_out` | on a `suite` result: each task of weight above zero that the result does not include, `{task, why, candidate, reference}`, `why` either `unpaired` or `too_few_trials`, with each side's valued trials |
| `confounds` | the result's confounds, below: on a task result for that task's cells, on a suite result for the tasks it includes |

An `insufficient` result carries both summaries and no `p`, `q` or
`delta_interval`.

A **confound** is one entry of a result's `confounds`:

| Member | Meaning |
|---|---|
| `name` | a tuple field besides `variable`; a layer confound, `observed.model`, `sent.model`, `sent.reasoning`, `observed.service_versions` or `declared.image`; `missing`; `self_reported`; or, once adoption is built, `window` (spec 008) |
| `tasks` | on a suite result, the task names it applies to |
| `candidate`, `reference` | per side: for `observed.model`, `sent.model` and `sent.reasoning` the number of requests it covers; for `observed.service_versions`, `declared.image` and `self_reported` the number of finished trials it covers; for `missing` the number of finished trials without a value; absent for a tuple field and for `window` |

A **summary** describes one metric over one set of trials, a cell or a
candidate's tasks:

| Member | Meaning |
|---|---|
| `aggregate` | the metric's `aggregate`; absent for a candidate-level metric |
| `n` | the finished trials of the set that the metric applies to |
| `not_applicable` | the finished trials of the set outside the metric's `applies_to`, which are in neither `n` nor `missing` |
| `value` | the rate, mean, median or p90; for `distribution`, an object from each label to its share; for `mode_share` and `distinct_count`, the statistic; for a candidate-level metric, its one current value |
| `numerator` | for `rate`: the finished trials whose value is true; for `mode_share`: the trials holding the most frequent label |
| `denominator` | the finished trials holding a current value of the metric; for a cell-level metric, of its `of` metric, less those `exclude_labels` leaves out |
| `excluded_labels` | for a cell-level metric: the valued trials its `exclude_labels` left out, by label |
| `missing` | `n` less `denominator`, and for a cell-level metric less the trials `excluded_labels` counts |
| `interval` | `[low, high]` at `CONFIDENCE_LEVEL`; absent where spec 008 computes none, such as a `p90` of fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values |
| `mean`, `sd`, `min`, `max` | for a numeric metric, whatever its aggregate |
| `distribution`, `mode` | for a label metric, and for a cell-level metric of the label metric it summarizes: each label's count and proportion, and the mode |
| `unmapped` | for an open-vocabulary metric: the finished trials whose raw label the taxonomy record read does not map, which are counted in `missing` (spec 006) |
| `tasks`, `coverage`, `advice` | at suite level: the number of tasks included, the share of the suite's weight they hold, and one sentence saying whether more trials or more tasks would narrow the interval more (spec 008); the tasks left out are named in the result's `left_out` |
| `excluded` | the trials of the set not counted in `n`, by reason: `retried`, `infra_failed`, `over_budget`, `canceled`, `not_done` |
| `final` | true once every declared trial of the set is `done` and graded; false while the figure can still change |

## Bar

A rule on a comparison that passes or fails.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner and suite |
| `suite` | object | the suite, every version of it: `{owner, name}` |
| `rules` | []object | at most `MAX_RULES_PER_BAR`; each: `metric` (a metric digest), exactly one of `no_regression` (true: fails on any `regressed` verdict for the metric in its scope), `at_least` and `at_most` (a value the candidate's suite-level summary must hold), and for `no_regression` a `scope`: `all` (the default), `suite` or `tasks` (spec 009) |
| `require_single_variable` | bool | fails when the comparison is confounded |
| `allow_partial` | bool | false by default: a bar fails `incomplete` when any task of the suite is left out of a suite-level result it reads; true lets a result covering at least `MIN_SUITE_COVERAGE` stand (spec 009) |

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
| `links` | object | computed when read and never stored: absolute links to the evaluation, its comparison and the comparison's page in a console, built on the installation's public address (spec 003) |

Its owner is the bar's owner.

## Share

An owner's grant to another owner: the "Only people invited" way of
sharing, which spec 013 defines with links and listings. Shares are a
later build step than the console path (spec 003).

| Field | Type | Meaning |
|---|---|---|
| `object` | enum | `suite`, `baseline`, `experiment` or `publication` |
| `suite` | object | for `suite` and `baseline`: the suite, every version of it, `{owner, name}` |
| `baseline` | string | for `baseline`: the baseline's name, which covers every record of its lineage |
| `experiment` | experiment id | for `experiment` |
| `publication` | publication id | for `publication` |
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
| `path` | string | the export's folder: `files/evals/exports/<export id>/` in the owner's space |
| `records` | int | the records written |
| `manifest` | object | `{path, digest}` of its manifest, set at `complete` |
| `error` | object | when `failed`: an error object of spec 003 |
| `created_by` | subject | the subject that created it |
| `completed_at` | time | |

## Report

A frozen document of an experiment's grid and comparisons (spec 010).

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | |
| `comparisons` | []comparison id | complete comparisons of that experiment, at most `MAX_COMPARISONS_PER_REPORT` |
| `document` | object | the report document of spec 010 as built at creation, format `examen.report/v1`; never changes |
| `document_digest` | string | `sha256:` of its canonical JSON |
| `narrative` | object | `{claims, findings, linter_version, draft, released, history}`: the claims as last edited and the linter's findings on them; the draft with its `model` and `prompt_digest`; the released narrative with its claims, its diff against the draft, `linter_version`, `released_by` and `released_at`; and every narrative released before it |
| `exports` | []object | each static export: `{n, path, manifest_digest, exported_at, narrative_released}` |

A report is shared by publishing it (spec 013).

## Publication

A report, a benchmark or a dataset its owner shares as immutable
versions, each a snapshot (spec 013).

| Field | Type | Meaning |
|---|---|---|
| `kind` | enum | `report`, `benchmark` or `dataset` |
| `name` | string | unique per owner and kind |
| `title` | string | one line for people, at most `PUBLICATION_LINE_MAX_BYTES` |
| `description` | text | optional, at most `PUBLICATION_TEXT_MAX_BYTES` |
| `visibility` | enum | `private` (the default), `unlisted` or `public`; `public` is reached only through a listing (spec 013) |
| `listing` | object | while `public`: `{listed_at, listed_by}` |
| `paused` | object | while an administrator holds it paused: `{at, by, reason}` |
| `submissions` | enum | for `benchmark`: `owner_only` (the default), `reviewed` or `open` |
| `link` | object | `{token, url}`: the link token, 256 bits from `crypto/rand` in base64url, and the link built on `EXAMEN_PUBLIC_URL`; answered only to a caller the authorizer allows `publication.update`, and never in a list, an event, a log line or an error |
| `versions` | []object | its publication versions, below, oldest first |
| `latest` | int | computed: the `number` of its newest version in state `published`; absent when none is |
| `created_by` | subject | the subject that created it |

A **publication version** is a record of a publication, named by the
publication's id and its `number`, with no identifier or owner of its
own:

| Field | Type | Meaning |
|---|---|---|
| `number` | int | 1 for the first, then the next |
| `source` | object | what it snapshots, one of: `{report}`, a report of the owner's, for `report`; `{suite}`, a suite digest of the owner's, for `benchmark`; `{export}`, a `complete` dataset export of the owner's, for `dataset` |
| `evidence_level` | enum | `aggregates` (the default), `outputs` or `full`; a benchmark's is `aggregates` |
| `exclude` | []string | evidence paths the owner left out of this version, relative to the version's folder (spec 013) |
| `dataset` | object | for `dataset`: `{license, citation}`, an SPDX license identifier or expression, required, and the Citation File Format fields the owner gives (spec 013) |
| `attribution` | object | `{name}`: the name the version shows for its owner, at most `PUBLICATION_LINE_MAX_BYTES` (spec 013) |
| `state` | enum | `preparing`, `scanning`, `published`, `blocked`, `failed`, `withdrawn` |
| `snapshot` | object | the snapshot document, format `examen.publication/v1`, set at `published` and never changed (spec 013) |
| `digest` | string | `sha256:` of the snapshot's canonical JSON |
| `folder` | string | for a version that carries files: `files/evals/publications/<publication id>/<number>/` in the owner's Arca space (spec 007) |
| `evidence_link` | object | for a version with a folder, while `published` and not paused: `{id}`, the Arca link on its folder; its token is held for building links and leaves Examen only inside the links of a read |
| `input_links` | []object | for a `benchmark` while `published` and not paused: `{task, name, id}` per Arca input and reference of the suite's tasks and per check's `files`, each an Arca link on that object or folder, read only by the `prepare` job of an experiment run through the benchmark (spec 013); never answered |
| `scan` | object | for `outputs` and `full`: `{image, findings, scanned_at}`, each finding `{path, rule, line}` and never the text it matched |
| `error` | object | when `failed`: an error object of spec 003 |
| `published_by` | subject | the subject that asked for the version |
| `published_at`, `withdrawn_at` | time | |
| `withdrawn_by` | subject | |

States: `preparing` → `scanning` → `published` → `withdrawn`;
`blocked` from `scanning` when the scan finds a secret; `failed` from
`preparing` or `scanning` on any other failure. A version of evidence
level `aggregates` skips `scanning`. A `blocked` or `failed` version
never publishes; the owner publishes a new version (spec 013).

## Submission

One candidate of a report publication version entered in a benchmark's
results table (spec 013). Its owner is the submitter.

| Field | Type | Meaning |
|---|---|---|
| `benchmark` | object | `{publication, version}`: the benchmark version it is entered in |
| `report` | object | `{publication, version}`: the submitter's report publication version |
| `candidate` | candidate digest | the candidate of the report's experiment the entry shows |
| `state` | enum | `pending`, `accepted`, `rejected`, `withdrawn` |
| `entry` | object | the figures the entry shows, copied from the report version at submission: `{attribution, candidate, summary, confounds, declared_provenance, self_reported}`; `candidate` its name, kind, digest, model and reasoning, and for `external` its declared `harness` and `version`; `summary` its suite-level summary of the benchmark's primary metric (spec 013) |
| `created_by` | subject | the subject that submitted it |
| `decided_by`, `decided_at` | subject, time | for an accepted submission of a `reviewed` benchmark, and a rejected one |

States: `pending` → `accepted` or `rejected`; `accepted` → `rejected`
by the benchmark's owner; `withdrawn` from `pending` or `accepted`, by
the submitter or when its report version is withdrawn.

## Event

One entry of an experiment's event log (spec 003), written in the same
transaction as the change it reports. The **sink events**,
`experiment.completed`, `experiment.canceled`, `cell.parked`,
`bar.passed`, `bar.failed`, `publication.published`,
`publication.withdrawn` and `listing.requested`, are also delivered to the installation's
event sink (spec 003).

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | the log it belongs to; empty on a publication event |
| `publication` | publication id | on a publication event (spec 013), the publication whose own log it belongs to; empty otherwise |
| `seq` | int | its position in that log, from 1 |
| `type` | enum | `experiment`, `cell`, `trial`, `graded`, `comparison`, `experiment.completed`, `experiment.canceled`, `cell.parked` (spec 003), `bar.passed`, `bar.failed` (spec 009), `publication.published`, `publication.withdrawn`, `listing.requested` (spec 013) |
| `data` | object | what changed, by type (specs 003 and 009) |
| `at` | time | when the change happened, which is the event's `created_at` |
| `delivery` | enum | for a sink event: `pending`, `acknowledged` or `dropped` (spec 003) |

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
rollout executes (005), how each grader kind runs and how taxonomies
change (006), how evidence is referenced and exported (007), the
statistics (008), bar evaluation (009), reports (010) and sharing (013).

## Acceptance criteria

- Every identifier is its prefix, an underscore and a lowercase ULID,
  and identifiers of one kind sort by creation time.
- Applying changed content to a versioned object makes a new digest and
  leaves the earlier version readable and unchanged.
- A reference to a version by name alone, a model alias, an image by
  tag or a workspace by branch is refused at apply, naming the field.
- A task without an `instruction` applies; an inline input or reference
  longer than `MAX_INLINE_INPUT_BYTES`, two inputs or two references of
  one name, and an `outputs` path that is absolute or contains `..` are
  refused at apply, naming the field.
- A measurement under `self:<harness>` is `self_reported`, and no other
  is.
- A closed-vocabulary label metric without `labels`, an open one with
  them, more than `MAX_LABELS_PER_METRIC` labels, and a label longer
  than `LABEL_MAX_BYTES` are refused at apply, naming the field; a
  cell-level metric whose `of` is not a trial-level label metric the
  suite lists, and a candidate-level primary metric, are refused at
  apply.
- A measurement of a cell-level metric is refused; a candidate-level
  measurement names an experiment and a candidate and no trial.
- A taxonomy record and a published version never change; a change
  appends the next record or version.
- An `applies_to` outcome outside `completed`, `declined`, `timed_out`
  and `turn_limit`, and `exclude_labels` on a metric that is not of
  level `cell`, are refused at apply; an `outputs` entry ending in `/`
  names a directory.
- Two `external` candidates that differ only in their declared
  `reasoning` give tuples that differ in `reasoning` alone; ones that
  differ in `flags` differ in `program`.
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
