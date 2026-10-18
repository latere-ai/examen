# Changelog

What changed for whoever runs `examend`, calls its API, or builds on the
public packages, one section per release. A `v*` tag without a section
here is refused before it is pushed.

## Unreleased

## v0.2.0 - 2026-10-18

### Fixed

- `POST /experiments` that names no `owner` now resolves its suite and
  candidates in the owner context your authorizer names. It asks
  `experiment.list` first and resolves in the owner that allow's `owner`
  limit names, so a member creating in an organization's context reaches
  the organization's suite instead of being refused
  `reference_not_found`. An authorizer whose list allows name no owner
  sees no change. Roll the authorizer that answers `experiment.list` with
  an `owner` limit before this release.

### Changed

- The specs state who creates, who runs and who signs off: a person
  creates under an owner, a person or an organization they belong to, so
  an experiment's `owner` and `created_by` can differ and both are
  answered; only the creator, the owner and the owner's members run an
  experiment, and a share, a link or a publication lets everyone else
  read it; a published benchmark runs on its owner's side alone and
  takes submissions from it alone; a publication version answers
  `published_by`, the person who signed off on publishing it, on every
  read.

## v0.1.0 - 2026-10-18

The first release serves the v0.1 profile: suites, candidates of kind
`external` and experiments whose trials your own harness runs or a study
you already made imports, with every comparison and grid figure computed
by Examen's analysis. It calls no core and mints no owner token. Agents
and programs as candidates, checks and judges, publishing and the console
are not part of it.

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
  `list`, `update`, `cancel` and `import`; `cell.retry`; `trial.lease`
  and `trial.submit`; `experiment.run`, which Examen asks as the
  experiment's creator, with no claims, before each rollout it submits
  and before it hands out a cell's external trials; and `baseline.pin`,
  `read` and `list`, with the fields each sends and the limits an allow
  may carry. The allow of a `.list` question may name the owner a name
  sent without `owner` is resolved in, so a member reads by name what it
  applied in an organization's context. The owner policy allows
  `experiment.run` when the experiment's owner and creator are the
  subject, and reads a shared baseline.
- Metrics, graders, tasks, suites and candidates: `PUT /{kind}/{name}`
  applies a version, idempotent by its digest, with `If-Match` and
  `If-None-Match`; `GET /{kind}/{ref}` reads one by name, `name@digest`,
  digest or id; `GET /{kind}/{name}/versions` lists a name's versions;
  `GET /{kind}` lists current versions with the filters of the API spec.
  A suite is held to what it pins: no two tasks of one name, one
  producer per metric and task, a cell-level metric's `of` listed. A
  program credential naming an audience Examen acts through is `422
  credential_audience_reserved`.
- Experiments: create in mode `execute` or `import`, list, read with
  progress, cancel, close an import's intake, the candidate × task grid,
  cells, trials with their current values and `metric`/`value` filters,
  measurements by generation, the event log as JSON and as server-sent
  events (`GET /experiments/{id}/stream`, with `Last-Event-ID` replay and
  keepalives), and `POST /cells/{id}/retry` for a cell parked `blocked`
  or `over_budget`; a retry of a complete experiment's cell reopens it.
- External harnesses: `POST /experiments/{id}/work/lease` leases a
  candidate's pending trials within each cell's share of the
  experiment's parallelism, smoke trials first and main trials once a
  cell's smoke passes, for 10 minutes, renewable; a lapsed lease returns
  its trials to pending. `POST /trials/{id}/submit` records the outcome,
  the times, the declared harness version and image, the Lux request
  ids as given, the outputs as Arca paths with their digests, and values
  the harness computed itself under `self:<harness>`, marked
  `self_reported`. An `infra_failed` trial is retried up to twice with
  `retry_of` lineage; a smoke trial that keeps failing parks its cell
  `blocked`; an `over_budget` trial parks its cell. The built-in
  metrics `completed` and `duration` are filled at done, and `declined`
  by the built-in decline judge when the suite lists it.
- Past runs: `POST /experiments/{id}/imports` with `executor: external`
  imports a harness's past run, with its past times and declared harness
  version, which must equal the candidate's version, into an experiment
  in mode `import`.
- Baselines: pin, read, history, list and by id. Taxonomies of an
  open-vocabulary metric: append a version, read, history, the raw
  labels the current version does not map, and by id.
- Comparisons: one automatic comparison of every candidate against the
  experiment's reference, made at create; `POST /comparisons` asks for
  one, against another candidate, a baseline or, with `"variable":
  "task"`, the candidate's cell of another task, exploratory; list, read
  with suite-level results, and page task-level rows.
- The analysis computes every comparison: per-task and suite-level
  summaries with their intervals, the test each metric's type and
  aggregate names, the Benjamini and Hochberg correction of a
  comparison's family beside its primary metric tested alone, a verdict
  and a plain reason for every result, and the confounds of the
  provenance tuple, the declared images, the service versions, the
  excluded trials and the time windows. An automatic comparison is
  computed once its candidate's first trial finishes, recomputed in
  place with `final` false as trials arrive, and computed a last time
  with `final` true when the experiment completes; one on request is
  computed once. Each records its `inputs_digest`, `seed`,
  `analysis_version` and `parameters`, and the measurements it read, so
  the same inputs give the same figures bit for bit on every machine. A
  value an external harness reported for itself keeps its `p`, carries
  the `self_reported` confound, and has no `q` and no verdict: it is
  never a finding.
- The candidate × task grid shows each cell's summary of the metric,
  and the verdict and reason of its candidate's automatic comparison.
- `experiment.create` sends your authorizer `grader_kinds`, the kinds of
  every grader the suite pins, an empty list for a suite that pins none,
  so it can tell work that needs no owner token.
- A request with an `Idempotency-Key` is served for at most a minute;
  a repeat while it is served is `409 request_in_progress` for up to
  five minutes, after which a stopped replica's key lapses.
- `422 executor_unavailable` refuses a candidate whose executor this
  release does not run.
- `GET /openapi.yaml` names the server's own base path in `servers`.

### Not yet

- Candidates of kind `topos` and `program` are stored and refused at an
  experiment's create, `422 executor_unavailable`, until their executors
  exist.
- No grader runs but the built-ins: a suite whose tasks have checks is
  `422 core_not_configured` with `core: cella`, and one whose graders,
  decline judge or canonicalizers are model judges `core: lux`, agent
  judges `core: topos`. Annotation queues are stored and fill nothing.
- Examen makes no call to Lux, Arca, Cella, Topos or Origo in this
  release, and obtains no owner token: a named Budget is recorded as
  named and not read; a cap is `422 core_not_configured` unless your
  authorizer assigns a Budget; a trial's cost reads `unknown`, never
  zero; an `execute` task with inputs is refused, since its input links
  come from Arca; outputs are taken only as Arca paths with digests,
  recorded as declared and never read, and a run's transcript and final
  message are not accepted; imports of Topos sessions and Cella
  sandboxes are `422 core_not_configured`. No pin is resolved at its
  core when an experiment is created.
- Of the analysis: label tests, which leave a label metric's results
  descriptive, with no `p`, `q` or verdict; cell-level and
  candidate-level metrics; the `uncalibrated` confound; benchmark
  entries and trends. A grid
  cell's summary is computed when the grid is read, by the same
  procedures a stored figure would be, and the spend and outcome figures
  of the stored analysis are not served.
- Sink events are journaled and not delivered: `serve` refuses to start
  while `EXAMEN_EVENTS_URL` is set.
- Bars, shares, dataset exports, annotations, re-grading, changing and
  deleting experiments, reports and publishing.
- The console, a client of this API that ships apart.
