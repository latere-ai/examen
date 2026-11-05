#!/bin/sh
# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

# Builds the server image, migrates the database named by EXAMEN_DATABASE_URL
# with it, then starts it with a read-only root file system and no
# capability, and waits for /readyz to answer. The e2e gate runs it with a
# Postgres it started (.lateregate.yaml, suite image-serve).
#
# The Postgres listens on the loopback of the machine that runs this script.
# On Linux a container shares that loopback with --network host. Where the
# container engine runs in a VM (podman on a Mac) the loopback is the VM's,
# so the container reaches the machine through host.containers.internal.
set -eu

: "${EXAMEN_DATABASE_URL:?EXAMEN_DATABASE_URL names the Postgres to migrate}"

engine=$(command -v podman 2>/dev/null || command -v docker 2>/dev/null || true)
if [ -z "$engine" ]; then
	echo "image-e2e needs podman or docker" >&2
	exit 1
fi

# A free loopback port for the server, so a service already on 8080 does not
# answer in its place.
port=""
for _ in $(seq 1 50); do
	candidate=$((20000 + $(od -An -N2 -tu2 /dev/urandom | tr -d ' ') % 40000))
	# curl exits 7 when nothing listens there.
	status=0
	curl -s -o /dev/null --max-time 1 "http://127.0.0.1:${candidate}/" || status=$?
	if [ "$status" -eq 7 ]; then
		port=$candidate
		break
	fi
done
if [ -z "$port" ]; then
	echo "no free loopback port found" >&2
	exit 1
fi

name="examen-e2e-$$"
url=$EXAMEN_DATABASE_URL
if [ "$(uname -s)" = "Linux" ]; then
	net="--network host"
	publish=""
	addr="127.0.0.1:${port}"
else
	net=""
	publish="-p 127.0.0.1:${port}:8080"
	addr=":8080"
	url=$(printf '%s' "$url" | sed -e 's#@127\.0\.0\.1:#@host.containers.internal:#' -e 's#@localhost:#@host.containers.internal:#')
fi

cleanup() {
	"$engine" rm -f "${name}-serve" > /dev/null 2>&1 || true
	"$engine" image rm "${name}:candidate" > /dev/null 2>&1 || true
}

"$engine" build -t "${name}:candidate" .
trap cleanup EXIT

# shellcheck disable=SC2086 # $net and $publish are flag lists
"$engine" run --rm $net --read-only --cap-drop ALL \
	-e EXAMEN_DATABASE_URL="$url" "${name}:candidate" migrate

# An issuer that is never asked: the probes need no token, and an issuer that
# does not answer at start is named and the server starts.
# shellcheck disable=SC2086
"$engine" run -d --name "${name}-serve" $net $publish --read-only --cap-drop ALL \
	-e EXAMEN_DATABASE_URL="$url" -e EXAMEN_OIDC_ISSUERS=https://issuer.example.com \
	-e EXAMEN_ADDR="$addr" "${name}:candidate" serve > /dev/null

for _ in $(seq 1 120); do
	if curl -fsS "http://127.0.0.1:${port}/readyz" > /dev/null 2>&1; then
		echo "the server answers"
		exit 0
	fi
	sleep 0.5
done
"$engine" logs "${name}-serve" || true
echo "the server did not answer" >&2
exit 1
