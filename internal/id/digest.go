// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package id

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
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
//   - strings escaped as encoding/json escapes them, with <, > and & kept
//     as they are, so a digest does not depend on a renderer's HTML rule.
//
// A document with anything after its one value, or that is not JSON, has
// no canonical form.

// Canonical is the canonical JSON of a JSON document.
func Canonical(doc []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(doc))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("id: the document is not JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("id: the document holds more than one value")
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("id: encoding the canonical form: %w", err)
	}
	return bytes.TrimSuffix(out.Bytes(), []byte("\n")), nil
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

// Digest is the digest of a value as encoding/json renders it.
func Digest(v any) (string, error) {
	doc, err := json.Marshal(v)
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
