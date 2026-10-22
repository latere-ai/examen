// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package arca

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestListAndRead: a listing is read deep, a page at a time, under the
// owner's escaped segment; a read answers the bytes up to its limit, and a
// larger object is too large, never cut short.
func TestListAndRead(t *testing.T) {
	owner := "https://issuer.example|ada"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.HasPrefix(r.URL.EscapedPath(), "/v1/storage/files/"+url.PathEscape(owner)+"/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found"}}`)
			return
		}
		switch {
		case r.URL.Query().Get("list") == "1" && r.URL.Query().Get("cursor") == "":
			_, _ = io.WriteString(w, `{"entries":[{"path":"files/w/a.py","size":2,"checksum":"aa","checksum_kind":"sha256"}],"next_cursor":"c2"}`)
		case r.URL.Query().Get("list") == "1":
			_, _ = io.WriteString(w, `{"entries":[{"path":"files/w/sub dir/b.txt","size":3,"checksum":"bb","checksum_kind":"sha256"}]}`)
		case strings.HasSuffix(r.URL.Path, "/big.bin"):
			_, _ = io.WriteString(w, strings.Repeat("x", 20))
		default:
			_, _ = io.WriteString(w, "hi")
		}
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1/storage", srv.Client())
	got, err := c.List(t.Context(), "tok", owner, "files/w")
	if err != nil || len(got) != 2 || got[1].Path != "files/w/sub dir/b.txt" || got[0].Checksum != "aa" {
		t.Fatalf("list: %+v %v", got, err)
	}
	if b, err := c.Read(t.Context(), "tok", owner, "files/w/sub dir/b.txt", 10); err != nil || string(b) != "hi" {
		t.Fatalf("read: %q %v", b, err)
	}
	var big *TooLargeError
	if _, err := c.Read(t.Context(), "tok", owner, "files/w/big.bin", 10); !errors.As(err, &big) || big.Path != "files/w/big.bin" || big.Error() == "" {
		t.Fatalf("a large object: %v", err)
	}
	var refused *RefusedError
	if _, err := c.Read(t.Context(), "tok", "https://issuer.example|bob", "files/x", 10); !errors.As(err, &refused) || refused.Status != http.StatusNotFound || refused.Code != "not_found" || refused.Error() == "" {
		t.Fatalf("another space: %v", err)
	}
}

// TestOutages: a 5xx, a 429, an answer that does not parse and no answer
// are outages, and no error names the address.
func TestOutages(t *testing.T) {
	status, body := http.StatusServiceUnavailable, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	c := New(srv.URL, srv.Client())
	for _, s := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		status = s
		if _, err := c.List(t.Context(), "tok", "o", "f"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%d: %v", s, err)
		}
	}
	status, body = http.StatusOK, "not json"
	if _, err := c.List(t.Context(), "tok", "o", "f"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a listing that does not parse: %v", err)
	}
	srv.Close()
	if _, err := c.Read(t.Context(), "tok", "o", "f", 1); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("no answer: %v", err)
	}
	if New("http://example.invalid", nil).HTTP == nil {
		t.Fatal("no default client")
	}
}

// TestOpenStreams: Open answers an object's bytes as a stream; an object
// whose answer declares more than the limit is refused before a byte is
// read, and one that declares nothing is cut off by Read once it passes
// the limit; a body that breaks off is an outage; an address that does
// not parse builds no request.
func TestOpenStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/undeclared"):
			// A flush before the body sends it chunked, with no length.
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, strings.Repeat("u", 20))
		case strings.HasSuffix(r.URL.Path, "/cut"):
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "only ten b")
		case strings.HasSuffix(r.URL.Path, "/declared"):
			_, _ = io.WriteString(w, strings.Repeat("d", 20))
		default:
			_, _ = io.WriteString(w, "streamed")
		}
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())
	body, err := c.Open(t.Context(), "tok", "o", "files/s", 10)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(body)
	if err != nil || string(raw) != "streamed" || body.Close() != nil {
		t.Fatalf("a stream: %q %v", raw, err)
	}
	var big *TooLargeError
	if _, err := c.Open(t.Context(), "tok", "o", "files/declared", 10); !errors.As(err, &big) || big.Path != "files/declared" || big.Size != 20 {
		t.Fatalf("a declared length past the limit: %v", err)
	}
	if _, err := c.Read(t.Context(), "tok", "o", "files/undeclared", 10); !errors.As(err, &big) || big.Size != 11 {
		t.Fatalf("an undeclared length past the limit: %v", err)
	}
	if _, err := c.Read(t.Context(), "tok", "o", "files/cut", 1000); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("a body that broke off: %v", err)
	}
	bad := New("http://exa mple.invalid", srv.Client())
	if _, err := bad.Open(t.Context(), "tok", "o", "f", 1); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("an address that does not parse: %v", err)
	}
}

// TestCreate: a write is a PUT of the whole body under the owner's escaped
// segment, with the owner token, its content type and If-None-Match: *,
// so a path that holds an object already is refused and left as it is.
func TestCreate(t *testing.T) {
	owner := "https://issuer.example|ada"
	held := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method != http.MethodPut || r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("If-None-Match") != "*" || r.Header.Get("Content-Type") != "text/plain; charset=utf-8":
			w.WriteHeader(http.StatusBadRequest)
			return
		case !strings.HasPrefix(r.URL.EscapedPath(), "/v1/storage/files/"+url.PathEscape(owner)+"/"):
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":"forbidden"}}`)
			return
		}
		if _, ok := held[r.URL.Path]; ok {
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, `{"error":{"code":"precondition_failed"}}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		held[r.URL.Path] = string(body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1/storage", srv.Client())
	p := "files/evals/experiments/exp_1/cells/cel_1/trials/tri_1/checks/ab/stdout.txt"
	if err := c.Create(t.Context(), "tok", owner, p, "text/plain; charset=utf-8", []byte("ok\n")); err != nil {
		t.Fatal(err)
	}
	if held["/v1/storage/files/"+owner+"/"+p] != "ok\n" {
		t.Fatalf("held %v", held)
	}
	var refused *RefusedError
	if err := c.Create(t.Context(), "tok", owner, p, "text/plain; charset=utf-8", []byte("again\n")); !errors.As(err, &refused) || refused.Status != http.StatusPreconditionFailed || refused.Code != "precondition_failed" {
		t.Fatalf("a second write of one path: %v", err)
	}
	if err := c.Create(t.Context(), "tok", "https://issuer.example|bob", p, "text/plain; charset=utf-8", []byte("x")); !errors.As(err, &refused) || refused.Status != http.StatusForbidden {
		t.Fatalf("another space: %v", err)
	}
}
