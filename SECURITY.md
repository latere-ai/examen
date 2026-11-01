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
| a caller with a valid token of another owner | every question is asked of the authorizer, or of the owner policy when none is configured, before anything is done; under the owner policy an owner reaches their own objects alone; a request about an object the caller may not see is answered exactly as one about an object that does not exist |
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

Dependencies are checked for known vulnerabilities on every push that
changes code. A release is built from a tagged commit whose code passed
verify, through the shared release stages in
[latere-ai/ci](https://github.com/latere-ai/ci): the image is signed by
the build stage's identity, run for this repository, and the release
carries a bill of materials and `checksums.txt`, signed by the publish
stage's identity. To check a release, with `TAG` its tag:

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
