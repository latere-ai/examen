# SPDX-FileCopyrightText: 2026 Latere AI
# SPDX-License-Identifier: Apache-2.0

GO ?= go

.PHONY: build check clean db fmt hooks openapi run specs

# The whole bar. Every gate lives in latere.ai/x/ci-gate, pinned as a tool
# in go.mod and configured in .lateregate.yaml, so this target is a name for
# `go tool lateregate` and nothing else. One gate at a time: `go tool
# lateregate cover`. The plan: `go tool lateregate list`.
check:
	@$(GO) tool lateregate

.DEFAULT_GOAL := check

OUT_DIR := out
SERVICE := examend
MODULE := $(shell $(GO) list -m)

# Build metadata, deferred so the git and date calls run only for a build. A
# dirty tree marks the commit, because a binary built from uncommitted
# changes cannot be reproduced from its commit.
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DIRTY = $(shell test -n "$$(git status --porcelain 2>/dev/null)" && echo -dirty)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
VERSION_PKG = $(MODULE)/internal/version
LDFLAGS = -X $(VERSION_PKG).Version=$(VERSION) \
          -X $(VERSION_PKG).Commit=$(COMMIT)$(DIRTY) \
          -X $(VERSION_PKG).Date=$(BUILD_DATE)

build:
	@mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
		-o $(OUT_DIR)/$(SERVICE) ./cmd/$(SERVICE)
	@echo "built $(OUT_DIR)/$(SERVICE)"

# The local database `make run` serves from: a Postgres container in the
# developer's container runtime, Podman where it is installed and Docker
# otherwise, on a loopback port. It is kept between runs; `make clean`
# leaves it, and removing the container removes what it held. Readiness is
# asked over TCP: the image's setup first runs a server on its socket
# alone, which a check over the socket would count as ready.
CONTAINER ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)
DB_CONTAINER := examen-postgres
DB_PORT ?= 55432
DB_URL := postgres://examen:examen@127.0.0.1:$(DB_PORT)/examen?sslmode=disable

db:
	@test -n "$(CONTAINER)" || { echo "make db needs podman or docker" >&2; exit 1; }
	@$(CONTAINER) inspect $(DB_CONTAINER) > /dev/null 2>&1 || \
		$(CONTAINER) run -d --name $(DB_CONTAINER) -p 127.0.0.1:$(DB_PORT):5432 \
			-e POSTGRES_USER=examen -e POSTGRES_PASSWORD=examen -e POSTGRES_DB=examen \
			docker.io/library/postgres:16-alpine > /dev/null
	@$(CONTAINER) start $(DB_CONTAINER) > /dev/null
	@for _ in $$(seq 1 60); do \
		$(CONTAINER) exec $(DB_CONTAINER) pg_isready -h 127.0.0.1 -U examen -d examen > /dev/null 2>&1 && exit 0; \
		sleep 0.5; \
	done; echo "the local database did not become ready" >&2; exit 1

# run builds examend, applies the migrations to the local database, and
# serves from it. It verifies tokens of the issuers EXAMEN_OIDC_ISSUERS
# names, and reads every other variable from the developer's environment
# (docs: specs/011-distribution.md).
run: build db
	EXAMEN_DATABASE_URL='$(DB_URL)' $(OUT_DIR)/$(SERVICE) migrate
	EXAMEN_DATABASE_URL='$(DB_URL)' $(OUT_DIR)/$(SERVICE) serve

# specs checks the spec tree alone.
specs:
	@$(GO) tool lateregate spec-lint

# openapi runs the tests that hold the router to api/openapi.yaml.
openapi:
	@$(GO) test ./api ./internal/httpapi -run 'Document|Contract|Routes'

fmt:
	@gofmt -w .

# hooks points git at the repository hook directory.
hooks:
	git config core.hooksPath .githooks
	@echo "installed git hooks (core.hooksPath=.githooks)"

clean:
	rm -rf $(OUT_DIR)
