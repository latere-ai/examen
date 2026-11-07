# Configuration

`examend` has three commands, `serve`, `migrate` and `version`, takes
no flags, and reads its whole configuration from `EXAMEN_*` environment
variables once, at start. This page lists every variable, says what each
one does in the current release, what each service adds, what refuses a
start and the telemetry. The image, the database, the probes and
upgrading are on [Installing examend](install.md).

Two variables are always required: `EXAMEN_DATABASE_URL` and
`EXAMEN_OIDC_ISSUERS`. Everything else has a default, and an unset
service address means that service is not used: the work that needs it
is refused with `422 core_not_configured`, naming the service, instead
of being accepted and left to wait.

Each bearer and signing secret is a file, named by a `*_TOKEN_FILE` or
`*_SECRET_FILE` variable and read at start, so a mounted secret carries
it. The database URLs carry their password in the variable, so keep
them in a secret too. A value that does not parse stops the start
with a line that names the variable and never its value.

## Every variable

Some variables are read and checked in this release but drive features
that are not built yet; they are marked **no effect yet**. Setting one
is harmless, and a value that does not parse still stops the start.

| Variable | Default | What it does |
|---|---|---|
| `EXAMEN_ADDR` | `:8080` | The address the listener binds, as host and port. |
| `EXAMEN_BASE_PATH` | `/v1` | The prefix of every API route. The probes stay at the root of the listener. Set it to `/` to serve the API at the root. |
| `EXAMEN_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. Logs go to standard error, one JSON object per line. |
| `EXAMEN_WORKERS` | `20` | How many work items, such as starting a session or grading a trial, one replica runs at once. |
| `EXAMEN_PUBLIC_URL` | none | Where readers reach this server, for links in bar evaluations, events and publications. **No effect yet.** |
| `EXAMEN_TRUSTED_PROXIES` | none | Comma-separated address ranges in CIDR form from which `X-Forwarded-For` names the client address the authorizer is told. A header from any other peer is ignored. |
| `EXAMEN_DATABASE_URL` | required | The direct `postgres://` URL of the database. `migrate` always uses it, and `serve` uses it unless `EXAMEN_DATABASE_POOL_URL` is set. Pool size and statement settings are the URL's own parameters. |
| `EXAMEN_DATABASE_POOL_URL` | none | A pooled endpoint, such as PgBouncer in transaction mode, that `serve` opens instead of the direct URL. |
| `EXAMEN_OIDC_ISSUERS` | required | Comma-separated OpenID Connect issuers whose tokens are accepted. `examend` reads each issuer's discovery document and keys. |
| `EXAMEN_OIDC_AUDIENCE` | `examen` | Comma-separated audiences a token may be addressed to, the first the primary one. |
| `EXAMEN_AUTHORIZER_URL` | none: the owner policy | The endpoint asked whether a caller may perform each action. See [Writing an authorizer](authorizer.md). |
| `EXAMEN_AUTHORIZER_TOKEN_FILE` | none; required with `EXAMEN_AUTHORIZER_URL` | A file holding the bearer the authorizer requires. |
| `EXAMEN_MINTER_URL` | none | The endpoint that issues a token for an experiment's owner, which Examen uses for every call it makes to another service on that owner's behalf. Needed for `topos` candidates and for grading. |
| `EXAMEN_MINTER_TOKEN_FILE` | none; required with `EXAMEN_MINTER_URL` | A file holding the bearer the minter requires. |
| `EXAMEN_SOLE_OWNER` | none | The one owner of an installation that reads that owner's tokens from files instead of a minter, written as `<issuer>\|<sub>`. **No effect yet**: token files are not built. Setting it together with `EXAMEN_MINTER_URL` stops the start. |
| `EXAMEN_OWNER_TOKEN_DIR` | none; required with `EXAMEN_SOLE_OWNER` | The directory of the sole owner's token files. It must exist. **No effect yet.** |
| `EXAMEN_KEYS_URL` | none | The key endpoint: an installation's service that issues the model Keys an experiment uses and reports their spend. With it, an external harness receives its experiment's own Key with each lease, and model judges get theirs. Without it, no Key is issued, so model judges are refused. |
| `EXAMEN_KEYS_TOKEN_FILE` | none; required with `EXAMEN_KEYS_URL` | A file holding the bearer the key endpoint requires. |
| `EXAMEN_TOPOS_URL` | none | The root of [Topos](https://github.com/latere-ai/topos)'s API with its base path, such as `https://topos.example.com/v1`. With `EXAMEN_MINTER_URL`, candidates of kind `topos` run, each trial one Topos session. Without the minter, `topos` candidates stay refused and the start logs a warning. |
| `EXAMEN_CELLA_URL` | none | The root of [Cella](https://github.com/latere-ai/cella)'s API. With Arca and the minter, a task's checks run in a Cella sandbox with no network. |
| `EXAMEN_CELLA_ENVIRONMENT` | none; required with `EXAMEN_CELLA_URL` | The Cella Environment check sandboxes are created in. |
| `EXAMEN_LUX_URL` | none | The root of [Lux](https://github.com/latere-ai/lux), the model gateway. With Arca, the minter and a key endpoint, model judges call their model through it. |
| `EXAMEN_ARCA_URL` | none | The root of [Arca](https://github.com/latere-ai/arca), the file store. A `topos` trial's kept files are read from it for grading, and each check's standard output and error are written to it. |
| `EXAMEN_ARCA_PUBLIC_URL` | `EXAMEN_ARCA_URL` | Where readers reach Arca, for links in published evidence. **No effect yet.** |
| `EXAMEN_FILESTORE_DOWNLOAD_ONLY` | `false` | The operator's declaration that the file store serves every link read as a download no browser renders, which publications that carry files will require. **No effect yet.** |
| `EXAMEN_ORIGO_URL` | none | The root repositories are cloned from, as `<root>/<repository>.git`. With it, tasks may start from an `origo` workspace, which a `topos` trial's session is given. |
| `EXAMEN_TOPOS_AUDIENCE` | `topos` | The audience of the owner token asked for to call Topos. |
| `EXAMEN_CELLA_AUDIENCE` | `cella` | The audience of the owner token asked for to call Cella. |
| `EXAMEN_LUX_AUDIENCE` | `lux` | The audience of the owner token asked for to call Lux. |
| `EXAMEN_ARCA_AUDIENCE` | `arca` | The audience of the owner token asked for to call Arca. |
| `EXAMEN_ORIGO_AUDIENCE` | `origo` | The audience of the owner token asked for to call Origo. |
| `EXAMEN_EVENTS_URL` | none | The installation's event sink. **This release delivers no events, so setting it stops the start.** |
| `EXAMEN_EVENTS_SECRET_FILE` | none; required with `EXAMEN_EVENTS_URL` | A file holding the events' signing secret, or two separated by a comma while one is rotated. |

`serve` also reads the standard OpenTelemetry variables (below).

## What each service adds

Examen runs nothing itself. It defines experiments, records what comes
back and computes the analysis; the services it is given run and meter
the work. With none of them, an installation runs experiments whose
trials an external harness runs, and imports runs a study already made.

| With | Examen can |
|---|---|
| Postgres and an issuer only | apply suites, tasks, metrics, graders and candidates; run experiments of `external` candidates through the lease, renew and submit routes; import past runs; compute every grid figure and comparison |
| a key endpoint | hand each experiment of an external harness its own model Key, drawn on the experiment's Budget, and read back each trial's cost and the model that served it |
| Topos and a minter | run candidates of kind `topos`: each trial is one unattended session of a pinned agent version, created as the experiment's owner |
| Cella and Arca, with the minter | grade a `topos` trial's run with its task's checks, in a sandbox with no network, and keep each check's output |
| Lux, Arca and a key endpoint, with the minter | grade a `topos` trial's run with model judges, each pinned to its model and prompt |
| Origo | start a `topos` trial's session from a pinned repository commit |
| an authorizer | decide who may do what, for organizations, shared reads and services acting for others; without one, the owner policy lets each caller act on their own objects alone |

## Refusals at start

`serve` exits with status 1 before it listens, with one line naming the
variable or the condition, when:

- `EXAMEN_DATABASE_URL` or `EXAMEN_OIDC_ISSUERS` is missing, or any value
  does not parse.
- One of a pair is set without the other: the authorizer, minter and key
  endpoint each with its token file, `EXAMEN_CELLA_URL` with
  `EXAMEN_CELLA_ENVIRONMENT`, `EXAMEN_SOLE_OWNER` with
  `EXAMEN_OWNER_TOKEN_DIR`, and the events URL with its secret file.
- `EXAMEN_MINTER_URL` and `EXAMEN_SOLE_OWNER` are both set.
- A token file is missing, unreadable or empty.
- The authorizer, minter, key endpoint, a service URL, the events URL or
  `EXAMEN_ARCA_PUBLIC_URL` is not `https` and its host is not loopback:
  each carries a bearer, a Key or an owner's data.
- `EXAMEN_EVENTS_URL` is set.
- The database does not answer, or its schema lacks a migration the
  binary carries, was left dirty by a failed migration, or belongs to
  another major version.
- The authorizer allows the probe question, a request no authorizer that
  reads the request would allow.

An issuer that does not answer at start, or an authorizer that does not
answer the probe, is logged and the server starts. Every request then
fails closed until it answers.

## Telemetry

With `OTEL_EXPORTER_OTLP_ENDPOINT` set, `serve` exports traces, metrics
and logs over OTLP, and reads the other standard OpenTelemetry variables
as the OpenTelemetry SDK defines them. Without it, `serve` logs to
standard error alone.
