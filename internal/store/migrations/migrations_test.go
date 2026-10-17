// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package migrations

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
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

func TestHighestOf(t *testing.T) {
	got, err := HighestOf(fstest.MapFS{
		"000002_b.up.sql": {}, "000010_c.up.sql": {}, "000001_a.up.sql": {}, "README": {},
	})
	if err != nil || got != 10 {
		t.Fatalf("%d %v", got, err)
	}
	if _, err := HighestOf(fstest.MapFS{"x_a.up.sql": {}}); err == nil {
		t.Error("a migration with no number")
	}
	if _, err := HighestOf(fstest.MapFS{}); err == nil {
		t.Error("no migration")
	}
	if _, err := HighestOf(brokenFS{}); err == nil {
		t.Error("a directory that cannot be read")
	}
}

type brokenFS struct{}

func (brokenFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }
