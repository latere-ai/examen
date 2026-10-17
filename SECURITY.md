# Security

Examen reads text nobody reviewed, the output of an agent or a program
and a task's content, keeps evidence of its owners' work, and holds the
measurements a release decision rests on. This file is what the project
promises about that, what it does not, and how to tell us when it is
wrong. The threat model is
[`specs/012-security-and-threat-model.md`](specs/012-security-and-threat-model.md).

## Reporting a vulnerability

Report one to security@latere.ai. Do not open a public issue for it. You
will hear back within three business days.

## Commitments

Each row is who could attack, and what Examen commits to against them.
Each commitment is held by tests in this repository: a control without a
test is a claim, and no claim is listed here. The list grows with the
code.

| Who | What Examen commits to |
|---|---|
| a caller with no token, or a token of another issuer, another audience, an expired one or a forged one | the request is refused before anything is read or done, with the verifier's reason and no part of the token in the answer |
| a caller with a valid token of another owner | every question is asked of the authorizer, or of the owner policy when none is configured, before anything is done; under the owner policy an owner reaches their own objects alone |
| a caller when the authorizer cannot answer | a request with no well-formed decision does nothing and is refused; limits or a filter Examen cannot read count as no decision; an authorizer that allows the reserved probe stops the start |
| a reader of responses and logs | an error never carries a token or the value of a variable that does not parse, and a configuration error names the variable alone |
| a caller that tries to exhaust a replica | a request body past its bound is refused before it is read whole, and a subject past its request rate is refused with the time to wait |

## What is not defended

- **The operator, a compromised core or a compromised host.** The cores
  Examen composes, the authorizer, the minter and the key endpoint are
  trusted as named; a core that reports false records is believed.
- **An authorizer that answers wrongly,** or an issuer that signs a token
  for the wrong subject. Examen refuses when no decision arrives. It
  follows the decision that does.
- **An external harness's honesty.** Its outputs are graded and its
  provenance is marked declared, but they may not be what its system
  produced. That is its owner's own experiment.
- **A provider that breaks its stated terms.** The owner chooses the
  model; what its provider does inside or against its terms is the
  provider's.
- **Denial of service** beyond the limits a replica holds.

## Supply chain

Dependencies are checked for known vulnerabilities on every push. A
release is built from a tagged commit whose verify run passed: the
image is signed by the release workflow's identity, and the release
carries a bill of materials with a signed checksum.
