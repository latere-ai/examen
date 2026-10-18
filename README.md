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

> Examen is being built. `examend` serves suites, candidates and
> experiments whose trials your own harness runs: it leases them,
> records what comes back, and imports the runs of a study you already
> made. Agents and programs as candidates, checks and judges, and the
> statistics of a comparison are not served yet. The design is
> [`specs/001-architecture.md`](specs/001-architecture.md),
> [`specs/README.md`](specs/README.md) lists the specs in build order,
> and [`CHANGELOG.md`](CHANGELOG.md) says what each release serves.

## What it is for

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
- **Results you can share.** Publish a report, a suite others can run
  with the results submitted to it, or a dataset, as immutable
  versions: for the people you invite, for anyone with the link, or
  listed once your installation approves. You choose how much of the
  runs' content goes with it, a secret scan runs first, and you can
  withdraw a version at any time.

Each run with its grades is also one scored trajectory, so the same
rollouts can feed a training loop.

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

## License

[Apache-2.0](LICENSE).
