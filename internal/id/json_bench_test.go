// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"testing"
)

// jbDocument is a deterministic document of about n bytes in the shape of
// a suite or an inputs document: a name, then one member per task holding
// its digests, its metric values (numbers as given) and a note.
func jbDocument(n int) []byte {
	doc := map[string]any{"name": "bench-suite", "analysis_version": "1", "primary_metric": "tests_pass"}
	var tasks []map[string]any
	size := 0
	for i := 0; size < n; i++ {
		sum := sha256.Sum256([]byte(strconv.Itoa(i)))
		t := map[string]any{
			"task":   "task-" + strconv.Itoa(i),
			"digest": DigestPrefix + hex.EncodeToString(sum[:]),
			"values": map[string]any{"tests_pass": jsontext.Value(`1`), "quality": jsontext.Value(`0.50`), "cost_usd": jsontext.Value(`0.0123`), "size": jsontext.Value(`12345678901234567890`)},
			"labels": []string{"game", "tool", "library"},
			"note":   "the run wrote the program, ran it against the sample input and kept the output",
			"graded": i%2 == 0,
		}
		tasks = append(tasks, t)
		raw, err := json.Marshal(t)
		if err != nil {
			panic(err)
		}
		size += len(raw) + 1
	}
	doc["tasks"] = tasks
	raw, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return raw
}

var jbSizes = []struct {
	name  string
	bytes int
}{{"small", 512}, {"medium", 10 << 10}, {"large", 200 << 10}}

// BenchmarkJSONCanonicalRoundTrip is the canonical form every digest is
// taken over: the document decoded with its numbers as given and encoded
// again with sorted members. It runs for each version applied, each inputs
// document and each idempotency key.
func BenchmarkJSONCanonicalRoundTrip(b *testing.B) {
	for _, c := range jbSizes {
		b.Run(c.name, func(b *testing.B) {
			doc := jbDocument(c.bytes)
			b.ReportAllocs()
			b.SetBytes(int64(len(doc)))
			for b.Loop() {
				if _, err := Canonical(doc); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkJSONDigestRoundTrip is the digest of a document: its canonical
// form and the SHA-256 of that.
func BenchmarkJSONDigestRoundTrip(b *testing.B) {
	for _, c := range jbSizes {
		b.Run(c.name, func(b *testing.B) {
			doc := jbDocument(c.bytes)
			b.ReportAllocs()
			b.SetBytes(int64(len(doc)))
			for b.Loop() {
				d, err := DigestJSON(doc)
				if err != nil {
					b.Fatal(err)
				}
				if !ValidDigest(d) {
					b.Fatal("not a digest: " + d)
				}
			}
		})
	}
}

// BenchmarkJSONDigestMarshal is the digest of a Go value, which renders it
// first: the path of an applied object and of a pinned environment.
func BenchmarkJSONDigestMarshal(b *testing.B) {
	for _, c := range jbSizes {
		b.Run(c.name, func(b *testing.B) {
			var v map[string]any
			if err := json.Unmarshal(jbDocument(c.bytes), &v); err != nil {
				b.Fatal(err)
			}
			raw, err := json.Marshal(v)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				if _, err := Digest(v); err != nil {
					b.Fatal(fmt.Sprint(err))
				}
			}
		})
	}
}
