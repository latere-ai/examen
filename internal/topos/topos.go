// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package topos is the client the `topos` executor reaches Topos with
// (specs/005-execution.md, "The `topos` executor"): it reads an agent's
// versions, creates one unattended session per trial, reads a session and
// its log, and stops one. Every call carries an owner token for Topos's
// audience, which the caller obtained for the experiment's owner; the
// client holds no credential of its own.
package topos

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
	"strconv"
	"strings"
	"time"

	"latere.ai/x/pkg/otel"
)

// The statuses and stop reasons of a session the executor reads.
const (
	StatusIdle    = "idle"
	StatusRunning = "running"
	StatusEnded   = "ended"

	StopEndTurn          = "end_turn"
	StopToolConfirmation = "tool_confirmation"
	StopQuestion         = "question"
	StopBudget           = "budget"
	StopTurnLimit        = "turn_limit"
	StopOutputLimit      = "output_limit"
	StopInterrupted      = "interrupted"
	StopError            = "error"
	StopMachineWaiting   = "machine_waiting"
	StopCompleted        = "completed"
	StopFailed           = "failed"
	StopCanceled         = "canceled"
	StopExpired          = "expired"
)

// The event types the executor reads of a session's log.
const (
	EventModelRequest   = "model.request"
	EventAgentMessage   = "agent.message"
	EventAgentToolUse   = "agent.tool_use"
	EventSessionStatus  = "session.status"
	EventSessionError   = "session.error"
	EventSessionMachine = "session.machine"
	EventUserMessage    = "user.message"
	EventModelChanged   = "session.model_changed"
)

// callTimeout bounds one call to Topos.
const callTimeout = 30 * time.Second

// answerLimit bounds one answer: a page of events holds at most 500.
const answerLimit = 32 << 20

// eventsPage is how many events one read of a session's log asks for.
const eventsPage = 500

// ErrUnavailable is Topos not answering, or answering an outage: the work
// waits and is tried again.
var ErrUnavailable = errors.New("topos: unavailable")

// RefusedError is a refusal of Topos or its authorizer: the status and
// the code of its error envelope.
type RefusedError struct {
	Status int
	Code   string
}

func (e *RefusedError) Error() string {
	return "topos: refused " + strconv.Itoa(e.Status) + " " + e.Code
}

// Client reaches Topos's API at URL, its root with its base path.
type Client struct {
	URL  string
	HTTP *http.Client
}

// New is the client of the API at base. A nil client is the traced
// default transport.
func New(base string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Transport: otel.Transport(nil)}
	}
	return &Client{URL: strings.TrimRight(base, "/"), HTTP: client}
}

// Model is a session's model: what runs and at which reasoning level.
type Model struct {
	Name      string `json:"name,omitempty"`
	Via       string `json:"via,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// Folder is the folder of the files store a session keeps its files in.
type Folder struct {
	Owner string `json:"owner"`
	Path  string `json:"path"`
}

// Session is what the executor reads of a session.
type Session struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	StopReason string    `json:"stop_reason"`
	LastSeq    uint64    `json:"last_seq"`
	Rollout    string    `json:"rollout"`
	Model      *Model    `json:"model"`
	Folder     *Folder   `json:"folder"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// Event is one event of a session's log.
type Event struct {
	ID      string          `json:"id"`
	Seq     uint64          `json:"seq"`
	Type    string          `json:"type"`
	Time    time.Time       `json:"time"`
	Thread  string          `json:"thread,omitempty"`
	Payload json.RawMessage `json:"payload"`
}

// Repository is a repository a session works in, at a ref.
type Repository struct {
	Type string `json:"type"`
	URL  string `json:"url"`
	Ref  string `json:"ref"`
}

// Limits are a session's turn timeout and maximum age, as durations.
type Limits struct {
	TurnTimeout string `json:"turn_timeout,omitempty"`
	MaxAge      string `json:"max_age,omitempty"`
}

// Create is the body of one trial's session: an agent version, its first
// message, unattended and ending when its one turn ends, as a run of the
// rollout, on the candidate's model.
type Create struct {
	Agent     string            `json:"agent"`
	Message   string            `json:"message,omitempty"`
	Title     string            `json:"title,omitempty"`
	Metadata  map[string]string `json:"metadata,omitempty"`
	EndOnIdle bool              `json:"end_on_idle"`
	Attended  bool              `json:"attended"`
	Rollout   string            `json:"rollout,omitempty"`
	Model     *Model            `json:"model,omitempty"`
	Resources []Repository      `json:"resources,omitempty"`
	Limits    *Limits           `json:"limits,omitempty"`
}

// Version answers the number of the version of agent name whose digest is
// digest; a RefusedError with status 404 when it has none.
func (c *Client) Version(ctx context.Context, token, name, digest string) (int, error) {
	cursor := ""
	for range 100 {
		path := "/agents/" + url.PathEscape(name) + "/versions?limit=500"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Items []struct {
				Version int    `json:"version"`
				Digest  string `json:"digest"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		}
		if err := c.call(ctx, token, http.MethodGet, path, "", nil, &page); err != nil {
			return 0, err
		}
		for _, v := range page.Items {
			if v.Digest == digest {
				return v.Version, nil
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return 0, &RefusedError{Status: http.StatusNotFound, Code: "version_not_found"}
}

// Create creates a session under key, Topos's Idempotency-Key, so a repeat
// with the same body answers the session the first created.
func (c *Client) Create(ctx context.Context, token, key string, body Create) (Session, error) {
	var s Session
	err := c.call(ctx, token, http.MethodPost, "/sessions", key, body, &s)
	return s, err
}

// Get reads a session.
func (c *Client) Get(ctx context.Context, token, id string) (Session, error) {
	var s Session
	err := c.call(ctx, token, http.MethodGet, "/sessions/"+url.PathEscape(id), "", nil, &s)
	return s, err
}

// Events reads a session's whole log, a page at a time.
func (c *Client) Events(ctx context.Context, token, id string) ([]Event, error) {
	var out []Event
	cursor := ""
	for {
		path := "/sessions/" + url.PathEscape(id) + "/events?limit=" + strconv.Itoa(eventsPage)
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Items      []Event `json:"items"`
			NextCursor string  `json:"next_cursor"`
		}
		if err := c.call(ctx, token, http.MethodGet, path, "", nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Items...)
		if page.NextCursor == "" || len(page.Items) == 0 {
			return out, nil
		}
		cursor = page.NextCursor
	}
}

// Interrupt interrupts a running session's turn.
func (c *Client) Interrupt(ctx context.Context, token, id string) error {
	body := map[string]any{"type": "user.interrupt", "payload": map[string]any{}}
	return c.call(ctx, token, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/events", "", body, nil)
}

// End ends an idle session with reason, completed or canceled.
func (c *Client) End(ctx context.Context, token, id, reason string) error {
	return c.call(ctx, token, http.MethodPost, "/sessions/"+url.PathEscape(id)+"/end", "", map[string]string{"reason": reason}, nil)
}

// call sends one request with the owner token and decodes a 2xx answer
// into out. The token, the address and the body are in no error.
func (c *Client) call(ctx context.Context, token, method, path, key string, in, out any) error {
	ctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return fmt.Errorf("topos: encode the request: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("topos: build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrUnavailable, method, scrub(err))
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, answerLimit))
	if err != nil {
		return fmt.Errorf("%w: read the answer: %w", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		if out == nil || len(raw) == 0 {
			return nil
		}
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: the answer does not parse", ErrUnavailable)
		}
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return fmt.Errorf("%w: %s answered %d", ErrUnavailable, method, resp.StatusCode)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
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
