# Using the API

For a builder writing a client of `examend`: an app, a CI job or an
external harness. It answers what a client needs to know before it calls
the API, the requests of each flow in order, the errors to branch on and
the limits a client meets. It does not list every route:
[`api/openapi.yaml`](../api/openapi.yaml) is the full reference, and
[Concepts](concepts.md) explains the objects and the analysis.

The requests below use the `$EXAMEN` base URL and the `api` helper that
[Running it locally](local.md#start-examend) defines, and they run
against the server and token that page sets up. Variables such as
`$METRIC`, `$SUITE`, `$A` and `$B` are the digests that page builds.
Responses are abridged to the members a client reads.

## Calling the API

**Base path.** Every route sits under `/v1`, or under
[`EXAMEN_BASE_PATH`](configuration.md#every-variable) when the
installation sets another. The probes `/livez`, `/readyz` and `/version`
stay at the root of the listener and take no token, and
`<base path>/version` answers the same build identity below the base
path. A running server
serves its own contract, the OpenAPI document of the release it runs,
without a token:

```sh
curl -s $EXAMEN/openapi.yaml | head -3
```

```text
openapi: 3.1.0
info:
  title: Examen
```

**Token and caller.** Every other route needs
`Authorization: Bearer <token>`, an ID token from an issuer the
installation trusts, addressed to one of its
[`EXAMEN_OIDC_AUDIENCE`](configuration.md#every-variable) audiences.
`examend` names the caller by the token's issuer and `sub` joined by a
pipe, `https://issuer.example.com|alice`. That string is the owner of
every object the caller creates, and a request acts in the caller's own
context unless it names another with the `owner` query parameter, which
the authorizer then decides. Without an authorizer a caller reaches only
the objects it owns ([the owner policy](authorizer.md#the-owner-policy)),
and an object the caller may not see answers as one that does not exist.
A request with no token is `401 missing_token`, and one with a token the
server refuses is `401 invalid_token` with the reason in
`details.reason`.

**Content.** Request and response bodies are JSON in UTF-8. Send
`Content-Type: application/json`. A body with a member the contract does
not describe is `400 unknown_field`, naming the member, so a client sends
exactly the members the reference lists. Times are RFC 3339 in UTC. A
duration is a string in Go's notation, such as `5m` or `2h30m`. Every
answer carries `X-Request-Id`; a client may send its own, a short id of
visible ASCII, to follow a request through the logs.

**Idempotency-Key.** `POST /experiments`,
`POST /experiments/{id}/imports`, `POST /experiments/{id}/work/lease`
and `POST /comparisons` take an `Idempotency-Key` header of visible ASCII
(the [fixed bounds](#fixed-bounds) give its length and how long a key is
kept). The same key from the same caller on the same route with the
same body answers the first response again, status and body, with
`Idempotent-Replayed: true`, and makes nothing. The same key with another
body or on another route is `409 idempotency_key_reused`. A repeat that
arrives while the first request is still being served is
`409 request_in_progress`, which a client retries. A first request
answered with a `5xx` keeps no answer, so retrying it runs it again. A
repeated lease is answered the experiment's `model_key` afresh, because
the kept copy leaves it out. A request without the header is not
deduplicated.

**If-Match and ETag.** A read or an apply of a versioned object (metric,
grader, task, suite or candidate) answers `ETag: "sha256:..."`, the
version's digest, and `Location`, the address of that version as
`<base>/suites/geography@sha256:...`. `If-Match: "<digest>"` on a `PUT`
applies only when the name's current version has that digest, and
`If-None-Match: *` only when the name has no version yet. Either that
does not hold is `412 precondition_failed`; read the object again and
decide. A baseline's tag is its id.

**Pagination.** A list answers `{"items": [...], "next_cursor": "..."}`,
newest first by id, and leaves out `next_cursor` on the last page.
`limit` is a whole number from 1 to the page maximum; pass `next_cursor`
back as `cursor`. A cursor is opaque and belongs to the list that issued
it: another list's, or a mangled one, is `400 invalid_cursor`. The grid
and a comparison's rows page the same way, by position in the suite. An
experiment's event log pages by sequence: `after=<seq>`, and its
`next_cursor` is the last sequence of the page.

## The objects

| Object | Identified by | What it is |
|---|---|---|
| Metric, grader, task, suite, candidate | an id (`met_`, `grd_`, `tsk_`, `sui_`, `cnd_` and a ULID), a name that is current for its owner, and the SHA-256 digest of its content | A versioned definition, applied by name with `PUT /<kind>/{name}`. A different body under the same name is a new version; the same body is no new version |
| Baseline | `bas_...`, and a name within a suite | A named, pinned reference for a suite, such as the candidate in production: `PUT /suites/{suite}/baselines/{name}` with an `experiment` and a `candidate` of that complete experiment |
| Experiment | `exp_...` | One execution of a suite against candidates, with a state of `pending`, `running`, `complete` or `canceled` and a `progress` summary |
| Cell | `cel_...` | One task of one candidate in an experiment, with a state such as `smoking`, `running`, `complete`, `blocked` or `over_budget` |
| Trial | `tri_...` | One run of a cell, with an `outcome`, its times, its `evidence` and, in a trial list, its current `values` by metric name |
| Measurement | `msr_...` | One value of one metric for one trial, from one grader, with `self_reported` set when a harness reported it |
| Comparison | `cmp_...` | A candidate's results against a reference, `final: true` once its figures are |

[Concepts](concepts.md#the-objects) explains each object and what the
analysis does with it.

**Apply by name.** `PUT /metrics/passed` creates the metric or a new
version of it and answers `201`; the same content again answers `200`
with the version it already is. Name each object once and read the
digest from the answer:

```sh
api -X PUT $EXAMEN/metrics/passed -d '{
  "type": "binary", "aggregate": "rate", "direction": "higher_is_better",
  "description": "Whether the reply was correct, as the harness checked it."}'
```

```json
{
  "id": "met_01m6srb02cgfy662jsh3kt8zxv",
  "name": "passed",
  "digest": "sha256:8b7d60caccadb437a4db31b54e58867cbedf3d1f6f45c96104a10136706a1b7a",
  "current": true,
  "type": "binary",
  "aggregate": "rate",
  "direction": "higher_is_better"
}
```

Read a version by name (its current version), by `name@sha256:...`, by
digest or by id at `GET /<kind>/{ref}`, list a name's versions at
`GET /<kind>/{name}/versions`, and list a kind at `GET /<kind>` with
`owner`, `name`, `versions=all` and the kind's own filters.

**Pinned references.** One object refers to another only by digest,
`sha256:...` or `name@sha256:...`, never by a bare name, so an experiment
always says exactly what it ran. A suite lists its tasks and metrics by
digest, an experiment names its suite and candidates by digest, and the
built-in metrics are `builtin:completed`, `builtin:declined` and
`builtin:duration`. A reference by name alone, a model alias, an image
by tag or an agent without its digest is refused:

```sh
api -X PUT $EXAMEN/suites/bad -d "{\"tasks\": [\"capital-of-france\"],
  \"metrics\": [\"$METRIC\"], \"primary_metric\": \"$METRIC\"}" | jq .error
```

```json
{
  "code": "unpinned_reference",
  "message": "A reference must name an exact version.",
  "details": {
    "detail": "a reference names a digest, or a name and a digest as name@sha256:..., and not \"capital-of-france\"",
    "field": "tasks[0]",
    "retryable": false
  }
}
```

A digest that names nothing the caller may use is
`422 reference_not_found` with `details.field` naming the member.

## The flows

### Define a metric, task, suite and candidates

[Running it locally](local.md#say-what-to-measure) builds these four
objects. Each is one `PUT` and each answer carries the `digest` the next
request needs:

```sh
METRIC=$(api -X PUT $EXAMEN/metrics/passed -d '{
  "type": "binary", "aggregate": "rate", "direction": "higher_is_better",
  "description": "Whether the reply was correct, as the harness checked it."}' | jq -r .digest)
TASK=$(api -X PUT $EXAMEN/tasks/capital-of-france -d '{
  "instruction": "What is the capital of France? Answer with the city name alone.",
  "timeout": "5m"}' | jq -r .digest)
SUITE=$(api -X PUT $EXAMEN/suites/geography -d "{\"tasks\": [\"$TASK\"],
  \"metrics\": [\"$METRIC\", \"builtin:completed\", \"builtin:duration\"],
  \"primary_metric\": \"$METRIC\"}" | jq -r .digest)
A=$(api -X PUT $EXAMEN/candidates/my-agent-v1 -d '{"kind": "external",
  "external": {"harness": "my-harness", "version": "1.0.0", "flags": {"prompt": "terse"}}}' | jq -r .digest)
B=$(api -X PUT $EXAMEN/candidates/my-agent-v2 -d '{"kind": "external",
  "external": {"harness": "my-harness", "version": "1.0.0", "flags": {"prompt": "step-by-step"}}}' | jq -r .digest)
```

A candidate has a `kind`. `external` candidates are your own harness's
configurations and need no other service. `topos` candidates run as Topos
sessions and `program` candidates are not built; a `program` candidate
applies, and an experiment that names one is refused
`422 executor_unavailable`.

### Create an experiment and read its progress

```sh
api -X POST $EXAMEN/experiments -H 'Idempotency-Key: geography-1' -d "{
  \"suite\": \"$SUITE\", \"candidates\": [\"$A\", \"$B\"], \"trials\": 10,
  \"budget\": {\"name\": \"local\"}}"
```

```json
{
  "id": "exp_01m6srbm5fvcwv2gbj0xcpcqhm",
  "trials": 10,
  "budget": { "name": "local" },
  "parallelism": 8,
  "reference": { "candidate": "sha256:d7895dd2..." },
  "mode": "execute",
  "scheduling": "task_by_task",
  "state": "pending",
  "progress": { "cells": { "pending": 2 }, "trials": { "pending": 2 }, "outcomes": {} }
}
```

The answer is `201` with `Location: <base>/experiments/<id>`. `suite`,
`candidates` and `budget` are required. `trials`, `parallelism`, `mode`
(`execute` or `import`), `scheduling` (`task_by_task` or `interleaved`),
`reference` (a `candidate` of the experiment or a `baseline` id),
`comparisons`, `independence` and `metadata` are optional. The first
candidate is the reference unless `reference` says otherwise. A budget is
a `name`; a `cap_usd_micro` is a Budget made at Lux and is refused
`422 core_not_configured` unless the authorizer assigns one. Needing a
service the installation lacks is refused at create, never left waiting:
[what each service adds](configuration.md#what-each-service-adds) lists
which.

Poll the experiment, or read the progress stream:

```sh
api $EXAMEN/experiments/$EXP | jq '{state, progress}'
```

```json
{
  "state": "running",
  "progress": { "cells": { "smoking": 2 }, "trials": { "pending": 2 }, "outcomes": {} }
}
```

`GET /experiments/{id}/stream` is a server-sent event stream. It opens
with a `snapshot` frame holding the experiment, then sends one frame per
event of the experiment's log, a `: keepalive` comment while quiet, and
an `end` frame once the experiment is `complete` or `canceled` and every
event was sent. Each frame's `id` is a sequence. A client that loses the
stream reconnects with `Last-Event-ID` and receives what it missed:

```sh
curl -sN -H "Authorization: Bearer $TOKEN" $EXAMEN/experiments/$EXP/stream
```

```text
id: 21
event: snapshot
data: {"id":"exp_01m6srey...","state":"running", ...}
```

The stream closes when the token expires, so a long reader reconnects
with a fresh token. `GET /experiments/{id}/events?after=<seq>` reads the
same log as pages.

### Be an external harness

A harness leases the pending trials of a candidate of kind `external`,
runs each, and submits what it produced. It calls the experiment's own
routes with a token of a caller who may act on the experiment.

```sh
api -X POST $EXAMEN/experiments/$EXP/work/lease -d "{\"candidate\": \"$A\", \"max\": 1}"
```

```json
{
  "lease": "lse_01m6srbwc90fxvvn03pg7gjjad",
  "expires_at": "2026-11-07T01:11:02.261938Z",
  "trials": [
    {
      "id": "tri_01m6srbm5mhpyrp6agsp5432j4",
      "cell": "cel_01m6srbm5h51jpfjazvcgm7fwk",
      "index": 0,
      "candidate": {
        "name": "my-agent-v1",
        "digest": "sha256:d7895dd2...",
        "external": { "harness": "my-harness", "version": "1.0.0", "flags": { "prompt": "terse" } }
      },
      "task": { "name": "capital-of-france", "digest": "sha256:0e643dbc..." },
      "instruction": "What is the capital of France? Answer with the city name alone.",
      "inputs": [],
      "outputs": [],
      "timeout": "5m",
      "lux_labels": "examen.trial=tri_01m6srbm5mhpyrp6agsp5432j4"
    }
  ]
}
```

`candidate` and `max` are optional. Without `candidate`, the lease takes
trials of every `external` candidate of the experiment; without `max`, it
takes every trial the experiment may run now, within its parallelism. A
cell's smoke trial comes first and its other trials only after the smoke
ends.

What a lease carries, per trial:

- `id`, `cell` and `index` identify the trial.
- `candidate` is the candidate it runs, with the `external`
  configuration its harness declared, so a lease that named no candidate
  still says what to run.
- `task` and `instruction` say what to ask, `outputs` lists the files
  the task keeps, and `timeout` is the task's time limit.
- `lux_labels` is the value for the `Lux-Labels` header of the trial's
  model calls.
- `max_turns` appears when the task sets it.
- `model_key` sits beside `trials` when the installation has a key
  endpoint: the experiment's own model Key, drawn on its Budget. The
  answer then carries `Cache-Control: no-store`.

A lease answers `204` with no body while nothing may be leased now. Ask
again after a moment. A lease answers `409 experiment_finished` once the
experiment is `complete` or `canceled`, and `409 wrong_mode` on an
experiment in mode `import`. A candidate the experiment does not hold is
`422 not_in_experiment`.

Renew a lease before it expires with the lease id alone. The answer is
the lease again with a new `expires_at` and the trials it still holds:

```sh
api -X POST $EXAMEN/experiments/$EXP/work/lease -d "{\"renew\": \"$LEASE\"}"
```

`renew` cannot be combined with `candidate` or `max`. A lease that has
lapsed is `409 lease_expired`, and its trials return to pending for
another lease. A lease that holds no trial any more, because every one
was submitted or canceled, is `409 lease_not_held`.

Submit each trial under its lease:

```sh
api -X POST $EXAMEN/trials/$TRIAL/submit -d "{
  \"lease\": \"$LEASE\", \"outcome\": \"completed\",
  \"started_at\": \"2026-11-07T01:01:02Z\", \"agent_ended_at\": \"2026-11-07T01:01:09Z\",
  \"measurements\": [{\"metric\": \"$METRIC\", \"value\": true}]}"
```

```json
{
  "id": "tri_01m6srbm5mhpyrp6agsp5432j4",
  "state": "done",
  "outcome": "completed",
  "observed": { "cost_state": "unknown", "duration": 7 },
  "evidence": [],
  "imported": false
}
```

- `lease`, `outcome`, `started_at` and `agent_ended_at` are required.
  `outcome` is any of `completed`, `declined`, `timed_out`, `turn_limit`,
  `over_budget` and `infra_failed`; `canceled` is only Examen's.
- `infra_failed` and `over_budget` also need `failure: {code, detail}`,
  and no other outcome may carry one. A trial that fails for
  infrastructure reasons is retried, and a cell whose smoke keeps
  failing is parked (see [retry a parked cell](#retry-a-parked-cell)).
- `measurements` are values the harness computed itself, of metrics the
  suite lists at trial level and no grader produces for the task. They
  are stored with `self_reported: true`.
- `outputs` names files the task keeps, each as
  `{"path": "answer.txt", "arca": {"path": "...", "digest": "sha256:..."}}`,
  an object the harness wrote into the owner's Arca space. Examen
  records the reference and does not read the file. A task with `inputs`
  is refused when an `execute` experiment is created, because this
  release delivers no input links.
- `declared` (`harness_version`, `image`), `ref`, `lux_requests`,
  `service_versions` and `service_usage` record provenance. A
  `harness_version` that differs from the candidate's `version` is
  `422 provenance_mismatch`.

Submitting the same lease and body again answers `200` with the trial and
changes nothing. A submit under a lease that does not hold the trial is
`409 lease_not_held`, and under a lapsed one `409 lease_expired`.

A harness loop repeats lease, run, submit until the experiment is
`complete`. [Running it locally](local.md#be-the-harness) has the loop as
a shell script.

### Import runs of a past study

An experiment created with `"mode": "import"` creates no cells and takes
no leases. Each import adds one finished run, with the times it really
had, to the cell of its task and candidate, which the first import makes:

```sh
TASK2=$(api -X PUT $EXAMEN/tasks/capital-of-spain -d '{
  "instruction": "What is the capital of Spain? Write the city name to answer.txt.",
  "outputs": ["answer.txt"], "timeout": "5m"}' | jq -r .digest)
SUITE2=$(api -X PUT $EXAMEN/suites/geography-2025 -d "{\"tasks\": [\"$TASK2\"],
  \"metrics\": [\"$METRIC\", \"builtin:completed\", \"builtin:duration\"],
  \"primary_metric\": \"$METRIC\"}" | jq -r .digest)

IMP=$(api -X POST $EXAMEN/experiments -d "{
  \"suite\": \"$SUITE2\", \"candidates\": [\"$A\", \"$B\"], \"trials\": 5,
  \"mode\": \"import\", \"budget\": {\"name\": \"local\"}}" | jq -r .id)

api -X POST $EXAMEN/experiments/$IMP/imports -H 'Idempotency-Key: import-a-0' -d "{
  \"executor\": \"external\", \"task\": \"$TASK2\", \"candidate\": \"$A\",
  \"outcome\": \"completed\",
  \"started_at\": \"2026-03-02T14:01:00Z\", \"agent_ended_at\": \"2026-03-02T14:09:12Z\",
  \"ended_at\": \"2026-03-02T14:09:30Z\",
  \"declared\": {\"harness_version\": \"1.0.0\", \"image\": \"sandbox-agent:2026-02-27\"},
  \"outputs\": [{\"path\": \"answer.txt\", \"arca\": {\"path\": \"studies/geo/run-0/answer.txt\",
    \"digest\": \"sha256:1111111111111111111111111111111111111111111111111111111111111111\"}}],
  \"measurements\": [{\"metric\": \"$METRIC\", \"value\": true}],
  \"ref\": \"results/geo/a-0\"}"
```

The answer is `201` with the trial:

```json
{
  "id": "tri_01m6srcszgx2f8n3bhm80trqjv",
  "index": 0,
  "executor_ref": "results/geo/a-0",
  "state": "done",
  "outcome": "completed",
  "declared": { "harness_version": "1.0.0", "image": "sandbox-agent:2026-02-27" },
  "evidence": [{ "kind": "output", "name": "answer.txt", "path": "studies/geo/run-0/answer.txt", "digest": "sha256:1111..." }],
  "imported": true,
  "started_at": "2026-03-02T14:01:00Z",
  "ended_at": "2026-03-02T14:09:30Z"
}
```

The task names `answer.txt` as an output, so an import may carry it. The body is a submit body without
`lease` and with `executor` (`external`), `task`, `candidate` and
`ended_at`. The three times may lie in the past, each no later than the
next, and `ended_at` no later than the request. `declared.harness_version`
is required and equals the candidate's `version`. A `ref` already
imported into the experiment is `409 already_imported`; without a `ref`,
two imports of one past run are two trials. A cell takes the experiment's
`trials` imports and then answers `409 cell_full`.

An import into an `execute` experiment is `409 wrong_mode`. The executors
`topos` and `cella` read finished sessions and sandboxes from those
services and are refused `422 core_not_configured`.

An imported experiment stays open for more imports until
`POST /experiments/{id}/close` closes its intake; it then completes and
an import answers `409 experiment_finished`. The closed experiment's
comparisons are computed as for any other.

### Read results

The grid holds each cell's summary of one metric, the suite's primary
metric unless `metric` names another, with a 95% interval:

```sh
api $EXAMEN/experiments/$EXP/grid | jq '.items[].cells[] | {state, summary: (.summary | {n, value, interval})}'
```

```json
{ "state": "complete", "summary": { "n": 10, "value": 0.5, "interval": [0.2366, 0.7634] } }
{ "state": "complete", "summary": { "n": 10, "value": 0.9, "interval": [0.5958, 0.9821] } }
```

Each cell also reports its `trials` counts and, for a candidate other
than the reference, the id of its `comparison` and the task's `verdict`
and `reason`. The grid takes `task`, `candidate`, `state`, `metric` and
`comparison` filters, and pages by task.

Comparisons are recomputed as trials arrive. Read them until `final` is
`true`:

```sh
until [ "$(api $EXAMEN/experiments/$EXP/comparisons | jq -r '.items[0].final')" = true ]; do sleep 1; done
api $EXAMEN/experiments/$EXP/comparisons | jq '.items[0] | {id, automatic, exploratory, final, confounds, results: [.results[] | {level, metric_name, delta, p, q, verdict}]}'
```

```json
{
  "id": "cmp_01m6srbm5h51jpfjazvcgm7fwn",
  "automatic": true,
  "exploratory": false,
  "final": true,
  "confounds": ["self_reported"],
  "results": [
    { "level": "suite", "metric_name": "passed", "delta": 0.4, "p": 0.1409, "q": null, "verdict": null },
    { "level": "suite", "metric_name": "completed", "delta": 0, "p": 1, "q": 1, "verdict": "within_noise" }
  ]
}
```

A comparison answers its suite-level and candidate-level results.
`GET /comparisons/{id}/rows` pages the task-level ones and takes `task`,
`metric` and `verdict`. A result that reads values a harness reported
itself keeps its `p`, has no `q` and no `verdict`, and says so in
`reason`; [Concepts](concepts.md#the-analysis) explains why. The list
`GET /comparisons` crosses experiments and takes `experiment`, `suite`,
`candidate`, `baseline`, `automatic`, `declared`, `exploratory`, `final`
and `single_variable`.

To ask for a comparison nobody declared, `POST /comparisons` names the
`experiment`, a `candidate` and exactly one reference: a
`reference_candidate`, a `baseline` id, or a `reference_task` with
`"variable": "task"`. The answer is `201` and the comparison is
`exploratory: true`, which the analysis never headlines. It takes an
`Idempotency-Key`.

```sh
api -X POST $EXAMEN/comparisons -d "{\"experiment\": \"$DEC\",
  \"candidate\": \"$C\", \"reference_candidate\": \"$A\"}"
```

Trials and their evidence:

```sh
api "$EXAMEN/experiments/$EXP/trials?candidate=$B&metric=passed&value=false" | jq '.items[] | {index, outcome, values}'
```

```json
{ "index": 3, "outcome": "completed", "values": { "passed": false, "completed": true, "duration": 0 } }
```

`GET /experiments/{id}/trials` takes `cell`, `task`, `candidate`,
`state`, `outcome`, `imported` and `retried`, and `metric` with `value`
(`true`, `false`, a number, a label or `missing`) together. A trial's
`evidence` lists what was kept about it, each entry with its `kind`,
`name`, `path` and `digest`: the outputs a harness submitted or imported
as Arca references, and, for graded trials, check outputs. A trial's
`checks`, `judgments` and `outputs` members hold the grading records.
`GET /trials/{id}/measurements` and `GET /experiments/{id}/measurements`
list every value with its `grader`, `generation` and `self_reported`;
`generation=all` includes the values later gradings replaced.

### Declared comparisons

An experiment can declare comparisons before any trial runs, so they are
tested as one corrected family rather than picked after seeing the
results. `comparisons` on create takes up to the declared-comparison
bound of the [fixed bounds](#fixed-bounds), in one of three forms:

```sh
C=$(api -X PUT $EXAMEN/candidates/my-agent-v3 -d '{"kind": "external",
  "external": {"harness": "my-harness", "version": "1.0.0", "flags": {"prompt": "chain-of-thought"}}}' | jq -r .digest)
SUITE3=$(api -X PUT $EXAMEN/suites/geography-both -d "{\"tasks\": [\"$TASK\", \"$TASK2\"],
  \"metrics\": [\"$METRIC\", \"builtin:completed\", \"builtin:duration\"],
  \"primary_metric\": \"$METRIC\"}" | jq -r .digest)

DEC=$(api -X POST $EXAMEN/experiments -H 'Idempotency-Key: decl-1' -d "{
  \"suite\": \"$SUITE3\", \"candidates\": [\"$A\", \"$B\", \"$C\"], \"trials\": 6,
  \"budget\": {\"name\": \"local\"},
  \"comparisons\": [
    {\"candidate\": \"$B\", \"reference_candidate\": \"$C\"},
    {\"candidate\": \"$A\", \"task\": \"capital-of-france\", \"reference_task\": \"capital-of-spain\"},
    {\"variable\": \"task\", \"levels\": [\"capital-of-france\", \"capital-of-spain\"],
     \"where\": {\"candidate\": \"$C\"}}]}" | jq -r .id)
```

- A pair of candidates: `candidate` and `reference_candidate`, with an
  optional `variable` naming the one logical variable they differ in.
- One candidate's pair of tasks: `candidate`, `task` and
  `reference_task`, by task name or digest.
- The general form: a cell `variable`, its two `levels` with the
  candidate side first, and optional `where` and `by` to select and
  stratify cells.

Each form takes an optional `metric` of the suite. The refusals name the
entry, for example
`comparisons[0].reference_candidate: a declared comparison compares two different candidates`
(`400 invalid_field`).

Before the first trial finishes, `GET /experiments/{id}/declared`
answers each entry as not computed yet. After the experiment completes it
answers the family:

```sh
api $EXAMEN/experiments/$DEC/declared | jq '{final, family, comparisons: [.comparisons[] | {index, comparison, p, final}]}'
```

```json
{
  "final": true,
  "family": { "method": "benjamini_hochberg", "size": 3, "level": 0.05 },
  "comparisons": [
    { "index": 0, "comparison": "cmp_01m6sre58zc9bdkmrfs7g1kjvy", "p": 0.5277, "final": true },
    { "index": 1, "comparison": "cmp_01m6sre58zc9bdkmrfs7g1kjvz", "p": 1, "final": true },
    { "index": 2, "comparison": "cmp_01m6sre58zc9bdkmrfs7g1kjw0", "p": 1, "final": true }
  ]
}
```

Each entry links its comparison by id. Entries over self-reported values
carry a `p` and no `q`, as in the table above. `GET
/experiments/{id}/comparisons?declared=true` lists the declared
comparisons, each with its position in `declared`.

### Retry a parked cell

In a running experiment `$EXP2`, a cell whose smoke trial keeps failing
for infrastructure reasons, or whose Budget ran out, parks in state
`blocked` or `over_budget` with a `parked_reason`. Nothing runs in it
until its owner retries it:

```sh
api $EXAMEN/experiments/$EXP2/cells | jq '.items[] | {id, state, parked_reason}'
```

```json
{ "id": "cel_01m6srey1ftvfd0jc21bzjvbpj", "state": "blocked", "parked_reason": "smoke_infra_failed: tri_01m6srez4jm9kvbgd1a0a29zqn: harness_crashed" }
```

```sh
api -X POST $EXAMEN/cells/$CELL/retry | jq '{id, state}'
```

```json
{ "id": "cel_01m6srey1ftvfd0jc21bzjvbpj", "state": "smoking" }
```

The cell deals a new smoke trial, which a harness leases as before. A
cell that is not parked is `409 cell_not_parked`, with its state in
`details.state`.

### Cancel an experiment

```sh
api -X POST $EXAMEN/experiments/$EXP2/cancel | jq '{state, progress}'
```

```json
{
  "state": "canceled",
  "progress": { "cells": { "canceled": 2 }, "trials": { "done": 5, "retried": 3 }, "outcomes": { "canceled": 2, "infra_failed": 3 } }
}
```

Trials not yet finished end with the outcome `canceled`, and a harness
still holding one finds its submit answered `409 lease_not_held`. Cancel
a second time answers `200` and changes nothing. A lease on a canceled
experiment is `409 experiment_finished`.

## Errors

Every error has one shape:

```json
{
  "error": {
    "code": "cell_not_parked",
    "message": "This cell is not waiting to be retried.",
    "details": {
      "detail": "retry: the cell is smoking, not parked blocked or over_budget",
      "state": "smoking",
      "retryable": false
    }
  }
}
```

Branch on `code`. The `message` is one fixed sentence per code, written
for a person, and `details` is for a developer: `detail` says what
happened, `field` names the member, parameter or header at fault,
`retryable` says whether the same request may succeed later, and some
errors add `constant` (the limit exceeded), `state` (the object's state),
`reason`, `figure`, `fields` or `core` (the service involved). Every
`429` carries `Retry-After` in whole seconds.

The codes a client branches on:

| Status | Code | What to do |
|---|---|---|
| 400 | `invalid_request` | The body is not one JSON object of the right types. Fix the request |
| 400 | `unknown_field` | The body has a member the contract does not describe, named in `details.field`. Remove it |
| 400 | `missing_field` | A required member is absent, named in `details.field` |
| 400 | `invalid_field` | A value is not allowed, named in `details.field`, such as `limit`, `outcome` or a header |
| 400 | `invalid_cursor` | The cursor is not from this list. Start again from the first page |
| 401 | `missing_token` | Send `Authorization: Bearer <token>` |
| 401 | `invalid_token` | Get a fresh token; `details.reason` says why this one was refused, and `retryable` whether trying again may help |
| 403 | `forbidden` | The authorizer denied the request or an authorizer limit applies; `details.reason` names it, and `details.figure` the limit's figure. See [authorizer limits](#limits-an-authorizer-sets) |
| 403 | `sign_in_not_kept` | The installation keeps no sign-in for this caller for the work the experiment runs later. Start it from a client that allows it |
| 404 | `not_found` | No route serves this path and method |
| 404 | `metric_not_found`, `grader_not_found`, `task_not_found`, `suite_not_found`, `candidate_not_found`, `experiment_not_found`, `cell_not_found`, `trial_not_found`, `measurement_not_found`, `baseline_not_found`, `comparison_not_found`, `taxonomy_not_found` | The object does not exist or the caller may not see it; the two cannot be told apart. Check the id and the owner |
| 409 | `experiment_finished` | The experiment is `complete` or `canceled`. Stop leasing, submitting or importing; `details.state` says which |
| 409 | `experiment_not_complete` | Pinning a baseline needs a complete experiment. Retryable once it completes |
| 409 | `cell_not_parked` | A retry of a cell that is not parked. Read the cell |
| 409 | `wrong_mode` | A lease on an `import` experiment, or an import into an `execute` one |
| 409 | `cell_full` | The cell already holds the experiment's `trials` trials |
| 409 | `already_imported` | A run with this `ref` is already a trial of the experiment. Skip it |
| 409 | `lease_expired` | The lease lapsed. Lease again; the trial returned to pending |
| 409 | `lease_not_held` | The trial is not held under this lease by this caller, or it was submitted or canceled. Lease again |
| 409 | `idempotency_key_reused` | The key was first used with another body or route. Use a new key |
| 409 | `request_in_progress` | The first request with this key is still running. Retry shortly |
| 412 | `precondition_failed` | `If-Match` or `If-None-Match` does not hold. Read the object again |
| 413 | `request_too_large` | The body is above the request bound |
| 422 | `unpinned_reference` | A reference names no exact version. Use a digest or `name@sha256:...` |
| 422 | `reference_not_found` | A digest or id in the body names nothing the caller may use, in `details.field` |
| 422 | `limit_exceeded` | The request is above a [fixed bound](#fixed-bounds); `details.constant` names it |
| 422 | `suite_mismatch` | A baseline or experiment belongs to another suite than the request's |
| 422 | `not_in_experiment` | The task or candidate named is not part of the experiment |
| 422 | `provenance_mismatch` | A run's declared `harness_version` differs from its candidate's `version`; `details.fields` lists the fields |
| 422 | `budget_refused` | The key endpoint refused the Budget named. Name another or ask the operator |
| 422 | `core_not_configured` | The installation lacks a service the request needs, in `details.core`. See [what each service adds](configuration.md#what-each-service-adds) |
| 422 | `executor_unavailable` | This kind of candidate or grader cannot run in this release |
| 422 | `unresolved_reference` | Topos holds no version of the agent a `topos` candidate pins |
| 422 | `credential_audience_reserved` | A `program` candidate asks for a credential for a service Examen acts through |
| 429 | `rate_limited` | Wait `Retry-After` seconds |
| 429 | `too_many_streams` | The caller holds the most progress streams. Close one and retry |
| 500 | `internal` | Retry later; report the `X-Request-Id` if it persists |
| 502 | `core_unavailable` | A service Examen depends on did not answer. Retry later |
| 503 | `authorizer_unavailable` | Permissions cannot be checked now. Retry later |
| 503 | `owner_tokens_unavailable` | The installation cannot act for the owner now. Retry later |
| 503 | `unavailable` | The database or an idempotency key could not be served. Retry later |

`details.retryable` says whether the same request may succeed later
unchanged. Retry a `429` after `Retry-After` and a `5xx` with backoff;
a retry of a request that carried an `Idempotency-Key` is safe.

## Limits

### Limits an authorizer sets

An installation with an authorizer can hold a caller below Examen's own
bounds: the highest parallelism, the most trials in an experiment, the
most active experiments, the executors and judge models allowed, and the
Budget an experiment is metered against. A request above one answers
`403 forbidden` with `details.reason` naming the limit (`max_parallelism`,
`max_trials`, `max_active_experiments`, `executors` or `budget`), the
member in `details.field` and the limit's figure in `details.figure`. An
authorizer narrows the bounds below and never widens them. The limits
and how an authorizer answers them are in
[Writing an authorizer](authorizer.md#the-answer). Without an
authorizer none of these apply.

### Bounds an installation sets

These come from the installation's settings, not from the request:

| Bound | Where it is set |
|---|---|
| Which issuers and audiences are accepted | [`EXAMEN_OIDC_ISSUERS`, `EXAMEN_OIDC_AUDIENCE`](configuration.md#every-variable) |
| Where the API is served | [`EXAMEN_BASE_PATH`](configuration.md#every-variable) |
| How many work items one replica runs at once | [`EXAMEN_WORKERS`](configuration.md#every-variable) |
| Which proxies may name the client address | [`EXAMEN_TRUSTED_PROXIES`](configuration.md#every-variable) |

### Fixed bounds

These are fixed in the code of the release and are not settings.

| Bound | Value | What happens above it |
|---|---|---|
| Request body | 4 MiB | `413 request_too_large`, refused from `Content-Length` before the body is read |
| Requests per minute | 600 per caller, and 6,000 per client address counted before the token is read, to one replica | `429 rate_limited` with `Retry-After` |
| Requests per minute to `<base path>/version` and `<base path>/openapi.yaml` | 120 per client address to one replica | `429 rate_limited` with `Retry-After` |
| Time to send a request | 10 seconds for the headers, 1 minute for the whole request | The connection is closed, or `400 invalid_request` for a body cut short |
| List page | 50 items by default, 200 at most | `400 invalid_field` on `limit` |
| Trials per cell | 10 by default, 1000 at most | `422 limit_exceeded`, `MAX_TRIALS_PER_CELL` |
| Cells per experiment | 2000 (tasks times candidates) | `422 limit_exceeded`, `MAX_CELLS_PER_EXPERIMENT` |
| Candidates per experiment | 32 | `422 limit_exceeded`, `MAX_CANDIDATES_PER_EXPERIMENT` |
| Declared comparisons per experiment | 128 | `422 limit_exceeded`, `MAX_DECLARED_COMPARISONS` |
| Active experiments per owner | 16 (neither complete nor canceled) | `422 limit_exceeded`, `MAX_ACTIVE_EXPERIMENTS_PER_OWNER` |
| Parallelism | 8 by default, 256 at most | `422 limit_exceeded`, `MAX_PARALLELISM` |
| Tasks per suite | 1000 | `422 limit_exceeded`, `MAX_TASKS_PER_SUITE` |
| Metrics per suite | 64 | `422 limit_exceeded`, `MAX_METRICS_PER_SUITE` |
| Metadata pairs on an object | 16 | `422 limit_exceeded`, `METADATA_MAX_PAIRS` |
| Task factors | 16 | `422 limit_exceeded`, `MAX_TASK_FACTORS` |
| Task timeout | 30 minutes by default, 24 hours at most | `422 limit_exceeded`, `MAX_TRIAL_TIMEOUT` |
| Inline text of a task file | 64 KiB | `422 limit_exceeded`, `MAX_INLINE_INPUT_BYTES` |
| Infrastructure retries of a trial | 2 | The trial's cell parks |
| Valued trials for a verdict | 5 per task side | The result is `insufficient` |
| External lease | 10 minutes, from the lease or the last renewal | `409 lease_expired` |
| Idempotency key | 1 to 255 bytes of visible ASCII, kept 24 hours | `400 invalid_field` |
| `X-Request-Id` a client sends | 128 bytes of visible ASCII | Ignored, and Examen mints one |
| Time to serve a request with an idempotency key | 1 minute | `503 unavailable` |
| Progress streams | 10 per caller on one replica | `429 too_many_streams` |
| Stream | polled every second, a keepalive after 15 seconds quiet | None |
| `sign_in` on a create | 255 bytes of visible ASCII | `400 invalid_field` |
| A submit's `ref` | 255 characters | `400 invalid_field` |
| A submitted rationale | 4 KiB | `422 limit_exceeded`, `JUDGE_MAX_RATIONALE_BYTES` |
| A service name, version or unit in a submit | 63 characters | `400 invalid_field` |

The request rates are counted per replica. Nothing bounds the trials of an
experiment as a whole beyond cells times `trials` per cell; an
authorizer's `max_trials` can.

## The full reference

- [`api/openapi.yaml`](../api/openapi.yaml) lists every route, request,
  response and error, and a running server serves the same document at
  `<base path>/openapi.yaml`.
- [`specs/003-api.md`](../specs/003-api.md) is the design of the API and
  the reasoning behind it, including routes this release does not serve.
