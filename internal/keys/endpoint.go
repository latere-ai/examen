// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/otel"
)

// answerLimit bounds one answer of the key endpoint: a Key is a few hundred
// bytes, and a page of records at most a few hundred kilobytes.
const answerLimit = 8 << 20

// Endpoint is the `endpoint` key source: the installation's key endpoint at
// URL, asked with Bearer. Every call is bounded by authz.Timeout, the retry
// included, and retried once on a connection failure.
type Endpoint struct {
	URL    string
	Bearer func() string
	HTTP   *http.Client
}

// NewEndpoint is the source at base with the bearer the function answers. A
// nil client is the traced default transport with no timeout of its own;
// each call carries authz.Timeout.
func NewEndpoint(base string, bearer func() string, client *http.Client) *Endpoint {
	if client == nil {
		client = &http.Client{Transport: otel.Transport(nil)}
	}
	return &Endpoint{URL: strings.TrimRight(base, "/"), Bearer: bearer, HTTP: client}
}

// leaseBody is what a lease posts (spec 004, "The key endpoint").
type leaseBody struct {
	Owner      string            `json:"owner"`
	Subject    string            `json:"subject"`
	Experiment string            `json:"experiment"`
	Purpose    string            `json:"purpose"`
	Models     []string          `json:"models"`
	Surface    string            `json:"surface,omitempty"`
	Labels     map[string]string `json:"labels"`
	TTL        int64             `json:"ttl,omitempty"`
}

// leaseAnswer is the endpoint's 200 to a lease. expires_at is absent for a
// Key that ends with its experiment.
type leaseAnswer struct {
	Key       string     `json:"key"`
	KeyID     string     `json:"key_id"`
	ExpiresAt *time.Time `json:"expires_at"`
	Remaining *int64     `json:"remaining"`
	Currency  string     `json:"currency"`
}

// Lease asks the endpoint for one Key.
func (e *Endpoint) Lease(ctx context.Context, r Request) (Lease, error) {
	body := leaseBody{
		Owner: r.Owner, Subject: r.Subject, Experiment: r.Experiment, Purpose: r.Purpose,
		Models: r.Models, Surface: r.Surface, Labels: r.Labels, TTL: int64(r.TTL / time.Second),
	}
	if body.Models == nil {
		body.Models = []string{}
	}
	if body.Labels == nil {
		body.Labels = map[string]string{}
	}
	var a leaseAnswer
	if err := e.call(ctx, http.MethodPost, "", body, &a); err != nil {
		return Lease{}, err
	}
	switch {
	case a.Key == "" || a.KeyID == "" || a.Remaining == nil || a.Currency == "":
		return Lease{}, fmt.Errorf("%w: the answer lacks a member", ErrUnavailable)
	case a.ExpiresAt != nil && !a.ExpiresAt.After(time.Now()):
		return Lease{}, fmt.Errorf("%w: the key's expiry is already past", ErrUnavailable)
	}
	l := Lease{Value: a.Key, ID: a.KeyID, Remaining: *a.Remaining, Currency: a.Currency}
	if a.ExpiresAt != nil {
		l.Expires = *a.ExpiresAt
	}
	return l, nil
}

// Release ends one Key. A Key the endpoint no longer holds is released.
func (e *Endpoint) Release(ctx context.Context, keyID string) error {
	err := e.call(ctx, http.MethodDelete, "/"+url.PathEscape(keyID), nil, nil)
	if errors.Is(err, ErrNoKey) {
		return nil
	}
	return err
}

// budgetBody and budgetAnswer are the Budget's ask and answer.
type budgetBody struct {
	Owner      string `json:"owner"`
	Subject    string `json:"subject"`
	Experiment string `json:"experiment"`
	Budget     string `json:"budget"`
}

type budgetAnswer struct {
	Name      string `json:"name"`
	Cap       *int64 `json:"cap"`
	Spent     *int64 `json:"spent"`
	Remaining *int64 `json:"remaining"`
	Currency  string `json:"currency"`
}

// Budget reads an experiment's Budget's figures.
func (e *Endpoint) Budget(ctx context.Context, r Request) (Budget, error) {
	var a budgetAnswer
	if err := e.call(ctx, http.MethodPost, "/budget", budgetBody{Owner: r.Owner, Subject: r.Subject, Experiment: r.Experiment, Budget: r.Budget}, &a); err != nil {
		return Budget{}, err
	}
	if a.Name == "" || a.Cap == nil || a.Spent == nil || a.Remaining == nil || a.Currency == "" {
		return Budget{}, fmt.Errorf("%w: the answer lacks a member", ErrUnavailable)
	}
	return Budget{Name: a.Name, Cap: *a.Cap, Spent: *a.Spent, Remaining: *a.Remaining, Currency: a.Currency}, nil
}

// usageBody and usageAnswer are a page of records' ask and answer.
type usageBody struct {
	Owner      string    `json:"owner"`
	Experiment string    `json:"experiment"`
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Cursor     string    `json:"cursor,omitempty"`
}

type usageAnswer struct {
	Records    []json.RawMessage `json:"records"`
	NextCursor string            `json:"next_cursor"`
}

// Usage reads one page of the records of an experiment's external Key.
func (e *Endpoint) Usage(ctx context.Context, r UsageRequest) (UsagePage, error) {
	var a usageAnswer
	body := usageBody{Owner: r.Owner, Experiment: r.Experiment, From: r.From.UTC(), To: r.To.UTC(), Cursor: r.Cursor}
	if err := e.call(ctx, http.MethodPost, "/usage", body, &a); err != nil {
		return UsagePage{}, err
	}
	if a.Records == nil {
		return UsagePage{}, fmt.Errorf("%w: the answer lacks records", ErrUnavailable)
	}
	page := UsagePage{NextCursor: a.NextCursor, Records: make([]Record, 0, len(a.Records))}
	for i, raw := range a.Records {
		rec, err := ParseRecord(raw)
		if err != nil {
			return UsagePage{}, fmt.Errorf("%w: record %d: %v", ErrUnavailable, i, err)
		}
		page.Records = append(page.Records, rec)
	}
	return page, nil
}

// refusal is the error envelope of a 403 and a 404.
type refusal struct {
	Reason string `json:"reason"`
	Error  struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// call sends one request and decodes a 200 into out. A 204 decodes
// nothing. The bearer, the address and the body are in no error.
func (e *Endpoint) call(ctx context.Context, method, path string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, authz.Timeout)
	defer cancel()
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return fmt.Errorf("keys: encode the request: %w", err)
		}
	}
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, e.URL+path, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("keys: build the request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+e.Bearer())
		req.Header.Set("Accept", "application/json")
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err = e.HTTP.Do(req)
		if err == nil {
			break
		}
		if attempt == 0 && connectionFailure(err) && ctx.Err() == nil {
			continue
		}
		return fmt.Errorf("%w: %s %s: %v", ErrUnavailable, method, path, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, answerLimit))
	if err != nil {
		return fmt.Errorf("%w: read the answer: %v", ErrUnavailable, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		if out == nil {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: the answer does not parse", ErrUnavailable)
		}
		return nil
	case http.StatusNoContent:
		return nil
	case http.StatusPaymentRequired:
		return ErrBudgetExhausted
	case http.StatusForbidden:
		var f refusal
		_ = json.Unmarshal(raw, &f)
		reason := f.Reason
		if reason == "" {
			reason = f.Error.Code
		}
		if reason == "" {
			reason = ReasonKeyRefused
		}
		return &RefusedError{Reason: reason}
	case http.StatusNotFound:
		return ErrNoKey
	}
	return fmt.Errorf("%w: %s %s answered %d", ErrUnavailable, method, path, resp.StatusCode)
}

// connectionFailure reports whether err is a failure to reach the
// endpoint, the one failure retried.
func connectionFailure(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// scrub is err without the request's address: a *url.Error carries the
// URL and a *net.OpError the host and port, and the endpoint's address is
// in no error.
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
