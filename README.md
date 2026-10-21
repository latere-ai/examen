# Examen

**A service that runs experiments on agents and on programs built on
models.** Describe a task once, with the inputs it gives and the
outputs you expect, name the systems to try it with and the models and
environments to try them on, and say how many times each combination
runs. A system under test can be an agent, a program in an image, such
as a vendor's command-line agent or an HTTP client of an API that uses
models, or a configuration of your own evaluation harness. Examen
starts every run through the services that already run agents,
sandboxes and model calls, or hands it to your harness, grades what
each run produced, and reports outcomes you can compare: every number
comes with its noise, and every comparison names what else differed
besides the thing you meant to change.

The name is Latin for the tongue of a balance, the pointer that shows
which way it tips.

> **Status: early releases.** `examend` runs experiments whose trials
> your own harness runs or a study you already made imports, and
> experiments on agents, each trial one unattended session of the agent,
> graded by checks in a sandbox and by model judges. It computes every
> comparison with its intervals, tests, corrected
> verdicts and confounds. [What works today](#what-works-today) says what
> a release serves and what is still design;
> [`CHANGELOG.md`](CHANGELOG.md) says what each release changed, and
> [`specs/README.md`](specs/README.md) lists the specs in build order.

## What it is for

This section is the design; [What works today](#what-works-today) says
how much of it a release serves.

Whenever a model, an agent's instructions or tools, or a program's
configuration change, the same question comes back: did the change
move what the system does on the tasks you care about? Examen makes
that loop declarative:

- **Rollouts, not anecdotes.** Each combination runs N times, and the
  run-to-run spread is measured, so a difference within the noise is
  reported as noise.
- **Comparisons that say what they compare.** Every run is pinned to its
  full conditioning context: the agent version or program image and
  configuration, the model and what actually served, the versions of
  the services it called, the environment and the task. When two
  results differ in more than one of those, the report says so.
- **Planned tests stay planned.** Declare the comparisons an experiment
  will make before any trial runs, over any variable you designed in:
  two candidates, two prompts, a factor your tasks declare or a
  reasoning level, within or across the rest, and whether a label
  depends on one of them. Their headline results are corrected
  together; a comparison asked for afterwards is marked exploratory. Trials can be interleaved across every task and
  candidate, so the conditions you compare run in the same window.
- **Expected outputs scored without a model.** Built-in comparators
  score an output against a reference answer, by exact match, numeric
  tolerance, set overlap, text overlap or field-level precision and
  recall, and the system under test never sees the reference.
- **Graders you can trust over time.** A model-based judge is pinned to
  its model and prompt; changing either makes a new judge, and
  re-grading never overwrites an earlier grade.
- **Budgets that hold.** Every suite has a spending cap, and each
  combination is probed once before it is run N times.
- **Evidence kept.** Transcripts, workspaces, outputs and request
  records stay available for the analysis nobody planned.
- **External harnesses.** A harness of your own can lease trials and
  submit what it produced, or bring the runs of a study it already
  made; Examen grades the outputs and marks what it can only take on
  trust.
- **Open-ended answers, measured.** A judge can label what each run
  did in its own words; a taxonomy you keep, versioned, groups those
  labels, and Examen tests whether two candidates' label distributions
  differ and how often each fixes on one answer.
- **Results you can share.** Publish a report, a benchmark of a suite
  with the results your side ran on it, or a dataset, as immutable
  versions: for the people you invite, for anyone with the link, or
  listed once your installation approves. Whoever you share with reads
  what you shared, and only your side runs your experiments. You
  choose how much of the runs' content goes with it, a secret scan
  runs first, and you can withdraw a version at any time.

Each run with its grades is also one scored trajectory, so the same
rollouts can feed a training loop.

## What works today

| | |
|---|---|
| **Suites, tasks, candidates, metrics and graders** as versioned objects: applied by name, idempotent by digest, read by name, digest or id | works |
| **An external harness**: your own setup leases trials, renews them and submits what each produced, smoke trials first; a failure of the infrastructure is retried, and a cell whose smoke keeps failing is parked | works |
| **Imported runs** of a study you already made, with their past times and declared harness version | works |
| **The analysis**: per-task and suite-level summaries with their intervals, the test each metric's type names, a family corrected together, a verdict and a plain reason for each result, and the confounds that differ besides the thing compared; recomputed as trials arrive, and the same inputs give the same figures on every machine | works |
| **Declared comparisons**, fixed before any trial runs and corrected together, over two candidates, two tasks, a task's factors or any other cell variable, and a declared test of independence | works |
| **Label metrics**: two label distributions tested exactly, or by permutation when the tables are too many, per task and stratified across tasks; a judge's labels marked uncalibrated until people agree with it | works |
| **Interleaved scheduling**: trial `i` of every cell runs in one time window | works |
| **Agent candidates** (`topos`): each trial is one unattended session of the agent's version, on its model and reasoning level, created as the experiment's owner; its log, model requests and kept files are the evidence, and its cost and tokens are read from them | works |
| **The experiment's own model key** for an external harness, drawn on the experiment's budget, with each trial's cost and what was served read back from the model gateway's records | works |
| **Checks** in a sandbox with no network, and **model judges** pinned to their model and prompt, grading an agent candidate's trials; each grade is Examen's own measurement, never self-reported | works |
| Checks and judges of an external harness's trials, canonicalizers, a configured decline judge and judges of a candidate as a whole | refused, not built |
| **Program candidates** run in sandboxes, the HTTP runner, built-in comparators, agent judges and annotations | not built |
| **Bars, dataset export, reports and publishing** | not built |
| **The console** | a client of this API that ships apart |

An experiment that needs a service this installation does not name is
refused `422 core_not_configured`, naming the service, never accepted
and left to wait.

## Run it

You need Go 1.27 or newer and a container runtime, Podman or Docker.

```sh
make        # the quality gate
make build  # out/examend
make run    # builds, migrates a local Postgres container, and serves
```

`examend migrate` applies the schema to `EXAMEN_DATABASE_URL`; run it
before you start or roll `examend serve`, which never migrates. `serve`
verifies bearer tokens from the issuers `EXAMEN_OIDC_ISSUERS` lists, for
an audience in `EXAMEN_OIDC_AUDIENCE`, and asks your authorizer at
`EXAMEN_AUTHORIZER_URL`, or applies the built-in owner policy when none
is set. Everything else is optional: `EXAMEN_TOPOS_URL` with
`EXAMEN_MINTER_URL` runs agent candidates, `EXAMEN_KEYS_URL` hands an
experiment its model key, and `EXAMEN_CELLA_URL`, `EXAMEN_ARCA_URL` and
`EXAMEN_LUX_URL` with the key source grade their trials by checks and
model judges.
[`specs/011-distribution.md`](specs/011-distribution.md) lists every
variable. Each release publishes the `examend` image for linux/amd64
and linux/arm64.

## What it is not

Not a benchmark dataset and not a bare leaderboard: a published
benchmark gives each entry a rank interval its data supports and a
comparison with a reference entry, never a single rank. Not
an agent runtime, sandbox, model gateway or HTTP client of the APIs it
evaluates. Examen calls the services that run things through their
APIs, and an API under evaluation is called by a program inside a
sandbox, never by Examen itself. Who may run what is answered by an
authorizer you write, or by a built-in policy that lets an owner act on
their own suites.

## Contributing

[`CONTRIBUTING.md`](CONTRIBUTING.md) is how to build and test a change.
[`SECURITY.md`](SECURITY.md) is what the project protects and where to
report a vulnerability; please do not open an issue for one.

## License

[Apache-2.0](LICENSE).
