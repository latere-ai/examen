# Concepts

What Examen's objects are, how a trial runs and is graded, and how the
analysis turns trials into results you can act on. The design behind
each part, with its reasoning, is in [`specs/`](../specs/README.md);
this page is the part a user needs.

## The objects

Five kinds are **versioned definitions**. Each is applied by name with
`PUT`, identified by the SHA-256 digest of its content, and never
changed in place: applying different content under the same name makes
a new version, and applying the same content again makes nothing. One
object refers to another by digest, `sha256:...` or `name@sha256:...`,
so an experiment always records exactly what it ran.

| Object | What it is |
|---|---|
| **Metric** | What a trial is scored on: its type (`binary`, `score`, `count`, `duration`, `cost` or `label`), how a cell aggregates it (`rate`, `mean`, `median`, `p90`, `distribution`, and the cell-level `repeat_rate`, `mode_share` and `distinct_count`), and which direction is better, if any. |
| **Grader** | What produces a metric's values from a trial: a check, a model judge, an agent judge or an annotation queue. |
| **Task** | What a system under test is asked to do: an instruction, optional inputs and reference answers, the outputs to keep, a timeout, the workspace it starts from, and its checks. |
| **Suite** | The tasks and metrics an experiment runs, with the metric its headline result reads, and the graders that apply to every task. |
| **Candidate** | One fully pinned system under test (below). |

The rest are **records** of running them:

| Object | What it is |
|---|---|
| **Experiment** | One execution of a suite against candidates, each combination run `trials` times, with a Budget, a reference candidate, and optionally comparisons declared before any trial runs. |
| **Cell** | One task and one candidate of an experiment. It deals one smoke trial first and the rest only once that passes, so a broken setup costs one trial. A cell whose smoke keeps failing, or whose Budget runs out, parks until its owner retries it. |
| **Trial** | One run of a cell, with its outcome (`completed`, `declined`, `timed_out`, `turn_limit`, `over_budget`, `infra_failed` or `canceled`), its times, and what was recorded about it. A trial that failed for the infrastructure's reasons is retried; the one it replaced is kept and marked retried. |
| **Measurement** | One value of one metric for one trial, from one grader, with its provenance. Measurements are appended and never changed: a grade once written is never overwritten. |
| **Comparison** | The results of a candidate's cells against a reference, per task and across the suite, recomputed as trials arrive and final once its figures are (below). |
| **Baseline** | A named, pinned reference for a suite, such as the candidate that is in production, that later experiments compare against. |
| **Taxonomy** | A versioned mapping that groups the free-form labels a judge wrote into the labels a metric counts. |

## Candidates and how their trials run

Examen runs no agent, sandbox or model call itself. A trial runs where
its candidate's kind says, through a service that already runs such
work, or in your own harness.

| Kind | What it pins | How a trial runs | In this release |
|---|---|---|---|
| `topos` | an agent version of [Topos](https://github.com/latere-ai/topos) by digest, a model, a reasoning level and tools | one unattended Topos session, created as the experiment's owner with a token from the minter; its log, model requests and kept files are the evidence, and its cost and tokens are read from them | works, with Topos and a minter |
| `external` | your harness's name, version and settings, as it declares them | your harness leases trials, runs them and submits what each produced (`mode: execute`), or you import runs of a study you already made, with their past times (`mode: import`) | works |
| `program` | a program in an image by digest, its command, configuration, model and credentials | one run in a [Cella](https://github.com/latere-ai/cella) sandbox | not built: an experiment that names one is refused `422 executor_unavailable` |

Trials run as their experiment's owner, never as the service: every
session, sandbox and model call is made with the owner's identity and
drawn on the owner's Budget. An API under evaluation is called by a
program inside a trial's sandbox, never by Examen.

## Grading

A value is either **Examen's own measurement** or **self-reported**.

- **Built-in measurements** of every trial: `builtin:completed`,
  `builtin:declined` and `builtin:duration`, read from the trial's
  outcome and times.
- **Checks** run commands over a trial's kept files after the trial
  ends, in one Cella sandbox per trial with no network, in the task's
  order. Every file is checked against the digest the trial's log
  recorded. A check of one binary metric passes with exit 0 and fails
  with exit 1; any other check prints one JSON object naming a value for
  each metric it produces. Its standard output and error are kept in the
  owner's file store.
- **Model judges** read a trial's final message, kept files, transcript
  and check results, and answer through one structured tool call. A
  judge is pinned to its model and prompt: changing either makes a new
  judge, so an old grade is never silently reinterpreted.
- **Self-reported values** are what an external harness submits about
  its own trials. Examen stores them, marks them, and never treats them
  as a finding (below).

In this release checks and model judges grade the trials of `topos`
candidates. Grading an external or imported trial's outputs, agent
judges, annotation queues, canonicalizers, a configured decline judge
and judges of a candidate as a whole are designed and not built; an
experiment that needs one is refused when it is created.

## Provenance and confounds

Every trial records what was configured, what was sent and what was
observed: the agent version or harness declared, the model and
reasoning level its requests named, the model that actually served
them, the image it ran in, and the versions of the services it called.
When two sides of a comparison differ in more than the thing you meant
to change, the result names each difference as a **confound**, such as
`observed.model` when some requests were served by another model than
the one asked for, `declared.image` when the two sides ran in different
images, or `window` when their trials ran more than a week apart. A comparison also
says whether its candidates differ in a single variable.

## The analysis

Every figure is computed by Examen's own code, deterministically: the
same inputs give the same figures, bit for bit, on every machine. A
comparison records the inputs digest, the seed and the parameters it was
computed with.

**Cell summaries.** Each cell summarizes each metric by its aggregate,
with a 95% interval: Wilson's score interval for a rate, a bootstrap-t
interval for a mean, an order-statistic interval for a median or 90th
percentile, and Wilson's interval for each label's share of a
distribution.

**Tests.** A task-level result compares the candidate's cell with the
reference's: Boschloo's exact test for a rate, studentized permutation
tests for a mean or quantile, and Fisher, Freeman and Halton's exact
test, or a chi-square permutation test when the tables are too many,
for a label distribution. A suite-level result combines the tasks with
their weights, stratified by task, and names the tasks it leaves out.

**Corrections.** A comparison's headline is the suite-level result of
the suite's primary metric against the reference declared before the
experiment ran: one pre-declared test, reported as is. Every other
result of the comparison is one family, corrected together with
Benjamini and Hochberg's procedure at a false discovery rate of 5%,
and keeps both its `p` and its corrected `q`. A comparison asked for
after the trials ran is **exploratory**: its primary result joins the
family and it is never headlined. Comparisons declared at create, over
two candidates, two tasks, a task's factors or another cell variable,
have their primary results corrected together across the experiment.

**Verdicts.** Each result is `improved`, `regressed`, `changed` (for a
metric with no direction), `within_noise`, or `insufficient` when a
task's side has fewer than 5 valued trials or a suite-level result
covers less than 80% of the suite's weight, and carries a one-sentence
reason that says what decided it. The verdict comes from the
test alone; the interval of the difference beside it is descriptive. A result that reads self-reported values keeps
its `p` but has no `q` and no verdict, carries the `self_reported`
confound, and stays out of the family, so a harness's claim about itself
never changes what the correction allows any other result.

**Label metrics.** A judge's labels are tested as distributions, per
task and stratified across tasks, and an experiment can declare a test
of whether a label depends on a cell variable. The repeat rate measures
how often a candidate gives the same answer twice. A judge's labels are
marked `uncalibrated` until people's labels agree with it.

**When a comparison is final.** A comparison is recomputed as trials
finish and is `final: true` once every trial of both sides is done and
graded and its figures were computed after that. Until then its figures
are provisional.

The analysis also holds a benchmark's rank intervals, simultaneous
intervals from Holm-adjusted pair results, which publishing will serve;
no route serves them yet.

## What is not built yet

Designed in the specs and not in this release: candidates of kind
`program` and the HTTP runner, built-in comparators that score outputs
against reference answers without a model, agent judges and annotation
queues, bars that a release check passes or fails, reports, dataset
export, publishing results with links and listings, comparisons across
batches, and event delivery. The [changelog](../CHANGELOG.md) says what
each release added.
