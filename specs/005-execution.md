---
title: "Execution: the executor contract, the topos, cella and external executors, a task's inputs and references, the HTTP runner, the check sandbox, smoke trials and the cost check, the work queue, retries and retrying a parked cell, timeouts, cancellation, adoption, imported trials and external past trials, and where cost and provenance are read"
status: in-progress
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
affects: [internal/execute/, internal/execute/topos/, internal/execute/cella/, internal/execute/external/, internal/queue/, internal/luxread/, cmd/examen-trial/, cmd/examen-http/]
effort: xlarge
created: 2026-10-17
updated: 2026-10-19
author: changkun
---

# Execution

## Overview

Examen runs no trial itself. It hands each cell's trials to an executor,
which asks the core that runs agents or sandboxes to run them, or hands
them to the owner's own external harness, and reads back what happened. This
spec fixes the executor contract and the three executors: `topos` for a
candidate of kind `topos`, `cella` for a candidate of kind `program`,
and `external` for a candidate of kind `external`. It fixes how a
task's inputs reach the system under test and its references reach
only the checks, the HTTP runner that lets a `program` candidate
evaluate an API, and the check sandbox in which a `cella` or `external`
trial's checks run, with network `none`, apart from where its system
under test ran. It also fixes what examend owns around the executors: the
work queue that spreads an experiment's parallelism across its cells,
the smoke trials and the cost check, retries of infrastructure failures
with their lineage, what happens when a Budget runs out, retrying a
parked cell, timeouts, cancellation, adoption of trials an identical
cell already ran, trials imported from sessions and sandboxes Examen
did not start and from an external harness's past trials, and where each
trial's cost and its sent and observed provenance are read. It ends
with the v0.1 profile, what an installation with no owner token
source, key source or file store runs, which is what the first release
serves.

Neither the `topos` nor the `cella` executor can run today as written.
Topos has sessions, budgets and metadata but no rollout primitive, no
model override at create and no checks in the machine; Cella's
SandboxSet is designed (Cella spec 020) and not built. What the `topos`
executor needs from Topos is listed below as numbered requirements,
from which Topos's rollout spec is written. The `external` executor
needs from Cella only a single sandbox, its file routes and exec, which
are built; a check that names an `image` also needs Cella's image
Volumes (Cella spec 019), on every executor.

## Design

### Rollouts of a cell

A cell's trials execute in rollouts. A rollout is one submission to the
cell's executor: a set of the cell's trials, run under one parallelism
bound and one key. A cell has at most three kinds:

| Rollout | Key | Trials |
|---|---|---|
| smoke | `<cell id>.smoke`, and `<cell id>.smoke.<round>` when a cell's retry runs its smoke again, rounds counting from 1 | indices 0 to `SMOKE_TRIALS` - 1 |
| main | `<cell id>.main` | every remaining index up to N - 1 |
| retry | `<cell id>.retry.<round>` | one new trial for each trial of the previous rollouts that ended `infra_failed` with retries left, or, at a cell's retry, for each index without a finished trial; rounds count from 1 |

A cell with N at most `SMOKE_TRIALS` has no main rollout. An adopted
cell (below) runs only the indices adoption did not fill.

Separate rollouts, not one rollout of N held after its smoke trials, is
a choice. Cella's SandboxSet has a mutable `parallelism` but no hold:
with `parallelism` at `SMOKE_TRIALS` it admits the next replica the
moment the smoke replica ends, before the smoke trial is graded and the
estimate made, and its `replicas` cannot grow for a retry. Separate
rollouts need nothing from either core for smoke or retries. A cell
therefore keeps a list of rollouts, its `rollouts` (spec 002), each
`{key, kind, executor, handle, parallelism, key_id, state}`: `queued` until the
work queue gives it a share, `submitting`, `submitted` with its handle,
and `ended` once every trial of it is `done`. An agent judge's rollouts
(spec 006) are kept in the same list.

### The executor contract

```go
package execute

// An Executor runs the trials of a cell through the core that runs
// them. examend holds one per candidate kind; the work queue is its
// only caller.
type Executor interface {
	// Name is "topos", "cella" or "external".
	Name() string
	// Submit starts r, or answers the handle of the rollout already
	// started under r.Key when its content is the same.
	Submit(ctx context.Context, r Rollout) (Handle, error)
	// Report answers the current state of every trial of the rollout.
	Report(ctx context.Context, h Handle) (Report, error)
	// Resize sets how many of the rollout's trials may run at once.
	Resize(ctx context.Context, h Handle, parallelism int) error
	// Cancel stops the named trials, or every trial not yet done when
	// none is named.
	Cancel(ctx context.Context, h Handle, trials ...string) error
	// Import reads one finished session or sandbox, named by ref, as a
	// trial of owner. The external executor's is never called, since an
	// import names topos or cella.
	Import(ctx context.Context, owner, ref string) (TrialReport, error)
}

// Handle names a submitted rollout: a Topos rollout id, a Cella
// SandboxSet id, or for the external executor the rollout's key.
type Handle struct {
	Executor string
	ID       string
}
```

A `Rollout` carries:

| Field | Type | Meaning |
|---|---|---|
| `Key` | string | the rollout's key from the table above; unique per owner at the core |
| `Owner` | string | the experiment's owner; every call the executor makes is made as this subject (spec 004) |
| `Experiment`, `Cell` | string | the `exp_` and `cel_` ids |
| `Trials` | []`{ID, Index, RetryOf}` | the trials to run, in index order; `ID` is the `tri_` id examend minted before submitting |
| `Parallelism` | int | at most this many of its trials running or checking at once; 1 or more |
| `Task` | resolved task | `instruction` when the task has one, `workspace`, `environment` (image digest, resources, network policy), `timeout` (resolved: the task's, else `DEFAULT_TRIAL_TIMEOUT`), `max_turns`, `evidence` paths, `outputs`, and the experiment's folders of the task's `inputs` and `reference` with each file's name and SHA-256 (Task inputs, references and outputs, below) |
| `Checks` | []resolved check grader | in the task's order: the grader digest, `command`, `workdir`, `env`, `files`, `image` |
| `Candidate` | resolved candidate | the `topos`, `program` or `external` object, every field pinned |
| `Budget` | string | the experiment's Lux Budget |
| `Lease` | lease | for a `cella` rollout whose candidate names a `model`: the Lux Key leased from the key source for it (spec 004); absent otherwise, and for a `topos` rollout, whose sessions get their Keys from Topos (R10) |
| `Credentials` | []`{Audience, Host, Env, File}` | for a `cella` rollout: the candidate's `credentials`, each with the owner token examend obtains for its audience with the purpose `trial` (spec 004), held in memory for the Cella Secret that carries it |
| `Evidence` | `{Owner, Prefix}` | for a `cella` or `external` rollout: the Arca space and the folder `files/evals/experiments/<experiment id>/cells/<cell id>/trials`, under which trial `t`'s evidence is written at `<Prefix>/<t>/` in spec 007's layout; absent for a `topos` rollout, whose sessions keep their evidence in their own folders (R9) |
| `Labels` | map | `examen.experiment`, `examen.cell`, `examen.rollout`; each trial adds `examen.trial` and `examen.index` |

The prefix `examen.` is reserved for these labels; spec 003 refuses it
on any `metadata` key a client supplies. The executor writes the labels
to every object it causes to exist: Topos session metadata, Cella
labels, Lux Key labels. Every value is written verbatim: a trial's is
its id, `tri_01j…`, underscore included.

A `Report` is `{Ended bool, Trials []TrialReport, Unattributed
Usage}`: one trial report per submitted trial, in the submitted order,
`Ended` true when every trial is `done`, and the rollout's spend that no
trial carried (below). A `TrialReport` carries:

| Field | Meaning |
|---|---|
| `Trial`, `Index` | the trial as submitted |
| `ExecutorRef` | the Topos session id, the replica's sandbox name, or the `ref` an external harness submitted; `trial.executor_ref` |
| `State` | `pending`, `running`, `checking`, `done` |
| `Outcome` | once `done`: one of spec 002's outcomes |
| `Failure` | for `infra_failed`, `over_budget` and `canceled`: `{code, detail}`, a stable code and a developer's detail |
| `Sent` | each model request as it was sent: `{at, model, reasoning, lux_request}`; for a `topos` trial from the session's records, and for a `cella` or `external` trial from Lux's records of the trial's requests, never from a file the program could write (below) |
| `Lux` | where Lux's records of the trial are: `{keys []string, requests []string, request_label string, by_owner bool}`, the Keys that served it, the request ids an external harness submitted, the label that marks its records, and whether the owner's records are read as well as the Keys' (below) |
| `Executor` | what the executor's own records add to `observed`: per request `{lux_request, usage, cost_micro}` from Topos, the program's exit code and the wall times from Cella |
| `ServiceVersions` | the `service_versions` a program wrote to `/examen/observed.json` or an external harness submitted: an object from a service's name to its version; empty for a `topos` trial |
| `ServiceUsage` | the `service_usage` a program wrote to `/examen/observed.json` or an external harness submitted: an object from a service's name to `{cost_usd_micro, units}`, what that API reported its own usage of the trial to be (Cost and the sent and observed layers, below); empty for a `topos` trial |
| `Declared` | for an `external` trial: the `declared` its external harness submitted, `{harness_version, image}` |
| `Checks` | for a `topos` trial: one `CheckRun` per check, in order: `{grader, state, exit_code, stdout, stdout_size, stdout_sha256, stderr_tail, duration}`, `stdout` at most `CHECK_MAX_OUTPUT_BYTES` and the size and digest of the whole; a check that did not run to its end does not change the trial's outcome; spec 006 turns it into measurements. A `cella` or `external` trial's report carries none: its checks run afterward in the check sandbox (below), which records the same runs |
| `Evidence` | each evidence object, as spec 007's evidence ref: its kind, name, path, SHA-256 digest computed while writing, size, and Arca's `checksum` and `checksum_kind`; for a `topos` trial, the objects its session keeps in its own folder, as the rollout reports them (R9) |
| `StartedAt`, `AgentEndedAt`, `EndedAt` | the agent's first step, the agent's end, and the trial's end after its checks and evidence |
| `Tuple` | on an imported trial only: the provenance tuple read from the records |

The wire semantics every executor keeps:

1. **Submit is idempotent by key.** A second `Submit` with the same
   `Key` and the same content answers the first rollout's handle; with
   other content it is `ErrConflict`, and nothing starts. The key holds
   for the rollout's whole life at the core, not for a window.
2. **Report is complete.** Every call answers every trial's current
   state, never a change since the last call, so a caller that lost
   every earlier answer rebuilds the whole rollout from one.
3. **Application moves forward.** examend applies a report as an upsert
   keyed by trial id, and a state moves only along `pending`,
   `running`, `checking`, `done`. A report that moves a trial backward
   is logged as an executor fault and not applied.
4. **A done trial is final.** Once examend has applied `done`, no later
   report changes the trial; one that differs is logged as an executor
   fault and ignored.
5. **Cancel and Resize are safe to repeat**, and act only on trials not
   yet `done`.
6. **Errors are classified.** `ErrConflict` (a key reused with other
   content), `ErrRefused{Code}` (the core or its authorizer refused:
   the cell parks `blocked` with the code as `parked_reason`),
   `ErrUnavailable` (transient: the job is retried with backoff and no
   trial's retry is spent), `ErrNotFound` (the rollout is gone at the
   core: every trial not `done` ends `infra_failed` with code
   `rollout_lost`).
7. **Pull only.** examend calls the executor; neither core calls
   examend, and examend serves no route an executor reports to. An
   external harness is not a core: it is a client of the owner's, and
   calls examend's lease and submit routes (spec 003) with a bearer the
   authorizer decides on, as any client does.
8. **No credential of its own.** Every call is the owner's (spec 004);
   a run that cannot be attributed to the owner does not start.

### Outcomes

A trial ends `done` with one outcome. The rules both executors follow:

| Outcome | When | Retried |
|---|---|---|
| `completed` | the agent ended its work itself | no |
| `declined` | the provider ended the agent's last response with a refusal (the IR stop reason `refusal`) | no |
| `timed_out` | the task's `timeout` passed while the agent ran | no |
| `turn_limit` | the harness stopped the agent at a bound other than time or money: `max_turns`, the model's output limit, or a stop for repeating a failing call | no |
| `over_budget` | Lux refused a request for spend (`budget_exhausted`, `spend_exceeded`): a Budget the trial's Key draws on, or the wallet behind it, ran out | not automatically: the cell parks `over_budget` (below), and its retry runs the index again |
| `infra_failed` | the trial could not be run to an outcome for a reason outside the agent: the machine could not start or was lost, the gateway did not answer or rejected the request, the workspace could not be delivered, the machine was lost during the checks, evidence could not be written, the deadline below passed | yes, up to `MAX_INFRA_RETRIES` |
| `canceled` | the experiment was canceled, or the trial's cell parked, before the trial ended | not automatically: a cell's retry runs the index again |

The executor sets the outcome once and examend never changes it (rule
4). An external trial's outcome is the one its external harness submitted, any
but `canceled`, which only Examen sets. A `canceled` or `over_budget`
trial, like an `infra_failed` one, is not a finished trial and is
excluded from analysis (spec 002): how much money was left says nothing
about the agent. The outcome `declined` is the executor's hard signal
only; the metric `declined`, which also covers an agent that refused or
gave up in its own words, is the decline judge's (spec 006), and the
judge never changes the outcome.

`turn_limit` covers every harness stop that is the agent's own doing
(spec 002); on `topos`, a bound on the agent's steps needs R7 below. It
is a result, and it reads `completed` false.

### The work queue

All of examend's work is jobs in one Postgres table, claimed with
`FOR UPDATE SKIP LOCKED` under a lease, so any number of examend
replicas share it and a replica that dies leaves its jobs to the next
claim when the lease ends. A replica runs at most `EXAMEN_WORKERS` jobs
at once (spec 011). Every job is idempotent: it reads the state it acts
on, and acting twice changes nothing the first act did not.

| Job | Does |
|---|---|
| `prepare` | copies one task's inputs and references into the experiment's folder (below), before the first rollout of the task is submitted |
| `submit` | submits one rollout; the rollout's row moves `submitting` to `submitted` with its handle |
| `report` | calls `Report` for one submitted rollout every `REPORT_INTERVAL`, applies it, and enqueues what follows: the check sandbox of each `cella` trial whose evidence is exported, grading of each trial that reached `done` (spec 006), the cost check, a retry round, parking on a spent Budget, the cell's next state; for a `cella` rollout it also replaces a credential Secret's value `OWNER_TOKEN_RENEWAL` before its token expires |
| `check_sandbox` | runs one `cella` or `external` trial's checks in its check sandbox (below), once the trial's outputs are exported or submitted |
| `cost_check` | the cost check, after a cell's smoke trials are graded and at a cell's retry |
| `resize` | sets a rollout's parallelism to its share |
| `watch` | the deadlines below and lapsed leases of external trials, every `REPORT_INTERVAL` |
| `cancel` | cancels an experiment's open rollouts |
| `usage` | reads Lux's records of a trial `USAGE_SETTLE_DELAY` after it ended |
| `publish` | builds one publication version, copies its files, runs its secret scan and creates its Arca links, or revokes a version's links after a withdrawal and replaces them after a change to `private` or a new link token, retried until Arca accepts (spec 013) |

An experiment's smoke rollouts are submitted before any main rollout,
so every cell is smoke-tested before any spends at scale. How cells
advance after that is the experiment's `scheduling` (spec 002), fixed
at create: under `task_by_task`, the default, cells advance task by
task, in the suite's order, and within a task the candidates' cells
advance together; under `interleaved`, every cell of every task
advances together (below). A retry rollout goes ahead of a main
rollout not yet submitted, since it finishes a cell already running.

### Parallelism across cells

An experiment's `parallelism` bounds its trials running or checking at
once, across all its cells. examend deals it to the experiment's open
rollouts one trial at a time, in the order its `scheduling` names,
until the parallelism is dealt or no rollout has an unfinished trial
left:

- **`task_by_task`**, the default: tasks in the suite's order, and
  within a task round robin across its cells' open rollouts in the
  experiment's candidate order. A task's cells take the parallelism
  before the next task's take any, so two candidates' cells of one
  task run in one window, and two tasks' cells generally do not.
- **`interleaved`**: a trial already running or checking keeps its
  place, and the rest of the parallelism goes round robin across the
  open rollouts of every cell of every task, by the trials' order:
  smoke trials, then retries, then main trials, each by index, and
  among trials of one index the cells in the suite's task order and,
  within a task, the experiment's candidate order. Every cell takes
  one trial before any takes a second, and a slot a finished trial
  frees goes to the next trial in that order rather than back to its
  own cell, so trial `i` of every cell runs alongside trial `i` of
  every other, whatever its task, and two tasks' cells of one
  experiment share one window as two candidates' cells do. An
  experiment that compares tasks with each other, such as two wordings
  of one instruction, declares it, so the comparison does not compare
  two times (spec 008).

Each core starts a rollout's trials in index order (R2), so trial `i`
of each cell dealt together runs alongside trial `i` of the others,
and two cells a comparison within one experiment reads share one time
window when the scheduling deals them together. A rollout's share is
what it was dealt, and one dealt nothing waits unsubmitted. The deal is
the same for every executor: a `topos` or `cella` rollout's share
reaches its core as `Resize`, and an `external` rollout's bounds what
may be leased (below). On every
`report`, the shares are dealt again and a rollout whose share changed
gets a `Resize`, never below 1 once submitted, since a SandboxSet's
`parallelism` has that floor. A trial counts against the
bound from its submission until it is `done`, pending trials included,
so the cores never hold more admitted trials than the experiment allows.
For an `external` rollout the share bounds how many of its trials may
be leased or checking at once.

### Smoke trials and the cost check

A cell runs its smoke rollout first and is `smoking` until the last
attempt at every smoke index is `done`, retries included, and graded by
every grader of kind `check`, `model_judge` and `agent_judge`
(annotations never hold a cell). Then:

1. **Blocked.** The cell parks `blocked` when a smoke trial's last
   attempt ended `infra_failed`, when a check run of a smoke trial did
   not end `ok` (spec 006), or when a judgment of a smoke trial ended
   in any state but `ok`, `refused` or `over_budget` (spec 006).
   `parked_reason` names the trial, the check or grader, and the code.
   An agent behavior outcome (`completed`, `declined`, `timed_out`,
   `turn_limit`) never blocks.
2. **The estimate.** Per trial, the estimate is the largest, over the
   smoke trials, of the trial's cost plus the cost of its grading, both
   from Lux's records. The cell's estimate is that times the trials
   left to run times `COST_ESTIMATE_MARGIN`.
3. **The remainder.** A cell whose estimate is zero, as an external
   cell's is when its smoke trials' cost is `unknown` and no grader
   spends (below), is admitted without a lease, since no remainder is
   below zero, unless it is a `cella` cell whose candidate names a
   `model`, which leases its main rollout's Key here as below; so an
   installation with no key source runs such cells (The v0.1 profile,
   below). Otherwise examend leases the cell's Key from the key source
   with the purpose `trial`, as the owner on behalf of `created_by`
   (spec 004), and reads its `remaining`: the smallest remainder across
   every Budget the Key draws on, which for the `lux` source is the
   experiment's Budget's `status.remaining` at that moment. A `cella`
   cell whose candidate names a `model` keeps the lease as its main
   rollout's Key; a `topos` cell releases it at once, since Topos issues
   its sessions' Keys under the same Budgets (R10), and so do a `cella`
   cell without a `model` and an `external` cell, whose external harness calls
   models on Keys its owner gave it. From the remainder examend
   subtracts the outstanding estimate of every other cell of the same
   Budget admitted past its cost check and not `complete`: its per-trial
   estimate times its trials not yet `done` times
   `COST_ESTIMATE_MARGIN`. This is admission bookkeeping recomputed from
   trial states, not a spend ledger: spend is only ever read from Lux.
4. **The check.** A cell whose estimate is above the remainder parks
   `over_budget`, with `parked_reason` naming both figures, and its
   lease is released; otherwise its main rollout is queued. A parked
   cell resumes only through a retry (below).

An external smoke trial whose cost is `unknown`, because Lux holds no
record of its calls, adds only its grading cost to the estimate: the
external harness's own spend is not drawn on the experiment's Budget unless its
owner arranged it, so it is neither estimated nor blocked on.
A smoke trial whose cost Lux cannot price (a record with `priced`
false) blocks the cell with `model_unpriced`: an estimate with an
unknown term would pass any check. With the `lux` key source, the
experiment's Budget must exist, the owner must be able to draw on it,
and it must be `hard: true`; a soft Budget is refused when the
experiment is applied, since it bounds nothing. With the `endpoint`
source the installation answers for its Budgets, and examend records
the Budget it was given (spec 004). A Budget's window may reset during
an experiment and raise the remainder; the check reads it as it stands.

Examen sets no per-trial cap of its own. A cap derived from the
smoke cost would cut off a trial that is legitimately dearer than the
smoke trial, and excluding it would bias the cell toward cheap trials;
the Budget's hard limit is the bound.

### A Budget that runs out

A trial that ends `over_budget`, smoke or not, or a judgment that ends
`over_budget` (spec 006), parks its cell `over_budget` as soon as a
report or the judgment shows it: examend cancels the cell's open
rollouts, so its trials not yet `done` end `canceled`, and
`parked_reason` names the trial and Lux's code. The `over_budget` trial
is excluded from analysis like an infrastructure failure (spec 002),
and nothing about it is retried automatically, since every attempt
would meet the same empty Budget. The cell waits for someone to refill
the Budget and retry it.

### Retrying a parked cell

`POST /cells/{id}/retry` (spec 003, `cell.retry`) takes a cell parked
`blocked` or `over_budget` back to the step that parked it:

- **A cell blocked by its smoke trials, or parked before they were all
  graded,** runs them again: a new trial
  for each smoke index, `retry_of` naming the last attempt, in the
  rollout `<cell id>.smoke.<round>`, and then the steps above.
- **Any other parked cell** first runs the cost check again, over the
  trials it has left to run. Past it, every index that holds no
  finished trial gets a new trial, `retry_of` naming the last attempt,
  in the cell's next retry rollout; an `infra_failed` index that had
  used its retries is run again too.

The trials a retry adds do not count against `MAX_INFRA_RETRIES`,
which bounds the automatic retries of one round of work. A retry that
parks again records the new reason; the earlier trials stay with their
outcomes and lineage.

### Retries and lineage

When a rollout has ended, every trial of it that ended `infra_failed`
whose index has had fewer than `MAX_INFRA_RETRIES` retries gets a new
trial: a new `tri_` id, the same `index`, `retry_of` naming it. They go
in the cell's next retry rollout. The retried trial stays, with its
outcome, failure code and evidence, and is excluded from analysis; the
chain of `retry_of` is its lineage. A trial whose index has used its
retries stays `infra_failed`, is also excluded from analysis, and
leaves the cell one result short of N until a retry of the cell (above)
runs it again; spec 008 reads what that does to verdicts. No other
outcome is retried automatically.

### Timeouts

| Bound | Covers | Enforced by | Past it |
|---|---|---|---|
| the task's resolved `timeout` | the agent's work, from its first step to its end | the executor | `timed_out` |
| `CHECK_TIMEOUT` | all of one trial's checks together | the executor, or examend's check sandbox job | spec 006 |
| the trial's deadline: `timeout` + `CHECK_TIMEOUT` + `TRIAL_GRACE` from `StartedAt`, and for a `cella` trial `timeout` + `TRIAL_GRACE` | the whole trial, machine start and evidence writing included, and a `topos` trial's checks | examend's `watch` | `Cancel` of that trial, then `infra_failed` with code `deadline_passed` |
| `TRIAL_START_TIMEOUT` | a pending trial of a rollout running fewer trials than its parallelism, counted from the later of the rollout's submission and the last end of one of its trials; not an `external` trial, which waits for an external harness to lease it | examend's `watch` | `Cancel` of that trial, then `infra_failed` with code `not_started` |
| `EXTERNAL_LEASE` | a leased external trial, from its lease or its last renewal | examend's `watch` | the trial returns to pending with its id, its lease ends, and no retry is spent |
| the check deadline: `CHECK_TIMEOUT` + `TRIAL_GRACE` from a `cella` trial's export, or an external trial's submit or import | its check sandbox and evidence | examend's `watch` | its check sandbox is deleted, then `infra_failed` with code `deadline_passed` |

The deadline exists for an executor that stops reporting; the executor
enforces the first two bounds itself. An external trial's `timeout` is
its external harness's to keep: the lease carries it, and Examen sees only the
times the external harness submits.

### Cancellation

Canceling an experiment parks every cell not `complete` as `canceled`
and enqueues one `cancel` job, which calls `Cancel` on every open
rollout of the experiment. A trial not yet `done` when its rollout is
canceled ends `canceled`; what it wrote to Arca stays and is referenced.
No grading starts after the cancel, and grading jobs already queued for
the experiment are dropped. A judge call already sent completes and its
measurement is kept. Spend after a cancel is bounded by how fast each
core stops: the `cella` executor releases the rollout's Key to the key
source and deletes its credential Secrets in the same job, the `topos`
executor relies on R13 below, and the `external` executor ends every
lease of the experiment, so a later submit is `409 lease_not_held`; the
check sandboxes of `cella` and `external` trials are deleted.

### Restarts and resubmission

Every rollout, trial and job is a Postgres row, so a restarted examend
resumes from them. A rollout row in `submitting` is submitted again
under its key and gets the handle of the rollout the first attempt
started, if it started one (contract rule 1). A rollout row in
`submitted` is reported again, and the report rebuilds every trial
(rule 2). A trial id is minted before its rollout is submitted and
carried to the core, so a trial is never run twice under two ids. A
resubmission never re-mints a trial.

### Adoption

Adoption comes after v0.1, a later build step than the console path;
the design is kept here so the first release leaves room for it. With `adopt` true, a
cell may take trials an identical cell already ran instead of running
them again.

- **Identical** is an equal provenance tuple, every field of it, grader
  digests included, within the same owner. Trials are never adopted
  across owners, a shared suite or baseline included (spec 012).
- **The source** is the most recent `complete` cell with an equal tuple,
  and adoption takes trials of that one cell only, so trials run under
  two different providers' answers for one model id are never mixed.
- **A trial is adoptable** when it is a finished trial (spec 002), it
  is not imported, and it ended no more than `ADOPTION_MAX_AGE` ago.
- **What is adopted** is up to N such trials, in index order. They stay
  the source cell's trials; the new cell lists their ids in `adopted`
  (spec 002) and its analysis reads them with its own.
  Their measurements already exist, since the graders are equal.
- **The rest run.** With `a` trials adopted, the cell runs N - `a`
  trials of its own, indexed from 0. When `a` is at least
  `SMOKE_TRIALS`, the cell has no smoke rollout and its per-trial
  estimate is the largest adopted trial's cost plus grading cost.
- **A different window.** Adopted trials ran before the experiment's
  own, so they do not share its time window: a comparison that reads
  them carries the confound `window` on the tasks they cover (spec
  008).

### Imported trials

An import registers a finished Topos session or Cella sandbox, which
Examen did not start, as a trial, so the same judges grade it. It is
built after the executed path (spec 001, decision 10).

- **The request** (spec 003) names an experiment in mode `import`, a
  task and a candidate of it, and the session id or sandbox name. A
  production sample is
  graded as a task written for the purpose, whose instruction says what
  the sample was asked to do. An experiment in mode `import` runs no
  trial, so no cell mixes trials Examen ran with trials it did not.
- **`Import`** reads the records as the owner and answers a trial report
  with `Tuple` filled, `imported` true and no checks: the machine is
  gone, so only judges and annotations grade an imported trial. A
  session or sandbox not yet ended is refused.
- **Provenance from a Topos session:** `agent.digest`, the model and
  reasoning level from the session's `model` and its
  `session.model_changed` events, the agent version's tools, and from
  the first `session.machine` the repositories and the machine's image
  digest, which R14 adds to that event.
  **From a Cella sandbox:** `status` image digest, the command, the
  labels, the exit. examend compares the tuple with the one the named
  task and candidate give; a field that differs, or that the records do
  not pin, refuses the import as spec 003's `provenance_mismatch`, and
  nothing is stored. Otherwise the trial joins the experiment's cell of
  that tuple at its next index, and the first import of a tuple creates
  its cell (spec 002); a cell takes imports until it holds N. Today
  `session.machine` names the image by tag alone, so an imported
  session's environment is unpinned, and refused as such, until R14.
- **Evidence is where it already is.** Examen copies nothing. A Topos
  session's evidence is its log, referenced by session id, last
  sequence and the SHA-256 of the log's canonical JSONL through that
  sequence, read through Topos as the owner; and the objects of its own
  folder in Arca when it keeps one, referenced where they are. A Cella sandbox's evidence is the
  Arca folder the import names. Every digest is taken at import, so a
  later change is detected (spec 012).
- **Cost** is read from Lux by the session's key ids when Topos reports
  them (R10), and otherwise from the session's `model.request` events,
  marked as the executor's figure rather than Lux's.

**An external harness's past trial** is imported too (spec 003,
`executor: external`), so a study run before Examen, or outside it, is
backfilled into an experiment in mode `import` and graded like any
trial. The import carries what a submit carries, without a lease, with
the trial's `ended_at` and its `declared` `harness_version` and image;
the three times may lie in the past. The cell is the one its task and
external candidate give, its tuple the candidate's declaration, and the
declared `harness_version` must equal the candidate's `version`.
examend writes its outputs, transcript and final message create-only
under the trial's folder, as for a submit (The `external` executor,
below), and when the task has checks runs them in a check sandbox; the
deadline of an external trial's checks runs from the import. Its cost
is read from Lux by the request ids it lists or the label it carried,
when its model calls were made on Keys whose records Lux still holds,
and is otherwise `unknown` (The `external` executor, below). Its provenance is
declared, and every comparison that reads it says so (spec 008).

### Cost and the sent and observed layers

Each trial's provenance has two layers (spec 002), read after the trial
ends:

| Layer | Source | Fields |
|---|---|---|
| `sent` | for a `topos` trial, the session's records of each request; for a `cella` or `external` trial, Lux's records of the trial's requests | when, the model the request named, the reasoning level it carried, the Lux request id |
| `observed` | Lux's records, joined per request by the Lux request id, with the executor's records beside them; the trial report's `ServiceVersions` | the Lux Model resolved, the provider and the `upstreamModel` that served, `status` and `error`, `tokens` (`input`, `output`, `cachedInput`, `cacheWrite`, `reasoning`, `estimated`), `cost` (`amount` in micro-units, `currency`, `priced`); `service_versions` |

The executor reports where Lux's records of a trial are: the ids of the
Keys that served it, the request ids an external harness submitted,
and, when a Key served several trials or the records are read by owner,
the request label that marks this trial's records. The `usage` job
reads `GET /v1/requests` as the owner `USAGE_SETTLE_DELAY` after the
trial ended and follows `next_cursor` to the end: with `key=` for each
Key id over the trial's time range, keeping the records whose
`requestLabels` carry the label when one is named; and, for a `program`
or `external` trial (`by_owner`), with `owner=` the experiment's owner
over the trial's time range, keeping the records whose request id the
external harness submitted, or, when it submitted none, whose `requestLabels`
carry the label. Each record counts once, by its request id. The owner
read attributes the model calls an external harness made, and those a
service that a program called made on the owner's behalf and labeled
with the trial, which the HTTP runner asks of every service it calls
(below). The label's value is the trial id verbatim:
`examen.trial=tri_01j…`.

**What this asks of Lux.** Lux's request list filters by a Key's
labels, not by a request's, so the owner read walks every record of the
owner in the trial's window and keeps the labeled ones. It is correct
and grows with the owner's traffic. L1: a filter of `GET /v1/requests`
by a request label makes it one query. L2: a Key scoped to one surface,
which Lux's Keys cannot name today; until they can, a `cella` rollout's
Key is leased with its `surface` recorded and not enforced, and a
program can reach a model through any surface its Key's models allow
(The `cella` executor, below).

For a `cella` or `external` trial, `sent` is read from the same
records: the model each request named and, where Lux records one, the
reasoning level it carried. A `cella` trial's forwarder also writes
`requests.jsonl`, which is kept as evidence and never read as either
layer, since the program shares the replica and could write it.

The trial's cost is the sum of its records' `cost.amount`, in the
Budget's currency. Lux's figure is the authority (spec 001, decision 5).

**An API's own cost.** An API a program calls may pay for its own
model calls, on its own Key, so Lux holds no record of them under the
trial; Lectio's parses are one such API. The program records what the
API reported in `service_usage` of `/examen/observed.json`, and an
external harness submits it: per service, the cost in micro-dollars and
the units the API counts, such as a parse's pages. Examen keeps it in
the trial's `observed` layer as the service's own figure, shows it
beside the Lux cost as a separate, labeled figure (spec 010), and never
adds it into the trial's cost, a built-in metric or a Budget's spend.
The executor's own figure (a Topos session's `budget.spent_cost_usd_micro`)
is kept beside it, and a difference is logged, never reconciled. A
trial with any record `priced` false has no cost measurement, and the
report says why. Spend under a rollout's Key that carries no trial's
label is the rollout's `Unattributed` usage, reported on the cell.

### Built-in metrics

examend fills every built-in metric of spec 002 but `declined` from the
trial report and Lux's records, with `grader` `builtin`, once the
`usage` job has read the records; a record that arrives later appends
the next `builtin` generation (spec 007). `declined` is a measurement
of the decline judge (spec 006).

| Metric | Value |
|---|---|
| `completed` | outcome `completed` or `declined`: the agent ended its work itself |
| `cost_usd` | the trial's cost from Lux, in USD; absent when unpriced or in another currency |
| `input_tokens`, `output_tokens`, `reasoning_tokens` | the sums of Lux's `tokens.input`, `tokens.output`, `tokens.reasoning`; cached and cache-write counts stay in `observed` |
| `duration` | `AgentEndedAt` less `StartedAt`, in seconds; the checks are not counted; for an external trial, from the times its external harness submitted |
| `turns` | `topos`: the agent's responses on the session's own thread; `cella` and `external`: the trial's Lux records with status `ok`, since a program's threads cannot be told apart |
| `tool_calls` | `topos`: every `agent.tool_use` of the session, on every thread; `cella` and `external`: absent, since nothing records it |

No built-in is filled for a trial that is not finished: an
`infra_failed`, `over_budget` or `canceled` one, or one a retry
replaced.

### Task inputs, references and outputs

Before the first rollout of a task is submitted, the `prepare` job
copies the task's inputs and references, as the owner with the purpose
`trial` (spec 004), into the experiment's folder in the owner's Arca
space: each input to
`files/evals/experiments/<experiment id>/tasks/<task hex>/inputs/<name>`
and each reference to `.../tasks/<task hex>/reference/<name>` (spec
007). An Arca input is copied with Arca's copy and its content digest
checked; an Origo input is read at its commit; inline text is written
as it stands. Each file's SHA-256 is recorded, so every executor
delivers the same bytes, and a change at the source after the copy
changes nothing. A copy whose digest does not match parks every cell of
the task `blocked`, `parked_reason` `input_changed` naming the input.
Each file is written once, create-only. For an experiment run through
a published benchmark (spec 013), whose tasks are another owner's, the
job reads each Arca input and reference, and each check's `files`, of
the benchmark owner's space through Arca links it mints for this run:
as the benchmark's owner on behalf of the version's `published_by`,
with the purpose `publish` (spec 004), one link on exactly each object
or folder, each expiring `INPUT_LINK_TTL` after it is minted. The links
are kept in the job's row only while the job runs, so a restarted
examend reuses them, and are revoked when it ends; they are never
answered, never logged, and scrubbed from every span (spec 011). The
job checks each digest and copies the checks' files beside the inputs
under `.../tasks/<task hex>/check-files/<grader hex>/`, from which they
are delivered as the experiment owner's own (spec 007). The copies are
the runner's: whoever runs a benchmark can read its references and
checks, which is why a benchmark is open and not held out (spec 013).
While a benchmark runs on its owner's side alone (spec 013), no
experiment is run through another owner's benchmark and this branch of
the job is not taken; it is kept so that decision can be reversed.

| Executor | Inputs | References | Outputs |
|---|---|---|---|
| `topos` | under `examen/inputs/` of the session's working directory, before the first step (R15) | read-only at `/examen/reference/`, after every process of the turn is stopped and the machine's network is `none` (R8) | the files and directories the task names under `/examen/outputs/`, kept in the session's folder with the output manifest (R9) |
| `cella` | read by the trial driver into `/examen/inputs/` through a read link on the inputs folder, before the program starts | written into the check sandbox alone, after the replica has ended and its outputs are exported; the replica holds no link to them and nothing in it can ask for them | collected from `/examen/outputs/` with the replica's evidence; the export sandbox writes each directory output as an archive and the output manifest |
| `external` | one presigned link per input from Arca's materialize route on the inputs folder, in the lease answer (spec 003) | written into the check sandbox alone | submitted by the external harness, a file inline or from the owner's Arca space and a directory as an archive there, and written by examend under the trial's folder with the output manifest |

A check reads the outputs at `EXAMEN_OUTPUTS` and the references at
`EXAMEN_REFERENCE` (spec 006).

**Directory outputs and the output manifest.** A task's `outputs` entry
ending in `/` names a directory, such as an agent's whole workspace,
which may hold thousands of files and single files of tens of
megabytes. It is never carried inline. Every executor keeps a
directory output as one archive, a tar file, streamed into the owner's
Arca space under the trial's folder (spec 007), and keeps one output
manifest per trial: each output file, a directory's files each, with
its path, size and SHA-256, and whether it was kept.

- **Kept by digest.** A file larger than `OUTPUT_FILE_KEEP_MAX_BYTES`
  is left out of the archive and listed in the manifest by its digest
  and size alone, `kept: false`.
- **Over the limit.** The files kept stop at `MAX_OUTPUT_FILES` files
  or `MAX_OUTPUT_BYTES` bytes, whichever comes first. The manifest
  records the constant passed and the files and bytes the outputs held,
  as far as they were read, and lists no file past the limit. The
  trial's `outputs` summary (spec 002) says so, and its outcome is
  unchanged: it is the system under test's.
- **Where it is built.** On `topos`, the session builds the archive
  and the manifest when it keeps its evidence (R9). On `cella`, the
  export sandbox builds them from the collected paths. For an
  `external` trial, the external harness streams the archive into the owner's
  Arca space with its own credentials, wherever it likes, and names it
  in the submit or the import with its digest (spec 003); examend reads
  it as the owner, checks the digest, and writes the trial's own
  archive and manifest by these rules. An archive entry other than a
  regular file or a directory, an absolute path, a `..`, or a path that
  leaves the output directory refuses the whole archive: the output is
  recorded as refused in the manifest and keeps no file.

A model judge that names a directory output reads its kept files from
the archive, its UTF-8 text files in path order within
`JUDGE_MAX_INPUT_BYTES` (spec 006), so a judge of what an agent built
reads the workspace and not its size and digest alone.

The checks of a `topos` trial run in the trial's machine, over the
directory as the system under test left it, so the limits bound what
is kept as evidence and not what the checks read. A `cella` or
`external` trial's check sandbox receives what was kept: each kept file
under `/examen/outputs/`, and the manifest at `EXAMEN_OUTPUTS_MANIFEST`
(spec 006), so a check can account for a file kept by digest alone; a
task whose checks read a program's working directory names it as a
directory output. When its outputs passed a limit or its archive was
refused, its checks do not run: each check run is `not_run` with the
reason `outputs_over_limit` or `outputs_refused`, and their metrics are
missing for that trial, never read from part of what it produced. No
sandbox holds a link to a task's references while its system under
test can run, no sandbox a program ran in ever holds one, and no
reference reaches an external harness: a system under test never sees
the answer it is graded against.

### The `topos` executor

One rollout is one Topos rollout: one session per trial, each in its
own Cella machine, unattended, ending when its one turn ends, its
checks run in the machine before the machine is released, and its
evidence kept in the session's own folder in Arca, which the rollout
reports by path and digest.

**Mapping.**

| Rollout field | Topos, today | Needs |
|---|---|---|
| `Candidate.topos.agent` | a session names `name@version`; examend resolves the digest to a version through `GET /agents/{ref}/versions` and checks it | R1 takes the digest |
| `Candidate.topos.model`, `.reasoning` | `PATCH /sessions/{id}` with `model {name, reasoning}` changes the next turn, and the authorizer may answer another model | R3 |
| `Candidate.topos.tools` | none | R4 |
| `Task.instruction` | the create's `message` | carried by R1 |
| `Task.workspace` `origo` | `resources` `[{type: repository, url, ref}]` with `ref` the full commit | carried by R1 |
| `Task.workspace` `arca` | none: a session's folder comes only from the authorizer's allow | R6 |
| `Task.environment` | none: a create naming `machine` is refused, "a session's machine is its agent's" | R5 |
| `Task.timeout` | `limits.turn_timeout`; `limits.max_age` is set to the trial's deadline | carried by R1 |
| `Task.max_turns` | none: the harness has no step bound | R7 |
| `Task.inputs` | none | R15 |
| `Checks`, their `image`, and `Task.reference` | none | R8 |
| `Task.outputs` | none | R9 |
| evidence | a session keeps files in a folder of an Arca-compatible files API only when the authorizer's allow names one | R9 |
| `Budget` | each session's two Lux keys are minted by the installation's authorizer, with a budget it chooses | R10 |
| `Labels` | session `metadata`, at most 32 pairs, listable by `metadata.<key>=<value>` | built |
| unattended, one turn | `attended` false (a question is answered at once as unanswered), `end_on_idle` true | built |
| idempotent create | `Idempotency-Key`, same subject and body, kept 24 hours | R1 holds a key for the rollout's life |
| the record of each request | `model.request` with `model`, `usage`, `cost_usd_micro`, `cost_source`, `stop_reason`, `outcome` | R11 adds the Lux request id |
| the level of each request | folded today from the session's `model` and its `session.model_changed` events: one trial is one turn, and the level standing at a request's `fold_seq` is the level it carried | built |
| cancel | `user.interrupt`, then `POST /sessions/{id}/end` `{reason: canceled}`, since `end` refuses a running session | R13 |

Each session of a rollout carries the rollout's id, and the executor
finds a rollout's sessions by it, through the rollout's report (R12).
Until R1 exists, it finds them by `metadata.examen.rollout=<key>` on
the session list, which works today and is the fallback a restart uses
if a handle is ever lost.

**Outcomes.**

| Topos end | Outcome |
|---|---|
| the turn ended `end_turn` and the session ended `completed` | `completed` |
| the same, with the last `agent.message`'s `stop_reason` `refusal` | `declined` |
| stop reason `turn_limit`: the harness's turn deadline, which is wall time | `timed_out` |
| the step bound of R7 | `turn_limit` |
| stop reason `output_limit`; `session.error` `repeated_call` | `turn_limit` |
| stop reason `budget`; a request refused `budget_exhausted` or `spend_exceeded` | `over_budget` |
| `session.machine` reason `replaced`; `session.error` `machine_lost`, `machine_unavailable`, `model_unavailable`, `model_busy`, `model_error`, `files_unavailable`, `authorizer_unavailable`, `agent_identity_missing`; session ended `failed` or `expired`; the machine lost during the checks (R8); an evidence write that failed (R9) | `infra_failed`, the `session.error` code or the reason as the failure code |
| session ended `canceled` by a cancel | `canceled` |

A session idle with `tool_confirmation` cannot occur under R1, which
refuses a call that would wait for a confirmation; one that does is
`infra_failed` with `confirmation_required`.

**What Topos has today**, and the executor uses as is: agent versions
with digests, sessions with `attended`, `end_on_idle`, `metadata`,
`budget.max_cost_usd_micro` and `limits`, repositories at a commit,
`Idempotency-Key` on every `POST`, the event log and its stream,
`model.request` with usage and cost, `session.model_changed`,
per-session Lux keys from the installation's authorizer, and a session
folder on an Arca-compatible files API with `work.saved` and
`files.kept`.

**What this spec requires of a Topos rollout primitive.** Numbered so
Topos's rollout spec can answer each; the rest of this spec calls them
R1 to R15:

1. **A rollout.** One call creates N sessions of one agent version named
   by digest, under a caller's key unique per owner: a repeat with the
   same key and body answers the existing rollout for as long as it
   exists, and other content is a conflict. Each session takes values
   of its own: the trial id, its index, its metadata. Every session ends
   when its one turn ends, and has the rollout's instruction as its
   first message, its repositories, its turn timeout and its maximum
   age. No session waits for a person: a question is answered at once
   as unanswered, as `attended` false does today, and a call that would
   wait for a confirmation is refused as a person's deny would be.
2. **Parallelism.** At most the rollout's parallelism of its sessions
   run at once; the rest wait unstarted and hold no machine, and start
   in index order. The bound can be raised and lowered while the rollout
   runs.
3. **Model and reasoning at create.** The rollout names a model and a
   reasoning level that override the agent version's for every session,
   from the first request; the version and its digest are unchanged.
   They run as named: an authorizer that would name another model
   refuses the rollout rather than substituting, and no session moves
   to another model mid-turn.
4. **Tool narrowing.** The rollout names a subset of the agent version's
   tools; a tool outside it is not offered, and a name outside the
   agent's tools is refused.
5. **The machine.** The rollout names the Cella sandbox template its
   sessions run in, by image digest, with its resources and network
   policy, in place of the agent's machine.
6. **An Arca workspace.** The rollout may name an Arca folder and its
   content digest as the working directory's starting content, delivered
   before the first step; a digest that does not match fails the session
   before the agent starts, with a code the report carries.
7. **A step bound.** The rollout may bound the model responses of each
   session's own thread; the bound ends the turn with a stop reason of
   its own, distinct from the turn deadline's `turn_limit`.
8. **Checks in the machine.** After the turn ends and before the machine
   is released, every process the turn started is stopped, the
   machine's network is set to `none` for the whole check phase, the
   checks' files (a pinned Arca folder, spec 006), each check's `image`
   and the task's references (an Arca folder, delivered at
   `/examen/reference/`) are delivered read-only, read with the
   server's own file-store identity and never with the identity of the
   agent's workload, and
   the rollout's check commands run in order in the session's machine,
   in the working directory the agent left, as spec 006 fixes: with the
   session's log written into the machine as JSONL and the final
   message as a file, without the session's model key in their
   environment, and under one total bound the caller gives. Each
   check's exit code, the first bytes of its stdout up to a bound the
   caller gives, the tail of its stderr, its duration, and whether it
   started, timed out or was not run are recorded in the session's log.
9. **Evidence in the session's folder.** Each session keeps, once and
   with the owner's identity, in its own folder in Arca: its log as
   JSONL, its final message, the task's evidence paths, the files under
   `/examen/outputs/` the task's `outputs` name, each directory output
   as an archive with the output manifest under the limits the caller
   gives (Directory outputs and the output manifest, above), and each
   check's whole stdout and stderr. The report names each object's
   path, SHA-256 and size, and Examen references them where they are;
   nothing is copied to a folder the caller names. A write that fails
   fails the session with a code of its own.
10. **The Budget.** Every model request of the rollout's sessions and of
    their sandboxes draws on the Lux Budget the rollout names, and on
    any Budget the installation adds above it, through Keys carrying
    the rollout's labels and the session's, and the report names each
    session's Key ids. A session never runs on a Key outside that
    Budget, and never on a Key that allows an unpriced model.
11. **The request join.** Each `model.request` records the
    `Lux-Request-Id` of the response it read.
12. **One report.** One read answers every session of the rollout, each
    carrying the rollout's id: its id, trial id and index, status, stop
    reason, `session.error` code, Key ids, the times its turn started
    and ended and its checks started and ended, its check results, and
    its evidence objects with their paths and digests.
13. **Cancel.** One call stops the rollout: every running session's turn
    is interrupted and the session ended `canceled`, no waiting session
    starts, and the rollout's Keys stop serving at once. The same call
    with session ids stops those alone.
14. **The machine on the record.** Each session's `session.machine`
    names the image digest its machine ran, so a session imported later
    has a pinned environment.
15. **Input files.** The rollout may name an Arca folder whose files,
    each with its SHA-256, are delivered under `examen/inputs/` of each
    session's working directory before the first step, read with the
    server's own file-store identity as R8's are; a digest that does
    not match fails the session before the agent starts, with a code
    the report carries, as for R6.

### The `cella` executor

One rollout is one Cella SandboxSet: one replica per trial of one
sandbox template, the results collected into a Volume and then into
Arca. Each trial's checks then run in its check sandbox (below), never
in the replica: the program and the checks share no machine, the
replica never holds the references, a link to them or the checks'
files, and nothing in it can cause their delivery.

**Which image runs.** A sandbox runs one image, and spec 002 pins two:
the task's `environment` and the `program` candidate's `image`. The
replica runs the task's environment image, the environment every
candidate of the task gets, and the check sandbox runs the same image,
so the checks run on the system they were written for (spec 001,
decision 2). The program's image reaches the replica as a read-only
Volume filled from that image (Cella spec 019, `source.kind: image`),
mounted at `/examen/program`, and its `command` runs from there. The
program must therefore run from that mount: statically linked, or
carrying its own loader and libraries under its root. A program that
needs its own root file system cannot run on this executor; running it
needs a sandbox of two images, which Cella does not design. The tuple
carries both digests. A check grader's `image` reaches the check
sandbox the same way, at `/examen/check-images/<image hex>`, and never
the replica.

**Objects.**

| Object | Per | Holds |
|---|---|---|
| the tools Volume | owner and examend build | `examen-trial`, a static binary, filled from the trial driver image whose digest the examend build pins; `shared-read` |
| the program Volume | owner and program image digest | the `program` candidate's image; `shared-read`; named from the digest, so every rollout of that candidate reuses it, and at every attach examend reads the Volume's source digest and attaches it only when it equals the name's, else the rollout fails `infra_failed` with `volume_mismatch` and the Volume is not used |
| a check image Volume | owner and check image digest | one check grader's `image`, such as the comparator image (spec 006); `shared-read`; named from the digest and verified by its source digest at every attach, as the program Volume is; attached to check sandboxes alone |
| the results Volume | rollout | `shared-read`, `retain` true until every replica is exported, then deleted |
| the rollout's Lux Key | rollout | for a candidate that names a `model`, leased from the key source with the purpose `trial` (spec 004): `models` the candidate's model alone, the candidate's `surface` alone, the rollout's labels, a `ttl` of the rollout's longest possible life; the `lux` source names the experiment's Budget and allows no unpriced model. examend leases it before submitting: at the cost check for the main rollout, at submission for the others. Its id, never its value, is kept as the rollout's `key_id` (spec 002), so it is released at the rollout's end after a restart too |
| the Lux Secret | rollout | the Key's value as a Cella Secret scoped to Lux's host, mounted under the candidate's key variable |
| the credential Secrets | rollout | from the release that builds program credentials, after v0.1, one per entry of the candidate's `credentials`: the owner token for its audience as a Cella Secret scoped to its `host` and injected into the `Authorization` header as a bearer, mounted under its `env`, or for `file` under a variable the driver writes into that file. The token lasts at most `OWNER_TOKEN_MAX_LIFETIME`, so examend replaces the Secret's value `OWNER_TOKEN_RENEWAL` before it expires while the rollout is open, which Cella applies at its gateway without touching a replica, since a replica holds only the placeholder; deleted at the rollout's end |
| the Arca Secret | rollout | a token of the owner for Arca, scoped to Arca's host, lasting the export's life; Secrets are not refreshed, so its life is bounded; mounted only in the export sandbox, which runs no agent code |
| Arca read links | trial | one per folder a replica reads (an `arca` workspace, the task's inputs), on exactly that folder, expiring at the trial's deadline; none on the checks' `files` or the task's references, which only the check sandbox receives |
| the SandboxSet | rollout | below |
| the export sandbox | rollout | the tools image, the results Volume read-only, the Arca Secret |

**The SandboxSet.** In a `queued` Cella Environment, which the operator
provides and examend's configuration names (a `direct` one is
`capability_unsupported` for a set):

```yaml
kind: SandboxSet
metadata:
  name: ex-<cell ulid>-<rollout kind>[-<round>]
  labels: {examen.experiment: exp_..., examen.cell: cel_..., examen.rollout: <key>}
spec:
  environment: <configured queued environment>
  replicas: <trials in the rollout>
  parallelism: <the rollout's share>
  completions: <replicas>
  template:
    image: <task.environment image digest>
    resources: <task.environment resources>
    network: <task.environment network policy>
    workspace: <origo: source git, the repository and the full commit>
    volumes:
      - {name: tools, path: /examen/bin, volume: <tools Volume>, readOnly: true}
      - {name: program, path: /examen/program, volume: <program Volume>, readOnly: true}
    secrets:
      - {name: <Lux Secret>, env: <candidate key variable>}           # when the candidate names a model
      - {name: <credential Secret>, env: <its env, or a driver variable for file>}   # one per credential
    env: <candidate env, EXAMEN_TASK, EXAMEN_TIMEOUT, EXAMEN_MAX_TURNS, EXAMEN_MODEL, EXAMEN_REASONING, the candidate's base URL variable>
    command: ["/examen/bin/examen-trial", "agent"]
    lifecycle: {ttl: <the trial's deadline>}
  variants:
    - {env: {EXAMEN_TRIAL: tri_..., EXAMEN_INDEX: "0"}}   # one per trial, in order
  completion:
    command: ["/examen/bin/examen-trial", "finish"]
    collect:
      paths: ["/examen/out", "/examen/outputs", <task.evidence paths>]
      volume: <results Volume>
  onFailure: continue
```

A variant cannot add a Secret the template does not mount (Cella spec
020's boundary check), so one Key serves the whole set and the trial
driver marks each request with its trial.

**Model access.** A program that calls models speaks one provider's
API and reads its key and base URL from variables of its own choosing,
so the candidate's `program` object names them in its `lux` settings
(spec 002): `surface`, the Lux API it speaks, one of
`anthropic_messages` (the Anthropic Messages API under Lux's
`/anthropic` door), `openai_responses` and `openai_chat_completions`
(the OpenAI Responses and Chat Completions APIs under its `/openai`
door), `gemini` and `lux` (default `lux`); `key_env` the variable the
Lux Secret is mounted under (default `LUX_KEY`); and `url_env` the
variable set to the forwarder's loopback address standing in for that
surface's base URL (default `LUX_URL`). The surface is the program's,
not the model's: a vendor's command-line agent that speaks the
Anthropic Messages API and is told to use another vendor's model calls
it through `anthropic_messages`, and Lux translates. The rollout's Key
is leased for that one surface (spec 004), so that Lux refuses a
request made with it on any other surface and a program cannot reach a
model through an API its candidate did not declare; that needs Lux's
Keys to name a surface, requirement L2 (Cost and the sent and observed
layers, above), and until then the surface is recorded on the lease and
not enforced. The key reaches the replica as a placeholder that
Cella's egress gateway swaps for the value on a request to Lux's host,
so the value is never in the replica. The candidate's `model` and
`reasoning` are passed as `EXAMEN_MODEL` and `EXAMEN_REASONING`;
whether the program honors them shows in `sent`, which is read from
Lux's records of the trial's requests, never from what the replica
records. A candidate without a `model` gets no Key, no Lux Secret and
no forwarder.

**The trial driver.** `examen-trial` is Examen's own program and runs in
the replica; it runs no task or check code of its own, only the
candidate's command.

- `examen-trial agent` is the replica's main process. It writes the
  instruction, when the task has one, to `/examen/in/instruction.md`
  and names it in `EXAMEN_INSTRUCTION_FILE`, and the candidate's
  `config` to `/examen/in/config.json`, named in `EXAMEN_CONFIG_FILE`.
  It reads the task's inputs through their read link into
  `/examen/inputs/`, named in `EXAMEN_INPUTS`, checking each file's
  SHA-256 (`infra_failed`, `input_changed`, on a mismatch); creates
  `/examen/outputs/`, named in `EXAMEN_OUTPUTS`, and names
  `/examen/observed.json` in `EXAMEN_OBSERVED_FILE`; and writes each
  `file` credential's placeholder into its file. For an `arca`
  workspace, it reads the folder into the workspace through an Arca
  read link examend mints for exactly that folder, expiring at the
  trial's deadline, and checks the folder's digest (`infra_failed`,
  `workspace_changed`, on a mismatch). No token of the owner is ever in
  a replica, only Secret placeholders. It runs the program in a process
  group of its own. When the candidate names a `model`, it starts a
  forwarder on loopback and sets the candidate's base URL variable to
  it. It runs the candidate's `command` with the task's `timeout`. The
  forwarder sends each request on to Lux's door unchanged but for one
  added header, `Lux-Labels: examen.trial=<trial>`, and records per
  request the model named and the reasoning level read from the body
  through `pkg/llmdialect`, with the `Lux-Request-Id` of the answer, in
  `/examen/out/requests.jsonl`, which is evidence and never the source
  of `sent`; it writes each request and response body to
  `/examen/out/transcript.jsonl`, the program's transcript, and the
  text of the last response's message to
  `/examen/out/final_message.md`. When the program ends, it stops every
  process in the replica but its own and the completion command's, so
  nothing the program left behind changes what is collected; keeps the
  `service_versions` and `service_usage` of `/examen/observed.json`,
  when the program wrote one, in `/examen/out/observed.json`, ignoring
  any other member and recording a file that does not parse with
  neither; records
  `{exit_code, started_at, ended_at, timed_out, not_runnable}` in
  `/examen/out/agent.json`; stops the forwarder; and then idles, since
  the main process's exit would stop the replica before it is
  collected.
- `examen-trial finish` is the completion command, which Cella runs once
  the replica is running. It waits for `agent.json` and exits 0 when the
  trial ran to an outcome, and 3 with `/examen/out/failure.json`
  `{code, detail}` when it found an infrastructure failure; any other
  exit is the driver's own failure. Cella then collects the replica's
  paths.

**Checks in the check sandbox.** Once a replica's evidence is exported
(below), examend enqueues its trial's `check_sandbox` job, which runs
the task's checks in a check sandbox exactly as for an external trial
(The check sandbox, below), over the outputs as the export kept them,
with the transcript and final message the forwarder wrote. The job is
examend's, started by the export Cella reports, never by anything the
replica writes, and the replica is gone before the check sandbox
exists, so no process of the program can read a reference or a check's
files, or change a check's result. The trial is `checking` from its
replica's end until its check sandbox ends, and `done` after; its
outcome is the one the replica's end gave.

**Evidence.** Cella collects each replica's paths into the results
Volume under `<index>/` when the replica ends. A Volume has no read
route, so the export sandbox reads it: examend starts it with the
results Volume attached read-only, and for each replica Cella reports
`collected: true` it runs `examen-trial export <index> <trial>` in it
through Cella's exec. That uploads `<index>/` to
`<Evidence.Prefix>/<trial>/` with `If-None-Match: *`, computes each
object's SHA-256, and prints the manifest, which with `agent.json` is
the trial's report; its `sent` and `observed` layers come from Lux's
records. A replica with `collected: false` is `infra_failed`,
`collect_failed`.

**Outcomes.**

| Cella end | Outcome |
|---|---|
| driver exit 0, the agent's process exited by itself (any exit code, recorded) | `completed` |
| driver exit 0, the agent killed at the task's `timeout` | `timed_out` |
| a record of the trial refused `budget_exhausted` or `spend_exceeded` | `over_budget` |
| driver exit 3 (`not_runnable` when the candidate's command could not start, `workspace_changed`, `input_changed`, ...); any other driver exit; replica `Failed` with `NoCapacity`, `StartDeadline`, `VolumeBusy`, `VolumeMissing`, or `Lost` past recovery; `collect_failed` | `infra_failed` |
| set stopped by a cancel | `canceled` |

The `cella` executor enforces no `max_turns`, since a program's turns
are not visible to it: it passes the bound as `EXAMEN_MAX_TURNS`, and
the trial report says the bound was advisory. It cannot report
`declined` either; a program's refusals reach analysis through the
decline judge.

**What Cella must build first.** The SandboxSet kind with its
completion command and results collection (Cella spec 020), Volumes
with an image source (Cella spec 019), and the `workspace.git` source.
Secrets with egress substitution, a Secret's value replaced in place,
single sandboxes with their file routes and the exec route (`POST
/v1/sandboxes/{id}/exec`), on which the check sandbox runs, exist.
Cancel is the set's `stop`, followed by releasing the rollout's Key,
deleting its credential Secrets and deleting its trials' check
sandboxes; Resize is the set's mutable `parallelism`. The rollout's content, for contract rule 1, leaves out
its `Lease`: a restarted examend that resubmits a set already created
gets its handle, and the Lux Key in the set's Secret serves on.

### The HTTP runner

An API built on models, such as Lectio's, is evaluated by a `program`
candidate that calls it, so examend never becomes a client of an
arbitrary host. Examen publishes a first-party HTTP runner image, pinned
by digest, so no owner writes that client: a candidate names the
runner's image, its `command`, and a `config` the runner reads from
`EXAMEN_CONFIG_FILE`. Two candidates that differ only in `config` differ
only in the tuple's `program` field (spec 002), so comparing two
configurations of one API is a single-variable comparison (spec 008).

```yaml
name: lectio-layout-reader
kind: program
program:
  image: sha256:...                 # the HTTP runner image
  command: ["/examen-http"]
  config:
    format: examen.http-runner/v1
    steps:
      - name: upload
        method: POST
        url: https://lectio.example/v1/files
        headers:
          Authorization: "Bearer {{env.LECTIO_TOKEN}}"
        upload: {field: file, input: invoice}
      - name: parse
        method: POST
        url: https://lectio.example/v1/parses
        headers:
          Content-Type: application/json
          Authorization: "Bearer {{env.LECTIO_TOKEN}}"
        body: '{"file": "{{steps.upload.body:$.id}}", "reader": "layout"}'
    poll:
      url: "https://lectio.example/v1/parses/{{steps.parse.body:$.id}}"
      headers:
        Authorization: "Bearer {{env.LECTIO_TOKEN}}"
      interval: 2s
      until: {path: "$.status", in: ["succeeded"]}
      fail: {path: "$.status", in: ["failed", "canceled"]}
      timeout: 10m
    extract:
      - {from: "$.fields", to: fields.json}
    service_version: {name: lectio, header: X-Service-Version}
    service_usage: {name: lectio, cost_usd: "$.cost", units: "$.usage"}
  credentials:
    - {audience: lectio, host: lectio.example, env: LECTIO_TOKEN}
```

| `config` member | Meaning |
|---|---|
| `format` | `examen.http-runner/v1` |
| `steps` | the requests sent in order, each `{name, method, url, headers}` with one of a `body` template, `body_input` (an input sent as the body unchanged) or `upload` (`{field, input}`, a multipart upload of an input, for an API that takes a file before the request that uses it); a later step's templates read an earlier step's answer through `{{steps.<name>.body:<selector>}}` and `{{steps.<name>.header:<name>}}`, and a step that answers an error status ends the trial as a failure. `request` is accepted as the one-step form |
| `poll` | optional, for an API that answers at once and completes later: `url`, `method` (default `GET`) and `headers`, templates over the steps' answers; `interval`; `until` and `fail`, each a path selector (spec 006) into the polled JSON body and the values that end the polling as done or as failed; and `timeout`, within the task's |
| `extract` | the files written from the last response: each `{from, to}`, `from` being `body` for the whole body, `header:<name>`, or a path selector into a JSON body, and `to` one of the task's `outputs` |
| `service_version` | optional: `{name, header}` reads a service's version from a header of the last response, or `{name, url, path}` from a version endpoint, with a path selector into its JSON body |
| `service_usage` | optional: `{name, cost_usd, cost_usd_micro, units}` reads the service's own usage from the last response's JSON body: `cost_usd` a path selector to a decimal string or number of dollars, converted to micro-dollars with exact decimal arithmetic, or `cost_usd_micro` to an integer of micro-dollars; and `units` a path selector to the API's usage object, kept verbatim as JSON whatever its values are, such as a parse answer's `usage`; each may be absent |

A template expands `{{input.<name>}}` to an input's content as UTF-8
text, `{{input.<name>:base64}}` to its bytes in standard base64,
`{{env.<variable>}}` to a variable of the replica, such as a
credential's placeholder, and, in a later step and in `poll`,
`{{steps.<name>.body:<selector>}}` and `{{steps.<name>.header:<name>}}`
to a value of an earlier step's answer.

The runner sends each request with `Lux-Labels: examen.trial=<trial
id>`, so a service that passes the label on with the model calls it
makes for the owner lets Examen attribute them to the trial (Cost and
the sent and observed layers, above). It writes each `extract` under
`/examen/outputs/`, and `{"service_versions": {"<name>": "<version>"},
"service_usage": {"<name>": {"cost_usd_micro": <n>, "units": {...}}}}`
to `/examen/observed.json` with the members its `service_version` and
`service_usage` set. It exits 0
when the last response was a success and every extract was written; 1
when the API answered an error status, or polling reached `fail` or its
`timeout`, which is the API's result and leaves its outputs as they
are; and 2, before any request, when its config cannot be read. Each is
an exit of the program, so the trial is `completed` with the exit code
recorded, and the checks score what the outputs hold.

The runner reaches only what the task's environment network policy
allows, from the replica, and its credentials only as placeholders;
examend makes no request on its behalf (spec 012). Any other program
may write `/examen/observed.json` the same way.

### The `external` executor

An `external` candidate's trials run in its owner's own external harness,
wherever the external harness likes. Examen hands them out, grades what comes
back, and reads their cost from Lux. No core runs them, so the
executor's calls act on examend's own rows:

| Contract call | The `external` executor |
|---|---|
| `Submit` | records the rollout's trials as leasable, and answers a handle naming the rollout's key |
| `Report` | answers each trial's state from examend's rows: `pending` until it is leased and again when its lease lapses, `running` while it is leased, `checking` from its submit until its check sandbox ends, and `done` after |
| `Resize` | sets how many of the rollout's trials may be leased or checking at once |
| `Cancel` | ends the named trials' leases, or every one, so a later submit is `409 lease_not_held`; ends each such trial `canceled`; and deletes its check sandboxes |
| `Import` | never called (spec 003) |

**Leasing.** `POST /experiments/{id}/work/lease` (spec 003,
`trial.lease`) hands out pending trials within each rollout's share,
smoke trials first and main trials only after the cell's cost check:
the named candidate's, or, when the lease names no candidate, those of
every `external` candidate of the experiment. Either way it takes them
in the order the deal deals them: each cell's trials smoke first, then
retries, then main trials, each by index, and across cells, under
`task_by_task`, task by task with a task's cells taking turns trial by
trial, and under `interleaved`, every cell taking its turn before any
takes its next trial. A lease never takes more than the parallelism
less every trial of the experiment already leased or checking, so the
bound holds when the deal moves a share from a cell that still holds
trials to one whose rollout opened later. So an external harness that
leases one trial at a time without naming a candidate, from any number
of workers, runs the experiment in the scheduling's order, and every
trial it takes says which candidate it runs (spec 003). A lease lasts
`EXTERNAL_LEASE` from the call or its last renewal, and the `watch` job returns each trial of a lapsed lease
to pending, keeping its id and spending no retry. A lease is renewed
rather than sized to the trial because a task's `timeout` runs from
minutes to `MAX_TRIAL_TIMEOUT`: one fixed lease would either hold an
abandoned trial for a day or end a long one while it runs. Each leased
trial carries presigned links to its inputs, from Arca's materialize
route on the task's inputs folder, and never a reference.

**Submitting.** `POST /trials/{id}/submit` (spec 003, `trial.submit`)
carries the outcome, the start and end of the system under test's work,
its outputs, and optionally its transcript, final message, service
versions, the usage its services reported, its declared external harness
version and image, self-reported values, and the Lux request ids of its
model calls. The declared external harness version, when given, equals the
candidate's `version`, and is kept with the image in the trial's
`declared` (spec 002). examend writes the outputs, the transcript and the final message
create-only under the trial's folder as the owner (spec 007), records
the rest, and, when the task has checks, enqueues its `check_sandbox`
job. The outcome is the external harness's, set once, and examend never changes
it (rule 4).

**Checks.** When the task has checks, a submitted trial's `check_sandbox`
job runs them in a check sandbox (The check sandbox, below), over the
outputs examend wrote from the submit.

**Attribution and cost.** An external trial's model calls are its
external harness's. With a key source, each experiment Examen executes
that has an `external` candidate has one Key of its own, the
**external Key**: Examen asks the key source for it with the purpose
`external` when the experiment is created, and again with every lease and
renewal of its trials, and the source answers the same Key for every ask
while the experiment runs (spec 004). It draws on the experiment's Budget,
so the experiment's spending cap binds every call made with it, and it
names no model list of the candidate's: a call to another model than the
candidate declared, such as a harness's side call to a small model, is
served, recorded and shown in the trial's `sent` and `observed` layers,
not refused. A lease hands the Key on as `model_key` and each trial its
label as `lux_labels`, the value `examen.trial=<trial id>` of the
`Lux-Labels` header each of the trial's calls carries (spec 003). Examen
releases the Key when the experiment completes or is canceled. An
external harness may call models with a Key of its owner's instead; its
trials' cost then reads `unknown`, as below.

The `usage` job reads Lux's records of the calls made with the
experiment's external Key through the key source (spec 004, `Usage`), with
no owner token: every `USAGE_EVERY` (one minute) for each experiment,
over the window from where the last read ended less `USAGE_LOOKBACK`
(30 minutes) to now, following `next_cursor` to the end. Lux files a
record under the time its call began and writes it when the call ends,
so the lookback keeps a long call's record that appeared after a read of
its start; each record is kept once by its request id. A record is the
trial's whose id its request label `examen.trial` names, or, when it
names none of the experiment's, the trial's that submitted its request id
in `lux_requests`. A record no trial of the experiment claims counts
toward the experiment and no trial: the experiment's answer shows it in
`usage.unattributed` (spec 003). A done trial that ended
`USAGE_SETTLE_DELAY` before the end of a read is settled from its records:
its `sent` layer, its `observed` layer's `served`, token counts,
`cost_state` and `cost_usd`, and a new generation of the `builtin` grader
with `cost_usd` (when known) and the token counts beside the values it had.
A record of it that arrives after unsettles it, and the next read settles
it again. The reads of an experiment end once one covers its end with the
lookback past it. A trial for which no record carries its label or a
request id it submitted, such as one whose harness used its owner's own
Key or a past trial made through another gateway, has cost `unknown`: no
`cost_usd` measurement, `observed.cost_state` `unknown` (spec 007), and
every figure that sums cost counts it apart and shows it as unknown,
never as zero. An executed trial's Keys are known, so no record there
means no spend.

Without a key source the external harness's calls are on Keys its owner
gave it, and Examen reads their records from Lux as the owner (Cost and
the sent and observed layers, above): by the request ids the external
harness submitted, or else by the request label it set. A call the
external harness neither listed nor labeled is not attributed to the
trial, and Examen cannot see that it is missing.

**Smoke and the cost check** run as for any cell: a cell's smoke trials
are the only ones it leases until they are graded and its cost check
passes. The estimate reads the smoke trials' Lux records, and it bounds
the external harness's spend through the experiment's Budget when the
external harness calls models with the external Key, which draws on that
Budget.

**Declared provenance.** Nothing Examen observes pins what an external
external harness ran: the candidate's `harness`, `version`, `digest`, `model`,
`reasoning` and `flags` are the external harness's declaration, made explicit so
that two configurations of one external harness, such as one run at a high and
one at a low reasoning level, are compared on the one field that
differs (spec 008), and so are the outcome, the times, the outputs,
the transcript, the service versions and usage, and the declared image. Examen grades the outputs as
it grades any trial's, and every comparison that reads an external
candidate lists that side in `declared_provenance` (spec 008).

**Self-reported values.** An external harness may submit values it computed
itself, each for a metric the suite lists that no grader produces for
the trial's task. examend stores them as one generation under the
grader `self:<harness>`, with `self_reported` true (spec 007). Such a
value is never headlined, and every result that reads one lists
`self_reported` among its confounds (spec 008).

### The check sandbox

The checks of a `cella` or `external` trial run in a fresh Cella
sandbox, created as the owner with the purpose `trial` (spec 004), from
the task's environment template: its image and resources, with network
`none` whatever the template's policy, so a check reaches nothing
beyond the sandbox, and with `lifecycle.ttl` at the check deadline. No
system under test ever ran in it. examend writes into it through
Cella's file routes (`PUT /v1/sandboxes/{id}/files`) the inputs under
`/examen/inputs/`, the outputs as kept under `/examen/outputs/` with
the output manifest, the references under `/examen/reference/`, the
checks' files, and the transcript and final message, those a `cella`
trial's forwarder wrote or an external harness submitted, empty files
when there are none, each read from the owner's Arca space with its
digest checked. Each check's `image` is attached as a read-only Volume,
verified by its source digest at the attach. examend then runs each
check in order through Cella's exec route (`POST
/v1/sandboxes/{id}/exec`), with spec 006's environment, under
`CHECK_TIMEOUT` for all of them; keeps the first
`CHECK_MAX_OUTPUT_BYTES` of each stdout for spec 006's contract; writes
each whole stdout and stderr to evidence; records each run as a check
run record of the trial; and deletes the sandbox. `EXAMEN_WORKSPACE`
there is the sandbox's empty working directory: a `cella` or `external`
trial's checks read its outputs, and a task whose checks need a
program's whole working directory names it as a directory output.
Examen still executes no check code: Cella runs each command, and
examend reads what it printed. A single Cella sandbox, its file routes
and exec are built, so this path does not wait for SandboxSet; a check
that names an `image`, a comparator among them, also needs Cella's
image Volumes (Cella spec 019).

### The v0.1 profile

An installation with no owner token source, no key source and no file
store serves the v0.1 profile (spec 003), and the first release serves
nothing else: the `external` executor, for experiments whose candidates
are all of kind `external` and whose graders include no `check`,
`model_judge` or `agent_judge`, and experiments in mode `import` of an
external harness's past trials. Nothing in it reaches a core as the
owner:

- **Smoke and the cost check.** A cell still runs its smoke trials
  first, leases only them, and parks `blocked` on a smoke trial whose
  last attempt ended `infra_failed`. Its estimate is zero, since an
  external trial's own spend is not drawn on the experiment's Budget and
  no grader of the profile calls a model, so the cell is admitted
  without a lease (Smoke trials and the cost check, above). A trial
  submitted `over_budget` parks its cell as any does.
- **A submit and an import** take each output only as `{path, arca:
  {path, digest}}`, an object the external harness wrote into the
  owner's file store. examend records the path and the digest as the
  trial's output evidence and never reads the object: no digest is
  checked, no archive is opened, no output manifest is written and no
  output limit of this spec is applied, since nothing in the profile
  reads an output. An output carried inline, a transcript and a final
  message are refused (spec 003).
- **No check sandbox** runs, since no `check` grader is served, and a
  submitted trial goes from `running` to `done`.
- **Cost.** Every trial's cost is `unknown`: its `lux_requests` and its
  request label are recorded and never read, since the `usage` job
  reads Lux as the owner. `observed.cost_state` is `unknown` (spec
  007), and every figure that sums cost counts it apart.
- **Built-in metrics** are filled from the trial record as for any
  external trial, `cost_usd` aside, and the built-in decline judge
  produces `declined` from the outcome.

Where this differs from the general text above: an external cell's
cost check leases no Key, where step 3 leases one and releases it; a
submitted Arca output is recorded unread, where The `external` executor
writes the outputs, the transcript and the final message create-only
under the trial's folder and reads each Arca object as the owner; and
an external trial whose request ids Lux holds still has cost
`unknown`, where Cost and the sent and observed layers reads them.

### Limits

The constants of spec 002 this spec holds: `SMOKE_TRIALS`,
`MAX_INFRA_RETRIES`, `DEFAULT_PARALLELISM`, `MAX_PARALLELISM`,
`DEFAULT_TRIAL_TIMEOUT`, `MAX_TRIAL_TIMEOUT`, `CHECK_TIMEOUT`,
`CHECK_MAX_OUTPUT_BYTES`, `ADOPTION_MAX_AGE`, `COST_ESTIMATE_MARGIN`,
`TRIAL_GRACE`, `TRIAL_START_TIMEOUT`, `EXTERNAL_LEASE`,
`REPORT_INTERVAL`, `USAGE_SETTLE_DELAY`, `OWNER_TOKEN_MAX_LIFETIME`,
`OWNER_TOKEN_RENEWAL`, `MAX_OUTPUT_FILES`, `MAX_OUTPUT_BYTES`,
`OUTPUT_FILE_KEEP_MAX_BYTES` and `INPUT_LINK_TTL`.

## Not in this spec

The routes that apply, cancel, retry a cell, import, lease and submit
(spec 003); acting as the owner toward Topos, Cella, Lux and Arca, the
key source, and the minting of a program's credentials (spec 004);
turning check runs and evidence into measurements, the comparators, and
every judge (spec 006); the
shape of an evidence reference, the provenance layers' storage and the
evidence layout's record format (spec 007); what lost indices do to a
verdict (spec 008); examend's
configuration, the Cella environment it names and the trial driver
image (spec 011); the threats around all of it (spec 012). Topos's
rollout primitive and Cella's SandboxSet are those projects' specs.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| `Submit` twice with one key and one content answers one handle and the core holds one rollout; with other content it is `ErrConflict` and nothing starts | `execute.TestSubmitIsIdempotentByKey`, against the stub Topos and the stub Cella |
| A report that moves a trial backward, or changes a `done` trial, is not applied and is logged as an executor fault | `execute.TestReportsOnlyMoveForward` |
| An examend killed after `Submit` returned and before the handle was stored, then restarted, holds one rollout and the same trial ids | `TestRestartResubmitsUnderTheSameKey`, e2e tier |
| A smoke trial that ends `infra_failed` after its retries, or whose check is `malformed`, parks the cell `blocked` naming the trial and the code; one that ends `timed_out` does not | `queue.TestSmokeBlocks` |
| A cell whose estimate exceeds the smallest remainder its lease reports, less the other admitted cells' outstanding estimates, parks `over_budget` with both figures and starts no main trial; the remainder is the lease's, from either key source | `queue.TestCostCheck`, over a stub Lux Budget and a stub key endpoint |
| A cell whose estimate is zero is admitted without a Key lease unless it is a `cella` cell whose candidate names a model; with no owner token source, key source or file store, an experiment of `external` candidates runs from smoke to its final comparisons with no request to any core, records each `arca` output's path and digest without reading it, and gives every trial cost `unknown` | `queue.TestZeroEstimateLeasesNothing`, `external.TestV01Profile` |
| A trial that ends `over_budget` mid-rollout parks its cell `over_budget`, cancels the cell's open rollouts, is excluded from analysis and is not retried automatically | `queue.TestBudgetRunsOut` |
| A retry of a cell parked `over_budget` runs the cost check again and then every index without a finished trial, with `retry_of` and outside `MAX_INFRA_RETRIES`; a retry of a cell blocked by its smoke runs new smoke trials | `queue.TestRetryParkedCell` |
| A smoke trial with an unpriced record blocks with `model_unpriced`, and an experiment naming a soft Budget is refused at apply | `queue.TestUnpricedBlocks`, `TestSoftBudgetRefused` |
| An experiment's trials running or pending never exceed its `parallelism` across its cells, shares are resized as rollouts finish, and a task's cells are dealt trials round robin, so their trials of one index run in one window | `queue.TestParallelismAcrossCells`, `queue.TestCandidatesInterleave` |
| Under `interleaved` scheduling held trials keep their places and the rest of the parallelism goes round robin across every cell of every task, so with a parallelism below the cells' unfinished trials every cell holds a share before any holds two, and among the trials leasable at once trial `i` of each cell is leased before trial `i + 1` of any | `execute.TestInterleavedAcrossTasks`, `execute.TestBefore`, `postgres.TestLeaseInterleavesAcrossTasks` |
| A lease that names no candidate takes trials of every `external` candidate in the scheduling's order, within each cell's share, and each leased trial names its candidate; under `task_by_task` it takes a task's trials before the next task's; a lease never holds more trials at once than the parallelism, counting those already held | `postgres.TestLeaseWithoutCandidate`, `postgres.TestLeaseHoldsTheParallelism`, `httpapi.TestLeaseWithoutCandidate` |
| Only `infra_failed` is retried automatically, as a new trial with the same index and `retry_of`, at most `MAX_INFRA_RETRIES` times per index; the retried trial stays readable and is excluded from analysis | `queue.TestRetriesAndLineage` |
| A trial past its deadline is canceled and recorded `infra_failed` with `deadline_passed`; a pending trial past `TRIAL_START_TIMEOUT` with free parallelism with `not_started` | `queue.TestDeadlines` |
| Canceling an experiment cancels every open rollout, ends unfinished trials `canceled`, starts no grading, and releases the `cella` rollout's Key in the same job | `queue.TestCancel` |
| Adoption, when built: with `adopt`, a cell with an equal tuple of the same owner adopts at most N adoptable trials of the most recent complete source cell, runs only the rest, and skips smoke when at least `SMOKE_TRIALS` were adopted; a cell of another owner, an older trial than `ADOPTION_MAX_AGE`, an imported or `infra_failed` trial is never adopted | `queue.TestAdoption` |
| An imported Topos session yields a trial with `imported` true, its tuple read from the records, no checks, and evidence digests taken at import; the first import of a tuple creates its cell and later ones join it; a tuple field that differs from the named task's and candidate's or is unpinned refuses it as `provenance_mismatch`; a session not ended is refused | `topos.TestImport` |
| An external past trial imports with its past times and declared `harness_version` and image, its outputs are written under its trial's folder and graded in a check sandbox, its provenance is declared, and a `harness_version` other than its candidate's is refused | `external.TestImportPastTrial` |
| A program's `service_usage`, and an external harness's, are kept in `observed` and never summed into the trial's cost or a built-in metric; the HTTP runner reads it from the last response | `cmd/examen-http.TestServiceUsage`, `luxread.TestServiceUsageKeptApart` |
| An experiment run through a benchmark copies the benchmark owner's Arca inputs, references and check files through links its `prepare` job mints for that run as the benchmark's owner, each expiring after `INPUT_LINK_TTL`, digests checked; the links leave the job's row and are revoked when it ends, and no span, log line or answer carries one, by canary | `execute.TestPrepareThroughBenchmarkLinks` |
| A trial's cost is the sum of Lux's records for its Key ids, and for its request label when the Key is shared; spend without a label is the rollout's unattributed usage; a difference from Topos's own figure is logged | `luxread.TestCostFromRecords` |
| `sent` and `observed` join per request by the Lux request id; a `cella` trial's `sent` is read from Lux's records, and a `requests.jsonl` the program rewrote changes neither layer | `luxread.TestLayersJoinByRequestID`, `cella.TestSentFromLux` |
| Each built-in metric is filled as its row says, and none for a trial that is not finished | `execute.TestBuiltinMetrics` |
| Every label value is written verbatim, the trial id with its underscore | `execute.TestLabelsVerbatim` |
| Each row of the `topos` outcome table maps as written | `topos.TestOutcomes`, over recorded session logs |
| A `topos` rollout's sessions are found by its rollout id, and by `metadata.examen.rollout` when the handle is missing; a `topos` trial's evidence refs point into its session's folder, and Examen copies nothing | `topos.TestFindsSessionsByRollout`, `topos.TestFindsSessionsByLabel`, `topos.TestEvidenceStaysInTheSessionFolder` |
| A `cella` rollout applies the SandboxSet above, with the environment image as the template, the program's image and the tools mounted read-only and no check image, one Key leased from the key source and its Secret when the candidate names a model, one Secret per credential, and one variant per trial; a reused Volume whose source digest differs from its name's is never attached | `cella.TestSetFromRollout`, `cella.TestVolumeDigestAtAttach` |
| A task's inputs and references are copied once per experiment into its folder with their SHA-256; a digest that does not match parks the task's cells `blocked` with `input_changed` | `execute.TestPrepareCopiesInputsAndReferences` |
| No sandbox a system under test ran in holds a task's references while it can run: a `cella` replica never holds the references, a link to them or the checks' files, and its checks run in a check sandbox started by examend after the export; a `topos` rollout names them for R8 alone; a lease answer carries none | `cella.TestChecksInTheCheckSandbox`, `topos.TestReferencesOnlyForChecks`, `external.TestLeaseCarriesNoReference` |
| Every check sandbox runs with network `none` whatever the task's network policy, and a `topos` rollout asks R8 for it in the check phase | `execute.TestCheckPhaseHasNoNetwork` |
| A `program` candidate's credentials are minted with the purpose `trial` for their audiences, carried as Secrets scoped to their hosts, replaced before the token expires, and deleted at the rollout's end; no value enters a replica | `cella.TestCredentialSecrets` |
| The HTTP runner sends the configured steps in order with their templates expanded, an upload step as multipart, and the trial's label, polls until `until`, `fail` or its timeout, writes each extract to its output, records the service version in `/examen/observed.json`, and exits 0, 1 or 2 as its rule says; the driver keeps the service versions | `cmd/examen-http.TestRunner`, `cmd/examen-trial.TestObservedVersions` |
| An `external` rollout leases only smoke trials before its cost check, never more than its share; a lapsed lease returns its trials to pending with no retry spent; a submit writes the outputs, and the trial's checks run in a check sandbox through exec with the outputs and references written in and the sandbox deleted after; a cancel ends every lease | `external.TestLeaseFollowsTheQueue`, `external.TestLeaseLapses`, `external.TestCheckSandbox`, `external.TestCancel` |
| An external trial's cost and sent layer are read from the owner's Lux records by its submitted request ids, or else by its request label, each record once; one with no record Lux can attribute has cost `unknown`, never zero; its self-reported values are stored under `self:<harness>` with `self_reported` true | `luxread.TestExternalAttribution`, `luxread.TestUnknownCost`, `external.TestSelfReported` |
| A directory output is kept as one archive and the output manifest, a file above `OUTPUT_FILE_KEEP_MAX_BYTES` by digest and size alone; outputs past `MAX_OUTPUT_FILES` or `MAX_OUTPUT_BYTES` keep nothing more and record the constant; an archive with a link, an absolute path or `..` is refused whole; an external trial over a limit or refused runs no check | `evidence.TestDirectoryOutputs`, `external.TestOutputLimits` |
| A rollout's Key names the candidate's one surface, and, once Lux's Keys name one (L2), a request on another surface is refused | `cella.TestKeySurface`, against the stub Lux |
| The trial driver runs the agent, stops every process the agent left before the replica is collected, runs no check, idles until it is collected, and marks every forwarded request with the trial's label | `cmd/examen-trial.TestDriver`, on the native Cella driver |
| Each row of the `cella` outcome table maps as written, and a collected replica's evidence lands under its trial's folder with its SHA-256 | `cella.TestOutcomes`, `cella.TestExport` |

## Outcome

In progress. v0.1.0 runs the `external` executor of the v0.1 profile.
v0.6.0 gives an experiment of `external` candidates its external Key
from the key source, hands it on with each lease beside each trial's
label, releases it at the experiment's end, and reads the records of its
calls to settle each trial's cost and its `sent` and `observed` layers.
A cost settled after the experiment completed is on the trial and its
measurements, and does not reopen a final comparison.
Rollouts are rows on their cell, keyed `<cell>.smoke`, `<cell>.main`,
`<cell>.retry.N` and `<cell>.smoke.N`. A cell's smoke trials run
first, and its main trials follow a cost check whose estimate is zero.
An `infra_failed` trial is retried with `retry_of` up to
`MAX_INFRA_RETRIES`; a cell parks `blocked` or `over_budget`, and a
parked cell is retried on request. Leases lapse and renew on
`EXTERNAL_LEASE`. A cancel ends every unfinished trial `canceled`. An
external harness's past runs import with their declared provenance.
`completed` and `duration` are filled at done, and `declined` by the
decline judge. Outputs are recorded as Arca paths with their digests
and never read, and every trial's cost is `unknown`. v0.3.0 adds
`interleaved` scheduling, a lease that names no candidate, and the
bound that keeps a lease within the parallelism.

The decisions are stateless functions in `internal/execute`, their
transactions are in `internal/store/postgres`, and one engine in
`internal/work` expires leases, advances rollouts and computes
comparisons every `REPORT_INTERVAL`. The criteria cite tests of
packages not yet written (`queue`, `external`, `topos`, `cella`,
`luxread`); the table names the tests that hold the same behavior.

Every row below shipped in v0.1.0 unless it names another release.

| # | Verdict | Held by |
|---|---|---|
| 4 | Partly met: a smoke trial `infra_failed` after its retries parks the cell `blocked` naming the trial and the code; a `malformed` check waits on checks, and that `timed_out` does not block is untested | `execute.TestInfraFailuresRetryThenBlock`, `httpapi.TestRetriesBlockAndResume` |
| 6 | Partly met: a zero estimate is admitted with no lease, and an experiment of the profile runs from smoke to completion with no core client; the `cella` exception waits on that executor, a computed final comparison in mode `execute` is held only by the live tier, and no test reads back an output's path and digest or a trial's `unknown` cost | `execute.TestSmokePassesAndQueuesMain`, `httpapi.TestTheExternalFlow`, `live.TestLiveSmoke` |
| 7 | Partly met: the cell parks `over_budget` naming the trial and the code, and the trial is excluded from analysis; ending the cell's open trials is asserted on the decision, not on the stored rows | `execute.TestOverBudgetParks`, `httpapi.TestOverBudgetParksAndRetryReopens`, `analysis.TestIncomplete` |
| 8 | Partly met: a retry runs every index without a finished trial, with `retry_of` and outside `MAX_INFRA_RETRIES`, and a cell blocked by its smoke runs new smoke trials; the cost check it runs again is the zero estimate | `execute.TestRetryOfAParkedCell`, `httpapi.TestRetriesBlockAndResume`, `httpapi.TestOverBudgetParksAndRetryReopens` |
| 10 | Met for the `external` executor (v0.3.0; the round robin dates from v0.1.0) | `execute.TestDeal`, `postgres.TestLeaseHoldsTheParallelism`, `postgres.TestLeaseWithoutCandidate` |
| 11 | Met (v0.3.0) | `execute.TestInterleavedAcrossTasks`, `execute.TestBefore`, `postgres.TestLeaseInterleavesAcrossTasks` |
| 12 | Met (v0.3.0) | `postgres.TestLeaseWithoutCandidate`, `postgres.TestLeaseHoldsTheParallelism`, `httpapi.TestLeaseWithoutCandidate` |
| 13 | Met | `execute.TestInfraFailuresRetryThenBlock`, `httpapi.TestRetriesBlockAndResume`, `objects.TestValuesAndCurrent` |
| 15 | Partly met: a cancel ends unfinished trials `canceled` and their leases and rollouts in one transaction; the tests assert the experiment's state and a refused lease rather than each trial, and the `cella` Key waits on that executor | `httpapi.TestTheExternalFlow`, `httpapi.TestExperimentReads`, `postgres.TestWritesFailWhole` |
| 15 | Partly met (v0.6.0 adds the external Key): a cancel releases the experiment's external Key at once, and a completion within `REPORT_INTERVAL` | `httpapi.TestTheExperimentKey` |
| 18 | Partly met: past times, the declared `harness_version` and image, `422 provenance_mismatch` for another version, and declared provenance; outputs written under the trial's folder and graded wait on the file store and the check sandbox | `httpapi.TestSubmitAndImportRefusals`, `httpapi.TestAnalyzedImport` |
| 19 | Partly met: an external harness's `service_usage` is kept in `observed` and summed apart from the trial's cost; programs and the HTTP runner are not built, and no test reads the stored usage back through the API | `compare.TestCostOf`, `analysis.TestUnknownCostNeverZero` |
| 21 | Partly met for `external` trials (v0.6.0): a trial's cost is the sum of the records of the experiment's external Key that its label marks or whose request id it submitted; a call no trial claims counts toward the experiment's `usage.unattributed` and no trial | `httpapi.TestTheExperimentKey`, `usage.TestTheLayersOfATrialsCalls` |
| 22 | Partly met for `external` trials (v0.6.0): `sent` and `observed` are read from the same records, each kept once by its request id; Lux records no reasoning level a request carried, so `sent.by_setting` carries the model alone | `httpapi.TestTheExperimentKey`, `usage.TestTheLayersOfATrialsCalls` |
| 23 | Partly met: `completed`, `duration` and `declined`; `cost_usd`, `input_tokens`, `output_tokens` and `reasoning_tokens` for an `external` trial whose records were read, as a later generation of `builtin` (v0.6.0); turns and tool calls wait on an executor, and an unfinished trial's missing value is tested for pending trials alone | `objects.TestValuesAndCurrent`, `httpapi.TestTheExternalFlow`, `httpapi.TestTheExperimentKey`, `postgres.TestMeasurementsAreAppendOnly` |
| 24 | Met for the trial label (v0.6.0): a lease answers each trial's `lux_labels` as `examen.trial=<trial id>`, the id verbatim | `httpapi.TestTheExperimentKey`, `httpapi.TestALeaseWithNoKeySource` |
| 33 | Partly met: only smoke trials lease before the smoke passes, never past the share, and a lapsed lease returns its trials to pending with no retry spent; a submit's outputs and the check sandbox wait on the file store and Cella, and that a cancel ends every lease is untested | `httpapi.TestTheExternalFlow`, `httpapi.TestLeasesLapseAndRenew`, `postgres.TestLapsesLeasesAndReopensFailWhole` |
| 34 | Partly met: self-reported values are stored with `self_reported` true; with a key source (v0.6.0) the records are those of the experiment's external Key, read through the key source rather than as the owner, by label or submitted request id, each once, a late record unsettling its trial; a trial with no record keeps cost `unknown`. Without one, every trial's cost is `unknown` | `httpapi.TestAnalyzedImport`, `postgres.TestMeasurementsAreAppendOnly`, `httpapi.TestTheExperimentKey`, `httpapi.TestTheExperimentKeyThroughAPooler` |

Not built: 1 to 3, the executor contract; 5 and 9, the cost check
against a Budget's remainder, unpriced models and soft Budgets; 14,
deadlines and `TRIAL_START_TIMEOUT`; 16, adoption; 17, 25 and 26, the
`topos` executor; 20 and 28, the prepare job; 27, 31 and 36 to 38, the `cella`
executor and the trial driver; 29 and 30, the check sandbox; 32, the
HTTP runner; 35, directory outputs.

The work queue as built differs from the design above: there is no
jobs table claimed by `EXAMEN_WORKERS` workers, a rollout has no
`submitting` state, a rollout's stored `parallelism` is 1 while shares
are dealt at each lease, and a parked cell's reason reads
`smoke_infra_failed: <trial>: <code>` or `budget_exhausted: <trial>:
<code>`.
