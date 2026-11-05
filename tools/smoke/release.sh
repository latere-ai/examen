#!/usr/bin/env bash
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0
#
# Live check of a deployed Examen, run after a rollout. The release pipeline
# runs it after the deploy credential is dropped and attaches its Markdown
# evidence to the release; an operator can run it by hand.
#
# Unauthenticated, read-only, safe to run any number of times. It does not
# wait: it fails at once and the caller runs it again.
#
# BASE_URL is the public origin plus the base path the API is served under,
# for example https://examen.example.com/v1/evals. The probes (/livez,
# /readyz, /version) are served at the root of examend and the ingress routes
# only /v1/evals to it, so none of them is public.
#
# MISSING ROUTE: the served version is not readable from any public route.
# The only unauthenticated route is GET <base>/openapi.yaml, whose info.version
# is the contract's version ("1"), not the build's. So this script checks
# that the route answers and serves the contract, and cannot compare the
# served version with TAG. TAG is echoed into the evidence and the evidence
# says the version was not checked. A public version route would close this.
#
# Required tools: curl, grep.
#
# Environment:
#   BASE_URL   the origin and base path to check; required
#   TAG        the release being proved; recorded, not compared (see above)
#   COMMIT     the release commit, for the evidence
#   DEPLOY_URL the run that deployed it, for the evidence
#   OUTPUT_MD  where to write the Markdown evidence; nowhere when unset

set -euo pipefail

BASE_URL="${BASE_URL:-}"
BASE_URL="${BASE_URL%/}"
TAG="${TAG:-}"
COMMIT="${COMMIT:-unknown}"
DEPLOY_URL="${DEPLOY_URL:-}"
OUTPUT_MD="${OUTPUT_MD:-}"

pass() { printf 'ok: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }

[ -n "$BASE_URL" ] || fail "BASE_URL is required; this smoke has no default target"
for cmd in curl grep; do
  command -v "$cmd" >/dev/null || fail "$cmd is required"
done

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

code=$(curl -sS -o "$tmp/openapi" -w '%{http_code}' "${BASE_URL}/openapi.yaml") \
  || fail "GET /openapi.yaml: curl failed"
[ "$code" = "200" ] || fail "GET /openapi.yaml: expected 200, got $code (body: $(head -c 200 "$tmp/openapi" 2>/dev/null || true))"
grep -q '^openapi:' "$tmp/openapi" || fail "GET /openapi.yaml: the body is not an OpenAPI document"
pass "GET /openapi.yaml (200)"

if [ -n "$OUTPUT_MD" ]; then
  {
    echo "<!-- release-evidence -->"
    echo
    echo "## Release evidence"
    echo
    echo "- Tag: \`${TAG:-unknown}\`"
    echo "- Commit: \`${COMMIT}\`"
    [ -n "$DEPLOY_URL" ] && echo "- Deploy: ${DEPLOY_URL}"
    echo "- Origin: ${BASE_URL}"
    echo "- Smoke: \`/openapi.yaml\` answered 200 with an OpenAPI document"
    echo "- Version: not checked; no public route serves the build's version"
  } > "$OUTPUT_MD"
fi

printf '\nrelease smoke passed\n'
