---
title: "Grading: the check contract, model judges, agent judges, annotation queues, generations and re-grading, the decline judge, and calibrating judges against people"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
affects: [internal/grade/, internal/grade/check/, internal/grade/judge/, internal/grade/annotate/, internal/calibrate/]
effort: large
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Grading

## Overview

A grader turns a trial into measurements. Spec 002 names four kinds: a
check, a model judge, an agent judge and an annotation queue. This spec
fixes how each one runs, what it reads, the exact shape of what it must
answer, what happens when the answer is wrong or missing, and what it
costs. It also fixes the decline judge that produces `declined`,
how re-grading appends generations, and how a judge's measurements are
calibrated against people's, so a report can say when a judge is not
yet to be trusted.

Every measurement is appended, never changed (spec 002). A grader that
fails to answer produces no measurement and a record of why; it never
produces a guessed value.

## Design

### The four kinds

| Kind | Runs | When | Reads | Costs |
|---|---|---|---|---|
| `check` | the task's command, in the trial's machine, run by the executor (spec 005) | after the agent ends, before the machine is released | the machine's files, the trial's transcript and final message | machine time; no model call of Examen's |
| `model_judge` | one structured call through Lux, made by examend on a Key from the key source | after the trial is `done` | the trial's evidence through its references, at most `JUDGE_MAX_INPUT_BYTES` | one Lux call on the experiment's Budget, per attempt |
| `agent_judge` | a session of a pinned Topos agent version, through the `topos` executor | after the trial is `done` | the trial's evidence, read-only | a session on the experiment's Budget |
| `annotation` | a person, through the API | when the trial is queued for them | the trial's evidence, through Examen | none on the Budget |

A trial is graded by its task's checks, the suite's `graders`, and,
when the suite lists `declined`, the suite's decline judge. A trial that
is not finished, one that ended `infra_failed`, `over_budget` or
`canceled`, is not graded. A cell is `grading` while
every trial is `done` and some check run or judgment is not yet ended,
and `complete` when every one has ended. Annotations never hold a cell:
a person's answer may come days later, as a generation.

### Checks

**Where.** The executor runs each check (spec 005); examend never runs
check code. The executor reports one check run per check, and examend
turns it into measurements under the contract below.

**Environment.** A check runs as the sandbox's user, under the sandbox's
network policy, in the order the task lists, each with:

| Variable | Value |
|---|---|
| `EXAMEN_TRIAL` | the trial's id |
| `EXAMEN_TASK` | the task's digest |
| `EXAMEN_CHECK` | this check grader's digest |
| `EXAMEN_WORKSPACE` | the absolute path of the working directory the agent left |
| `EXAMEN_TRANSCRIPT` | the path of the trial's transcript as JSONL in the machine: the session's log for `topos`, the forwarded request and response bodies for `cella` (spec 005) |
| `EXAMEN_FINAL_MESSAGE` | the path of a file holding the text of the agent's last message |
| `EXAMEN_CHECK_FILES` | the read-only directory holding the grader's `files`, when it names them |
| the grader's `env` | as declared; a key beginning `EXAMEN_` is refused at apply |

The trial's model key is not in a check's environment, the forwarder
that marks the trial's requests is stopped, and every process the agent
started is ended before checks begin (spec 005).

**What a check may trust.** The agent worked in the same machine, so a
check trusts no file the agent could have changed. A check's own files
come from the environment image at a path the sandbox's user cannot
write, or from the grader's `files`, a pinned Arca folder and its digest
(spec 002), which the executor delivers read-only at
`EXAMEN_CHECK_FILES`: after the agent ends on `topos` (spec 005,
requirement 8), so the agent never sees them; on `cella`, through a
read link whose token is in the replica from the start, so the agent
can read them but not change them.

The working directory is the grader's `workdir`,
resolved against `EXAMEN_WORKSPACE`; a `workdir` that is absolute or
leaves the workspace is refused at apply, and an absent one is the
workspace itself. Checks share the machine, so a check sees what an
earlier one left; a check that must not see that copies the workspace
first, and the task's author orders them.

**The output contract.** A check answers in one of two forms:

1. **The JSON form.** The check exits 0 and its stdout is exactly one
   JSON object, white space around it allowed, with one member,
   `metrics`, whose members are every metric the grader `produces`, by
   name, each with a value typed by its metric or `null` for no value:

   ```json
   {"metrics": {"tests_pass": true, "coverage": 0.83, "failures": 2}}
   ```

2. **The exit-status form.** The grader produces exactly one `binary`
   metric, and the check's stdout is empty or white space: exit 0 is
   `true` and exit 1 is `false`. A checker that passes on exit 0, such
   as Topos spec 025's `check.sh`, moves over unchanged this way.

**The task's failure is a value; the check's failure is `error`.** A
check that ran to its end reports what it found, however bad: tests
that fail are `false`, a count of failures, a score of 0. That is the
task failing, a measurement like any other. A check that could not do
its job, because its own build broke, it crashed, or it could not read
what it needed, fails itself: it exits with any status but 0, or with 1
and something on stdout, and the run is `error`, which leaves the
metric without a value for that trial (below). In the exit-status form
exit 1 is the task's failure and any other exit the check's. A check
author keeps the two apart, because analysis reads a value and never
reads a missing one as `false`.

| Metric type | JSON value | Rule |
|---|---|---|
| `binary` | `true` or `false` | |
| `score` | a number | within `range`, both ends included |
| `count` | an integer | at least 0, no fraction |
| `duration` | a number | seconds, at least 0 |
| `cost` | a number | in the metric's `unit`, at least 0 |
| `label` | a string | one of `labels` |

**Check run states.** Each run ends in one state:

| The run | State | Measurements |
|---|---|---|
| exit 0, stdout in the JSON form | `ok` | one per metric whose value is not `null` |
| exit 0 or 1, empty stdout, one `binary` metric | `ok` | one |
| exit 0, and stdout is not one object, lacks a produced metric, names one the grader does not produce, has a member besides `metrics`, has a value of the wrong type, out of range or not among the labels, or is longer than `CHECK_MAX_OUTPUT_BYTES` | `malformed` | none |
| exit 1 with stdout; any other exit; ended by a signal | `error` | none |
| the command could not start: not found or not executable | `not_runnable` | none |
| killed when `CHECK_TIMEOUT` ran out | `timed_out` | none |
| not started because `CHECK_TIMEOUT` had run out | `not_run` | none |

A run yields all its measurements or none: a check that names one
value wrongly yields nothing, so it cannot report a convenient subset.
The run's state, exit code and a developer's reason (such as
`metrics.coverage: 1.4 is outside [0, 1]`) are recorded in a check run
record of the trial's `checks` (spec 002), with a reference to its
whole stdout and stderr in evidence. A smoke trial with any run not
`ok` blocks its cell (spec 005). After smoke, a run that is not `ok`
leaves its metrics without a value for that trial, and spec 008 counts
missing values per cell, never reads one as `false`, and names them on
every result they touch.

`CHECK_TIMEOUT` covers all of a trial's checks together: each check
starts with what the earlier ones left of it, and is killed when it
runs out. Checks run after every end of the agent's work but an
infrastructure failure, a spend refusal or a cancel, so the work a
timed-out agent left is graded. A check run's state never changes the
trial's outcome, which is the agent's; a check that cannot start on
every trial blocks the cell at its smoke trial instead.

### Model judges

**The Key.** For each model judge of an experiment, examend leases one
Key from the key source with the purpose `judge` (spec 004): `models`
exactly the grader's `model`, and the labels `examen.experiment` and
`examen.grader` (the grader's id); the `lux` source names the
experiment's Budget and allows no unpriced model. Which providers may
keep what a judge sends is the key source's to decide, not the
grader's: an installation's key endpoint may hold a judge's Key to the
owner's own data terms. The Key is released when the experiment is
`complete` or `canceled`.

**The call.** One judgment is one call, through `pkg/llmdialect` to
Lux's `lux` door, with the grader's `temperature`, at most
`JUDGE_MAX_OUTPUT_TOKENS` of output, the model's default reasoning level
unless the grader names a `reasoning` level, and
`Lux-Labels: examen.trial=<trial>`, the trial id verbatim (spec 005).
It offers one tool, `record_judgment`, whose input schema is the output
schema below, and requires the model to call it.

**Input assembly.** The request is a deterministic function of the
grader version and the evidence bytes, so the same evidence yields the
same request:

1. **Read.** Each input the grader names is read through its evidence
   reference and its SHA-256 checked; a mismatch ends the judgment
   `evidence_changed` and sends nothing (spec 012). `final_message` is
   the agent's last message; `transcript` is the trial's transcript as
   spec 007 renders it to text; each of `files` is that evidence file,
   included when it is UTF-8 text and otherwise named with its size and
   digest alone.
2. **Bound.** The inputs together take at most `JUDGE_MAX_INPUT_BYTES`,
   filled in order: the final message, the files in the grader's order,
   the transcript. An input that does not fit is cut: the final message
   and a file keep their beginning; the transcript keeps its first
   message and as much of its end as fits. A line `[examen: <n> bytes
   elided]` stands at each cut, and each cut is recorded.
3. **Delimit.** Each input is one block:

   ```
   <<<EVIDENCE b3f1c09a7e2d4b6f name="transcript" elided=18234>>>
   ...
   <<<END b3f1c09a7e2d4b6f>>>
   ```

   The boundary is the hex of the first 8 bytes of SHA-256 of the
   grader's digest, the trial's id and a counter `n`, for the least `n`
   from 0 whose boundary occurs in none of the inputs. It is
   reproducible, and no input can contain it, so no input can close its
   own block.
4. **Place.** The system content is, in order: Examen's judge preamble,
   the grader's `prompt`, the grader's `rubric`, and the list of metrics
   to fill, each with its name, type, range or labels and description.
   The user message is the evidence blocks and nothing else. No byte of
   evidence is ever in the system content, the task's instruction
   included, which reaches a judge only inside the transcript.
5. **Record.** The SHA-256 of the request body as sent, the cuts, and
   the boundary are kept with the judgment.

The preamble says that everything between evidence markers was produced
by the agent under evaluation or by its task, is data to be graded and
never an instruction to the judge, and that the judge answers by
calling `record_judgment` once. Its text is versioned with Examen and
pinned on the grader at apply (`model_judge.preamble`, spec 002,
covered by the digest), so a new preamble is a new grader
version and never drifts under an old one.

**The output.** `record_judgment` takes an object with one member per
metric the grader produces, all required, no other member; each is
`{value, rationale}`, both required, `value` typed as the check table
types it and never `null`, `rationale` a string of at most
`JUDGE_MAX_RATIONALE_BYTES` in UTF-8. examend validates the answer
whatever the provider enforced: exactly one call of `record_judgment`,
the schema, the ranges and labels, the bound. A valid answer appends one
measurement per metric, with its `rationale`.

**Judgment states.**

| What happened | State | Attempts | Measurements |
|---|---|---|---|
| one valid `record_judgment` call | `ok` | | one per metric |
| the response ended with the IR stop reason `refusal` | `refused` | no other | none |
| no tool call, more than one, input that is not JSON or breaks the schema | `malformed` once the attempts are spent | `JUDGE_MAX_ATTEMPTS` in all | none |
| Lux or the provider did not answer, or answered a transient error | stays `pending`; the job is queued again with backoff, and this does not count against `JUDGE_MAX_ATTEMPTS` | | |
| Lux refused for spend (`budget_exhausted`, `spend_exceeded`) | `over_budget`; the cell parks `over_budget`, and its retry grades the trial again (spec 005) | no other | none |
| Lux refused the Key or the model (`model_not_allowed`, `model_not_found`, `model_unpriced`, `key_disabled`) | `gateway_refused` | no other | none |
| an evidence digest did not match | `evidence_changed` | none sent | none |

A judgment record keeps each attempt's Lux request id and state, the
request digest, the cuts, the model that served from Lux's record, the
tokens and the cost: one entry of the trial's `judgments` (spec 002),
which spec 007 stores beside the measurements. Every attempt's cost is the trial's
grading cost, which the smoke estimate reads (spec 005). Reports count
each state per cell and grader, so a judge that refuses often is seen.

### Agent judges

An agent judge is a session of the pinned Topos agent version
`agent_judge.agent`, given read-only access to one trial's evidence.

- **It runs as a rollout** of the `topos` executor (spec 005): one
  rollout per cell, judge version and generation, keyed `<cell
  id>.judge.<grader id>.<generation>`, one session per trial, its
  sessions counted against the experiment's parallelism.
- **The agent version pins its own model and reasoning**; no override is
  sent, and the model that served is recorded from Lux.
- **Its first message** is Examen's agent judge preamble (pinned as
  `agent_judge.preamble`, as the model judge's is), the grader's
  `instructions`, the metrics to fill,
  and the path of the output file.
- **Its inputs** are the evidence the grader's `inputs` name, delivered
  read-only outside its working directory. Its machine's network is
  `none` and its tools are narrowed to those of `read`, `grep`, `glob`
  and `bash` its agent holds (requirements 4 and 5 of spec 005), with no
  connection, so hostile evidence can
  neither change what is graded nor make the judge send it anywhere.
- **Its answer** is `judgment.json` in its working directory: the same
  object `record_judgment` takes, written to evidence by requirement 9
  and validated as a model judge's answer. A missing or invalid file is
  `malformed`, with no second attempt, since a session is dear; a last
  message that ended in a refusal is `refused`. A session past
  `AGENT_JUDGE_TIMEOUT` is `timed_out`, with no measurement. An
  infrastructure failure is retried as a trial's is, up to
  `MAX_INFRA_RETRIES`.
- **What it does inside is its own.** The agent may spawn critic threads
  as Topos spec 031 sketches, or run Agon's adversarial review; Examen
  reads only the answer, and that variance is part of what the agent
  version pins.

Beyond spec 005's requirements, an agent judge needs one thing from
Topos: per-session read-only inputs from an Arca folder, delivered
outside the working directory with their digests on the record, as
Topos spec 075 (drafted) designs for a message's attachments, without an
attachment's size and count limits.

### Annotations

An annotation grader is a queue of trials for people.

- **Filling.** When a trial is `done` and graded by an annotation
  grader, it is queued when the first 8 bytes of SHA-256 of the
  grader's digest and the trial's id, read as an unsigned integer and
  divided by 2^64, are below the grader's `sample`. The draw is fixed
  by the two ids, so the same trials are chosen whenever the same
  grader asks, whatever order trials end in. The owner may also queue
  chosen trials (`POST /annotations/queue`, spec 003), for instance
  trials a judge graded, to calibrate it.
- **Claiming.** An annotator claims the oldest open item for
  `ANNOTATION_CLAIM_TTL`; a claim not answered by then returns the item
  to open. An annotator may skip an item with a reason, which releases
  the claim and leaves the item open to others.
- **What a person sees** is the grader's `instructions` and the
  evidence its inputs name, through Examen. A person never sees the
  candidate's name or kind, another grader's measurements, or another
  person's answer, so an annotation is an independent label.
- **The answer** is one `{value, rationale}` per metric the grader
  produces, typed and validated as a judge's; `rationale` is the
  person's note. Each measurement records its annotator as
  `human:<subject>`, the subject rendered as spec 004 renders it, in the
  measurement's `annotator` (spec 002).
- **Generations** count per trial, grader version and annotator: a
  person's second answer appends their next generation, and their
  current value is their latest. Several people's answers stand side by
  side; calibration below reads their consensus.
- **Who may answer** is the authorizer's decision (spec 004).

### The decline judge

`declined` means the agent refused the task, said it would not or could
not do it, or stopped without attempting it. It is false for an agent
that attempted the task, however badly, and for one a limit stopped
while attempting.

`declined` is produced by the suite's decline judge alone, as a
measurement under the judge's grader digest, for every finished trial
when the suite lists `declined`. A trial's outcome is set by its
executor and is final (spec 005): the decline judge reads it and never
changes it.

- **The built-in decline judge**, `builtin:decline`, is the decline
  judge of a suite whose `decline_judge` is absent. It calls no model:
  it records `declined` true exactly when the outcome is `declined`, a
  provider's refusal (spec 005), and false for every other outcome.
- **A configured decline judge.** Examen ships a decline prompt and
  rubric, versioned with Examen. A suite configures its decline judge
  by naming in `decline_judge` a grader of kind `model_judge` that
  produces only `declined` and reads `final_message` and `transcript`,
  usually Examen's prompt and rubric with a judge model the owner
  chooses. The owner chooses the model because the judge sends the
  owner's transcripts to that model's provider, and Examen ships no
  default judge model (spec 012). It calls its model for a trial whose
  outcome is `completed`; it records true without a call for the
  outcome `declined`, and false without a call for `timed_out` and
  `turn_limit`, each a limit that stopped an agent while attempting.
- **Changing the decline judge** makes a new suite version, and the
  cells' grader digests differ, so a comparison across the change
  carries the confound (spec 008).

### Generations and re-grading

- **Numbering.** Generations count per trial and grader version, and for
  annotations per annotator too, from 1. A re-grade appends the next;
  nothing is overwritten (spec 002).
- **The current value** is the latest generation of the grader version
  the suite names; for an annotation grader, each annotator's latest.
- **A re-grade** (spec 003) names a grader version and the trials, by
  experiment, cell or trial, and carries a key: two requests with one
  key append once. Re-grading a judge runs it again, which measures the
  judge's own run-to-run spread when its temperature is above 0, or
  fills judgments an outage left `pending`.
- **A changed judge** (model, prompt, rubric, inputs, temperature,
  preamble, `reasoning`) is a new grader version. Its
  first grading of a trial is generation 1 under its own digest, and a
  comparison across two versions is a confound spec 008 computes from
  the tuple's grader digests.
- **A check is re-graded by parsing its stored stdout again,** digest
  checked, since its machine is gone. That is allowed only for a check
  grader version whose `command`, `workdir` and `env` equal the version
  that ran.
- **Evidence that changed** ends a re-grade's judgment
  `evidence_changed`, and nothing is appended.

### Calibration

A judge is calibrated per metric against people's annotations of the
same trials.

- **Pairs.** For a judge version J (a model judge, an agent judge or the
  decline judge), a metric m it produces and one owner, the pairs are
  the owner's trials holding a current measurement of m from J and at
  least one current annotation of m. Pairs pool across the owner's
  experiments, since agreement is a property of the judge version on the
  owner's work; the figure for one experiment is readable too.
- **The people's value** of a trial is its one annotator's value, or
  with several their consensus: the majority for `binary` and `label`,
  a tie dropping the trial, and the median for the other types.
- **The statistic** is Krippendorff's alpha between J and the people's
  value, with the nominal distance for `binary` and `label` and the
  interval distance for the other types, and its interval at
  `CONFIDENCE_LEVEL` by bootstrap over trials. Over trials with two or
  more annotators, the people's own alpha is reported beside it, as the
  ceiling a judge cannot be expected to pass.
- **Uncalibrated.** J is uncalibrated for m when it has fewer than
  `CALIBRATION_MIN_PAIRS` pairs or its alpha is below
  `CALIBRATION_MIN_AGREEMENT`. The flag is derived, recomputed whenever
  a measurement of m from J or an annotation of m arrives, and never
  stored on a measurement. `GET /graders/{ref}/calibration` reads the
  flag, the alpha, its interval and the pair count (spec 003), and a
  console's and a report's calibration column shows them beside every
  result of m from J, once that column is built (spec 010).
- **A new version starts uncalibrated**: agreement of one judge version
  says nothing of another.

### Limits

The constants of spec 002 this spec holds: `CHECK_TIMEOUT`,
`CHECK_MAX_OUTPUT_BYTES`, `JUDGE_MAX_INPUT_BYTES`,
`JUDGE_MAX_OUTPUT_TOKENS`, `JUDGE_MAX_ATTEMPTS`,
`JUDGE_MAX_RATIONALE_BYTES`, `AGENT_JUDGE_TIMEOUT`,
`ANNOTATION_CLAIM_TTL`, `CALIBRATION_MIN_PAIRS`,
`CALIBRATION_MIN_AGREEMENT`, `CONFIDENCE_LEVEL` and
`MAX_INFRA_RETRIES`.

## Not in this spec

Running a check and writing evidence (spec 005); the routes that queue,
claim, answer, re-grade and read calibration (spec 003); who may
annotate and acting as the owner toward Lux and Topos (spec 004); the
evidence reference, the transcript's text rendering, the storage of
judgment and check run records (spec 007); how missing values and
grader confounds enter a verdict (spec 008); how views and reports show
calibration (spec 010); the threats to judges (spec 012).

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Each row of the check run state table yields its state and measurements, over recorded check runs; a check that reports failing tests yields a `false` value, and one that crashed yields `error` and no value | `check.TestRunStates`, `check.TestTaskFailureIsAValue` |
| A JSON form output with a missing, unknown or mistyped metric, an out-of-range score, an unknown label, an extra member or more than `CHECK_MAX_OUTPUT_BYTES` yields no measurement at all and a `malformed` record naming the field | `check.TestMalformedYieldsNothing`, `check.FuzzParse` |
| The exit-status form maps exit 0 to `true` and exit 1 to `false` for a grader of one `binary` metric, and is `error` for any other grader | `check.TestExitStatusForm` |
| A check's environment carries the `EXAMEN_` variables and the grader's `env`, and never the trial's model key, on both executors | `check.TestEnvironment`, e2e tier |
| A grader `env` key beginning `EXAMEN_`, an absolute `workdir` and one leaving the workspace are refused at apply | `check.TestApplyRefusals` |
| Input assembly is deterministic: the same evidence gives a byte-identical request, and the recorded digest matches it | `judge.TestAssemblyIsDeterministic` |
| Inputs past `JUDGE_MAX_INPUT_BYTES` are cut in the order and at the ends given, each cut marked and recorded | `judge.TestInputBound` |
| No byte of evidence or of the task's instruction appears in the system content, and the boundary occurs in no input | `judge.TestEvidenceStaysInTheUserTurn`, `judge.FuzzBoundary` |
| An answer with two tool calls, a missing metric, a `null` value, an out-of-range value or a rationale past `JUDGE_MAX_RATIONALE_BYTES` is retried up to `JUDGE_MAX_ATTEMPTS` and then `malformed`, with no measurement | `judge.TestMalformedAnswers`, against the stub Lux |
| A refusal is `refused` with no second call; a spend refusal is `over_budget` and parks the cell; a refused model is `gateway_refused`; a transient failure leaves the judgment `pending` | `judge.TestJudgmentStates` |
| A judge's Key is leased from the key source with the purpose `judge`, names only the grader's model, carries the labels, and is released when the experiment ends; from the `lux` source it draws on the experiment's Budget and allows no unpriced model | `judge.TestJudgeKey` |
| An evidence object whose digest changed ends the judgment `evidence_changed` and nothing is sent | `judge.TestEvidenceChanged` |
| An agent judge session gets read-only inputs, network `none` and the narrowed tools, and its `judgment.json` is validated like a model judge's answer | `judge.TestAgentJudge`, against the stub Topos |
| The annotation draw is a function of the grader and the trial alone, selecting close to `sample` of trials over a large set; an unanswered claim returns after `ANNOTATION_CLAIM_TTL` | `annotate.TestSampling`, `annotate.TestClaims` |
| An annotator never receives the candidate, another grader's measurements, or another person's answer; each answer records `human:<subject>` and appends that annotator's next generation | `annotate.TestBlindAnswers` |
| `declined` is produced by the decline judge alone: `builtin:decline` from the outcome with no model call, or the suite's `decline_judge`, which calls its model for a `completed` trial only; no grading changes a trial's outcome | `grade.TestDeclined` |
| A re-grade appends the next generation and leaves earlier ones unchanged; one key appends once; a changed judge starts at generation 1 under its own digest | `grade.TestRegrade` |
| A check re-grade parses the stored stdout again and is refused for a version whose command differs | `check.TestRegradeReparses` |
| Krippendorff's alpha matches reference values for nominal and interval data; a judge with fewer than `CALIBRATION_MIN_PAIRS` pairs or alpha below `CALIBRATION_MIN_AGREEMENT` is reported uncalibrated with its alpha, interval and count | `calibrate.TestAlpha`, `calibrate.TestUncalibrated` |
