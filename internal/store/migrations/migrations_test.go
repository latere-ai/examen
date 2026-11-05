// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

// The binary carries its migrations, forward only, the first of them
// writing the major.
func TestTheEmbeddedMigrations(t *testing.T) {
	highest, err := Highest()
	if err != nil || highest < 1 {
		t.Fatalf("%d %v", highest, err)
	}
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".up.sql") {
			t.Errorf("%s is not a forward migration", e.Name())
		}
	}
	first, err := fs.ReadFile(FS, "000001_schema.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "INSERT INTO examen_schema (major) VALUES (0)") || Major != 0 {
		t.Fatal("the first migration does not write the major this binary carries")
	}
}
