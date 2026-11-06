// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"unicode/utf8"
)

// DigestPrefix starts every digest.
const DigestPrefix = "sha256:"

// The canonical JSON of a value, which a digest is taken over, and which
// must never change, since a pinned reference names a digest for good:
//
//   - object members sorted by their names' UTF-8 bytes;
//   - no whitespace between tokens and no newline at the end;
//   - a number written as it was given, digit for digit, so 1.50 stays
//     1.50 and a large integer loses no precision;
//   - strings escaped as JSON requires, plus U+2028 and U+2029, with <, > and
//     & kept as they are, so a digest does not depend on a renderer's HTML rule.
//
// A document with anything after its one value, or that is not JSON, has
// no canonical form.

// documentOptions reads a document as it may arrive: a repeated member name
// keeps its last value and bytes that are not UTF-8 read as U+FFFD.
var documentOptions = json.JoinOptions(
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
)

// Canonical is the canonical JSON of a JSON document.
func Canonical(doc []byte) ([]byte, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(doc), documentOptions)
	var out bytes.Buffer
	if err := writeCanonical(dec, &out); err != nil {
		return nil, fmt.Errorf("id: the document is not JSON: %w", err)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.New("id: the document holds more than one value")
	}
	return out.Bytes(), nil
}

// writeCanonical reads the next value of dec and writes its canonical form.
func writeCanonical(dec *jsontext.Decoder, out *bytes.Buffer) error {
	tok, err := dec.ReadToken()
	if err != nil {
		return err
	}
	switch tok.Kind() {
	case '{':
		members := map[string][]byte{}
		for dec.PeekKind() != '}' {
			k, err := dec.ReadToken()
			if err != nil {
				return err
			}
			// A token is valid until the next read, so the name is taken now.
			name := validUTF8(k.String())
			var v bytes.Buffer
			if err := writeCanonical(dec, &v); err != nil {
				return err
			}
			members[name] = v.Bytes()
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		names := slices.Sorted(maps.Keys(members))
		out.WriteByte('{')
		for i, name := range names {
			if i > 0 {
				out.WriteByte(',')
			}
			q, err := quote(name)
			if err != nil {
				return err
			}
			out.Write(q)
			out.WriteByte(':')
			out.Write(members[name])
		}
		out.WriteByte('}')
	case '[':
		out.WriteByte('[')
		for first := true; dec.PeekKind() != ']'; first = false {
			if !first {
				out.WriteByte(',')
			}
			if err := writeCanonical(dec, out); err != nil {
				return err
			}
		}
		if _, err := dec.ReadToken(); err != nil {
			return err
		}
		out.WriteByte(']')
	case '"':
		q, err := quote(validUTF8(tok.String()))
		if err != nil {
			return err
		}
		out.Write(q)
	default:
		// A number keeps the digits it was written with.
		out.WriteString(tok.String())
	}
	return nil
}

// validUTF8 replaces each byte that is not UTF-8 with U+FFFD.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string([]rune(s))
}

// quote writes a string as a JSON string with <, > and & kept as they are;
// U+2028 and U+2029 are escaped.
func quote(s string) ([]byte, error) {
	return json.Marshal(s, jsontext.EscapeForJS(true))
}

// DigestJSON is the digest of a JSON document: "sha256:" and the hex of
// the SHA-256 of its canonical JSON.
func DigestJSON(doc []byte) (string, error) {
	c, err := Canonical(doc)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return DigestPrefix + hex.EncodeToString(sum[:]), nil
}

// RenderOptions render a value whose JSON a digest is taken over as it has
// always been rendered: members of a map in order, a nil slice or map as
// null, and bytes that are not UTF-8 as U+FFFD. Another rendering would
// change the digest of an object already pinned.
var RenderOptions = json.JoinOptions(
	json.Deterministic(true),
	json.FormatNilSliceAsNull(true),
	json.FormatNilMapAsNull(true),
	jsontext.AllowInvalidUTF8(true),
)

// Digest is the digest of a value as its JSON tags render it.
func Digest(v any) (string, error) {
	doc, err := json.Marshal(v, RenderOptions)
	if err != nil {
		return "", fmt.Errorf("id: rendering the value: %w", err)
	}
	return DigestJSON(doc)
}

// ValidDigest reports whether s is a digest: "sha256:" and 64 characters
// of lowercase hex.
func ValidDigest(s string) bool {
	hexPart, ok := strings.CutPrefix(s, DigestPrefix)
	if !ok || len(hexPart) != sha256.Size*2 {
		return false
	}
	for i := range len(hexPart) {
		if !strings.ContainsRune("0123456789abcdef", rune(hexPart[i])) {
			return false
		}
	}
	return true
}

// FolderFile is one file of a folder as its digest reads it: its path
// relative to the folder and the hex SHA-256 of its bytes.
type FolderFile struct {
	Path   string
	SHA256 string
}

// FolderDigest is the digest of a folder's content
// (specs/007-measurements-and-evidence.md): the SHA-256 of one line
// "<hex sha256>  <path>\n" per file, in ascending bytewise order of path,
// the form sha256sum prints. Two folders holding the same bytes at the
// same paths have one digest, whatever store holds them.
func FolderDigest(files []FolderFile) string {
	sorted := slices.Clone(files)
	slices.SortFunc(sorted, func(a, b FolderFile) int { return strings.Compare(a.Path, b.Path) })
	h := sha256.New()
	for _, f := range sorted {
		_, _ = io.WriteString(h, f.SHA256+"  "+f.Path+"\n")
	}
	return DigestPrefix + hex.EncodeToString(h.Sum(nil))
}
