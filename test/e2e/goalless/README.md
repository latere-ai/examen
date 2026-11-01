# The goalless study, imported end to end

This harness imports the
[goalless-agents study](https://github.com/changkun/goalless-agents), a
study of what coding agents build when nobody tells them what to build,
into a local `examend`, or in remote mode a deployed one, and checks
what Examen computes against what the study's own files say. It is the
first release's end-to-end test of external harnesses: every run of the study becomes a declared past trial, imported
with `POST /experiments/{id}/imports` and `executor: external`
([`specs/003-api.md`](../../../specs/003-api.md), Imported trials), and
every comparison and grid figure is the analysis's.

The study's data stays where it is. The harness reads a checkout of the
study at run time and copies nothing of it into this repository.

## Running it

```sh
GOALLESS_DIR=/path/to/goalless-agents \
GOALLESS_ORACLE=/path/to/oracle.json \
go test ./test/e2e/goalless -run TestGoallessStudy -v -timeout 40m
```

Without `GOALLESS_DIR` the test is skipped; the converter's own rules
(`TestConverterRules`) run on run directories the test writes itself.
`GOALLESS_ORACLE` is optional (below). The harness needs a container
runtime for Postgres, as the rest of the suite does, builds `examend`,
runs `examend migrate` and `examend serve` against a database of its own,
with the built-in owner policy and a test issuer, and stops the server
when it ends. It calls no model. A later step that runs judges over the
study uses `claude-haiku-5-5` for every model call a test makes.

A full run imports every run of the study and waits for each experiment
to complete and for the server's work loop to compute every automatic
comparison a last time, some minutes in all.

## Remote mode: importing into a deployed examend

With `EXAMEN_E2E_URL` set to a server's base URL, its base path
included, the same import runs against that server instead of a local
one, and stores the study there:

```sh
GOALLESS_DIR=/path/to/goalless-agents \
EXAMEN_E2E_URL=https://api.example.com/v1/evals \
EXAMEN_E2E_TOKEN_FILE=/path/to/bearer \
go test ./test/e2e/goalless -run TestGoallessStudy -v -count=1 -timeout 3h
```

The bearer is `EXAMEN_E2E_TOKEN`, or the file `EXAMEN_E2E_TOKEN_FILE`
names, read again before every request, so a short-lived token another
process renews is never stale. The test still reads the study from
`GOALLESS_DIR` and sends only what the converter computed from it.

A run can be repeated: it makes nothing twice.

- Metrics, tasks, the suite and the candidates are applied under their
  stable names; an apply of content already applied makes no new
  version.
- Each experiment carries `goalless.definition` in its metadata, a
  digest of its suite, candidates, N and every import it holds. A run
  finds an experiment of the same definition that is not canceled and
  uses it; a study or converter that changed makes new experiments
  beside the old ones.
- A run imports only the runs an experiment does not hold yet, by their
  `ref`. Creates and imports carry an `Idempotency-Key`, so one whose
  answer was lost is answered again rather than made twice.
- After each experiment's intake is closed, the run waits for it to
  complete before making the next, so the import holds one active
  experiment at a time. A create refused because the owner already holds
  as many active experiments as allowed waits and is sent again.

A remote-mode create gives a spending cap of one cent in place of a
Budget's name, since an installation whose authorizer makes each
experiment's Budget accepts no name. An import calls no model, so
nothing is drawn on it.

The run ends with a report: the experiments created or found, the trials
imported and those already there, the cells held to their runs and, with
`GOALLESS_ORACLE`, to the study's figures, and the comparisons read back.

## The live smoke test

`test/e2e/live` checks a deployed examend from outside with the same
variables, and is skipped without them. It reads the probes and the
contract, refuses a missing and a bad bearer, and refuses an experiment
of a `topos` candidate: `422 executor_unavailable` from examend, or
`403 sign_in_not_kept` from an authorizer that keeps no sign-in for
unattended work. It then applies a metric, a task, a suite and two
`external` candidates named `smoke-<time>-<random>`, runs an experiment
of 5 trials each through the lease, renewal and submit an external
harness uses, lets one lease lapse, and holds the grid and the automatic
comparison to the values it submitted. It calls no model, leaves its
objects in place and its experiment complete, and takes a little longer
than `EXTERNAL_LEASE`, 10 minutes:

```sh
EXAMEN_E2E_URL=https://api.example.com/v1/evals EXAMEN_E2E_TOKEN_FILE=/path/to/bearer \
go test ./test/e2e/live -v -count=1 -timeout 40m
```

The probes sit at the root of examend's listener. When the API is served
under a base path of its own, the URL's origin may route its root to
another service, so the test checks the probes only at
`EXAMEN_E2E_PROBES_URL`, examend's own address, such as a port forward
to its service, and otherwise says they were not checked.

## Rehearsing against a local examend

`TestLocalTarget` serves a local examend as a hosted installation is set
up: under the base path `/v1/evals`, asking an authorizer that creates
an experiment only when every candidate is external and no check or
judge grades it, makes its Budget from the spending cap, and holds it to
at most 4 active experiments. It writes the variables of a remote-mode
run to the file `EXAMEN_E2E_SERVE` names and serves until that file is
removed:

```sh
EXAMEN_E2E_SERVE=$PWD/e2e.env go test ./test/e2e/goalless -run TestLocalTarget -v -timeout 4h &
# once e2e.env exists:
set -a; . ./e2e.env; set +a
go test ./test/e2e/live -v -count=1 -timeout 40m
GOALLESS_DIR=/path/to/goalless-agents go test ./test/e2e/goalless -run TestGoallessStudy -v -count=1 -timeout 3h
rm e2e.env
```

`TestRemoteMode` runs the remote path the same way on a study the test
writes itself, twice, and holds the second run to finding every
experiment and importing nothing.

## What a run directory becomes

The study keeps each run as `resultsN/<backend>/<model>/run-NN/`, or
`resultsN/<cell>/run-NN/` where an experiment groups by cell.

| The study's file | The import |
|---|---|
| `meta.md` `## Prompt` | the task whose instruction is the prompt file's text without trailing newlines; Experiment 1, which ran the first prompt with the sandbox's RTK tooling left enabled (the study disabled it from Experiment 2 on), is the task `prompt1-rtk` |
| `meta.md` `Timestamp`, `Duration` | `agent_ended_at` and `ended_at` are the timestamp, written when the run ended; `started_at` is the timestamp less the duration |
| `meta.md` `Exit Code`, `output.json` | the outcome, below |
| `meta.md` `Backend`, `Model`, `Fast mode`, `Codex max_output_tokens pin`; the cell directory's `-high`, `-low` or `-fast` | the candidate, below |
| `log.txt` `>>> Image:`, `>>> Endpoint:` | the candidate's `flags`, and the trial's `declared.image`; nothing else of the log is read, since it holds a key's prefix and local paths |
| `workspace/` | the values below, computed by the converter |
| the run's path | `ref` |

Experiments 1 and 2 are imported into one experiment, and so are 5 and
7, so the prompt comparisons they hold compare two tasks of one
candidate; the other study experiments are one experiment each, 21 in
all. Each experiment is in mode `import`, holds its candidates in the
order its runs first name them, with N the most runs one cell holds, and
its intake is closed once every run is in.

**Candidates.** One `external` candidate per declared configuration:
the harness (`goalless-claude-code` or `goalless-codex`), the CLI
version a pinned image tag names or `unrecorded`, the model in one
spelling (Anthropic's provider prefix dropped and a Claude version's
dots written as dashes), the reasoning effort where the cell directory
names it, and the image, the gateway, the fast-mode flag and the output
token pin as `flags`. Two configurations that differ in any of them are
two candidates.

**Outcomes.** Exit 0 is `completed`. Exit 137 is `infra_failed` with
`killed`, and any other exit `infra_failed` with `harness_exit`, the code
and the log's last line as detail. A Codex event stream whose turn
failed on a refused prefill after the output cap is `infra_failed` with
`gateway_output_cap`, and one whose turn failed on a gateway status
(403, 429, a 5xx, high demand) or after its reconnects ran out is
`infra_failed` with `gateway_error`, whatever the exit code. A turn that
failed on anything else, with exit 0, is `completed`.

**Values.** This release runs no check or judge, so the converter
computes what it can from the workspace and imports it as the harness's
own, stored `self_reported` and never a finding:

| Metric | Type | The rule |
|---|---|---|
| `loc` | count, median | newline bytes in files ending `.py .js .html .css .ts .sh .go .c`, skipping every `node_modules`, `__pycache__`, `.git`, `.pytest_cache`, `.venv`, `venv`, `vendor` and `target` directory; 0 is no value |
| `loc_nonblank` | count, median | the non-blank lines of the same files |
| `implemented` | binary | a counted file, a file of another source extension, or one that starts with `#!` |
| `browser_interactive` | binary | an `.html` file with a `<script` element and an event listener, an animation frame or an inline handler |

An `infra_failed` run carries no value. The built-in `completed` and
`duration` are Examen's own. The suite's primary metric is `completed`,
and the fifth prompt, which the study held from its seventh experiment
on, is the one task that weighs in suite-level figures.

Not imported in this release: outputs, transcripts and final messages,
which need a file store; the topics and media the study's tables name
per run, which are the figures a judge's labels are checked against, not
values of their own; and the token counts the runs recorded.

## What it checks

- Every run directory is imported or listed with why it was not
  (`unreadable_meta`, `unknown_prompt`), and the log shows, per study
  experiment, the directories read, the runs imported and those that
  ended `infra_failed`.
- Each cell of the grid holds exactly the runs mapped to it, with their
  outcomes.
- Each cell's stored summary of `loc` has the cell's finished runs as
  `n` and its runs with a value as its denominator, and its median and
  mean are those of the values the converter computed; its `implemented`
  rate counts the runs with a source file.
- No result that reads a self-reported value is a finding: it has no `q`
  and no verdict but `insufficient`, and a `p` it keeps comes with the
  `self_reported` confound and a reason that says so. Every comparison
  names both sides' provenance as declared.
- With `GOALLESS_ORACLE`, the study's own figures, below.

## The oracle

The oracle is the study's figures, transcribed from its `RESULTS.md`
tables by whoever runs the harness, and kept with the study, never in
this repository. It is a JSON file, here with made-up figures:

```json
{
  "run_directories": 700,
  "experiments": 21,
  "cells": [
    { "runs": "results9/claude/model-a", "finished": 5,
      "loc": [120, 80, null, 95, 101], "loc_mean": 99, "loc_median": 98 },
    { "runs": "results9/codex/model-a", "only": ["run-01", "run-04"],
      "finished": 0, "infra_failed": 2 }
  ]
}
```

A cell names the run directories under `runs`, or only those `only`
names, which must be one Examen cell. Each member is optional: `finished`
and `infra_failed` count outcomes; `loc` and `loc_nonblank` are per-run
values in run order, `null` for a run without one; `loc_values` are the
values in any order; `loc_valued`, `loc_min` and `loc_max` count and
bound them; `loc_mean`, `loc_median`, `loc_nonblank_mean` and
`loc_nonblank_median` are the stored summary's, rounded to whole lines;
and `implemented` counts the finished runs with a source file. A count
must be equal, not close.
