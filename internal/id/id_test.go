// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var shape = regexp.MustCompile(`^[a-z]{3}_[0-7][0-9a-hjkmnp-tv-z]{25}$`)

// Every identifier is its prefix, an underscore and a lowercase ULID, and ids
// of one kind sort by creation time, within one millisecond too.
func TestPrefixedAndOrdered(t *testing.T) {
	var made []string
	for range 200 {
		for _, p := range Prefixes() {
			s := New(p)
			if !shape.MatchString(s) || !Valid(p, s) {
				t.Fatalf("%q is not a %s id", s, p)
			}
			if got, ok := PrefixOf(s); !ok || got != p {
				t.Fatalf("PrefixOf(%q) = %q, %v", s, got, ok)
			}
			if p == Experiment {
				made = append(made, s)
			}
		}
	}
	if !slices.IsSorted(made) {
		t.Fatal("ids made one after another do not sort in the order they were made")
	}
}

func TestValid(t *testing.T) {
	good := New(Experiment)
	for _, bad := range []string{
		"", "exp_", "cel" + good[3:], good + "x", good[:len(good)-1], "exp_8" + good[5:], good[:len(good)-1] + "i",
	} {
		if Valid(Experiment, bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
	if !Valid(Experiment, good) {
		t.Errorf("Valid(%q) = false", good)
	}
	// An identifier minted in uppercase is as valid as one in lowercase.
	upper := good[:4] + strings.ToUpper(good[4:])
	if !Valid(Experiment, upper) {
		t.Errorf("Valid(%q) = false", upper)
	}
	if p, ok := PrefixOf(upper); !ok || p != Experiment {
		t.Errorf("PrefixOf(%q) = %q, %v", upper, p, ok)
	}
	for _, bad := range []string{"", "release", "zzz" + good[3:], "exp-" + good[4:], "exp_short"} {
		if p, ok := PrefixOf(bad); ok {
			t.Errorf("PrefixOf(%q) = %q", bad, p)
		}
	}
}

// The prefixes are the table of specs/002-objects.md, in its order, and
// the lease's beside it.
func TestPrefixesAreTheSpecs(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "specs", "002-objects.md"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	}()
	row := regexp.MustCompile("^\\| [a-z ]+ \\| `([a-z]{3})_` \\| (yes|no) \\|$")
	var table []string
	scan := bufio.NewScanner(f)
	text := ""
	for scan.Scan() {
		text += scan.Text() + "\n"
		if m := row.FindStringSubmatch(scan.Text()); m != nil {
			table = append(table, m[1])
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if want := Prefixes()[:len(Prefixes())-1]; !slices.Equal(table, want) {
		t.Fatalf("spec 002 lists %v, the package %v", table, want)
	}
	if !strings.Contains(text, "the prefix `"+Lease+"_`") {
		t.Fatalf("spec 002 does not name the lease's prefix %s_", Lease)
	}
}
