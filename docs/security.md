# Security

For an operator deciding how to run Examen and a builder deciding what
to send it: what Examen guarantees, what it sends to each service and
what it keeps, what it does not defend, and what an operator keeps safe.
Reporting a vulnerability, and the tests that hold each guarantee, are
in [SECURITY.md](../SECURITY.md).

## What Examen guarantees

**Every route but a few takes a token.** A token must come from an
issuer in `EXAMEN_OIDC_ISSUERS`, for an audience in
`EXAMEN_OIDC_AUDIENCE`, expired by no more than 30 seconds and signed;
a refusal names the reason and never repeats the token. The routes that
take none are the probes, `<base path>/version`, the contract at
`<base path>/openapi.yaml`, the `404` of a path Examen does not serve,
and the `413` of a body above the bound, answered before the token is
read.

**Another owner's object is not there.** A request the decision
denies, on any route that names a stored object, is answered exactly as
one for an object that does not exist, and changes nothing. With no
authorizer configured, the owner policy decides, and an owner acts on
their own objects alone.

**No decision is a refusal.** A request the authorizer gives no
well-formed answer for is refused `503 authorizer_unavailable` and makes
nothing, unattended work waits, and `examend` does not start against an
authorizer that allows the reserved probe question.

**Trials act as their owner.** Every session, check sandbox and file a
`topos` trial reaches is reached with a token minted for the experiment's
owner, and a model judge calls Lux with a Key
leased for its one model. A check sandbox is asked for with no network.

**A grade is never overwritten.** Postgres refuses to change a
measurement or a version, or to delete a measurement while its trial and
experiment exist, and each grading ends once. A kept file whose bytes
are not the ones recorded when the trial ended is not graded.

**A result says what was measured.** A value a harness reports about
itself gets no verdict, a comparison asked for after the fact is
exploratory, and the analysis gives the same bytes from the same inputs.

## What Examen sends, and to whom

Each service gets only what its part of the work needs.

| Service | Credential Examen sends | What it sends |
|---|---|---|
| The issuers | none | requests for the discovery document and the key set |
| The authorizer | `EXAMEN_AUTHORIZER_TOKEN_FILE` | the question: the subject, every claim of the caller's token as the issuer signed it, the action, the object's kind, id and the fields [Writing an authorizer](authorizer.md#the-question) lists, the request id, the client address and the user agent; on a create, the `sign_in` handle |
| The minter | `EXAMEN_MINTER_TOKEN_FILE` | the owner, the subject, the audience, the experiment, the purpose and a lifetime of 15 minutes |
| Topos | an owner token for Topos | for each trial a session: the agent and version, the task's instruction as its first message, a title with the candidate's and task's names and the run number, the rollout, the model, the limits, ids of the experiment, cell and trial, and the task's Origo repository and commit |
| Cella | an owner token for Cella | a sandbox with no network egress; the run's kept files, the session log, the final message and the check's own files; the check's command and environment |
| Lux | a Key for the judge's one model | the judge's prompt, rubric and metrics, and the evidence it names: the final message, the session log, the check values and the kept files it reads, cut to its input bound |
| Arca | an owner token for Arca | reads of the check files and of the run's kept files; writes of each check's standard output and error |
| The key endpoint | `EXAMEN_KEYS_TOKEN_FILE` | requests for a Key, a Budget, a release and usage records, naming the owner, the subject, the experiment, the purpose, the models, the Budget's name, the Key's labels (the experiment's, trial's or grader's ids) and a lifetime |
| An external harness | none: the harness calls Examen with its own token | in a lease: the trial's id, candidate, task name and digest, the task's instruction, the outputs it must return, its limits, and the experiment's model Key when a key source is configured |
| A telemetry backend | `OTEL_EXPORTER_OTLP_HEADERS` | with `OTEL_EXPORTER_OTLP_ENDPOINT` set: spans of requests and outbound calls, logs and metrics, below |

What no service receives: the caller's bearer token, which Examen keeps
nowhere; a Key, except by the harness that leased it and the judge it
was leased for; an owner token, except by the service it was minted
for; and a task's references, which this release keeps and sends to
nobody, checks included.

The authorizer sees every claim of every caller's token, which may
include an email address or other personal data, with the caller's
address and user agent. It never sees a request body whole, only the
fields listed there, nor an instruction, evidence or a Key.

## What Examen keeps

Everything is in Postgres, through `EXAMEN_DATABASE_URL`.

| Kept | For how long |
|---|---|
| every version of a metric, grader, task, suite and candidate, with its instructions, check commands and environments, and judge prompts and rubrics | for good: a version never changes |
| experiments, cells and trials: owners, states, outcomes, failures, what each trial used and cost, and references to its evidence in Topos and Arca | for good |
| measurements, with a judge's or a harness's rationale of up to 4 KiB | for good: a measurement never changes |
| gradings: a check's exit code, the size and digest of its output and the last 2 KiB of its error; a judgment's request digest, attempts, model, tokens and cost | for good |
| an experiment's events, comparisons, baselines and taxonomies | for good |
| the usage records the key endpoint returns for an experiment's Key, and the IDs of its Keys, never their values | for good |
| the answer to a request sent with an `Idempotency-Key`, without the model Key of a lease | 24 hours, then removed within 10 minutes |

Examen keeps no token, no Key value, no session log, no final message,
no kept file and no `sign_in` handle. The session log and kept files
stay at Topos and in the owner's Arca space, and a check's output is
written to that space. Nothing is ever removed but the idempotency
answers and a provisional comparison's list of inputs, rewritten on
every recompute: no route deletes an object, and no retention applies. An
operator who deletes an experiment's row in Postgres removes its cells,
trials, measurements, events and comparisons with it.

## What it logs

One JSON line per event on standard error, at `EXAMEN_LOG_LEVEL`. There
is no log line per request. A start logs which services are configured
and how decisions are made; an error logs its route's pattern, never
its path, with URL queries and user info cut; background work logs ids
of experiments, cells, trials, sessions, sandboxes and Keys, never a
Key's value, and why a judge's answer was malformed, which can name
what the model wrote. Some errors are logged as their library wrote them and can
name the host and path of an issuer, the authorizer or a service.

With `OTEL_EXPORTER_OTLP_ENDPOINT` set, request spans carry the path
with object ids and names, the user agent, and a client address taken
from the first `X-Forwarded-For` entry as sent, whatever
`EXAMEN_TRUSTED_PROXIES` says. Spans of outbound calls carry each URL
with its query, such as an owner's file path at Arca. The probes are
not traced.

## For a builder

- A lease gives an external harness the task's name, digest,
  instruction, outputs and limits, and no reference, check, rubric or
  other candidate.
- A value your harness reports about its own trials is marked
  `self_reported` and never gets a verdict. Grading of an external or
  imported trial is not built: its outputs are recorded unread.
- A model judge reads the evidence as content, inside a boundary, and
  only an answer in its one tool counts. Text in the evidence can still
  lean a judge within the ranges it may give; read a judge's rationale
  before trusting a surprising score.
- A task's instruction becomes the first message of a session at Topos,
  and a grader's check command runs over your agent's files in a Cella
  sandbox, both as your owner identity.
- What you send is kept for good, and the experiment's model Key, when
  your harness holds one, can spend the experiment's Budget on any model
  it allows. Errors to branch on and the bounds a request is held to are
  in [Using the API](api.md#errors).

## What Examen does not defend

[SECURITY.md](../SECURITY.md#what-is-not-defended) is the full list.
The limits an operator plans around:

- **An issuer over plain `http`.** Unlike the services Examen calls,
  `EXAMEN_OIDC_ISSUERS` is not held to `https` or loopback. Name issuers
  over `https`.
- **A permission withdrawn moments ago.** An allow is reused for its
  `ttl`, and a repeated idempotent request within 24 hours replays its
  answer, a lease's Key included, without a new question.
- **What a deny says.** The authorizer's reason for a `403` reaches the
  caller.
- **Load beyond one replica.** Rate and stream limits count per replica,
  after authentication, and nothing caps how much Postgres holds.
- **The database's owner.** The role `examend migrate` ran as can drop
  the triggers that keep measurements unchanged, and a role that can
  delete an experiment's row removes its measurements with it.
- **The services you configure,** and a compromised host. Examen
  believes Topos, Cella, Lux, Arca, the minter, the key endpoint, the
  authorizer and the issuers.

## For an operator

Each `*_FILE` secret is a file, read once at start. The database URLs
and the telemetry headers are variables, so keep them in a secret too.

| Secret | What its holder can do |
|---|---|
| `EXAMEN_MINTER_TOKEN_FILE` | ask the minter for an owner token for any owner, subject and service the minter lets `examend` ask for, for as long as the minter grants (`examend` asks for 15 minutes): act as those owners at Topos, Cella and Arca. The most powerful secret. |
| `EXAMEN_KEYS_TOKEN_FILE` | lease, release and read the usage of Keys for any experiment, spending its Budget |
| `EXAMEN_AUTHORIZER_TOKEN_FILE` | ask your authorizer any question as `examend`, and learn what a subject may do |
| `EXAMEN_DATABASE_URL`, `EXAMEN_DATABASE_POOL_URL` | read and change everything Examen keeps; the password is in the URL |
| `EXAMEN_EVENTS_SECRET_FILE` | sign event deliveries; delivery is not built, so it signs nothing yet |
| `OTEL_EXPORTER_OTLP_HEADERS`, when it carries a token | send telemetry as your installation |

Beside the secrets:

- Name every issuer over `https`, and keep `EXAMEN_OIDC_AUDIENCE` to the
  audiences you mean: a token for any of them is accepted.
- Keep `EXAMEN_TRUSTED_PROXIES` to the proxies in front of `examend`;
  any peer in it can name the address the authorizer is told.
- Without `EXAMEN_AUTHORIZER_URL` the owner policy decides: owners act
  on their own objects alone, with no organizations, shares or limits
  beyond Examen's own.
- Put a proxy with read and write timeouts in front of `examend`, which
  sets none past the 10 seconds it allows for the headers.
- [Installing examend](install.md#what-a-production-installation-must-have)
  lists the rest of what a production installation must have.
