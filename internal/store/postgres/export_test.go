// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

// MigrateWith is Migrate with the migration files given, for a test that
// applies a migration the binary does not carry.
var MigrateWith = migrate
