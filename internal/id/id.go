// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package id makes the identifiers of specs/002-objects.md: a prefix that
// says what the object is, an underscore, and a ULID in lowercase, which is
// 48 bits of time followed by 80 random bits in 26 characters of
// Crockford's base 32. An identifier sorts by when it was made, so a list
// ordered by id is a list ordered by time, and a page of that list is
// addressed by the last id on it.
//
// The package also makes the digest a versioned object is pinned by: the
// SHA-256 of the object's canonical JSON, written "sha256:" and the hex.
package id

import "latere.ai/x/pkg/ulid"

// The prefixes of the identifiers of specs/002-objects.md.
const (
	Metric        = "met"
	Grader        = "grd"
	Task          = "tsk"
	Suite         = "sui"
	Candidate     = "cnd"
	Experiment    = "exp"
	Cell          = "cel"
	Trial         = "tri"
	Measurement   = "msr"
	Taxonomy      = "tax"
	Baseline      = "bas"
	Comparison    = "cmp"
	Bar           = "bar"
	BarEvaluation = "bev"
	Share         = "shr"
	DatasetExport = "dsx"
	Report        = "rep"
	Publication   = "pub"
	Submission    = "sbm"
	Event         = "evt"
	// Lease names a lease of external trials: a hold on trials, not an
	// object, with no owner or row of its own.
	Lease = "lse"
)

// Prefixes lists every prefix, in the order of the table of spec 002, the
// lease's last.
func Prefixes() []string {
	return []string{
		Metric, Grader, Task, Suite, Candidate, Experiment, Cell, Trial, Measurement, Taxonomy,
		Baseline, Comparison, Bar, BarEvaluation, Share, DatasetExport, Report, Publication, Submission, Event, Lease,
	}
}

// New returns a new identifier with the prefix: "<prefix>_<ulid>", in
// lowercase. Two made in the same millisecond still sort in the order they
// were made.
func New(prefix string) string { return ulid.New(prefix + "_") }

// Valid reports whether s is an identifier with the prefix: the prefix, an
// underscore and a ULID, in either case, since an identifier is stored in
// the case it was minted in.
func Valid(prefix, s string) bool { return ulid.Valid(prefix+"_", s) }

// PrefixOf is the prefix of an identifier of any kind of spec 002, and
// false for a string that is none.
func PrefixOf(s string) (string, bool) {
	for _, p := range Prefixes() {
		if Valid(p, s) {
			return p, true
		}
	}
	return "", false
}
