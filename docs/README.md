# Documentation

| | |
|---|---|
| [Running it locally](local.md) | `examend` on your machine with Postgres and a local issuer: a task, two candidates, an experiment of ten trials each run by a stand-in harness, and the comparison Examen computes |
| [Installing examend](install.md) | what an installation needs, the image and how to verify it, the database, running, probes and shutdown, upgrading and rollback, and what production must have |
| [Concepts](concepts.md) | the objects, how each kind of candidate runs, grading and provenance, and the analysis: intervals, tests, corrections, verdicts and confounds |
| [Configuration](configuration.md) | every `EXAMEN_*` variable and its default, what each service adds, what refuses a start and telemetry |
| [Using the API](api.md) | calling the API, the objects, the flows of an app and of an external harness, the errors to branch on, and every bound a request meets |
| [Writing an authorizer](authorizer.md) | the question Examen asks, the answer and its limits, the rules every authorizer keeps, and the owner policy |
| [Security](security.md) | what Examen guarantees, what it sends to each service and keeps, what it logs, what it does not defend, and what an operator keeps safe |
| [API reference](../api/openapi.yaml) | every route, request, response and error code, as OpenAPI 3.1; a running server also serves it at `<base path>/openapi.yaml` |

The design of each component, with the reasoning behind it, is in
[`specs/`](../specs/README.md). What each release changed is in the
[changelog](../CHANGELOG.md).
