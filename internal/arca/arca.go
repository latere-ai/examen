// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package arca is the client Examen reads an owner's files store with
// (specs/005-execution.md, specs/007-measurements-and-evidence.md): the
// files a run left in its session's folder, and a check grader's files.
// Every call carries an owner token for Arca's audience, which the caller
// obtained for the experiment's owner; the client holds no credential of
// its own, and reads only.
package arca

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"latere.ai/x/pkg/otel"
)

// callTimeout bounds one call to Arca.
const callTimeout = 60 * time.Second

// listLimit is how many entries one page of a listing asks for.
const listLimit = 500

// listPages bounds the pages one listing reads.
const listPages = 100

// ErrUnavailable is Arca not answering, or answering an outage: the work
// waits and is tried again.
var ErrUnavailable = errors.New("arca: unavailable")

// RefusedError is a refusal of Arca or its authorizer: the status and the
// code of its error envelope.
type RefusedError struct {
	Status int
	Code   string
}

func (e *RefusedError) Error() string {
	return "arca: refused " + strconv.Itoa(e.Status) + " " + e.Code
}

// TooLargeError is an object larger than the read allowed.
type TooLargeError struct {
	Path string
	Size int64
}

func (e *TooLargeError) Error() string {
	return "arca: " + e.Path + " is larger than the read allows"
}

// Client reaches Arca's API at URL, its root with its base path.
type Client struct {
	URL  string
	HTTP *http.Client
}

// New is the client of the API at base. A nil client is the traced default
// transport.
func New(base string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Transport: otel.Transport(nil)}
	}
	return &Client{URL: strings.TrimRight(base, "/"), HTTP: client}
}

// Entry is one object of a listing: its path in the owner's space, its
// size, and its checksum with what kind of checksum it is, sha256 for an
// object Arca streamed.
type Entry struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Checksum     string `json:"checksum"`
	ChecksumKind string `json:"checksum_kind"`
}

// object is an owner's path as one route segment for the owner and the
// path's own segments.
func (c *Client) object(owner, path string) string {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return c.URL + "/files/" + url.PathEscape(owner) + "/" + strings.Join(segs, "/")
}

// List answers every object under a folder of the owner's space, deep, in
// the order Arca lists them.
func (c *Client) List(ctx context.Context, token, owner, folder string) ([]Entry, error) {
	var out []Entry
	cursor := ""
	for range listPages {
		u := c.object(owner, folder) + "?list=1&limit=" + strconv.Itoa(listLimit)
		if cursor != "" {
			u += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Entries    []Entry `json:"entries"`
			NextCursor string  `json:"next_cursor"`
		}
		raw, err := c.get(ctx, token, u, 32<<20)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, fmt.Errorf("%w: a listing does not parse", ErrUnavailable)
		}
		out = append(out, page.Entries...)
		if page.NextCursor == "" || len(page.Entries) == 0 {
			return out, nil
		}
		cursor = page.NextCursor
	}
	return nil, fmt.Errorf("%w: a listing of %s holds more than %d pages", ErrUnavailable, folder, listPages)
}

// Read reads one object's bytes, at most limit of them: a larger object is
// a *TooLargeError, never cut short.
func (c *Client) Read(ctx context.Context, token, owner, path string, limit int64) ([]byte, error) {
	raw, err := c.get(ctx, token, c.object(owner, path), limit)
	var big *TooLargeError
	if errors.As(err, &big) {
		big.Path = path
	}
	return raw, err
}

// get sends one GET with the owner token and reads at most limit bytes of
// a 2xx answer. The token and the address are in no error.
func (c *Client) get(ctx context.Context, token, u string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("arca: build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if resp.ContentLength > limit {
			return nil, &TooLargeError{Size: resp.ContentLength}
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return nil, fmt.Errorf("%w: read the answer: %w", ErrUnavailable, err)
		}
		if int64(len(raw)) > limit {
			return nil, &TooLargeError{Size: int64(len(raw))}
		}
		return raw, nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return nil, fmt.Errorf("%w: answered %d", ErrUnavailable, resp.StatusCode)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	_ = json.Unmarshal(raw, &e)
	return nil, &RefusedError{Status: resp.StatusCode, Code: e.Error.Code}
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
