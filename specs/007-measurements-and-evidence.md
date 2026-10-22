---
title: "Measurements and evidence: the append-only measurement store and its current values, the configured, sent and observed layers of a trial, evidence references into the owner's Arca space, where each kind of evidence lives, retention, and dataset export"
status: in-progress
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
  - specs/006-grading.md
affects: [internal/store/, internal/evidence/, internal/export/, api/]
effort: medium
created: 2026-10-17
updated: 2026-10-21
author: changkun
---

# Measurements and evidence

## Overview

Every figure Examen reports is computed from measurements, and every
measurement traces to a trial, a grader version and the evidence the
grader read. This spec fixes how measurements are stored and which one
is current, what a trial records about the conditions it ran under,
how evidence is referenced in the owner's Arca space and laid out
there, how long each piece is kept, and the dataset export a training
loop reads.

Examen holds rows and references. The bytes of a transcript, a file or
a check's output live in Arca, in the space of the experiment's owner,
written with the owner's identity (spec 004): a `topos` trial's in its
session's own folder, everything else under the owner's reserved
folder `files/evals/`, which also holds the copies of each task's
inputs and references that an experiment delivers (spec 005). Examen
never copies evidence into its own database, nor out of a session's
folder.

## Design

### The measurement store

One Postgres table holds every measurement of every owner.

```sql
CREATE TABLE measurements (
  id          text PRIMARY KEY,              -- msr_<ulid>
  owner       text NOT NULL,
  experiment  text NOT NULL REFERENCES experiments (id) ON DELETE CASCADE,
  cell        text,                          -- null for a candidate-level metric
  trial       text REFERENCES trials (id) ON DELETE CASCADE,
  candidate   text,                          -- candidate digest, for a candidate-level metric
  metric      text NOT NULL,                 -- metric digest
  grader      text NOT NULL,                 -- grader digest, 'builtin', 'builtin:decline' or 'self:<harness>'
  annotator   text NOT NULL DEFAULT '',      -- 'human:<subject>' for an annotation grader
  self_reported boolean NOT NULL DEFAULT false,
  generation  integer NOT NULL CHECK (generation >= 1),
  value_bool  boolean,
  value_num   double precision,
  value_text  text,
  rationale   text NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (trial, grader, annotator, generation, metric),
  CHECK (num_nonnulls(value_bool, value_num, value_text) = 1),
  CHECK (self_reported = (grader LIKE 'self:%')),
  CHECK ((trial IS NOT NULL AND cell IS NOT NULL AND candidate IS NULL)
      OR (trial IS NULL AND cell IS NULL AND candidate IS NOT NULL))
);

CREATE UNIQUE INDEX measurements_candidate_level
  ON measurements (experiment, candidate, grader, generation, metric)
  WHERE trial IS NULL;

CREATE INDEX measurements_cell_grader
  ON measurements (cell, grader, trial, annotator, generation DESC);
CREATE INDEX measurements_experiment_metric
  ON measurements (experiment, metric);
```

`experiment` and `cell` repeat what the trial already says, so that
the analysis reads one cell's values without a join through trials. A
candidate-level measurement (spec 002) names its experiment and
candidate and no trial or cell; its generations count per experiment,
candidate and grader, under the second unique index.

**A grading is one transaction.** One pass of one grader version over
one trial is a grading. All of its measurements carry one generation
and are inserted in one transaction, so a grading is visible whole or
not at all. The generation is `1 + max(generation)` over the rows with
that trial, grader and annotator, or 1 when there are none, computed
inside the transaction; `annotator` is empty but for an annotation
grader, whose generations count per person (spec 006). Two gradings of
one trial by one grader that race collide
on the unique constraint; the loser's transaction is retried and takes
the next generation. A generation therefore numbers gradings, not
values: a grader that produces three metrics writes three rows with
the same generation.

**Validation on append.** A measurement is refused, naming the field,
when:

- its metric is not in the grader's `produces` (spec 002);
- its value does not fit the metric's type: `binary` takes a boolean;
  `score` a finite number inside `range`; `count` a non-negative
  integer; `duration` a non-negative number of seconds; `cost` a
  non-negative number of US dollars; `label` one of `labels`, or for an
  open vocabulary a label as below. NaN and infinities are refused for
  every numeric type;
- its trial is not a finished trial (spec 002): not `done`, or ended
  `infra_failed`, `over_budget` or `canceled`, or retried;
- it is self-reported, under `self:<harness>`, and its trial is not of
  an `external` candidate whose `harness` that is, or its metric is not
  in the suite, or a grader produces its metric for the trial's task;
- its trial's outcome is outside its metric's `applies_to` (spec 002);
- its metric is of level `cell`, which is never measured; or its
  metric's level and its scope disagree: a trial for a metric of level
  `trial`, an experiment and one of its candidates for one of level
  `candidate`.

A `label` value of an open-vocabulary metric is any string of 1 to
`LABEL_MAX_BYTES` once normalized (spec 006), and is stored
normalized.

**Append-only.** A trigger on the table raises on every `UPDATE`, and
on a `DELETE` while the measurement's trial row still exists, or for a
candidate-level row its experiment row. The cascade from deleting a
trial or an experiment runs after its row is gone, so it passes. No route updates or deletes a measurement; the only way a
measurement leaves the store is the deletion of its experiment
(Retention, below).

### The current value

For each task of a suite version, each metric the suite lists has one
**producing grader**: the one grader among the task's `checks` and the
suite's `graders` whose `produces` names it, other than an
`annotation` grader. `declined`'s producer is the suite's decline
judge, `builtin:decline` or its `decline_judge` (spec 006), and every
other built-in metric's is `builtin`. A metric that only an annotation
grader produces has that grader as its producer. Spec 002's rule holds
at apply: a suite in which two producers other than annotation graders
produce one metric for one task is refused, naming the metric and both
graders. An annotation grader may produce a metric that a judge also
produces; its measurements calibrate the judge (spec 006) and are never
that metric's current value.

The **current value** of metric `m` for trial `t` under producing
grader `G` is read in two steps:

1. `g*` is the highest generation of `G` for `t`, over every metric.
2. The current value is the measurement `(t, m, G, g*)`. When grading
   `g*` produced no value for `m`, the value is **missing**. An
   earlier generation's value is never substituted: a re-grade
   supersedes the whole earlier grading.

When `G` is an annotation grader, both steps run per annotator, and the
current value is the people's value of spec 006: the one annotator's
value, or their consensus.

A metric the suite lists that no grader produces for a task has no
producing grader there. For an `external` trial of that task its
current value is read as above with `G` the grader `self:<harness>`:
the latest value the external harness submitted, `self_reported` true. For any
other trial it is missing.

A candidate-level metric's current value for a candidate of an
experiment is the measurement of its producing grader's highest
generation for that experiment and candidate. An open-vocabulary
metric's current value is the raw label; analysis reads it as its
canonical label at the taxonomy record a comparison or summary names,
and a raw label the record does not map as a label of its own, marked
unmapped and never missing (specs 006 and 008). A
cell-level metric has no current value of its own: analysis computes
it from the cell's current values of its `of` (spec 008).

The analysis reads one cell's current values with one query over the
`measurements_cell_grader` index:

```sql
SELECT m.*
FROM measurements m
JOIN (SELECT trial, grader, annotator, max(generation) AS g
      FROM measurements
      WHERE cell = $1 AND grader = ANY($2)
      GROUP BY trial, grader, annotator) latest
  ON m.trial = latest.trial
 AND m.grader = latest.grader
 AND m.annotator = latest.annotator
 AND m.generation = latest.g
WHERE m.cell = $1 AND m.metric = ANY($3);
```

A comparison reads a point in time and records exactly what it read,
one row per measurement, so that it can be recomputed after later
generations have been appended. A provisional comparison, `final`
false, replaces its rows in the transaction that recomputes it; once
`final` is true its rows never change (spec 008):

```sql
CREATE TABLE comparison_inputs (
  comparison  text NOT NULL REFERENCES comparisons (id) ON DELETE CASCADE,
  measurement text NOT NULL,
  PRIMARY KEY (comparison, measurement)
);
```

Which grader each side of a comparison is read at, when the two sides
ran different suite versions, is spec 008's.

**Built-in measurements.** When a finished trial reaches `done`,
Examen writes generation 1 of
grader `builtin` with every built-in metric of spec 002 but `declined`,
from the trial's outcome and its observed layer: `completed` is true
when the outcome is `completed` or `declined`, and `cost_usd`,
`input_tokens`, `output_tokens`, `reasoning_tokens`, `duration`,
`turns` and `tool_calls` come from the observed layer below. `declined`
is a measurement of the decline judge (spec 006). A trial's outcome is
final, so only its observed layer changes later: when a usage record
reaches Lux late, Examen appends the next `builtin` generation with
every built-in metric it fills again.

**Check run and judgment records.** A trial's `checks` and `judgments`
(spec 002) are rows beside its measurements, keyed by trial and
grader, and a judgment also by generation, appended and never updated, and removed only with their
experiment. A judgment record keeps the request digest a judge sent
and its Lux request ids, so a judge's spend and a refusal rate are read
from them (spec 006).

**Indexes on trials.** The analysis counts a cell's finished trials
(spec 002) through an index on `trials (cell, state, outcome)`.

### Provenance layers

What a candidate was configured to do, what its agent actually asked
for, and what actually served it can differ: a harness rewrites a
reasoning setting, a gateway falls back to another model, an adaptive
model spends no reasoning tokens at a high reasoning level. Examen
keeps the three layers apart and never merges them.

| Layer | Fields | Source | Kept on |
|---|---|---|---|
| configured | the provenance tuple of spec 002: `candidate.kind`, `agent_or_image_digest`, `program`, `model`, `reasoning`, `tools`, `task_hash`, `environment_digest`, `workspace_ref`, `grader_digests` | resolved by Examen when the experiment is applied, from the candidate, the task and the suite | the cell's `tuple`; not copied onto trials |
| sent | `requests`: the number of model requests the trial's system under test made; `by_setting`: a list of `{model, reasoning, requests}`, one entry per distinct pair, sorted by `model` and then `reasoning` | for a `topos` trial, the session's records of its requests; for a `cella` or `external` trial, Lux's records of them, never a file the program could write (spec 005) | the trial's `sent` |
| observed | `served`: a list of `{model, requests}`, the model Lux records as having served each request, sorted by `model`; `input_tokens`, `output_tokens`, `reasoning_tokens`, `cost_usd`; `cost_state`: `known`, `unpriced` when a record has `priced` false, or `unknown` for an external trial Lux holds no attributable record of (spec 005), and `cost_usd` is absent but for `known`; `turns`, `tool_calls`; `duration`; `service_versions`: an object from a service's name to its version; `service_usage`: an object from a service's name to `{cost_usd_micro, units}`, the usage that API reported for itself; `machines`: for a `topos` trial, each machine its session ran on, `{kind, id}`, once, in the order its log's `session.machine` events attached them | Lux's usage records of the trial's requests for `served`, the token counts and `cost_usd`; the executor for `turns` and `tool_calls`; Examen for `duration`, as `agent_ended_at` less `started_at`; for `service_versions` and `service_usage`, the program's `/examen/observed.json` or the external harness's submission (spec 005); the session's log for `machines` | the trial's `observed` |
| declared | for an `external` trial: `harness_version` and `image`, as its external harness declared them | the external harness's submit or import (spec 005) | the trial's `declared` |

`reasoning` is one canonical string per request: `level:<value>`
when the request named a reasoning level, `budget:<tokens>` when it
named a reasoning token budget, `off` when it turned reasoning off, and
`default` when it named nothing.

Cost is never estimated from token counts; Lux's records are its one
source (spec 001, decision 5), and a cost that is `unpriced` or
`unknown` is never read as zero. `service_usage` is an API's own figure,
kept apart and never added into `cost_usd` (spec 005). An imported trial (spec 005) carries
the same three layers, its configured layer read from the records of
the session or sandbox it came from.

Spec 008 computes confounds from the configured layer and from the
sent, observed and declared layers; spec 010 shows the configured and
observed values side by side wherever they differ.

### Evidence references

Every item of evidence is one object in the owner's Arca space,
referenced from the trial's `evidence` list by an **evidence ref**:

| Field | Type | Meaning |
|---|---|---|
| `kind` | enum | one of the kinds below |
| `name` | string | for `file`, the workspace-relative path the task's `evidence` names; for `output`, the path under `/examen/outputs/` the task's `outputs` names; for `check_output`, the file within the check's folder; empty otherwise |
| `owner` | subject | the Arca space, which is the experiment's owner |
| `path` | string | the object's path in that space: within the session's own folder for a `topos` trial's evidence, and under `files/evals/` for everything else (Evidence layout, below) |
| `digest` | string | `sha256:` and the hex of the SHA-256 of the object's bytes, computed by the writer while writing |
| `size` | int | bytes |
| `content_type` | string | as written |
| `checksum`, `checksum_kind` | string, string | Arca's checksum of the object and its kind, exactly as Arca answers them: `checksum_kind` is `sha256` or `etag` |
| `grader`, `generation` | grader digest, int | for `judge_rationale`: the grading it belongs to |
| `written_at` | time | |

`digest` and `checksum` are both kept because Arca checksums an object
it assembled from upload parts by the store's ETag, not by SHA-256. A
ref is checked cheaply against Arca's checksum, without reading the
bytes, and fully against `digest`.

| Kind | Holds | Written by | When |
|---|---|---|---|
| `transcript` | the system under test's whole event log: a Topos session's log, a program's forwarded requests and responses, or what an external harness submitted | the executor's core: the Topos session, or the `cella` trial driver; examend for an external trial | when the system under test ends, or at the submit |
| `final_message` | the system under test's last message, as text | the executor's core; examend for an external trial | when the system under test ends, or at the submit |
| `output` | one file under `/examen/outputs/` that the task's `outputs` names, as the system under test left it; for a directory output, one tar archive of its kept files (spec 005) | the executor's core; examend, from the submit or the import, for an external trial | when the system under test ends, or at the submit |
| `output_manifest` | every output file with its path, size and SHA-256 and whether it was kept, and, when the outputs passed a limit or an archive was refused, which | the executor's core; examend for an external trial | with the outputs |
| `observed` | the service versions a program recorded, as the trial driver kept them | the `cella` trial driver | when the system under test ends |
| `requests` | one JSON line per model request as the forwarder saw it: the request id, the model and reasoning sent; kept as evidence and never read as the sent or observed layer, which come from Lux's records | the `cella` trial driver; a Topos session's requests are in its log | when the agent ends |
| `file` | one workspace file the task's `evidence` names, as it stood after the agent ended | the executor's core | before the checks run |
| `check_output` | for each check: `result.json`, the output document of spec 006, and `stdout.txt` and `stderr.txt` | the executor's core for a `topos` trial; examend, from the check sandbox, for a `cella` or `external` trial | after the checks |
| `judge_rationale` | for each judge grading: the digest of the inputs the judge read, its whole structured answer and its rationale; for a candidate-level judge and a canonicalizer, kept with the experiment rather than a trial | Examen for a model judge; Examen, from the judge session's final answer, for an agent judge | when the grading is recorded |

**Writes are create-only.** Every evidence object is written once, with
`If-None-Match: *` where Examen or the trial driver writes it, so a
path holds one content for its whole life and a retried trial or
grading never overwrites another's evidence. An
object above Arca's inline size (Arca's `ARCA_INLINE_BYTES`) is written
through Arca's uploads. The executor contract of spec 005 carries this
requirement.

**Verification at intake.** When the executor reports a trial, Examen
reads each ref's object headers from Arca as the owner (spec 004) and
compares the size and Arca's checksum with the ref; for an object Arca
checksums with SHA-256, Arca's checksum must also equal `digest`. A
missing object or a mismatch marks the trial `infra_failed` with the
reason `evidence_unverified`, and the retry rules of spec 005 apply.
A judge's evidence is verified the same way before its grading is
recorded, and a failure fails the grading (spec 006).

**In the v0.1 profile**, with no file store and no owner token, an
external trial's `output` refs are the `arca` references its external
harness submitted, with the digests it declared, recorded and never
read: no intake verification runs, no `output_manifest` is written, and
the trial has no `transcript` or `final_message` ref (specs 003 and
005). `evidence verify` reports such a ref `unverified` until a file
store is configured.

**Reading.** Examen's API returns refs, never bytes. The bytes are
read from Arca by whoever Arca allows to read them; a judge reads
them through Arca as the owner (spec 006). The `evidence verify`
operation downloads each ref of an experiment, recomputes its digest,
and reports every ref as `ok`, `changed`, `missing` or, with no
file store to read it from, `unverified`. It never alters
a measurement or a trial.

### Evidence layout

A `topos` trial's evidence stays in its session's own folder, where
the session kept it (spec 005, R9): the rollout reports each object's
path and digest, and the trial's refs point there. Everything Examen
writes for an owner, and everything a `cella` trial writes, lives under
one reserved folder of the owner's space, `files/evals/`. The folder is
fixed so that an Arca authorization endpoint can recognize it, reserve
it, and name Examen as a writer there.

```
files/evals/
  experiments/<experiment id>/
    experiment.json                   the experiment as applied, with its suite,
                                      candidates, tasks, metrics and graders
    tasks/<task hex>/
      inputs/<name>                   each input of the task, copied once (spec 005)
      reference/<name>                each reference, delivered only to checks
      check-files/<grader hex>/       a benchmark's check files, for an experiment
                                      run through it (spec 013)
    cells/<cell id>/
      cell.json                       the cell and its resolved provenance tuple
      trials/<trial id>/
        transcript.jsonl              a cella trial's, as the trial driver wrote them,
        final_message.txt             and an external trial's, as examend wrote them
        requests.jsonl
        observed.json
        outputs/<path>                a file output, or for a directory output <path>.tar
        outputs.manifest.json         the output manifest
        files/<workspace-relative path>
        checks/<grader hex>/result.json
        checks/<grader hex>/stdout.txt
        checks/<grader hex>/stderr.txt
        judges/<grader hex>/<generation>/rationale.json   every trial's
    candidates/<candidate hex>/judges/<grader hex>/<generation>/rationale.json
                                      a candidate-level judge's (spec 006)
    taxonomies/<metric hex>/<generation>/rationale.json
                                      a canonicalizer's (spec 006)
  exports/<export id>/                Dataset export, below
  reports/<report id>/<n>/            spec 010
  publications/<publication id>/<number>/
                                      a publication version's copies (spec 013)
```

- `<grader hex>`, `<task hex>`, `<candidate hex>` and `<metric hex>`
  are the 64 hexadecimal characters of the digest, without the
  `sha256:` prefix.
- A `file` keeps the path the task names, under `files/`. Applying a
  task whose `evidence` names an absolute path or a path containing
  `..` is refused.
- A retried trial is a new trial with its own id and its own folder.
- A `topos` trial's folder here holds only `judges/`.
- `tasks/` is written by the `prepare` job, once per experiment and
  task, before the task's first rollout (spec 005). A task's
  `reference/` is read only to deliver it to checks.
- Examen writes `experiment.json` once, when the experiment is
  applied, and each `cell.json` once, when the cell is created, so the
  tree is readable without Examen's database. The `cella` trial driver
  writes each `cella` trial's folder except `checks/` and `judges/`,
  under the prefix the rollout names (spec 005). Examen writes `tasks/`,
  `judges/`, `candidates/`, `taxonomies/`, `exports/`, `reports/` and
  `publications/`, an external trial's folder from its submit or
  import, and a `cella` or external trial's `checks/` from its check
  sandbox.
- `publications/` holds copies, each with the digest of the object it
  copies, so a published version stands without the evidence it came
  from; spec 013 fixes what each version copies.

### Retention

**Examen keeps** every object, measurement, evidence ref and comparison
until the owner deletes the experiment that holds it. Nothing in
Examen expires by age.

**Arca keeps** the bytes, under Arca's own rules: an overwrite keeps a
version, a delete goes to the trash for Arca's `ARCA_TRASH_RETENTION`, and an
Arca authorization endpoint may set a quota or turn versions and the
trash off for a path. Examen sets no retention in Arca and deletes
nothing there except as below. When an owner deletes evidence in Arca
directly, the refs stay, `evidence verify` reports them `missing`, the
measurements are unaffected, and a re-grade that needs the missing
evidence fails (spec 006).

**Deleting an experiment** (`DELETE /experiments/{id}`, spec 003) takes
two parameters and asks the authorizer for `experiment.delete` (spec
004):

| Parameter | Values | Effect |
|---|---|---|
| `evidence` | `keep`, `trash`, `purge`; required, with no default | `keep` leaves the Arca folder; `trash` deletes `files/evals/experiments/<id>/` recursively into Arca's trash; `purge` deletes it permanently. A `topos` trial's evidence in its session's folder belongs to the session and is left to Topos's and Arca's rules |
| `baselines` | `refuse` (default), `delete` | what happens when a baseline record names the experiment |

The deletion is refused, naming what blocks it, when:

- the experiment is `pending`, `running` or `grading`
  (`experiment_not_finished`); it is canceled first;
- a baseline record names it and `baselines` is `refuse`
  (`experiment_is_baseline`, listing the records);
- another experiment adopted any of its trials (spec 005)
  (`experiment_adopted_from`, naming those experiments).

Otherwise one transaction removes the experiment, its cells, trials,
measurements, check run and judgment records, and event log; every
comparison that read any of those measurements, on either side, with
its inputs; every report of the experiment and every report that
rendered a removed comparison; every share of the experiment; and,
with `baselines=delete`, the baseline records that name it. A
publication version made from a removed report stays, with its
snapshot and its folder: a publication is withdrawn, never removed with
its source (spec 013). Bar evaluations stay: each holds a
copy of what it read (spec 009) and reads afterwards as an evaluation
whose comparison is gone. After the commit Examen deletes the Arca
folder as `evidence` says, as the owner. An Arca failure does not undo
the deletion; the folder deletion is queued and retried until Arca
accepts it, and every failure is logged. Arca's endpoint may answer a
delete with `limits.trash: false`, which makes `trash` permanent.

Exports, report bundles and publication folders already written under
`exports/`, `reports/` and `publications/` are the owner's files and
are left alone. Their evidence
refs may stop resolving.

### Dataset export

An export writes trials, with their outcome, measurements and evidence
refs, as JSON lines into the owner's Arca space, in a versioned format
a training loop reads without Examen. In the build order, dataset
export follows the console path: the comparisons of spec 008 shown in
a console as spec 010 fixes.

**The request.** Creating an export (`POST /exports`, spec 003) asks
`export.create` (spec 004) and takes the request fields of spec 002's
dataset export:

| Field | Meaning |
|---|---|
| `format` | `examen.dataset/v1`, the only version so far, and the default |
| `filter.suites` | suite digests; a trial matches when its experiment ran one of them |
| `filter.experiments` | experiment ids, at most `MAX_EXPORT_EXPERIMENTS` |
| `filter.candidates` | candidate digests |
| `filter.tasks` | task digests |
| `filter.outcomes` | trial outcomes; by default every outcome except `infra_failed`, `over_budget` and `canceled` |
| `filter.metrics` | at most `MAX_EXPORT_CONDITIONS` conditions `{metric, op, value}`, `op` one of `>=`, `<=`, `>`, `<`, `=`, `!=`, evaluated on current values; a trial whose value is missing fails the condition; `label` and `binary` metrics take only `=` and `!=` |
| `include_imported` | include imported trials; default true |
| `all_generations` | also write every earlier generation of each measurement; default false |
| `evidence` | `reference` (default): records point at the experiment's evidence; `copy`: each matching trial's folder is copied into the export with Arca's folder copy, and records point at the copies |

Every filter field is optional and the conditions are joined by "and".
A filter that names nothing exports every trial of the owner. An
export that would hold more than `MAX_EXPORT_RECORDS` records is
refused before anything is written, naming the count.

**The record.** One JSON object per line, one line per trial, in the
canonical JSON of spec 002 (keys sorted, no insignificant whitespace):

| Field | Holds |
|---|---|
| `format` | `examen.dataset/v1` |
| `trial`, `index`, `retry_of`, `imported` | the trial's id and its fields of spec 002 |
| `experiment`, `cell` | ids |
| `suite`, `task`, `candidate` | each `{name, digest}`; the full objects are in the side files |
| `tuple` | the configured layer: the cell's provenance tuple |
| `sent`, `observed`, `declared` | the trial's layers, as above; `declared` for an `external` trial alone |
| `outcome` | the trial's outcome |
| `started_at`, `ended_at` | |
| `measurements` | an object keyed by metric name. Each value is `{metric, type, value, grader, generation, rationale, measurement}`: the current value under the metric's producing grader, with the metric digest and the measurement id, and `self_reported: true` when an external harness submitted it; for an open-vocabulary metric also `canonical`, the canonical label at the suite's current taxonomy record, `null` when it maps none, and `taxonomy`, that record's id. A missing value is `{metric, type, value: null, grader, generation, missing: true}` |
| `generations` | present with `all_generations`: per metric name, every measurement of every producer, oldest first, in the same shape |
| `evidence` | the trial's evidence refs, as above |

A metric's name is unique per owner, and one trial belongs to one
suite version, so the keys of `measurements` never collide in one
record.

**The files.** Under `files/evals/exports/<export id>/`:

```
records-00000.jsonl ... records-NNNNN.jsonl   at most EXPORT_RECORDS_PER_FILE lines each
tasks.jsonl          one line per task digest the records name: the task object of spec 002
candidates.jsonl     one line per candidate digest: the candidate object
metrics.jsonl        one line per metric digest: the metric object
graders.jsonl        one line per grader digest: the grader object
taxonomies.jsonl     one line per taxonomy record the records read: the record of spec 002
candidate-measurements.jsonl
                     one line per candidate-level measurement of the exported
                     experiments' candidates, in the shape of a record's measurement
                     with `experiment` and `candidate`
evidence/<trial id>/ with evidence=copy, each trial's copied folder
manifest.json        written last
```

Records are ordered by trial id, which orders them by creation time,
and split across files in that order. Every file is written
create-only.

**The manifest.**

| Field | Holds |
|---|---|
| `format` | `examen.dataset/v1` |
| `export` | the export's id |
| `owner` | the subject |
| `created_at`, `completed_at` | |
| `examen_version` | the version of the `examend` that wrote it |
| `request` | the request exactly as accepted, filter included |
| `records` | the number of records |
| `measurements_digest` | `sha256:` of the sorted list of measurement ids the records hold, one per line |
| `files` | each file's path relative to the export folder, its `digest`, its `size` and, for a `.jsonl` file, its line count |

A reader takes the manifest's presence as the export's completeness:
a folder without `manifest.json` is an export that has not finished
or that failed. A training loop fetches a whole export in one call
with Arca's materialize route (`GET /v1/files/materialize` with the
export folder as `prefix`), and checks each file against the
manifest's digests. With `evidence=copy` that one call also fetches
every transcript and file the records reference, and the export keeps
working after its experiments are deleted.

**Determinism.** The rows an export reads are read in one
`REPEATABLE READ` transaction. Two exports with the same request over
the same measurements write byte-identical record files.

**The export object** is spec 002's dataset export, with the prefix
`dsx_`: the request fields above, `state` (`pending`, `writing`,
`complete`, `failed`), `path`, `records`, `manifest` (`{path, digest}`
of `manifest.json`), `error` when failed, `created_by`, `created_at`
and `completed_at`. An export that fails leaves the files it wrote and
no manifest; `error` names the cause.

**Versioning.** Within `examen.dataset/v1`, fields are only ever
added, and a reader ignores fields it does not know. Removing a field
or changing what one means makes `examen.dataset/v2`, and Examen keeps
writing every version it has published on request.

### Limits

The constants of spec 002 this spec holds: `EXPORT_RECORDS_PER_FILE`,
`MAX_EXPORT_RECORDS`, `MAX_EXPORT_CONDITIONS`,
`MAX_EXPORT_EXPERIMENTS` and `LABEL_MAX_BYTES`.

## Not in this spec

How an executor captures the sent layer and attributes Lux records to
a trial, how it receives each trial's evidence prefix, and retries
(005). The check output document, how judges read evidence, and
calibration (006). The analysis of measurements (008). The routes and
their shapes (003). Who may read an export or an evidence object
beyond its owner (013, and Arca's own grants), and what a published
dataset carries (013). Compression of
evidence, and removing evidence by age, which is Arca's and its
operator's.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| A grading's measurements share one generation and appear together or not at all; two concurrent gradings of one trial by one grader get generations 1 and 2 | `store.TestAGradingIsOneTransaction`, `store.TestConcurrentGradingsTakeSuccessiveGenerations` |
| A candidate-level measurement names an experiment and a candidate and no trial, takes generations per experiment, candidate and grader, and a row with both a trial and a candidate, or neither, is refused by the database; a cell-level measurement is refused | `store.TestMeasurementScopes` |
| An open-vocabulary value is stored normalized, read through a taxonomy record, and read as a label of its own, never missing, when that record does not map it; an export record carries its canonical label, `null` for an unmapped one, and the record | `store.TestOpenVocabularyReads`, `export.TestCanonicalLabels` |
| A trial's `service_usage` is kept in `observed` and `cost_usd` equals Lux's records alone | `store.TestServiceUsageKeptApart` |
| An `UPDATE` of a measurement and a direct `DELETE` are refused by the database; deleting the experiment removes its measurements | `store.TestMeasurementsAreAppendOnly` |
| A value of the wrong type, a score outside its range, a label outside its labels, NaN, infinity and a negative count are each refused naming the field | `store.TestMeasurementValidation` |
| When the latest grading of a grader lacks a metric, its current value is missing and the earlier generation's value is not returned | `store.TestCurrentValueNeverFallsBack` |
| A suite in which two graders other than annotation graders produce one metric for one task is refused at apply, naming both | `store.TestOneProducerPerMetric` |
| A late Lux record appends the next `builtin` generation with every built-in metric Examen fills, and generation 1 is unchanged; `declined` is never written under `builtin` | `store.TestBuiltinsAppendAGeneration` |
| Two annotators' first answers to one trial each take generation 1, and an annotation-only metric's current value is their consensus | `store.TestAnnotatorGenerations` |
| A final comparison recomputed after a re-grade from its recorded inputs gives byte-identical results, and a provisional one's recorded inputs are replaced with each recompute | `analysis.TestRecomputeFromRecordedInputs` |
| A trial's configured, sent and observed layers are filled from the cell, the executor and Lux respectively, and `cost_usd` equals the sum of Lux's records for the trial | `store.TestProvenanceLayers`, against the Lux stub |
| A trial whose evidence object is missing, or whose size or checksum differs from its ref, is marked `infra_failed` with `evidence_unverified` | `evidence.TestIntakeVerification`, against the Arca stub |
| A self-reported value is refused for a trial of another candidate kind or external harness, for a metric the suite does not list, and for a metric a grader produces for the task; an accepted one is the current value of its metric for that external trial, and the database refuses a row whose `self_reported` disagrees with its grader | `store.TestSelfReportedValues` |
| A task's inputs and references are written once under `tasks/<task hex>/`, and an external trial's outputs, transcript and final message under its trial folder, create-only | `evidence.TestTaskFolderAndExternalTrial`, against the Arca stub |
| A second write to an evidence path fails and the first content stays | `evidence.TestWritesAreCreateOnly` |
| Every path Examen or a `cella` trial writes for an experiment is under `files/evals/experiments/<id>/` in the layout above, a `topos` trial's refs point into its session's folder, and a task naming an absolute or `..` evidence path is refused | `evidence.TestLayout`, `evidence.TestEvidencePathRefused` |
| Deleting an experiment without `evidence` is refused; one that is running, named by a baseline, or adopted from is refused naming the blocker; a permitted deletion removes the rows and comparisons and trashes or purges the folder as asked, and its bar evaluations stay readable | `retention.TestDeleteExperiment`, against the Arca stub |
| An export writes records sorted by trial id in files of at most `EXPORT_RECORDS_PER_FILE`, the side files, and the manifest last with every file's digest; two identical requests write identical record files | `export.TestExportLayout`, `export.TestExportIsDeterministic` |
| Each filter field and each condition operator selects exactly the matching trials, and a missing value fails a condition | `export.TestFilters` |
| An export above `MAX_EXPORT_RECORDS` and a filter with more than `MAX_EXPORT_CONDITIONS` conditions are refused before any file is written | `export.TestExportLimits` |
| With `evidence=copy`, every ref in the records points into the export's folder and its digest matches the copy | `export.TestEvidenceCopy`, against the Arca stub |
| A record of `examen.dataset/v1` validates against the committed schema, and a reader built against it accepts a record with an added field | `export.TestRecordSchema` |

## Outcome

In progress. v0.1.0 builds the measurement store: the `measurements`
table with its value, scope and `self_reported` constraints and the
trigger that keeps it append-only, the current value, and the
gradings written when a trial is done (`builtin` for `completed` and
`duration`, the built-in decline judge, and `self:<harness>` for the
values an external harness reports, each checked against its metric).
Of the provenance layers it fills the configured tuple, the declared
layer, and the observed layer's duration, service versions, service
usage and the Lux request ids as given; every cost is `unknown`.
Evidence references are the Arca paths and digests a harness declares,
recorded and never read. v0.2.0 to v0.4.0 add nothing to this spec.
v0.7.0 grades a `topos` trial with its checks and model judges (spec
006): each grading that gives values appends its grader's next
generation under the grader's digest, its check run or judgment record
is kept on the trial (`checks`, `judgments`) with the generation it
appended, and a judge's rationale is on each measurement. A `topos`
trial's evidence is the session's log, pinned at the last event read by
the SHA-256 of its JSONL, and its kept working directory as the output
`workspace/`, a folder of the owner's Arca space pinned by the folder
digest of the files its log's `work.saved` events record (the SHA-256 of
one `<hex sha256>  <path>` line per file in path order, `id.FolderDigest`),
each file's digest checked when a grader reads it. A check's stdout and
stderr are kept in its record by size, digest and tail, not written to
the file store. Not built: re-grading, annotator generations and
candidate-level measurements; every write to the file store; retention;
and dataset export.

Every row below shipped in v0.1.0. The criteria cite tests of
packages not yet written (`store`, `evidence`, `export`); the table
names the tests that hold the same behavior.

| # | Verdict | Held by |
|---|---|---|
| 1 | Partly met: one grading's rows take one generation and are written in the transaction of the change that ends the trial; no test counts them, and no path makes two gradings of one trial by one grader | `postgres.TestWritesFailWhole` |
| 2 | Partly met: the table admits a trial's row or a candidate's, with one generation sequence per experiment, candidate and grader; no test inserts a candidate-level row, and nothing writes one yet | no test yet |
| 3 | Partly met: a value is stored normalized, read through the suite's current taxonomy record, and an unmapped label reads as a label of its own; export records wait on dataset export | `objects.TestValuesAndCurrent`, `httpapi.TestTheExternalFlow`, `analysis.TestTaxonomyReads` |
| 4 | Partly met: `service_usage` is kept in `observed` and summed apart from the cost in USD; `cost_usd` waits on Lux, and no test reads stored usage back | `compare.TestCostOf`, `httpapi.TestSubmitAndImportRefusals` |
| 5 | Met | `postgres.TestMeasurementsAreAppendOnly` |
| 6 | Partly met: a wrong type, a score out of range, a label outside its labels, NaN, a fractional count and a negative duration are refused; infinity and a negative count have no case of their own, and the field named is not asserted | `objects.TestValuesAndCurrent`, `httpapi.TestSubmitAndImportRefusals` |
| 7 | Met | `objects.TestValuesAndCurrent` |
| 8 | Partly met: the suite is refused on `graders`, and annotation graders are exempt; that the refusal names both graders is not asserted | `objects.TestResolvedChecks`, `httpapi.TestApplyPreconditionsAndRefusals` |
| 9 | Partly met: `declined` is never written under `builtin`; late Lux records, and the generation they would append, wait on Lux | `objects.TestValuesAndCurrent`, `postgres.TestMeasurementsAreAppendOnly` |
| 11 | Partly met: one analysis input gives byte-identical results however it is ordered; re-grading is not built, a recompute reads the current measurements rather than the recorded `comparison_inputs`, and replacing a provisional comparison's recorded inputs is untested | `analysis.TestRecomputeFromRecordedInputs` |
| 12 | Partly met: the configured layer is filled from the cell; the sent layer, and the observed layer read from Lux, are not built | `objects.TestTuples` |
| 14 | Partly met: a value for a metric the suite does not list, or one a grader produces for the task, is refused, and an accepted one is the current value; other candidate kinds are refused at create, so that arm cannot be reached | `httpapi.TestSubmitAndImportRefusals`, `httpapi.TestTheExternalFlow` |
| 17 | Partly met: a task's `evidence` path with `..` is refused; the experiment's folder layout and `topos` refs wait on the file store and the `topos` executor | `objects.TestTaskRules` |

Not built: 10, annotations; 13, 15 and 16, the file store, with
verification at intake and create-only writes; 18, deleting an
experiment; 19 to 23, dataset export.
