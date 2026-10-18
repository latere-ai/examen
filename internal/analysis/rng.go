// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"math/bits"
	"math/rand/v2"
)

// Seed is a SHA-256 value a generator is seeded from.
type Seed [sha256.Size]byte

// String is the seed in hex, as a comparison records it.
func (s Seed) String() string { return hex.EncodeToString(s[:]) }

// The procedures of a result, each of which draws from its own seed, so no
// result's draws depend on the order in which results are computed.
const (
	procSummaryCandidate = "summary_interval:candidate"
	procSummaryReference = "summary_interval:reference"
	procDeltaInterval    = "delta_interval"
	procTest             = "test"
)

// RootSeed is SHA-256("examen.analysis/v1" || 0x00 || inputs_digest).
func RootSeed(inputsDigest string) Seed {
	h := sha256.New()
	h.Write([]byte(Version))
	h.Write([]byte{0})
	h.Write([]byte(inputsDigest))
	var s Seed
	copy(s[:], h.Sum(nil))
	return s
}

// ProcedureSeed is SHA-256(root || metric digest || 0x00 || scope || 0x00 ||
// procedure).
func ProcedureSeed(root Seed, metric, scope, procedure string) Seed {
	h := sha256.New()
	h.Write(root[:])
	h.Write([]byte(metric))
	h.Write([]byte{0})
	h.Write([]byte(scope))
	h.Write([]byte{0})
	h.Write([]byte(procedure))
	var s Seed
	copy(s[:], h.Sum(nil))
	return s
}

// The scopes of a result's seed. A name of spec 003 holds no ":" and no
// space, so no task's scope equals the suite's or a pair's.
func taskScope(name string) string        { return "task:" + name }
func acrossTasksScope(a, b string) string { return "task:" + a + " vs " + b }
func pairScope(a, b string) string        { return "pair:" + a + ":" + b }
func resultScope(level Level, task string) string {
	if level == LevelSuite {
		return "suite"
	}
	return task
}

// generator draws from the 128-bit permuted congruential generator with
// DXSM output that math/rand/v2's PCG implements, by two methods of its
// own, so that no library helper whose algorithm may change sits between
// the seed and a draw.
type generator struct{ src *rand.PCG }

// newGenerator seeds the generator with the first and second 8 bytes of
// the seed, read as big-endian unsigned integers.
func newGenerator(s Seed) *generator {
	return &generator{src: rand.NewPCG(binary.BigEndian.Uint64(s[:8]), binary.BigEndian.Uint64(s[8:16]))}
}

// below returns a uniform integer in [0, n) by Lemire's method: the high
// half of the 128-bit product of a 64-bit draw and n, drawn again while
// the low half is below (2^64 - n) mod n. n is at least 1.
func (g *generator) below(n int) int {
	un := uint64(n)
	threshold := -un % un
	for {
		hi, lo := bits.Mul64(g.src.Uint64(), un)
		if lo >= threshold {
			return int(hi)
		}
	}
}

// shuffle permutes v by Fisher and Yates: for i from len-1 down to 1, swap
// position i with the uniform integer in [0, i+1).
func (g *generator) shuffle(v []float64) {
	for i := len(v) - 1; i >= 1; i-- {
		j := g.below(i + 1)
		v[i], v[j] = v[j], v[i]
	}
}
