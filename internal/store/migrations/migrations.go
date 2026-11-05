// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package migrations embeds the schema of the store as golang-migrate
// files, numbered from 000001 and applied in order over the direct
// connection by `examend migrate`. Only forward migrations are shipped, and
// a released migration is never edited, because the databases that ran it
// already have. The first migration of each major release writes that
// major into the one-row table examen_schema, which `examend serve` reads.
package migrations

import (
	"embed"

	"latere.ai/x/pkg/pgxmigrate"
)

// Major is the major release whose schema this binary carries. A database
// whose examen_schema names another is one `examend serve` refuses.
const Major = 0

// FS holds the migration files.
//
//go:embed *.sql
var FS embed.FS

// Highest is the newest migration this binary carries.
func Highest() (uint, error) { return pgxmigrate.HighestOf(FS, ".") }
