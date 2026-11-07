---
title: "Objects: metric, grader, task, suite, candidate, experiment, cell, trial, measurement, taxonomy, baseline, comparison, bar, bar evaluation, share, dataset export, report, publication, submission and event; their fields, states, identifiers and limits"
status: in-progress
track: core
depends_on:
  - specs/001-architecture.md
affects: [api/, internal/store/, internal/id/]
effort: medium
created: 2026-10-17
updated: 2026-11-01
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
| check | a command run after the system under test ends, in a `topos` trial's machine or, for a `cella` or `external` trial, in the check sandbox, producing measurements | a CI check |
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
| system under test | another word for a candidate: the agent, program or external harness configuration a trial exercises | |
| program | a program in an image, pinned by digest, that a candidate of kind `program` runs in a Cella sandbox: an agent command-line tool of any vendor, Examen's HTTP runner that calls an API, or any other (spec 005) | the tuple field `program`, which holds how it is started and configured |
| external harness | the owner's own program that runs the trials of a candidate of kind `external` wherever it likes, leasing them from Examen and submitting their outcomes and outputs (spec 005) | an executor, which is Examen's |
| lease (of external trials) | an external harness's hold on pending trials for `EXTERNAL_LEASE`, renewable, after which they return to pending (spec 005) | a Key leased from the key source, the delivery lease, and a work queue job's lease |
| check sandbox | a fresh Cella sandbox from a task's environment template, with network `none`, in which the checks of a `cella` or `external` trial run, with its outputs and references delivered; no system under test ever ran in it (spec 005) | the trial's machine, in which a `topos` trial's checks run |
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
| exploratory comparison | a comparison not declared before its experiment ran: one computed on request, or an automatic one against a reference set after a cell left `pending`; never headlined, and its primary metric is corrected with every other result (spec 008) | the automatic comparison against the reference the experiment declared before it ran |
| repeat rate | the cell-level statistic `repeat_rate` of a label metric: the share of pairs of a cell's trials that hold one label, an unbiased estimate of the probability that two of its trials agree (spec 008) | a mode share, which depends on the number of trials and is descriptive only |
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
| benchmark | a publication of a suite version that others may read and its owner's side runs, with the results its owner's side submits to it; open, not held out: whoever runs it reads its references and checks (spec 013) | a public benchmark's content, imported as ordinary tasks (spec 001) |
| submission | one candidate of a report publication version entered in a benchmark's results table (spec 013) | an external trial's submit |
| event | one entry of an experiment's event log; a sink event is also delivered to the installation's event sink (spec 003) | a telemetry event |
| key source | what leases the Lux Keys that model judges, `cella` rollouts and narrative drafts call through: `endpoint`, or `lux` after v0.1; an installation without one runs the v0.1 profile (spec 004) | the Keys Topos issues its own sessions |
| spending cap | the US-dollar cap an experiment's create gives as `budget.cap_usd_micro` in place of a Budget's name, for a hard Budget made for the experiment (spec 004) | a Lux Key's own spend cap |
| v0.1 profile | what an installation serves with no owner token source, no key source and no file store: definitions, `external` candidates, experiments in modes `execute` and `import`, leases and submits, past trials imported, self-reported values, the grid, comparisons and the progress stream; the first release serves exactly this (specs 003, 005 and 011) | |

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
one owner never reference another owner's objects: a share grants
reading alone (spec 013), so an experiment runs a suite and names a
baseline of its own owner's, and a baseline or a bar names a suite of
its own owner's. A published benchmark runs on its owner's side alone,
and a submission names a benchmark of its own owner's (spec 013).

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
the grader of every measurement that external harness submitted itself (spec
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
| `MIN_TRIALS_FOR_P90_INTERVAL` | 29 | valued trials below which a `p90` summary carries no interval: the fewest for which a pair of order statistics covers the 90th percentile at a `CONFIDENCE_LEVEL` of 0.95, since below it every value lies under that percentile with probability 0.9^n, above 5%; a suite-level `p90` needs it of every task it includes | 008 |
| `MAX_INFRA_RETRIES` | 2 | retries of a trial that failed for an infrastructure reason | 005 |
| `MAX_MACHINE_WAITS` | 8 | retries of one index whose run found no machine free, which spend none of `MAX_INFRA_RETRIES`; a run past them that finds none spends one | 005 |
| `MAX_CELLS_PER_EXPERIMENT` | 2000 | tasks × candidates in one experiment | 003 |
| `MAX_TASKS_PER_SUITE` | 1000 | tasks in one suite version | 002 |
| `MAX_METRICS_PER_SUITE` | 64 | metrics in one suite version | 002 |
| `MAX_CANDIDATES_PER_EXPERIMENT` | 32 | candidates in one experiment | 003 |
| `MAX_DECLARED_COMPARISONS` | 128 | comparisons one experiment declares; it holds every pair of 16 candidates, and eleven per model for ten models | 003, 008 |
| `MAX_ACTIVE_EXPERIMENTS_PER_OWNER` | 16 | experiments of one owner neither `complete` nor `canceled` | 004 |
| `DEFAULT_PARALLELISM` | 8 | trials of one experiment running at once, unless set lower or higher | 005 |
| `MAX_PARALLELISM` | 256 | the highest parallelism an experiment may set | 005 |
| `DEFAULT_TRIAL_TIMEOUT` | 30 minutes | wall time of one trial when a task names none | 005 |
| `MAX_TRIAL_TIMEOUT` | 24 hours | the longest timeout a task may set | 005 |
| `CHECK_TIMEOUT` | 10 minutes | wall time of all of one trial's checks together | 006 |
| `TRIAL_GRACE` | 10 minutes | a trial's time past its `timeout` and `CHECK_TIMEOUT` before examend cancels it as `deadline_passed` | 005 |
| `TRIAL_START_TIMEOUT` | 1 hour | how long a trial may stay pending while its rollout runs fewer trials than its parallelism | 005 |
| `EXTERNAL_LEASE` | 10 minutes | how long one lease of an external trial, or one renewal of it, holds the trial before it returns to pending | 005 |
| `MACHINE_WAIT` | 30 seconds | how long the retry of a trial whose run found no machine free waits after that trial ended before its run starts, doubled for each such retry before it in its lineage, up to `MACHINE_WAIT_MAX` | 005 |
| `MACHINE_WAIT_MAX` | 5 minutes | the longest one wait of `MACHINE_WAIT` grows to | 005 |
| `REPORT_INTERVAL` | 15 seconds | how often an open rollout is reported | 005 |
| `USAGE_SETTLE_DELAY` | 2 minutes | the wait after a trial ends before Lux's records of it are read | 005 |
| `USAGE_EVERY` | 1 minute | how often the records of the calls made with one experiment's external Key are read | 005 |
| `USAGE_LOOKBACK` | 30 minutes | how far before the end of the last read each read of an experiment's records starts again, so the record of a call that ended after a read of its start is kept | 005 |
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
| `PERMUTATION_RESAMPLES` | the larger of 10,000 and ceil(20 · m / α), with m the number of p-values in the test's family and α the level that family is held at: for a comparison, m is the corrected family's size, 1 for the primary metric's suite-level result of a comparison that is not exploratory, D, the number of declared comparisons, for a declared comparison's primary result, 1 for an experiment's test of independence, and α is `FALSE_DISCOVERY_RATE`; for a benchmark's results table, m is K(K − 1) / 2 over its K ranked entries and α is 1 − `CONFIDENCE_LEVEL` | relabelings of a Monte Carlo permutation test | 008 |
| `PERMUTATION_EXACT_MAX` | 200,000 | the most relabelings a permutation test, or tables Freeman and Halton's test, enumerates instead of sampling; it holds C(20, 10) = 184,756, so two cells of 10 trials are tested exactly | 008 |
| `PERMUTATION_STOP_EXCEEDANCES` | 200 | the relabelings at least as extreme as the observed one after which a Monte Carlo permutation test stops drawing (Besag and Clifford's sequential rule); a stopped estimate's relative standard error is about 1 / sqrt(200), 7% | 008 |
| `WINDOW_TOLERANCE` | 7 days | how far apart the starts, or the ends, of two sides' trial time windows may lie before a result carries the confound `window` | 008 |
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
| `ADDRESS_REQUESTS_PER_MINUTE` | 6,000 | requests one client address may make to the routes that take a token on one replica in a minute, counted before the token is read | 012 |
| `READ_HEADER_TIMEOUT` | 10 seconds | how long examend waits for a request's headers to arrive | 012 |
| `READ_TIMEOUT` | 1 minute | how long examend waits for a whole request, its headers and its body, to arrive | 012 |
| `IDLE_TIMEOUT` | 2 minutes | how long examend keeps a connection open with no request on it | 012 |
| `IDEMPOTENCY_WINDOW` | 24 hours | how long an `Idempotency-Key` answers its first response | 003 |
| `IDEMPOTENCY_KEY_MAX_BYTES` | 255 | the longest `Idempotency-Key` | 003 |
| `SIGN_IN_MAX_BYTES` | 255 | the longest `sign_in` an experiment's create carries | 003 |
| `IDEMPOTENCY_RESERVATION` | 5 minutes | how long an `Idempotency-Key` whose first request is still being served holds off a repeat with `request_in_progress`; it lapses so a repeat runs when a replica stopped mid-request | 003 |
| `REQUEST_DEADLINE` | 1 minute | how long examend serves one request that carries an `Idempotency-Key` before it abandons it; shorter than `IDEMPOTENCY_RESERVATION`, so no first request ends after its reservation lapsed | 003 |
| `STREAM_POLL_INTERVAL` | 1 second | how often an open stream reads the event log for new entries | 003 |
| `STREAM_KEEPALIVE` | 15 seconds | the longest a stream stays silent before a keepalive comment | 003 |
| `MAX_STREAMS_PER_SUBJECT` | 10 | streams one subject holds open on one replica | 003 |
| `OWNER_TOKEN_MAX_LIFETIME` | 15 minutes | the lifetime Examen asks of the minter, and the longest it uses any owner token after obtaining it | 004 |
| `OWNER_TOKEN_RENEWAL` | 2 minutes | how long before its expiry a cached owner token is replaced, and how long a token file's read is reused | 004 |
| `METADATA_MAX_PAIRS` | 16 | pairs in the `metadata` of any object, and in an external candidate's `flags` | 002 |
| `MAX_TASK_FACTORS` | 16 | factors one task declares | 002 |
| `MAX_INLINE_INPUT_BYTES` | 64 KiB | the text of one inline input or reference of a task | 002 |
| `REFERENCE_MAX_BYTES` | 16 MiB | the bytes of all of one task's references together that are delivered to its checks; more ends the trial's checks `not_run` without running them | 006 |
| `MAX_OUTPUT_FILES` | 10,000 | files one trial's outputs may hold, every file of a directory output counted | 005 |
| `MAX_OUTPUT_BYTES` | 1 GiB | bytes of the output files one trial keeps | 005 |
| `OUTPUT_FILE_KEEP_MAX_BYTES` | 16 MiB | the largest output file kept; a larger one is kept by its digest and size alone, listed in the trial's output manifest | 005 |
| `LABEL_MAX_BYTES` | 128 | one label: declared by a metric, proposed by a judge for an open-vocabulary metric, or canonical in a taxonomy | 002 |
| `MAX_LABELS_PER_METRIC` | 64 | the labels a closed-vocabulary metric declares | 006 |
| `MAX_LABELS_OPEN_VOCABULARY` | 4096 | the canonical labels of one taxonomy version of an open-vocabulary metric, which grows with every topic its judges meet | 006 |
| `MAX_TAXONOMY_RAW_LABELS` | 10,000 | the raw labels one taxonomy version maps | 006 |
| `BUDGET_STATUS_TTL` | 15 seconds | how long an experiment's budget figures, read through the key source, are reused for its answers | 004 |
| `PUBLIC_REQUESTS_PER_MINUTE` | 120 | requests one client address may make to the public routes of one replica in a minute: the build identity and the contract below the base path, and the public reads of spec 013 | 013 |
| `PUBLICATION_LINE_MAX_BYTES` | 200 | a publication's title, and the attribution a version shows | 013 |
| `PUBLICATION_TEXT_MAX_BYTES` | 16 KiB | a publication's description, and a dataset's citation as canonical JSON | 013 |
| `PUBLICATION_EVIDENCE_MAX_BYTES` | 10 GiB | the evidence and files one publication version copies into its folder | 013 |
| `SECRET_SCAN_TIMEOUT` | 30 minutes | wall time of one publication version's secret scan | 013 |
| `INPUT_LINK_TTL` | 1 hour | how long an Arca link a `prepare` job mints on a published benchmark's input, reference or check files serves | 005 |
| `MAX_BENCHMARK_ENTRIES` | 32 | accepted entries of one benchmark version; its results table tests every pair of them, so the tests grow with the square of the entries and each test's relabelings grow with it again | 013 |
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
| `aggregate` | enum | how a cell summarizes its trials: `rate` (binary), `mean`, `median`, `p90`, `distribution` (label); for a cell-level metric, `repeat_rate` (the share of pairs of the cell's valued trials that hold one label; type `score`, range [0, 1]; tested), `mode_share` (the share of the cell's valued trials that hold its most frequent label; type `score`, range [0, 1]; descriptive only) or `distinct_count` (the number of distinct labels among them; type `count`; descriptive only) (spec 008); absent for a candidate-level metric, whose one value is its summary |
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
| `model_judge` | object | for `model_judge`: `model` (a pinned model id), `prompt` (text), `rubric` (text), `inputs` (which evidence it reads: `transcript`, `final_message`, named `files`, named `outputs`, a directory output by one file of it, `<output>/<path>`, or whole, `<output>/`, whose UTF-8 text files it reads in path order, and `checks`, the values the trial's checks recorded, spec 006), `temperature`, `reasoning` (optional: the reasoning level the call names; the model's default when absent), `preamble` (Examen's judge preamble, pinned at apply), and `role`: `grade` (the default), or `canonicalize` for a canonicalizer, which produces exactly one open-vocabulary metric, reads that metric's raw labels and no evidence, and answers taxonomy versions instead of measurements (spec 006). Which providers may keep what a judge sends is the key source's, not the grader's (spec 004) |
| `agent_judge` | object | for `agent_judge`: `agent` (a pinned Topos agent version digest), `inputs`, `instructions`, `preamble` (Examen's agent judge preamble, pinned at apply) |
| `annotation` | object | for `annotation`: `instructions` for the person, `sample` (the fraction of trials to queue) |

A grader's digest covers every field above. Changing a judge's model,
prompt, rubric, inputs, temperature, reasoning, preamble, role or level therefore
makes a new grader version, and its measurements are a new generation
(spec 006).

**One producer per metric.** For each task of a suite version, each
trial-level metric the suite lists has at most one producer that is not
an annotation grader: one of the task's `checks`, the task's `graders`, the suite's
`graders` and, for `declined`, the suite's decline judge; `builtin` for the other
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
| `graders` | []grader digest | judges and annotation queues of level `trial` applied to this task's trials alone, beside the suite's `graders`, such as a judge whose prompt speaks of the task's seed project; none of them is one of the suite's graders |
| `timeout` | duration | the trial's wall time, at most `MAX_TRIAL_TIMEOUT`; `DEFAULT_TRIAL_TIMEOUT` when unset |
| `max_turns` | int | optional bound on the model responses of the agent's own thread; the `topos` executor needs a step bound Topos does not have today (spec 005, requirement R7), and the `cella` and `external` executors pass it on as advice |
| `evidence` | []path | files of the workspace kept as evidence besides the transcript |
| `factors` | map | the task's place in a design: each factor's name to the task's level of it, at most `MAX_TASK_FACTORS` pairs, such as `{"object": "build", "volition": "want"}`; declared comparisons and a test of independence select and stratify cells by them (Cell, below; spec 008) |
| `metadata` | map | at most `METADATA_MAX_PAIRS` |

An input or reference name is 1 to 63 characters of `[a-z0-9._-]`,
starting with a letter or a digit, unique within its list; an `outputs`
path and an Origo `path` are relative and contain no `..`. The task's
digest covers its inputs and references as declared, a pinned digest or
commit and inline text included, and is its `task_hash` in the
provenance tuple.

A factor's name and its level are each 1 to 63 characters of
`[a-z0-9._-]`, starting with a letter or a digit, and a name is none of
the cell variables Examen defines itself: `candidate`, `task`, `model`,
`reasoning` and `batch` (Cell, below). Factors are part of the task's
content, so changing one makes a new task version; a task that declares
none has the digest it had before factors existed.

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
| `program` | object | for `program`: `image` (digest-pinned), `command`, `env` (no secrets), `config` (an object the program defines and reads, such as the HTTP runner's request, spec 005), `model` and `reasoning` (optional: the model and reasoning level the program is told to use; model access comes from the trial's Lux Key), `credentials` (optional: each `{audience, host, env}` or `{audience, host, file}`, an owner token for `audience` delivered as a Cella Secret scoped to `host`, the API's host name, with its placeholder under the variable `env` or in the file `file`; never an audience Examen acts through, a core's of spec 004 or Examen's own, which is refused at apply, spec 003), and `lux` (`surface`: the Lux API the program speaks, `anthropic_messages`, `openai_responses`, `openai_chat_completions`, `gemini` or `lux`, default `lux`, for which its Key is scoped; `key_env`, the variable the Key is mounted under, default `LUX_KEY`; `url_env`, the variable set to the surface's base URL, default `LUX_URL`) (spec 005) |
| `external` | object | for `external`: `harness` (the external harness's name, 1 to 63 characters of `[a-z0-9._-]`), `version` (a string the external harness declares for itself), `digest` (optional: a digest the external harness declares for its build), `model` and `reasoning` (optional: the pinned model id and the reasoning level the external harness runs its system with), and `flags` (optional: an object from a setting's name to its value, each a string, at most `METADATA_MAX_PAIRS` pairs: the external harness's own settings that change what it runs); declared by the external harness, never observed (spec 005) |
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
declare one external harness and model and differ only in `reasoning` are
compared on `reasoning` alone.
`grader_digests` holds every grader of the cell's trials: the task's
checks, the task's graders, the suite's graders and the suite's decline
judge. Every field
is pinned: an agent named without a version digest, a model alias with
nothing recording what served it, an image by tag, a workspace by
branch, is refused when applied. An `external` candidate's fields are
pinned by its external harness's declaration, which Examen cannot observe, so a
comparison that reads one says so in `declared_provenance` (spec 008).
A comparison's confounds are computed from this tuple (spec 008).

## Experiment

One execution of a suite against candidates.

| Field | Type | Meaning |
|---|---|---|
| `suite` | suite digest | |
| `candidates` | []candidate digest | at most `MAX_CANDIDATES_PER_EXPERIMENT` |
| `trials` | int | N per cell, declared before any cell runs; `DEFAULT_TRIALS` when unset, at most `MAX_TRIALS_PER_CELL` |
| `budget` | object | `{name}`: the Lux Budget every model call of this experiment is metered against, as resolved at create: the Budget the request named, the one an authorizer's `budget` limit assigned, or the one created for the cap the request gave (specs 003 and 004); in the v0.1 profile, without an authorizer's `budget` limit, `{cap_usd_micro}` alone, the spending cap the request gave, which no Lux Budget holds since nothing the profile serves calls a model through Examen (spec 003). Read with `cap_usd_micro`, `spent_usd_micro` and `remaining_usd_micro` beside `name`, computed through the key source when read, at most `BUDGET_STATUS_TTL` old, and never stored |
| `usage` | object | computed on a read of one experiment whose runs hold its external Key, never stored: `{requests, cost_usd_micro, unpriced_requests, unattributed, read_to}`, what Lux recorded of the calls made with the Key, `unattributed` those no trial claims (specs 003 and 005) |
| `benchmark` | object | `{publication, version}`: the published benchmark version through which the experiment runs another owner's suite (spec 013); absent otherwise. No experiment holds one while a benchmark runs on its owner's side alone, since that create is refused; the field is kept so the decision can be reversed |
| `parallelism` | int | `DEFAULT_PARALLELISM` when unset, at most `MAX_PARALLELISM` |
| `reference` | object | what every other candidate is compared with: `{baseline}`, a baseline record's id, or `{candidate}`, the digest of one of the experiment's candidates; `{candidate}` naming the first candidate when unset. The reference set before any cell leaves `pending` is the declared one; a reference set later makes exploratory comparisons (spec 008) |
| `adopt` | bool | reuse finished trials of identical cells no older than `ADOPTION_MAX_AGE`; after v0.1, a later build step (spec 005) |
| `mode` | enum | `execute` (the default): Examen runs every cell; `import`: Examen runs no trial, and each cell is made from the resolved provenance tuple of the first trial imported into it (below) |
| `comparisons` | []object | the comparisons the experiment declares it will make, at most `MAX_DECLARED_COMPARISONS`, set at create and never changed, each in one of three forms: `{candidate, reference_candidate, variable}`, two different candidates of the experiment and optionally one logical variable of spec 008 other than `task`; `{candidate, task, reference_task}`, one candidate and two different tasks of the suite, by digest; or the general form `{variable, levels, where, by}`: the cell variable it varies (Cell, below), its two `levels`, the candidate side's first, `where`, cell variables each held to one level, and `by`, the cell variables it is stratified by (spec 008). Each form takes an optional `metric`, a metric of the suite of level `trial` or `cell` whose result is the comparison's primary result, the suite's `primary_metric` when absent. No comparison twice, a pair in either orientation. Each is computed without being asked and is not exploratory, and the primary results of all of them are corrected together (spec 008). Empty when the experiment declares none |
| `independence` | object | `{metric, variable, by, where}`: one trial-level label metric of the suite, by digest, whose dependence on one cell variable, `variable` (`candidate` when absent), the experiment declares it will test, stratified by the cell variables `by`, over the cells `where` selects (every cell when absent), as the experiment-level test of independence (spec 008); `by` is `["task"]` when absent for a variable of the candidate, `candidate`, `model` or `reasoning`, and `["candidate"]` for the task or a factor, and an empty `by` makes one stratum, named `all`; the variable holds at least two levels among those cells and some stratum holds two of them; set at create and never changed |
| `scheduling` | enum | how the cells take the experiment's `parallelism` and its external trials are leased, set at create and never changed: `task_by_task` (the default), a task's cells before the next task's, or `interleaved`, every cell of every task in turn, so trial `i` of every cell runs in one window whatever its task (spec 005) |
| `state` | enum | below |
| `created_by` | subject | the person who created it (spec 004), which may differ from `owner`: an experiment created in an organization's context is owned by the organization and created by the member. Set at create from the caller, never changed, and answered on every read |
| `judgments` | []object | one judgment record per grading that belongs to no trial: a candidate-level judge's, with `candidate` set, and a canonicalizer's, with `metric` set; each otherwise as a trial's `judgments` entry (spec 006) |
| `progress` | object | computed and never written: `{cells: {<state>: n}, trials: {<state>: n, retried: n}, outcomes: {<outcome>: n}}`; `trials.retried`, present when a retry replaced a trial, counts the trials a retry replaced, each also counted under its state, so `done` less `retried` counts each finished run once |
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
finished session or sandbox or a past trial an external harness reports,
names a task and a candidate of the experiment; its provenance tuple is
resolved from the records it came from, or for an external trial from
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

**Cell variables.** A declared comparison of the general form and a
test of independence (spec 008) name the cells they read by variables
every cell carries: `candidate`, its candidate's digest; `task`, its
task, named by name or digest; `model` and `reasoning`, its tuple's,
carried when not empty; and each factor its task declares, by the
factor's name. `batch` is reserved for comparisons across experiments
(spec 008). The tuple's other fields, an agent or image digest, a
program, a tool set, an environment and a workspace, are digests and
sets rather than levels a person names, and select no cell; a task
factor names what differs between tasks, such as `workspace: seeded`.

## Trial

One attempt by a candidate at a task.

| Field | Type | Meaning |
|---|---|---|
| `cell` | cell id | |
| `index` | int | 0 to N-1; the smoke trials take the first indices |
| `executor_ref` | string | the Topos session id, the Cella sandbox name, or for an `external` trial the reference its external harness submitted for the trial, when it submitted one |
| `lease` | object | for an `external` trial while it is leased: `{id, subject, expires_at}`, the lease's `lse_` id, the subject that holds it, and when it lapses unless renewed (spec 005) |
| `state` | enum | `pending`, `running`, `checking`, `done` |
| `outcome` | enum | once `done`, below |
| `failure` | object | for `infra_failed`, `over_budget` and `canceled`: `{code, detail}`, a stable code and a developer's detail |
| `retry_of` | trial id | when this trial retries an earlier one: an infrastructure failure, or an index a cell's retry runs again (spec 005) |
| `sent` | object | what was sent: the model and reasoning settings of each request, from the executor (spec 007) |
| `observed` | object | what happened: the model that served, token usage including reasoning, cost, from the executor's and Lux's records, and the service versions and the usage figures of the APIs it called that a program recorded or an external harness submitted (spec 007) |
| `declared` | object | for an `external` trial: what its external harness declared about the trial that Examen cannot observe, `harness_version` and `image`, a tag or a digest as the external harness names it (spec 005) |
| `checks` | []object | one check run record per check of the task, in order: `{grader, state, exit_code, reason, duration, stdout_size, stdout_sha256, stderr_tail, image, sandbox, generation, outputs}`, `state` one of spec 006's check run states, or `pending` until the run ended; `image` the image of the check sandbox it ran in, `sandbox` that sandbox's id, `generation` the generation its values appended; `outputs`, for a check that ran, its `stdout.txt` and `stderr.txt` as evidence references of kind `check_output` in spec 007's layout, absent where the store did not take them |
| `judgments` | []object | one judgment record per judge grading: `{grader, generation, state, attempts, request_digest, cuts, boundary, served_model, tokens, cost}`, `attempts` each `{lux_request, state}`, `state` one of spec 006's judgment states |
| `evidence` | []evidence ref | spec 007 |
| `outputs` | object | a summary of the trial's output manifest (spec 005): `{files, bytes, kept_bytes, not_kept, over_limit}`, the files and bytes its outputs held, the bytes kept, the files kept by digest and size alone, and, when its outputs passed `MAX_OUTPUT_FILES` or `MAX_OUTPUT_BYTES`, the constant they passed |
| `imported` | bool | true when the trial was imported rather than executed: a finished session or sandbox, or a past trial of an external harness (spec 005) |
| `started_at`, `agent_ended_at`, `ended_at` | time | the system under test's first step; its end; the trial's end after its checks and evidence. For an `external` trial the first two are what its external harness submitted, and for an imported external trial all three (spec 003) |

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
| `self_reported` | bool | true exactly when `grader` is `self:<harness>`: the value is the external harness's claim, never headlined, and every result that reads it carries the confound `self_reported` (spec 008) |
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
| `canonical` | []string | the canonical labels in the order results list them, at most `MAX_LABELS_OPEN_VOCABULARY`, each at most `LABEL_MAX_BYTES` |
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

A baseline's owner is the owner that pinned it, which is its suite's
owner: a suite shared with another owner is read there and never used
(spec 013). Moving a
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
| `candidate` | candidate digest | the candidate side's candidate; for a declared comparison of the general form, the one candidate every cell of its candidate side belongs to, and empty when they belong to several |
| `baseline` | baseline id | the reference, when it is a baseline |
| `reference_candidate` | candidate digest | the reference, when it is another candidate of the same experiment |
| `task`, `reference_task` | task digest | for a comparison whose variable is `task`: the candidate's task, and the task of the same experiment whose cell of the same candidate is the reference (spec 008), whose `unpaired_tasks` and `candidate_suite` are then empty; exactly one of `baseline`, `reference_candidate`, `reference_task` and `declaration` is set |
| `declaration` | object | for a declared comparison of the general form: its declaration as the experiment's `comparisons` hold it, `{variable, levels, where, by, metric}`, whose two levels select its two sides' cells (spec 008); absent otherwise |
| `automatic` | bool | true for the comparison Examen keeps of a candidate against its experiment's `reference`; false for one computed on request (spec 003) and for a declared one that is not that pair |
| `declared` | int | for a comparison its experiment declared: its position in the experiment's `comparisons`, from 0; absent otherwise. A declared comparison is computed without being asked, as the automatic one is, and its primary result is corrected with the other declared comparisons' in the experiment's declared analysis (spec 008) |
| `exploratory` | bool | true for a comparison computed on request, and for an automatic one against a reference set after a cell left `pending`: it is never headlined, and its primary metric's suite-level result is corrected in the family (spec 008); false for the automatic comparison against the declared reference and for a declared comparison |
| `state` | enum | `computing`, `complete`, `failed` |
| `final` | bool | true once both sides are final: every declared trial of each side is `done` and graded; false while a figure can still change |
| `variable` | []tuple field | what the comparison intends to vary: the tuple fields of one logical variable, `task_hash` for a comparison across tasks and for a declared comparison whose cell variable is `task` or a task factor (spec 008) |
| `single_variable` | bool | no tuple field besides `variable` differs on any paired task (spec 008); layer confounds, missing, self-reported and excluded values, differing time windows and an uncalibrated judge are scoped to the results they affect and leave it unchanged |
| `declared_provenance` | []string | the sides, `candidate` and `reference`, whose candidate is of kind `external`, so whose tuple its external harness declared rather than an executor observed; empty otherwise (spec 008) |
| `confounds` | []string | the name of every confound of its results, computed: the union of its results' `confounds` names |
| `results` | []result | per metric and task, and per metric over the suite; below |
| `taxonomies` | object | for each open-vocabulary metric it reads: the id of the taxonomy record its canonical labels were read at, the suite's current record, or the record a bar pins for a comparison a bar evaluation asked for (specs 006, 008 and 009) |
| `unpaired_tasks` | object | `{candidate_only, reference_only}`, task names held by one side alone |
| `candidate_suite` | object | per metric, the candidate's own suite-level summary over the tasks of its suite with weight greater than zero, whatever the reference holds |
| `windows` | object | for each side, the earliest `started_at` and the latest `ended_at` of every trial the comparison read; a result's `window` confound carries the same figures over that result's trials alone, which may be narrower (spec 008) |
| `verdict_counts` | object | per metric, the number of task-level results with each verdict |
| `correction` | object | `{method, primary, family, declared}`: the correction applied, `benjamini_hochberg`; `primary`, the digest of the metric whose primary result is out of the comparison's family, tested alone and reported uncorrected, or for a declared comparison corrected in the declared family, absent on an exploratory comparison, whose primary result is in the family; `family`, how many p-values the one corrected family held; and `declared`, on a declared comparison alone, `D`, the size of the declared family its primary result is corrected in (spec 008) |
| `analysis_version` | string | `examen.analysis/v1` |
| `parameters` | object | the values it was computed with of `FALSE_DISCOVERY_RATE`, `CONFIDENCE_LEVEL`, `MIN_TRIALS_FOR_VERDICT`, `MIN_TRIALS_PER_TASK_IN_SUITE`, `MIN_SUITE_COVERAGE`, `MIN_TRIALS_FOR_P90_INTERVAL`, `BOOTSTRAP_RESAMPLES`, `PERMUTATION_EXACT_MAX`, `PERMUTATION_STOP_EXCEEDANCES`, `BOSCHLOO_GRID_POINTS`, `TIE_TOLERANCE` and `WINDOW_TOLERANCE`, and `PERMUTATION_RESAMPLES` as resolved for the primary result and for the family |
| `inputs_digest` | string | the digest of its inputs document (spec 008) |
| `seed` | string | the root seed, hex |
| `computed_at` | time | |

A **result** is one row of `results`:

| Member | Meaning |
|---|---|
| `level` | `task`, over one task's cells; `suite`, over the paired tasks it includes; or `candidate`, a candidate-level metric's one value per side, which is descriptive (spec 008) |
| `metric`, `metric_name`, `direction` | the metric's digest, name and direction |
| `task`, `task_digests` | on a `task` result: the task's name, and each side's task digest; for a comparison across tasks, both tasks' names joined by ` vs `; for a declared comparison of the general form, its stratum's name (spec 008) |
| `candidate`, `reference` | the two sides' summaries, below |
| `delta`, `delta_kind` | the change, candidate less reference, and its kind: `difference`, or for a `label` metric `per_label`, an object from each label to the difference of its proportions |
| `delta_interval` | the delta's interval at `CONFIDENCE_LEVEL`, for `per_label` an object from each label to its interval; absent where spec 008 computes none |
| `no_delta_interval` | when `delta_interval` is absent on a result that is not `insufficient`: one plain sentence saying why, from spec 008's templates |
| `method`, `statistic`, `exact`, `resamples` | the test: `boschloo_exact`, `permutation`, `stratified_permutation`, `fisher_freeman_halton` or, when its tables are too many to enumerate, `chi_square_permutation`; the statistic it orders by: `fisher_p` for `boschloo_exact`, `welch_t`, `studentized_quantile`, `difference`, `table_probability`, `pearson_x2` or, for the stratified label test, `pearson_x2_sum` (spec 008); whether it enumerated every case; how many relabelings or tables it enumerated or drew, absent for `boschloo_exact`; all four absent on a descriptive result |
| `seed` | this result's derived seed, hex |
| `p`, `q` | the uncorrected p-value, and the value after the correction at `FALSE_DISCOVERY_RATE`; on the primary metric's suite-level result of a comparison that is not exploratory, which is not corrected, `q` equals `p`; on a declared comparison's primary result, `p` alone, its `q` being the declared analysis's; on a result that reads self-reported values, which stays outside the family, `p` alone; on a pair result of two benchmark entries, `h`, its Holm-adjusted value, in place of `q` (spec 008) |
| `verdict` | one of the verdicts of the glossary; absent on a descriptive result: a `candidate` result, a `mode_share` or `distinct_count` result, a result that reads self-reported values, and a result of a `label` metric of a comparison computed before label tests were built, in v0.5.0; and on a declared comparison's primary result unless it is `insufficient`, whose verdict the declared analysis gives (spec 008) |
| `reason` | one plain sentence saying why the verdict is what it is, such as "p = 0.048; q = 0.19 after correcting 40 results at 5%" or "task t3 has 3 trials; 5 needed" (spec 008) |
| `left_out` | on a `suite` result: each task of weight above zero that the result does not include, `{task, why, candidate, reference}`, `why` either `unpaired` or `too_few_trials`, with each side's valued trials |
| `confounds` | the result's confounds, below: on a task result for that task's cells, on a suite result for the tasks it includes |

An `insufficient` result carries both summaries and no `p`, `q` or
`delta_interval`.

A **confound** is one entry of a result's `confounds`:

| Member | Meaning |
|---|---|
| `name` | a tuple field besides `variable`; a layer confound, `observed.model`, `sent.model`, `sent.reasoning`, `observed.service_versions` or `declared.image`; `missing`; `self_reported`; `incomplete`; `window`; `uncalibrated`; or, on a pair result of two benchmark entries only, `declared` or `taxonomy` (spec 008) |
| `tasks` | on a suite result, the task names it applies to |
| `candidate`, `reference` | per side: for `observed.model`, `sent.model` and `sent.reasoning` the number of requests it covers; for `observed.service_versions`, `declared.image`, `self_reported` and `declared` the number of finished trials it covers; for `missing` the number of finished trials without a value; for `incomplete` the number of trials excluded from analysis that no finished retry replaced; for `window` the side's window, `{from, to}`, the earliest `started_at` and the latest `ended_at` of the trials the result read; for `taxonomy` the taxonomy record the entry's values were read at; absent for a tuple field and for `uncalibrated` |
| `grader` | for `uncalibrated`: the judge version whose calibration the result read (spec 006) |

A **summary** describes one metric over one set of trials, a cell or a
candidate's tasks:

| Member | Meaning |
|---|---|
| `aggregate` | the metric's `aggregate`; absent for a candidate-level metric |
| `n` | the finished trials of the set that the metric applies to |
| `not_applicable` | the finished trials of the set outside the metric's `applies_to`, which are in neither `n` nor `missing` |
| `value` | the rate, mean, median or p90; for `distribution`, an object from each label to its share; for `repeat_rate`, `mode_share` and `distinct_count`, the statistic; for a candidate-level metric, its one current value |
| `numerator` | for `rate`: the finished trials whose value is true; for `mode_share`: the trials holding the most frequent label |
| `denominator` | the finished trials holding a current value of the metric; for a cell-level metric, of its `of` metric, less those `exclude_labels` leaves out |
| `excluded_labels` | for a cell-level metric: the valued trials its `exclude_labels` left out, by label |
| `missing` | `n` less `denominator`, and for a cell-level metric less the trials `excluded_labels` counts |
| `interval` | `[low, high]` at `CONFIDENCE_LEVEL`; absent where spec 008 computes none, such as a `p90` of fewer than `MIN_TRIALS_FOR_P90_INTERVAL` values |
| `no_interval` | when `interval` is absent: one plain sentence saying why, from spec 008's templates |
| `mean`, `sd`, `min`, `max` | for a numeric metric, whatever its aggregate |
| `distribution`, `mode` | for a label metric, and for a cell-level metric of the label metric it summarizes: each label's count and proportion, and the mode |
| `unmapped` | for an open-vocabulary metric: the finished trials whose raw label the taxonomy record read does not map; each such raw label counts as a label of its own in `distribution`, marked unmapped, and never as missing (spec 006) |
| `tasks`, `coverage` | at suite level: the number of tasks included, and the share of the suite's weight they hold (spec 008); the tasks left out are named in the result's `left_out` |
| `excluded` | the trials of the set not counted in `n`, by reason: `retried`, `infra_failed`, `over_budget`, `canceled`, `not_done` |
| `final` | true once every declared trial of the set is `done` and graded; false while the figure can still change |

## Declared analysis

What an experiment declared before it ran, with its outcome: the
correction of its declared comparisons' primary results together, and
its test of independence. Computed by Examen (spec 008) for an
experiment that declares `comparisons` or `independence`, and never
written by a client. It is recomputed as the declared comparisons are,
and replaced each time, so it always reads each declared comparison's
newest record.

| Field | Type | Meaning |
|---|---|---|
| `experiment` | experiment id | |
| `final` | bool | true once every declared comparison's newest record is final and every cell the test of independence reads is final |
| `family` | object | `{method, size, level}`: `benjamini_hochberg`; `D`, the number of comparisons the experiment declared, every one of which counts; and `FALSE_DISCOVERY_RATE` as the declared comparisons recorded it |
| `comparisons` | []object | per declared comparison, in the experiment's order: `{index, comparison, metric, p, q, verdict, reason, final}`, its position, the id of its newest comparison, absent until one is computed, the digest of its primary result's metric, its declaration's `metric` or the suite's primary metric, its primary result's `p` and the `q` the declared family gives it, absent when it has no usable `p`, which counts as 1, the verdict from that `q` by spec 008's rule, `insufficient` for an insufficient result and absent for a descriptive or self-reported one, its reason, and whether that comparison is final |
| `independence` | object | the experiment's independence result when it declares one: `metric`, `metric_name`, `variable`, `by` and `where` as declared, `levels` (the variable's levels, the sides, in order: the experiment's candidates in its order for `candidate`, ascending otherwise), `summaries` (each level's suite-level summary over the included strata, in that order), `tasks` (the strata included), `coverage`, `left_out` (each stratum of weight above zero left out, `{task, why, counts}`, `task` the stratum's name, `why` `unpaired` or `too_few_trials`, `counts` each level's valued trials), `method`, `statistic`, `exact`, `resamples`, `seed`, `p`, `q` (equal to `p`), `verdict` (`changed`, `within_noise` or `insufficient`; absent on self-reported values), `reason`, `confounds`, `final`, `analysis_version`, `parameters`, `inputs_digest` (spec 008); before any cell it reads holds a finished trial, `{metric, metric_name, variable, by, where, levels, reason, final}` alone, with the reason "not computed yet: no cell has a finished trial" and `final` false |
| `computed_at` | time | |

## Bar

A rule on a comparison that passes or fails.

| Field | Type | Meaning |
|---|---|---|
| `name` | string | unique per owner and suite |
| `suite` | object | the suite, every version of it: `{owner, name}` |
| `rules` | []object | at most `MAX_RULES_PER_BAR`; each: `metric` (a metric digest), exactly one of `no_regression` (true: fails on any `regressed` verdict for the metric in its scope), `at_least` and `at_most` (a value the candidate's suite-level summary must hold), and for `no_regression` a `scope`: `all` (the default), `suite` or `tasks` (spec 009) |
| `require_single_variable` | bool | fails when the comparison is confounded |
| `allow_partial` | bool | false by default: a bar fails `incomplete` when any task of the suite is left out of a suite-level result it reads, or any result it reads carries the confound `incomplete`; true lets a result covering at least `MIN_SUITE_COVERAGE` stand (spec 009) |
| `taxonomies` | object | for each open-vocabulary metric a rule reads, directly or as a cell-level metric's `of`: the id of the taxonomy record the bar reads comparisons at; filled at apply with the suite's current record when the body leaves it out, and covered by the digest (spec 009) |

A bar's owner is the owner that applied it, which is its suite's owner,
as a baseline's is.

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
later build step than the console path (spec 003). Every share grants
reading alone: its grantee reads what it names, a suite's versions and
what they pin, a baseline's records, an experiment's setup and
outcomes, a publication's versions, and runs, uses, changes and submits
nothing. No experiment, baseline or bar of the grantee's names a shared
suite or baseline. Running an experiment stays with its creator, its
owner and the owner's members (spec 004), and there is no member of an
experiment beside them.

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
| `submissions` | enum | for `benchmark`: `owner_only` (the default), `reviewed` or `open`; every value admits submissions from the benchmark's owner alone, so the three answer alike, and the field is kept so submissions can be reopened to others without a change to the object; a listed benchmark is `reviewed`, with at most one accepted entry per owner besides its own (spec 013) |
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
| `attribution` | object | `{name}`: the name the version shows for its owner, at most `PUBLICATION_LINE_MAX_BYTES`, or `null` when neither the authorizer nor the request names one; never a subject (spec 013) |
| `state` | enum | `preparing`, `scanning`, `published`, `blocked`, `failed`, `withdrawn` |
| `snapshot` | object | the snapshot document, format `examen.publication/v1`, set at `published` and never changed (spec 013) |
| `digest` | string | `sha256:` of the snapshot's canonical JSON |
| `folder` | string | for a version that carries files: `files/evals/publications/<publication id>/<number>/` in the owner's Arca space (spec 007) |
| `evidence_link` | object | for a version with a folder, while `published`: `{id}`, the Arca link on its folder, replaced when the publication's link token is replaced or it is made `private`; its token is held for building links and leaves Examen only inside the links of a read |
| `reference` | submission id | for a `benchmark`: the accepted entry of this version every other entry is compared with: the one the publication's owner names, else the owner's own accepted entry of the lowest submission id; absent when there is none (spec 013) |
| `results` | object | for a `benchmark`, computed: its results table as last computed, `{method, reference, common, entries, unranked, pairs}`, with `method` holding the test, the adjustment, the levels, the number of pairs, the resamples, the table's `inputs_digest` and `seed`, and `computed_at`; `common` the tasks every ranked entry is compared over, with the share of the suite's weight they hold; and `pairs` every pair result of two accepted entries (spec 008); recomputed when the version's accepted entries or its `reference` change, and never part of the snapshot (spec 013) |
| `scan` | object | for every version: `{image, findings, scanned_at}`, each finding `{path, rule, line}` and never the text it matched; `path` names the snapshot itself for a finding in its JSON |
| `error` | object | when `failed`: an error object of spec 003 |
| `published_by` | subject | the person who signed off on publishing the version: the subject of the request that asked for it, on whose behalf its `publish` job acts. Set from that request, never changed, answered on the signed-in reads of the version and on no public read; no name is stored for it (spec 013) |
| `published_at`, `withdrawn_at` | time | |
| `withdrawn_by` | subject | |

States: `preparing` → `scanning` → `published` → `withdrawn`;
`blocked` from `scanning` when the scan finds a secret; `failed` from
`preparing` or `scanning` on any other failure. Every version passes
`scanning`, since its snapshot's JSON is scanned at every evidence
level. A `blocked` or `failed` version never publishes; the owner
publishes a new version (spec 013).

## Submission

One candidate of a report publication version entered in a benchmark's
results table (spec 013). Its owner is the submitter's context, which
is the benchmark's owner: a submission from another owner is refused
(spec 013).

| Field | Type | Meaning |
|---|---|---|
| `benchmark` | object | `{publication, version}`: the benchmark version it is entered in |
| `report` | object | `{publication, version}`: the submitter's report publication version |
| `candidate` | candidate digest | the candidate of the report's experiment the entry shows |
| `state` | enum | `pending`, `accepted`, `rejected`, `withdrawn` |
| `entry` | object | the figures the entry shows, copied at submission: `{attribution, candidate, summary, confounds, declared_provenance, self_reported, window, taxonomy, values}`; `candidate` its name, kind, digest, `agent_or_image_digest`, `program`, model, reasoning and tools, the tuple fields a pair's confounds read (spec 008), and for `external` its declared `harness` and `version`; `summary` its suite-level summary of the benchmark's primary metric; `window` the earliest `started_at` and the latest `ended_at` of the trials `values` holds; `taxonomy`, for a primary metric that reads an open vocabulary, the taxonomy record its labels were read at; and `values`, per task of the suite, `{task, weight, values, tuple, layers}`: the task's name and weight, the primary metric's value of each of the candidate's finished trials in trial id order, a number or a boolean, or for a cell-level metric the canonical label of its `of` metric that the statistic is computed from, `null` for a trial without one, and never other trial content (spec 013); the cells' tuple fields outside the candidate, `task_hash`, `environment_digest`, `workspace_ref` and `grader_digests`; and the counts the confounds of spec 008 read: requests served by another model, requests of trials that sent none to the configured model, each reasoning value sent with its requests, each service version with its finished trials, each declared image with its finished trials, the finished trials whose value is self-reported, and the trials excluded from analysis that no finished retry replaced |
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
- A `program` candidate whose `credentials` name the audience of a core
  Examen acts through, or Examen's own, is refused at apply, naming the
  credential; a taxonomy version with more than
  `MAX_LABELS_OPEN_VOCABULARY` canonical labels is refused.
- A measurement is never updated; a re-grade appends the next
  generation.
- Every limit in this spec is enforced where it applies, with an error
  naming the constant.
- Metadata with more than `METADATA_MAX_PAIRS` pairs, or a key beginning
  `examen.`, is refused on every object that has it.
- An experiment's N cannot change once any of its cells has started.
- An experiment's `comparisons`, `independence` and `scheduling` are
  set at create and never change; a declared pair named twice, in
  either orientation, more than `MAX_DECLARED_COMPARISONS` of them, a
  candidate or task the experiment does not hold, a declaration of the
  general form whose variable no cell carries, whose level or `where`
  selects no cell, whose stratum holds two cells of one side or two
  cells that also differ in a variable it neither holds nor names, or
  that pairs no stratum, one comparison declared twice in any of its
  forms, a declared `metric` the suite does not list at
  level `trial` or `cell`, and an `independence` metric that is not a
  trial-level label metric of the suite, or whose variable holds fewer
  than two levels, are refused at create, naming the member.
- An experiment's `created_by` is the caller of its create and its
  `owner` the context the create resolved, the two different for an
  organization's experiment; both are stored and answered on every
  read, and neither changes.
- The decline judge never changes a trial's outcome.
- A task's `factors` with more than `MAX_TASK_FACTORS` pairs, a name
  or level outside the name rule, or a name Examen reserves, are
  refused at apply naming the field; a task without factors keeps the
  digest it had before factors existed.

## Outcome

In progress. v0.1.0 builds the identifiers, the five versioned kinds
with their canonical digests and version rows that never change, the
rules each kind is held to at apply, the provenance tuple, measurement
values and the current value, taxonomy records, and the limits table,
which a test holds to this spec. v0.2.0 adds an experiment's
`created_by` beside its `owner`, and v0.3.0 an experiment's
`comparisons` and `scheduling`, a comparison's `declared` and the
declared analysis. v0.5.0 adds a task's `factors`, the cell variables,
the general form of a declared comparison with its `metric` and a
comparison's `declaration`, and an experiment's `independence`. Not
built: bars, shares, dataset exports, reports, publications,
submissions, annotation items, re-grading, writing candidate-level
measurements, the cell variable `batch`, and any change to an
experiment after its create.

Every row below shipped in v0.1.0 unless it names another release.

| # | Verdict | Held by |
|---|---|---|
| 1 | Met | `id.TestPrefixedAndOrdered`, `id.TestAClockThatStepsBackKeepsTheOrder`, `id.TestPrefixesAreTheSpecs` |
| 2 | Met | `httpapi.TestApplyAndRead`, `postgres.TestMeasurementsAreAppendOnly` |
| 3 | Partly met: a name alone, an image by tag and a workspace by branch are refused naming the field; a model alias is recognized only as a name ending in `latest` or holding white space | `objects.TestNamesAndRefs`, `objects.TestGraderRules`, `httpapi.TestApplyPreconditionsAndRefusals` |
| 4 | Partly met: a task with no instruction applies, and an inline input over the bound, two inputs of one name and an absolute or `..` output are refused naming the field; two references of one name and an inline reference over the bound pass the same check and have no case of their own | `objects.TestTaskRules` |
| 5 | Partly met: a value under `self:<harness>` reads back `self_reported`; that no other is rests on a table constraint no test exercises | `httpapi.TestAnalyzedImport` |
| 6 | Met | `objects.TestMetricRules`, `objects.TestResolvedChecks` |
| 7 | Partly met: a submit or import refuses a metric that is not trial-level, and the table admits a trial's or a candidate's row alone; no test submits a cell-level metric, and nothing writes candidate-level measurements yet | no test yet |
| 8 | Partly met: a taxonomy change appends the next record; the trigger that refuses changing a record has no test, and published versions are not built | `httpapi.TestTaxonomies` |
| 9 | Met | `objects.TestMetricRules`, `objects.TestTaskRules` |
| 10 | Met | `objects.TestTuples`, `httpapi.TestAnalyzedImport` |
| 11 | Met | `objects.TestResolvedChecks`, `httpapi.TestApplyPreconditionsAndRefusals` |
| 12 | Partly met: a self-reported value for a metric the suite does not list, or one a grader produces, is refused; no other grader but the built-ins writes measurements yet | `httpapi.TestSubmitAndImportRefusals` |
| 13 | Partly met: a core's audience is refused naming the credential, and a taxonomy over `MAX_LABELS_OPEN_VOCABULARY` labels is refused; Examen's own audience is reserved and has no case of its own | `objects.TestCandidateRules`, `httpapi.TestApplyPreconditionsAndRefusals`, `httpapi.TestTaxonomies` |
| 14 | Partly met: the store refuses an update of a measurement; re-grading is not built | `postgres.TestMeasurementsAreAppendOnly` |
| 15 | Partly met: every constant holds its table's value, and the limits of what is built answer `limit_exceeded` with `details.constant`, asserted by name for `MAX_ACTIVE_EXPERIMENTS_PER_OWNER` alone; most constants of what is not built are not yet read | `limits.TestEveryLimitIsTheSpecs`, `httpapi.TestCreateLimits`, `objects.TestSuiteRules` |
| 16 | Met on tasks, candidates and experiments, the objects built that carry metadata | `objects.TestNamesAndRefs`, `objects.TestTaskRules`, `objects.TestCandidateRules`, `httpapi.TestCreateRefusals` |
| 17 | Partly met: no route changes an experiment, so N cannot change; no test asserts it | no test yet |
| 18 | Partly met (v0.3.0, v0.5.0): `comparisons`, `independence` and `scheduling` are set at create, and a pair named twice, more than `MAX_DECLARED_COMPARISONS`, a candidate or task the experiment does not hold, a general declaration that does not read as one comparison, a declared metric the suite does not list and an `independence` that is not a trial-level label metric or whose variable holds one level are refused naming the member; no route changes an experiment | `httpapi.TestDeclaredComparisons`, `httpapi.TestDeclaredComparisonsRefused`, `httpapi.TestGeneralDeclarationsRefused`, `objects.TestSelectRefusals`, `objects.TestSelectIndependence` |
| 19 | Met (v0.2.0) | `httpapi.TestAnOrganizationsExperimentKeepsItsCreator` |
| 20 | Partly met: the built-in decline judge derives `declined` from the outcome and writes only a measurement; no test reads the outcome after it ran, and a model decline judge is refused at create | `objects.TestValuesAndCurrent` |
| 21 | Met (v0.5.0) | `objects.TestTaskFactors`, `objects.TestDesign`, `httpapi.TestGeneralDeclarationsRefused` |
