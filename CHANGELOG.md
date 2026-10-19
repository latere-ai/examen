# Changelog

What changed for whoever runs `examend`, calls its API, or builds on the
public packages, one section per release. A `v*` tag without a section
here is refused before it is pushed.

## Unreleased

### Added

- An experiment run by your own setup can call models with a key of its
  own. With a key source configured (`EXAMEN_KEYS_URL`), creating an
  experiment of `external` candidates asks the key source for the
  experiment's key, and every lease and renewal of its trials answers it
  as `model_key`, with `Cache-Control: no-store`. The key draws on the
  experiment's budget, so its spending cap stops what your setup spends
  through it, and it reaches every model your account does, so a call to
  another model than the candidate declared is recorded, not refused. It
  is released when the experiment completes or is canceled. No read of
  the experiment and no log line carries it. Your setup may keep using a
  key of its own; its runs' cost then reads unknown, as before.
- Each leased trial carries `lux_labels`, `examen.trial=<trial id>`: send
  it as the `Lux-Labels` header on each of the trial's model calls.
- A trial whose calls went through the experiment's key gets its cost and
  what it sent and was served from Lux's records of them, about two
  minutes after it is submitted: `observed.cost_state` and `cost_usd`,
  `observed.served` and the token counts, a new `sent` member with each
  model the calls named, and the built-in metrics `cost_usd`,
  `input_tokens`, `output_tokens` and `reasoning_tokens`. A call with no
  label that names none of your trials counts toward the experiment and no
  trial.
- Candidates of kind `topos` run. With Topos's address
  (`EXAMEN_TOPOS_URL`) and the minter (`EXAMEN_MINTER_URL`), each trial
  of a `topos` candidate is one unattended session of the candidate's
  agent version, on its model and reasoning level, created at Topos as
  the experiment's owner on behalf of the person who created it. The
  task's instruction is the session's one message, an `origo` workspace
  is the repository at its commit, cloned from `EXAMEN_ORIGO_URL`, and
  the session ends when its turn ends. A question it asks is answered at
  once, since nobody attends it. Its log, the model requests it made and
  the files it kept in its own folder are the trial's evidence; the
  requests give its `sent` layer, its cost and the built-in metrics, with
  `turns` and `tool_calls`. A cancel stops the sessions of the trials it
  ends. A create checks, as you, that Topos holds each candidate's agent
  version (`422 unresolved_reference`) and that your sign-in is kept for
  work that runs after you leave (`403 sign_in_not_kept`), and refuses
  what a session cannot carry yet: a candidate's `tools`, a task's
  `environment` or `arca` workspace, a task with no `instruction`, and an
  `origo` workspace with no Origo address.
- `GET /experiments/{id}` carries the budget's `cap_usd_micro`,
  `spent_usd_micro` and `remaining_usd_micro` when the key source answers
  them, and `usage`: the calls made with the experiment's key, their cost,
  and those no trial claims as `unattributed`.

### Changed

- Run `examend migrate` before you roll `serve`: this release adds the
  tables that keep an experiment's key, the records of its calls and
  each trial's sent layer.
- A lease is refused `403 forbidden`, `422 budget_refused` or `503
  owner_tokens_unavailable` when the key source refuses the experiment's
  key, has no money left on it, or does not answer, and takes no trial.
- A `topos` candidate on an installation that does not start Topos
  sessions is refused `422 core_not_configured` naming `topos`, in place
  of `executor_unavailable`. `examend migrate` adds the column that marks
  a session to stop after its trial was canceled.

## v0.5.0 - 2026-10-19

### Added

- Label metrics are tested. A comparison's label result on a task now
  carries Fisher, Freeman and Halton's exact test of its two label
  distributions, or a permutation of Pearson's chi-square when the
  tables are too many to enumerate, and at suite level the stratified
  label test, the chi-square summed over the tasks and permuted within
  each. A significant difference is `changed`, and the per-label
  differences keep their intervals. A comparison that was final before
  this release keeps its label results descriptive.
- Place a task in a design with `factors`, such as
  `{"object": "build", "volition": "want"}`: names and levels of 1 to 63
  characters of `[a-z0-9._-]`, at most `MAX_TASK_FACTORS` (16), none
  named `candidate`, `task`, `model`, `reasoning` or `batch`. A task
  without factors keeps its digest.
- Declare a comparison over any cell variable: `{"variable": "object",
  "levels": ["build", "do"], "where": {"candidate": "sha256:…"}, "by":
  ["volition"]}` compares the cells at the first level with those at the
  second, within the cells `where` keeps and stratified by `by`. A cell
  carries `candidate`, `task`, its `model` and `reasoning`, and its
  task's factors. With `by`, each stratum is a task-level result named
  by its levels, such as `volition=want`, or by its task for `by:
  ["task"]`, and the suite-level result tests over the strata; without
  it, each side is one cell and its one result per metric is the
  primary one. The comparison answers its `declaration`, its `candidate`
  is empty when its candidate side spans several candidates, and its
  primary result joins the one corrected family of every declared
  comparison. In the experiment's `comparisons` such an entry carries
  `variable`, `levels`, `where` and `by`, and no `candidate`. A
  declaration whose variable no cell carries, whose level selects no
  cell, whose stratum holds two cells of one side, or whose paired cells
  also differ in a factor it neither holds nor names, is refused at
  create, naming the member. The pair and task-pair forms are
  unchanged.
- Any declared comparison takes a `metric`, by name or digest and
  answered by digest, whose result is its primary result in place of
  the suite's primary metric. A pair that names one is a comparison of
  its own beside the automatic comparison.
- Declare a test of independence with `independence` on `POST
  /experiments`: `{"metric": "kind"}` tests whether a trial-level label
  metric depends on the candidate, stratified by task, over every cell;
  `variable`, `by` and `where` test it against any cell variable,
  stratified by others, over the cells you keep, by candidate unless you
  name the strata when the variable is the task or a factor. It is one
  planned test, tested alone at 5%: `GET /experiments/{id}/declared`
  answers it under `independence` with its levels, their summaries, its
  strata and `p`, and the experiment answers its declaration. Over
  values your harness reported itself it answers `p` without a verdict,
  as every result on such values does.
- A result read from a model or agent judge's labels carries
  `uncalibrated`, naming the judge, until the judge agrees with people
  on enough of your trials (Krippendorff's alpha of at least 0.67 over
  at least 30); no annotation route is served yet, so every judge reads
  uncalibrated. A value your harness reports itself is never
  calibrated.
- Candidate-level values reach the comparisons that read their
  metric.

### Changed

- Run `examend migrate` before you roll `serve`: this release adds an
  experiment's `independence` and the mark that recomputes it, and a
  comparison's `declaration`, all empty for what exists, so the release
  before it serves the migrated schema with a warning. Do not roll back
  once an experiment declares a comparison of the general form: the
  release before reads it as a pair with no candidate.
- A comparison still computing is computed with label tests from its
  next computation, so its `inputs_digest` and seed change, and a
  Monte Carlo `p` of any of its metrics may move within its sampling
  error with no new trial.
- One comparison declared twice is refused across forms too: a pair of
  candidates and the general form of the same pair, two orders of one
  `by`, or one metric named by name and by digest.

## v0.4.1 - 2026-10-19

### Security

- A request about an object you may not see is answered exactly as a
  request about one that does not exist, status, headers and body, on
  every route that names a stored object by id, name, digest or
  reference, in its path, its query or its body. The developer detail of
  these `404` answers is reworded: it names the object as your request
  named it.
- `POST /trials/{id}/submit`, `POST /experiments/{id}/work/lease` and
  `GET /suites/{suite}/taxonomies/{metric}/history` check the body, the
  `candidate` reference and the page before they read the object the
  path names, so a malformed request is refused `400` or `422` before
  any `404`. A page link into a taxonomy's history is now issued for the
  metric as the path names it, by name or by digest; one issued before
  this release to a request that named the metric by name is refused
  `400 invalid_cursor`, so start again from the first page.
- `PUT /suites/{suite}/taxonomies/{metric}` asks `taxonomy.apply` before
  it answers that the suite or its metric does not exist, with `owner`
  and `name` alone: a caller the authorizer denies is answered `403
  forbidden`, and one it allows `404 taxonomy_not_found`.

## v0.4.0 - 2026-10-18

### Added

- Pass your installation's handle for a create to its authorizer:
  `sign_in` on `POST /experiments` is an opaque value of 1 to 255 bytes
  of visible ASCII (`SIGN_IN_MAX_BYTES`) that reaches the authorizer as
  the field `sign_in` of `experiment.create` and nowhere else. Examen
  does not store it, answer it, log it or write it to an event, so an
  installation that keeps a sign-in for each experiment its people start
  can tie a create to the one its client obtained. A value that is empty,
  too long or not visible ASCII is `400 invalid_field`.

## v0.3.0 - 2026-10-18

### Added

- Declare the comparisons an experiment will make when you create it:
  `comparisons` on `POST /experiments` takes pairs of its candidates,
  each with an optional `variable`, and pairs of tasks for one
  candidate, at most 128. Each is computed as the automatic comparison
  is, from the first finished trial to its final form, and is never
  exploratory. Their primary results are corrected together, by
  Benjamini and Hochberg over every comparison you declared, in the
  experiment's declared analysis at `GET /experiments/{id}/declared`,
  which gives each its `p`, `q`, verdict and reason; the comparison
  itself carries its primary result's `p` alone. A declared pair that is
  the automatic comparison's own pair is that comparison. A comparison
  answers `declared`, its position in the list, and
  `GET /experiments/{id}/comparisons?declared=true` lists them. The
  list is fixed at create, so declare what you mean to test before any
  trial runs.
- `scheduling: interleaved` on `POST /experiments` deals the
  experiment's trials round robin across every cell of every task, so
  trial `i` of every cell runs in one time window and two tasks
  compared with each other do not compare two times. `task_by_task`,
  the order every experiment ran in so far, stays the default.
- `POST /experiments/{id}/work/lease` without a `candidate` takes the
  trials of every external candidate, in the order the experiment's
  scheduling deals them. Every leased trial now names its `candidate`,
  with the configuration your harness declared for it, so workers that
  lease without a candidate know what to run. Such a lease asks
  `trial.lease` with no `candidate` and no `harness`: an authorizer
  that allows one harness's leases alone refuses it.

### Fixed

- A lease never holds more trials at once than the experiment's
  `parallelism`. A lease could take more when the trials of a later
  task were held and an earlier task's rollout opened.

### Changed

- Run `examend migrate` before you roll `serve`: this release adds an
  experiment's `scheduling`, its declared comparisons and its declared
  analysis, and a comparison's `declared`, with defaults, so the
  release before it serves the migrated schema with a warning.
- Every share is a grant of reading. A suite or a baseline shared with
  you is read and never run on: the owner policy refuses an
  `experiment.create` or `experiment.update` whose `suite_owner` or
  `baseline_owner` names another owner, shared or not, and the specs
  no longer let `suite.use` or `baseline.use` rest on a share. An
  authorizer of your own should refuse the same questions.
- The specs keep every subject out of the public reads. A publication
  version's `published_by` is answered by the signed-in reads alone,
  `GET /publications/{id}` and `GET /publications/{id}/versions/{n}`,
  and never under `/public/`; a snapshot leaves out a released
  narrative's `released_by` and a measurement's `annotator`; and a
  version whose owner neither your authorizer nor the request names
  shows `attribution: null` instead of the owner's subject. A page that
  names who signed off reads the version signed in and shows the name it
  resolves, never the subject.
- The specs define the stratified label test, the summed chi-square of
  a label metric permuted within each task, as a label result's
  suite-level test and as an experiment's declared test of independence
  over all its candidates, both served with label tests; and a model
  judge that reads a directory output's UTF-8 files in path order,
  served with judges.

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
