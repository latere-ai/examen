// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package version holds the build metadata the linker stamps into a binary.
package version

import "fmt"

// Version, Commit and Date are set at link time by `make build` and by the
// image's build stage. A binary built any other way reports the defaults.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String is the build identity: the binary's name, its version, and the
// commit and date it was built from. The release workflow matches the
// line `examend version` prints on its first two words.
func String() string {
	return fmt.Sprintf("examend %s (%s, %s)", Version, Commit, Date)
}

// Line is the one line `examend version` prints: the build identity with
// the newest schema version the binary carries.
func Line(schema uint) string {
	return fmt.Sprintf("examend %s (%s, %s, schema %d)", Version, Commit, Date, schema)
}

// UserAgent is what Examen sends as its own User-Agent on a request it
// makes.
func UserAgent() string { return "examen/" + Version }
