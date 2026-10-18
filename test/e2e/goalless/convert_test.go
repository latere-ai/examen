// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package goalless_test

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The converter: it reads the study's results directories, run by run,
// into declared past trials, by the mapping of the study's end-to-end spec
// for what the first release of Examen can carry. It keeps nothing of the
// study but what it reads at run time from the directory it is given.

// Why a run directory is not imported.
const (
	skipUnreadableMeta = "unreadable_meta"
	skipUnknownPrompt  = "unknown_prompt"
)

// The tasks of the suite, by the prompt file each instruction is read
// from. prompt1-rtk is prompt1 in the sandbox that initialized RTK, which
// only Experiment 1 ran.
var tasks = []struct{ name, file string }{
	{"prompt1", "prompt1.txt"}, {"prompt1-rtk", "prompt1.txt"}, {"prompt2", "prompt2.txt"},
	{"prompt3", "prompt3.txt"}, {"prompt4", "prompt4.txt"}, {"prompt5", "prompt5.txt"},
}

// controlledTask is the one task that weighs in the suite's figures: the
// prompt the study held from Experiment 7 on.
const controlledTask = "prompt5"

// merged are the study's experiments imported into one Examen experiment,
// so the prompt comparisons they hold compare two tasks of one candidate.
var merged = map[int]int{2: 1, 7: 5}

// The lines-of-code rule: the files counted, by extension, and the
// directories skipped at any depth.
var (
	locExtensions    = []string{".py", ".js", ".html", ".css", ".ts", ".sh", ".go", ".c"}
	sourceExtensions = []string{".rs", ".java", ".rb", ".cpp", ".h", ".jsx", ".tsx", ".mjs", ".lua", ".swift", ".kt"}
	skippedDirs      = []string{"node_modules", "__pycache__", ".git", ".pytest_cache", ".venv", "venv", "vendor", "target"}
)

// failure is why a run is infra_failed.
type failure struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

// config is a candidate: one model on one harness in one configuration, as
// the run's files declare it.
type config struct {
	Harness   string            `json:"harness"`
	Version   string            `json:"version"`
	Model     string            `json:"model"`
	Reasoning string            `json:"reasoning,omitempty"`
	Flags     map[string]string `json:"flags,omitempty"`
}

// name is the candidate's name: the harness, the model and a digest of the
// whole configuration, so two configurations are two candidates.
func (c config) name() string {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // a config of strings always marshals
	}
	sum := sha256.Sum256(b)
	short := map[string]string{harnessClaude: "cc", harnessCodex: "cx"}[c.Harness]
	model := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
			return r
		}
		return '-'
	}, strings.ToLower(c.Model))
	if len(model) > 40 {
		model = model[:40]
	}
	return "gl-" + short + "-" + model + "-" + hex.EncodeToString(sum[:4])
}

// The harnesses, one per backend.
const (
	harnessClaude = "goalless-claude-code"
	harnessCodex  = "goalless-codex"
)

// studyRun is one run directory read into what its import carries.
type studyRun struct {
	Ref        string // the run's path under the study, which is its ref
	Study      int    // the study's experiment number
	Experiment int    // the Examen experiment: the first of a merged pair
	Task       string
	Config     config
	Image      string
	StartedAt  time.Time
	EndedAt    time.Time
	Outcome    string
	Failure    *failure
	// The values the converter computes from the workspace, reported as
	// the harness's own. A count of 0 is no value.
	LOC, LOCNonblank                *int
	Implemented, BrowserInteractive bool
}

// skipped is a run directory not imported, with why.
type skipped struct {
	Ref, Reason string
}

// study is everything the converter read.
type study struct {
	instructions map[string]string // task name to instruction
	runs         []studyRun
	skipped      []skipped
	// dirs counts the run directories of each study experiment.
	dirs map[int]int
}

// readStudy reads every run directory of every resultsN directory.
func readStudy(dir string) (*study, error) {
	s := &study{instructions: map[string]string{}, dirs: map[int]int{}}
	for _, t := range tasks {
		b, err := os.ReadFile(filepath.Join(dir, t.file))
		if err != nil {
			return nil, err
		}
		s.instructions[t.name] = strings.TrimRight(string(b), "\n")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		n, ok := strings.CutPrefix(e.Name(), "results")
		number, err := strconv.Atoi(n)
		if !ok || err != nil || !e.IsDir() {
			continue
		}
		var runDirs []string
		err = filepath.WalkDir(filepath.Join(dir, e.Name()), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "workspace" {
				return filepath.SkipDir
			}
			if !d.IsDir() && d.Name() == "meta.md" {
				runDirs = append(runDirs, filepath.Dir(p))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		sort.Strings(runDirs)
		s.dirs[number] = len(runDirs)
		for _, rd := range runDirs {
			rel, err := filepath.Rel(dir, rd)
			if err != nil {
				return nil, err
			}
			r, why, err := s.readRun(rd, filepath.ToSlash(rel), number)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", rel, err)
			}
			if why != "" {
				s.skipped = append(s.skipped, skipped{Ref: filepath.ToSlash(rel), Reason: why})
				continue
			}
			s.runs = append(s.runs, r)
		}
	}
	slices.SortFunc(s.runs, func(a, b studyRun) int {
		if a.Study != b.Study {
			return a.Study - b.Study
		}
		return strings.Compare(a.Ref, b.Ref)
	})
	return s, nil
}

// meta is what a run's meta.md records: its fields, its prompt, and the
// exit code, duration and end its fields give; ok is false when one of
// them is missing or does not parse.
type meta struct {
	fields   map[string]string
	prompt   string
	exit     int
	duration time.Duration
	ended    time.Time
	ok       bool
}

// readMeta reads meta.md's table of fields and its ## Prompt section.
func readMeta(b []byte) meta {
	m := meta{fields: map[string]string{}}
	text := string(b)
	head, prompt, found := strings.Cut(text, "\n## Prompt\n")
	for line := range strings.SplitSeq(head, "\n") {
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 2 {
			continue
		}
		k, v := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		if k != "Field" && !strings.HasPrefix(k, "---") {
			m.fields[k] = v
		}
	}
	m.prompt = strings.TrimRight(strings.TrimLeft(prompt, "\n"), "\n")
	m.ok = found && m.prompt != ""
	for _, k := range []string{"Backend", "Model", "Run", "Exit Code", "Duration", "Timestamp"} {
		if m.fields[k] == "" {
			m.ok = false
		}
	}
	exit, errExit := strconv.Atoi(m.fields["Exit Code"])
	seconds, errDur := strconv.Atoi(strings.TrimSuffix(m.fields["Duration"], "s"))
	ended, errAt := time.Parse(time.RFC3339, m.fields["Timestamp"])
	m.exit, m.duration, m.ended = exit, time.Duration(seconds)*time.Second, ended.UTC()
	m.ok = m.ok && errExit == nil && errDur == nil && errAt == nil
	return m
}

// logHeader reads the image and the endpoint a run's log.txt names, and
// nothing else of it: the log also holds a key's prefix and local paths.
func logHeader(p string) (image, endpoint string, err error) {
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, ">>> Image:"); ok && image == "" {
			image = strings.TrimSpace(v)
		}
		if v, ok := strings.CutPrefix(line, ">>> Endpoint:"); ok && endpoint == "" {
			endpoint = strings.TrimSpace(v)
		}
		if image != "" && endpoint != "" {
			break
		}
	}
	return image, endpoint, sc.Err()
}

// lastLine is the last line of a run's log.txt that is not blank and names
// no key, for a harness failure's detail.
func lastLine(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	for _, line := range slices.Backward(lines) {
		l := strings.TrimSpace(line)
		if l != "" && !strings.Contains(l, "API Key") {
			if len(l) > 200 {
				l = l[:200]
			}
			return l
		}
	}
	return ""
}

var (
	pinnedTag  = regexp.MustCompile(`pinned-cc([0-9][0-9.]*)-cx([0-9][0-9.]*)`)
	codexTag   = regexp.MustCompile(`sandbox-codex:v([0-9][0-9.]*)$`)
	statusLike = regexp.MustCompile(`\b(403|429|5[0-9][0-9])\b`)
)

// version is the CLI version an image tag names for a backend, or
// unrecorded.
func version(image, backend string) string {
	if m := pinnedTag.FindStringSubmatch(image); m != nil {
		if backend == "codex" {
			return m[2]
		}
		return m[1]
	}
	if m := codexTag.FindStringSubmatch(image); m != nil && backend == "codex" {
		return m[1]
	}
	return "unrecorded"
}

// model is a model id in one spelling: a provider prefix of Anthropic's
// dropped and the dots of a Claude model's version written as dashes, so
// claude-opus-4.6 and anthropic/claude-opus-4-6 are one model.
func model(m string) string {
	m = strings.TrimPrefix(m, "anthropic/")
	if strings.HasPrefix(m, "claude-") {
		m = strings.ReplaceAll(m, ".", "-")
	}
	return m
}

// gateway is the endpoint a run reached, without its scheme.
func gateway(endpoint string) string {
	if i := strings.Index(endpoint, "://"); i >= 0 {
		endpoint = endpoint[i+3:]
	}
	return strings.TrimRight(endpoint, "/")
}

// readRun reads one run directory.
func (s *study) readRun(dir, ref string, number int) (studyRun, string, error) {
	b, err := os.ReadFile(filepath.Join(dir, "meta.md"))
	if err != nil {
		return studyRun{}, "", err
	}
	m := readMeta(b)
	if !m.ok {
		return studyRun{}, skipUnreadableMeta, nil
	}
	r := studyRun{Ref: ref, Study: number, Experiment: number, EndedAt: m.ended, StartedAt: m.ended.Add(-m.duration)}
	if first, ok := merged[number]; ok {
		r.Experiment = first
	}
	if number == 1 {
		r.Task = "prompt1-rtk"
	}
	for _, t := range tasks {
		if r.Task == "" && t.name != "prompt1-rtk" && s.instructions[t.name] == m.prompt {
			r.Task = t.name
		}
	}
	if r.Task == "" {
		return studyRun{}, skipUnknownPrompt, nil
	}
	backend := m.fields["Backend"]
	image, endpoint, err := logHeader(filepath.Join(dir, "log.txt"))
	if err != nil {
		return studyRun{}, "", err
	}
	r.Image = image
	r.Config = config{Harness: harnessClaude, Version: version(image, backend), Model: model(m.fields["Model"]), Flags: map[string]string{}}
	if backend == "codex" {
		r.Config.Harness = harnessCodex
	}
	// The cell directory of an experiment that groups by cell names what
	// differs: codex-high and codex-low their effort, claude-fast its fast
	// mode.
	cellDir := strings.Split(ref, "/")[1]
	switch {
	case strings.HasSuffix(cellDir, "-high"):
		r.Config.Reasoning = "high"
	case strings.HasSuffix(cellDir, "-low"):
		r.Config.Reasoning = "low"
	case strings.HasSuffix(cellDir, "-fast"):
		r.Config.Flags["fast"] = "on"
	}
	if v := m.fields["Fast mode"]; v != "" {
		r.Config.Flags["fast"] = v
	}
	if v := m.fields["Codex max_output_tokens pin"]; v != "" {
		r.Config.Flags["max_output_tokens"] = v
	}
	if image != "" {
		r.Config.Flags["image"] = image
	}
	if endpoint != "" {
		r.Config.Flags["gateway"] = gateway(endpoint)
	}
	r.Outcome, r.Failure = classify(m.exit, backend, filepath.Join(dir, "output.json"), filepath.Join(dir, "log.txt"))
	if err := measure(&r, filepath.Join(dir, "workspace")); err != nil {
		return studyRun{}, "", err
	}
	return r, "", nil
}

// classify is a run's outcome by the spec's table: exit 137 is killed, any
// other exit but 0 a harness failure, and a Codex stream whose turn failed
// for a refused prefill after the output cap, or on a gateway status or
// with its reconnects spent, a gateway failure whatever the exit code. A
// turn that failed on anything else, with exit 0, is completed: the run
// ended on its own.
func classify(exit int, backend, output, log string) (string, *failure) {
	if backend == "codex" {
		if msg, failed := turnFailed(output); failed {
			switch {
			case strings.Contains(msg, "prefill") || strings.Contains(msg, "max_output_tokens"):
				return "infra_failed", &failure{Code: "gateway_output_cap", Detail: msg}
			case statusLike.MatchString(msg) || strings.Contains(msg, "high demand") ||
				strings.Contains(msg, "stream disconnected") || strings.Contains(msg, "retry limit"):
				return "infra_failed", &failure{Code: "gateway_error", Detail: msg}
			}
		}
	}
	switch exit {
	case 0:
		return "completed", nil
	case 137:
		return "infra_failed", &failure{Code: "killed", Detail: "exit 137"}
	}
	return "infra_failed", &failure{Code: "harness_exit", Detail: strings.TrimSpace(fmt.Sprintf("exit %d: %s", exit, lastLine(log)))}
}

// turnFailed reads a Codex event stream for a turn.failed event, and
// answers its error message. Lines that are not JSON are not events.
func turnFailed(p string) (string, bool) {
	f, err := os.Open(p)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"turn.failed"`)) {
			var ev struct {
				Type  string `json:"type"`
				Error struct {
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(bytes.TrimSpace(line), &ev) == nil && ev.Type == "turn.failed" {
				msg := ev.Error.Message
				if len(msg) > 300 {
					msg = msg[:300]
				}
				return msg, true
			}
		}
		if err != nil {
			return "", false
		}
	}
}

// measure computes the values of a workspace by the study's rules: lines
// of code as newline bytes and as non-blank lines of the counted files,
// whether it holds a source file, and whether a page in it is interactive.
// A missing workspace is an empty one.
func measure(r *studyRun, workspace string) error {
	var loc, nonblank int
	err := filepath.WalkDir(workspace, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && p == workspace {
			return fs.SkipAll
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != workspace && slices.Contains(skippedDirs, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		ext := strings.ToLower(path.Ext(d.Name()))
		switch {
		case slices.Contains(locExtensions, ext):
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			r.Implemented = true
			loc += bytes.Count(b, []byte{'\n'})
			for line := range bytes.SplitSeq(b, []byte{'\n'}) {
				if len(bytes.TrimSpace(line)) > 0 {
					nonblank++
				}
			}
			if ext == ".html" && interactive(b) {
				r.BrowserInteractive = true
			}
		case slices.Contains(sourceExtensions, ext):
			r.Implemented = true
		default:
			if shebang, err := startsWithShebang(p); err != nil {
				return err
			} else if shebang {
				r.Implemented = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if loc > 0 {
		r.LOC = &loc
	}
	if nonblank > 0 {
		r.LOCNonblank = &nonblank
	}
	return nil
}

var inlineHandler = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)

// interactive reports whether a page holds a script element and a marker
// of interaction: an event listener, an animation frame or an inline
// handler.
func interactive(page []byte) bool {
	s := string(page)
	return strings.Contains(strings.ToLower(s), "<script") &&
		(strings.Contains(s, "addEventListener") || strings.Contains(s, "requestAnimationFrame") || inlineHandler.MatchString(s))
}

// startsWithShebang reports whether a file's first two bytes are #!.
func startsWithShebang(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 2)
	n, err := io.ReadFull(f, head)
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return false, nil
	}
	return n == 2 && string(head) == "#!", err
}

// importBody is the past-trial import of a run, with the values it
// computed as the harness's own.
func (r studyRun) importBody(task, candidate string) map[string]any {
	body := map[string]any{
		"executor": "external", "task": task, "candidate": candidate, "outcome": r.Outcome,
		"started_at": r.StartedAt, "agent_ended_at": r.EndedAt, "ended_at": r.EndedAt,
		"declared": map[string]string{"harness_version": r.Config.Version, "image": r.Image},
		"ref":      r.Ref,
	}
	if r.Failure != nil {
		body["failure"] = r.Failure
		return body
	}
	ms := []map[string]any{
		{"metric": "implemented", "value": r.Implemented},
		{"metric": "browser_interactive", "value": r.BrowserInteractive},
	}
	if r.LOC != nil {
		ms = append(ms, map[string]any{"metric": "loc", "value": *r.LOC})
	}
	if r.LOCNonblank != nil {
		ms = append(ms, map[string]any{"metric": "loc_nonblank", "value": *r.LOCNonblank})
	}
	body["measurements"] = ms
	return body
}
