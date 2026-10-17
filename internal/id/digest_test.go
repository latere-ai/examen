// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"math"
	"strings"
	"testing"
)

// The canonical form of a document: members sorted, no whitespace, numbers
// as written, <, > and & kept, no newline at the end. The digests are
// fixed: they were computed apart from this package, and a pinned
// reference names one for good, so a change here breaks every pin.
func TestCanonicalGolden(t *testing.T) {
	cases := []struct {
		doc, canonical, digest string
	}{
		{
			doc:       "{ \"n\": 12345678901234567890,\n  \"c\": {\"z\": 1.50, \"y\": \"é\"},\n  \"b\": 1,\n  \"a\": [true, null, \"x<y&z\"] }\n",
			canonical: `{"a":[true,null,"x<y&z"],"b":1,"c":{"y":"é","z":1.50},"n":12345678901234567890}`,
			digest:    "sha256:2576b11d8d0aafe0e9993f8b4eac486fa0d270ded2163988ff8fa002a0c5dcf9",
		},
		{doc: " {} ", canonical: `{}`, digest: "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"},
	}
	for _, c := range cases {
		got, err := Canonical([]byte(c.doc))
		if err != nil || string(got) != c.canonical {
			t.Fatalf("Canonical(%q) = %s, %v; want %s", c.doc, got, err, c.canonical)
		}
		d, err := DigestJSON([]byte(c.doc))
		if err != nil || d != c.digest {
			t.Fatalf("DigestJSON(%q) = %s, %v; want %s", c.doc, d, err, c.digest)
		}
		if !ValidDigest(d) {
			t.Fatalf("ValidDigest(%q) = false", d)
		}
	}
}

// Two documents that differ in member order or whitespace alone have one
// digest, and a value's digest is its rendering's.
func TestDigestIgnoresOrderAndSpace(t *testing.T) {
	a, err := DigestJSON([]byte(`{"name":"release","tasks":["a","b"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := DigestJSON([]byte("{\n \"tasks\" : [ \"a\", \"b\" ],\n \"name\" : \"release\"\n}"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := Digest(struct {
		Tasks []string `json:"tasks"`
		Name  string   `json:"name"`
	}{[]string{"a", "b"}, "release"})
	if err != nil {
		t.Fatal(err)
	}
	if a != b || a != c {
		t.Fatalf("%s %s %s", a, b, c)
	}
	other, err := DigestJSON([]byte(`{"name":"release","tasks":["b","a"]}`))
	if err != nil || other == a {
		t.Fatalf("the order of an array changed nothing: %s %v", other, err)
	}
}

func TestNoCanonicalForm(t *testing.T) {
	for _, doc := range []string{``, `{`, `{"a":1} {"b":2}`, `{"a":1} x`, `nope`} {
		if c, err := Canonical([]byte(doc)); err == nil {
			t.Errorf("Canonical(%q) = %s", doc, c)
		}
		if _, err := DigestJSON([]byte(doc)); err == nil {
			t.Errorf("DigestJSON(%q) had no error", doc)
		}
	}
	if _, err := Digest(math.Inf(1)); err == nil || !strings.Contains(err.Error(), "rendering") {
		t.Errorf("a value encoding/json cannot render: %v", err)
	}
}

func TestValidDigest(t *testing.T) {
	good := "sha256:" + strings.Repeat("0a", 32)
	if !ValidDigest(good) {
		t.Fatal(good)
	}
	for _, bad := range []string{"", "sha256:", good[:len(good)-1], good + "0", strings.ToUpper(good), "sha512:" + strings.Repeat("0a", 32), "sha256:" + strings.Repeat("0g", 32)} {
		if ValidDigest(bad) {
			t.Errorf("ValidDigest(%q) = true", bad)
		}
	}
}
