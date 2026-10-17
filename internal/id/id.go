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

import (
	"crypto/rand"
	"strings"
	"sync"
	"time"
)

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

// alphabet is Crockford's base 32 in lowercase.
const alphabet = "0123456789abcdefghjkmnpqrstvwxyz"

// Generator makes identifiers. Two made in the same millisecond still sort
// in the order they were made: the second takes the first's random part
// plus one. The zero value is ready to use.
type Generator struct {
	// Now is the clock. Nil takes time.Now.
	Now func() time.Time

	mu   sync.Mutex
	ms   uint64
	tail [10]byte
}

// Default is the generator the server uses.
var Default = &Generator{}

// New returns a new identifier with the prefix from Default.
func New(prefix string) string { return Default.New(prefix) }

// New returns a new identifier with the prefix: "<prefix>_<ulid>".
func (g *Generator) New(prefix string) string {
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	ms := uint64(now().UnixMilli())

	g.mu.Lock()
	if ms > g.ms {
		g.ms = ms
		// crypto/rand.Read does not fail: it aborts the process when the
		// system's source is unavailable.
		_, _ = rand.Read(g.tail[:])
	} else {
		// The same millisecond, or a clock that stepped back: stay on the
		// last time and count up, so order is kept.
		for i := len(g.tail) - 1; i >= 0; i-- {
			if g.tail[i]++; g.tail[i] != 0 {
				break
			}
		}
	}
	var raw [16]byte
	for i := range 6 {
		raw[i] = byte(g.ms >> (40 - 8*i))
	}
	copy(raw[6:], g.tail[:])
	g.mu.Unlock()

	// 128 bits in 26 characters of 5 bits: the first character holds the
	// top 3 bits, and every one after it the next 5.
	out := make([]byte, 0, len(prefix)+27)
	out = append(out, prefix...)
	out = append(out, '_', alphabet[raw[0]>>5])
	acc, bits := uint(raw[0]&0x1f), 5
	for _, b := range raw[1:] {
		acc, bits = acc<<8|uint(b), bits+8
		for bits >= 5 {
			bits -= 5
			out = append(out, alphabet[acc>>bits&0x1f])
		}
	}
	return string(out)
}

// Valid reports whether s is an identifier with the prefix: the prefix, an
// underscore and 26 characters of the alphabet.
func Valid(prefix, s string) bool {
	rest, ok := strings.CutPrefix(s, prefix+"_")
	return ok && ulid(rest)
}

// PrefixOf is the prefix of an identifier of any kind of spec 002, and
// false for a string that is none.
func PrefixOf(s string) (string, bool) {
	prefix, rest, ok := strings.Cut(s, "_")
	if !ok || !ulid(rest) {
		return "", false
	}
	for _, p := range Prefixes() {
		if p == prefix {
			return p, true
		}
	}
	return "", false
}

// ulid reports whether s is 26 characters of the alphabet whose first
// holds no more than 3 bits.
func ulid(s string) bool {
	if len(s) != 26 || s[0] > '7' {
		return false
	}
	for i := range len(s) {
		if !strings.ContainsRune(alphabet, rune(s[i])) {
			return false
		}
	}
	return true
}
