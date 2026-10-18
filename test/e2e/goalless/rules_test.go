// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package goalless_test

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The converter's rules, on run directories this test writes itself in
// the study's layout; they run without the study.

func write(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func metaFile(backend, model string, exit int, extra, prompt string) string {
	return "# Run Metadata\n\n| Field | Value |\n|-------|-------|\n| Backend | " + backend + " |\n| Model | " + model +
		" |\n| Run | 1 |\n| Exit Code | " + strconv.Itoa(exit) + " |\n| Duration | 100s |\n" + extra +
		"| Timestamp | 2026-09-05T16:37:19Z |\n\n## Prompt\n\n" + prompt + "\n"
}

// A synthetic study: the prompts, and runs that exercise every rule.
func synthetic(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for i, p := range []string{"First line.\nSecond line.", "Two.", "Three.", "Four.", "Five."} {
		write(t, filepath.Join(dir, "prompt"+strconv.Itoa(i+1)+".txt"), p+"\n\n")
	}
	log := func(image, endpoint string) string {
		return ">>> Running claude sandbox\n>>> Image:     " + image + "\n>>> Endpoint:  " + endpoint + "\n>>> API Key:   sk-XXXX...\nsomething went wrong\n"
	}
	// Experiment 1: prompt1 in the RTK sandbox, a run that exits 1.
	r := filepath.Join(dir, "results1", "claude", "claude-opus-4.6", "run-01")
	write(t, filepath.Join(r, "meta.md"), metaFile("claude", "claude-opus-4.6", 1, "", "First line.\nSecond line."))
	write(t, filepath.Join(r, "log.txt"), log("ghcr.io/x/sandbox-claude:latest", "https://gateway.example/v1"))
	// Experiment 2, merged into 1: a run that wrote code, a page and a
	// vendored directory the rule skips.
	r = filepath.Join(dir, "results2", "claude", "claude-opus-4.6", "run-01")
	write(t, filepath.Join(r, "meta.md"), metaFile("claude", "anthropic/claude-opus-4-6", 0, "", "Two."))
	write(t, filepath.Join(r, "log.txt"), log("ghcr.io/x/sandbox-claude:latest", "https://gateway.example/v1"))
	write(t, filepath.Join(r, "workspace", "app.py"), "import sys\n\nprint(1)\n")
	write(t, filepath.Join(r, "workspace", "web", "index.html"), "<html>\n<script>\nwindow.addEventListener('x', f)\n</script>\n</html>")
	write(t, filepath.Join(r, "workspace", ".venv", "lib", "big.py"), strings.Repeat("x\n", 1000))
	write(t, filepath.Join(r, "workspace", "README.md"), "notes\n\n\n")
	// Experiment 20: a codex cell by effort, whose turn failed on a
	// gateway status, and one with only a shebang script.
	r = filepath.Join(dir, "results20", "codex-high", "run-01")
	write(t, filepath.Join(r, "meta.md"), metaFile("codex", "deepseek/x-flash", 0, "", "Five."))
	write(t, filepath.Join(r, "log.txt"), log("sandbox-harness:pinned-cc2.1.220-cx0.146.0", "https://gateway.example/compat/openai"))
	write(t, filepath.Join(r, "output.json"), "RTK setup line\n{\"type\":\"turn.started\"}\n{\"type\":\"turn.failed\",\"error\":{\"message\":\"exceeded retry limit, last status: 429 Too Many Requests\"}}\n")
	r = filepath.Join(dir, "results20", "codex-high", "run-02")
	write(t, filepath.Join(r, "meta.md"), metaFile("codex", "deepseek/x-flash", 0, "", "Five."))
	write(t, filepath.Join(r, "log.txt"), log("sandbox-harness:pinned-cc2.1.220-cx0.146.0", "https://gateway.example/compat/openai"))
	write(t, filepath.Join(r, "output.json"), "{\"type\":\"turn.failed\",\"error\":{\"message\":\"input must be a string or item array\"}}\n")
	write(t, filepath.Join(r, "workspace", "tnote"), "#!/bin/sh\necho hi\n")
	// Experiment 18: a run whose turn failed on a refused prefill after the
	// output cap, with the pin recorded; and one killed.
	r = filepath.Join(dir, "results18", "codex", "claude-opus-5", "run-01")
	write(t, filepath.Join(r, "meta.md"), metaFile("codex", "claude-opus-5", 0, "| Codex max_output_tokens pin | 64000 |\n", "Five."))
	write(t, filepath.Join(r, "output.json"), "{\"type\":\"turn.failed\",\"error\":{\"message\":\"This model does not support assistant message prefill.\"}}\n")
	r = filepath.Join(dir, "results18", "claude", "claude-opus-5", "run-01")
	write(t, filepath.Join(r, "meta.md"), metaFile("claude", "claude-opus-5", 137, "| Fast mode | off |\n", "Five."))
	// A run whose meta.md lacks a field, one whose prompt is no task's, and
	// one whose duration does not parse.
	write(t, filepath.Join(dir, "results21", "claude-fast", "m", "run-01", "meta.md"), "# Run Metadata\n\n| Backend | claude |\n\n## Prompt\n\nFive.\n")
	write(t, filepath.Join(dir, "results21", "claude-fast", "m", "run-02", "meta.md"), metaFile("claude", "m", 0, "", "Something else."))
	write(t, filepath.Join(dir, "results21", "claude-fast", "m", "run-03", "meta.md"), metaFile("claude", "m", 0, "", "Five."))
	write(t, filepath.Join(dir, "results21", "claude-fast", "m", "run-04", "meta.md"), strings.Replace(metaFile("claude", "m", 0, "", "Five."), "| 100s |", "| soon |", 1))
	return dir
}

func find(t *testing.T, s *study, ref string) studyRun {
	t.Helper()
	for _, r := range s.runs {
		if r.Ref == ref {
			return r
		}
	}
	t.Fatalf("no run %s among %d", ref, len(s.runs))
	return studyRun{}
}

func TestConverterRules(t *testing.T) {
	s, err := readStudy(synthetic(t))
	if err != nil {
		t.Fatal(err)
	}
	if s.instructions["prompt1"] != "First line.\nSecond line." || s.instructions["prompt1-rtk"] != s.instructions["prompt1"] {
		t.Fatalf("instructions %q", s.instructions)
	}
	if len(s.runs) != 7 || len(s.skipped) != 3 || s.dirs[21] != 4 || s.dirs[2] != 1 {
		t.Fatalf("%d runs, skipped %v, dirs %v", len(s.runs), s.skipped, s.dirs)
	}
	reasons := map[string]string{}
	for _, sk := range s.skipped {
		reasons[sk.Ref] = sk.Reason
	}
	if reasons["results21/claude-fast/m/run-01"] != skipUnreadableMeta || reasons["results21/claude-fast/m/run-02"] != skipUnknownPrompt ||
		reasons["results21/claude-fast/m/run-04"] != skipUnreadableMeta {
		t.Fatalf("skipped %v", reasons)
	}

	rtk := find(t, s, "results1/claude/claude-opus-4.6/run-01")
	if rtk.Task != "prompt1-rtk" || rtk.Experiment != 1 || rtk.Outcome != "infra_failed" || rtk.Failure.Code != "harness_exit" ||
		rtk.Failure.Detail != "exit 1: something went wrong" || strings.Contains(rtk.Failure.Detail, "sk-") {
		t.Fatalf("an exit of 1 %+v %+v", rtk, rtk.Failure)
	}
	if !rtk.EndedAt.Equal(time.Date(2026, 9, 5, 16, 37, 19, 0, time.UTC)) || rtk.EndedAt.Sub(rtk.StartedAt) != 100*time.Second {
		t.Fatalf("times %v %v", rtk.StartedAt, rtk.EndedAt)
	}
	built := find(t, s, "results2/claude/claude-opus-4.6/run-01")
	if built.Experiment != 1 || built.Task != "prompt2" || built.Outcome != "completed" {
		t.Fatalf("a merged run %+v", built)
	}
	// app.py has 3 newlines and 2 non-blank lines; index.html 4 newlines
	// and 5 non-blank lines; the vendored directory and the README are not
	// counted.
	if built.LOC == nil || *built.LOC != 7 || built.LOCNonblank == nil || *built.LOCNonblank != 7 || !built.Implemented || !built.BrowserInteractive {
		t.Fatalf("the workspace %v %v %v %v", built.LOC, built.LOCNonblank, built.Implemented, built.BrowserInteractive)
	}
	// One spelling of a model, so the two experiments' arms are one
	// candidate.
	if rtk.Config.name() != built.Config.name() || built.Config.Model != "claude-opus-4-6" || built.Config.Version != "unrecorded" ||
		built.Config.Flags["gateway"] != "gateway.example/v1" || built.Config.Flags["image"] != "ghcr.io/x/sandbox-claude:latest" {
		t.Fatalf("configurations %+v %+v", rtk.Config, built.Config)
	}
	failed := find(t, s, "results20/codex-high/run-01")
	if failed.Outcome != "infra_failed" || failed.Failure.Code != "gateway_error" || failed.Config.Reasoning != "high" || failed.Config.Version != "0.146.0" || failed.Config.Harness != harnessCodex {
		t.Fatalf("a gateway failure %+v %+v", failed, failed.Failure)
	}
	shebang := find(t, s, "results20/codex-high/run-02")
	if shebang.Outcome != "completed" || !shebang.Implemented || shebang.LOC != nil || shebang.BrowserInteractive {
		t.Fatalf("a shebang script %+v", shebang)
	}
	capped := find(t, s, "results18/codex/claude-opus-5/run-01")
	if capped.Outcome != "infra_failed" || capped.Failure.Code != "gateway_output_cap" || capped.Config.Flags["max_output_tokens"] != "64000" {
		t.Fatalf("a capped run %+v", capped)
	}
	killed := find(t, s, "results18/claude/claude-opus-5/run-01")
	if killed.Outcome != "infra_failed" || killed.Failure.Code != "killed" || killed.Config.Flags["fast"] != "off" || killed.Image != "" {
		t.Fatalf("a killed run %+v", killed)
	}
	if fast := find(t, s, "results21/claude-fast/m/run-03"); fast.Config.Flags["fast"] != "on" || fast.Config.Version != "unrecorded" {
		t.Fatalf("the fast cell %+v", fast.Config)
	}
	if n := rtk.Config.name(); !strings.HasPrefix(n, "gl-cc-claude-opus-4-6-") || len(n) > 63 {
		t.Fatalf("a candidate name %q", n)
	}

	// An import carries no value of an infra_failed run, and the values
	// the converter computed otherwise, a count of 0 as none.
	if b := rtk.importBody("sha256:t", "sha256:c"); b["measurements"] != nil || b["failure"] == nil {
		t.Fatalf("the import of a failed run %v", b)
	}
	b := shebang.importBody("sha256:t", "sha256:c")
	ms := b["measurements"].([]map[string]any)
	if len(ms) != 2 || ms[0]["value"] != true || b["ref"] != "results20/codex-high/run-02" {
		t.Fatalf("the import of a shebang run %v", b)
	}
	if got := len(built.importBody("t", "c")["measurements"].([]map[string]any)); got != 4 {
		t.Fatalf("the import of a built run carries %d values", got)
	}
}

func TestVersionAndModel(t *testing.T) {
	for _, c := range []struct{ image, backend, want string }{
		{"sandbox-harness:pinned-cc2.1.258-cx0.153.4", "claude", "2.1.258"},
		{"sandbox-harness:pinned-cc2.1.258-cx0.153.4", "codex", "0.153.4"},
		{"ghcr.io/x/sandbox-codex:v0.144.0", "codex", "0.144.0"},
		{"ghcr.io/x/sandbox-claude:v0.0.9", "claude", "unrecorded"},
		{"", "codex", "unrecorded"},
	} {
		if got := version(c.image, c.backend); got != c.want {
			t.Errorf("version(%q, %s) = %q, want %q", c.image, c.backend, got, c.want)
		}
	}
	for in, want := range map[string]string{"claude-opus-4.6": "claude-opus-4-6", "anthropic/claude-opus-4-6": "claude-opus-4-6", "z-ai/glm-5.2": "z-ai/glm-5.2"} {
		if got := model(in); got != want {
			t.Errorf("model(%q) = %q", in, got)
		}
	}
	if q := quantile7([]float64{1, 2, 3, 10}, 0.5); q != 2.5 {
		t.Errorf("the median of four %v", q)
	}
	if q := quantile7([]float64{4}, 0.5); q != 4 {
		t.Errorf("the median of one %v", q)
	}
}
