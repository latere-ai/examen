// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package origo

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// blobOid is git's object id for data.
func blobOid(data string) string { return oidOf(data) }

func oidOf(data string) string {
	h := sha1.New()
	_, _ = io.WriteString(h, "blob "+itoa(len(data))+"\x00"+data)
	return hex.EncodeToString(h.Sum(nil))
}

// TestRead: a file is read by its repository's owner and slug, its
// directory's tree at the commit and its blob by object id, each call with
// the owner token; a tree is paged by its cursor; the bytes are checked
// against the object id.
func TestRead(t *testing.T) {
	body := "expected answer\n"
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/v1/repos":
			if r.URL.Query().Get("owner") != "ada" || r.URL.Query().Get("slug") != "gold" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = io.WriteString(w, `{"error":{"code":"repo_not_found"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":"repo_9"}`)
		case r.URL.Path == "/v1/repos/repo_9/tree/"+strings.Repeat("c", 40) && r.URL.Query().Get("cursor") == "":
			_, _ = io.WriteString(w, `{"entries":[{"path":"ref/a.txt","mode":"100644","type":"blob","sha":"`+strings.Repeat("a", 40)+`","size":1}],"next_cursor":"ref/a.txt"}`)
		case r.URL.Path == "/v1/repos/repo_9/tree/"+strings.Repeat("c", 40):
			_ = json.MarshalWrite(w, map[string]any{"entries": []map[string]any{
				{"path": "ref/answer.txt", "mode": "100644", "type": "blob", "sha": blobOid(body), "size": len(body)},
				{"path": "ref/link", "mode": "120000", "type": "blob", "sha": blobOid("x"), "size": 1},
				{"path": "ref/sub", "mode": "040000", "type": "tree", "sha": blobOid("t"), "size": nil},
				{"path": "ref/forged.txt", "mode": "100644", "type": "blob", "sha": blobOid("real"), "size": 4},
			}, "next_cursor": nil})
		case r.URL.Path == "/v1/repos/repo_9/blob/"+blobOid(body):
			_, _ = io.WriteString(w, body)
		case r.URL.Path == "/v1/repos/repo_9/blob/"+blobOid("real"):
			_, _ = io.WriteString(w, "fake")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL+"/", srv.Client())
	commit := strings.Repeat("c", 40)

	got, err := c.Read(t.Context(), "tok", "ada/gold", commit, "ref/answer.txt", 100)
	if err != nil || string(got) != body {
		t.Fatalf("read: %q %v", got, err)
	}
	want := []string{"/v1/repos?owner=ada&slug=gold", "/v1/repos/repo_9/tree/" + commit + "?path=ref", "/v1/repos/repo_9/tree/" + commit + "?cursor=ref%2Fa.txt&path=ref"}
	if len(calls) != 4 || calls[0] != want[0] || calls[1] != want[1] || calls[2] != want[2] {
		t.Fatalf("calls %v", calls)
	}

	var big *TooLargeError
	if _, err := c.Read(t.Context(), "tok", "ada/gold", commit, "ref/answer.txt", 4); !errors.As(err, &big) || big.Path != "ref/answer.txt" || big.Error() == "" {
		t.Fatalf("a file past the limit: %v", err)
	}
	var refused *RefusedError
	for _, f := range []string{"ref/missing.txt", "ref/link", "ref/sub"} {
		if _, err := c.Read(t.Context(), "tok", "ada/gold", commit, f, 100); !errors.As(err, &refused) || refused.Status != http.StatusNotFound || refused.Error() == "" {
			t.Fatalf("%s: %v", f, err)
		}
	}
	for _, repo := range []string{"ada/other", "ada", "ada/a/b", "/gold"} {
		if _, err := c.Read(t.Context(), "tok", repo, commit, "ref/answer.txt", 100); !errors.As(err, &refused) || refused.Code != "repo_not_found" {
			t.Fatalf("%s: %v", repo, err)
		}
	}
	if _, err := c.Read(t.Context(), "bad", "ada/gold", commit, "ref/answer.txt", 100); !errors.As(err, &refused) || refused.Status != http.StatusUnauthorized {
		t.Fatalf("a token Origo refuses: %v", err)
	}
	var changed *ChangedError
	if _, err := c.Read(t.Context(), "tok", "ada/gold", commit, "ref/forged.txt", 100); !errors.As(err, &changed) || changed.Error() == "" {
		t.Fatalf("bytes that are not the object's: %v", err)
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
		if _, err := c.Read(t.Context(), "tok", "o/r", "c", "f", 1); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%d: %v", s, err)
		}
	}
	status, body = http.StatusOK, "not json"
	if _, err := c.Read(t.Context(), "tok", "o/r", "c", "f", 1); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a repository that does not parse: %v", err)
	}
	srv.Close()
	if _, err := c.Read(t.Context(), "tok", "o/r", "c", "f", 1); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("no answer: %v", err)
	}
	if New("http://example.invalid", nil).HTTP == nil {
		t.Fatal("no default client")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestTheEdgesOfARead: a file at the root with a SHA-256 object id is read
// and checked; an id of another length is not checked; a blob whose tree
// entry gives no size is still held to the limit, whether its answer
// declares its length or not; a tree or a blob that is not answered, a tree
// that does not parse, a listing past its page bound and an answer cut
// short are outages; an address that does not parse is an error that
// names no address.
func TestTheEdgesOfARead(t *testing.T) {
	root, other := "root file\n", "other id\n"
	sum := sha256.Sum256([]byte("blob " + itoa(len(root)) + "\x00" + root))
	rootOid := hex.EncodeToString(sum[:])
	big := strings.Repeat("x", 64)
	commit := strings.Repeat("c", 40)
	tree := "/v1/repos/repo_9/tree/" + commit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("path")
		switch {
		case r.URL.Path == "/v1/repos" && r.URL.Query().Get("slug") == "cut":
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, `{"id":`)
		case r.URL.Path == "/v1/repos":
			_, _ = io.WriteString(w, `{"id":"repo_9"}`)
		case r.URL.Path == tree && dir == "":
			_ = json.MarshalWrite(w, map[string]any{"entries": []map[string]any{
				{"path": "root.txt", "mode": "100644", "type": "blob", "sha": rootOid, "size": len(root)},
				{"path": "other.txt", "mode": "100644", "type": "blob", "sha": "short", "size": len(other)},
				{"path": "sized.txt", "mode": "100644", "type": "blob", "sha": "sized", "size": nil},
				{"path": "streamed.txt", "mode": "100644", "type": "blob", "sha": "streamed", "size": nil},
				{"path": "gone.txt", "mode": "100644", "type": "blob", "sha": "gone", "size": 1},
			}, "next_cursor": nil})
		case r.URL.Path == tree && dir == "down":
			w.WriteHeader(http.StatusBadGateway)
		case r.URL.Path == tree && dir == "garbled":
			_, _ = io.WriteString(w, "not json")
		case r.URL.Path == tree && dir == "endless":
			_, _ = io.WriteString(w, `{"entries":[{"path":"endless/a","type":"blob","sha":"a"}],"next_cursor":"more"}`)
		case r.URL.Path == "/v1/repos/repo_9/blob/"+rootOid:
			_, _ = io.WriteString(w, root)
		case r.URL.Path == "/v1/repos/repo_9/blob/short":
			_, _ = io.WriteString(w, other)
		case r.URL.Path == "/v1/repos/repo_9/blob/sized":
			_, _ = io.WriteString(w, big)
		case r.URL.Path == "/v1/repos/repo_9/blob/streamed":
			_, _ = io.WriteString(w, big[:8])
			w.(http.Flusher).Flush()
			_, _ = io.WriteString(w, big[8:])
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, srv.Client())

	if got, err := c.Read(t.Context(), "tok", "ada/gold", commit, "root.txt", 100); err != nil || string(got) != root {
		t.Fatalf("a root file with a SHA-256 id: %q %v", got, err)
	}
	if got, err := c.Read(t.Context(), "tok", "ada/gold", commit, "other.txt", 100); err != nil || string(got) != other {
		t.Fatalf("an id of another length: %q %v", got, err)
	}
	for _, f := range []string{"sized.txt", "streamed.txt"} {
		var tooLarge *TooLargeError
		if _, err := c.Read(t.Context(), "tok", "ada/gold", commit, f, 16); !errors.As(err, &tooLarge) || tooLarge.Path != f {
			t.Fatalf("%s past the limit: %v", f, err)
		}
	}
	for _, f := range []string{"gone.txt", "down/a", "garbled/a", "endless/b"} {
		if _, err := c.Read(t.Context(), "tok", "ada/gold", commit, f, 100); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%s: %v", f, err)
		}
	}
	if _, err := c.Read(t.Context(), "tok", "ada/cut", commit, "root.txt", 100); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an answer cut short: %v", err)
	}
	bad := New("http://origo.example\x7f", srv.Client())
	if _, err := bad.Read(t.Context(), "tok", "ada/gold", commit, "root.txt", 100); err == nil || errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "origo.example") {
		t.Fatalf("an address that does not parse: %v", err)
	}
}
