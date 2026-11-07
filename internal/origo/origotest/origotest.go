// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package origotest is a stub of Origo's read API for tests: repositories
// by owner and slug, a commit's tree and its blobs, answered as Origo
// answers them.
package origotest

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// Stub is one Origo with one repository, whose files are at one commit.
type Stub struct {
	*httptest.Server
	mu sync.Mutex
	// Repository is owner/slug, Commit the full commit id the files are at.
	Repository, Commit string
	// Token is the bearer token the stub accepts.
	Token string
	files map[string][]byte
	// Calls are the paths of the requests answered, and Tokens the bearer
	// tokens they carried.
	Calls, Tokens []string
	// Status, when not zero, answers every request with it.
	Status int
}

// New starts a stub holding repository at commit, which accepts token.
func New(t *testing.T, repository, commit, token string) *Stub {
	t.Helper()
	s := &Stub{Repository: repository, Commit: commit, Token: token, files: map[string][]byte{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Put adds a file at the commit.
func (s *Stub) Put(path string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = data
}

// Oid is the object id git gives a blob of data.
func Oid(data []byte) string {
	h := sha1.New() //nolint:gosec // git's own object id
	_, _ = io.WriteString(h, "blob "+strconv.Itoa(len(data))+"\x00")
	_, _ = h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Stub) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Calls = append(s.Calls, r.URL.Path)
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.Tokens = append(s.Tokens, got)
	refuse := func(status int, code string) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"m"}}`)
	}
	switch {
	case s.Status != 0:
		w.WriteHeader(s.Status)
	case got != s.Token:
		refuse(http.StatusUnauthorized, "unauthenticated")
	case r.URL.Path == "/v1/repos":
		if r.URL.Query().Get("owner")+"/"+r.URL.Query().Get("slug") != s.Repository {
			refuse(http.StatusNotFound, "repo_not_found")
			return
		}
		_ = json.MarshalWrite(w, map[string]any{"id": "repo_1", "owner": r.URL.Query().Get("owner"), "slug": r.URL.Query().Get("slug")})
	case r.URL.Path == "/v1/repos/repo_1/tree/"+s.Commit:
		dir := r.URL.Query().Get("path")
		type entry struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		}
		entries := []entry{}
		for p, b := range s.files {
			if d := p[:max(strings.LastIndex(p, "/"), 0)]; d == dir {
				entries = append(entries, entry{Path: p, Mode: "100644", Type: "blob", SHA: Oid(b), Size: int64(len(b))})
			}
		}
		_ = json.MarshalWrite(w, map[string]any{"entries": entries, "next_cursor": nil})
	case strings.HasPrefix(r.URL.Path, "/v1/repos/repo_1/blob/"):
		oid := strings.TrimPrefix(r.URL.Path, "/v1/repos/repo_1/blob/")
		for _, b := range s.files {
			if Oid(b) == oid {
				_, _ = w.Write(b)
				return
			}
		}
		refuse(http.StatusNotFound, "object_not_found")
	default:
		refuse(http.StatusNotFound, "not_found")
	}
}
