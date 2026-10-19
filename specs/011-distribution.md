---
title: "Distribution: the examend binary and its commands, configuration by environment, Postgres migrations, the image, probes, telemetry, versioning and release, upgrade and rollback, and a standalone installation"
status: in-progress
track: core
depends_on:
  - specs/001-architecture.md
  - specs/002-objects.md
  - specs/003-api.md
  - specs/004-identity-and-authorization.md
  - specs/005-execution.md
  - specs/006-grading.md
  - specs/007-measurements-and-evidence.md
  - specs/008-analysis.md
  - specs/009-bars.md
  - specs/010-reports.md
affects: [cmd/examend/, internal/config/, internal/version/, internal/store/migrations/, test/stubs/, deploy/, docs/, Makefile, Dockerfile, .lateregate.yaml, .githooks/, .github/, CHANGELOG.md]
effort: medium
created: 2026-10-17
updated: 2026-10-19
author: changkun
---

# Distribution

## Overview

How Examen is built, configured, run and shipped. It is one binary,
`examend`, with three commands, configured by environment variables
listed once in this spec. It keeps everything in Postgres under
migrations the binary carries, ships as one image and as tagged releases
that each have a changelog section, and serves probes and telemetry the
way every open core does. It runs on its own against any OpenID Connect
issuer and the open cores it composes, and needs no other service. With
no owner token source, no key source and no file store it serves the
v0.1 profile, which is what the first release serves (below).

## Design

### Layout

| Path | Holds |
|---|---|
| `cmd/examend/` | the binary: `serve`, `migrate`, `version`; the e2e tier sits beside the function `serve` runs |
| `cmd/examen-trial/` | the trial driver that runs in a `cella` replica, built into the trial driver image whose digest the build pins (spec 005) |
| `cmd/examen-compare/` | the built-in comparators, built into the comparator image whose digest each release names (spec 006) |
| `cmd/examen-http/` | the HTTP runner, built into the HTTP runner image whose digest each release names (spec 005) |
| `cmd/examen-scan/` | the secret scanner and its pinned rule set, built into the scanner image whose digest the build pins (spec 013) |
| `api/` | public: `openapi.yaml` (spec 003), embedded for the server to serve |
| `authorizer/` | public: the action vocabulary, the limits of an allow, the minter's request and answer, and the key endpoint's request and answer (spec 004) |
| `internal/access/` | verification, the authorizer client, the owner policy |
| `internal/owner/` | the owner token source: the minter client and the token files |
| `internal/keys/` | the key source: the `endpoint` implementation and, after v0.1, the `lux` one (spec 004) |
| `internal/httpapi/` | routes, errors, the progress stream |
| `internal/store/` | the store over Postgres, and `migrations/` |
| `internal/queue/` | the work queue and its leases (spec 005) |
| `internal/execute/` | the executor contract and the `topos`, `cella` and `external` executors; `internal/luxread/` reads Lux's records (spec 005) |
| `internal/grade/`, `internal/calibrate/` | checks, judges, annotations and calibration (spec 006) |
| `internal/evidence/`, `internal/export/` | evidence references and dataset export (spec 007) |
| `internal/analysis/` | the analysis (spec 008) |
| `internal/bar/` | bars (spec 009) |
| `internal/events/` | the sink events' signing and delivery, under the delivery lease (spec 003) |
| `internal/view/`, `internal/report/`, `internal/narrative/` | the report document, reports and the narrative linter (spec 010) |
| `internal/publish/` | publications, their snapshots, scans and Arca links, submissions and the public reads (spec 013) |
| `internal/config/` | configuration, read once into one typed value |
| `internal/id/` | prefixed, time-ordered identifiers (spec 002) |
| `internal/version/` | the build's version, commit and date, stamped by the linker |
| `test/stubs/` | a stub issuer, authorizer, minter and key endpoint, and stubs of Topos, Cella, Lux, Arca and Origo |
| `deploy/base/`, `deploy/examples/generic/` | manifests with no host or account in them, and an example installation under `example.com` |
| `docs/` | running it, the configuration reference, the API guide, writing an authorizer, a minter and a key endpoint |

The public packages are `api` and `authorizer`, which a client, an
authorizer, a minter and a key endpoint import. Go, current release, no cgo. Community
files: `LICENSE` (Apache-2.0), `SECURITY.md`, `CODE_OF_CONDUCT.md`,
`CONTRIBUTING.md`, `CHANGELOG.md`.

### The gate

`make check` runs `go tool lateregate`, the bar every push and every
hook runs: formatting, no cgo, license notices, the spec tree, the
identity and Postgres declarations, lint, the vulnerability check, and
the suite under the race detector with a coverage floor of 90% per
package. `.lateregate.yaml` declares what is this repository's own:
`identity` with `role: core`, `audience: examen`, `config_prefix:
EXAMEN`, `reached_by: self-hosted`; `postgres` with `role: pooled` and
`prefix: EXAMEN`; the license holder; and each package that cannot clear
the floor, with its reason. `make hooks` points git at `.githooks`,
whose two hooks call the gate.

The suite runs in three tiers, once, with coverage taken over every
package:

| Tier | Proves |
|---|---|
| unit | pure logic: configuration, digests, the state machines, the error table, the owner policy |
| integration | the store against a real Postgres started by Testcontainers: every query, the queue under contention, the migrations, and serving through a pooler in transaction mode |
| e2e | the whole server in process against the stubs: every route, an experiment end to end, a rolling restart mid-experiment, two replicas over one database, and the canary tests of spec 004 |

Where no container runtime answers, the integration tests skip and the
floor fails for the packages only that tier reaches, so a skipped tier
is never a green gate. `TestEveryRouteIsExercised` compares the routes
the e2e tier called with the OpenAPI document.

### The binary

`examend` takes one command and no flag, and reads its configuration
from the environment.

| Command | Does | Reads |
|---|---|---|
| `serve` | reads the configuration, checks the schema, warms the issuers, probes the authorizer, then serves the API and runs the executors until a signal | every variable below |
| `migrate` | applies the migrations the binary carries that the database lacks, then exits | `EXAMEN_DATABASE_URL`, `EXAMEN_LOG_LEVEL` |
| `version` | prints the version, the commit, the build date and the newest schema version the binary carries | nothing |

A missing or unknown command prints the usage on stderr and exits 2.
Otherwise a command exits 0 on success and 1 on failure, with one line
on stderr that names what failed.

`serve` starts in this order: configuration, telemetry, the database
pool, the schema check, the issuers, the authorizer probe (spec 004),
the owner token source, the key source, the listener; and only then
does `/readyz` answer `200`. Its first log lines say which token source
is in use (the minter, the token files of `EXAMEN_SOLE_OWNER` after
v0.1, or none), which key source (`endpoint`, `lux` after v0.1, or
none), whether it serves the v0.1 profile, who decides (the authorizer
or the owner policy), and which cores are configured.

### Configuration

Every variable is `EXAMEN_*` and is listed once, here, with the spec
that owns its meaning. `internal/config` reads each once at start into
one typed value, and no other package reads the environment; the one
exception is `pkg/otel`, which reads the standard OpenTelemetry
variables as the SDK defines them. The table is generated into
`docs/configuration.md`, and a test fails when the binary reads a
variable the table lacks. A spec that adds a variable adds its row here
in the same change. The `EXAMEN_` variables Examen sets inside a trial's
sandbox for the agent and its checks (specs 005 and 006) are not
configuration, and `examend` reads none of them.

| Variable | Default | Owner |
|---|---|---|
| `EXAMEN_ADDR` | `:8080` | this spec |
| `EXAMEN_BASE_PATH` | `/v1` | spec 003 |
| `EXAMEN_PUBLIC_URL` | none: bar evaluations and events carry no `links`, a report bundle's sources are not links, and a publication has no shareable page link | specs 003, 009, 010 and 013 |
| `EXAMEN_TRUSTED_PROXIES` | none: a public read's client address is its peer address. Set, a comma list of address ranges in CIDR form, from which `X-Forwarded-For` names the client address the public routes rate limit | spec 003 |
| `EXAMEN_LOG_LEVEL` | `info`; or `debug`, `warn`, `error` | this spec |
| `EXAMEN_DATABASE_URL` | none; required | this spec |
| `EXAMEN_DATABASE_POOL_URL` | none: `serve` opens `EXAMEN_DATABASE_URL` | this spec |
| `EXAMEN_OIDC_ISSUERS` | none; required | spec 004 |
| `EXAMEN_OIDC_AUDIENCE` | `examen` | spec 004 |
| `EXAMEN_AUTHORIZER_URL` | none: the owner policy | spec 004 |
| `EXAMEN_AUTHORIZER_TOKEN_FILE` | none; required with `EXAMEN_AUTHORIZER_URL` | spec 004 |
| `EXAMEN_MINTER_URL` | none | spec 004 |
| `EXAMEN_MINTER_TOKEN_FILE` | none; required with `EXAMEN_MINTER_URL` | spec 004 |
| `EXAMEN_SOLE_OWNER` | none; read from the release that builds token files, after v0.1 | spec 004 |
| `EXAMEN_OWNER_TOKEN_DIR` | none; required with `EXAMEN_SOLE_OWNER`, after v0.1 | spec 004 |
| `EXAMEN_KEYS_URL` | none: no key source, which with no owner token source and no file store is the v0.1 profile; after v0.1, the `lux` key source, which creates Keys at Lux as the owner, when `EXAMEN_LUX_URL` and an owner token source are set | spec 004 |
| `EXAMEN_KEYS_TOKEN_FILE` | none; required with `EXAMEN_KEYS_URL` | spec 004 |
| `EXAMEN_TOPOS_URL` | none: candidates of kind `topos` and agent judges are refused; the root of Topos's API with its base path, such as `https://topos.example/v1`. Set without `EXAMEN_MINTER_URL`, no owner token reaches Topos, so `topos` candidates stay refused and the start logs it | spec 005 |
| `EXAMEN_CELLA_URL` | none: candidates of kind `program`, `external` candidates on a suite whose tasks have checks, and every publication version, whose secret scan runs in Cella, are refused | spec 005 |
| `EXAMEN_CELLA_ENVIRONMENT` | none; required with `EXAMEN_CELLA_URL`: the `queued` Cella Environment a SandboxSet and a check sandbox run in | spec 005 |
| `EXAMEN_LUX_URL` | none: model judges, agent judges, the `lux` key source and a named Budget no authorizer assigned are refused, and every trial's cost is `unknown`; an experiment that needs none of them, as in the v0.1 profile, is created | spec 005 |
| `EXAMEN_ARCA_URL` | none: an `arca` workspace a trial starts from, tasks with inputs or references, outputs carried inline, transcripts and final messages, judges that read evidence, exports, and publications that carry files are refused; an `external` trial's outputs are taken only as `arca` references with their digests, recorded and not read (the v0.1 profile) | spec 007 |
| `EXAMEN_ARCA_PUBLIC_URL` | `EXAMEN_ARCA_URL`: the address readers reach Arca at, on which a public read builds evidence links | spec 013 |
| `EXAMEN_FILESTORE_DOWNLOAD_ONLY` | `false`: publication versions at `outputs` and `full` are refused, `422 file_store_not_download_only`. `true` declares that every link read of the file store answers `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff` and `Content-Security-Policy: sandbox`, so a published file a model wrote is downloaded and never rendered on the file store's origin; Examen cannot check the declaration, and an operator sets it only once the file store does so | spec 013 |
| `EXAMEN_ORIGO_URL` | none: tasks with an `origo` workspace are refused; the root a repository is cloned from as `<root>/<repository>.git`, which a `topos` trial's session is given | spec 005 |
| `EXAMEN_TOPOS_AUDIENCE` | `topos` | spec 004 |
| `EXAMEN_CELLA_AUDIENCE` | `cella` | spec 004 |
| `EXAMEN_LUX_AUDIENCE` | `lux` | spec 004 |
| `EXAMEN_ARCA_AUDIENCE` | `arca` | spec 004 |
| `EXAMEN_ORIGO_AUDIENCE` | `origo` | spec 004 |
| `EXAMEN_WORKERS` | `20`: work items one replica runs at once | spec 005 |
| `EXAMEN_EVENTS_URL` | none: sink events are journaled acknowledged and nothing is sent. Set, `serve` refuses to start in a release that does not deliver sink events, v0.1 among them (Refusals at start) | spec 003 |
| `EXAMEN_EVENTS_SECRET_FILE` | none; required with `EXAMEN_EVENTS_URL`: a file holding one signing secret, or two separated by a comma while one is rotated, reread when it changes | spec 003 |
| the variables `pkg/otel` reads | the SDK's own | this spec |

A refusal for a core that is not configured is `422
core_not_configured` naming the core (spec 003). A core's URL is its
root; Examen joins the core's own API paths to it. `EXAMEN_OIDC_ISSUERS`
and `EXAMEN_OIDC_AUDIENCE` are comma-separated lists. A duration is
written as Go writes one. A token file holds one bearer, so a Secret is
mounted and no variable carries a bearer or a secret; the events
signing secret is a file too. The database pool's size
and statement settings are the URL's own parameters. A certificate
authority of the operator's own is trusted the way the process trusts
any, through the system's roots.

A value that does not parse is an error that names its variable and
never its value, since a variable may hold a secret: a database URL
holds a password.

### Refusals at start

`serve` stops before it listens, with status 1 and one line that names
the variable or the condition, in each of these cases:

| Case | Why |
|---|---|
| a required variable is missing | `EXAMEN_DATABASE_URL` and `EXAMEN_OIDC_ISSUERS` always; the others as the table says |
| a value does not parse | the line names the variable |
| `EXAMEN_MINTER_URL` and `EXAMEN_SOLE_OWNER` are both set | two token sources would make it unclear whose token a request carries (spec 004) |
| `EXAMEN_EVENTS_URL` without `EXAMEN_EVENTS_SECRET_FILE`, the secret without the URL, or an `http://` events URL whose host is not loopback | events would go unsigned, nowhere, or readable on the way (spec 003) |
| `EXAMEN_EVENTS_URL` is set, in a release that does not deliver sink events: every release before event delivery is built, v0.1 among them | the installation would believe it receives events that are never sent; without the URL, events are journaled and nothing is sent (spec 003) |
| `EXAMEN_KEYS_URL` without `EXAMEN_KEYS_TOKEN_FILE`, or the token file without the URL | a key endpoint takes a bearer, and a bearer with no endpoint means the installation believes Examen asks it for Keys while Examen would create them itself (spec 004) |
| a token file is missing, unreadable or empty | the endpoint or core it is for requires a bearer |
| the authorizer, minter, key endpoint or a core URL is not `https` and its host is not loopback | each carries a bearer, and an owner token or a Key over plain HTTP is readable on the way |
| `EXAMEN_ARCA_PUBLIC_URL` is not `https` and its host is not loopback | readers follow it with an Arca link token in the path, which plain HTTP exposes on the way |
| the database does not answer, or its schema fails the check below | the server cannot serve what it cannot store |
| the authorizer allows the probe | spec 004 |

### Postgres and migrations

Postgres holds every object of spec 002, the work queue (spec 005), the
event log, the delivery lease and the idempotency keys (spec 003), the
shares (spec 004), and the publications with their snapshots and
submissions (spec 013). Evidence stays in Arca. Any Postgres release that its project
still supports is accepted.

Migrations are SQL files in `internal/store/migrations/`, numbered,
embedded in the binary, and applied with `latere.ai/x/pkg/pgxmigrate`,
whose table records the version and whether a migration is dirty. Only
forward migrations are shipped. The first migration of each major
release writes that major into the one-row table `examen_schema`, which
`serve` reads.

`examend migrate` opens `EXAMEN_DATABASE_URL`, the direct connection,
never a pooler, since the migrator holds a session lock. It applies the
pending migrations in order under that lock, so two runs at once apply
each migration once, and the second waits and then finds nothing to do.
It exits 0 when the schema holds every migration the binary carries,
whether it applied them now or they were there. A migration that fails
leaves the schema dirty, and `migrate` exits 1 naming it; the way back
is the backup taken before the upgrade (below).

`serve` checks the schema at start and in `/readyz`:

| The database's schema | `serve` |
|---|---|
| holds every migration the binary carries, and none newer | serves |
| lacks one the binary carries | refuses: `run examend migrate` |
| holds migrations newer than the binary, of the same major | logs a warning naming them, and serves: this is a rollback within a major |
| belongs to another major | refuses |
| is dirty | refuses |

`serve` may serve through `EXAMEN_DATABASE_POOL_URL`, a pooler in
transaction mode, so the replicas' pools do not each claim the
database's connections. The store uses nothing such a pooler loses: no
session-level advisory lock, no `LISTEN`, and no state kept on a
connection between transactions. The queue claims work with row locks
inside one transaction, sink event delivery runs under a lease row
taken in a transaction, and a progress stream polls the event log
(spec 003).

**The compatibility rule.** Each migration is additive, or is preceded
by one release that writes both shapes, so a replica of release N and a
replica of release N+1 serve the same database while a rollout is half
done, and the previous minor's binary serves the newer schema. From
v1.0.0 a migration that breaks this is only in a new major. Before
v1.0.0 such a migration is named in its changelog section with what a
replica of the previous release does until it is replaced.

### The image

There is one server image, `examend`: the static binary on a distroless
base, with the roots it verifies certificates against, a numeric user
that is not root, no shell and no package manager. It is built for
linux/amd64 and linux/arm64 from one `Dockerfile` that cross-compiles,
and is at most `IMAGE_MAX_SIZE`. It starts and answers with a read-only
root file system and every capability dropped; `examend` starts no other
program and writes no file. The same image runs `serve` and `migrate`.
The trial driver, comparator, HTTP runner and secret scanner images
are built from the same tree, each one static binary on an empty base,
for the same platforms; they run only inside Cella sandboxes, never
beside `examend`.

### Probes

`latere.ai/x/pkg/health` serves the probes on the main listener, outside
the base path, with no token:

| Path | Answers |
|---|---|
| `/livez` | `200` while the process runs; it never carries an error |
| `/readyz` | `200` when ready; otherwise `503` with the failing check's name and error. The checks: `database`, the pool answers and the schema check holds; `draining`, which fails from the stop signal on |
| `/version` | `{version, commit, build_time}` as the linker stamped them; the release workflow compares `version` with the tag |

Readiness does not depend on the authorizer, the minter, the key
endpoint, the issuers or the cores. Their outage fails the requests that need them with a `503`
and holds unattended work (spec 004), and taking a replica out of
rotation would not bring them back.

### Shutdown

On `SIGINT` or `SIGTERM` a replica fails readiness at once, stops
claiming work from the queue, releases the work it claimed and has not
begun so another replica takes it (spec 005), closes its progress
streams without `end` so clients reconnect elsewhere with
`Last-Event-ID` (spec 003), stops accepting connections, gives open
requests `SHUTDOWN_GRACE` to finish, and exits 0. Trials keep running in
their executors throughout: a replica's exit never ends a trial, and
another replica follows each cell's rollout by its handle (spec 005).
The deployment's termination grace is longer than `SHUTDOWN_GRACE`.

### Telemetry

`latere.ai/x/pkg/otel` sets up logs, traces and metrics with
`otel.Bootstrap`, the service name `examend` and the stamped version.
Export starts when `OTEL_EXPORTER_OTLP_ENDPOINT` is set; without it,
logs are JSON lines on stderr at `EXAMEN_LOG_LEVEL` and traces and
metrics are discarded. Every request is a span through `otel.Handler`.
Every outbound call goes through `otel.HTTPClient`, so a call's span
joins the core's trace; its instrumentation scrubs each outbound URL
before it reaches a span or a log line, dropping the query and the
token segment of an Arca link's path, so no link Examen mints, a
`prepare` job's input links among them, reaches telemetry. Each work item is a span carrying
`examen.experiment`, `examen.cell` and `examen.trial` as attributes.

| Metric | Kind | Labels |
|---|---|---|
| `examen_experiments` | gauge | `state` |
| `examen_trials_total` | counter | `executor`, `outcome` |
| `examen_queue_depth` | gauge | `work` |
| `examen_authorizer_calls_total`, `examen_authorizer_call_seconds` | counter, histogram | `result`: `allow`, `deny`, `error` |
| `examen_minter_calls_total`, `examen_minter_call_seconds` | counter, histogram | `audience`, `result`: `token`, `refused`, `error` |
| `examen_key_leases_total` | counter | `source` (`lux`, `endpoint`), `purpose`, `result`: `leased`, `refused`, `exhausted`, `error` |
| `examen_core_requests_total` | counter | `core`, `class` (`2xx`, `4xx`, `5xx`, `error`) |
| `examen_streams_open` | gauge | none |

No log line, span, event or metric label carries a token, a Lux Key's
value, a publication's link token, an Arca link's token or another
credential (spec 004), nor an owner's content: a task's instruction, a
transcript, an evidence file or a judge's input. A request is logged
and traced by its route pattern, never its path, which on a public
route holds a link token. Identifiers and rendered subjects are logged;
content is not.

### Versioning and release

A release is a tag `vX.Y.Z` with a section in `CHANGELOG.md`.

| Change | Bump |
|---|---|
| a route, field, error code, event type, configuration variable, action, resource field or limits member, or a comparator's parameter or quantity or an HTTP runner `config` member, removed or given another meaning | major |
| a new route, field, error code, event type, variable, metric, action, resource field, limits member, comparator, comparator parameter or quantity, or HTTP runner `config` member | minor |
| a fix with no visible change | patch |

Before v1.0.0 a minor may break, with a changelog entry that names the
break. From v1.0.0 a client written against release N works against
release N+1 within one major. The base path names the API's major,
`/v1`; a new API major is served beside the old one for at least one
minor. The vocabulary and the minter's contract are versioned with the
API, and a new action is named in the changelog, since an operator's
authorizer must learn it. The two most recent minors receive fixes.

`CHANGELOG.md` keeps `## Unreleased` at its top, and a change a person
running `examend` or calling its API would notice adds a note there in
the same commit, written for that reader. `lateregate release vX.Y.Z`,
from a green main, moves those notes under a section for the version,
tags and pushes. A tag whose commit has no section is refused by the
pre-push hook and by the release workflow.

The release workflow waits for the verify run of the tagged commit,
builds the five images for both platforms, signs them, attests their
bills of materials and their provenance, checks that the `examend`
image's `/version` reports the tag, archives `deploy/` with the image
pinned by digest, and creates the release from the changelog section,
naming the comparator and HTTP runner digests a grader or a candidate
pins, and the scanner digest a publication's scan records.

### Upgrade and rollback

1. Read the changelog section of every release between the running one
   and the target.
2. Take a backup of the database with `pg_dump`. It is the only way
   back across a migration the previous release cannot serve.
3. Run `examend migrate` with the new image, as a job, before the
   rollout.
4. Roll the `serve` replicas to the new image.
5. Check `/readyz` and that `/version` reports the new release.

During the rollout, replicas of both releases serve one database under
the compatibility rule. Experiments in flight continue: their work queue
is in Postgres, their trials run in the executors, and the new replicas
take each claimed item as the old ones release it and follow each cell's
rollout by its handle. An upgrade starts no trial twice and loses none.

A rollback deploys the previous image. Within a major, the previous
binary warns about the newer schema and serves it, and no migration is
undone. Across a migration that the changelog names as one the previous
release cannot serve, the database is restored from the backup, which
loses what was written since. Trials started after the backup are then
unknown to the database; their sessions and sandboxes remain at their
cores, labeled with their experiment (spec 005), and can be imported
(spec 003).

### Standalone

Examen needs no service but the cores it composes, an OpenID Connect
issuer and Postgres. Every address is configuration with no default
host.

| Needed | For | Without it |
|---|---|---|
| Postgres | everything | `serve` does not start |
| an OpenID Connect issuer, any | the callers' tokens | `serve` does not start |
| an owner token source: a minter, or, after v0.1, `EXAMEN_SOLE_OWNER` with its token files | every request to a core (spec 004) | the v0.1 profile: definitions, `external` candidates and past trials, graders without checks or judges, comparisons and reads; nothing that resolves a pin at a core |
| Lux | model judges, agent judges, cost; with the `lux` key source, the Keys and the Budget Examen creates | those are refused, and every trial's cost is `unknown` |
| a key endpoint | Keys from an installation that keeps its own budgets (spec 004) | no key source in v0.1; after v0.1, with Lux and an owner token source, the `lux` key source: Examen creates its Keys at Lux as the owner |
| Topos, itself running sessions in Cella | candidates of kind `topos`, agent judges | those are refused |
| Cella | candidates of kind `program`; the check sandboxes of `cella` and `external` trials; the secret scan of every publication version | those are refused |
| Arca | `arca` workspaces, task inputs and references, outputs Examen writes or reads, evidence for judges, exports, published files and their links | those are refused, and an `external` trial's outputs are recorded as `arca` references and not read |
| Origo | `origo` workspaces | those are refused |
| an authorizer | organizations as owners, annotators and services acting for others, limits | the owner policy (spec 004) |

One person can run the v0.1 profile for themselves with Postgres, an
issuer and the owner policy alone, and, after v0.1, all of Examen with
`EXAMEN_SOLE_OWNER` and their own tokens in files, the owner policy,
the `lux` key source, and the cores they already run. A multi-owner installation adds a minter
and, for anything beyond owners acting on their own objects, an
authorizer, and an installation that keeps its own budgets adds a key
endpoint; each is an endpoint the operator writes against the public
package `authorizer`, and `docs/` holds a minimal one of each.

`deploy/base` holds a Deployment of two replicas, its Service, the
account it runs as, a disruption budget, a network policy, and the
migration job. `deploy/examples/generic` is an overlay that renders with
`kubectl kustomize`: the settings as a ConfigMap, the template of the
Secrets that hold the token files, and a pooler. Every host is under
`example.com`. The container runs as a user that is not root, with a
read-only root file system, no capability, the runtime's default seccomp
profile and no token of the cluster's API. Egress is allowed to the
resolver, the pooler and the database, the issuer, the authorizer, the
minter, the key endpoint, each configured core, the event sink and the
telemetry collector, as the overlay names them, and to no host a task
or a candidate names: an API under evaluation is called from a trial's
sandbox (spec 005).

`make run` builds `examend`, runs `migrate` and then `serve` against a
local Postgres in the developer's container runtime, with the owner
policy, and, from the release that reads token files, with
`EXAMEN_SOLE_OWNER` and the token files of the developer's own
environment.

### The v0.1 profile

An installation with no owner token source (neither
`EXAMEN_MINTER_URL` nor `EXAMEN_SOLE_OWNER`), no key source
(`EXAMEN_KEYS_URL` unset, and no `lux` key source, which comes after
v0.1) and no file store (`EXAMEN_ARCA_URL` unset) serves the v0.1
profile, and the first release serves it alone: the versioned objects,
experiments whose candidates are all of kind `external` in modes
`execute` and `import`, the lease, renewal and submit of their trials,
the import of past trials, self-reported values, the grid, comparisons
and the progress stream (specs 003 and 005). It needs Postgres and an
issuer, and an authorizer only for what the owner policy does not
answer. No request leaves examend for a core, since nothing it serves
acts as the owner:

- an experiment's create resolves only the pins a served path uses, so
  it resolves none at a core, asks no minter and refuses a candidate,
  grader or task that would need a core `422 core_not_configured`;
- a Budget an authorizer assigns as `limits.budget` is recorded with no
  request to Lux, and without one a spending cap is recorded with no
  Budget made for it;
- a cell's cost check leases no Key, since its estimate is zero;
- `EXAMEN_LUX_URL` and `EXAMEN_ARCA_URL` are needed only by the work
  that uses them, none of which the profile serves: every trial's cost
  is `unknown`, and an external trial's outputs are taken only as
  `arca` references with their digests, recorded and not read;
- sink events are journaled and not sent, and `EXAMEN_EVENTS_URL` set
  stops the start of a release that does not deliver them (Refusals at
  start).

Where this differs from the general text of specs 003 and 005: an
experiment's create resolves no pin as the owner and asks no minter;
`EXAMEN_LUX_URL` unset no longer stops every create, and
`EXAMEN_KEYS_URL` unset no longer means the `lux` key source; an
`external` candidate needs no Arca; and a submitted output is recorded
unread, where the general text writes or reads it in the owner's Arca
space. Spec 003's section of the same name lists every difference of
the API.

### Limits

The constants of spec 002 this spec holds: `SHUTDOWN_GRACE` and
`IMAGE_MAX_SIZE`.

## Not in this spec

A `check` command that reaches every dependency once and prints a line
per requirement. A command-line client and client libraries. A
reference console, which is a client of the API and ships apart from
`examend` (spec 010). A Helm chart. Manifests of a particular installation, which live with it. The
work queue and its leases (spec 005). Retention of objects and the event
log (spec 007). The text of `SECURITY.md` (spec 012).

## Acceptance criteria

- `make check` passes on a fresh clone with the toolchain and a
  container runtime, and every package clears the coverage floor.
- `examend` accepts exactly `serve`, `migrate` and `version`; any other
  command or none exits 2 with the usage.
- The configuration table lists every variable the binary reads and
  none it does not, and no package but `internal/config` reads an
  `EXAMEN_*` variable.
- A missing required variable and a value that does not parse each stop
  the start with the variable's name and never its value.
- Each case of the table of refusals stops `serve` before it listens.
- `migrate` against an empty database applies every migration and exits
  0; a second run applies none and exits 0; two runs at once apply each
  migration once.
- `serve` refuses a schema that lacks a migration it carries, a dirty
  schema and a schema of another major, and warns and serves on a newer
  schema of its own major.
- The previous minor's binary passes its own e2e tier against a
  database the new binary migrated.
- The store suite passes on Postgres directly and through a pooler in
  transaction mode.
- `/livez` answers `200` while the process runs; `/readyz` answers `503`
  naming `database` when the database is lost and naming `draining`
  after the stop signal, and answers `200` while the authorizer, the
  minter, the key endpoint and every core are down; `/version` reports what `examend
  version` prints.
- On the stop signal a replica fails readiness, closes its streams,
  completes an open request within `SHUTDOWN_GRACE`, and exits 0.
- A rolling restart of every replica during an experiment starts each
  trial once, loses none, and the experiment completes with the trial
  count it declared.
- The image runs as a user that is not root, holds no shell, is at most
  `IMAGE_MAX_SIZE`, and starts and answers with a read-only root file
  system and no capability, on both platforms.
- A tag without a changelog section is refused by the pre-push hook and
  by the release workflow.
- With Postgres, the stub issuer and the owner policy, and no owner
  token source, key source, file store or core, an experiment of
  `external` candidates runs from create through its leases, submits
  and a past trial's import to its final comparisons, and no request
  leaves for any core.
- After v0.1, with Postgres, the stub issuer, the stub cores,
  `EXAMEN_SOLE_OWNER` with token files and no authorizer, an experiment
  runs from create to its comparison, and no request leaves for an
  address the configuration does not name.
- No log line, span or metric label carries a token, a Key's value, a
  link token, an Arca link's token or a task's instruction, by the
  canary of spec 004, one planted as a publication's link token and
  one planted as a `prepare` job's input link.
- Without `EXAMEN_FILESTORE_DOWNLOAD_ONLY` set to `true`, a
  publication version at `outputs` or `full` is refused before
  anything is copied.
- No file of the repository names an installation's hostname, account
  or credential, by the gate's identity scan.

## Outcome

In progress. v0.1.0 ships the distribution of the v0.1 profile: the
binary's three commands, every `EXAMEN_*` variable read once in
`internal/config` and held to the table above by a test, the refusals
at start, forward migrations under a session lock and the schema
check, serving through a pooler in transaction mode, the probes, the
drain on the stop signal, the image, and the changelog check before a
tag. v0.2.0 adds a live smoke test against a deployed server.

Every row below shipped in v0.1.0.

| # | Verdict | Held by |
|---|---|---|
| 1 | Met: the gate passes in CI, with `internal/store/postgres/pgtest` exempt from the coverage floor for the reason `.lateregate.yaml` gives | CI `verify`, job `gate` |
| 2 | Met | `main.TestOneCommandAndNoFlag`, `main.TestVersionIsStamped` |
| 3 | Met; the e2e tiers' test code reads `EXAMEN_E2E_*` variables, which the table does not list | `config.TestTableMatchesWhatIsRead`, `examen_test.TestOnlyConfigReadsTheEnvironment` |
| 4 | Met | `config.TestErrorsNameTheVariableOnly`, `main.TestMigrateCommand` |
| 5 | Met | `config.TestRefusals`, `main.TestServeRefuses`, `postgres.TestTheSchemaCheck` |
| 6 | Met | `postgres.TestMigrate`, `main.TestMigrateCommand` |
| 7 | Met | `postgres.TestTheSchemaCheck`, `main.TestServeRefuses`, `main.TestServeANewerSchema` |
| 9 | Partly met: idempotency, one external flow and `serve`'s start run through a pooler in transaction mode; the rest of the store suite runs on the direct connection alone | `postgres.TestIdempotency`, `httpapi.TestTheExternalFlowThroughAPooler` |
| 10 | Partly met: `/livez`, `/readyz` naming `database` or `draining`, `200` with the authorizer and an issuer down, and `/version`; `draining` after a real stop signal is untested, and the minter, the key endpoint and the cores have no client yet | `httpapi.TestProbes`, `main.TestServe`, `postgres.TestReady` |
| 11 | Partly met: readiness fails while draining, and the process exits 0; open streams are not closed at the signal and hold the stop for `SHUTDOWN_GRACE`, and an open request finishing within it is untested | `main.TestServe`, `httpapi.TestProbes` |
| 13 | Partly met: the image holds no shell, is under 40 MiB, and migrates and serves with a read-only root file system and no capability; its non-root user is set and not checked, and only the runner's platform is run | CI `verify`, job `image` |
| 14 | Met, by `lateregate` in both places | `.githooks/pre-push`; CI `release`, job `build` |
| 15 | Partly met: in process, with the owner policy and a test authenticator, an `execute` experiment runs through leases and submits to completion and an `import` experiment to computed final comparisons; the binary's run with the stub issuer is skipped in CI, no test records the absence of requests to a core, and one experiment cannot both lease and import, since an import into an `execute` experiment is `409 wrong_mode` | `httpapi.TestRetriesBlockAndResume`, `httpapi.TestAnalyzedImport`, `goalless_test.TestGoallessStudy` |
| 17 | Partly met: bearers, the authorizer's token and the configuration's secrets stay out of every log line and error; spans are not captured, no metric is emitted, and Keys, link tokens and `prepare` jobs do not exist yet | `main.TestServe`, `httpapi.TestRoutesNeedABearer`, `config.TestSecretsDoNotPrint` |
| 19 | Met; the identity scan reads code, documents and manifests, and leaves `specs/` out by design | CI `verify`, job `gate` |

Not built: 8, the previous minor's e2e tier against a migrated
database; 12, a rolling restart; 16, the profile after v0.1 with token
files and stub cores; 18, publications.

The release differs from the design above in a few places: with
`EXAMEN_KEYS_URL` unset the key source reads `lux` rather than none;
the start log does not say whether `serve` serves the v0.1 profile; no
metric of the telemetry table is emitted; and the layout's `docs/`,
`deploy/` and `test/stubs/`, and every binary but `examend`, do not
exist.
