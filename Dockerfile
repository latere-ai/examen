# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

# The server image: examend and nothing else. Build it from the repository
# root:
#
#   podman build -t examend .
#
# and stamp a version the way `make build` does:
#
#   podman build -t examend \
#     --build-arg VERSION="$(git describe --tags --always)" \
#     --build-arg COMMIT="$(git rev-parse --short HEAD)" \
#     --build-arg DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)" .
#
# The release pipeline builds this same file for linux/amd64 and
# linux/arm64, so a released image differs from a developer's in its
# version and in nothing else. The same image runs `serve` and `migrate`.

# The build stage runs on the machine that builds, whatever platform the
# image is for, and cross-compiles: the binary has no cgo, so the compiler
# needs no emulation. TARGETOS and TARGETARCH are set by the builder per
# platform; a builder that sets neither leaves them empty, and the compiler
# then targets the platform it runs on.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -buildvcs=false \
      -ldflags "-X latere.ai/x/examen/internal/version.Version=${VERSION} -X latere.ai/x/examen/internal/version.Commit=${COMMIT} -X latere.ai/x/examen/internal/version.Date=${DATE}" \
      -o /out/examend ./cmd/examend

# examend starts no other program and writes no file: every fact it keeps
# is in the database. So the runtime stage is distroless: the roots it
# verifies certificates against, no shell, no package manager. The user is
# numeric, so a cluster that requires a non-root user can check it without
# reading the image's passwd file.
FROM gcr.io/distroless/static-debian12:nonroot
EXPOSE 8080
USER 65532:65532
COPY --from=build /out/examend /usr/local/bin/examend
ENTRYPOINT ["/usr/local/bin/examend"]
CMD ["serve"]
