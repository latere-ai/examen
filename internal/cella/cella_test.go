// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package cella

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestTheClient: a create asks for no network, the name, labels, image,
// environment and time to live given, and holds the answer; a write sends
// the tar below its directory; a run sends the command and reads its
// result; a delete of one gone is done; every call carries the token.
func TestTheClient(t *testing.T) {
	var got []string
	var create map[string]any
	var exec map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		got = append(got, r.Method+" "+r.URL.RequestURI())
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/environments/environments/default":
			_, _ = io.WriteString(w, `{"apiVersion":"cella.example/v9","kind":"Environment"}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/environments/sandboxes":
			create = nil
			_ = json.Unmarshal(body, &create)
			_, _ = io.WriteString(w, `{"spec":{"image":"base@sha256:aa","workspace":{"path":"/workspace"}},"status":{"id":"sbx_1","phase":"Running"}}`)
		case r.Method == http.MethodGet:
			_, _ = io.WriteString(w, `{"spec":{"image":"base@sha256:aa"},"status":{"id":"sbx_1","phase":"Failed","reason":"Exited"}}`)
		case r.Method == http.MethodPut:
			if r.Header.Get("Content-Type") != "application/x-tar" || string(body) != "tar" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/exec"):
			_ = json.Unmarshal(body, &exec)
			_, _ = io.WriteString(w, `{"exitCode":3,"stdout":"out","stderr":"err","truncated":true,"durationMs":1500}`)
		case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/gone"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"not_found"}}`)
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()
	c := New(srv.URL+"/v1/environments/", srv.Client())
	ctx := t.Context()
	sb, err := c.Create(ctx, "tok", Spec{Name: "chk", Labels: map[string]string{"a": "b"}, Image: "base", Environment: "default", TTL: time.Hour}, time.Minute)
	if err != nil || sb != (Sandbox{ID: "sbx_1", Image: "base@sha256:aa", Workspace: "/workspace", Phase: PhaseRunning}) {
		t.Fatalf("create: %+v %v", sb, err)
	}
	if create["apiVersion"] != "cella.example/v9" {
		t.Fatalf("the manifest's version is not the server's: %v", create["apiVersion"])
	}
	first := create
	if _, err := c.Create(ctx, "tok", Spec{Name: "again", Environment: "default", TTL: time.Hour}, time.Minute); err != nil || got[len(got)-1] != "POST /v1/environments/sandboxes?wait=1&timeout=1m0s" {
		t.Fatalf("a second create read the version again: %v %v", got, err)
	}
	got, create = append(got[:2], got[3:]...), first
	spec := create["spec"].(map[string]any)
	if spec["network"].(map[string]any)["egress"].(map[string]any)["mode"] != "none" || spec["image"] != "base" || spec["environment"] != "default" ||
		spec["lifecycle"].(map[string]any)["ttl"] != "1h0m0s" || create["metadata"].(map[string]any)["labels"].(map[string]any)["a"] != "b" {
		t.Fatalf("the manifest: %v", create)
	}
	if sb, err := c.Get(ctx, "tok", "sbx_1"); err != nil || sb.Phase != PhaseFailed || sb.Reason != "Exited" || sb.Workspace != "/workspace" {
		t.Fatalf("get: %+v %v", sb, err)
	}
	if err := c.PutTar(ctx, "tok", "sbx_1", "/workspace", []byte("tar")); err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(ctx, "tok", "sbx_1", Exec{Command: []string{"sh", "-c", "x"}, Env: map[string]string{"K": "v"}, Workdir: "/workspace/work", Timeout: time.Minute})
	if err != nil || res != (Result{ExitCode: 3, Stdout: "out", Stderr: "err", Truncated: true, Duration: 1500}) {
		t.Fatalf("run: %+v %v", res, err)
	}
	if exec["timeout"] != "1m0s" || exec["workdir"] != "/workspace/work" || exec["env"].(map[string]any)["K"] != "v" {
		t.Fatalf("the command: %v", exec)
	}
	for _, id := range []string{"sbx_1", "gone"} {
		if err := c.Delete(ctx, "tok", id); err != nil {
			t.Fatalf("delete %s: %v", id, err)
		}
	}
	if !strings.Contains(got[1], "wait=1") || !strings.Contains(got[3], "dest=%2Fworkspace") {
		t.Fatalf("the calls: %v", got)
	}
}

// TestRefusalsAndOutages: a 4xx is a refusal with its code and detail, a
// 5xx, a 429 and no answer are outages, and an answer that does not parse
// is an outage.
func TestRefusalsAndOutages(t *testing.T) {
	status := http.StatusForbidden
	body := `{"error":{"code":"quota_exceeded","details":{"detail":"at the limit"}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	c := New(srv.URL, srv.Client())
	if _, err := c.Create(t.Context(), "tok", Spec{TTL: time.Minute}, time.Second); err == nil {
		t.Fatal("a create with no version read went ahead")
	}
	c.apiVersion = "v"
	_, err := c.Create(t.Context(), "tok", Spec{TTL: time.Minute}, time.Second)
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Code != CodeQuotaExceeded || refused.Status != http.StatusForbidden || !strings.Contains(refused.Error(), "at the limit") {
		t.Fatalf("a refusal: %v", err)
	}
	for _, s := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		status = s
		if _, err := c.Get(t.Context(), "tok", "sbx"); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%d: %v", s, err)
		}
	}
	status, body = http.StatusOK, "not json"
	if _, err := c.Run(t.Context(), "tok", "sbx", Exec{Command: []string{"true"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an answer that does not parse: %v", err)
	}
	srv.Close()
	if err := c.Delete(t.Context(), "tok", "sbx"); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("no answer: %v", err)
	}
	if New("http://example.invalid", nil).HTTP == nil {
		t.Fatal("no default client")
	}
}
