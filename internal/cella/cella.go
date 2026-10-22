// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package cella is the client Examen runs check sandboxes with
// (specs/005-execution.md, "The check sandbox"): it creates a sandbox with
// no network, writes files into it, runs a command in it and reads what
// the command printed, and deletes it. Examen runs no check code itself:
// Cella runs each command. Every call carries an owner token for Cella's
// audience, which the caller obtained for the experiment's owner; the
// client holds no credential of its own.
package cella

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
	"sync"
	"time"

	"latere.ai/x/pkg/otel"
)

// The phases of a sandbox the client reads.
const (
	PhaseRunning = "Running"
	PhaseFailed  = "Failed"
	PhaseLost    = "Lost"
	PhaseStopped = "Stopped"
)

// CodeQuotaExceeded is Cella's refusal of a create past the owner's
// sandbox limit: the check sandbox waits for a place, as a run does.
const CodeQuotaExceeded = "quota_exceeded"

// CodeNameTaken is Cella's refusal of a create under a name the owner
// already holds.
const CodeNameTaken = "conflict"

// callTimeout bounds a call other than a held create and an exec, which
// carry their own bound.
const callTimeout = 60 * time.Second

// answerLimit bounds one answer: an exec's two channels at a mebibyte
// each, escaped.
const answerLimit = 16 << 20

// ErrUnavailable is Cella not answering, or answering an outage: the work
// waits and is tried again.
var ErrUnavailable = errors.New("cella: unavailable")

// RefusedError is a refusal of Cella or its authorizer: the status, the
// code of its error envelope and its developer detail.
type RefusedError struct {
	Status int
	Code   string
	Detail string
}

func (e *RefusedError) Error() string {
	s := "cella: refused " + strconv.Itoa(e.Status) + " " + e.Code
	if e.Detail != "" {
		s += ": " + e.Detail
	}
	return s
}

// Client reaches Cella's API at URL, its root with its base path.
type Client struct {
	URL  string
	HTTP *http.Client

	mu sync.Mutex
	// apiVersion is the manifest version the server answers its own
	// objects with, read once from the environment a sandbox runs on.
	apiVersion string
}

// New is the client of the API at base. A nil client is the traced default
// transport.
func New(base string, client *http.Client) *Client {
	if client == nil {
		client = &http.Client{Transport: otel.Transport(nil)}
	}
	return &Client{URL: strings.TrimRight(base, "/"), HTTP: client}
}

// Spec is what a check sandbox asks of Cella: its image, empty for the
// installation's default, its environment, empty for the default one, no
// network, and how long it may live.
type Spec struct {
	Name        string
	Labels      map[string]string
	Image       string
	Environment string
	TTL         time.Duration
}

// Sandbox is what the client reads of a sandbox: its id, the image it
// runs, its workspace, its phase and why.
type Sandbox struct {
	ID        string
	Image     string
	Workspace string
	Phase     string
	Reason    string
}

// wire is a sandbox as Cella answers it.
type wire struct {
	Spec struct {
		Image     string `json:"image"`
		Workspace struct {
			Path string `json:"path"`
		} `json:"workspace"`
	} `json:"spec"`
	Status struct {
		ID     string `json:"id"`
		Phase  string `json:"phase"`
		Reason string `json:"reason"`
	} `json:"status"`
}

func (w wire) sandbox() Sandbox {
	ws := w.Spec.Workspace.Path
	if ws == "" {
		ws = "/workspace"
	}
	return Sandbox{ID: w.Status.ID, Image: w.Spec.Image, Workspace: ws, Phase: w.Status.Phase, Reason: w.Status.Reason}
}

// version is the manifest version the server writes its own objects
// under, read from the environment a sandbox runs on, so a manifest names
// the version the server it is sent to serves.
func (c *Client) version(ctx context.Context, token, environment string) (string, error) {
	c.mu.Lock()
	v := c.apiVersion
	c.mu.Unlock()
	if v != "" {
		return v, nil
	}
	if environment == "" {
		environment = "default"
	}
	var env struct {
		APIVersion string `json:"apiVersion"`
	}
	if err := c.call(ctx, token, http.MethodGet, "/environments/"+url.PathEscape(environment), "", nil, callTimeout, &env); err != nil {
		return "", err
	}
	if env.APIVersion == "" {
		return "", fmt.Errorf("%w: the environment names no apiVersion", ErrUnavailable)
	}
	c.mu.Lock()
	c.apiVersion = env.APIVersion
	c.mu.Unlock()
	return env.APIVersion, nil
}

// Create creates a sandbox with egress none and holds the answer until it
// left its start, at most hold.
func (c *Client) Create(ctx context.Context, token string, s Spec, hold time.Duration) (Sandbox, error) {
	apiVersion, err := c.version(ctx, token, s.Environment)
	if err != nil {
		return Sandbox{}, err
	}
	spec := map[string]any{
		"network":   map[string]any{"egress": map[string]any{"mode": "none"}},
		"lifecycle": map[string]any{"ttl": s.TTL.String()},
	}
	if s.Image != "" {
		spec["image"] = s.Image
	}
	if s.Environment != "" {
		spec["environment"] = s.Environment
	}
	meta := map[string]any{"name": s.Name}
	if len(s.Labels) > 0 {
		meta["labels"] = s.Labels
	}
	body := map[string]any{"apiVersion": apiVersion, "kind": "Sandbox", "metadata": meta, "spec": spec}
	raw, err := json.Marshal(body)
	if err != nil {
		return Sandbox{}, fmt.Errorf("cella: encode the manifest: %w", err)
	}
	var w wire
	path := "/sandboxes?wait=1&timeout=" + url.QueryEscape(hold.String())
	err = c.call(ctx, token, http.MethodPost, path, "application/json", raw, hold+callTimeout, &w)
	return w.sandbox(), err
}

// Get reads a sandbox by its id or its name.
func (c *Client) Get(ctx context.Context, token, ref string) (Sandbox, error) {
	var w wire
	err := c.call(ctx, token, http.MethodGet, "/sandboxes/"+url.PathEscape(ref), "", nil, callTimeout, &w)
	return w.sandbox(), err
}

// Delete deletes a sandbox; one already gone is deleted.
func (c *Client) Delete(ctx context.Context, token, ref string) error {
	err := c.call(ctx, token, http.MethodDelete, "/sandboxes/"+url.PathEscape(ref), "", nil, callTimeout, nil)
	var refused *RefusedError
	if errors.As(err, &refused) && refused.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// PutTar extracts a tar stream below dest in the sandbox, sent as it is
// read from tar with no length declared, so a caller that writes the
// stream as it reads its files holds none of them whole. size is about how
// many bytes the stream carries, a mebibyte of which adds a second to the
// call's bound.
func (c *Client) PutTar(ctx context.Context, token, id, dest string, tar io.Reader, size int64) error {
	path := "/sandboxes/" + url.PathEscape(id) + "/files?dest=" + url.QueryEscape(dest)
	return c.send(ctx, token, http.MethodPut, path, "application/x-tar", tar, callTimeout+time.Duration(size>>20)*time.Second, nil)
}

// Exec is one command to run: its argv, its environment, its working
// directory and its bound.
type Exec struct {
	Command []string
	Env     map[string]string
	Workdir string
	Timeout time.Duration
}

// Result is what a command did: its exit code, what it printed on each
// channel, at most a mebibyte each with the head kept, whether either was
// cut, and its wall time.
type Result struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Duration  int64  `json:"durationMs"`
}

// Run runs one command and answers once it ended. A command its bound
// stopped answers exit code 124 with a duration at the bound.
func (c *Client) Run(ctx context.Context, token, id string, e Exec) (Result, error) {
	body := map[string]any{"command": e.Command}
	if len(e.Env) > 0 {
		body["env"] = e.Env
	}
	if e.Workdir != "" {
		body["workdir"] = e.Workdir
	}
	if e.Timeout > 0 {
		body["timeout"] = e.Timeout.String()
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return Result{}, fmt.Errorf("cella: encode the command: %w", err)
	}
	var r Result
	err = c.call(ctx, token, http.MethodPost, "/sandboxes/"+url.PathEscape(id)+"/exec?wait=1", "application/json", raw, e.Timeout+callTimeout, &r)
	return r, err
}

// call sends one request with the owner token and decodes a 2xx answer
// into out. The token, the address and the body are in no error.
func (c *Client) call(ctx context.Context, token, method, path, contentType string, body []byte, bound time.Duration, out any) error {
	return c.send(ctx, token, method, path, contentType, bytes.NewReader(body), bound, out)
}

// send is call with a body read as it is sent: a *bytes.Reader declares
// its length, and any other reader is sent chunked.
func (c *Client) send(ctx context.Context, token, method, path, contentType string, body io.Reader, bound time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, bound)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, c.URL+path, body)
	if err != nil {
		return fmt.Errorf("cella: build the request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
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
			Code    string `json:"code"`
			Details struct {
				Detail string `json:"detail"`
			} `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	return &RefusedError{Status: resp.StatusCode, Code: e.Error.Code, Detail: e.Error.Details.Detail}
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
