// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package smoke

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// stub serves the one public route of examend below its base path: the
// contract. A nil handler leaves the base path routing nothing.
func stub(t *testing.T, contract http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	if contract != nil {
		mux.HandleFunc("GET /v1/evals/openapi.yaml", contract)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func healthy(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write([]byte("openapi: 3.1.0\ninfo:\n  title: Examen\n  version: \"1\"\n"))
}

// script is the path to release.sh, found from this file rather than from
// the working directory, which `go test` sets to the package.
func script(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Join(filepath.Dir(file), "release.sh")
}

// run executes the script and returns its combined output and whether it
// exited zero. It skips when the tools the script needs are not on PATH,
// as in the gate's hermetic run, which holds only the Go toolchain.
func run(t *testing.T, env ...string) (string, bool) {
	t.Helper()
	for _, cmd := range []string{"bash", "curl", "grep", "mktemp"} {
		if _, err := exec.LookPath(cmd); err != nil {
			t.Skipf("%s is not on PATH, so release.sh cannot run here", cmd)
		}
	}
	cmd := exec.CommandContext(context.Background(), "bash", script(t))
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func TestSmokePassesAndWritesEvidence(t *testing.T) {
	srv := stub(t, healthy)
	evidence := filepath.Join(t.TempDir(), "smoke.md")
	out, ok := run(t, "BASE_URL="+srv.URL+"/v1/evals", "TAG=v1.2.3", "COMMIT=abc1234", "OUTPUT_MD="+evidence)
	if !ok {
		t.Fatalf("the smoke failed against a healthy service:\n%s", out)
	}
	md, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Release evidence", "- Tag: `v1.2.3`", "- Commit: `abc1234`", "Version: not checked"} {
		if !strings.Contains(string(md), want) {
			t.Errorf("the evidence lacks %q:\n%s", want, md)
		}
	}
}

func TestSmokeFailsWhenTheRouteIsNotServed(t *testing.T) {
	srv := stub(t, nil)
	out, ok := run(t, "BASE_URL="+srv.URL+"/v1/evals", "TAG=v1.2.3")
	if ok {
		t.Fatalf("the smoke passed against a service that routes nothing:\n%s", out)
	}
	if !strings.Contains(out, "GET /openapi.yaml: expected 200, got 404") {
		t.Errorf("the failure does not name the route:\n%s", out)
	}
}

func TestSmokeFailsOnADocumentThatIsNotOpenAPI(t *testing.T) {
	srv := stub(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>\n")) })
	out, ok := run(t, "BASE_URL="+srv.URL+"/v1/evals")
	if ok {
		t.Fatalf("the smoke passed against a page:\n%s", out)
	}
	if !strings.Contains(out, "not an OpenAPI document") {
		t.Errorf("the failure does not say what is wrong:\n%s", out)
	}
}

func TestSmokeFailsWhenTheServiceIsDown(t *testing.T) {
	srv := stub(t, healthy)
	url := srv.URL
	srv.Close()
	out, ok := run(t, "BASE_URL="+url+"/v1/evals")
	if ok {
		t.Fatalf("the smoke passed against a service that is down:\n%s", out)
	}
	if !strings.Contains(out, "curl failed") {
		t.Errorf("the failure does not say the service is unreachable:\n%s", out)
	}
}

func TestSmokeRefusesWithNoTarget(t *testing.T) {
	out, ok := run(t, "BASE_URL=")
	if ok || !strings.Contains(out, "BASE_URL is required") {
		t.Fatalf("expected a refusal naming BASE_URL (ok=%v):\n%s", ok, out)
	}
}
