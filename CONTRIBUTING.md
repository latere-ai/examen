# Contributing

Thanks for looking. This file is for people and agents changing Examen.
Users read the [README](README.md) and [`docs/`](docs/README.md); the
design, and the reasoning behind each decision, lives in
[`specs/`](specs/README.md).

## Getting set up

You need Go 1.27 or newer, `git`, and a container runtime that
Testcontainers can reach, Podman or Docker. Then:

```sh
make        # the quality gate
make build  # out/examend
make run    # builds, migrates a local Postgres container, and serves
```

`make run` starts a Postgres container named `examen-postgres` on a
loopback port, applies the migrations to it with `examend migrate`, and
serves from it. Every other variable comes from your environment, so set
`EXAMEN_OIDC_ISSUERS` to the issuer whose tokens you will send first;
[Running it locally](docs/local.md) sets one up.
[Configuration](docs/configuration.md) lists every variable.

The gate starts Postgres, and PgBouncer in front of it, in containers for
the store's suite. Where no container runtime answers, that suite cannot
run, and the coverage floor fails for the packages only it reaches: a
skipped suite is never a green gate. Under Podman, where Testcontainers'
reaper container cannot start, the helper turns the reaper off itself. It
labels every container it starts, and each start first removes the ones a
killed test binary left behind.

Install the hooks once with `make hooks`. They run formatting and license
checks before a commit and the linter before a push, so you see a finding
before CI does.

## Sending a change

Work on a branch and open a pull request. Keep one logical change per
commit, stage the files explicitly, and write the subject as `scope:
what changed`, for whoever reads the log. The pipeline runs the gate on
every push to `main` and every pull request, except one that changes only
specs or markdown, which no build or test reads; `lateregate release`
runs the whole bar before every tag.

A feature starts as a spec with acceptance criteria that are testable
sentences; a design that lands without one is harder to review than one
that arrives with the reasoning attached. Small fixes need no spec.

A change to a behavior a spec describes updates that spec in the same
change: its implementation status, its status, and its Outcome once it
is complete. A change to the API, a configuration variable, or the
authorizer's vocabulary or limits also updates
[`api/openapi.yaml`](api/openapi.yaml) and [`docs/`](docs/README.md)
where it applies. A change someone running `examend` or calling its API
would notice gets a line under `## Unreleased` in
[`CHANGELOG.md`](CHANGELOG.md), written for them; a `v*` tag without a
section there is refused.

## The bar

`make` runs the whole gate (`go tool lateregate`): formatting, the linter,
modernization, known vulnerabilities, the suite under the race detector,
per-package coverage at 90% or more, the license notice, the identity and
database declarations, and the spec tree. `go tool lateregate list` names
the gates and `go tool lateregate <name>` runs one. `make specs` checks
the spec tree alone, and `make openapi` the tests that hold the router to
the OpenAPI document.

A bug fix carries a test that fails without it. A change that lowers a
threshold or adds a waiver records the reason in `.lateregate.yaml`, so the
exception is reviewable rather than invisible.

## The test tiers

`go test ./...` runs all three, and the gate runs them once with coverage
taken over every package.

| Tier | What it proves | Needs |
|---|---|---|
| unit | pure logic: configuration, identifiers and digests, the error table, the vocabulary, the owner policy | Go |
| integration | the store against a real Postgres, directly and through a pooler in transaction mode; the migrations and the schema check | a container runtime |
| end to end | the whole server in process: the probes, the contract, verification against a stub issuer, and the commands | a container runtime |

## Rules that are not style

**A measurement is the thing this service exists to get right.** A number
in a report traces to the trials and grades it came from, a comparison
states its confounds, and a claim that a figure is unaffected by
something is proven by a test.

**Examen executes nothing.** It starts no agent loop, sandbox or model
call on a trial's behalf, runs no task or check code, and sends no request
to a host a task or a candidate names. Every outbound client is built with
`otel.HTTPClient` or `otel.Transport`, so its calls join the caller's
trace.

**A credential never leaves.** No route, log line, span, error, event or
database row carries a token, a key or any part of one, and a value that
does not parse is reported by the name of its variable, never by the
value. Only `internal/config` reads the environment.

## Where things go

| Path | What lives there |
|---|---|
| `cmd/examend/` | the server and its commands, `serve`, `migrate` and `version`, with the end-to-end tier beside it |
| `api/` | the HTTP contract, `openapi.yaml`. Public |
| `authorizer/` | the action vocabulary and the limits of an allow. Public |
| `internal/access/` | token verification, the authorizer client and the owner policy |
| `internal/apierr/` | the error table of the API |
| `internal/httpapi/` | the routes, the probes, idempotency, paging and the progress stream |
| `internal/objects/` | every object of spec 002 as a Go value, with the rules that need no other object |
| `internal/work/` | the unattended work of an experiment: sessions, grading, usage and comparisons, run on a ticker |
| `internal/execute/` | the scheduling decisions of spec 005: smoke trials, rollouts, retries and parking, read from a cell and written by the store |
| `internal/grade/` | the check output contract and a model judge's input, tool and answer |
| `internal/calibrate/` | how well a judge agrees with people |
| `internal/analysis/` | the statistics of spec 008, on Examen's own deterministic math in `mathx/` |
| `internal/compare/` | the seam between what the store keeps and the analysis: a comparison's inputs and its stored record |
| `internal/topos/`, `internal/cella/`, `internal/arca/` | the clients of Topos, Cella and Arca, with a Topos stub in `topos/topostest/` |
| `internal/owner/` | owner tokens from the minter |
| `internal/keys/` | the key source: the key endpoint's client |
| `internal/usage/` | a trial's cost and the models that served it, from Lux's records |
| `internal/store/` | the store's contract, its Postgres adapter, the migrations and the test database |
| `internal/config/` | configuration, read once |
| `internal/id/` | identifiers and digests |
| `internal/limits/` | the limits of spec 002, each defined once |
| `internal/version/` | the build's version, stamped by the linker |
| `test/e2e/` | end-to-end tests against a built `examend`: importing a published study, and a smoke test of a deployed server |
| `docs/` | the documentation for people running and calling Examen |
| `specs/` | one spec per component, in build order in [`specs/README.md`](specs/README.md) |

The public packages, `api` and `authorizer`, change additively within a
major version of the module: a change keeps every existing call site
compiling, or the CHANGELOG names the break.

## Reporting a vulnerability

Do not open an issue. [`SECURITY.md`](SECURITY.md) says where to send it.
