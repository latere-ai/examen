# Examen

**A service that runs experiments on agents.** Describe a task once,
name the agents, models and environments to try it with, and say how
many times each combination runs. Examen starts every run through the
services that already run agents, sandboxes and model calls, grades
what each run produced, and reports outcomes you can compare: every
number comes with its noise, and every comparison names what else
differed besides the thing you meant to change.

The name is Latin for the tongue of a balance, the pointer that shows
which way it tips.

> Examen is at the design stage. Nothing here runs yet. The design is
> [`specs/001-architecture.md`](specs/001-architecture.md), and
> [`specs/README.md`](specs/README.md) lists the specs in build order.

## What it is for

Whenever a model, an agent's instructions or its tools change, the same
question comes back: did the change move what the agent does on the
tasks you care about? Examen makes that loop declarative:

- **Rollouts, not anecdotes.** Each combination runs N times, and the
  run-to-run spread is measured, so a difference within the noise is
  reported as noise.
- **Comparisons that say what they compare.** Every run is pinned to its
  full conditioning context: the agent version, the model and what
  actually served, the environment and the task. When two results
  differ in more than one of those, the report says so.
- **Graders you can trust over time.** A model-based judge is pinned to
  its model and prompt; changing either makes a new judge, and
  re-grading never overwrites an earlier grade.
- **Budgets that hold.** Every suite has a spending cap, and each
  combination is probed once before it is run N times.
- **Evidence kept.** Transcripts, workspaces and request records stay
  available for the analysis nobody planned.

Each run with its grades is also one scored trajectory, so the same
rollouts can feed a training loop.

## What it is not

Not a benchmark dataset, not a leaderboard, and not an agent runtime,
sandbox or model gateway. Examen calls those through their APIs. Who may
run what is answered by an authorizer you write, or by a built-in policy
that lets an owner act on their own suites.

## License

[Apache-2.0](LICENSE).
