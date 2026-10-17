# Specs

Design specs for Examen, a service that runs experiments on agents: it
runs each combination of task, agent, model and environment many times
through the services that run them, grades every trial, and reports
outcomes that can be compared, with their noise and confounds stated.
One spec covers one component. Each states the problem, the design with
enough precision to build from, and acceptance criteria that are
testable statements. Spec 001 fixes the architecture every other spec
assumes; read it first.

## Layout

Flat files `specs/NNN-name.md` in one number space with `track: core`
in the frontmatter. Numbers are stable identifiers and are never
reused. Open specs sit here and are the work queue. A terminal spec
moves to `specs/.archive/` keeping its number so `depends_on` paths
still resolve.

```mermaid
flowchart LR
  vague --> drafted --> validated --> dispatched --> in-progress --> testing --> complete
  drafted --> stale
  complete --> superseded
```

## Index

| Spec | Status | What it fixes |
|---|---|---|
| [001 Architecture](001-architecture.md) | drafted | the thin core, the cores it composes, how an experiment executes, the decisions, and the spec index |
| [002 Objects](002-objects.md) | drafted | every object, its fields, states, identifiers and limits |
| [003 API](003-api.md) | drafted | the contract a client codes against, its errors, and the event sink |
| [004 Identity and authorization](004-identity-and-authorization.md) | drafted | the authorizer contract, actions, the built-in owner policy, sharing, acting as the owner, the key source |
| [005 Execution](005-execution.md) | drafted | the executor contract, the `topos` and `cella` executors, smoke and the cost check, retries and retrying a parked cell, cancellation, imported trials |
| [006 Grading](006-grading.md) | drafted | checks, model judges, agent judges, annotations, the decline judge, generations, calibration |
| [007 Measurements and evidence](007-measurements-and-evidence.md) | drafted | the measurement store, provenance layers, evidence references and where evidence lives, retention, dataset export |
| [008 Analysis](008-analysis.md) | drafted | aggregation and intervals, tests, the primary result and the corrected family, confounds, automatic comparisons, verdicts and reasons, trends |
| [009 Bars](009-bars.md) | drafted | bar rules, evaluation, release use, bar events |
| [010 Reports](010-reports.md) | drafted | what a console reads and shows, the report document, export, sharing |
| [011 Distribution](011-distribution.md) | drafted | the binary, configuration, Postgres, upgrades, versioning |
| [012 Security](012-security-and-threat-model.md) | drafted | untrusted task content, judges reading hostile output, cost exhaustion, evidence privacy |

## Build order

The first consumer is a person comparing agents in a console (spec
001, decision 16), so the first slice is the path a comparison needs,
and the rest follows in this order.

| Step | What is built |
|---|---|
| first slice | 002 objects; 003 the routes the console path needs, the progress stream and the event sink; 004 without sharing, with both key sources; 005 the `topos` executor with smoke trials, the cost check, interleaved scheduling and cell retries; 006 checks, model judges and the decline judge; 007 measurements and evidence; 008 analysis, with automatic comparisons and descriptive label results; 010 what a console reads and shows, without the calibration column, the gap map and trends |
| bars | 009 |
| dataset export | 007 |
| agent judges and annotations | 006; 010 the calibration column |
| imported trials | 005 |
| the `cella` executor | 005 |
| adoption | 005, with the `window` confound of 008 |
| label tests | 008 |
| the gap map and trends | 008 trends; 010 the gap map and trends |
| the cross-architecture check | 008: the golden comparison run on amd64 and arm64 in CI |
| shares | 004 sharing; 003 the share routes; 010 sharing |
| static report exports | 010 reports, the narrative and its linter, and the exports |
