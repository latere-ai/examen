# Writing an authorizer

Examen verifies who a caller is and never decides what they may do. For
every request it asks one question, may this subject perform this
action on this resource, and follows the answer. With
`EXAMEN_AUTHORIZER_URL` unset, the built-in owner policy answers. Set
it, and your endpoint answers instead: that is how an installation adds
organizations, shared reads, services acting for their users, and
limits on what an owner may run.

The Go package [`latere.ai/x/examen/authorizer`](https://pkg.go.dev/latere.ai/x/examen/authorizer)
([source](../authorizer)) holds the vocabulary: every action, the resource kind each acts on, the fields a
resource carries, and the limits an allow may carry. Import it rather
than copying the strings.

## The question

`examend` sends one `POST` to `EXAMEN_AUTHORIZER_URL` per question, with
`Authorization: Bearer <token>` read from `EXAMEN_AUTHORIZER_TOKEN_FILE`,
and waits at most 5 seconds. A create of an experiment, abridged:

```json
{
  "subject": "https://issuer.example.com|alice",
  "issuer": "https://issuer.example.com",
  "sub": "alice",
  "claims": { "sub": "alice", "aud": "examen", "email": "alice@example.com" },
  "action": "experiment.create",
  "resource": {
    "kind": "Experiment",
    "id": "exp_01m6cc1mbjmgncjpdjdbjre6xa",
    "suite": "sha256:adfa4ad0717e69197699fae19e02719fc92c5911fc684bf2e8023da3b45a3c51",
    "suite_owner": "https://issuer.example.com|alice",
    "executors": ["external"],
    "cells": 2,
    "trials": 10,
    "trials_total": 20,
    "budget": "local",
    "mode": "execute"
  },
  "request": { "id": "req_...", "ip": "203.0.113.7", "user_agent": "curl/8.7.1" }
}
```

- `subject` is the token's issuer and `sub` joined by `|`, and is how
  Examen names every owner and creator. `claims` are the token's,
  verbatim. Examen reads none of them, so telling a person from a
  service, or a member of an organization from anyone else, is yours.
- `action` is one of the vocabulary's, such as `metric.apply`,
  `experiment.read`, `trial.lease` or `baseline.pin`.
- `resource` is flat: its `kind` and `id` beside the fields the action
  sends, under the names of the package's `Field` constants. A field
  Examen does not know for a request is absent, never empty.

## The answer

```json
{ "allow": true, "reason": "", "ttl": 60, "limits": { "max_parallelism": 4 } }
```

| Member | Meaning |
|---|---|
| `allow` | Required. A body without it is no answer, and the request fails closed. |
| `reason` | Optional. Returned to the caller in `details.reason` of a `403`, so write what the caller may read. A deny about a stored object answers that object's `404` and shows no reason. Examen does not log it. |
| `ttl` | Optional. Seconds Examen may reuse an allow for the same question, 60 by default and 600 at most. A deny is reused for 5 seconds. |
| `limits` | Optional, on an allow. What the request is held to: the owner a created object belongs to, the highest parallelism, the most trials, the most active experiments, the executors and judge models allowed, and the Budget the experiment is metered against. An allow can narrow Examen's own limits and never widen them. A limits object Examen cannot read counts as no answer. |
| `filter` | Optional, on a list. `{"owners": [...], "labels": {...}}` narrows the list to those owners and labels. |

An endpoint that does not answer, answers with an error, or answers
without `allow` is no decision: the request is refused and nothing is
done.

## Rules every authorizer keeps

- **Deny the probe.** At start, Examen asks about the reserved resource
  id `00000000-0000-0000-0000-000000000001`. An endpoint that allows it
  is not reading the request, and `serve` refuses to start.
- **Running stays with the owner.** Every action that starts, feeds or
  changes an experiment's execution (`experiment.create`,
  `experiment.update`, `cell.retry`, `experiment.cancel`,
  `experiment.import`, `trial.lease`, `trial.submit` and
  `experiment.run`) belongs to its creator, its owner and the owner's
  members. A share is a reason to allow a read, never one of these.
- **An experiment runs on its owner's suite.** Refuse an
  `experiment.create`, `experiment.update` or `baseline.pin` whose
  `suite_owner` or `baseline_owner` names another owner, shared or not.
- **People create.** Refuse an apply, create, pin or import to a token
  that is not a person's, and to an owner the caller does not belong to.
- **`experiment.run` has no caller.** Examen asks it itself before each
  step of an experiment's unattended work, such as submitting a rollout
  or handing out a cell's external trials. Its subject is the
  experiment's creator and its claims are empty, so a creator who lost
  access stops their experiments.

## The owner policy

Without an endpoint, the owner policy decides: each subject acts on what
it owns and nothing else. It denies the probe and anonymous requests,
narrows every list to the caller's own objects, allows any action on an
object the caller owns and any apply or create under the caller's own
name, and allows `experiment.run` when the experiment's creator and owner
are the subject. It has no administrators and holds no limits of its
own. Organizations, and services acting for their users, need an
authorizer.

## Testing an endpoint

`latere.ai/x/pkg/authz/conformance` drives the cases every authorizer
must pass, with Examen's vocabulary:

```go
conformance.Run(t, url, token, conformance.WithVocabulary(authorizer.Vocabulary()))
```

`authorizer.DecodeLimits` is the reading half of `limits`, so an
endpoint's tests can hold their answers to what Examen will accept.
