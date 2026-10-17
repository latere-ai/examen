---
title: "Security and threat model: what Examen protects, who attacks it, the trust boundaries, each defense and the test that holds it, and what is not defended"
status: drafted
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
  - specs/006-grading.md
  - specs/007-measurements-and-evidence.md
  - specs/008-analysis.md
  - specs/011-distribution.md
affects: [cmd/examend/, cmd/examen-trial/, internal/, SECURITY.md]
effort: medium
created: 2026-10-17
updated: 2026-10-17
author: changkun
---

# Security and threat model

## Overview

Examen reads text nobody reviewed, an agent's output and a task's
content, and hands it to models that grade it. It spends its owners'
money, keeps evidence of their work, and holds the measurements a
release bar decides on. This spec names what is worth taking or
corrupting, who would do it, and where the boundaries are. For each
threat it states what the attacker can do, the defense, the spec that
owns the defense, and the test that holds it. A defense without a test
is a claim, so a test reads this file and holds every test it names to
one that exists.

It designs no mechanism of its own; each defense is another spec's, and
this is the place a reviewer checks that none is missing.

## Design

### What is protected

| Asset | Where it lives | What taking or changing it is worth |
|---|---|---|
| measurements and their provenance | Postgres | a release decision: an altered or forged measurement passes a bar it should fail, or hides a regression |
| the definitions: tasks, checks, rubrics, baselines | Postgres; check files in the owner's Arca space | a check or rubric the agent reads tells it how to pass without doing the work; a held-out task that leaks is spoiled |
| the owner's spend | the experiment's Lux Budget | money |
| evidence: transcripts, final messages, workspace files, check outputs | the owner's Arca space; Topos session logs | the owner's code, data and prompts |
| annotations | Postgres | the ground truth judges are calibrated against |
| the owner's identity as Examen acts with it | short-lived tokens and leased Keys in examend's memory (spec 004); a Lux Key placeholder in a Cella Secret; the export sandbox's Arca token; Arca read links | acting as the owner at Topos, Cella, Lux and Arca |
| examend's availability | the process and its queue | experiments and release checks wait on it |

### Who acts, and how far each is trusted

| Actor | Trust |
|---|---|
| the owner, and the people the authorizer lets act on the owner's objects | trusted for those objects, as the authorizer decides |
| a caller of another owner | not trusted with this owner's objects |
| the agent under evaluation | not trusted at all: it may be adversarial, may game its grading, and may be steered by what it reads |
| task content: instructions, workspaces, imported benchmark material | not trusted as instructions, even when the owner wrote the task, since it carries text from repositories and benchmarks |
| a check | the owner's code, trusted as the owner's; it runs over files the agent produced and may run code the agent wrote |
| a judge model and its provider | trusted to answer within its schema; trusted with data only under terms the owner accepted |
| an annotator | an authorized person, trusted to answer honestly; kept from what would bias the answer |
| Topos, Cella, Lux, Arca, the authorizer, the minter and the key endpoint | trusted by configuration: the operator named them |
| the operator | trusted, and not defended against |

### Trust boundaries

```mermaid
flowchart LR
  subgraph untrusted["untrusted"]
    caller["a caller of another owner"]
    agent["the agent and what it writes"]
    content["task content"]
  end
  subgraph machine["the trial's sandbox, under its egress policy"]
    run["the agent's work"]
    checks["the checks"]
  end
  subgraph examen["Examen"]
    examend["examend"]
    pg[("Postgres: definitions, measurements, append-only")]
  end
  subgraph cores["named by the operator, trusted by configuration"]
    topos["Topos"]
    cella["Cella"]
    lux["Lux"]
    arca["Arca"]
    az["the authorizer"]
  end
  provider["the judge model's provider"]
  caller -->|"a bearer"| examend
  examend -->|"one question per action"| az
  examend -->|"as the owner, pull only"| topos
  examend -->|"as the owner, pull only"| cella
  topos --> machine
  cella --> machine
  agent --> run
  content --> run
  machine -->|"evidence, written once with its digest"| arca
  arca -. "evidence, read as data, digest checked" .-> examend
  examend -->|"evidence in the user turn only, delimited; schema-validated answer"| lux
  lux --> provider
  examend --> pg
```

Four boundaries carry the defenses:

1. **Caller to examend.** Every route verifies the bearer and asks the
   authorizer about this caller, this action and this object before it
   reads or acts (spec 004). Objects of one owner reference another's
   only through an explicit share (spec 002).
2. **The trial's sandbox to everything else.** The agent and the checks
   run inside the trial's sandbox under its egress policy. Nothing they
   write is executed or obeyed by examend: a check's output is parsed
   under a strict contract (spec 006), and evidence reaches a judge only
   as delimited data.
3. **examend to a judge.** Evidence enters only the user turn, inside
   blocks it cannot close, and the only answer accepted is one call of
   one schema, validated by examend.
4. **examend to the cores.** examend pulls: it calls Topos, Cella, Lux
   and Arca as the owner and serves no route any of them calls (spec
   005, contract rule 7). There is no inbound report to forge or replay.

### Threats and defenses

| # | Who | What they can do | The defense | Spec | Test |
|---|---|---|---|---|---|
| 1 | hostile agent output or task content reaching a model judge | write into its transcript, final message or files text that tells the judge to ignore the rubric, give a value, add a metric, or end the evidence early with a forged marker | evidence is read as data and never as instructions: it is placed only in the user turn, each input in a block whose boundary is chosen after the content is fixed and occurs in none of it, so no content closes its block; the system content is the preamble, the prompt, the rubric and the metric list, and never a byte of evidence, the task's instruction included; the judge has one tool, `record_judgment`, and examend accepts only one call of it, validated against the schema, the ranges, the labels and the rationale bound, so injected text cannot add a metric, change a type or reach anything; temperature and model are pinned; calibration against people flags a judge whose agreement falls | 006 | `TestCanaryNeverInSystemContent`, `judge.TestEvidenceStaysInTheUserTurn`, `judge.FuzzBoundary`, `TestAnObedientJudgeCannotChangeTheSchema` |
| 2 | hostile evidence reaching an agent judge | steer the judge agent to change the evidence, reach out of its machine, or answer for another trial | its inputs are read-only and outside its working directory, its machine's network is `none`, its tools are narrowed to reading ones with no connection, one session judges one trial, and its answer is read only from that session's own evidence and validated like a model judge's | 006, 005 | `judge.TestAgentJudge`, `TestAnAgentJudgeCannotReachOut` |
| 3 | the agent gaming its own grading | edit a check's files, leave a process that rewrites check results while checks run, write a fake result file in advance, print a forged result that a check passes through | a check trusts no file the agent could change: its files are in the image at a path the sandbox's user cannot write, or delivered read-only from a pinned Arca folder; every process the agent started is stopped before the first check; the driver clears its results directory before checks and writes it itself; a check's stdout must be exactly one object with every metric, so a forged object beside the real one is `malformed`, never a pass; the whole stdout is kept in evidence, so a result can be audited | 006, 005 | `TestAForgedSecondObjectIsMalformed`, `cmd/examen-trial.TestDriver`, `TestCheckFilesAreReadOnly`, `TestALeftoverProcessCannotTouchResults` |
| 4 | a check that lies or exfiltrates, or agent code it runs | report a value that is not true, send the workspace somewhere, spend on the model | a check runs only in the trial's sandbox, under that sandbox's egress policy, and never in examend, which parses and never executes; a check holds no model key and no owner credential; a value is the check's owner's claim, kept with its stdout; agent code that reads the key's placeholder from another process can spend only through Lux's host, on the candidate's model, under the hard Budget, and the spend carries no trial's label and is reported as the rollout's unattributed usage | 005, 006 | `check.TestEnvironment`, `TestExamendExecutesNothing`, `luxread.TestCostFromRecords` |
| 5 | cost exhaustion: an agent that loops, a model dearer than expected, a judge over large evidence, a large experiment, many experiments | spend the owner's money past what was meant | one hard Lux Budget per experiment, a soft one refused by the `lux` key source; every Key the key source leases or a core issues for the experiment draws on it, and on any Budget an installation adds above it, and allows no unpriced model; with the `endpoint` source Examen writes no Key or Budget, so a cap a budget owner set at the installation cannot be bypassed; smoke trials and the cost check against the smallest remainder across those Budgets, with `COST_ESTIMATE_MARGIN`, before any cell spends at scale; the limits `MAX_TRIALS_PER_CELL`, `MAX_CELLS_PER_EXPERIMENT`, `MAX_CANDIDATES_PER_EXPERIMENT`, `MAX_PARALLELISM`, `MAX_TRIAL_TIMEOUT`, `JUDGE_MAX_INPUT_BYTES`, `JUDGE_MAX_OUTPUT_TOKENS`, `JUDGE_MAX_ATTEMPTS` and `MAX_ACTIVE_EXPERIMENTS_PER_OWNER`; a refused request ends the trial `over_budget`, which parks its cell and is never retried automatically; a cancel releases the experiment's Keys | 005, 006, 004, 002 | `queue.TestCostCheck`, `queue.TestBudgetRunsOut`, `queue.TestUnpricedBlocks`, `TestSoftBudgetRefused`, `judge.TestJudgeKey`, `TestEveryKeyDrawsOnTheBudget`, `TestActiveExperimentsAreBounded` |
| 6 | a caller of another owner, or a share used too widely | name another owner's experiment, trial, evidence or annotation queue by id; read through a shared suite or baseline; adopt or import across owners | every route asks the authorizer before it reads or acts, and with no authorizer an owner reaches only their own objects; a deny on a stored object answers as its absence, so an id is not confirmed; a shared baseline gives its reader the comparison's summaries and never its trials' evidence, a shared suite gives its definitions and never another owner's experiments, and a shared experiment or report gives reading and never evidence bytes, which Arca grants; adoption never crosses owners; an import reads the core's records as the owner, so what the owner cannot read cannot be imported; evidence is written to and read from the owner's own Arca space with the owner's identity; annotators see only items of experiments the authorizer lets them answer | 004, 005, 006 | `TestAnotherOwnersTokenReachesNothing`, `queue.TestAdoption`, `TestASharedBaselineExposesNoEvidence`, `TestImportIsAskedAsTheOwner` |
| 7 | a judge's provider, and evidence that holds secrets | keep or train on the transcripts and files a judge sends; read a credential the agent printed | the owner chooses every judge model, the decline judge's included, Examen ships no default judge model, and the built-in decline judge calls no model; an authorizer holds judges to the models whose terms an owner accepts with `judge_models`; a judge's Key comes from the key source, which decides its data terms, so an installation's key endpoint can hold it to providers that keep nothing when the owner asks for that; a judge reads only the inputs its grader names, within `JUDGE_MAX_INPUT_BYTES`; model keys reach a machine only as placeholders the egress gateway swaps, so an agent cannot print one into evidence; evidence stays in the owner's Arca space and Topos | 006, 005, 004 | `judge.TestJudgeKey`, `TestNoDefaultJudgeModel`, `TestNoKeyValueInEvidence` |
| 8 | a writer of the database, a client, a faulty executor, an overwrite in Arca | change a measurement, rewrite a trial's result, swap the evidence a judge reads | measurements are append-only: no route updates or deletes one, and the database refuses an update or delete of a measurement row from examend's own role; versioned objects are named by the digest of their canonical JSON and every reference pins one; a `done` trial is final and a report that differs is refused; evidence is written once (`If-None-Match: *`) with its SHA-256 taken when it is reported, and every read for grading checks it, ending in `evidence_changed` on a mismatch; comparisons and bar evaluations are computed, recorded with what they read, and never written by a client | 002, 005, 006 | `TestMeasurementsAreAppendOnly`, `execute.TestReportsOnlyMoveForward`, `judge.TestEvidenceChanged`, `TestDigestsAreCanonical` |
| 9 | someone holding a captured token or link | replay a caller's bearer, an acting-as-owner token, the export sandbox's Arca token, a read link, a Lux Key; send a write twice | examend checks issuer, audience and expiry on every request and keeps no caller's token; a token it obtains to act as the owner is short-lived, bound to one core's audience, held in memory and never logged or stored (spec 004); examend serves no route a core calls, so there is no report to replay; every write that creates (apply, run, re-grade, annotation answer) carries a key and appends once; the export token lasts the export and is scoped by its Secret to Arca's host; a read link names one folder and expires at the trial's deadline; a rollout's Key is released when the rollout ends, and a judge's when the experiment ends | 004, 005, 006 | `TestStaleOrForeignTokenRefused`, `TestNoInboundExecutorRoute`, `TestReplayedWriteAppendsOnce`, `cella.TestRolloutKeyReleasedAtEnd` |
| 10 | a caller flooding the API | send large bodies, many requests, many experiments, or requests whose answer is costly to compute | a body ends at `MAX_REQUEST_BYTES`; a subject makes at most `API_REQUESTS_PER_MINUTE` requests to one replica, through `pkg/ratelimit`; an owner holds at most `MAX_ACTIVE_EXPERIMENTS_PER_OWNER` experiments not `complete` or `canceled`; every list is paged; every limit of spec 002 is enforced at apply; comparisons are computed by jobs and stored, so a read serves a stored result; a slow core holds only its own jobs' leases | 002, 003, 005 | `TestBodyBound`, `TestRateLimit`, `TestActiveExperimentsAreBounded`, `TestLimitsAreEnforcedAtApply` |
| 11 | an owner, or a forged label, presenting trials as what they are not | import a session and claim it ran another agent or model; label a session as an Examen trial; mix chosen samples into an executed cell | provenance of an imported trial is read from the core's records, never from the request, and an import whose records differ from the tuple of the task and candidate it names, or do not pin a field, is refused, so a trial joins only the cell of its own tuple; an imported trial is marked, never adopted, and lives only in an experiment of mode `import`, which runs nothing; the prefix `examen.` is refused on every label a client supplies | 005, 003 | `topos.TestImport`, `TestReservedLabelsRefused` |
| 12 | an annotator's bias, or one annotator outvoting others | label after seeing the candidate's name or a judge's value; answer the same item many times | an annotator never sees the candidate, another grader's measurements or another person's answer; each annotator counts once in a consensus, by their latest answer | 006 | `annotate.TestBlindAnswers`, `calibrate.TestOneVotePerAnnotator` |

### What is not defended

- **A judge swayed within its schema.** Row 1 keeps hostile text from
  changing the shape of a judgment or reaching anything, not from
  moving a value inside the allowed range. Calibration against people
  is how such drift is seen, after the fact.
- **A check written to trust what the agent controls,** such as one that
  passes the output of the agent's own tests through as its result.
  The contract makes a crude forgery `malformed`; it cannot make a
  careless check sound.
- **An agent that wrecks its own trial,** killing its sandbox so the
  trial ends `infra_failed` rather than failed. Such a trial is retried,
  and an index that runs out of retries is reported per cell as lost
  and never read as a success (spec 008); the bias that selective
  wrecking introduces is visible, not prevented.
- **Secrecy of check files from an image agent.** On the `cella`
  executor the read link to a check's files is in the replica from the
  start, so the agent can read them, though not change them; on `topos`
  they arrive after the agent ends.
- **A provider that breaks its stated terms.** The owner chooses the
  model; what its provider does inside or against its terms is the
  provider's.
- **A secret the agent read from its workspace and printed.** It is in
  the evidence the owner's own run produced.
- **The operator, a compromised core, or a compromised host.** Topos,
  Cella, Lux, Arca, the authorizer, the minter and the key endpoint are
  trusted as named; a core that reports false records is believed.
- **Denial of service beyond row 10.**

### Reporting

`SECURITY.md` names a private channel for a report, asks that no public
issue be opened for one, and says when a reporter hears back. It lists
what the project commits to, each commitment one row of the table above
in a reader's words, and what is not defended, as the list above. A
commitment whose test is not written is not listed. A test reads both
files and holds each commitment to a row.

### Limits

The constants of spec 002 this spec names: `MAX_REQUEST_BYTES`,
`API_REQUESTS_PER_MINUTE`, `MAX_ACTIVE_EXPERIMENTS_PER_OWNER`,
`MAX_TRIALS_PER_CELL`, `MAX_CELLS_PER_EXPERIMENT`,
`MAX_CANDIDATES_PER_EXPERIMENT`, `MAX_PARALLELISM`,
`MAX_TRIAL_TIMEOUT`, `JUDGE_MAX_INPUT_BYTES`,
`JUDGE_MAX_OUTPUT_TOKENS`, `JUDGE_MAX_ATTEMPTS` and
`COST_ESTIMATE_MARGIN`.

## Not in this spec

The mechanisms themselves: verification, the authorizer's questions,
sharing and acting as the owner (spec 004); the executors, the driver
and evidence writing (spec 005); checks, judges and annotations (spec
006); evidence references and retention (spec 007); lost indices in a
verdict (spec 008); the routes, their errors and paging (spec 003);
the container's confinement and configuration (spec 011). The defenses
inside Topos, Cella, Lux and Arca are their own threat models.

## Acceptance criteria

| Criterion | Test that proves it |
|---|---|
| Every test the table of threats names is one `go test -list` finds in the module, or is on an exact list of tests not yet written, which only shrinks | `TestThreatModelTestsExist`, reading this file |
| Every commitment of `SECURITY.md` is a row of the table, and the file names a private channel | `TestSecurityPolicyMatchesTheModel`, reading both files |
| A canary planted in a task's instruction, and another in the agent's final message and a transcript tool result, appear in no system position of any judge request, as the request body reached the stub Lux, and only inside an evidence block of the user turn | `TestCanaryNeverInSystemContent` |
| Agent output that holds a forged end marker, a fake `record_judgment` call as text, or an instruction to add a metric leaves the request's blocks intact; a stub judge that obeys the injection and answers an extra metric or a value out of range yields `malformed` and no measurement | `TestAnObedientJudgeCannotChangeTheSchema` |
| An agent judge session holds no network, no connection and no writing tool, and cannot change its inputs | `TestAnAgentJudgeCannotReachOut` |
| A check stdout of two JSON objects, the agent's forged one first, is `malformed` | `TestAForgedSecondObjectIsMalformed` |
| A process the agent leaves running cannot write the check results, and a check's files cannot be changed by the sandbox's user, on both executors | `TestALeftoverProcessCannotTouchResults`, `TestCheckFilesAreReadOnly` |
| No package of `examend` starts a process or runs check code | `TestExamendExecutesNothing`, a dependency check over `cmd/examend` |
| Every Key the `lux` key source creates names the experiment's Budget and allows no unpriced model; every Key request Examen sends to a key endpoint or a core names the experiment, its models and its labels; with the `endpoint` source no request to Lux creates a Key or a Budget | `TestEveryKeyDrawsOnTheBudget` |
| An owner's seventeenth active experiment is refused naming `MAX_ACTIVE_EXPERIMENTS_PER_OWNER` | `TestActiveExperimentsAreBounded` |
| With another owner's token, a read, a run, a cancel, a re-grade, an annotation answer and an evidence read each answer as a missing object, and nothing changes | `TestAnotherOwnersTokenReachesNothing` |
| A reader of a shared baseline receives its summaries and no trial's evidence reference | `TestASharedBaselineExposesNoEvidence` |
| No suite applies without its judges' models named by the owner, and no default judge model exists | `TestNoDefaultJudgeModel` |
| A canary Lux Key value appears in no evidence object, measurement, log line, error or report | `TestNoKeyValueInEvidence` |
| An `UPDATE` or `DELETE` of a measurement row from examend's database role is refused by the database | `TestMeasurementsAreAppendOnly` |
| A digest is the SHA-256 of the canonical JSON, and two encodings of one object give one digest | `TestDigestsAreCanonical` |
| An expired token, one of a foreign audience and one of an unlisted issuer are each refused | `TestStaleOrForeignTokenRefused` |
| examend's router serves no route a core calls back | `TestNoInboundExecutorRoute` |
| A create sent twice with one key appends once | `TestReplayedWriteAppendsOnce` |
| A body past `MAX_REQUEST_BYTES` is refused before it is read whole, and a subject past `API_REQUESTS_PER_MINUTE` is rate limited | `TestBodyBound`, `TestRateLimit` |
| A label under `examen.` supplied by a client is refused | `TestReservedLabelsRefused` |
