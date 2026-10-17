---
title: "Identity and authorization: verifying a caller, the action vocabulary, the authorizer with its limits and filters, the built-in owner policy, sharing, and acting as the owner toward Topos, Cella, Lux, Arca and Origo"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
affects: [authorizer/, internal/access/, internal/owner/, internal/httpapi/, internal/execute/]
effort: large
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Identity and authorization

## Overview

Examen answers three questions about its work and decides none of them
alone. Who is calling is settled by verifying a bearer token against a
configured OpenID Connect issuer. What the caller may do is asked of an
authorizer, an HTTP endpoint the operator writes on the contract of
`latere.ai/x/pkg/authz`, which also returns the limits a request is held
to; with no authorizer configured, a built-in policy lets an owner act
on what it owns. Whose identity a trial's work carries is always the
experiment's owner: every request Examen sends to a core for that work
carries a short-lived token for the owner, which Examen obtains from a
minter the operator provides or, on an installation run by one owner,
receives from files that owner keeps. Examen never uses an identity of
its own toward a core. It holds no accounts, roles or plans.

## Design

### Verification

`latere.ai/x/pkg/authkit/jwt` verifies every bearer: its signature
against the issuer's published keys, `iss` among `EXAMEN_OIDC_ISSUERS`,
`aud` among `EXAMEN_OIDC_AUDIENCE` (a comma list, the first entry
primary, `examen` when unset), and `exp`, `nbf` and `iat` within the
verifier's own skew and age bounds. The subject is rendered once, by
`authz.Subject`, as `<iss>|<sub>`, and is what an object's `owner` and
`created_by`, a share's `grantee` and the authorizer's question carry.
Two issuers that agree on a `sub` are two subjects.

A request with no bearer is `401 missing_token`. A bearer that is
refused is `401 invalid_token`, with the verifier's word for why,
`jwt.ReasonOf`, as `details.reason`; `retryable` is true for
`issuer_unavailable` alone. No part of a token is ever in an error, a
log line, a span, an event or a database row.

The verifier reads each issuer's discovery document and key set and
calls the issuer for nothing else. `examend serve` warms it at start; an
issuer that does not answer then is named, the server starts, and its
keys are read when its first token arrives. A server that lists no
issuer does not start, and the error names the variable (spec 011).

A token may carry grants, the actions its holder narrowed it to, in
`authorization_details`. The verifier admits such a token and hands the
grants on with every other claim; they narrow a decision where it is
made: an authorizer written on `latere.ai/x/pkg/authz/server` narrows by
them, and so does the owner policy, with `authz.Restrict`.

Examen reads no claim for meaning: no role, no organization, no scope.
Whose context a request acts in is the authorizer's answer (below), not
a claim.

### Owners and subjects

An owner is a subject or an organization, written as the authorizer
names it. A request acts in an owner context, which spec 003 fixes: the
owner the request names, else the owner the allow's `owner` limit names,
else the caller's subject. Under the owner policy the context is always
the caller's subject, so every owner is a person.

Examen records who acted beside whose an object is: an experiment, a
dataset export, a baseline record, a share and a bar evaluation carry
`created_by`, the subject that created it, and an annotation's
measurements carry `annotator`.
Unattended work for an experiment acts for its `owner` on behalf of its
`created_by` (below).

### The vocabulary

The vocabulary is published as Go constants in the public package
`authorizer`, with `Vocabulary()`, an `authz.Vocabulary` built by
`authz.NewVocabulary` whose core is `examen`, `Fields` for the fields
each action sends, and `SendsID` for whether it sends the id of a stored
object, so an authorizer validates against the same table the client
checks before the wire. Each action names one kind.

| Kind | Action | Asked by (spec 003) | Resource fields sent |
|---|---|---|---|
| `Metric` | `metric.apply` | `PUT /metrics/{name}` | `owner`, `name`, `digest`, `metric_type` |
| `Metric` | `metric.read` | `GET /metrics/{ref}`, `GET /metrics/{name}/versions` | id; `owner`, `name`, `digest`, `shared`, `pinned` |
| `Metric` | `metric.list` | `GET /metrics` | `owner` |
| `Grader` | `grader.apply` | `PUT /graders/{name}` | `owner`, `name`, `digest`, `grader_kind`, `model`, `agent` |
| `Grader` | `grader.read` | `GET /graders/{ref}`, `GET /graders/{name}/versions`, `GET /graders/{ref}/calibration` | id; `owner`, `name`, `digest`, `grader_kind`, `shared`, `pinned` |
| `Grader` | `grader.list` | `GET /graders` | `owner` |
| `Task` | `task.apply` | `PUT /tasks/{name}` | `owner`, `name`, `digest`, `workspace_kind`, `repository`, `commit`, `path`, `image` |
| `Task` | `task.read` | `GET /tasks/{ref}`, `GET /tasks/{name}/versions` | id; `owner`, `name`, `digest`, `shared`, `pinned` |
| `Task` | `task.list` | `GET /tasks` | `owner` |
| `Suite` | `suite.apply` | `PUT /suites/{name}` | `owner`, `name`, `digest`, `task_count`, `metric_count` |
| `Suite` | `suite.read` | `GET /suites/{ref}`, `GET /suites/{name}/versions` | id; `owner`, `name`, `digest`, `shared`, `pinned` |
| `Suite` | `suite.list` | `GET /suites` | `owner` |
| `Suite` | `suite.share` | `PUT`, `DELETE` and `GET` of `/suites/{suite}/shares` | `owner`, `name`, `grantee` |
| `Suite` | `suite.use` | `POST /experiments` naming another owner's suite; `PUT` of a bar or a baseline on another owner's suite | id; `owner`, `name`, `digest`, `for_owner`, `shared` |
| `Candidate` | `candidate.apply` | `PUT /candidates/{name}` | `owner`, `name`, `digest`, `candidate_kind`, `agent`, `image`, `model`, `reasoning` |
| `Candidate` | `candidate.read` | `GET /candidates/{ref}`, `GET /candidates/{name}/versions` | id; `owner`, `name`, `digest`, `candidate_kind` |
| `Candidate` | `candidate.list` | `GET /candidates` | `owner` |
| `Experiment` | `experiment.create` | `POST /experiments` | `owner`, `suite`, `suite_owner`, `candidates`, `executors`, `models`, `judge_models`, `cells`, `trials`, `trials_total`, `parallelism`, `budget`, `baseline`, `baseline_owner`, `mode`, `adopt` |
| `Experiment` | `experiment.read` | `GET /experiments/{id}` and every route below it that reads, `GET /cells/{id}`, `GET /trials/{id}`, `GET /measurements/{id}`, `GET /comparisons/{id}` with its rows and its view, `POST /comparisons`; and the experiment each export, baseline pin or bar evaluation names | id; `owner`, `suite`, `state`, `mode`, `shared` |
| `Experiment` | `experiment.list` | `GET /experiments`, `GET /comparisons` | `owner` |
| `Experiment` | `experiment.update` | `PATCH /experiments/{id}`, `POST /experiments/{id}/close`, `POST /cells/{id}/gate` | id; `owner`, `suite`, `state`, `mode`, and what it changes: `trials` with `trials_total`, `parallelism`, `baseline` with `baseline_owner`, or the `cell` it gates |
| `Experiment` | `experiment.cancel` | `POST /experiments/{id}/cancel` | id; `owner`, `suite`, `state` |
| `Experiment` | `experiment.delete` | `DELETE /experiments/{id}` | id; `owner`, `suite`, `state`, `evidence` |
| `Experiment` | `experiment.import` | `POST /experiments/{id}/imports` | id; `owner`, `suite`, `state`, `mode`, `task`, `candidate`, `executor`, `executor_ref` |
| `Experiment` | `experiment.regrade` | `POST /experiments/{id}/regrade` | id; `owner`, `suite`, `state`, `grader`, `judge_models` |
| `Experiment` | `experiment.share` | `PUT`, `DELETE` and `GET` of `/experiments/{id}/shares` | id; `owner`, `grantee` |
| `Experiment` | `experiment.run` | Examen itself, before unattended work (below) | id; `owner`, `created_by`, `purpose`, `cell`, `executor`, `budget` |
| `Baseline` | `baseline.pin` | `PUT /suites/{suite}/baselines/{name}` | `owner`, `suite`, `suite_owner`, `name`, `experiment`, `candidate` |
| `Baseline` | `baseline.read` | `GET` of a baseline, its history, and `GET /baselines/{id}` | id; `owner`, `suite`, `suite_owner`, `name`, `shared` |
| `Baseline` | `baseline.list` | `GET /suites/{suite}/baselines` | `owner`, `suite`, `suite_owner` |
| `Baseline` | `baseline.share` | `PUT`, `DELETE` and `GET` of `/suites/{suite}/baselines/{name}/shares` | `owner`, `suite`, `suite_owner`, `name`, `grantee` |
| `Baseline` | `baseline.use` | `POST /experiments`, `PATCH /experiments/{id}` and `POST /comparisons` naming another owner's baseline | id; `owner`, `suite`, `suite_owner`, `name`, `for_owner`, `shared` |
| `Bar` | `bar.apply` | `PUT /suites/{suite}/bars/{name}` | `owner`, `suite`, `suite_owner`, `name`, `digest` |
| `Bar` | `bar.read` | `GET` of a bar, its versions, `GET /bars/{id}`, the evaluation lists, `GET /evaluations/{id}` | id; `owner`, `suite`, `suite_owner`, `name`, `digest` |
| `Bar` | `bar.list` | `GET /suites/{suite}/bars` | `owner`, `suite`, `suite_owner` |
| `Bar` | `bar.evaluate` | `POST /suites/{suite}/bars/{ref}/evaluate` | id; `owner`, `suite`, `suite_owner`, `name`, `digest`, `experiment`, `comparison` |
| `Export` | `export.create` | `POST /exports` | `owner`, `experiments`, `all_generations`, `evidence` |
| `Export` | `export.read` | `GET /exports/{id}` | id; `owner`, `state` |
| `Export` | `export.list` | `GET /exports` | `owner` |
| `Annotation` | `annotation.list` | `GET /annotations` | `owner`, `grader` |
| `Annotation` | `annotation.submit` | `POST /annotations`, `POST /annotations/claim`, `POST /annotations/skip` | `owner`, `grader`, `trial`, `experiment`, `regrade` |
| `Annotation` | `annotation.queue` | `POST /annotations/queue` | `owner`, `grader`, `experiment` |
| `Report` | `report.create` | `POST /experiments/{id}/reports` | `owner`, `experiment` |
| `Report` | `report.read` | `GET /reports/{id}` | id; `owner`, `experiment`, `shared` |
| `Report` | `report.update` | `POST /reports/{id}/draft`, `PUT /reports/{id}/narrative`, `POST /reports/{id}/exports` | id; `owner`, `experiment`, `model` |
| `Report` | `report.release` | `POST /reports/{id}/release` | id; `owner`, `experiment` |
| `Report` | `report.share` | `PUT`, `DELETE` and `GET` of `/reports/{id}/shares` | id; `owner`, `grantee` |
| `Share` | `share.list` | `GET /shares` | `grantee` |

`GET /openapi.yaml` asks nothing: it is a public document.

The shared envelope keeps two names for the resource itself:
`resource.kind`, the kind of the table's first column, and
`resource.id`, the id of a stored object, written "id" in the table. It
drops a field of either name on the wire, so no field takes one: an
object's own `type` or `kind` is sent as `metric_type`, `grader_kind`,
`candidate_kind` or `workspace_kind`.

| Field | Meaning |
|---|---|
| `owner` | the object's owner; on an apply, a create, a pin or a list, the owner the request names, left out when it names none, which means the caller's context. On `annotation.submit`, the trial's owner |
| `name`, `digest` | the object's name and digest (spec 002); on a baseline or bar, `suite` is the suite's name |
| `suite_owner` | on a baseline or bar: the owner of its suite, which differs from `owner` when the suite is shared with it |
| `shared` | on a read or use of another owner's object: whether a share record names the context owner for it, or for a suite or baseline that pins it |
| `pinned` | on a read of another owner's task, metric, grader or suite: whether an experiment of the context owner pins it |
| `for_owner` | the owner that would use another owner's suite or baseline: the experiment's, baseline's or bar's owner |
| `metric_type`, `grader_kind`, `candidate_kind`, `workspace_kind` | the object's `type`, `kind`, or workspace kind (`none`, `origo`, `arca`) |
| `model`, `agent`, `image`, `reasoning` | a grader's judge model or judge agent; a candidate's pinned agent version or image, its model and its reasoning level; on `report.update`, the model a narrative draft calls |
| `repository`, `commit`, `path`, `image` on a task | the workspace's Origo repository and commit, or Arca path; the environment's image digest |
| `task_count`, `metric_count` | how many tasks and metrics a suite version pins |
| `suite`, `suite_owner` | the experiment's suite digest and its owner |
| `candidates`, `models`, `judge_models`, `executors` | the candidate digests; every model the candidates name; every model the suite's model judges name; the executors the candidates need, `topos` or `cella` |
| `cells`, `trials`, `trials_total` | tasks × candidates; N; cells × N |
| `baseline`, `baseline_owner` | the baseline's id and its owner |
| `purpose`, `cell`, `executor` | on `experiment.run`: the work about to be done (below), its cell, and its executor |
| `grader` | on a re-grade and an annotation action: the grader version's digest |
| `experiment` | on a report or annotation action: the experiment's id |
| `experiments`, `all_generations`, `evidence` | on `export.create`: the experiments its filter names, and its `all_generations` and `evidence` (spec 002); on `experiment.delete`, `evidence` is the parameter of spec 007 |
| `grantee` | the owner a share names; on `share.list`, the context owner |

A member Examen does not know for a request is left out, never sent
empty.

### The question

The shared contract, `latere.ai/x/pkg/authz`: Examen posts the subject,
its issuer and `sub` apart, every verified claim verbatim, the action
and the resource, with a bearer of its own, and receives `{allow,
reason, ttl, limits, filter}`. The request also carries what is known of
the HTTP request: its id, the peer address and the user agent. Examen
fills no `workload` member.

The endpoint is `EXAMEN_AUTHORIZER_URL`. Its bearer is the content of
the file `EXAMEN_AUTHORIZER_TOKEN_FILE` names, read once at start and
never logged; a URL with no readable, non-empty token file stops the
start, naming the variable.

The client is `authz.Client`, with Examen's vocabulary, so an action
outside it fails before the wire. Its rules are the contract's: an allow
is cached for its `ttl`, `authz.DefaultTTL` when the answer names none
and `authz.MaxTTL` at most; a deny for `authz.DenyTTL`; an unavailable
answer never. One retry follows a connection failure, and the whole
call is bounded by `authz.Timeout`. The cache is keyed by the whole
question, every claim included, except the request id. A question with
no id is never cached, so every apply, create, pin, share, list,
submit and export is asked each time. A change at the authorizer reaches
a caller within the `ttl` the authorizer chose.

A route asks its own action first. A question with no id is asked
before any name in the request is resolved, so its answer never depends
on whether the name exists. Then, for each other owner's object the
request names, the route asks `suite.use` or `baseline.use`; and for
each experiment an export names, the experiment a baseline pin names,
and the experiment of the comparison a bar evaluation names, it asks
`experiment.read`. Those questions come before any state of the named
object is checked, and a deny of one answers `422 reference_not_found`
on the member that names it, so no answer confirms that another owner's
object exists.

At start `examend serve` sends the contract's probe with
`authz.Check(ctx, client, "experiment.read", "Experiment")`. An endpoint
that allows it does not read the request, and the server does not start.
One that does not answer is named and the server starts, since every
request then fails closed.

### Denies and outages

| Answer | Response |
|---|---|
| a deny of a question with no id: an apply, create, pin, share, list, export, annotation list or submit | `403 forbidden`, the authorizer's `reason` as `details.reason`, never in `message` |
| a deny of a question about a stored object: a read, change, cancel, import, evaluation | the `404` of that object's kind, byte for byte the answer for an id that names nothing |
| a deny of `suite.use` or `baseline.use`, or of `experiment.read` about an object another route's body names | `422 reference_not_found` on the member that names it, as for a missing object |
| anything that is not a well-formed answer: another status than `200`, a body that does not parse or carries no `allow`, no answer within `authz.Timeout`, limits or a filter Examen cannot read | `503 authorizer_unavailable`, with nothing done; the error names neither the endpoint nor its bearer |

An id that names nothing is `404` before any question is asked, so
during an outage an id that exists answers `503` and one that does not
answers `404`. This is accepted: an id carries 80 random bits, and
asking about unknown ids would let any caller drive the authorizer.

For unattended work, a deny of `experiment.run` parks the cell (below)
and an outage holds the work until an answer arrives.

### Limits

The limits on an allow are how an operator's plane tells Examen what an
owner is held to. Examen enforces what it is told and stores no plan.

```go
package authorizer

type Limits struct {
    Owner                string   // whose a created object is; empty is the owner the request named, else the caller's subject
    MaxParallelism       int      // the highest parallelism an experiment may set; 0 leaves MAX_PARALLELISM
    MaxTrials            int      // cells × trials one experiment may declare; 0 is no cap beyond spec 002's
    MaxActiveExperiments int      // the owner's experiments neither complete nor canceled; 0 leaves MAX_ACTIVE_EXPERIMENTS_PER_OWNER
    Executors            []string // the executors an experiment may use, "topos" and "cella"; empty is every configured one
    JudgeModels          []string // globs over the model ids a model judge may call; empty is all
    Budget               string   // the Lux Budget the experiment is metered against, assigned by the authorizer; empty leaves the one the request named
}
```

An authorizer that is also its installation's budget authority answers
`experiment.create` with `budget`: Examen records that Budget on the
experiment, and a request that named a different one is refused `403
forbidden` with `details.reason` `budget`. With no `budget` on the
allow, the request's own `budget` is used and must exist.

The JSON form has every member optional and exactly these keys:
`owner`, `max_parallelism`, `max_trials`, `max_active_experiments`,
`executors`, `judge_models`, `budget`. `DecodeLimits(authz.Decision)` reads it. An
object that does not parse, a key this version does not know, a figure
below zero, an empty glob and an executor that is not `topos` or `cella`
make the answer no decision, and the request fails closed with `503
authorizer_unavailable`: a ceiling Examen cannot read is not one it can
hold.

| Member | Read from the allow of | Held |
|---|---|---|
| `owner` | every `.apply`, `experiment.create`, `baseline.pin`, `export.create`, `report.create`, and a question that resolves a name in the caller's context (spec 003) | the object is recorded under it, and every question after carries it |
| `max_parallelism` | `experiment.create`, `experiment.update` | a declared `parallelism` above it is `403 forbidden` with `details.reason` `max_parallelism`; an absent one is the lower of `DEFAULT_PARALLELISM` and it |
| `max_trials` | `experiment.create`, and `experiment.update` when it changes `trials` | `trials_total` above it is `403 forbidden`, reason `max_trials`. Retries of infrastructure failures, bounded by `MAX_INFRA_RETRIES`, are not counted |
| `max_active_experiments` | `experiment.create` | counted with the insert, under one lock on the owner: `403 forbidden`, reason `max_active_experiments` |
| `executors` | `experiment.create` | a candidate whose executor is not listed is `403 forbidden`, reason `executors`, on that candidate |
| `judge_models` | `experiment.create`, `experiment.regrade` | a model judge of the suite, or the judge a re-grade names, whose model no glob matches is `403 forbidden`, reason `judge_models`, with the grader in `details.detail`. This is how an installation holds judges to the models whose data terms an owner accepts |

An allow narrows spec 002's constants and never widens them: a
`max_parallelism` above `MAX_PARALLELISM` is held to `MAX_PARALLELISM`,
`max_trials` never admits a cell above `MAX_TRIALS_PER_CELL`, and
`max_active_experiments` never admits more than
`MAX_ACTIVE_EXPERIMENTS_PER_OWNER`. The
limits of any other allow are not read. With no `owner` on the allow, a
create is recorded under the owner the request named, and under the
caller's subject when it named none. A limit refuses a request after its
allow, so the answer is `403`, never `404`, and `details.figure` carries
the limit's figure. With no authorizer, spec 002's constants alone hold.

### Filters

`filter.owners` narrows every `.list`. An allow with no filter lists the
caller's own objects, so an authorizer that lets a caller see other
owners names them. The request's `owner` narrows further and never
widens, and a list that names an owner sends it in the question, so a
service that lists one person's experiments for them is answered by a
filter naming that person. `GET /comparisons` lists the comparisons of
the experiments the filter of `experiment.list` admits.

`filter.labels` is applied to `metadata` on `task.list`,
`candidate.list` and `experiment.list`, the listed kinds that have it
(spec 002). On any other list a filter that names labels cannot be
applied, and is treated as no answer: `503 authorizer_unavailable`.

### The owner policy

With no `EXAMEN_AUTHORIZER_URL`, `examend` logs `owner policy` at start
and decides itself. The rows, in the order they are tried:

| Question | Answer |
|---|---|
| the probe id, as any subject | deny, `probe` |
| no subject | deny, `anonymous` |
| an action outside the vocabulary, or asked about another kind | deny, `unknown_action` |
| `share.list` naming the subject as `grantee`, or naming none | allow |
| any other `.list` | allow, with a filter that narrows the list to the subject's own |
| a stored object whose owner is the subject, any action | allow |
| `suite.read`, `suite.use`, `baseline.read`, `baseline.use` with `shared` true and the subject as context owner | allow |
| `task.read`, `metric.read`, `grader.read`, `suite.read` with `shared` or `pinned` true | allow |
| `experiment.read`, `report.read` with `shared` true | allow |
| an apply, create, pin or export that names no owner, or names the subject | allow |
| `experiment.run` whose `created_by` and `owner` are the subject | allow |
| anything else | deny, `not_owner` |

A subject applies, creates and pins under its own subject only; reads,
changes, cancels and exports what it owns; annotates its own trials; and
reads and uses what another subject shared with it. Naming another owner
is denied for every subject, so organizations, annotators who work for
others, and a service acting for its users need an authorizer. The
policy has no administrators and names no limit of its own. An allow is
narrowed by the grants of the caller's token, with `authz.Restrict`, as
an authorizer's is, and a token whose grants cannot be read is denied.

### Sharing

A share is spec 002's record, `{id, owner, object, suite, baseline,
experiment, report, grantee, created_by, created_at}`, with the prefix
`shr_`. An owner shares a suite it owns, a baseline it pinned, an
experiment or a report with another owner through the routes of spec
003; the question is `suite.share`, `baseline.share`,
`experiment.share` or `report.share`, and under the owner policy the
owner alone may ask it. A share names a suite or baseline by name, so
it covers every record of that name, those made after it included,
until it is removed.

| Share | The grantee may | The grantee may not |
|---|---|---|
| a suite | read every version of the suite, and every task, metric and grader those versions pin; name any version in its own experiments, whose work is then its own, on its own budget; pin baselines and apply bars of its own on the suite | apply a version under the name, share it on, or read the sharer's experiments, candidates, baselines or bars |
| a baseline | read the baseline's records; name one as its experiment's baseline, so its comparisons are computed against the sharer's pinned cells | read the sharer's experiment, trials, measurements or evidence: a comparison carries the baseline side's summaries alone (spec 003) |
| an experiment | read the experiment, its cells, trials, measurements, comparisons and live view (spec 010) | change, cancel, re-grade, delete or share it; evidence bytes are read only as Arca allows |
| a report | read the report's document and its released narrative (spec 010) | read a draft, or the experiment and its trials |

A baseline is used only by an experiment of the same suite, so a
baseline share is useful beside a share of its suite. Evidence stays in
the sharer's Arca space, and sharing it is Arca's own act.

An authorizer sees a share as the field `shared` on a read or a use of
another owner's object, and may honor or ignore it; the owner policy
honors it. Removing a share stops new references and new reads once any
cached allow has lapsed. An experiment that already pins a version of a
shared suite or a shared baseline keeps it: reads of the objects it pins
carry `pinned: true`, and its cells, comparisons and reports keep
reading what they pinned, so a result stays reproducible.

### Acting as the owner

Every request Examen sends to Topos, Cella, Lux, Arca or Origo for an
owner's work carries an owner token: a token for that owner, addressed
to that one core, which Examen obtained from the minter or received from
the owner's token files. Examen never sends a credential of its own to a
core, never forwards a caller's bearer, which is addressed to Examen, and
holds no credential of any core.

| From Examen to | Credential | Identity carried |
|---|---|---|
| an OIDC issuer, for discovery and keys | none | none |
| the authorizer | the bearer in `EXAMEN_AUTHORIZER_TOKEN_FILE` | the installation's trust in Examen |
| the minter | the bearer in `EXAMEN_MINTER_TOKEN_FILE` | the installation's trust in Examen |
| Topos, Cella, Lux, Arca, Origo | an owner token for that core's audience | the owner, on behalf of the subject below |
| Postgres | the database URL (spec 011) | none |
| the telemetry collector | the standard OpenTelemetry headers | none |

An owner token is asked for one piece of work, its **purpose**:

| Purpose | When | Cores |
|---|---|---|
| `resolve` | an apply or an experiment create checks its pins and its budget (spec 003) | Topos (agent versions), Lux (models, the Budget), Origo (commits), Arca (workspace paths and digests), Cella (images) |
| `trial` | a cell's rollouts: submitting, following, resizing and canceling them, and the Lux Key and Cella Secret of a `cella` rollout (spec 005) | Topos, Cella, Lux |
| `judge` | a model judge's Lux Key and calls, an agent judge's Topos session, and the evidence either reads (spec 006) | Lux, Topos, Arca |
| `import` | reading a finished session or sandbox and its records (spec 005) | Topos, Cella, Lux, Arca |
| `export` | writing a dataset (spec 007) | Arca |
| `report` | a narrative draft's call and a report's HTML bundle (spec 010) | Lux, Arca |

The token's subject is the caller, for work done during a request, and
the experiment's or export's `created_by` for work done later. Examen
asks for a token at first need for one owner, subject, audience,
experiment and purpose, keeps it in memory until `OWNER_TOKEN_RENEWAL`
before its expiry, and never uses it longer than
`OWNER_TOKEN_MAX_LIFETIME` after obtaining it.

**Model access.** Lux's model doors take Keys. A Key Examen creates,
for a `cella` rollout or for a model judge, is created at Lux's control
plane with a Lux owner token and names the experiment's Budget, so its
`owner` is the owner, the owner is its writer, and every call through it
is metered against the experiment. Examen never assigns an owner on a
create made with its own identity. A Key's value is held in memory for
the calls it serves, and for a `cella` trial in the Cella Secret that
carries it; it is never written to Postgres, an event, a log line or an
error, and the Key is deleted when its work ends (specs 005 and 006).

**Inside a trial** Examen places no token. A Topos trial reaches models
and its machine with the credentials Topos gives its sessions; a
`cella` trial reaches Lux through the Key in its Secret, which Cella's
egress gateway substitutes for a placeholder at the last hop.

**Attribution check.** Where a core's answer names the owner it
recorded for what Examen created, such as a Lux Key's `owner`, Examen
compares it with the experiment's owner. A mismatch deletes or ends the
object, and the cell parks `blocked` with `parked_reason`
`owner_mismatch`.

### The minter

The minter is an HTTP endpoint the operator provides, usually in front
of the installation's identity provider. Examen asks it for one owner
token at a time, with a bearer of its own. Examen offers no route that
turns one token into another, and asks the minter only for work the
authorizer allowed: the caller's action during a request, or
`experiment.run` for unattended work.

```json
POST {EXAMEN_MINTER_URL}
Authorization: Bearer <the content of EXAMEN_MINTER_TOKEN_FILE>

{ "owner": "https://issuer.example|9ab3",
  "subject": "https://issuer.example|9ab3",
  "audience": "topos",
  "claims": { "examen": { "experiment": "exp_01jb…", "purpose": "trial" } } }
```

```json
200
{ "token": "eyJ…", "expires_in": 600 }
```

| Member | Meaning |
|---|---|
| `owner` | the owner the work is for |
| `subject` | the subject on whose behalf it happens: the caller, or the experiment's or export's `created_by` |
| `audience` | the core's audience, from `EXAMEN_TOPOS_AUDIENCE`, `EXAMEN_CELLA_AUDIENCE`, `EXAMEN_LUX_AUDIENCE`, `EXAMEN_ARCA_AUDIENCE` or `EXAMEN_ORIGO_AUDIENCE` |
| `claims` | `examen: {experiment, purpose}`, for the minter to place on the token; `experiment` is absent for a `resolve` of an apply |
| `ttl` | the lifetime asked: `OWNER_TOKEN_MAX_LIFETIME`, in seconds |
| `token`, `expires_in` | the answer: the token and its lifetime in seconds |

The minter's obligations, which the cores and not Examen check: the
token is addressed to `audience` alone and verifies against that core's
issuers; its identity is `subject` acting in `owner`'s context, as the
installation's issuer writes a context, and for a personal owner the two
are one; it carries `claims` verbatim, so the core's authorizer, which
receives every verified claim, can attribute the call to the experiment
and refuse a token whose experiment it knows has ended; it lasts no
longer than `ttl`; and the minter answers `403` for an owner and subject
it does not let Examen act for.

| Minter answer | During a request | In unattended work |
|---|---|---|
| `200` with `token` and `expires_in` | used | used |
| `403` | `403 forbidden`, `details.reason` `owner_token_refused` | the cell parks `blocked`, `parked_reason` `owner_token_refused`; an export fails with the same reason |
| anything else, or no answer within `authz.Timeout`, after one retry on a connection failure as `authz.Retryable` judges it | `503 owner_tokens_unavailable`, retryable | the work waits and is tried again; nothing parks for an outage |

A minted token is opaque to Examen, which reads none of its claims. It
is kept in memory alone, and never written to Postgres, an event, a
span, a log line or an error.

### Owner token files

An installation run by one owner may receive that owner's tokens
instead. `EXAMEN_SOLE_OWNER` names the owner, a rendered subject, and
`EXAMEN_OWNER_TOKEN_DIR` a directory holding one file per core, named by
that core's configured audience (`topos`, `cella`, `lux`, `arca`,
`origo` by default), each holding a bearer of that owner for that core.
Examen reads a file when its last read is older than
`OWNER_TOKEN_RENEWAL`, so a process outside Examen may refresh it in
place.

Work for any owner other than `EXAMEN_SOLE_OWNER`, or on behalf of any
other subject, is refused `503 owner_tokens_unavailable` with
`details.reason` `sole_owner`, not retryable; unattended work for
another owner cannot exist, since its create is refused. A file that is
missing or empty when a request needs it is `503
owner_tokens_unavailable` with reason `token_file`; unattended work
waits for it.

`EXAMEN_SOLE_OWNER` and `EXAMEN_MINTER_URL` exclude each other, and
setting both stops the start (spec 011). With neither, Examen serves
every route that reaches no core, and a request that needs one answers
`503 owner_tokens_unavailable` with reason `not_configured`, not
retryable.

### Unattended work

Before each step Examen takes for an experiment with no caller present
(a cell's smoke, main or retry rollout, a judge's run over its trials),
Examen asks `experiment.run`. The question's subject is the experiment's
`created_by`, its issuer and `sub` apart, and its claims are empty,
since Examen holds no token of that subject: an authorizer decides it
from its own state, as it decides a scheduled start it did not see a
person make. The resource is the stored experiment with `purpose`,
`cell` and `executor`. The answer is cached for its `ttl`, since it
carries an id, so a cell's trials do not each ask.

| Answer | The step |
|---|---|
| allow | Examen obtains its owner tokens and acts |
| deny | the cell parks `canceled` with `parked_reason` `run_denied`, and the step is not taken |
| unavailable | the step waits and is tried again; nothing parks for an outage |

So an owner who loses the right to run, or an experiment an operator
stops, ends its work within one `ttl`, without Examen deciding.

### What a handler depends on

Three interfaces of `internal/access` and `internal/owner`, and a
handler cannot tell what is behind any of them. One turns a bearer into
a caller: the subject, its issuer and `sub` apart, and every verified
claim. One answers a question with a decision: allow or deny, the
reason, the limits, the filter. One answers an owner token for an owner,
a subject, an audience, an experiment and a purpose, from the minter,
from the token files, or not at all. Every route is written the same
way: authenticate, build the resource from the request and the stored
object, ask, act within the decision, and reach a core only through an
owner token.

The routes are kept as data, one row per route of spec 003 with the
action and the fields it sends, held to spec 003 and to `authorizer` by
a test.

### Limits of this spec

The constants of spec 002 this spec holds: `OWNER_TOKEN_MAX_LIFETIME`,
`OWNER_TOKEN_RENEWAL`, `MAX_PARALLELISM`, `DEFAULT_PARALLELISM`,
`MAX_TRIALS_PER_CELL`, `MAX_INFRA_RETRIES` and
`MAX_ACTIVE_EXPERIMENTS_PER_OWNER`.

## Not in this spec

Browser sessions and cookies: a person reaches Examen through a client.
A token exchange route, a delegation claim, or a chain of actors: a
token's subject is the person on whose behalf the work happens, and the
minter is the one place an installation issues tokens for Examen's
work. Administrators. Rate limits on requests. How a core's authorizer
reads the `examen` claim, which is that core's. Sharing evidence, which
is Arca's. Deciding by an object's content beyond the fields sent.

## Acceptance criteria

- The verifier passes the shared conformance suite; a verified token
  becomes a caller whose subject is `<iss>|<sub>` and whose claims are
  the token's, verbatim; two issuers that agree on a `sub` are two
  subjects.
- A token for another audience, of an unlisted issuer, expired, not yet
  valid or badly signed is `401 invalid_token` with the verifier's
  reason, and no error repeats any part of the token.
- A server that lists no issuer, or an authorizer or minter URL with no
  readable token file, does not start and names the variable.
- The constants of `authorizer` are the vocabulary table above, in its
  order with its fields; the table of routes has one row per route of
  spec 003, each asking the action named there; no field of any action
  is named `kind` or `id`; every field reaches the stub authorizer
  through the envelope as Examen built it.
- Every route asks its action before it acts; a question with no id is
  asked before a name is resolved; a deny and an outage are answered
  with nothing done.
- A deny of a route about a stored object is byte for byte the `404` of
  an id that names nothing; a deny of a create, apply, pin, share, list,
  export or submission is `403 forbidden`; a deny of `suite.use` or
  `baseline.use` is `422 reference_not_found`.
- Another status, a body with no `allow`, a late answer, limits with an
  unknown key, a negative figure, an empty glob or an unknown executor,
  and a label filter on a list other than tasks are each `503
  authorizer_unavailable` with nothing done.
- The client holds an allow for its `ttl`, a deny for `authz.DenyTTL`
  and an outage never, asks every question with no id each time, and
  retries once on a connection failure.
- Each member of `Limits` has a case in which an allow carrying it
  changes the outcome of a request, and a limit above its spec 002
  constant is held to the constant.
- A list is narrowed by its allow's filter and by the request's
  `owner`, and never widened by `owner`.
- Under the owner policy, with verified tokens of two subjects against a
  running server, neither reads, lists, changes, cancels, imports into,
  exports or annotates anything of the other's, and neither creates in
  the other's name.
- After a suite share, the grantee reads the suite's versions and the
  objects they pin and runs an experiment on it under its own owner and
  budget, and cannot apply under the name or read the sharer's
  experiments; after a baseline share, the grantee's comparison is
  computed against it and no route answers the sharer's trials or
  measurements; after the share is removed, a new reference is refused
  and an experiment that pinned the shared version still reads it.
- After a suite share, the grantee pins a baseline and applies a bar of
  its own on the suite; after an experiment share, the grantee reads the
  experiment, its trials and its comparisons and cannot change, cancel,
  re-grade or delete it; after a report share, the grantee reads the
  report's document and released narrative and no draft.
- The owner policy answers the contract's conformance suite and narrows
  an allow by the grants of a token; a server whose authorizer allows
  the probe does not start.
- Against stub cores that record every request, every request to Topos,
  Cella, Lux, Arca and Origo carries a token the stub minter minted for
  the experiment's owner, the right audience and the right purpose; none
  carries Examen's authorizer or minter bearer, and none carries a
  caller's bearer.
- A canary planted as a minted token, a token file's content and a Lux
  Key's value is found only in requests to the core it belongs to, and
  in no response, log line, span, event, error or database row.
- A minter `403` parks the cell `blocked` with `owner_token_refused`; a
  minter outage parks nothing, and the work resumes when the minter
  answers.
- A deny of `experiment.run` parks the cell `canceled` with
  `run_denied` and starts no trial; an outage holds the work.
- A Key whose recorded owner differs from the experiment's owner is
  deleted and the cell parks `blocked` with `owner_mismatch`.
- With `EXAMEN_SOLE_OWNER` set, work for that owner uses the token
  files, a refreshed file is read within `OWNER_TOKEN_RENEWAL`, and any
  other owner's request that needs a core is `503
  owner_tokens_unavailable` with reason `sole_owner`.
