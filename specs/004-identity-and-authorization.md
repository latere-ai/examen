---
title: "Identity and authorization: verifying a caller, the action vocabulary, the authorizer with its limits and filters, the built-in owner policy, share grants and readers who do not sign in, acting as the owner toward Topos, Cella, Lux, Arca and Origo, and the key source that leases model Keys and reads budgets"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
affects: [authorizer/, internal/access/, internal/owner/, internal/keys/, internal/httpapi/, internal/execute/]
effort: large
created: 2026-10-17
updated: 2026-10-18
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
minter the operator provides or, after v0.1, on an installation run by
one owner, receives from files that owner keeps. The Lux Keys that
model judges, `cella` rollouts and narrative drafts call through come
from a key source: Examen asks the installation for them when the
installation keeps its own budgets, or, after v0.1, creates them at Lux
as the owner when it runs on its own; the same source reads an
experiment's budget figures. An installation with no owner token source
and no key source serves the v0.1 profile, whose work reaches no core
as the owner (spec 003). A reader
who does not sign in reads only what was published, and the authorizer
is still asked about each read, with no subject. After v0.1, with the
`cella` executor, a `program` candidate may also be given owner tokens
for the APIs it calls, each placed in the
trial's sandbox only as a placeholder Cella's egress gateway swaps on
the way to that API's host. Examen never uses an identity of its own
toward a core. It holds no accounts, roles or plans.

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
dataset export, a baseline record, a taxonomy record, a share, a bar
evaluation, a publication and a submission carry `created_by`, the
subject that created it, a publication version `published_by`, the
person who signed off on publishing it (spec 013), and an annotation's
measurements carry `annotator`.
Unattended work for an experiment acts for its `owner` on behalf of its
`created_by` (below).

### Who creates and who runs

Objects are created by people. A person, signed in, creates an
experiment, or applies, pins or imports, under an owner: themselves, or
an organization they belong to. The owner and the creator can differ:
an experiment created in an organization's context has the organization
as its `owner` and the member as its `created_by`, both stored at the
create, never changed, and answered on every read (spec 002). Examen
reads no claim, so it cannot tell a person's token from a service's, an
agent's or a sandbox's, nor whether a person belongs to the
organization a request names. The authorizer decides both: it is where
an installation refuses every apply, create, pin and import, and every
action that runs an experiment, to a token that is not a person's, and
allows an owner other than the caller only to a member of that owner.
Under the owner policy the owner is always the caller's subject, and
the issuers `EXAMEN_OIDC_ISSUERS` lists decide who holds a subject.

Running an experiment is every action that starts, feeds or changes its
execution: `experiment.create`; `experiment.update`, a change and the
close of its intake; `cell.retry`; `experiment.cancel`;
`experiment.import`; `trial.lease`, a lease and its renewal;
`trial.submit`; `experiment.regrade`; and `experiment.run`, which Examen
asks before its own unattended work. Only the experiment's creator, its
owner and, for an organization, the owner's members run it. Everyone
else who can see it, by a share, a link or a publication, reads its
setup and outcomes and runs nothing: no allow of an action that runs an
experiment rests on `shared`, and the owner policy allows each to the
owner alone. Examen keeps no role of its own for this, no member of an
experiment beside its owner's, and every share stays a grant of reading
(spec 013). That holds for what an experiment runs on too: a share of a
suite or a baseline lets its grantee read it, and never lets the
grantee name it in an experiment, a baseline or a bar of its own, so no
allow of `suite.use` or `baseline.use` rests on `shared`, and an
experiment runs only on a suite of its own owner's.

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
| `Suite` | `suite.use` | `POST /experiments` naming another owner's suite, directly or through `benchmark`; `PUT` of a bar or a baseline on another owner's suite | id; `owner`, `name`, `digest`, `for_owner`, `shared`, `publication`, `published` |
| `Suite` | `taxonomy.apply` | `PUT /suites/{suite}/taxonomies/{metric}` | `owner`, `name`, `metric`, `version` |
| `Suite` | `taxonomy.read` | `GET` of a taxonomy, its history and its unmapped labels, and `GET /taxonomies/{id}` | id; `owner`, `name`, `metric`, `version`, `shared`, `pinned` |
| `Candidate` | `candidate.apply` | `PUT /candidates/{name}` | `owner`, `name`, `digest`, `candidate_kind`, `agent`, `image`, `model`, `reasoning`, `harness`, `credential_audiences` |
| `Candidate` | `candidate.read` | `GET /candidates/{ref}`, `GET /candidates/{name}/versions` | id; `owner`, `name`, `digest`, `candidate_kind` |
| `Candidate` | `candidate.list` | `GET /candidates` | `owner` |
| `Experiment` | `experiment.create` | `POST /experiments` | id (minted before the question, so an authorizer can keep a record of the experiment it allows); `owner`, `suite`, `suite_owner`, `candidates`, `executors`, `models`, `judge_models`, `grader_kinds`, `credential_audiences`, `cells`, `trials`, `trials_total`, `parallelism`, `budget`, `budget_cap_usd_micro`, `reference_candidate`, `baseline`, `baseline_owner`, `benchmark`, `mode`, `adopt` |
| `Experiment` | `experiment.read` | `GET /experiments/{id}` and every route below it that reads, `GET /cells/{id}`, `GET /trials/{id}`, `GET /measurements/{id}`, `GET /comparisons/{id}` with its rows, `POST /comparisons`; and the experiment each export, baseline pin or bar evaluation names | id; `owner`, `suite`, `state`, `mode`, `shared` |
| `Experiment` | `experiment.list` | `GET /experiments`, `GET /comparisons` | `owner` |
| `Experiment` | `experiment.update` | `PATCH /experiments/{id}`, `POST /experiments/{id}/close` | id; `owner`, `suite`, `state`, `mode`, and what it changes: `trials` with `trials_total`, `parallelism`, or the reference as `reference_candidate`, or `baseline` with `baseline_owner` |
| `Experiment` | `cell.retry` | `POST /cells/{id}/retry` | id; `owner`, `suite`, `state`, `mode`, `cell`, `cell_state`, `parked_reason` |
| `Experiment` | `experiment.cancel` | `POST /experiments/{id}/cancel` | id; `owner`, `suite`, `state` |
| `Experiment` | `experiment.delete` | `DELETE /experiments/{id}` | id; `owner`, `suite`, `state`, `evidence` |
| `Experiment` | `experiment.import` | `POST /experiments/{id}/imports` | id; `owner`, `suite`, `state`, `mode`, `task`, `candidate`, `executor`, `executor_ref` |
| `Experiment` | `trial.lease` | `POST /experiments/{id}/work/lease` | id; `owner`, `suite`, `state`, `mode`, `candidate`, `harness`, `lease` |
| `Experiment` | `trial.submit` | `POST /trials/{id}/submit` | id; `owner`, `suite`, `state`, `mode`, `candidate`, `harness`, `trial`, `lease`, `outcome` |
| `Experiment` | `experiment.regrade` | `POST /experiments/{id}/regrade` | id; `owner`, `suite`, `state`, `grader`, `judge_models` |
| `Experiment` | `experiment.share` | `PUT`, `DELETE` and `GET` of `/experiments/{id}/shares` | id; `owner`, `grantee` |
| `Experiment` | `experiment.run` | Examen itself, before unattended work (below) | id; `owner`, `created_by`, `state`, `purpose`, `cell`, `executor`, `budget` |
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
| `Report` | `report.read` | `GET /reports/{id}` | id; `owner`, `experiment` |
| `Report` | `report.update` | `POST /reports/{id}/draft`, `PUT /reports/{id}/narrative`, `POST /reports/{id}/exports` | id; `owner`, `experiment`, `model` |
| `Report` | `report.release` | `POST /reports/{id}/release` | id; `owner`, `experiment` |
| `Publication` | `publication.create` | `POST /publications` | id (minted before the question); `owner`, `publication_kind`, `name`, `visibility`, `evidence_level`, `source`, `license`, `submissions` |
| `Publication` | `publication.read` | `GET /publications/{id}`, `GET /publications/{id}/versions/{n}`, `GET /publications/{id}/results`, `GET /publications/{id}/results/compare`, and before `publication.list` and `publication.pause`; every public route, asked with no subject | id; `owner`, `publication_kind`, `visibility`, `listed`, `paused`, `version`, `via`, `shared` |
| `Publication` | `publication.browse` | `GET /publications` | `owner`, `publication_kind`, `visibility` |
| `Publication` | `publication.update` | `PATCH /publications/{id}`, `POST /publications/{id}/versions`, `POST /publications/{id}/link`; `GET /publications/{id}`, to decide whether its answer carries `link` | id; `owner`, `publication_kind`, `visibility`, `evidence_level`, `source`, `license`, `submissions`, `version` (the latest version number the request saw, so an authorizer can refuse a stale change) |
| `Publication` | `publication.withdraw` | `POST /publications/{id}/versions/{n}/withdraw` | id; `owner`, `publication_kind`, `version` |
| `Publication` | `publication.list` | `PUT` and `DELETE` of `/publications/{id}/listing` | id; `owner`, `publication_kind`, `visibility`, `listed` |
| `Publication` | `publication.pause` | `POST /publications/{id}/pause`, `POST /publications/{id}/resume` | id; `owner`, `publication_kind`, `visibility`, `paused`, `reason` |
| `Publication` | `publication.share` | `PUT`, `DELETE` and `GET` of `/publications/{id}/shares` | id; `owner`, `grantee` |
| `Submission` | `submission.create` | `POST /submissions` | id (minted before the question); `owner`, `benchmark`, `benchmark_owner`, `submissions`, `via`, `shared`, `report` |
| `Submission` | `submission.read` | `GET /submissions/{id}` | id; `owner`, `benchmark`, `benchmark_owner`, `state` |
| `Submission` | `submission.list` | `GET /submissions` | `owner`, `benchmark` |
| `Submission` | `submission.withdraw` | `POST /submissions/{id}/withdraw` | id; `owner`, `benchmark`, `benchmark_owner`, `state` |
| `Submission` | `submission.review` | `GET /publications/{id}/submissions`, `POST /submissions/{id}/accept`, `POST /submissions/{id}/reject` | id, the benchmark's on the list and the submission's otherwise; `owner`, `benchmark`, `benchmark_owner`, `state` |
| `Share` | `share.list` | `GET /shares` | `grantee` |

`GET /openapi.yaml` asks nothing: it is a public document.

`cell.retry`, `trial.lease` and `trial.submit` are actions of their
own, so an authorizer can tell a retry or an external harness's work
from a change and a grant can name each, but they act on the experiment
and are decided as `experiment.update` is: an authorizer that lets a
subject change an experiment lets it retry the experiment's cells and
lease and submit its external trials, and the owner policy allows them
to the owner. Each runs the experiment (Who creates and who runs,
above), so a share never allows one. A token whose grants name
`trial.lease` and `trial.submit` alone is how the owner, or a member of
an owning organization, runs an external harness that can do nothing
else; the token is still that person's.

The shared envelope keeps two names for the resource itself:
`resource.kind`, the kind of the table's first column, and
`resource.id`, the id of a stored object, written "id" in the table. It
drops a field of either name on the wire, so no field takes one: an
object's own `type` or `kind` is sent as `metric_type`, `grader_kind`,
`candidate_kind`, `workspace_kind` or `publication_kind`.

| Field | Meaning |
|---|---|
| `owner` | the object's owner; on an apply, a create, a pin or a list, the owner the request names, left out when it names none, which means the caller's context. On `annotation.submit`, the trial's owner; on a taxonomy action, the suite's owner; on a submission action, the submitter's, left out on the review queue |
| `name`, `digest` | the object's name and digest (spec 002); on a baseline or bar, `suite` is the suite's name |
| `suite_owner` | on a baseline or bar: the owner of its suite, which differs from `owner` when the suite is shared with it |
| `shared` | on a read or use of another owner's object: whether a share record names the context owner for it, or for a suite or baseline that pins it; on `suite.use` and `submission.create`, also whether one names it for the benchmark the request goes through |
| `pinned` | on a read of another owner's task, metric, grader or suite: whether an experiment of the context owner pins it |
| `for_owner` | the owner that would use another owner's suite or baseline: the experiment's, baseline's or bar's owner |
| `publication`, `published` | on `suite.use` through a benchmark: the benchmark's publication id, and its `visibility` when the request resolved it by its link token or public id |
| `metric`, `version` on a taxonomy action | the open-vocabulary metric's digest, and the record's `version`: on `taxonomy.apply`, the one it would append |
| `metric_type`, `grader_kind`, `candidate_kind`, `workspace_kind` | the object's `type`, `kind`, or workspace kind (`none`, `origo`, `arca`) |
| `model`, `agent`, `image`, `reasoning` | a grader's judge model or judge agent; a candidate's pinned agent version or a `program` candidate's image, its model and its reasoning level; on `report.update`, the model a narrative draft calls |
| `harness` | an `external` candidate's `harness`; on `trial.lease` and `trial.submit`, that of the candidate whose trials are leased or submitted |
| `credential_audiences` | on `candidate.apply`, the audiences a `program` candidate's `credentials` name; on `experiment.create`, those of every candidate |
| `repository`, `commit`, `path`, `image` on a task | the workspace's Origo repository and commit, or Arca path; the environment's image digest |
| `task_count`, `metric_count` | how many tasks and metrics a suite version pins |
| `suite`, `suite_owner` | the experiment's suite digest and its owner |
| `candidates`, `models`, `judge_models`, `executors` | the candidate digests; every model the candidates name; every model the suite's model judges name; the executors the candidates need, `topos`, `cella` or `external` |
| `grader_kinds` | on `experiment.create`: the kinds of the graders the suite version names, its `decline_judge` and its `canonicalizers` included, each once: `check`, `model_judge`, `agent_judge` or `annotation`; the built-in decline judge is none of them. With `executors`, it tells an authorizer whether the experiment's work needs an owner token at all: an experiment whose candidates are all `external` and whose graders include no `check`, `model_judge` or `agent_judge` reaches no core as the owner while no file store is configured (spec 003, The v0.1 profile) |
| `cells`, `trials`, `trials_total` | tasks × candidates; N; cells × N |
| `budget`, `budget_cap_usd_micro` | on `experiment.create`: the Budget's name the request gave, or the cap it gave in micro-dollars in its place; on `experiment.run`, `budget` is the stored Budget's name |
| `benchmark` | on `experiment.create`: the publication id of the benchmark the request runs another owner's suite through; on a submission action, the benchmark's publication id |
| `benchmark_owner` | on a submission action: the benchmark's owner |
| `reference_candidate` | on `experiment.create` and `experiment.update`: the candidate digest the experiment's `reference` names, when it names a candidate |
| `baseline`, `baseline_owner` | the baseline record's id and its owner, when the experiment's `reference` names a baseline; on a baseline action, the baseline's |
| `state` | on a stored experiment: its state |
| `purpose`, `executor` | on `experiment.run`: the work about to be done (below) and its executor |
| `cell` | on `experiment.run` and `cell.retry`: the cell's id |
| `candidate`, `trial`, `lease`, `outcome` | on `trial.lease` and `trial.submit`: the candidate digest; the trial's id; the lease's id, on a renewal or a submit; the outcome submitted |
| `cell_state`, `parked_reason` | on `cell.retry`: the cell's parked state, `blocked` or `over_budget`, and its `parked_reason` |
| `grader` | on a re-grade and an annotation action: the grader version's digest |
| `experiment` | on a report or annotation action: the experiment's id |
| `experiments`, `all_generations`, `evidence` | on `export.create`: the experiments its filter names, and its `all_generations` and `evidence` (spec 002); on `experiment.delete`, `evidence` is the parameter of spec 007 |
| `grantee` | the owner a share names; on `share.list`, the context owner |
| `publication_kind`, `name`, `visibility` on a publication | the publication's `kind`, `name` and `visibility`; on `publication.create` and `publication.update`, the visibility asked for |
| `evidence_level`, `source`, `license` | on `publication.create` and a `publication.update` that publishes a version: its evidence level; its source, the report id, suite digest or export id; and a dataset's license |
| `submissions` | the benchmark's `submissions` setting, or on `publication.update` the one asked for |
| `listed` | on `publication.read`, whether it is listed; on `publication.list`, true for a listing and false for its end |
| `paused`, `reason` | on `publication.read`, whether it is paused; on `publication.pause`, true for a pause and false for a resume, with the pause's reason |
| `version` on a publication | the version a read or a withdrawal names; absent for the latest |
| `via` | on `publication.read` and `submission.create`, how the publication was reached: `link` (its link token), `public_id`, `listing` (an item of `GET /public/listings`), or absent for a signed-in caller by its id |
| `report` | on `submission.create`: the report publication's id |
| `state` on a submission | its state |

A member Examen does not know for a request is left out, never sent
empty.

### The question

The shared contract, `latere.ai/x/pkg/authz`: Examen posts the subject,
its issuer and `sub` apart, every verified claim verbatim, the action
and the resource, with a bearer of its own, and receives `{allow,
reason, ttl, limits, filter}`. The request also carries what is known of
the HTTP request: its id, the peer address and the user agent. A
question with no subject, a public read's, leaves out the peer address
and the user agent, so every reader of one published version asks one
question, which the cache below absorbs; spec 003's per-address limit
still holds each reader. Examen fills no `workload` member.

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

A route asks its own action first, after the list question that fixes
its owner context where it resolves names before its own question, as
`POST /experiments` does when it names no owner (spec 003, The owner
context). A question with no id is asked
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
| a deny of a question with no id, or minted before the question: an apply, create, pin, share, list, export, annotation list or submit | `403 forbidden`, the authorizer's `reason` as `details.reason`, never in `message`; or, when the reason is one of the refusal codes below, that code |
| a deny of a question about a stored object: a read, change, cancel, import, evaluation | the `404` of that object's kind, byte for byte the answer for an id that names nothing |
| a deny of `publication.list` or `publication.pause` after `publication.read` let the same caller see the publication | `403 forbidden`, the reason as `details.reason`: the caller already knows it exists |
| a deny of `publication.read` with no subject, on a public route | `404 publication_not_found`, byte for byte the answer for a key that resolves to nothing |
| a deny of `publication.update`, asked on `GET /publications/{id}` after `publication.read` allowed the same caller, to decide whether the answer carries `link` | `link` is left out of the answer, and nothing else changes |
| a deny of `suite.use` or `baseline.use`, or of `experiment.read` about an object another route's body names | `422 reference_not_found` on the member that names it, as for a missing object |
| anything that is not a well-formed answer: another status than `200`, a body that does not parse or carries no `allow`, no answer within `authz.Timeout`, limits or a filter Examen cannot read | `503 authorizer_unavailable`, with nothing done; the error names neither the endpoint nor its bearer |

**A deny reason that names a refusal code.** An authorizer may refuse
for a reason that Examen documents as a refusal of its own. When the
reason of a deny answered with `403` is one of the codes spec 003's
table answers with `403`, other than `forbidden`, the answer is that
code with its own `message`, and `details.reason` carries it too: an
installation that refuses an experiment's create at its authorizer with
the reason `sign_in_not_kept`, because it keeps no sign-in of the
caller, is answered `403 sign_in_not_kept`, as if the minter had
refused. Today that code is `sign_in_not_kept` alone. Any other reason
stays `403 forbidden` with the reason in `details.reason`. A deny about
a stored object stays its `404` whatever its reason, so no reason
confirms that an object exists.

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
    Executors            []string // the executors an experiment may use, "topos", "cella" and "external"; empty is every configured one
    JudgeModels          []string // globs over the model ids a model judge may call; empty is all
    CredentialAudiences  []string // the audiences a program candidate may be given owner tokens for; empty is none
    Budget               string   // the Lux Budget the experiment is metered against, assigned or created by the authorizer; empty leaves the one the request named or the cap it gave
    Attribution          string   // the name a publication version shows for its owner; empty leaves the one the request gave
}
```

An authorizer that is also its installation's budget authority answers
`experiment.create` with `budget`: Examen records that Budget on the
experiment, with no request to Lux, and a request that named a different one is refused `403
forbidden` with `details.reason` `budget`. A request that gave a spending cap
in place of a name sends it as `budget_cap_usd_micro`, and such an
authorizer creates a hard Budget of that cap for the experiment and
answers its name in `budget`; a client never needs a Budget's name in
advance. With no `budget` on the allow, a named Budget is used and must
exist, and for a spending cap the key source decides: the `lux` source creates a
hard Budget at Lux as the owner, with the purpose `resolve`, named
`examen-<experiment id>`, of that amount in USD, with the window `none`
and the experiment's labels, so the owner is its writer; the `endpoint`
source refuses the create `422 budget_refused`, since an installation
that keeps its own budgets writes every Budget itself.

An authorizer that knows its people answers `publication.create`, and a
`publication.update` that publishes a version, with `attribution`: the
person's profile name or the organization's name, which the version
shows in place of any the request gave (spec 013). It is at most
`PUBLICATION_LINE_MAX_BYTES`.

The JSON form has every member optional and exactly these keys: `owner`,
`max_parallelism`, `max_trials`, `max_active_experiments`, `executors`,
`judge_models`, `credential_audiences`, `budget`, `attribution`.
`DecodeLimits(authz.Decision)` reads it. An object that does not parse,
a key this version does not know, a figure below zero, an empty glob, an
empty audience, an attribution that is empty or longer than
`PUBLICATION_LINE_MAX_BYTES`, and an executor that is not `topos`,
`cella` or `external` make the answer no decision, and the request fails
closed with `503 authorizer_unavailable`: a ceiling Examen cannot read
is not one it can hold.

| Member | Read from the allow of | Held |
|---|---|---|
| `owner` | every `.apply`, `experiment.create`, `baseline.pin`, `export.create`, `report.create`, `publication.create`, `submission.create`, and a question that resolves a name in the caller's context (spec 003) | the object is recorded under it, and every question after carries it |
| `max_parallelism` | `experiment.create`, `experiment.update` | a declared `parallelism` above it is `403 forbidden` with `details.reason` `max_parallelism`; an absent one is the lower of `DEFAULT_PARALLELISM` and it |
| `max_trials` | `experiment.create`, and `experiment.update` when it changes `trials` | `trials_total` above it is `403 forbidden`, reason `max_trials`. Retries of infrastructure failures, bounded by `MAX_INFRA_RETRIES`, are not counted |
| `max_active_experiments` | `experiment.create` | counted with the insert, under one lock on the owner: `403 forbidden`, reason `max_active_experiments` |
| `executors` | `experiment.create` | a candidate whose executor is not listed is `403 forbidden`, reason `executors`, on that candidate |
| `judge_models` | `experiment.create`, `experiment.regrade` | a model judge of the suite, or the judge a re-grade names, whose model no glob matches is `403 forbidden`, reason `judge_models`, with the grader in `details.detail`. This is how an installation holds judges to the models whose data terms an owner accepts |
| `credential_audiences` | `experiment.create`, from the release that builds program credentials with the `cella` executor, after v0.1 | a `program` candidate whose `credentials` name an audience the list does not hold is `403 forbidden`, reason `credential_audiences`, on that candidate. An empty or absent list allows none, so a program is given a credential only where an authorizer names its audience. No list admits a core audience Examen acts through or Examen's own: a credential naming one is refused at apply, `422 credential_audience_reserved` (spec 003), so a program under test never holds a token toward the services that run it, meter it, keep its evidence or grade it |
| `budget` | `experiment.create` | recorded as the experiment's Budget (above) |
| `attribution` | `publication.create`, and `publication.update` when it publishes a version | recorded as the version's `attribution` (spec 013) |

An allow narrows spec 002's constants and never widens them: a
`max_parallelism` above `MAX_PARALLELISM` is held to `MAX_PARALLELISM`,
`max_trials` never admits a cell above `MAX_TRIALS_PER_CELL`, and
`max_active_experiments` never admits more than
`MAX_ACTIVE_EXPERIMENTS_PER_OWNER`. The
limits of any other allow are not read. With no `owner` on the allow, a
create is recorded under the owner the request named, and under the
caller's subject when it named none. A limit refuses a request after its
allow, so the answer is `403`, never `404`, and `details.figure` carries
the limit's figure. With no authorizer, spec 002's constants alone hold,
and no program is given a credential.

### Filters

`filter.owners` narrows every list action: each `.list` but
`publication.list`, which lists a publication for everyone rather than
listing objects (spec 013), and `publication.browse`, which lists
publications. An allow with no filter lists the caller's own objects, so
an authorizer that lets a caller see other owners names them. The
request's `owner` narrows further and never widens, and a list that
names an owner sends it in the question, so a service that lists one
person's experiments for them is answered by a filter naming that
person. `GET /comparisons` lists the comparisons of the experiments the
filter of `experiment.list` admits.

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
| `publication.read` with no subject, `via` `link`, `public_id` or `listing` | allow: the key resolved to a publication its visibility lets anyone read |
| no subject | deny, `anonymous` |
| an action outside the vocabulary, or asked about another kind | deny, `unknown_action` |
| `publication.list`, `publication.pause` | deny, `no_review`: the policy reviews no listing and has no administrators, so nothing is listed or paused under it |
| `share.list` naming the subject as `grantee`, or naming none | allow |
| any other list action | allow, with a filter that narrows the list to the subject's own |
| `experiment.create` or `experiment.update` whose `suite_owner` or `baseline_owner` is present and is not the subject | deny, `not_owner`: an experiment runs only on its owner's suite and baseline (below) |
| a stored object whose owner is the subject, any action | allow |
| `submission.review` whose `benchmark_owner` is the subject | allow |
| `suite.use`, `baseline.use` of another owner's object | deny, `not_owner`: a share grants reading, and a suite or a baseline is used only by its owner's side, whoever can see it by a share, a link or a listing, a benchmark's suite included (spec 013) |
| `suite.read`, `baseline.read` with `shared` true and the subject as context owner | allow |
| `task.read`, `metric.read`, `grader.read`, `suite.read`, `taxonomy.read` with `shared` or `pinned` true | allow |
| `experiment.read`, `publication.read` with `shared` true | allow |
| an apply, create, pin or export that names no owner, or names the subject | allow |
| `experiment.run` whose `created_by` and `owner` are the subject | allow |
| anything else | deny, `not_owner` |

A subject applies, creates and pins under its own subject only; reads,
changes, cancels and exports what it owns, and runs only its own
experiments, on its own suites; annotates its own trials; reads what
another subject shared with it, and uses none of it: no experiment,
baseline or bar of its own names another subject's suite or baseline;
publishes `private` and `unlisted` items and reviews the submissions to
its own benchmarks; and runs no other subject's benchmark and submits
to none, whether it reached it by its link or a share. Naming another owner
is denied for every subject, so organizations, annotators who work for
others, and a service acting for its users need an authorizer. The
policy has no administrators and names no limit of its own. An allow is
narrowed by the grants of the caller's token, with `authz.Restrict`, as
an authorizer's is, and a token whose grants cannot be read is denied.

### Share grants and readers who do not sign in

Sharing is a later build step than the console path (spec 003); until
it is built, an owner reaches only its own objects. Spec 013 is the one
definition of sharing: what a share grant lets its grantee do, a
publication's link, and its listing. This spec holds how each reaches
the authorizer.

A share is spec 002's record with the prefix `shr_`. The questions that
create, remove and list one are `suite.share`, `baseline.share`,
`experiment.share` and `publication.share`, and under the owner policy
the owner alone may ask them. An authorizer sees a share as the field
`shared` on a read or a use of another owner's object, and may honor or
ignore it on a read; the owner policy honors it on a read alone, and no
allow of a use or of an action that runs an experiment rests on it. A share is a question with an
id, so a removed share stops new reads once any cached allow has
lapsed.

A publication's visibility reaches the authorizer as `visibility`, and
the way a request reached it as `via`. A reader who does not sign in is
asked about as a question with no subject, the publication's id and
`via`, so an installation can refuse public reads per owner, per kind
or altogether, as Arca's `link.read` lets an operator refuse a link
(spec 013). Making a publication `public` is the question
`publication.list`, which an authorizer allows only for a listing the
installation approved; Examen keeps no review of its own. Pausing one is
`publication.pause`, an administrator's question.

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
| the key endpoint, with the `endpoint` key source | the bearer in `EXAMEN_KEYS_TOKEN_FILE` | the installation's trust in Examen; the request names the owner and the experiment |
| Topos, Cella, Lux, Arca, Origo | an owner token for that core's audience | the owner, on behalf of the subject below |
| an API a `program` candidate calls | none from Examen: an owner token for the credential's audience, in a Cella Secret scoped to the credential's `host`, which Cella's egress gateway substitutes on the program's requests (spec 005) | the owner, on behalf of the subject below |
| Postgres | the database URL (spec 011) | none |
| the telemetry collector | the standard OpenTelemetry headers | none |

An owner token is asked for one piece of work, its **purpose**:

| Purpose | When | Cores |
|---|---|---|
| `resolve` | an experiment's create checks that every pin it uses exists, that each environment image passes Cella's admission, and its budget, and with the `lux` key source creates the Budget for a spending cap (spec 003); an experiment's budget figures are read (below) | Topos (agent versions), Lux (models, the Budget), Origo (commits), Arca (workspace paths and digests), Cella (images) |
| `trial` | a cell's rollouts: submitting, following, resizing and canceling them, the cost check's lease, and the Cella Secret of a `cella` rollout with its leased Key; copying a task's inputs and references into the experiment's folder and the links that deliver them; an external trial's check sandbox, its Lux records, and the outputs its external harness wrote into the owner's Arca space; and an owner token for each audience a `program` candidate's `credentials` name, delivered as a Cella Secret (spec 005) | Topos, Cella, Lux, Arca, Origo, and each credential audience |
| `judge` | a model judge's calls through its leased Key, an agent judge's Topos session, and the evidence either reads (spec 006) | Lux, Topos, Arca |
| `import` | reading a finished session or sandbox and its records (spec 005) | Topos, Cella, Lux, Arca |
| `export` | writing a dataset (spec 007) | Arca |
| `report` | a narrative draft's call through its leased Key, and a report's HTML bundle (spec 010) | Lux, Arca |
| `publish` | a publication version's copies into its folder, its secret scan's sandbox, the Arca link of its folder, replaced when the publication is made `private` or its link token replaced and revoked at a withdrawal (spec 013); and the input links a `prepare` job mints on a published benchmark's files for one run, as the benchmark's owner (spec 005) | Arca, Cella |

The token's subject is the caller, for work done during a request, and
the experiment's or export's `created_by`, or the version's
`published_by`, for work done later. Examen
asks for a token at first need for one owner, subject, audience,
experiment and purpose, keeps it in memory until `OWNER_TOKEN_RENEWAL`
before its expiry, and never uses it longer than
`OWNER_TOKEN_MAX_LIFETIME` after obtaining it.

**Model access.** Lux's model doors take Keys. Examen's own calls, a
model judge's, a `cella` rollout's through its Lux Secret, and a
narrative draft's, go through Keys it leases from the **key source**,
an interface of `internal/keys` with two implementations (below),
`endpoint` and, after v0.1, `lux`. With neither, no Key is leased, and
the installation serves the v0.1 profile (spec 003). A
`topos` trial's Keys are not Examen's: Topos issues them to its
sessions (spec 005, R10).

```go
package keys

// A Source leases the Lux Keys an experiment's own calls go through.
type Source interface {
	// Lease answers one Key for work of one experiment.
	Lease(ctx context.Context, r Request) (Lease, error)
	// Release ends a lease: the Key stops serving. Releasing a Key
	// already released is no error.
	Release(ctx context.Context, keyID string) error
	// Budget answers the figures of an experiment's Budget.
	Budget(ctx context.Context, r Request) (Budget, error)
}

// Budget is what an experiment's answer shows of its Budget.
type Budget struct {
	Name      string
	Cap       int64  // micro-units: the Budget's amount
	Spent     int64  // micro-units: spent in its current window
	Remaining int64  // micro-units: the smallest remainder across every Budget the experiment's Keys draw on
	Currency  string
}

// Request names whose work a Key is for and what it may call.
type Request struct {
	Owner      string            // the experiment's owner
	Subject    string            // on whose behalf: the caller, or the experiment's created_by
	Experiment string            // the exp_ id
	Purpose    string            // "trial", "judge" or "report"
	Models     []string          // the only models the Key may call
	Surface    string            // the one Lux surface the Key may call (spec 005): a program's surface; "lux" for a judge and a narrative draft
	Labels     map[string]string // the Key's labels (spec 005)
	TTL        time.Duration     // the longest the work can last
}

// Lease is one Key and what is left to spend through it.
type Lease struct {
	Value     string    // the Key's value, held in memory only
	ID        string    // the Key's id at Lux
	Expires   time.Time // when the Key stops serving
	Remaining int64     // micro-units: the smallest remainder across every Budget the Key draws on
	Currency  string
}
```

| Source | Used when | `Lease` | `Release` | `Remaining` |
|---|---|---|---|---|
| none | v0.1, with `EXAMEN_KEYS_URL` unset: the v0.1 profile | never asked: a cost check whose estimate is zero leases nothing (spec 005), and work that would need a Key is refused at create `422 core_not_configured` naming `lux` (spec 003) | never asked | none: an experiment's answer carries no budget figures |
| `lux`, after v0.1 | `EXAMEN_KEYS_URL` is unset and Lux and an owner token source are configured: Examen runs on its own | creates a Key at Lux's control plane with a Lux owner token of the request's purpose: `spec.budget` the experiment's Budget, `models`, the surface, `allowUnpriced` false, the labels and a `ttl`, so the Key's `owner` is the owner and the owner is its writer | deletes the Key | the experiment's Budget's `status.remaining`, read as the owner |
| `endpoint` | `EXAMEN_KEYS_URL` is set: the installation keeps its own budgets | asks the installation's key endpoint (below) | `DELETE {EXAMEN_KEYS_URL}/{key id}` with the same bearer | what the endpoint answers: the smallest remainder across every Budget the Key draws on, such as the experiment's, a wallet that pays for it and a budget line above them |

`Budget` reads the figures an experiment's answer carries (spec 003).
The `lux` source reads the experiment's Budget at Lux as the owner with
the purpose `resolve`: `spec.amount`, `status.spent` and
`status.remaining`, money strings it turns into micro-units, and
`spec.currency`. The `endpoint` source posts `{owner, subject,
experiment, budget}` to `{EXAMEN_KEYS_URL}/budget` with the same bearer
and reads `{name, cap, spent, remaining, currency}`, `remaining` the
smallest remainder across every Budget a Key of the experiment would
draw on. The answer is kept for `BUDGET_STATUS_TTL` per experiment, and
any failure leaves the figures out of the experiment's answer and fails
nothing.

With `endpoint`, Examen writes no Key and no Budget at Lux: the
installation creates, scopes and ends them, so no experiment bypasses a
cap a budget owner set there, and it decides each Key's data terms,
such as whether a provider that keeps what it is sent may serve a
judge. With `lux`, a Key takes Lux's defaults for data terms. Examen
never assigns an owner on a create made with its own identity. A Key's
value is held in memory for the calls it serves, and for a `cella`
trial in the Cella Secret that carries it; it is never written to
Postgres, an event, a log line or an error, and the Key is released
when its work ends (specs 005 and 006).

**The key endpoint.** The `endpoint` source posts one request per
lease:

```json
POST {EXAMEN_KEYS_URL}
Authorization: Bearer <the content of EXAMEN_KEYS_TOKEN_FILE>

{ "owner": "https://issuer.example|9ab3",
  "subject": "https://issuer.example|9ab3",
  "experiment": "exp_01jb…", "purpose": "judge",
  "models": ["<the grader's model id>"], "surface": "lux",
  "labels": { "examen.experiment": "exp_01jb…", "examen.grader": "grd_01jb…" },
  "ttl": 86400 }
```

| Answer | During a request | In unattended work |
|---|---|---|
| `200` with `{key, key_id, expires_at, remaining, currency}` | used | used |
| `402`: no Budget the Key would draw on has money left | `422 budget_refused` | the cell parks `over_budget`, `parked_reason` `budget_exhausted` |
| `403`, optionally with `{reason}` | `403 forbidden`, the reason as `details.reason`, `key_refused` when absent | the cell parks `blocked`, `parked_reason` the reason or `key_refused` |
| anything else, a body that does not parse or lacks a member, an expiry already past, or no answer within `authz.Timeout`, after one retry on a connection failure | `503 owner_tokens_unavailable`, `details.reason` `key_source`, retryable | the work waits and is tried again; nothing parks for an outage |

The endpoint should scope the Key to `models` and `surface`, carry
`labels`, and let it serve no longer than `ttl`; Examen holds a Key until its expiry and
never past it. Neither the endpoint's address nor its bearer nor a byte
of a Key is in an error.

**Inside a trial** Examen places no token's value. A Topos trial
reaches models and its machine with the credentials Topos gives its
sessions; a `cella` trial reaches Lux through the Key in its Secret,
and each API its program's `credentials` name through an owner token in
a Secret of its own, which Cella's egress gateway substitutes for a
placeholder at the last hop, only on a request to the Secret's host. A
credential's token lasts at most `OWNER_TOKEN_MAX_LIFETIME`; Examen
replaces the Secret's value `OWNER_TOKEN_RENEWAL` before it expires,
which Cella applies without touching the sandbox (spec 005). An
external harness holds whatever credentials its owner gave it; Examen
gives it none.

**Attribution check.** Where a core's answer names the owner it
recorded for what Examen created, such as the `owner` of a Key the
`lux` source created, Examen compares it with the experiment's owner. A mismatch deletes or ends the
object, and the cell parks `blocked` with `parked_reason`
`owner_mismatch`.

### The minter

The minter is an HTTP endpoint the operator provides, usually in front
of the installation's identity provider. Examen asks it for one owner
token at a time, with a bearer of its own. Examen offers no route that
turns one token into another, and asks the minter only for work the
authorizer allowed: the caller's action during a request;
`experiment.run` for an experiment's unattended work; for a
publication version's `publish` job, which runs after its request on
behalf of `published_by`, the `publication.create` or
`publication.update` that asked for the version, with a withdrawal's
revokes and a change to `private`'s or a new link token's link
replacements covered by their own requests (spec 013); or, for the
`prepare` job of an experiment run through another owner's published
benchmark, the `suite.use` that let its create run it: the job asks for
an `arca` token of the benchmark's owner on behalf of the version's
`published_by`, with the purpose `publish`, to mint that run's input
links (spec 005). While a benchmark runs on its owner's side alone
(spec 013), no create runs another owner's benchmark and this ask is
never made; the design is kept so that decision can be reversed.

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
| `audience` | the core's audience, from `EXAMEN_TOPOS_AUDIENCE`, `EXAMEN_CELLA_AUDIENCE`, `EXAMEN_LUX_AUDIENCE`, `EXAMEN_ARCA_AUDIENCE` or `EXAMEN_ORIGO_AUDIENCE`; or, with the purpose `trial`, an audience a `program` candidate's `credentials` name and the authorizer's `credential_audiences` hold, which is never one of those five nor Examen's own, so a minter can tell a program's credential from Examen's own work by the audience alone |
| `claims` | `examen: {experiment, purpose}`, or `examen: {publication, purpose}` for the purpose `publish`, and `examen: {publication, experiment, purpose}` for a `prepare` job's input links, for the minter to place on the token where its issuer can (above) |
| `ttl` | the lifetime asked: `OWNER_TOKEN_MAX_LIFETIME`, in seconds |
| `token`, `expires_in` | the answer: the token and its lifetime in seconds |

The minter's obligations, which the cores and not Examen check: the
token is addressed to `audience` alone and verifies against that core's
issuers; its identity is `subject` acting in `owner`'s context, as the
installation's issuer writes a context, and for a personal owner the two
are one; it carries `claims` verbatim when the installation's issuer
can place them, so the core's authorizer, which receives every verified
claim, can attribute the call to the experiment and refuse a token whose
experiment it knows has ended. An issuer that cannot place claims mints
a plain token for the subject, and the installation attributes the call
through its own experiment record and the labels Examen sets on what it
creates (spec 005); Examen behaves the same either way. It lasts no
longer than `ttl`; and the minter answers `403` for an owner and subject
it does not let Examen act for.

A `403` may carry a body `{"reason": "sign_in_not_kept"}`: the minter
mints from a sign-in the subject left with the installation for work
they started, and it keeps none for this subject's work, as for a
client whose sign-in the installation does not keep. Because work the
subject started may run long after they leave, Examen asks the minter
at an experiment's create (the `resolve` purpose), so an experiment
whose later work could not be attributed is refused before it exists.
Any other reason, or none, reads as `owner_token_refused`. The request
and the `200` answer are otherwise as above.

| Minter answer | During a request | In unattended work |
|---|---|---|
| `200` with `token` and `expires_in` | used | used |
| `403` with reason `sign_in_not_kept` | `403 sign_in_not_kept` | the cell parks `blocked`, `parked_reason` `sign_in_not_kept`; an export or a publication version fails with the same reason |
| `403` otherwise | `403 forbidden`, `details.reason` `owner_token_refused` | the cell parks `blocked`, `parked_reason` `owner_token_refused`; an export or a publication version fails with the same reason |
| anything else, or no answer within `authz.Timeout`, after one retry on a connection failure as `authz.Retryable` judges it | `503 owner_tokens_unavailable`, retryable | the work waits and is tried again; nothing parks for an outage |

A minted token is opaque to Examen, which reads none of its claims. It
is kept in memory alone, and never written to Postgres, an event, a
span, a log line or an error.

### Owner token files

Token files come after v0.1; the design is kept here so the first
release leaves room for them. An installation run by one owner may
receive that owner's tokens instead. `EXAMEN_SOLE_OWNER` names the owner, a rendered subject, and
`EXAMEN_OWNER_TOKEN_DIR` a directory holding one file per core, named by
that core's configured audience (`topos`, `cella`, `lux`, `arca`,
`origo` by default), each holding a bearer of that owner for that core,
and one file per credential audience a `program` candidate may be
given, named by that audience.
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
the v0.1 profile (spec 003): an experiment's create refuses the work
that would reach a core `422 core_not_configured`, and any other
request that needs one answers `503 owner_tokens_unavailable` with
reason `not_configured`, not retryable.

### Unattended work

Before each step Examen takes for an experiment with no caller present
(a cell's smoke, main or retry rollout, a judge's run over its trials),
Examen asks `experiment.run`. The question's subject is the experiment's
`created_by`, its issuer and `sub` apart, and its claims are empty,
since Examen holds no token of that subject: an authorizer decides it
from its own state, as it decides a scheduled start it did not see a
person make, such as whether `created_by` still belongs to the owner's
context and whether the experiment has ended. The resource is the
stored experiment with its `state`, `purpose`, `cell` and `executor`.
The answer is cached for its `ttl`, since it carries an id, so a cell's
trials do not each ask.

| Answer | The step |
|---|---|
| allow | Examen obtains its owner tokens and acts |
| deny | the cell parks `canceled` with `parked_reason` `run_denied`, and the step is not taken |
| unavailable | the step waits and is tried again; nothing parks for an outage |

So an owner who loses the right to run, or an experiment an operator
stops, ends its work within one `ttl`, without Examen deciding.

### What a handler depends on

Four interfaces of `internal/access`, `internal/owner` and
`internal/keys`, and a handler cannot tell what is behind any of them.
One turns a bearer into a caller: the subject, its issuer and `sub`
apart, and every verified claim. One answers a question with a
decision: allow or deny, the reason, the limits, the filter. One
answers an owner token for an owner, a subject, an audience, an
experiment and a purpose, from the minter, from the token files, or not
at all. One leases a Key for an experiment and a purpose, from Lux or
from the installation's key endpoint. Every route is written the same
way: authenticate, build the resource from the request and the stored
object, ask, act within the decision, and reach a core only through an
owner token or a leased Key.

The routes are kept as data, one row per route of spec 003 with the
action and the fields it sends, held to spec 003 and to `authorizer` by
a test.

### Limits of this spec

The constants of spec 002 this spec holds: `OWNER_TOKEN_MAX_LIFETIME`,
`OWNER_TOKEN_RENEWAL`, `MAX_PARALLELISM`, `DEFAULT_PARALLELISM`,
`MAX_TRIALS_PER_CELL`, `MAX_INFRA_RETRIES`,
`MAX_ACTIVE_EXPERIMENTS_PER_OWNER`, `BUDGET_STATUS_TTL` and
`PUBLICATION_LINE_MAX_BYTES`.

## Not in this spec

Browser sessions and cookies: a person reaches Examen through a client.
A token exchange route, a delegation claim, or a chain of actors: a
token's subject is the person on whose behalf the work happens, and the
minter is the one place an installation issues tokens for Examen's
work. Administrators, beyond asking `publication.pause` of whoever an
authorizer lets pause. Rate limits on requests. How a core's authorizer
reads the `examen` claim, which is that core's. What each way of
sharing grants, which is spec 013's; evidence grants of Arca's own.
Reviewing a listing, which is the installation's. Deciding by an
object's content beyond the fields sent.

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
  with nothing done; every public route asks `publication.read` with no
  subject before it answers.
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
- A deny of `experiment.create` with the reason `sign_in_not_kept` is
  `403 sign_in_not_kept`; a deny with any other reason is `403
  forbidden` with that reason; a deny about a stored object is its
  `404` whatever the reason.
- Under the owner policy, `publication.list` and `publication.pause` are
  denied for every subject, the owner included; `publication.read` with
  no subject is allowed for a key that resolved and denied otherwise;
  `suite.use` through a benchmark, with `publication` set, is denied,
  whether `published` or `shared` is set; a deny of
  `publication.read` with no subject answers exactly as an unknown key.
- Under the owner policy, a subject an experiment is shared with reads
  it, and is denied every action that runs it: `experiment.update`,
  `cell.retry`, `experiment.cancel`, `experiment.import`, `trial.lease`
  and `trial.submit`, each answered as the experiment's `404`.
- Under the owner policy, `shared` opens reads alone: for every action
  of the vocabulary that is not a read, a question about another
  owner's object that carries `shared` true is denied `not_owner`;
  `suite.use` and `baseline.use` of another owner's suite or baseline
  are denied whether `shared` is set or not; and an `experiment.create`
  or `experiment.update` whose `suite_owner` or `baseline_owner` is not
  the subject is denied `not_owner`.
- An experiment created in a context the authorizer's `owner` limit
  names records that owner as its `owner` and the caller as its
  `created_by`, answers both on every read, and its unattended work asks
  `experiment.run` with that `created_by` as the subject.
- An allow's `attribution` replaces the name a version's request gave,
  and an empty or overlong one is `503 authorizer_unavailable`; an
  allow's `budget` on a create that gave `budget_cap_usd_micro` becomes
  the experiment's Budget, and without one the `lux` source, after
  v0.1, creates a hard Budget of that cap as the owner, the `endpoint`
  source refuses `422 budget_refused`, and in the v0.1 profile the cap
  is recorded with no Budget and no request to Lux.
- With no minter, no token files and no key source, an experiment of
  `external` candidates whose `grader_kinds` hold no `check`,
  `model_judge` or `agent_judge` is created, run and compared with no
  question to a minter and no request to any core; `experiment.create`
  carries `grader_kinds`.
- `Budget` of either key source answers the experiment's cap, spend and
  remainder, reused for `BUDGET_STATUS_TTL`; a failure leaves them out
  and fails no read.
- The owner policy answers the contract's conformance suite and narrows
  an allow by the grants of a token; a server whose authorizer allows
  the probe does not start.
- Against stub cores that record every request, every request to Topos,
  Cella, Lux, Arca and Origo carries a token the stub minter minted for
  the experiment's owner, the right audience and the right purpose; none
  carries Examen's authorizer or minter bearer, and none carries a
  caller's bearer.
- A canary planted as a minted token, a token file's content and a Lux
  Key's value, from either key source, is found only in requests to the
  core it belongs to, and in no response, log line, span, event, error
  or database row.
- A minter `403` parks the cell `blocked` with `owner_token_refused`,
  and with the reason `sign_in_not_kept` refuses a create `403
  sign_in_not_kept` and parks unattended work `blocked` with
  `sign_in_not_kept`; a minter outage parks nothing, and the work
  resumes when the minter answers.
- With the `endpoint` key source, no request to Lux creates or deletes a
  Key or a Budget, every Key Examen uses comes from the endpoint and is
  released when its work ends, and a `402` parks the cell `over_budget`;
  with the `lux` source, every Key names the experiment's Budget, only
  the requested models and no unpriced model, and is deleted at its
  work's end.
- `experiment.run` carries the experiment's `state`, and `cell.retry`
  reaches the authorizer as its own action with the cell's parked state
  and reason; `trial.lease` and `trial.submit` reach it as their own
  actions with the candidate, its `harness` and the lease, and the owner
  policy allows them to the owner alone.
- A question with no subject carries no peer address and no user agent,
  and two readers of one published version hit one cached answer.
- A `program` candidate whose credential names a core audience or
  Examen's own is refused at apply `422 credential_audience_reserved`,
  whatever the authorizer holds.
- A `program` candidate whose credential names an audience outside the
  allow's `credential_audiences`, or any audience with no authorizer, is
  refused `403 forbidden` with reason `credential_audiences`; an allowed
  one is minted with the purpose `trial` for that audience, reaches the
  sandbox only as a Secret scoped to its `host`, and is in no response,
  log line, span, event, error or database row, by canary.
- A deny of `experiment.run` parks the cell `canceled` with
  `run_denied` and starts no trial; an outage holds the work.
- A Key whose recorded owner differs from the experiment's owner is
  deleted and the cell parks `blocked` with `owner_mismatch`.
- With `EXAMEN_SOLE_OWNER` set, work for that owner uses the token
  files, a refreshed file is read within `OWNER_TOKEN_RENEWAL`, and any
  other owner's request that needs a core is `503
  owner_tokens_unavailable` with reason `sole_owner`.
