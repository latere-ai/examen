## What this changes

<!-- What behavior is different after this, for whoever runs examend, calls its API, runs a harness against it, or writes an authorizer. -->

## Why

<!-- The problem it solves. Link the issue if there is one. -->

## Checklist

- [ ] `make` passes locally.
- [ ] A bug fix carries a test that fails without it.
- [ ] A change to a figure the analysis reports is held by a test, and a
      claim that a figure is unaffected by something is proven by one.
- [ ] No route, record, log line, error or test fixture carries a token,
      a key or any part of one.
- [ ] A change to the API, a configuration variable, or the authorizer's
      vocabulary or limits is reflected in its spec, in `docs/`, and in
      `api/openapi.yaml` where it applies.
- [ ] A change someone running `examend` or calling its API would notice
      has a line under `## Unreleased` in `CHANGELOG.md`.
