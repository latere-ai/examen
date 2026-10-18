// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package api holds the HTTP contract of specs/003-api.md: openapi.yaml,
// the one description of every route, request, response and error code.
// The server serves this file and is held to it by its tests, and a client
// may be generated from it. A route of a later build step is in the
// document only from the release that serves it.
package api

import (
	"bytes"
	_ "embed"
)

// OpenAPI is the contract, as OpenAPI 3.1 in YAML.
//
//go:embed openapi.yaml
var OpenAPI []byte

// servers is the line of the document that names the default base path.
var servers = []byte("servers:\n  - url: /v1\n")

// Served is the document as a server mounted under basePath serves it: its
// servers entry names that base path, "/" for none.
func Served(basePath string) []byte {
	if basePath == "/v1" {
		return OpenAPI
	}
	if basePath == "" {
		basePath = "/"
	}
	return bytes.Replace(OpenAPI, servers, []byte("servers:\n  - url: "+basePath+"\n"), 1)
}
