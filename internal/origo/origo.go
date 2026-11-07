// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package origo is the client Examen reads one file of an Origo repository
// with, at a full commit, without a clone (specs/005-execution.md, "Task
// inputs, references and outputs"): the repository by its owner and slug,
// the file's entry in the commit's tree, and the blob by its object id.
// Every call carries an owner token for Origo's audience, which the caller
// obtained for the experiment's owner; the client holds no credential of
// its own.
package origo

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/pkg/otel"
)

// callTimeout bounds one call to Origo.
const callTimeout = 60 * time.Second

// treePages bounds the pages one directory listing reads.
const treePages = 100

// ErrUnavailable is Origo not answering, or answering an outage: the work
// waits and is tried again.
var ErrUnavailable = errors.New("origo: unavailable")

// RefusedError is a refusal of Origo or its authorizer, or a path that
// is no file of the commit: the status and the code of its error envelope.
type RefusedError struct {
	Status int
	Code   string
}

func (e *RefusedError) Error() string {
	return "origo: refused " + strconv.Itoa(e.Status) + " " + e.Code
}

// TooLargeError is a file larger than the read allowed.
type TooLargeError struct {
	Path string
	Size int64
}

func (e *TooLargeError) Error() string {
	return "origo: " + e.Path + " is larger than the read allows"
}

// ChangedError is a blob whose bytes are not the ones their object id
// names.
type ChangedError struct{ Path string }

func (e *ChangedError) Error() string {
	return "origo: " + e.Path + " does not hash to its object id"
}

// Client reaches Origo's API at URL, its root.
type Client struct {
	URL  string
	HTTP *http.Client
}

// New is the client of the installation at base. A nil client is the
// traced default transport.
func New(base string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Transport: otel.Transport(nil)}
	}
	return &Client{URL: strings.TrimRight(base, "/"), HTTP: client}
}

// treeEntry is one entry of a tree listing.
type treeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"`
	SHA  string `json:"sha"`
	Size *int64 `json:"size"`
}

// symlinkMode is the mode of a blob that holds a link's target.
const symlinkMode = "120000"

// Read reads the file at path of the repository, named owner/slug, at the
// full commit, at most limit bytes of it: a larger file is a
// *TooLargeError, never cut short. The bytes are checked against the
// object id the commit's tree names, and a path that is no regular file of
// the commit is a *RefusedError of status 404.
func (c *Client) Read(ctx context.Context, token, repository, commit, file string, limit int64) ([]byte, error) {
	owner, slug, ok := strings.Cut(repository, "/")
	if !ok || owner == "" || slug == "" || strings.Contains(slug, "/") {
		return nil, &RefusedError{Status: http.StatusNotFound, Code: "repo_not_found"}
	}
	var repo struct {
		ID string `json:"id"`
	}
	q := url.Values{"owner": {owner}, "slug": {slug}}
	raw, err := c.read(ctx, token, c.URL+"/v1/repos?"+q.Encode(), 1<<20)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &repo); err != nil || repo.ID == "" {
		return nil, fmt.Errorf("%w: a repository does not parse", ErrUnavailable)
	}
	base := c.URL + "/v1/repos/" + url.PathEscape(repo.ID)
	entry, err := c.find(ctx, token, base+"/tree/"+url.PathEscape(commit), file)
	if err != nil {
		return nil, err
	}
	if entry.Size != nil && *entry.Size > limit {
		return nil, &TooLargeError{Path: file, Size: *entry.Size}
	}
	data, err := c.read(ctx, token, base+"/blob/"+url.PathEscape(entry.SHA), limit)
	var big *TooLargeError
	if errors.As(err, &big) {
		big.Path = file
	}
	if err != nil {
		return nil, err
	}
	if !hashes(entry.SHA, data) {
		return nil, &ChangedError{Path: file}
	}
	return data, nil
}

// find lists the directory of file at the tree URL and answers file's
// entry, which must be a regular blob.
func (c *Client) find(ctx context.Context, token, tree, file string) (treeEntry, error) {
	dir := path.Dir(file)
	if dir == "." {
		dir = ""
	}
	cursor := ""
	for range treePages {
		q := url.Values{}
		if dir != "" {
			q.Set("path", dir)
		}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var page struct {
			Entries    []treeEntry `json:"entries"`
			NextCursor *string     `json:"next_cursor"`
		}
		raw, err := c.read(ctx, token, tree+"?"+q.Encode(), 32<<20)
		if err != nil {
			return treeEntry{}, err
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return treeEntry{}, fmt.Errorf("%w: a tree does not parse", ErrUnavailable)
		}
		for _, e := range page.Entries {
			if e.Path != file {
				continue
			}
			if e.Type != "blob" || e.Mode == symlinkMode || e.SHA == "" {
				return treeEntry{}, &RefusedError{Status: http.StatusNotFound, Code: "not_a_file"}
			}
			return e, nil
		}
		if page.NextCursor == nil || *page.NextCursor == "" || len(page.Entries) == 0 {
			return treeEntry{}, &RefusedError{Status: http.StatusNotFound, Code: "path_not_found"}
		}
		cursor = *page.NextCursor
	}
	return treeEntry{}, fmt.Errorf("%w: a listing of %s holds more than %d pages", ErrUnavailable, dir, treePages)
}

// hashes reports whether data is the blob that the object id names: git's
// hash of "blob", the length, a zero byte and the bytes, with SHA-1 for a
// 40-character id and SHA-256 for a 64-character one. An id of another
// length is not checked here, since the commit that names it is the pin.
func hashes(oid string, data []byte) bool {
	head := "blob " + strconv.Itoa(len(data)) + "\x00"
	switch len(oid) {
	case sha1.Size * 2:
		h := sha1.New() //nolint:gosec // git's own object id, not a security digest
		_, _ = io.WriteString(h, head)
		_, _ = h.Write(data)
		return hex.EncodeToString(h.Sum(nil)) == oid
	case sha256.Size * 2:
		h := sha256.New()
		_, _ = io.WriteString(h, head)
		_, _ = h.Write(data)
		return hex.EncodeToString(h.Sum(nil)) == oid
	}
	return true
}

// read sends one GET with the owner token and reads at most limit bytes of
// a 2xx answer: one that declares or carries more is a *TooLargeError.
func (c *Client) read(ctx context.Context, token, u string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("origo: build the request: %w", scrub(err))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, answerError(resp)
	}
	if resp.ContentLength > limit {
		return nil, &TooLargeError{Size: resp.ContentLength}
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read the answer: %w", ErrUnavailable, scrub(err))
	}
	if int64(len(raw)) > limit {
		return nil, &TooLargeError{Size: int64(len(raw))}
	}
	return raw, nil
}

// answerError is the error of an answer that is no 2xx: an outage for 429
// and 5xx, else a refusal with its envelope's code.
func answerError(resp *http.Response) error {
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return fmt.Errorf("%w: answered %d", ErrUnavailable, resp.StatusCode)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(raw, &e)
	return &RefusedError{Status: resp.StatusCode, Code: e.Error.Code}
}

// scrub is err without the request's address.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Err != nil {
		return op.Err
	}
	return err
}
