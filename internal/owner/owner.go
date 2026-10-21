// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package owner obtains owner tokens (specs/004-identity-and-authorization.md,
// "Acting as the owner" and "The minter"): a token for one owner, on
// behalf of one subject, addressed to one core, for one piece of an
// experiment's work. Examen sends a core no credential of its own; every
// call it makes for an owner's work carries a token this package obtained.
//
// A token is held in memory alone, used until OWNER_TOKEN_RENEWAL before
// it expires and never longer than OWNER_TOKEN_MAX_LIFETIME after it was
// obtained, and never written to Postgres, an event, a log line or an
// error.
package owner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/otel"

	"latere.ai/x/examen/internal/limits"
)

// The purposes a token is asked for (spec 004).
const (
	PurposeResolve = "resolve"
	PurposeTrial   = "trial"
	PurposeJudge   = "judge"
)

// ReasonSignInNotKept is the minter's reason for a subject whose sign-in
// it keeps none of for this work, and ReasonRefused every other refusal.
const (
	ReasonSignInNotKept = "sign_in_not_kept"
	ReasonRefused       = "owner_token_refused"
)

// ErrUnavailable is a minter that did not answer, or answered what is not
// a token: the work waits and is tried again.
var ErrUnavailable = errors.New("owner: the minter is unavailable")

// RefusedError is a minter's 403: the subject may not be acted for, with
// the reason the cell parks with.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "owner: the minter refused: " + e.Reason }

// Request names one owner token: whose work, on whose behalf, for which
// core, for which experiment and why.
type Request struct {
	Owner      string
	Subject    string
	Audience   string
	Experiment string
	Purpose    string
}

// Source answers owner tokens.
type Source interface {
	Token(ctx context.Context, r Request) (string, error)
}

// Labeler is a source that also answers the labels the installation holds
// an object the token creates at a core to, such as the owner's tenancy
// (spec 004, "The minter"): Examen writes them on a check sandbox it
// creates as the owner. A source that answers none answers an empty map.
type Labeler interface {
	Labels(ctx context.Context, r Request) (map[string]string, error)
}

// answerLimit bounds one answer of the minter: one token.
const answerLimit = 64 << 10

// Minter is the minter at URL, asked with Bearer. Each call is bounded by
// authz.Timeout, its one retry on a connection failure included.
type Minter struct {
	URL    string
	Bearer func() string
	HTTP   *http.Client
	// Now is the clock tokens are kept by; time.Now when nil.
	Now func() time.Time

	mu    sync.Mutex
	cache map[Request]held
}

// held is a token, the labels it came with, and until when it is used.
type held struct {
	token  string
	labels map[string]string
	until  time.Time
}

// NewMinter is the minter at base with the bearer the function answers.
// A nil client is the traced default transport.
func NewMinter(base string, bearer func() string, client *http.Client) *Minter {
	if client == nil {
		client = &http.Client{Transport: otel.Transport(nil)}
	}
	return &Minter{URL: strings.TrimRight(base, "/"), Bearer: bearer, HTTP: client}
}

func (m *Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// mintBody is what one ask posts (spec 004, "The minter").
type mintBody struct {
	Owner    string            `json:"owner"`
	Subject  string            `json:"subject"`
	Audience string            `json:"audience"`
	Claims   map[string]claims `json:"claims"`
	TTL      int64             `json:"ttl"`
}

type claims struct {
	Experiment string `json:"experiment"`
	Purpose    string `json:"purpose"`
}

type mintAnswer struct {
	Token     string            `json:"token"`
	ExpiresIn int64             `json:"expires_in"`
	Labels    map[string]string `json:"labels"`
}

// Token answers a token for r: a held one while it is fresh, else a new
// one from the minter.
func (m *Minter) Token(ctx context.Context, r Request) (string, error) {
	h, err := m.mint(ctx, r)
	return h.token, err
}

// Labels answers the labels the minter gave with r's token, an empty map
// for a minter that gives none.
func (m *Minter) Labels(ctx context.Context, r Request) (map[string]string, error) {
	h, err := m.mint(ctx, r)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	maps.Copy(out, h.labels)
	return out, nil
}

// mint answers a held token for r while it is fresh, else a new one.
func (m *Minter) mint(ctx context.Context, r Request) (held, error) {
	now := m.now()
	m.mu.Lock()
	if h, ok := m.cache[r]; ok && now.Before(h.until) {
		m.mu.Unlock()
		return h, nil
	}
	m.mu.Unlock()
	body := mintBody{
		Owner: r.Owner, Subject: r.Subject, Audience: r.Audience,
		Claims: map[string]claims{"examen": {Experiment: r.Experiment, Purpose: r.Purpose}},
		TTL:    int64(limits.OwnerTokenMaxLifetime / time.Second),
	}
	var a mintAnswer
	if err := m.call(ctx, body, &a); err != nil {
		return held{}, err
	}
	if a.Token == "" || a.ExpiresIn <= 0 {
		return held{}, fmt.Errorf("%w: the answer lacks a token or its lifetime", ErrUnavailable)
	}
	h := held{token: a.Token, labels: a.Labels}
	// Used until OWNER_TOKEN_RENEWAL before it expires, and never past
	// OWNER_TOKEN_MAX_LIFETIME after it was obtained.
	until := now.Add(min(time.Duration(a.ExpiresIn)*time.Second-limits.OwnerTokenRenewal, limits.OwnerTokenMaxLifetime))
	if until.After(now) {
		m.mu.Lock()
		if m.cache == nil {
			m.cache = map[Request]held{}
		}
		for k, h := range m.cache {
			if !now.Before(h.until) {
				delete(m.cache, k)
			}
		}
		h.until = until
		m.cache[r] = h
		m.mu.Unlock()
	}
	return h, nil
}

// call posts one ask and decodes a 200 into out. Neither the bearer, the
// address nor a token is in an error.
func (m *Minter) call(ctx context.Context, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, authz.Timeout)
	defer cancel()
	payload, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("owner: encode the ask: %w", err)
	}
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.URL, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("owner: build the ask: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+m.Bearer())
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		resp, err = m.HTTP.Do(req)
		if err == nil {
			break
		}
		if attempt == 0 && connectionFailure(err) && ctx.Err() == nil {
			continue
		}
		return fmt.Errorf("%w: %w", ErrUnavailable, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, answerLimit))
	if err != nil {
		return fmt.Errorf("%w: read the answer: %w", ErrUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: the answer does not parse", ErrUnavailable)
		}
		return nil
	case http.StatusForbidden:
		var f struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &f)
		if f.Reason == ReasonSignInNotKept {
			return &RefusedError{Reason: ReasonSignInNotKept}
		}
		return &RefusedError{Reason: ReasonRefused}
	}
	return fmt.Errorf("%w: the minter answered %d", ErrUnavailable, resp.StatusCode)
}

// connectionFailure reports whether err is a failure to reach the minter,
// the one failure retried.
func connectionFailure(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// scrub is err without the minter's address.
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
