# Running it locally

`examend` on your own machine, built from source, with a local Postgres
and a local OpenID Connect issuer. You define a task, a metric and two
candidates, run an experiment of ten trials each, play the external
harness that runs them, and read the comparison Examen computes. Nothing
here calls a model or needs an account anywhere.

`examend` has no development mode and no static token. Every request
needs a token from an OpenID Connect issuer, so a local run needs one
too. This page uses [Dex](https://dexidp.io/) in a container. Any issuer
works whose discovery document `examend` can read and whose tokens name
the audience `examen`.

The blocks run in order in one shell, from the root of a clone.

## What you need

Go 1.27 or newer, `git`, `curl`, `jq`, and Podman or Docker. The
commands say `podman`; `docker` takes the same arguments.

## Build

```sh
make build
./out/examend version
```

## A database

`make db` starts Postgres in a container named `examen-postgres` on
`127.0.0.1:55432` and waits until it answers. `examend migrate` applies
the schema; `serve` never does.

```sh
make db
export EXAMEN_DATABASE_URL='postgres://examen:examen@127.0.0.1:55432/examen?sslmode=disable'
./out/examend migrate
```

## An issuer

Dex with one user, `alice@example.com` with the password `password`,
and one client whose id is `examen`. Dex addresses its ID tokens to the
client that asked, so a token for the client `examen` carries the
audience `examend` accepts by default.

```sh
mkdir -p local
cat > local/dex.yaml <<'EOF'
issuer: http://127.0.0.1:5556/dex
storage:
  type: memory
web:
  http: 0.0.0.0:5556
oauth2:
  passwordConnector: local
enablePasswordDB: true
staticPasswords:
  - email: alice@example.com
    # bcrypt of "password"
    hash: "$2a$10$2b2cU8CPhOTaGrs1HRQuAueS7JTT5ZHsHSzYiFPm1leZck7Mc8T4W"
    username: alice
    userID: alice
staticClients:
  - id: examen
    name: Examen
    secret: local-client-secret
    redirectURIs: ["http://127.0.0.1:5555/callback"]
EOF

podman run -d --name examen-dex -p 127.0.0.1:5556:5556 \
  -v "$PWD/local/dex.yaml:/etc/dex/config.yaml:ro" \
  ghcr.io/dexidp/dex:v2.43.1 dex serve /etc/dex/config.yaml
```

The password grant gives a token without a browser:

```sh
until curl -fs http://127.0.0.1:5556/dex/.well-known/openid-configuration > /dev/null; do sleep 1; done
TOKEN=$(curl -s http://127.0.0.1:5556/dex/token -u examen:local-client-secret \
  -d grant_type=password -d username=alice@example.com -d password=password \
  -d scope=openid | jq -r .id_token)
```

The caller a token names is the issuer and the token's `sub` joined by
a pipe, `http://127.0.0.1:5556/dex|<sub>`, and that string is the owner
of everything the token creates. Run the last command again for a fresh
token once this one expires.

## Start examend

```sh
EXAMEN_OIDC_ISSUERS=http://127.0.0.1:5556/dex \
  ./out/examend serve 2> local/examend.log &

until curl -fs http://localhost:8080/readyz > /dev/null; do sleep 1; done
EXAMEN=http://localhost:8080/v1
api() { curl -s -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' "$@"; }
```

The log, one JSON object per line, says what this installation can do:

```sh
jq -r .msg local/examend.log
```

```text
token source: none
key source: none
decides: owner policy
cores: none
listening
```

With no authorizer configured, the owner policy decides: each caller
reaches the objects it owns and nothing else. With no other service
configured, Examen runs experiments whose trials an external harness
runs, which is what the rest of this page does. With no key endpoint,
`key source: none`, no model Key is issued here.

`make run` builds, starts the database, migrates and serves in one
step, and reads `EXAMEN_OIDC_ISSUERS` and every other variable from
your environment.

## Say what to measure

A metric is what a trial is scored on. This one is binary, a rate
across trials, and higher is better:

```sh
METRIC=$(api -X PUT $EXAMEN/metrics/passed -d '{
  "type": "binary", "aggregate": "rate", "direction": "higher_is_better",
  "description": "Whether the reply was correct, as the harness checked it."}' | jq -r .digest)
```

A task is what a system under test is asked to do, and a suite is the
tasks and metrics an experiment runs, with the metric its headline
result reads. `builtin:completed` and `builtin:duration` are Examen's
own measurements of every trial.

```sh
TASK=$(api -X PUT $EXAMEN/tasks/capital-of-france -d '{
  "instruction": "What is the capital of France? Answer with the city name alone.",
  "timeout": "5m"}' | jq -r .digest)

SUITE=$(api -X PUT $EXAMEN/suites/geography -d "{
  \"tasks\": [\"$TASK\"],
  \"metrics\": [\"$METRIC\", \"builtin:completed\", \"builtin:duration\"],
  \"primary_metric\": \"$METRIC\"}" | jq -r .digest)
```

Every object is applied by name with `PUT` and identified by the digest
of its content. Applying the same content again makes no new version.
One object refers to another by digest, `sha256:...` or
`name@sha256:...`, never by a bare name, so an experiment always says
exactly what it ran. A bare name is refused `400 unpinned_reference`.

## Two candidates

A candidate is one fully pinned system under test. Candidates of kind
`external` are configurations of your own harness, declared by it: here,
one harness at one version with two prompt styles.

```sh
A=$(api -X PUT $EXAMEN/candidates/my-agent-v1 -d '{"kind": "external",
  "external": {"harness": "my-harness", "version": "1.0.0", "flags": {"prompt": "terse"}}}' | jq -r .digest)
B=$(api -X PUT $EXAMEN/candidates/my-agent-v2 -d '{"kind": "external",
  "external": {"harness": "my-harness", "version": "1.0.0", "flags": {"prompt": "step-by-step"}}}' | jq -r .digest)
```

## Run an experiment

Ten trials of each candidate on every task of the suite. The first
candidate is the reference the others are compared with. An experiment
names the Budget its model calls are metered against; nothing here calls
a model, so the name is only recorded.

```sh
EXP=$(api -X POST $EXAMEN/experiments -d "{
  \"suite\": \"$SUITE\", \"candidates\": [\"$A\", \"$B\"], \"trials\": 10,
  \"budget\": {\"name\": \"local\"}}" | jq -r .id)
api $EXAMEN/experiments/$EXP | jq '{state, mode, scheduling, progress}'
```

```json
{
  "state": "running",
  "mode": "execute",
  "scheduling": "task_by_task",
  "progress": { "cells": { "smoking": 2 }, "trials": { "pending": 2 }, "outcomes": {} }
}
```

Each combination of task and candidate is a cell. A cell first deals one
smoke trial, and only once it ends are the other nine dealt, so a
broken setup fails one trial instead of ten. Both cells are smoking: two
trials wait for a harness.

## Be the harness

An external harness leases trials, runs each one, and submits what it
produced. A lease answers `204` while there is nothing to deal yet, and
`409 experiment_finished` once the experiment is complete. A lease
expires unless the harness submits or renews it. This stand-in "runs"
each trial by deciding the answer: `my-agent-v1` is right on every
other trial and `my-agent-v2` on all but one.

```sh
while [ "$(api $EXAMEN/experiments/$EXP | jq -r .state)" != complete ]; do
  for C in $A $B; do
    LEASE=$(api -X POST $EXAMEN/experiments/$EXP/work/lease -d "{\"candidate\": \"$C\"}")
    ID=$(echo "$LEASE" | jq -r '.lease // empty')
    [ -n "$ID" ] || continue  # 204: nothing to lease now
    echo "$LEASE" | jq -c '.trials[] | [.id, .index]' | while read -r t; do
      TRIAL=$(echo "$t" | jq -r '.[0]'); INDEX=$(echo "$t" | jq -r '.[1]')
      if [ "$C" = "$A" ]; then PASSED=$([ $((INDEX % 2)) -eq 0 ] && echo true || echo false)
      else PASSED=$([ "$INDEX" -ne 3 ] && echo true || echo false); fi
      NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
      api -X POST $EXAMEN/trials/$TRIAL/submit -d "{
        \"lease\": \"$ID\", \"outcome\": \"completed\",
        \"started_at\": \"$NOW\", \"agent_ended_at\": \"$NOW\",
        \"measurements\": [{\"metric\": \"$METRIC\", \"value\": $PASSED}]}" > /dev/null
    done
  done
  sleep 1
done
```

Each leased trial carries what a harness needs to run it: the task's
instruction, inputs and timeout, and the candidate's declared
configuration. A real harness runs its system on the instruction and
checks the reply; the loop above stands in for that.

## Read the results

The grid holds each cell's summary of the primary metric, with its 95%
interval (Wilson's, for a rate):

```sh
api $EXAMEN/experiments/$EXP/grid | jq '.items[].cells[] | {state, summary: (.summary | {n, value, interval})}'
```

```json
{ "state": "complete", "summary": { "n": 10, "value": 0.5, "interval": [0.2366, 0.7634] } }
{ "state": "complete", "summary": { "n": 10, "value": 0.9, "interval": [0.5958, 0.9821] } }
```

Every experiment gets a comparison of each candidate with the reference,
recomputed as trials arrive and final shortly after the experiment
completes:

```sh
until [ "$(api $EXAMEN/experiments/$EXP/comparisons | jq -r '.items[0].final')" = true ]; do sleep 1; done
api $EXAMEN/experiments/$EXP/comparisons | jq '.items[0].results[] | {metric_name, delta, p, q, verdict, reason}'
```

```json
{
  "metric_name": "passed",
  "delta": 0.4,
  "p": 0.1409,
  "q": null,
  "verdict": null,
  "reason": "p = 0.14 on values the external harness reported itself: not a finding, and outside the corrected family"
}
{
  "metric_name": "completed",
  "delta": 0,
  "p": 1,
  "q": 1,
  "verdict": "within_noise",
  "reason": "no difference between the two sides"
}
```

Two things to read here:

- **Ten trials do not settle 50% against 90%.** Boschloo's exact test
  gives `p = 0.14`: a difference this large arises by chance often
  enough that it would not count as a finding even if Examen had
  measured it.
- **A value the harness reports about itself is never a finding.** The
  `passed` result carries the `self_reported` confound, no `q` and no
  verdict, and stays outside the family of results corrected together.
  Examen's own measurements, such as `completed` and `duration`, and the
  grades its own checks and judges give a trial they ran, are the ones
  that get a verdict. [Concepts](concepts.md) explains both rules.

## What this installation refuses

An experiment that needs a service this installation does not have is
refused when it is created, naming the service, and never left waiting:

```sh
T=$(api -X PUT $EXAMEN/candidates/an-agent -d '{"kind": "topos",
  "topos": {"agent": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
  "model": "claude-haiku-5-5"}}' | jq -r .digest)
api -X POST $EXAMEN/experiments -d "{\"suite\": \"$SUITE\", \"candidates\": [\"$T\"],
  \"trials\": 5, \"budget\": {\"name\": \"local\"}}" | jq .error
```

```json
{
  "code": "core_not_configured",
  "message": "This installation is not set up to run this kind of work.",
  "details": {
    "core": "topos",
    "detail": "a candidate of kind topos runs as a Topos session, which this installation does not start: it names no Topos address or no minter",
    "field": "candidates[0]",
    "retryable": false
  }
}
```

[Configuration](configuration.md#what-each-service-adds) lists what
each service adds.

## Clean up

```sh
kill %1
podman rm -f examen-dex examen-postgres
rm -r local
```
