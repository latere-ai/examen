# Installing examend

For an operator who runs `examend` for other people: what it needs, how
to get the image and check it, how to start it, probe it and stop it,
how to upgrade it, and what a production installation must have. To run
it once on your own machine, see [Running it locally](local.md). Every
`EXAMEN_*` variable is in [Configuration](configuration.md).

## What an installation needs

- **Postgres 16 or newer.** Examen keeps everything in it.
- **An OpenID Connect issuer.** Every request carries a token from an
  issuer that `EXAMEN_OIDC_ISSUERS` lists. `examend` has no static token
  and no development mode.
- **An authorizer, optionally.** It decides who may do what for
  organizations, shared reads and services acting for others. Without
  one, the owner policy lets each caller act on their own objects alone.
  See [Writing an authorizer](authorizer.md).
- **The optional services.** Topos, Cella, Lux, Arca, Origo, a minter
  and a key endpoint each add something. See
  [what each service adds](configuration.md#what-each-service-adds).

## The image

Every release publishes `ghcr.io/latere-ai/examend:<tag>` for
linux/amd64 and linux/arm64. The same image runs `serve` and `migrate`;
`serve` is its default command. It holds the binary and the roots it
verifies certificates against, with no shell and no package manager. It
runs as the numeric user `65532:65532`, which is not root, and works
with a read-only root file system and every capability dropped: it
writes no file.

The tags are the release tags, such as `v0.8.6`, listed on the
[releases page](https://github.com/latere-ai/examen/releases). Pin the
tag, or better its digest, and set `TAG` from that page in the commands
below:

```sh
TAG=<the tag of the release you install>
podman pull "ghcr.io/latere-ai/examend:$TAG"
podman run --rm "ghcr.io/latere-ai/examend:$TAG" version
```

Verify a release before you run it. [`SECURITY.md`](../SECURITY.md#verifying-a-release)
shows how to check the image's signature and attestation and the
release's checksums.

## The database

Examen keeps everything in Postgres, 16 or newer. `examend migrate`
applies the schema over `EXAMEN_DATABASE_URL` under a session lock, so
two migrations never run at once, and prints the version it reached.
`serve` never migrates: run `migrate` before you start or roll `serve`.
At start, `serve` checks the schema. A schema behind the binary stops
the start; a newer one of the same major version is logged and served,
which is what a rollback within a major version looks like.

Behind a transaction-mode pooler, set `EXAMEN_DATABASE_POOL_URL` to the
pooler for `serve` and keep `EXAMEN_DATABASE_URL` direct: the migration
lock is held across statements, which a transaction-mode pooler loses
between two.

## Running it

Run `migrate` as a job, and when it exits 0 start `serve`. Both take
their configuration from the environment, which this example passes
through from the shell:

```sh
podman run --rm -e EXAMEN_DATABASE_URL \
  "ghcr.io/latere-ai/examend:$TAG" migrate
podman run -d -p 8080:8080 --read-only --cap-drop ALL \
  -e EXAMEN_DATABASE_URL -e EXAMEN_OIDC_ISSUERS \
  "ghcr.io/latere-ai/examend:$TAG" serve
```

`serve` listens on `EXAMEN_ADDR`, `:8080` by default, and serves the
API under `EXAMEN_BASE_PATH`, `/v1` by default. The probes stay at the
root of the listener whatever the base path is. If a variable does not
parse or a condition fails, `serve` exits with status 1 before it
listens and names the variable; see
[refusals at start](configuration.md#refusals-at-start). On a good
start it logs which services it found, then `listening`.

## Probes and shutdown

The probes sit at the root of the listener, outside the base path, and
take no token:

| Probe | Answers |
|---|---|
| `GET /livez` | `200` while the process runs |
| `GET /readyz` | `200` while the database answers; otherwise `503`, naming what is not ready, and also while the server drains |
| `GET /version` | the build's `version`, `commit` and `build_time` as JSON |

On `SIGTERM` the server fails readiness at once, stops taking
connections, and gives open requests 25 seconds to finish. Give the
container a termination grace period longer than that.

## Upgrading

1. Read the changelog section of every release between the running one
   and the target. Before v1.0.0 a minor release may break, and its
   section names what an operator must do, such as a setting that now
   stops the start or a migration the previous release cannot serve.
2. Back up the database with `pg_dump`. It is the only way back across a
   migration the previous release cannot serve.
3. Run `migrate` from the new image, as a job.
4. Roll the `serve` replicas to the new image.
5. Check `/readyz`, and that `/version` reports the new release.

During the rollout, replicas of both releases serve one database. A
migration is additive, or is preceded by a release that writes both
shapes, so the previous release serves the newer schema. Experiments in
flight continue: the work queue is in Postgres, and the new replicas take
each item as the old ones release it.

To roll back, deploy the previous image. Within a major version it logs a
warning about the newer schema and serves it; no migration is undone.
When the changelog names a migration the previous release cannot serve,
restore the database from the backup. That loses what was written since,
and trials started after the backup are then unknown to the database.

## What a production installation must have

- **`https` issuers.** `examend` reads each issuer's discovery document
  and keys over the address you give. It accepts an `http` issuer, so
  the check is yours: an issuer reached over plain HTTP lets anyone on
  the path substitute the keys that decide who a caller is.
- **An authorizer, or the owner policy on purpose.** Without
  `EXAMEN_AUTHORIZER_URL`, the owner policy decides and organizations are
  not available. Decide that on purpose, and see
  [Writing an authorizer](authorizer.md) for the rest.
- **The image's hardening kept.** Run it as the image's non-root user,
  with a read-only root file system and every capability dropped, as
  above. `examend` starts no other program and writes no file.
- **Secrets kept as secrets.** Each bearer and signing secret is a
  file: mount each one and name it with
  [`EXAMEN_AUTHORIZER_TOKEN_FILE`](configuration.md#every-variable),
  `EXAMEN_MINTER_TOKEN_FILE`, `EXAMEN_KEYS_TOKEN_FILE` or
  `EXAMEN_EVENTS_SECRET_FILE`. The database URL may hold a password, so
  keep it in a secret too. A start that fails names a variable and never
  its value.
- **TLS in front.** `examend` listens for plain HTTP and has no TLS
  settings. Put a TLS-terminating proxy or load balancer in front, and
  set `EXAMEN_TRUSTED_PROXIES` to its address range so the authorizer is
  told the caller's address and not the proxy's.
- **`https` service addresses.** The authorizer, minter, key endpoint,
  each service URL and the events URL must be `https` unless the host is
  loopback; `serve` refuses to start otherwise.
- **The network the services need.** The server opens connections to the
  database or its pooler, the issuer, the authorizer, the minter, the
  key endpoint, each configured service, and the telemetry collector
  when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, and needs a resolver. It
  needs no route to any host a task or a candidate names.
- **A backup before every upgrade,** as in [Upgrading](#upgrading).
