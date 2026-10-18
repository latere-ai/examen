---
title: "Grading: the check contract, the built-in comparators, model judges, candidate-level judges, agent judges, annotation queues, generations and re-grading, the decline judge, open vocabularies and their taxonomies with the canonicalizer, and calibrating judges against people"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
affects: [internal/grade/, internal/grade/check/, internal/grade/judge/, internal/grade/annotate/, internal/calibrate/, cmd/examen-compare/]
effort: large
created: 2026-10-17
updated: 2026-10-18
author: changkun
---

# Grading

## Overview

A grader turns a trial into measurements. Spec 002 names four kinds: a
check, a model judge, an agent judge and an annotation queue. This spec
fixes how each one runs, what it reads, the exact shape of what it must
answer, what happens when the answer is wrong or missing, and what it
costs. It fixes the built-in comparators, which score an output against
a task's reference through an ordinary check, with no model. It also
fixes the decline judge that produces `declined`, the judges that
grade a candidate over all its trials, how an open-vocabulary label is
read through a suite's versioned taxonomy, how re-grading appends
generations, and how a judge's measurements are calibrated against
people's, so a report can say when a judge is not yet to be trusted.
A cell-level metric is not graded: Examen computes it from the cell's
trial values (spec 008).

Every measurement is appended, never changed (spec 002). A grader that
fails to answer produces no measurement and a record of why; it never
produces a guessed value.

## Design

### The four kinds

| Kind | Runs | When | Reads | Costs |
|---|---|---|---|---|
| `check` | the task's command, with network `none`: in a `topos` trial's machine, run by the executor, or for a `cella` or `external` trial in the check sandbox (spec 005) | after the system under test ends: before a `topos` machine is released, or once a `cella` trial's outputs are exported or an external trial's submitted | the machine's or the check sandbox's files, the trial's outputs, transcript and final message, and the task's references | machine time; no model call of Examen's |
| `model_judge` | one structured call through Lux, made by examend on a Key from the key source | after the trial is `done`; at level `candidate`, once per candidate after its cells are graded | the trial's evidence through its references, or at level `candidate` every finished trial's of the candidate, at most `JUDGE_MAX_INPUT_BYTES` | one Lux call on the experiment's Budget, per attempt |
| `agent_judge` | a session of a pinned Topos agent version, through the `topos` executor | after the trial is `done` | the trial's evidence, read-only | a session on the experiment's Budget |
| `annotation` | a person, through the API | when the trial is queued for them | the trial's evidence, through Examen | none on the Budget |

A trial is graded by its task's checks, the suite's `graders`, and,
when the suite lists `declined`, the suite's decline judge. A metric
applies only to trials of the outcomes its `applies_to` names (spec
002): examend drops a value a check or a judge gives for a trial
outside them and records none; a check may answer `null` for it, which
the JSON form admits for any metric; a check in the exit-status form,
which cannot say "no value", is not run for a trial its one metric does
not apply to; and a judge none of whose metrics apply to a trial is not
called for it, so no money is spent on a value that would not be read. A trial that
is not finished, one that ended `infra_failed`, `over_budget` or
`canceled`, is not graded. A cell is `grading` while
every trial is `done` and some check run or judgment is not yet ended,
and `complete` when every one has ended. Annotations never hold a cell:
a person's answer may come days later, as a generation. A value an
external harness submitted itself is not a grading: it is stored under
`self:<harness>` with `self_reported` true (spec 007), is never
calibrated, and takes no part in this spec.

### Checks

**Where.** The `topos` executor runs each check of a `topos` trial in
the trial's machine, and Cella runs each check of a `cella` or
`external` trial in its check sandbox, through examend's exec calls
(spec 005); examend never runs check code. Each check yields one check
run, which examend turns into measurements under the contract below.

**Environment.** A check runs as the sandbox's user, with network
`none` for the whole check phase, in the order the task lists, each
with:

| Variable | Value |
|---|---|
| `EXAMEN_TRIAL` | the trial's id |
| `EXAMEN_TASK` | the task's digest |
| `EXAMEN_CHECK` | this check grader's digest |
| `EXAMEN_WORKSPACE` | the absolute path of the working directory the agent left; in the check sandbox, its empty working directory |
| `EXAMEN_TRANSCRIPT` | the path of the trial's transcript as JSONL in the machine: the session's log for `topos`, the forwarded request and response bodies for `cella`, and what the external harness submitted for `external`, an empty file when it submitted none (spec 005) |
| `EXAMEN_FINAL_MESSAGE` | the path of a file holding the text of the system under test's last message, empty when there is none |
| `EXAMEN_INPUTS` | the directory holding the task's inputs, each at its name: `/examen/inputs`, or `examen/inputs` of the working directory on `topos` |
| `EXAMEN_OUTPUTS` | the directory holding the system under test's outputs, `/examen/outputs` |
| `EXAMEN_OUTPUTS_MANIFEST` | in the check sandbox: the path of the trial's output manifest, which lists every output file with its size and digest and whether it was kept (spec 005) |
| `EXAMEN_REFERENCE` | the read-only directory holding the task's references, each at its name, `/examen/reference`; empty when the task has none |
| `EXAMEN_CHECK_FILES` | the read-only directory holding the grader's `files`, when it names them |
| `EXAMEN_CHECK_IMAGE` | the read-only mount of the grader's `image`, when it names one; `command[0]` is then a path inside that image, run from this mount |
| the grader's `env` | as declared; a key beginning `EXAMEN_` is refused at apply |

The trial's model key is not in a check's environment. On `topos`
every process the agent started is ended before checks begin; the
check sandbox holds no process of a system under test (spec 005).

**What a check may trust.** On `topos` the agent worked in the same
machine, so a check trusts no file the agent could have changed. A
check's own files come from the environment image at a path the
sandbox's user cannot write, or from the grader's `files`, a pinned
Arca folder and its digest (spec 002), delivered read-only at
`EXAMEN_CHECK_FILES`: on `topos` after the agent ends (spec 005,
requirement 8), and otherwise into the check sandbox alone, so no
system under test sees them. A task's references are delivered
read-only only after the system under test's processes have stopped on
`topos`, and only into the check sandbox for a `cella` or `external`
trial (spec 005); a grader's `image` is mounted read-only. In the check
sandbox no system under test ever ran: it holds only what examend wrote
into it.

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
| `label` | a string | one of `labels`; for an open-vocabulary metric, any string of 1 to `LABEL_MAX_BYTES` once normalized (Open vocabularies, below) |

**Check run states.** Each run ends in one state:

| The run | State | Measurements |
|---|---|---|
| exit 0, stdout in the JSON form | `ok` | one per metric whose value is not `null` |
| exit 0 or 1, empty stdout, one `binary` metric | `ok` | one |
| exit 0, and stdout is not one object, lacks a produced metric, names one the grader does not produce, has a member besides `metrics`, has a value of the wrong type, out of range or not among the labels, or is longer than `CHECK_MAX_OUTPUT_BYTES` | `malformed` | none |
| exit 1 with stdout; any other exit; ended by a signal | `error` | none |
| the command could not start: not found or not executable | `not_runnable` | none |
| killed when `CHECK_TIMEOUT` ran out | `timed_out` | none |
| not started because `CHECK_TIMEOUT` had run out, or, for a `cella` or `external` trial, because its outputs passed a limit or its archive was refused (spec 005), with the reason `outputs_over_limit` or `outputs_refused` | `not_run` | none |

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

### Built-in comparators

Scoring an output against a reference needs no model. Examen publishes a
first-party comparator image, pinned by digest, holding one static
program, `/examen-compare`, which a `check` grader runs as any check:
the grader names the image in `image`, and its `command` is
`["/examen-compare", "<comparator>", "<parameters>"]`, the parameters
one JSON object. The comparator reads the output under `EXAMEN_OUTPUTS`
and the reference under `EXAMEN_REFERENCE`, and prints the JSON form
above with the quantities its parameters' `metrics` map to the grader's
metric names. So a comparator produces the owner's declared metrics like
any check, runs where checks run (the trial's machine, or the check
sandbox), and costs nothing on the Budget. Its image digest is part of
the grader version, so a new comparator release is a new grader version.
A comparator is deterministic: the same output and reference give the
same values.

```yaml
name: invoice-fields
kind: check
produces: [sha256:..., sha256:...]       # field_f1, total_f1
check:
  image: sha256:...                       # Examen's comparator image
  command:
    - /examen-compare
    - json_fields
    - '{"output": "fields.json", "reference": "fields",
        "fields": [{"name": "total", "path": "$.total"},
                   {"name": "items", "path": "$.line_items[*].sku"}],
        "tolerance": 0.01,
        "metrics": {"f1": "field_f1", "total_f1": "total_f1"}}'
```

| Parameter | Comparators | Meaning |
|---|---|---|
| `output` | all | the output's path under `/examen/outputs/`, one of the task's `outputs` |
| `reference` | all | the name of one of the task's references |
| `metrics` | all | an object from each quantity the comparator emits to the name of a metric the grader produces; it names every metric the grader produces, and the comparator prints only those |
| `normalize` | `json_fields`, `set`, `text` | `{case, whitespace}`, both true by default: before strings are compared, fold them to lower case, and trim and collapse each run of white space to one space |
| `fields` | `json_fields` | each `{name, path}`: the field's name, `[a-z][a-z0-9_]*`, used in its quantities, and a path selector |
| `tolerance` | `json_fields` | the absolute difference within which two numbers match; 0 by default |
| `path` | `numeric`, `set` | optional: a path selector into a JSON file; absent, `numeric` reads the whole file as one number and `set` reads its non-empty lines |
| `absolute_tolerance`, `relative_tolerance` | `numeric` | two numbers match when their difference is at most the larger of `absolute_tolerance` and `relative_tolerance` times the reference's magnitude; both 0 by default |

| Comparator | Compares | Quantities it emits |
|---|---|---|
| `exact` | the output's bytes with the reference's | `match`, binary: true when they are equal |
| `json_fields` | per field, the values its selector yields from the output (predicted) and from the reference (expected), as multisets matched one to one: two strings match when equal after normalization, two numbers when within `tolerance`, any other values when equal as JSON | `precision`, `recall` and `f1` over every field together, from the sums of matched, predicted and expected values; and `<name>_precision`, `<name>_recall` and `<name>_f1` per field; each a score in [0, 1] |
| `numeric` | the number at `path` in each | `match`, binary: true when within tolerance; `absolute_error`: the absolute difference, a number at least 0 |
| `set` | the values at `path`, or the lines, as sets after normalization | `precision`, `recall` and `f1`, each a score in [0, 1] |
| `text` | the two files as text after normalization | `exact`, binary: true when equal; `token_f1`: the F1 of the two multisets of tokens split on white space, a score in [0, 1] |

Precision is matched over predicted and recall is matched over
expected. With nothing predicted, precision is 1 when nothing was
expected and 0 otherwise; with nothing expected, recall is 1 when
nothing was predicted and 0 otherwise. F1 is `2PR / (P + R)`, and 0
when both are 0. Each quantity's type fits the metric it maps to: a
binary quantity a `binary` metric, a score a `score` metric of range
[0, 1], and `absolute_error` a `score` metric whose range holds every
error expected, since a value outside it makes the run `malformed`.

**A path selector** is `$` for the root, then any sequence of
`.<member>` for an object's member, `['<member>']` for a member whose
name holds `.`, `[` or `'`, `[<n>]` for an array's element counted from
0, and `[*]` for every element, such as `$.line_items[*].sku`. A
selector that finds nothing yields no values. The HTTP runner reads the
same selectors (spec 005).

**The task's failure is a value here too.** An output that is missing,
or that does not parse where JSON is read, is the system under test's
failure: each quantity is computed with nothing predicted, so `match`
and `exact` are false and `absolute_error` is `null`. A reference that
is missing or does not parse, parameters that do not parse, an unknown
comparator, or a `metrics` key naming a quantity the comparator does
not emit is the check's own failure: the comparator prints nothing and
exits 2, and the run is `error`.

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
   digest alone; a file is UTF-8 text when its bytes are valid UTF-8 and
   hold no NUL byte. Each of `outputs` is that output of the trial, read
   the same way. A directory output, kept as one archive with the output
   manifest (spec 005), is read from the archive, each file extracted
   and its digest checked against the manifest, a mismatch ending the
   judgment `evidence_changed` as above:
   - **One file of it**, named `<output>/<path>`, is that file, read as a
     file is.
   - **The whole output**, named `<output>/` alone, is every file the
     manifest lists, in ascending bytewise order of path: each kept file
     that is UTF-8 text is a block of its own, named `<output>/<path>`,
     and every other file, one that is not UTF-8 text or that was kept by
     digest alone (spec 005), is a line `<path> <size> <digest> <why>` of
     one listing block named `<output>/` after the files, `why` being
     `not_text` or `not_kept`. So a judge that reads what an agent built
     reads its source and its notes, and is told what else is there.

   Examen reads the archive as data and runs nothing from it. For an external trial, `transcript` and `final_message`
   are what its external harness submitted, and one it did not submit is an
   empty block.
2. **Bound.** The inputs together take at most `JUDGE_MAX_INPUT_BYTES`,
   filled in order: the final message, the files and then the outputs in
   the grader's order, a whole directory output's file blocks in their
   path order and then its listing, the transcript. An input that does
   not fit is cut: the final message, a file, an output, a directory
   output's file and its listing keep their beginning; the transcript
   keeps its first message and as much of its end as fits. Once the
   bound is reached, a whole directory output's files not yet read are
   not read: each joins its listing as a line with the `why`
   `over_bound`, and the listing, cut like any input, keeps as many of
   them as fit. A line `[examen: <n> bytes elided]` stands at each cut, and
   each cut is recorded.
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

### Candidate-level judges

A model judge of level `candidate` judges a candidate's behavior over
all its trials, such as a behavioral profile, and produces only
candidate-level metrics: one value per candidate of an experiment.

- **When.** Once per candidate, when every cell of the candidate has
  left `grading`, complete or parked, over the candidate's finished
  trials then. The experiment completes only after it ends (spec 002).
- **Input assembly** is a model judge's, with one block per input per
  trial, the trials in ascending id order and each block's `name`
  carrying the trial's id and task. `JUDGE_MAX_INPUT_BYTES` bounds the
  whole call: each trial is given an equal share of it, filled in the
  grader's order and cut at the ends a trial-level judge cuts, and a
  share a trial leaves unused passes to the trials after it. Every cut
  is recorded.
- **Its answer** is a `record_judgment` call as above, one value and
  rationale per metric it produces, validated the same way, with the
  same states and attempts, but for `over_budget`, which parks no cell:
  the grading ends `over_budget`, and a re-grade runs it again once the
  Budget is refilled. It appends one measurement per metric, naming the
  experiment and the candidate and no trial (spec 007).
- **Its Key** is leased as a trial-level judge's, with the purpose
  `judge`. Its judgment record is kept in the experiment's `judgments`
  with `candidate` set (spec 002), and its rationale in evidence (spec
  007).
- **Generations** count per experiment, candidate and grader version. A
  re-grade with a candidate-level grader (spec 003) appends each named
  candidate's next generation.

A candidate-level value is one figure per candidate, with no trials to
spread across, so it is descriptive: spec 008 reports it beside the
other side's and tests nothing.

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
  provider's refusal or, for an external trial, what its external harness
  submitted (spec 005), and false for every other outcome.
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

### Open vocabularies and taxonomies

A `label` metric of open vocabulary declares no labels (spec 002). A
check or a judge answers a raw label per trial, any string of 1 to
`LABEL_MAX_BYTES` once normalized: in Unicode NFC, trimmed, each run of
white space one space, and folded to lower case. The normalized string
is the measurement's value. A measurement is never rewritten: what a
raw label means is read through the suite's taxonomy.

**The taxonomy.** Each suite, by owner and name, keeps one taxonomy per
open-vocabulary metric it lists: a lineage of records (spec 002), each
an ordered list of canonical labels and a map from raw labels to them.
Results, summaries and reports read an open-vocabulary metric as the
canonical label its raw label maps to at one named record, and name
that record (spec 008). A trial whose raw label the record does not map
has no canonical value at that record: its normalized raw label counts
as a label of its own, beside the canonical labels and marked unmapped,
and the summary's `unmapped` counts it (spec 008). It is never guessed
into a canonical label and never read as missing, since a label nobody
named yet is still the judge's answer.

**Changing it.** A change to the mapping is never a rewrite. It appends
the lineage's next record, which maps every raw label the one before it
mapped, so a raw label never falls back to unmapped. Two things append
records:

- **The owner**, through the taxonomy route (spec 003), with any
  mapping, a remapping included, and canonical labels added, renamed or
  reordered.
- **A canonicalizer**, a model judge of role `canonicalize` the suite
  names for the metric in `canonicalizers` (spec 002), pinned like any
  judge. It runs once per experiment of the suite's own owner, after
  every cell has left `grading` and before the experiment completes,
  when the experiment's trials hold raw labels the current record does
  not map, and again after a re-grade of the metric's producer. It is
  one structured call through Lux on a Key leased with the purpose
  `judge`, whose system content is Examen's canonicalizer preamble, the
  grader's `prompt` and `rubric`, and the current record's canonical
  labels, and whose user message is one evidence block listing each
  unmapped raw label, with its count, of the experiment's trials and of
  the trials every reference side of its comparisons reads, such as a
  baseline's, so a raw label is mapped for both sides of a comparison
  or for neither. It offers one tool, `record_taxonomy`, taking
  `{canonical, map}`: the canonical labels, which keep every current
  one in its order and may add new ones up to
  `MAX_LABELS_OPEN_VOCABULARY`, and a map of some of the listed raw
  labels to them. examend validates the answer as a judge's, with
  the same states and attempts as a candidate-level judge's, and
  appends a record of the current map
  with the new entries, `source` naming the canonicalizer, the
  experiment and the run's generation; it never remaps a raw label, and
  one it leaves out stays unmapped. Its judgment record is kept in the
  experiment's `judgments` with `metric` set. An experiment of an owner
  a suite is shared with reads the suite's taxonomy and never runs its
  canonicalizer.

**A new record is a new generation of reading.** A provisional
comparison moves to the current record when it is recomputed. A final
comparison keeps the record it read, and a record appended after it
makes a new comparison of the same pair, as a later grading does (spec
008); the earlier stays. A bar reads comparisons at the record it pinned
when it was applied (spec 009). A taxonomy record is never deleted, so
every past comparison can be reproduced.

### Generations and re-grading

- **Numbering.** Generations count per trial and grader version, and for
  annotations per annotator too, from 1; for a candidate-level judge
  per experiment, candidate and grader version, and for a canonicalizer
  per experiment and metric. A re-grade appends the next; nothing is
  overwritten (spec 002).
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
  result of m from J, once that column is built (spec 010). A result of
  a label metric m, or of a cell-level metric of m, read while J is
  uncalibrated for m carries the confound `uncalibrated` and is never
  headlined (spec 008); until annotations are built, no judge has a
  pair, so every judge is uncalibrated.
- **A new version starts uncalibrated**: agreement of one judge version
  says nothing of another.
- **Not calibrated:** a candidate-level judge, since people annotate
  trials, not candidates, and a canonicalizer, whose records an owner
  reads and edits directly.

### Limits

The constants of spec 002 this spec holds: `CHECK_TIMEOUT`,
`CHECK_MAX_OUTPUT_BYTES`, `JUDGE_MAX_INPUT_BYTES`,
`JUDGE_MAX_OUTPUT_TOKENS`, `JUDGE_MAX_ATTEMPTS`,
`JUDGE_MAX_RATIONALE_BYTES`, `AGENT_JUDGE_TIMEOUT`,
`ANNOTATION_CLAIM_TTL`, `CALIBRATION_MIN_PAIRS`,
`CALIBRATION_MIN_AGREEMENT`, `CONFIDENCE_LEVEL`, `MAX_INFRA_RETRIES`,
`LABEL_MAX_BYTES`, `MAX_LABELS_PER_METRIC`,
`MAX_LABELS_OPEN_VOCABULARY` and `MAX_TAXONOMY_RAW_LABELS`.

## Not in this spec

Running a check, the check sandbox, delivering references and writing
evidence (spec 005); the routes that queue,
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
| A check's environment carries `EXAMEN_INPUTS`, `EXAMEN_OUTPUTS`, `EXAMEN_REFERENCE` and, for a grader with an `image`, `EXAMEN_CHECK_IMAGE`, on every executor and in the check sandbox | `check.TestEnvironment`, e2e tier |
| Each comparator emits exactly the quantities its parameters map, with the values of committed fixtures: `exact` on equal and unequal bytes, `json_fields` per field and over all fields with normalization and tolerance, `numeric` at both tolerances, `set`, and `text` with token F1; the empty-side rules hold; a missing output yields values and a missing reference exits 2 | `compare.TestComparators`, `compare.TestEmptySides`, `compare.TestFailures`, in `cmd/examen-compare` |
| A path selector finds exactly the values its fixtures list, quoted members and `[*]` included | `compare.TestSelectors` |
| Input assembly is deterministic: the same evidence gives a byte-identical request, and the recorded digest matches it | `judge.TestAssemblyIsDeterministic` |
| Inputs past `JUDGE_MAX_INPUT_BYTES` are cut in the order and at the ends given, each cut marked and recorded | `judge.TestInputBound` |
| A directory output named alone is read as its kept UTF-8 text files in ascending bytewise order of path, each a block named `<output>/<path>` whose digest is checked against the manifest, then a listing of every other file with its size, digest and why; a file with a NUL byte or invalid UTF-8 is listed `not_text`, one kept by digest `not_kept`; past `JUDGE_MAX_INPUT_BYTES` the files not yet read are listed `over_bound`; one file named `<output>/<path>` is read alone; the same archive gives the same request bytes | `judge.TestDirectoryOutputFiles` |
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
| An open-vocabulary value is stored normalized and never rewritten; one longer than `LABEL_MAX_BYTES` makes the run `malformed` | `grade.TestOpenVocabularyValues` |
| A taxonomy record never changes, a new one keeps every mapped raw label, and an unmapped raw label counts as a label of its own and never as missing | `grade.TestTaxonomyLineage` |
| A canonicalizer runs once for an experiment of the suite's owner with unmapped labels, reads only the raw labels and their counts, of the experiment's trials and of its comparisons' reference sides, adds labels up to `MAX_LABELS_OPEN_VOCABULARY` and maps only unmapped ones, appends one record naming itself, and never runs for a grantee's experiment | `grade.TestCanonicalizer`, against the stub Lux |
| Every check runs with network `none`, in a `topos` machine's check phase and in every check sandbox | `check.TestNoNetworkInTheCheckPhase`, e2e tier |
| A candidate-level judge runs once per candidate after its cells are graded, bounds its input at `JUDGE_MAX_INPUT_BYTES` in equal shares per trial, appends one generation per candidate with no trial, and holds the experiment from completing until it ends | `grade.TestCandidateLevelJudge` |
