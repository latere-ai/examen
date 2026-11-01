# Documentation

| | |
|---|---|
| [Running it locally](local.md) | `examend` on your machine with Postgres and a local issuer: a task, two candidates, an experiment of ten trials each run by a stand-in harness, and the comparison Examen computes |
| [Concepts](concepts.md) | the objects, how each kind of candidate runs, grading and provenance, and the analysis: intervals, tests, corrections, verdicts and confounds |
| [Configuration](configuration.md) | every `EXAMEN_*` variable and its default, what each service adds, what refuses a start, the database, the image, the probes and telemetry |
| [Writing an authorizer](authorizer.md) | the question Examen asks, the answer and its limits, the rules every authorizer keeps, and the owner policy |
| [API reference](../api/openapi.yaml) | every route, request, response and error code, as OpenAPI 3.1; a running server also serves it at `<base path>/openapi.yaml` |

The design of each component, with the reasoning behind it, is in
[`specs/`](../specs/README.md). What each release changed is in the
[changelog](../CHANGELOG.md).
