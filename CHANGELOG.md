# Changelog

What changed for whoever runs `examend`, calls its API, or builds on the
public packages, one section per release. A `v*` tag without a section
here is refused before it is pushed.

## Unreleased

### Added

- `examend serve`, `examend migrate` and `examend version`, configured by
  the `EXAMEN_*` variables of the distribution spec. `serve` checks every
  variable before it opens anything and stops with one line naming the
  variable, never its value, when one is missing or does not parse, when a
  bearer file is missing or empty, or when an endpoint that carries a
  bearer is plain HTTP to a host that is not loopback.
- Run `examend migrate` against `EXAMEN_DATABASE_URL`, the direct
  connection, before you start or roll `serve`. `serve` never migrates: it
  refuses a schema that lacks a migration it carries, a dirty schema and a
  schema of another major, and serves a newer schema of its own major with
  a warning, so a rollback within a major needs no restore.
  `EXAMEN_DATABASE_POOL_URL` may name a pooler in transaction mode.
- The probes `GET /livez`, `GET /readyz` (naming `draining` or `database`
  when it fails) and `GET /version`, outside the base path and with no
  token, and the contract at `GET /v1/openapi.yaml`.
- Every request carries an `X-Request-Id`, yours or one the server makes;
  a body over 4 MiB is `413 request_too_large`; a caller over 600 requests
  a minute on one replica is `429 rate_limited` with `Retry-After`.
- Bearer tokens are verified against `EXAMEN_OIDC_ISSUERS` for an audience
  `EXAMEN_OIDC_AUDIENCE` lists. Decisions come from your authorizer at
  `EXAMEN_AUTHORIZER_URL`, or from the built-in owner policy when none is
  set; a decision that does not arrive is a refusal. An authorizer that
  allows the start-up probe stops the start.
- The public package `authorizer` publishes the actions this release asks
  your authorizer: `metric.*`, `grader.*`, `task.*` and `candidate.*`
  `apply`, `read` and `list`; `suite.apply`, `suite.read`, `suite.list`,
  `taxonomy.apply` and `taxonomy.read`; `experiment.create`, `read`,
  `list`, `update`, `cancel` and `import`; `trial.lease` and
  `trial.submit`; and `baseline.pin`, `read` and `list`, with the fields
  each sends and the limits an allow may carry.

### Not yet

- The objects, experiments and their routes are not served yet; the API
  answers its contract and `404 not_found` everywhere else.
- Sink events are not delivered: `serve` refuses to start while
  `EXAMEN_EVENTS_URL` is set.
