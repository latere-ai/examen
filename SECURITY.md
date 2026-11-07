# Security

Examen starts sessions of other people's agents in their owners' names,
reads text nobody reviewed (an agent's transcript and files, a task's
content, a harness's report), and holds the measurements a release
decision rests on. This file is what the project promises about that,
what it does not, and how to tell us when it is wrong.
[`docs/security.md`](docs/security.md) says the same for the people who
run it and build on it.

## Reporting a vulnerability

Report one privately, through
[GitHub's private vulnerability reporting](https://github.com/latere-ai/examen/security/advisories/new)
or by email to security@latere.ai. Do not open a public issue for it. The
[organization's security policy](https://github.com/latere-ai/.github/blob/main/SECURITY.md)
sets the response times, how disclosure works, and the safe harbor for
research done in good faith. Credit in the release notes on request.

Fixes ship in the next release of the current major line, as the
organization's policy on
[supported versions](https://github.com/latere-ai/.github/blob/main/SECURITY.md#supported-versions)
says; the [releases page](https://github.com/latere-ai/examen/releases)
lists them.

The reports we most want: a caller that reads or changes another
owner's object, or learns that it exists; a request that acts after a
deny, or while the authorizer does not answer; a bearer token, a model
Key, an owner token or a secret file's content in an answer, a log line
or a row; a trial that acts as anyone but its experiment's owner; a
measurement or a version that changes after it is written; and evidence
that changed after its trial ended and is graded all the same. What
[is not defended](#what-is-not-defended) is not a vulnerability, but a
report that one of those limits is wider than written here is.

## What is protected

Examen is attacked from five places: a caller with a token of its own,
an external harness that reports on its own trials, text written to sway
a model judge, a reader of what the server answers, logs and stores, and
someone who wants a result to say other than what was measured. Each
commitment below is held by the named tests, which run on every change.
A test is named by its package's directory, and `examen.` is the
repository's root. Tests marked (container)
run against Postgres in a container and fail, never skip, where no
container runtime answers. A number in a row is a bound fixed in the
code; [Fixed bounds](docs/api.md#fixed-bounds) lists the ones a client
meets.

| Against | What Examen commits to | Held by |
|---|---|---|
| a caller with no token, or a token Examen does not accept | a request to a route that takes a token is refused `401` when it carries no bearer, or one from an issuer not named in `EXAMEN_OIDC_ISSUERS`, for an audience not named in `EXAMEN_OIDC_AUDIENCE`, expired by more than 30 seconds, not valid until more than 30 seconds from now, or with a signature that does not check; the answer names the verifier's reason and never repeats the token; a progress stream closes when its bearer expires | `access.TestWhatTheVerifierRefuses`, `access.TestTheSkewOnExpAndNbf`, `access.TestVerifierConformance`, `httpapi.TestRoutesNeedABearer`, `httpapi.TestTheStreamCloses` (container) |
| a caller with a token of another owner | under the owner policy, which decides when no authorizer is configured, every route that names a stored object answers a request with another owner's token exactly as it answers one for an object that does not exist, status, headers and body, and changes nothing; an owner acts on their own objects alone, and a create or an apply that names another owner is refused `403` | `httpapi.TestAnotherOwnersTokenReachesNothing` (container), `access.TestOwnerPolicy`, `httpapi.TestReadsAcrossOwnersAndOutages` (container), `httpapi.TestARouteAsks` (container) |
| a caller when the authorizer cannot answer | a request for which the authorizer gives no well-formed decision, or an allow whose limits or filter Examen cannot read, is refused `503 authorizer_unavailable` and makes nothing; unattended work waits while the authorizer does not answer, and an experiment whose `experiment.run` is denied hands out no trial and stops; `examend` does not start when the configured authorizer allows the reserved probe question | `access.TestNoDecisionFailsClosed`, `access.TestTheLimitsOfAnAllow`, `access.TestFilters`, `access.TestNewAndTheProbe`, `httpapi.TestARouteAsks` (container), `httpapi.TestRunDeniedAndUnavailable` (container), `work.TestAdvanceDenied` (container), `examend.TestServeRefuses` (container) |
| a caller that names its own client address | the client address the authorizer is told comes from `X-Forwarded-For` only when the peer is in `EXAMEN_TRUSTED_PROXIES`, as the last entry no trusted proxy wrote, and is the peer's address otherwise; past the server's first handler, which runs before the request's span starts, `X-Forwarded-For` holds that address alone or is gone | `httpapi.TestAuthorizerIsToldTheClientAddress`, `httpapi.TestTheSpanSeesTheTrustedClientAddress`, `access.TestAsk` |
| a caller that tries to exhaust a replica | a request body above 4 MiB is refused `413` and nothing is made; a client address past 6,000 requests a minute to the routes that take a token on one replica is refused `429 rate_limited` with `Retry-After` before its token is read, valid or not, and a peer `EXAMEN_TRUSTED_PROXIES` does not list is counted by its own address whatever `X-Forwarded-For` it sends; a client address past 120 requests a minute to `<base path>/version` and `<base path>/openapi.yaml` is refused alike; a subject past 600 requests a minute to one replica is refused `429 rate_limited`, and another subject is not; a request's headers must arrive within 10 seconds and the whole request within 1 minute; an owner holds at most 16 active experiments, and a subject at most 10 progress streams on one replica | `httpapi.TestRequestTooLarge`, `httpapi.TestAddressRateLimit`, `httpapi.TestRateLimit`, `examend.TestTheServerBoundsItsReads`, `httpapi.TestCreateLimits` (container), `httpapi.TestTheStream` (container), `limits.TestEveryLimitIsTheSpecs` |
| a reader of answers and logs | the verifier's refusal never repeats the token; neither a served request's token nor the authorizer's bearer is in the log; a refusal at start names the variable and never repeats a value that does not parse or a secret file's content, a secret prints as nothing, and a database URL's password is not in a start error; the minter's and the key endpoint's clients put neither their bearer nor a token in an error; an error nobody classified is answered `internal` with no detail and logged under its route's pattern, never its path; every error a log line carries has its URL queries and user info cut | `access.TestWhatTheVerifierRefuses`, `examend.TestServe` (container), `examend.TestServeAnnouncesWhatIsConfigured` (container), `config.TestErrorsNameTheVariableOnly`, `config.TestRefusals`, `config.TestSecretsDoNotPrint`, `examend.TestServeRefuses` (container), `owner.TestRefusalsAndOutages`, `keys.TestTheEndpointsRefusals`, `apierr.TestInternal`, `httpapi.TestRoutesNeedABearer`, `examen.TestLoggedErrorsAreCut` |
| a reader of an experiment, its log or its kept answers, after a model Key | an experiment's model Key is in no read of the experiment, no log line and no kept answer of a repeated lease, and a leased Key prints without its value; the `sign_in` handle a create carries goes to the authorizer alone and is in no answer, read of the experiment, event or log line | `httpapi.TestTheExperimentKey` (container), `httpapi.TestARepeatedLeaseKeepsNoKey` (container), `keys.TestTheEndpointLeasesAKey`, `httpapi.TestCreatePassesSignInToTheAuthorizerAlone` (container) |
| a trial that would act beyond its owner | every call a `topos` trial makes to Topos carries an owner token minted for the experiment's owner and for Topos, and every owner token a check or a judge asks for is minted for the experiment's owner; a token is used for 15 minutes at most; a model judge's call carries a Key leased for that judge's one model, released when the experiment ends; a check sandbox is asked for with no network egress, which Cella enforces | `owner.TestTokensAreKeptUntilTheirRenewal`, `owner.TestALongTokenIsUsedNoLongerThanTheMaxLifetime`, `work.TestToposRunsEachTrialAsASession` (container), `work.TestEvalsGradesARun` (container), `work.TestCheckCallsCarryAFreshOwnerToken` (container), `cella.TestTheClient` |
| an external harness that holds a lease | a lease belongs to the subject that took it, and a renewal or a submit by another subject is refused with `lease_not_held` | `postgres.TestStateRefusals` (container) |
| a harness that reports on itself | a value an external harness reports about its own trials is marked `self_reported`, keeps its p-value, gets no q-value and no verdict, and stays out of the family of comparisons corrected together | `analysis.TestSelfReported`, `httpapi.TestAnalyzedImport` (container) |
| text written to sway a model judge | a judge reads the evidence in its user turn only, inside a boundary that no input contains; its answer counts only as one call of its one tool with exactly the metrics, ranges and labels it produces, and anything else is `malformed` and yields no value; a check's output is read as strictly | `grade.TestEvidenceStaysInTheUserTurn`, `grade.FuzzBoundary`, `grade.TestMalformedAnswers`, `grade.TestMalformedYieldsNothing` |
| a system under test that would read the expected answers | a task's references reach only the check sandbox of its trials, read-only at `examen/reference/<name>`: a Topos session's request and an external harness's lease carry none of them; an `arca` reference whose bytes do not match its digest, an `origo` one whose bytes are not the blob its commit names, references above 16 MiB together, or one its store refuses, end the trial's checks `not_run` naming the reference, and none is delivered in part; a task with checks and an `origo` reference is refused at create without `EXAMEN_ORIGO_URL` | `work.TestChecksReceiveTheTasksReferences` (container), `work.TestAReferenceThatCannotBeDeliveredEndsTheChecks` (container), `work.TestASessionIsSentNoReference` (container), `httpapi.TestALeaseCarriesNoReference` (container), `httpapi.TestAnOrigoReferenceNeedsOrigoToReachItsChecks` (container), `origo.TestRead` |
| a session that names a path outside its working directory | a file a session's log names by an absolute path, or by one with an empty, `.` or `..` part, is left out of the trial's kept copy | `topos.TestAPathOutsideTheWorkingDirectoryIsNoFile` |
| evidence that changed after its trial ended | a kept file whose bytes are not the ones recorded when the trial ended is not graded: no check runs on it and no judge is asked; a session log that is gone runs no check | `work.TestEvidenceThatChanged` (container) |
| a grade or a definition rewritten after the fact | Postgres refuses an `UPDATE` of a measurement or of a version of a metric, grader, task, suite or candidate, and a `DELETE` of a measurement while its trial and experiment exist, so a grade once written is never overwritten; each grading ends once | `postgres.TestMeasurementsAreAppendOnly` (container), `postgres.TestGradingsRunInOrderAndEndOnce` (container) |
| a comparison chosen after the results | comparisons declared at create, before any trial runs, are corrected together as one family; a comparison asked for afterwards is marked exploratory and has no result tested alone, since its primary metric is corrected with the rest; a comparison is final only once computed after the experiment ends | `analysis.TestPrimaryStandsAlone`, `analysis.TestExploratory`, `analysis.TestDeclaredComparison`, `httpapi.TestComparisons` (container), `httpapi.TestDeclaredComparisonsRefused` (container), `httpapi.TestAComparisonIsFinalOnceComputedAfterTheEnd` (container) |
| a result that differs on another machine | one golden comparison has a fixed SHA-256 digest; the analysis uses only operations IEEE 754 defines exactly and its own vendored functions, with no product the compiler could fuse with an add; a final comparison recomputed from its recorded inputs is byte-identical | `analysis.TestGoldenAcrossArchitectures`, `analysis.TestUsesVendoredMath`, `analysis.TestNoFusedMultiplyAdd`, `analysis.TestRecomputeFromRecordedInputs` |
| the network between Examen and the services it calls | an issuer, the authorizer, the minter, the key endpoint, Topos, Cella, Lux, Arca or Origo named over plain `http` stops the start unless its host is `localhost` or a loopback address | `config.TestRefusals` |

## What is not defended

- **Routes that take no token.** `/livez`, `/readyz` and `/version` at
  the listener's root, `GET <base path>/version` and
  `GET <base path>/openapi.yaml` answer anyone, and so does the `404` of
  a path Examen does not serve and the `413` of a body above the bound.
  They tell the version, the contract and whether the replica is ready.
  The probes are not rate limited; the version and the contract below
  the base path are held to 120 requests a minute from one client
  address.
- **A permission withdrawn moments ago.** An allow is reused for its
  `ttl`, 60 seconds unless the authorizer says otherwise and 600 at
  most. A request repeated with the same `Idempotency-Key` within 24
  hours replays the kept answer without a new question. A repeated lease
  asks again before it carries the experiment's model Key, and is
  refused as a first lease would be. A progress stream asks again only
  when it has an event to send.
- **What a deny says.** The authorizer's reason for a `403` is returned
  to the caller in `details.reason`, and so is a key source's reason for
  refusing a Key. An authorizer writes reasons its callers may read.
- **Every audience listed.** A token for any audience in
  `EXAMEN_OIDC_AUDIENCE` is accepted.
- **Load beyond one replica's limits.** The request rates and the
  stream count are counted per replica in memory, so each replica added
  allows as much again. Behind a proxy `EXAMEN_TRUSTED_PROXIES` does not
  list, every client has the proxy's address and shares its rate. The
  server sets no write timeout, which would cut progress streams, so a
  client that reads an answer slowly holds its connection.
  Postgres grows without bound: Examen removes only idempotency keys
  and a provisional comparison's list of inputs.
- **An external harness's honesty.** In this release its outputs are
  recorded unread: checks and model judges grade `topos` trials only. It
  holds the experiment's model Key and can spend the Budget on any model
  that Key allows. What it reports about itself is marked
  `self_reported` and declared, not checked.
- **A judge swayed within its schema.** The evidence cannot leave the
  user turn, but text in it can still lean a judge's answer inside the
  ranges and labels it may give. A transcript holds the task's
  instruction and every tool's output.
- **The services Examen is configured with.** Topos, Cella, Lux, Arca,
  the minter, the key endpoint, the authorizer and the issuers are
  believed. Examen checks a session log against the digest it recorded
  when the trial ended, not against Topos's own record. Cella, not
  Examen, holds a check sandbox without network.
- **The database's owner.** The triggers refuse statements, not the
  role that owns the tables, which is the role `examend migrate` ran as.
  A role that can drop a trigger can rewrite measurements, and Examen
  does not notice. A role that can delete an experiment's or a trial's
  row removes its measurements with it. `EXAMEN_DATABASE_URL` carries
  the password.
- **What telemetry carries.** With `OTEL_EXPORTER_OTLP_ENDPOINT` set,
  server spans hold the request path with object ids and names, the user
  agent and the client address. Client spans hold each outbound URL's
  scheme, host and path, such as an owner's file path at Arca, and never
  its query.
- **Error text from libraries.** A log line's error has its URL queries
  and user info cut, but one a library wrote can still name the host and
  path of an issuer, the authorizer or a service. A handler that panics logs the peer's address
  and a stack.
- **An authorizer that answers wrongly,** or an issuer that signs a
  token for the wrong subject. Examen refuses when no decision arrives
  and follows the one that does. It asks the probe question once, at
  start, and starts when the authorizer does not answer it; requests
  then fail closed until it does.
- **A provider that breaks its stated terms.** The owner chooses the
  model; what its provider does inside or against its terms is the
  provider's.

## Audit

Examen has had no external security audit. The commitments above are
held by tests in this repository, which run on every change.

## Supply chain

Dependencies are checked for known vulnerabilities on every push that
changes the code or its dependencies. A release is built from a tagged
commit whose verify run passed, through the shared release stages in
[latere-ai/ci](https://github.com/latere-ai/ci): the `examend` image is
signed by the build stage's identity, run for this repository, and the
release carries a bill of materials and `checksums.txt`, signed by the
publish stage's identity. `test/deploy.TestTheReleaseRunsThroughTheSharedStagesInOrder`
and `test/deploy.TestEveryThirdPartyActionIsPinned` hold the release
workflow to those stages and every third-party action to a pinned
commit.

### Verifying a release

With `TAG` the release's tag:

```sh
gh release download "$TAG" -R latere-ai/examen -D examen-release
cd examen-release
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/latere-ai/ci/\.github/workflows/image-publish\.yml@' \
  --certificate-github-workflow-repository latere-ai/examen checksums.txt
sha256sum -c checksums.txt

cosign verify "ghcr.io/latere-ai/examend:$TAG" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/latere-ai/ci/\.github/workflows/image-build\.yml@' \
  --certificate-github-workflow-repository latere-ai/examen
gh attestation verify "oci://ghcr.io/latere-ai/examend:$TAG" -R latere-ai/examen \
  --signer-workflow latere-ai/ci/.github/workflows/image-build.yml
```

The release workflow runs the same checks against every release it
publishes. Releases up to v0.8.2 were made while the repository was
private and carry no attestation, so `gh attestation verify` applies
from the release after it; releases before v0.8.2 were signed by this
repository's own release workflow, so their signatures name it instead
of the shared stages.
