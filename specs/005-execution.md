---
title: "Execution: the executor contract, the topos and cella executors, smoke trials and the cost gate, the work queue, retries, timeouts, cancellation, adoption, imported trials, and where cost and provenance are read"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
affects: [internal/execute/, internal/execute/topos/, internal/execute/cella/, internal/queue/, internal/luxread/, cmd/examen-trial/]
effort: xlarge
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Execution

## Overview

Examen runs no trial itself. It hands each cell's trials to an executor,
which asks the core that runs agents or sandboxes to run them, and reads
back what happened. This spec fixes the executor contract and the two
executors: `topos` for a candidate of kind `topos`, and `cella` for a
candidate of kind `image`. It also fixes what examend owns around them:
the work queue that spreads an experiment's parallelism across its
cells, the smoke trials and the cost gate, retries of infrastructure
failures with their lineage, timeouts, cancellation, adoption of trials
an identical cell already ran, trials imported from sessions and
sandboxes Examen did not start, and where each trial's cost and its
sent and observed provenance are read.

Neither executor can run today as written. Topos has sessions, budgets
and metadata but no rollout primitive, no model override at create and
no checks in the machine; Cella's SandboxSet is designed (Cella spec
020) and not built. What the `topos` executor needs from Topos is listed
below as numbered requirements, from which Topos's rollout spec is
written.

## Design

### Rollouts of a cell

A cell's trials execute in rollouts. A rollout is one submission to the
cell's executor: a set of the cell's trials, run under one parallelism
bound and one key. A cell has at most three kinds:

| Rollout | Key | Trials |
|---|---|---|
| smoke | `<cell id>.smoke` | indices 0 to `SMOKE_TRIALS` - 1 |
| main | `<cell id>.main` | every remaining index up to N - 1 |
| retry | `<cell id>.retry.<round>` | one new trial for each trial of the previous rollouts that ended `infra_failed` with retries left; rounds count from 1 |

A cell with N at most `SMOKE_TRIALS` has no main rollout. An adopted
cell (below) runs only the indices adoption did not fill.

Separate rollouts, not one rollout of N held after its smoke trials, is
a choice. Cella's SandboxSet has a mutable `parallelism` but no hold:
with `parallelism` at `SMOKE_TRIALS` it admits the next replica the
moment the smoke replica ends, before the smoke trial is graded and the
estimate made, and its `replicas` cannot grow for a retry. Separate
rollouts need nothing from either core for smoke or retries. A cell
therefore keeps a list of rollouts, its `rollouts` (spec 002), each
`{key, kind, executor, handle, parallelism, state}`: `queued` until the
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
	// Name is "topos" or "cella".
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
	// trial of owner.
	Import(ctx context.Context, owner, ref string) (TrialReport, error)
}

// Handle names a submitted rollout at its core: a Topos rollout id or a
// Cella SandboxSet id.
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
| `Task` | resolved task | `instruction`, `workspace`, `environment` (image digest, resources, network policy), `timeout` (resolved: the task's, else `DEFAULT_TRIAL_TIMEOUT`), `max_turns`, `evidence` paths |
| `Checks` | []resolved check grader | in the task's order: the grader digest, `command`, `workdir`, `env`, `files` |
| `Candidate` | resolved candidate | the `topos` or `image` object, every field pinned |
| `Budget` | string | the experiment's Lux Budget |
| `Evidence` | `{Owner, Prefix}` | the Arca space and the folder `files/examen/experiments/<experiment id>/cells/<cell id>/trials`, under which trial `t` writes `<Prefix>/<t>/` in spec 007's layout |
| `Labels` | map | `examen.experiment`, `examen.cell`, `examen.rollout`; each trial adds `examen.trial` and `examen.index` |

The prefix `examen.` is reserved for these labels; spec 003 refuses it
on any `metadata` key a client supplies. The executor writes the labels to every
object it causes to exist: Topos session metadata, Cella labels, Lux Key
labels.

A `Report` is `{Ended bool, Trials []TrialReport, Unattributed
Usage}`: one trial report per submitted trial, in the submitted order,
`Ended` true when every trial is `done`, and the rollout's spend that no
trial carried (below). A `TrialReport` carries:

| Field | Meaning |
|---|---|
| `Trial`, `Index` | the trial as submitted |
| `ExecutorRef` | the Topos session id or the replica's sandbox name; `trial.executor_ref` |
| `State` | `pending`, `running`, `checking`, `done` |
| `Outcome` | once `done`: one of spec 002's outcomes |
| `Failure` | for `infra_failed` and `canceled`: `{code, detail}`, a stable code and a developer's detail |
| `Sent` | each model request as the executor's records show it was sent: `{at, model, reasoning, lux_request}` |
| `Lux` | where Lux's records of the trial are: `{keys []string, request_label string}` (below) |
| `Executor` | what the executor's own records add to `observed`: per request `{lux_request, usage, cost_micro}` from Topos, the agent's exit code and the wall times from Cella |
| `Checks` | one `CheckRun` per check, in order: `{grader, state, exit_code, stdout, stdout_size, stdout_sha256, stderr_tail, duration}`, `stdout` at most `CHECK_MAX_OUTPUT_BYTES` and the size and digest of the whole; a check that did not run to its end does not change the trial's outcome; spec 006 turns it into measurements |
| `Evidence` | each object written to Arca, as spec 007's evidence ref: its kind, name, path, SHA-256 digest computed while writing, size, and Arca's checksum |
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
   examend, and examend serves no route an executor reports to.
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
| `over_budget` | Lux refused a request for spend (`budget_exhausted`, `spend_exceeded`) | no |
| `infra_failed` | the trial could not be run to an outcome for a reason outside the agent: the machine could not start or was lost, the gateway did not answer or rejected the request, the workspace could not be delivered, the machine was lost during the checks, evidence could not be written, the deadline below passed | yes, up to `MAX_INFRA_RETRIES` |
| `canceled` | the experiment was canceled before the trial ended | no |

The executor sets the outcome once and examend never changes it
(rule 4). A `canceled` trial, like an `infra_failed` one, is not a
finished trial and is excluded from analysis (spec 002). The outcome
`declined` is the executor's hard signal only; the metric `declined`,
which also covers an agent that refused or gave up in its own words, is
the decline judge's (spec 006), and the judge never changes the outcome.

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
| `submit` | submits one rollout; the rollout's row moves `submitting` to `submitted` with its handle |
| `report` | calls `Report` for one submitted rollout every `REPORT_INTERVAL`, applies it, and enqueues what follows: grading of each trial that reached `done` (spec 006), the gate, a retry round, the cell's next state |
| `gate` | the cost gate after a cell's smoke trials are graded |
| `resize` | sets a rollout's parallelism to its share |
| `watch` | the deadlines below, every `REPORT_INTERVAL` |
| `cancel` | cancels an experiment's open rollouts |
| `usage` | reads Lux's records of a trial `USAGE_SETTLE_DELAY` after it ended |

Cells advance in id order, and an experiment's smoke rollouts are
submitted before any main rollout, so every cell is smoke-tested before
any spends at scale. A retry rollout goes ahead of a main rollout not yet
submitted, since it finishes a cell already running.

### Parallelism across cells

An experiment's `parallelism` bounds its trials running or checking at
once, across all its cells. examend divides it among the experiment's
open rollouts in queue order: each takes the smaller of the trials it
has not finished and what is left, and a rollout that gets none waits
unsubmitted. On every `report`, the shares are computed again and a
rollout whose share changed gets a `Resize`, never below 1 once
submitted, since a SandboxSet's `parallelism` has that floor. A trial counts against the
bound from its submission until it is `done`, pending trials included,
so the cores never hold more admitted trials than the experiment allows.

### Smoke trials and the cost gate

A cell runs its smoke rollout first and is `smoking` until the last
attempt at every smoke index is `done`, retries included, and graded by
every grader of kind `check`, `model_judge` and `agent_judge`
(annotations never hold a cell). Then:

1. **Blocked.** The cell parks `blocked` when a smoke trial's last
   attempt ended `infra_failed`, when a check run of a smoke trial did
   not end `ok` (spec 006), or when a judgment of a smoke trial ended
   in any state but `ok`, `refused` or `over_budget` (spec 006).
   `parked_reason`
   names the trial, the check or grader, and the code. An agent
   behavior outcome (`completed`, `declined`, `timed_out`,
   `turn_limit`) never blocks.
2. **Over budget in smoke.** A smoke trial `over_budget`, or a judgment
   of one `over_budget`, parks the cell `over_budget`.
3. **The estimate.** Per trial, the estimate is the largest, over the
   smoke trials, of the trial's cost plus the cost of its grading, both
   from Lux's records. The cell's estimate is that times the trials
   left to run times `COST_ESTIMATE_MARGIN`.
4. **The remainder.** Examen reads the Budget's `status.remaining` from
   Lux at that moment, as the owner, and subtracts the outstanding
   estimate of every other cell of the same Budget admitted past its
   gate and not `complete`: its per-trial estimate times its trials not
   yet `done` times `COST_ESTIMATE_MARGIN`. This is admission
   bookkeeping recomputed from trial states, not a spend ledger: spend
   is only ever read from Lux.
5. **The gate.** The cell's estimate above the remainder parks it
   `over_budget` with `parked_reason` naming both figures; otherwise its
   main rollout is queued. A parked `over_budget` cell is gated again
   only when the owner asks (`POST /cells/{id}/gate`, spec 003).

A smoke trial whose cost Lux cannot price (a record with `priced`
false) blocks the cell with `model_unpriced`: an estimate with an
unknown term would pass any gate. The Budget must be `hard: true`; a
soft Budget is refused when the experiment is applied, since it bounds
nothing. A Budget's window may reset during an experiment and raise the
remainder; the gate reads it as it stands.

Examen sets no per-trial spend cap of its own. A cap derived from the
smoke cost would cut off a trial that is legitimately dearer than the
smoke trial and record it `over_budget`, which biases the cell; the
Budget's hard limit is the bound.

### Retries and lineage

When a rollout has ended, every trial of it that ended `infra_failed`
whose index has had fewer than `MAX_INFRA_RETRIES` retries gets a new
trial: a new `tri_` id, the same `index`, `retry_of` naming it. They go
in the cell's next retry rollout. The retried trial stays, with its
outcome, failure code and evidence, and is excluded from analysis; the
chain of `retry_of` is its lineage. A trial whose index has used its
retries stays `infra_failed`, is also excluded from analysis, and
leaves the cell one result short of N; spec 008 reads what that does to
verdicts. No other outcome is retried.

### Timeouts

| Bound | Covers | Enforced by | Past it |
|---|---|---|---|
| the task's resolved `timeout` | the agent's work, from its first step to its end | the executor | `timed_out` |
| `CHECK_TIMEOUT` | all of one trial's checks together | the executor | spec 006 |
| the trial's deadline: `timeout` + `CHECK_TIMEOUT` + `TRIAL_GRACE` from `StartedAt` | the whole trial, machine start and evidence writing included | examend's `watch` | `Cancel` of that trial, then `infra_failed` with code `deadline_passed` |
| `TRIAL_START_TIMEOUT` | a pending trial of a rollout running fewer trials than its parallelism, counted from the later of the rollout's submission and the last end of one of its trials | examend's `watch` | `Cancel` of that trial, then `infra_failed` with code `not_started` |

The deadline exists for an executor that stops reporting; the executor
enforces the first two bounds itself.

### Cancellation

Canceling an experiment parks every cell not `complete` as `canceled`
and enqueues one `cancel` job, which calls `Cancel` on every open
rollout of the experiment. A trial not yet `done` when its rollout is
canceled ends `canceled`; what it wrote to Arca stays and is referenced.
No grading starts after the cancel, and grading jobs already queued for
the experiment are dropped. A judge call already sent completes and its
measurement is kept. Spend after a cancel is bounded by how fast each
core stops: the `cella` executor disables the rollout's Lux Key in the
same job, and the `topos` executor relies on R13 below.

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

With `adopt` true, a cell may take trials an identical cell already
ran instead of running them again.

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
  `session.model_changed` events, the agent version's tools, the
  machine's image, the repositories from the first `session.machine`.
  **From a Cella sandbox:** `status` image digest, the command, the
  labels, the exit. examend compares the tuple with the one the named
  task and candidate give; a field that differs, or that the records do
  not pin, refuses the import as spec 003's `provenance_mismatch`, and
  nothing is stored. Otherwise the trial joins the experiment's cell of
  that tuple at its next index, and the first import of a tuple creates
  its cell (spec 002); a cell takes imports until it holds N. Topos records
  a machine's image by name today, so an imported session's environment
  is unpinned until R14.
- **Evidence is where it already is.** Examen copies nothing. A Topos
  session's evidence is its log, referenced by session id, last
  sequence and the SHA-256 of the log's canonical JSONL through that
  sequence, read through Topos as the owner; and the objects of its
  folder in Arca when it keeps one. A Cella sandbox's evidence is the
  Arca folder the import names. Every digest is taken at import, so a
  later change is detected (spec 012).
- **Cost** is read from Lux by the session's key ids when Topos reports
  them (R10), and otherwise from the session's `model.request` events,
  marked as the executor's figure rather than Lux's.

### Cost and the sent and observed layers

Each trial's provenance has two layers (spec 002), read after the trial
ends:

| Layer | Source | Fields |
|---|---|---|
| `sent` | the executor's records of each request | when, the model the request named, the reasoning level it carried, the Lux request id |
| `observed` | Lux's records, joined per request by the Lux request id, with the executor's records beside them | the Lux Model resolved, the provider and the `upstreamModel` that served, `status` and `error`, `tokens` (`input`, `output`, `cachedInput`, `cacheWrite`, `reasoning`, `estimated`), `cost` (`amount` in micro-units, `currency`, `priced`) |

The executor reports where Lux's records of a trial are: the ids of the
Keys that served it, and, when a Key served several trials, the request
label that marks this trial's records. The `usage` job reads `GET
/v1/requests` with `key=` for each id over the trial's time range,
`USAGE_SETTLE_DELAY` after the trial ended, follows `next_cursor` to
the end, and keeps the records whose `requestLabels` carry the label
when one is named. A request label value cannot hold `_`, so the trial
id is written with its underscore as a period: `examen.trial=tri.01j...`.

The trial's cost is the sum of its records' `cost.amount`, in the
Budget's currency. Lux's figure is the authority (spec 001, decision 5).
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
| `duration` | `AgentEndedAt` less `StartedAt`, in seconds; the checks are not counted |
| `turns` | `topos`: the agent's responses on the session's own thread; `cella`: the trial's Lux records with status `ok`, since an image agent's threads cannot be told apart |
| `tool_calls` | `topos`: every `agent.tool_use` of the session, on every thread; `cella`: absent, since nothing records it |

No built-in is filled for an `infra_failed` or `canceled` trial.

### The `topos` executor

One rollout is one Topos rollout: one session per trial, each in its
own Cella machine, unattended, ending when its one turn ends, its
checks run in the machine before the machine is released, and its
evidence written to Arca.

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
| `Checks` | none | R8 |
| `Evidence` | a session keeps files in a folder of an Arca-compatible files API only when the authorizer's allow names one | R9 |
| `Budget` | each session's two Lux keys are minted by the installation's authorizer, with a budget it chooses | R10 |
| `Labels` | session `metadata`, at most 32 pairs, listable by `metadata.<key>=<value>` | built |
| unattended, one turn | `attended` false (a question is answered at once as unanswered), `end_on_idle` true | built |
| idempotent create | `Idempotency-Key`, same subject and body, kept 24 hours | R1 holds a key for the rollout's life |
| the record of each request | `model.request` with `model`, `usage`, `cost_usd_micro`, `cost_source`, `stop_reason`, `outcome` | R11 adds the Lux request id |
| the level of each request | folded today from the session's `model` and its `session.model_changed` events: one trial is one turn, and the level standing at a request's `fold_seq` is the level it carried | built |
| cancel | `user.interrupt`, then `POST /sessions/{id}/end` `{reason: canceled}`, since `end` refuses a running session | R13 |

Until R1 exists, the executor finds a rollout's sessions by
`metadata.examen.rollout=<key>` on the session list, which works today
and is the fallback a restart uses if a handle is ever lost.

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
R1 to R14:

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
2. **Parallelism.** At most the rollout's parallelism of its sessions run
   at once; the rest wait unstarted and hold no machine. The bound can
   be raised and lowered while the rollout runs.
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
   is released, every process the turn started is stopped, the checks'
   files (a pinned Arca folder, spec 006) are delivered read-only, and
   the rollout's check commands run in order in the session's machine,
   in the working directory the agent left, as spec 006 fixes: with the
   session's log written into the machine as JSONL and the final
   message as a file, without the session's model key in their
   environment, and under one total bound the caller gives. Each
   check's exit code, the first bytes of its stdout up to a bound the
   caller gives, the tail of its stderr, its duration, and whether it
   started, timed out or was not run are recorded in the session's log.
9. **Evidence to Arca.** The rollout names an Arca space and a folder;
   each session writes, once and with the owner's identity, under
   `<folder>/<trial id>/`: its log as JSONL, its final message, the
   task's evidence paths, and each check's whole stdout and stderr. It
   records each object's path, SHA-256 and size. A write that fails
   fails the session with a code of its own.
10. **The Budget.** Every model request of the rollout's sessions and of
    their sandboxes draws on the Lux Budget the rollout names, through
    Keys carrying the rollout's labels and the session's, and the
    report names each session's Key ids. A session never runs on a Key
    outside that Budget, and never on a Key that allows an unpriced
    model.
11. **The request join.** Each `model.request` records the
    `Lux-Request-Id` of the response it read.
12. **One report.** One read answers every session of the rollout: its
    id, trial id and index, status, stop reason, `session.error` code,
    Key ids, the times its turn started and ended and its checks
    started and ended, its check results, and its evidence objects.
13. **Cancel.** One call stops the rollout: every running session's turn
    is interrupted and the session ended `canceled`, no waiting session
    starts, and the rollout's Keys stop serving at once. The same call
    with session ids stops those alone.
14. **The machine on the record.** Each session's `session.machine`
    names the image digest its machine ran, so a session imported later
    has a pinned environment.

### The `cella` executor

One rollout is one Cella SandboxSet: one replica per trial of one
sandbox template, the checks as the completion command, the results
collected into a Volume and then into Arca.

**Which image runs.** A sandbox runs one image, and spec 002 pins two:
the task's `environment` and the candidate's `image`. The replica runs
the task's environment image, because the checks were written for it
and run where the work was done (spec 001, decision 2). The candidate's
image reaches the replica as a read-only Volume filled from that image
(Cella spec 019, `source.kind: image`), mounted at `/examen/agent`, and
its `command` runs from there. The candidate's program must therefore
run from that mount: statically linked, or carrying its own loader and
libraries under its root. A candidate whose program needs its own root
file system cannot run on this executor; running it needs a sandbox of
two images, which Cella does not design. The tuple carries both digests.

**Objects.**

| Object | Per | Holds |
|---|---|---|
| the tools Volume | owner and examend build | `examen-trial`, a static binary, filled from the trial driver image whose digest the examend build pins; `shared-read` |
| the agent Volume | owner and candidate image digest | the candidate's image; `shared-read`; named from the digest, so every rollout of that candidate reuses it |
| the results Volume | rollout | `shared-read`, `retain` true until every replica is exported, then deleted |
| the rollout's Lux Key | rollout | `spec.budget` the experiment's Budget, `models` the candidate's model alone, `allowUnpriced` false, the rollout's labels, a `ttl` of the rollout's longest possible life; applied by examend as the owner |
| the Lux Secret | rollout | the Key's value as a Cella Secret scoped to Lux's host, mounted under the candidate's key variable |
| the Arca Secret | rollout | a token of the owner for Arca, scoped to Arca's host, lasting the export's life; Secrets are not refreshed, so its life is bounded; mounted only in the export sandbox, which runs no agent code |
| Arca read links | trial | one per folder a replica reads (an `arca` workspace, the checks' `files`), on exactly that folder, expiring at the trial's deadline |
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
      - {name: agent, path: /examen/agent, volume: <agent Volume>, readOnly: true}
    secrets: [{name: <Lux Secret>, env: <candidate key variable>}]
    env: <candidate env, EXAMEN_TASK, EXAMEN_TIMEOUT, EXAMEN_MAX_TURNS, EXAMEN_REASONING, the candidate's base URL variable>
    command: ["/examen/bin/examen-trial", "agent"]
    lifecycle: {ttl: <the trial's deadline>}
  variants:
    - {env: {EXAMEN_TRIAL: tri_..., EXAMEN_INDEX: "0"}}   # one per trial, in order
  completion:
    command: ["/examen/bin/examen-trial", "check"]
    collect:
      paths: ["/examen/out", <task.evidence paths>]
      volume: <results Volume>
  onFailure: continue
```

A variant cannot add a Secret the template does not mount (Cella spec
020's boundary check), so one Key serves the whole set and the trial
driver marks each request with its trial.

**Model access.** An image agent speaks one provider's dialect and reads
its key and base URL from variables of its own choosing, so the
candidate's `image` object names them in its `lux` settings (spec
002): `door` one of `openai`, `anthropic`,
`gemini`, `lux` (default `lux`), `key_env` the variable the Lux Secret
is mounted under (default `LUX_KEY`), and `url_env` the variable set to
the forwarder's loopback address standing in for that door's base URL
(default `LUX_URL`). The key reaches the replica as a placeholder that
Cella's egress gateway swaps for the value on a request to Lux's host,
so the value is never in the replica. The candidate's `reasoning` is
passed as `EXAMEN_REASONING`; whether the agent honors it shows in
`sent`, which the forwarder reads from each request.

**The trial driver.** `examen-trial` is Examen's own program and runs in
the replica; it runs no task or check code of its own, only the
candidate's command and the task's checks.

- `examen-trial agent` is the replica's main process. It writes the
  instruction to `/examen/in/instruction.md` and names it in
  `EXAMEN_INSTRUCTION_FILE`; for an `arca` workspace, it reads the
  folder into the workspace through an Arca read link examend mints
  for exactly that folder, expiring at the trial's deadline, and checks
  the folder's digest (`infra_failed`, `workspace_changed`, on a
  mismatch). No token of the owner is ever in a replica. It runs the
  agent in a process group of its own. It starts a
  forwarder on loopback, sets the candidate's base URL variable to it,
  and runs the candidate's `command` with the task's `timeout`. The
  forwarder sends each request on to Lux's door unchanged but for one
  added header, `Lux-Labels: examen.trial=<trial>`, and records per
  request the model named and the reasoning level read from the body
  through `pkg/llmdialect`, with the `Lux-Request-Id` of the answer, in
  `/examen/out/requests.jsonl`; it writes each request and response
  body to `/examen/out/transcript.jsonl`, the image agent's transcript,
  and the text of the last response's message to
  `/examen/out/final_message.md`. When the agent ends, it stops every
  process in the replica but its own and the completion command's, so
  nothing the agent left behind runs during the checks, records
  `{exit_code, started_at, ended_at, timed_out, not_runnable}` in
  `/examen/out/agent.json`, stops the forwarder, and then idles, since
  the main process's exit would stop the replica before its checks.
- `examen-trial check` is the completion command, which Cella runs once
  the replica is running. It waits for `agent.json`, removes anything
  under `/examen/out/checks/`, reads the checks' files through a read
  link as above (spec 006), then runs the checks in order under
  `CHECK_TIMEOUT` as spec 006 fixes, without the key variable in their
  environment, and writes each check's result to
  `/examen/out/checks/<i>.json` with its whole stdout and stderr beside
  it. It exits 0 when the trial ran to an outcome, and 3 with
  `/examen/out/failure.json` `{code, detail}` when it found an
  infrastructure failure; any other exit is the driver's own failure.

**Evidence.** Cella collects each replica's paths into the results
Volume under `<index>/` when the replica ends. A Volume has no read
route, so the export sandbox reads it: examend starts it with the
results Volume attached read-only, and for each replica Cella reports
`collected: true` it runs `examen-trial export <index> <trial>` in it
through Cella's exec. That uploads `<index>/` to
`<Evidence.Prefix>/<trial>/` with `If-None-Match: *`, computes each
object's SHA-256, and prints the manifest, which with `agent.json`,
`requests.jsonl` and the check results is the trial's report. A replica
with `collected: false` is `infra_failed`, `collect_failed`.

**Outcomes.**

| Cella end | Outcome |
|---|---|
| driver exit 0, the agent's process exited by itself (any exit code, recorded) | `completed` |
| driver exit 0, the agent killed at the task's `timeout` | `timed_out` |
| a record of the trial refused `budget_exhausted` or `spend_exceeded` | `over_budget` |
| driver exit 3 (`not_runnable` when the candidate's command could not start, `workspace_changed`, ...); any other driver exit; replica `Failed` with `NoCapacity`, `StartDeadline`, `VolumeBusy`, `VolumeMissing`, or `Lost` past recovery; `collect_failed` | `infra_failed` |
| set stopped by a cancel | `canceled` |

The `cella` executor enforces no `max_turns`, since an image agent's
turns are not visible to it: it passes the bound as `EXAMEN_MAX_TURNS`,
and the trial report says the bound was advisory. It cannot report
`declined` either; an image agent's refusals reach analysis through the
decline judge.

**What Cella must build first.** The SandboxSet kind with its
completion command and results collection (Cella spec 020), Volumes
with an image source (Cella spec 019), and the `workspace.git` source.
Secrets with egress substitution and the exec route
(`POST /v1/sandboxes/{id}/exec`) exist. Cancel is the set's `stop`,
followed by disabling the rollout's Key; Resize is the set's mutable
`parallelism`.

### Limits

The constants of spec 002 this spec holds: `SMOKE_TRIALS`,
`MAX_INFRA_RETRIES`, `DEFAULT_PARALLELISM`, `MAX_PARALLELISM`,
`DEFAULT_TRIAL_TIMEOUT`, `CHECK_TIMEOUT`, `CHECK_MAX_OUTPUT_BYTES`,
`ADOPTION_MAX_AGE`, `COST_ESTIMATE_MARGIN`, `TRIAL_GRACE`,
`TRIAL_START_TIMEOUT`, `REPORT_INTERVAL` and `USAGE_SETTLE_DELAY`.

## Not in this spec

The routes that apply, cancel, re-gate and import (spec 003); acting as
the owner toward Topos, Cella, Lux and Arca (spec 004); turning check
runs and evidence into measurements, and every judge (spec 006); the
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
| A cell whose estimate exceeds the Budget's remainder less the other admitted cells' outstanding estimates parks `over_budget` with both figures and starts no main trial | `queue.TestCostGate`, over a stub Lux Budget |
| A smoke trial with an unpriced record blocks with `model_unpriced`, and an experiment naming a soft Budget is refused at apply | `queue.TestUnpricedBlocks`, `TestSoftBudgetRefused` |
| An experiment's trials running or pending never exceed its `parallelism` across its cells, and shares are resized as rollouts finish | `queue.TestParallelismAcrossCells` |
| Only `infra_failed` is retried, as a new trial with the same index and `retry_of`, at most `MAX_INFRA_RETRIES` times per index; the retried trial stays readable and is excluded from analysis | `queue.TestRetriesAndLineage` |
| A trial past its deadline is canceled and recorded `infra_failed` with `deadline_passed`; a pending trial past `TRIAL_START_TIMEOUT` with free parallelism with `not_started` | `queue.TestDeadlines` |
| Canceling an experiment cancels every open rollout, ends unfinished trials `canceled`, starts no grading, and disables the `cella` rollout's Key in the same job | `queue.TestCancel` |
| With `adopt`, a cell with an equal tuple of the same owner adopts at most N adoptable trials of the most recent complete source cell, runs only the rest, and skips smoke when at least `SMOKE_TRIALS` were adopted; a cell of another owner, an older trial than `ADOPTION_MAX_AGE`, an imported or `infra_failed` trial is never adopted | `queue.TestAdoption` |
| An imported Topos session yields a trial with `imported` true, its tuple read from the records, no checks, and evidence digests taken at import; the first import of a tuple creates its cell and later ones join it; a tuple field that differs from the named task's and candidate's or is unpinned refuses it as `provenance_mismatch`; a session not ended is refused | `topos.TestImport` |
| A trial's cost is the sum of Lux's records for its Key ids, and for its request label when the Key is shared; spend without a label is the rollout's unattributed usage; a difference from Topos's own figure is logged | `luxread.TestCostFromRecords` |
| `sent` and `observed` join per request by the Lux request id | `luxread.TestLayersJoinByRequestID` |
| Each built-in metric is filled as its row says, and none for an `infra_failed` or `canceled` trial | `execute.TestBuiltinMetrics` |
| Each row of the `topos` outcome table maps as written | `topos.TestOutcomes`, over recorded session logs |
| A `topos` rollout's sessions are found by `metadata.examen.rollout` when the handle is missing | `topos.TestFindsSessionsByLabel` |
| A `cella` rollout applies the SandboxSet above, with the environment image as the template, the candidate's image and the tools mounted read-only, one Key and one Secret, and one variant per trial | `cella.TestSetFromRollout` |
| The trial driver runs the agent, stops every process the agent left before the first check, runs the checks without the key variable, idles until the checks end, and marks every forwarded request with the trial's label | `cmd/examen-trial.TestDriver`, on the native Cella driver |
| Each row of the `cella` outcome table maps as written, and a collected replica's evidence lands under its trial's folder with its SHA-256 | `cella.TestOutcomes`, `cella.TestExport` |
