// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/execute"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/topos"
	"latere.ai/x/examen/internal/topos/topostest"
)

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// files is a store of objects by path, served as Arca serves an owner's
// space, deep listings included.
type files struct {
	mu      sync.Mutex
	objects map[string][]byte
	tokens  []string
	outage  bool
	// reads counts the objects served whole; an object below down
	// answers an outage.
	reads int
	down  string
	// refuseWrites answers every write 403, as a store whose authorizer
	// does not let the owner token write there.
	refuseWrites bool
	// valid, when set, answers 401 to a request whose token it refuses,
	// as a store does to an expired one.
	valid func(token string) bool
}

func (f *files) put(path string, b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[path] = b
}

func (f *files) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens = append(f.tokens, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if f.valid != nil && !f.valid(f.tokens[len(f.tokens)-1]) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated"}}`)
		return
	}
	if f.outage {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// A space is one owner's: the experiment's for the check's files, and
	// the one the session's folder names for the run's.
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/files/"+url.PathEscape(owner)+"/")
	if !ok {
		rest, ok = strings.CutPrefix(r.URL.EscapedPath(), "/files/owner/")
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found"}}`)
		return
	}
	path, err := url.PathUnescape(rest)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	// A write is create-once: a path that holds an object is refused.
	if r.Method == http.MethodPut {
		_, held := f.objects[path]
		switch {
		case f.refuseWrites:
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":"forbidden"}}`)
		case r.Header.Get("If-None-Match") != "*":
			w.WriteHeader(http.StatusBadRequest)
		case held:
			w.WriteHeader(http.StatusPreconditionFailed)
			_, _ = io.WriteString(w, `{"error":{"code":"precondition_failed"}}`)
		default:
			b, _ := io.ReadAll(r.Body)
			f.objects[path] = b
			w.WriteHeader(http.StatusCreated)
		}
		return
	}
	if r.URL.Query().Get("list") == "1" {
		var entries []arca.Entry
		for p, b := range f.objects {
			if strings.HasPrefix(p, path+"/") {
				entries = append(entries, arca.Entry{Path: p, Size: int64(len(b)), Checksum: sha(b), ChecksumKind: "sha256"})
			}
		}
		_ = json.MarshalWrite(w, map[string]any{"entries": entries})
		return
	}
	if f.down != "" && strings.HasPrefix(path, f.down) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	b, ok := f.objects[path]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found"}}`)
		return
	}
	f.reads++
	_, _ = w.Write(b)
}

// sandboxes is a Cella that runs no command: each exec answers what the
// test set for the check its environment names, and the stub keeps the
// manifests, the archives and the commands it was given.
type sandboxes struct {
	mu        sync.Mutex
	manifests []map[string]any
	archives  map[string][]byte
	execs     []map[string]any
	deleted   []string
	answers   map[string]cella.Result
	refuse    string
	// phase is the phase a create answers, Running when empty; put and
	// exec are the statuses a write and a command answer, 2xx when zero.
	phase     string
	put, exec int
	// stream, when set, reads an archive as it arrives and keeps each
	// entry's size and SHA-256 in received rather than its bytes.
	stream   bool
	received map[string]string
	// valid, when set, answers 401 to a request whose token it refuses,
	// as Cella does to an expired one; ran, when set, is called with each
	// check a command ran, before its answer.
	valid func(token string) bool
	ran   func(check string)
}

func (c *sandboxes) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.valid != nil && !c.valid(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"unauthenticated"}}`)
		return
	}
	if r.Method == http.MethodPut && c.stream && c.put != 0 {
		// A refusal before the archive is read.
		w.WriteHeader(c.put)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_field"}}`)
		return
	}
	if r.Method == http.MethodPut && c.stream {
		c.received = map[string]string{}
		tr := tar.NewReader(r.Body)
		for {
			h, err := tr.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			sum := sha256.New()
			n, err := io.Copy(sum, tr)
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			c.received[h.Name] = strconv.FormatInt(n, 10) + " " + hex.EncodeToString(sum.Sum(nil))
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	body, _ := io.ReadAll(r.Body)
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/environments/default":
		_, _ = io.WriteString(w, `{"apiVersion":"cella.example/v1","kind":"Environment"}`)
	case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
		if c.refuse != "" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"error":{"code":"`+c.refuse+`"}}`)
			return
		}
		var m map[string]any
		_ = json.Unmarshal(body, &m)
		c.manifests = append(c.manifests, m)
		phase := c.phase
		if phase == "" {
			phase = cella.PhaseRunning
		}
		_, _ = io.WriteString(w, `{"spec":{"image":"base@sha256:`+strings.Repeat("b", 64)+`","workspace":{"path":"/workspace"}},"status":{"id":"sbx_`+itoa(len(c.manifests))+`","phase":"`+phase+`"}}`)
	case r.Method == http.MethodPut && c.put != 0:
		w.WriteHeader(c.put)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_field"}}`)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec") && c.exec != 0:
		w.WriteHeader(c.exec)
		_, _ = io.WriteString(w, `{"error":{"code":"invalid_field","details":{"detail":"no such command"}}}`)
	case r.Method == http.MethodPut:
		c.archives[strings.Split(r.URL.Path, "/")[2]] = body
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exec"):
		var e map[string]any
		_ = json.Unmarshal(body, &e)
		c.execs = append(c.execs, e)
		check := e["env"].(map[string]any)["EXAMEN_CHECK"].(string)
		if c.ran != nil {
			c.ran(check)
		}
		_ = json.MarshalWrite(w, c.answers[check])
	case r.Method == http.MethodDelete:
		c.deleted = append(c.deleted, strings.Split(r.URL.Path, "/")[2])
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// gateway is Lux's door answering each judgment with the tool call the
// test set, or a refusal.
type gateway struct {
	stop string
	// raw, when set, is the whole answer to a call; unpriced leaves the
	// cost out of an answer.
	raw      string
	unpriced bool
	mu       sync.Mutex
	calls    []map[string]any
	keys     []string
	labels   []string
	answers  []string
	status   int
	code     string
}

func (g *gateway) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	var call map[string]any
	_ = json.Unmarshal(body, &call)
	g.calls = append(g.calls, call)
	g.keys = append(g.keys, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	g.labels = append(g.labels, r.Header.Get("Lux-Labels"))
	if r.URL.Path != "/lux/v1/generate" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if g.status != 0 {
		w.WriteHeader(g.status)
		_, _ = io.WriteString(w, `{"error":{"code":"`+g.code+`","details":{"request_id":"req_refused"}}}`)
		return
	}
	args := g.answers[0]
	if len(g.answers) > 1 {
		g.answers = g.answers[1:]
	}
	w.Header().Set("Lux-Request-Id", "req_"+itoa(len(g.calls)))
	if g.raw != "" {
		_, _ = io.WriteString(w, g.raw)
		return
	}
	stop := g.stop
	if stop == "" {
		stop = "tool_use"
	}
	cost := `,"cost_usd_micro":1500`
	if g.unpriced {
		cost = ""
		w.Header().Del("Lux-Request-Id")
	}
	if g.unpriced {
		w.Header().Del("Lux-Request-Id")
	}
	_, _ = io.WriteString(w, `{"id":"msg_1","model":"anthropic/claude-haiku-5.5","stop_reason":"`+stop+`","usage":{"input_tokens":900,"output_tokens":60`+cost+`},
		"blocks":[{"type":"tool_use","tool_use":{"id":"tu_1","name":"record_judgment","args":`+args+`}}]}`)
}

// graded is an experiment of one topos candidate on one task with two
// checks and a judge, and the versions it pins.
type graded struct {
	eid, cid          string
	inventory, runner string
	judge, judgeID    string
	metrics           map[string]string
}

// seedGraded stores an experiment whose task has an inventory check and a
// run check, whose files are an Arca folder, and whose suite has a model
// judge reading the workspace, the final message and the check values.
func seedGraded(ctx context.Context, t *testing.T, st store.Store, fs *files, n int) graded {
	t.Helper()
	g := graded{metrics: map[string]string{}}
	put := func(kind, name string, v any) objects.Version {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := id.DigestJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		out, _, err := st.Versions().Apply(ctx, objects.Version{ID: id.New(objects.Prefix(kind)), Kind: kind, Owner: owner, Name: name, Digest: digest, Body: raw}, store.Precondition{})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, m := range []objects.Metric{
		{Name: "entry_found", Type: objects.TypeBinary, Direction: objects.DirectionNone},
		{Name: "size", Type: objects.TypeCount, Direction: objects.DirectionNone, AppliesTo: []string{objects.OutcomeCompleted}},
		{Name: "runs", Type: objects.TypeBinary, Direction: objects.HigherIsBetter},
		{Name: "kind", Type: objects.TypeLabel, Labels: []string{"game", "tool", "none"}, Direction: objects.DirectionNone},
		{Name: "idea", Type: objects.TypeLabel, Vocabulary: objects.VocabularyOpen, Direction: objects.DirectionNone},
	} {
		g.metrics[m.Name] = put(objects.KindMetric, m.Name, m).Digest
	}
	checkFiles := map[string][]byte{"checks/inventory.py": []byte("#!/usr/bin/env python3\nprint('inventory')\n"), "seed/README.md": []byte("seed\n")}
	var list []id.FolderFile
	for p, b := range checkFiles {
		fs.put("files/evals/graders/"+p, b)
		list = append(list, id.FolderFile{Path: p, SHA256: sha(b)})
	}
	ref := &objects.ArcaRef{Path: "files/evals/graders", Digest: id.FolderDigest(list)}
	cmd := []string{"/bin/sh", "-c", `exec python3 "$EXAMEN_CHECK_FILES/checks/inventory.py" "$EXAMEN_OUTPUTS/workspace"`}
	g.inventory = put(objects.KindGrader, "inventory", objects.Grader{Name: "inventory", Kind: objects.GraderCheck, Produces: []string{g.metrics["entry_found"], g.metrics["size"]},
		Check: &objects.Check{Command: cmd, Files: ref, Env: map[string]string{"HOME": "/tmp"}}}).Digest
	g.runner = put(objects.KindGrader, "run", objects.Grader{Name: "run", Kind: objects.GraderCheck, Produces: []string{g.metrics["runs"]},
		Check: &objects.Check{Command: []string{"run"}}}).Digest
	zero := 0.0
	jv := put(objects.KindGrader, "artifact", objects.Grader{Name: "artifact", Kind: objects.GraderModelJudge, Produces: []string{g.metrics["kind"], g.metrics["idea"]},
		ModelJudge: &objects.ModelJudge{Model: "anthropic/claude-haiku-5.5", Prompt: "Label what the run made.", Temperature: &zero, Preamble: objects.JudgePreamble,
			Inputs: objects.JudgeInputs{FinalMessage: true, Outputs: []string{"workspace/"}, Checks: true}}})
	g.judge, g.judgeID = jv.Digest, jv.ID
	task := put(objects.KindTask, "build", objects.Task{Name: "build", Instruction: "Build something.", Timeout: "20m", Checks: []string{g.inventory, g.runner}})
	metrics := []string{"builtin:completed", g.metrics["entry_found"], g.metrics["size"], g.metrics["runs"], g.metrics["kind"], g.metrics["idea"]}
	suite := put(objects.KindSuite, "s", objects.Suite{Name: "s", Tasks: []string{task.Digest}, Metrics: metrics, PrimaryMetric: "builtin:completed", Graders: []string{g.judge}})
	cand := put(objects.KindCandidate, "helper-low", objects.Candidate{Name: "helper-low", Kind: objects.CandidateTopos,
		Topos: &objects.Topos{Agent: "helper@" + agentDigest, Model: "anthropic/claude-haiku-5.5", Reasoning: "low"}})
	e := objects.Experiment{ID: id.New(id.Experiment), Owner: owner, Suite: suite.Digest, SuiteID: suite.ID, SuiteName: "s",
		Candidates: []string{cand.Digest}, CandidateIDs: []string{cand.ID}, Trials: n, Budget: objects.Budget{Name: "b"},
		Parallelism: 4, Reference: objects.Reference{Candidate: cand.Digest}, Mode: objects.ModeExecute, CreatedBy: owner}
	cell := objects.Cell{ID: id.New(id.Cell), Task: task.Digest, TaskID: task.ID, Candidate: cand.Digest, CandidateID: cand.ID}
	smoke := execute.Smoke(cell.ID, n, objects.ExecutorTopos)
	ne := store.NewExperiment{Experiment: e, MaxActive: 16, Cells: []store.NewCell{{Cell: cell, Smoke: &store.Rollout{Rollout: smoke.Rollout, Trials: []store.RolloutTrial{{Index: 0}}}}}}
	if err := st.Experiments().Create(ctx, ne); err != nil {
		t.Fatal(err)
	}
	g.eid, g.cid = e.ID, cell.ID
	return g
}

// gradingEngine is a topos engine that grades with the stubs.
func gradingEngine(t *testing.T, st store.Store, stub *topostest.Stub, m *minter, fs *files, cl *sandboxes, lx *gateway) *Engine {
	t.Helper()
	srvFiles := httptest.NewServer(http.HandlerFunc(fs.serve))
	srvCella := httptest.NewServer(http.HandlerFunc(cl.serve))
	srvLux := httptest.NewServer(http.HandlerFunc(lx.serve))
	t.Cleanup(func() { srvFiles.Close(); srvCella.Close(); srvLux.Close() })
	e := toposEngine(st, stub, m, &toposGate{})
	e.Keys = keys.NewMemory()
	e.Grading = &Grading{Tokens: m, Arca: arca.New(srvFiles.URL, srvFiles.Client()), ArcaAudience: "arca", Cella: cella.New(srvCella.URL, srvCella.Client()),
		CellaAudience: "cella", Environment: "default", LuxURL: srvLux.URL, HTTP: srvLux.Client(), Workers: 2}
	return e
}

// runSession drives the stub's one session to the end of a run that wrote
// two files, which the stub's folder holds.
func runSession(stub *topostest.Stub, fs *files, sid string) {
	at := time.Date(2026, 10, 19, 12, 1, 0, 0, time.UTC)
	game, readme := []byte("print('snake')\n"), []byte("# Snake\n")
	fs.put("files/conversations/"+sid+"/work/game.py", game)
	fs.put("files/conversations/"+sid+"/work/README.md", readme)
	stub.Set(sid, topos.StatusEnded, topos.StopCompleted,
		topostest.Event(topos.EventSessionStatus, "", at, map[string]any{"status": "running"}),
		topostest.Event(topos.EventModelRequest, "", at.Add(time.Second), map[string]any{"model": "anthropic/claude-haiku-5.5", "outcome": "ok", "cost_usd_micro": 900,
			"usage": map[string]any{"input_tokens": 100, "output_tokens": 40}}),
		topostest.Event(topos.EventWorkSaved, "", at.Add(40*time.Second), map[string]any{"covers": 3, "written": []topos.WorkFile{
			{Path: "README.md", Size: int64(len(readme)), Mode: 0o644, SHA256: sha(readme)}, {Path: "game.py", Size: int64(len(game)), Mode: 0o755, SHA256: sha(game)}}}),
		topostest.Event(topos.EventAgentMessage, "", at.Add(50*time.Second), map[string]any{"stop_reason": "end_turn",
			"message": map[string]any{"role": "assistant", "blocks": []map[string]any{{"type": "text", "text": "I built a snake game."}}}}),
		topostest.Event(topos.EventSessionStatus, "", at.Add(time.Minute), map[string]any{"status": "ended", "stop_reason": topos.StopCompleted}),
	)
}

// values is each current value of a trial, by metric name.
func values(t *testing.T, st store.Store, trial string, metrics map[string]string) map[string]any {
	t.Helper()
	ms, err := st.Experiments().Measurements(t.Context(), store.MeasurementFilter{Trial: trial, Generation: "current"}, store.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for n, d := range metrics {
		names[d] = n
	}
	out := map[string]any{}
	for _, m := range ms {
		if n, ok := names[m.Metric]; ok {
			out[n] = m.Value
		}
	}
	return out
}

// TestEvalsGradesARun is the step's end to end against stubs of every core:
// a topos trial that ended is given its gradings; its checks run in one
// check sandbox created with no network as the owner, with the run's kept
// files, its log, its final message and each check's pinned files written
// in and the EXAMEN_ variables set, the sandbox deleted after; their values
// are recorded under each check's digest; the judge then reads the files,
// the final message and the check values through Lux on a judge Key of its
// own, labeled with the trial; its labels are recorded with their
// rationales; the trial answers its check runs and judgment; and the cell
// and the experiment complete only once every grading ended.
func TestEvalsGradesARun(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	fs := &files{objects: map[string][]byte{}}
	cl := &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}}
	lx := &gateway{answers: []string{`{"kind":{"value":"game","rationale":"it scores"},"idea":{"value":"Snake","rationale":"a snake"}}`}}
	m := &minter{}
	ctx := t.Context()
	g := seedGraded(ctx, t, st, fs, 1)
	e := gradingEngine(t, st, stub, m, fs, cl, lx)
	cl.answers[g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 2}}`, Stderr: "entry: game.py", Duration: 300}
	cl.answers[g.runner] = cella.Result{ExitCode: 1, Duration: 900}

	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	sid := stub.Sessions()[0]
	runSession(stub, fs, sid)
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	trial := trialsOf(t, st, g.eid)[0]
	if trial.State != objects.StateDone || len(trial.Checks) != 2 || len(trial.Judgments) != 1 || !strings.Contains(string(trial.Checks[0]), `"pending"`) {
		t.Fatalf("the done trial's gradings: %s %v %v", trial.State, trial.Checks, trial.Judgments)
	}
	if work := trial.Evidence[1]; work.Name != "workspace/" || work.Digest == "" || *work.Size != 23 {
		t.Fatalf("the kept copy's evidence %+v", work)
	}
	cell, err := st.Experiments().Cell(ctx, g.cid)
	if err != nil || cell.State != objects.StateGrading {
		t.Fatalf("the cell before grading: %s %v", cell.State, err)
	}

	// The first round runs the checks and holds the judge until they end.
	if err := e.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	if len(cl.manifests) != 1 || len(cl.execs) != 2 || len(lx.calls) != 0 || len(cl.deleted) != 1 {
		t.Fatalf("one sandbox, two checks, no judgment yet: %d %d %d %d", len(cl.manifests), len(cl.execs), len(lx.calls), len(cl.deleted))
	}
	spec := cl.manifests[0]["spec"].(map[string]any)
	if spec["network"].(map[string]any)["egress"].(map[string]any)["mode"] != "none" || spec["environment"] != "default" {
		t.Fatalf("the check sandbox's manifest %v", cl.manifests[0])
	}
	env := cl.execs[0]["env"].(map[string]any)
	for k, want := range map[string]string{"EXAMEN_TRIAL": trial.ID, "EXAMEN_CHECK": g.inventory, "EXAMEN_OUTPUTS": "/workspace/examen/outputs",
		"EXAMEN_CHECK_FILES": "/workspace/examen/check-files/" + strings.TrimPrefix(g.inventory, "sha256:"), "EXAMEN_WORKSPACE": "/workspace/examen/outputs/workspace", "HOME": "/tmp"} {
		if env[k] != want {
			t.Errorf("%s is %v, want %s", k, env[k], want)
		}
	}
	if _, has := cl.execs[1]["env"].(map[string]any)["EXAMEN_CHECK_FILES"]; has {
		t.Error("a check with no files names EXAMEN_CHECK_FILES")
	}
	written := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(cl.archives["sbx_1"]))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(tr)
		written[h.Name] = string(b)
	}
	inv := "examen/check-files/" + strings.TrimPrefix(g.inventory, "sha256:") + "/"
	for name, want := range map[string]string{"examen/outputs/workspace/game.py": "print('snake')\n", "examen/final_message.txt": "I built a snake game.",
		inv + "checks/inventory.py": "#!/usr/bin/env python3\nprint('inventory')\n", inv + "seed/README.md": "seed\n"} {
		if written[name] != want {
			t.Errorf("%s holds %q, want %q", name, written[name], want)
		}
	}
	if !strings.Contains(written["examen/transcript.jsonl"], "work.saved") || !strings.Contains(written["examen/outputs.manifest.json"], `"workspace/game.py"`) {
		t.Error("the log or the output manifest is missing from the check sandbox")
	}
	got := values(t, st, trial.ID, g.metrics)
	if got["entry_found"] != true || got["size"] != 2.0 || got["runs"] != false {
		t.Fatalf("the check values %v", got)
	}

	// The second round runs the judge on its own Key.
	if err := e.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	if len(lx.calls) != 1 || lx.labels[0] != "examen.trial="+trial.ID || !strings.HasPrefix(lx.keys[0], "lux_value") {
		t.Fatalf("the judgment's call: %d calls, labels %v, key %v", len(lx.calls), lx.labels, lx.keys)
	}
	user := lx.calls[0]["messages"].([]any)[0].(map[string]any)["blocks"].([]any)[0].(map[string]any)["text"].(string)
	for _, want := range []string{"I built a snake game.", `"entry_found":true`, "print('snake')", `name="workspace/README.md"`} {
		if !strings.Contains(user, want) {
			t.Errorf("the judge did not read %q", want)
		}
	}
	asked := e.Keys.(*keys.Memory).Asked()
	if len(asked) != 1 || asked[0].Purpose != keys.PurposeJudge || asked[0].Labels["examen.grader"] != g.judgeID || asked[0].Models[0] != "anthropic/claude-haiku-5.5" {
		t.Fatalf("the judge's Key was asked as %+v", asked)
	}
	got = values(t, st, trial.ID, g.metrics)
	if got["kind"] != "game" || got["idea"] != "snake" {
		t.Fatalf("the judge's values %v", got)
	}
	trial = trialsOf(t, st, g.eid)[0]
	// Each check that ran keeps its standard output and standard error in
	// the experiment's child of files/evals/, by cell, trial and grader.
	var inventory objects.CheckRun
	if err := json.Unmarshal(trial.Checks[0], &inventory); err != nil || len(inventory.Outputs) != 2 || !strings.HasPrefix(inventory.Sandbox, "sbx_") {
		t.Fatalf("the inventory check's outputs %s %v", trial.Checks[0], err)
	}
	folder := CheckOutputFolder(g.eid, g.cid, trial.ID, g.inventory)
	for i, want := range []struct{ name, body string }{{"stdout.txt", `{"metrics": {"entry_found": true, "size": 2}}`}, {"stderr.txt", "entry: game.py"}} {
		ref := inventory.Outputs[i]
		if ref.Kind != objects.EvidenceCheckOutput || ref.Name != want.name || ref.Owner != owner || ref.Path != folder+want.name || ref.Digest != "sha256:"+sha([]byte(want.body)) || *ref.Size != int64(len(want.body)) {
			t.Fatalf("the inventory check's %s %+v", want.name, ref)
		}
		if string(fs.objects[ref.Path]) != want.body {
			t.Fatalf("the store holds %q at %s", fs.objects[ref.Path], ref.Path)
		}
	}
	var run objects.CheckRun
	var judgment objects.Judgment
	if err := json.Unmarshal(trial.Checks[1], &run); err != nil || run.State != objects.CheckOK || *run.ExitCode != 1 || run.Duration != 0.9 || run.Image == "" {
		t.Fatalf("the run check's record %s %v", trial.Checks[1], err)
	}
	if err := json.Unmarshal(trial.Judgments[0], &judgment); err != nil || judgment.State != objects.JudgmentOK || judgment.Generation != 1 ||
		*judgment.Cost != 1500 || judgment.Attempts[0].LuxRequest != "req_1" || judgment.RequestDigest == "" {
		t.Fatalf("the judgment's record %s %v", trial.Judgments[0], err)
	}
	cell, _ = st.Experiments().Cell(ctx, g.cid)
	exp, _ := st.Experiments().Get(ctx, g.eid)
	if cell.State != objects.StateComplete || exp.State != objects.StateComplete {
		t.Fatalf("after grading the cell is %s and the experiment %s", cell.State, exp.State)
	}
	for _, a := range m.asks {
		if a.Owner != owner || a.Experiment != g.eid {
			t.Fatalf("an owner token was asked as %+v", a)
		}
	}
	if err := e.ReleaseJudgeKeys(ctx); err != nil || !e.Keys.(*keys.Memory).Released("key-0001") {
		t.Fatalf("the judge's Key after the experiment ended: %v", err)
	}
}

// TestGradingsThatCannotRun: files whose digest changed leave the checks
// not run with why, and a malformed answer is tried again and then
// malformed, a spend refusal parks the cell over_budget, and the cell
// blocks on a smoke trial whose check did not run.
func TestGradingsThatCannotRun(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	fs := &files{objects: map[string][]byte{}}
	cl := &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}}
	lx := &gateway{answers: []string{`{"kind":{"value":"film","rationale":"r"},"idea":{"value":"x","rationale":"r"}}`}}
	m := &minter{}
	ctx := t.Context()
	g := seedGraded(ctx, t, st, fs, 2)
	e := gradingEngine(t, st, stub, m, fs, cl, lx)
	cl.answers[g.runner] = cella.Result{Stdout: "ok"}
	fs.put("files/evals/graders/checks/inventory.py", []byte("changed"))
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runSession(stub, fs, stub.Sessions()[0])
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e.Grade(ctx); err != nil {
			t.Fatal(err)
		}
	}
	trial := trialsOf(t, st, g.eid)[0]
	var inv, run objects.CheckRun
	var judgment objects.Judgment
	_ = json.Unmarshal(trial.Checks[0], &inv)
	_ = json.Unmarshal(trial.Checks[1], &run)
	_ = json.Unmarshal(trial.Judgments[0], &judgment)
	if inv.State != objects.CheckNotRun || !strings.Contains(inv.Reason, "files_changed") || run.State != objects.CheckMalformed {
		t.Fatalf("the check runs %+v %+v", inv, run)
	}
	if judgment.State != objects.CheckNotRun || !strings.Contains(judgment.Reason, "parked blocked") || len(lx.calls) != 0 {
		t.Fatalf("the judgment of a blocked cell %+v after %d calls", judgment, len(lx.calls))
	}
	if got := values(t, st, trial.ID, g.metrics); len(got) != 0 {
		t.Fatalf("a run with no ok grading recorded %v", got)
	}
	cell, _ := st.Experiments().Cell(ctx, g.cid)
	if cell.State != objects.StateBlocked || !strings.HasPrefix(cell.ParkedReason, "smoke_check_failed: "+trial.ID) || len(stub.Sessions()) != 1 {
		t.Fatalf("the cell %s %q after %d sessions", cell.State, cell.ParkedReason, len(stub.Sessions()))
	}

	// A judge's answer outside its labels is tried again, then malformed.
	st3 := pgtest.Open(t, false, postgres.Options{})
	stub3 := topostest.New(t)
	stub3.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	fs3 := &files{objects: map[string][]byte{}}
	cl3 := &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}}
	g3 := seedGraded(ctx, t, st3, fs3, 1)
	e3 := gradingEngine(t, st3, stub3, &minter{}, fs3, cl3, lx)
	cl3.answers[g3.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": false, "size": null}}`}
	if err := e3.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runSession(stub3, fs3, stub3.Sessions()[0])
	if err := e3.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e3.Grade(ctx); err != nil {
			t.Fatal(err)
		}
	}
	trial = trialsOf(t, st3, g3.eid)[0]
	_ = json.Unmarshal(trial.Judgments[0], &judgment)
	if judgment.State != objects.JudgmentMalformed || len(judgment.Attempts) != 2 || len(lx.calls) != 2 || !strings.Contains(judgment.Reason, "kind.value") {
		t.Fatalf("a malformed judgment %+v after %d calls", judgment, len(lx.calls))
	}
	if got := values(t, st3, trial.ID, g3.metrics); got["entry_found"] != false || got["kind"] != nil || got["runs"] != true || got["size"] != nil {
		t.Fatalf("the values beside a malformed judgment %v", got)
	}
	if cell, _ := st3.Experiments().Cell(ctx, g3.cid); cell.State != objects.StateComplete {
		t.Fatalf("a cell whose judgment ended malformed is %s", cell.State)
	}

	// A judge refused for spend parks its cell over_budget.
	st2 := pgtest.Open(t, false, postgres.Options{})
	stub2 := topostest.New(t)
	stub2.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	fs2 := &files{objects: map[string][]byte{}}
	cl2 := &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}}
	lx2 := &gateway{status: http.StatusPaymentRequired, code: "budget_exhausted"}
	g2 := seedGraded(ctx, t, st2, fs2, 1)
	e2 := gradingEngine(t, st2, stub2, &minter{}, fs2, cl2, lx2)
	cl2.answers[g2.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 2}}`}
	cl2.answers[g2.runner] = cella.Result{}
	// A store that refuses Examen's writes keeps no check output, and the
	// checks end as they would.
	fs2.refuseWrites = true
	if err := e2.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runSession(stub2, fs2, stub2.Sessions()[0])
	if err := e2.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := e2.Grade(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cell2, _ := st2.Experiments().Cell(ctx, g2.cid)
	if cell2.State != objects.StateOverBudget || !strings.Contains(cell2.ParkedReason, "judge") {
		t.Fatalf("a judge refused for spend: %s %q", cell2.State, cell2.ParkedReason)
	}
	for _, raw := range trialsOf(t, st2, g2.eid)[0].Checks {
		var run objects.CheckRun
		if err := json.Unmarshal(raw, &run); err != nil || run.State != objects.CheckOK || len(run.Outputs) != 0 {
			t.Fatalf("a check whose output the store refused %s %v", raw, err)
		}
	}
}

// TestGradingWaitsOutAnOutage: a files store that does not answer puts the
// trial's checks off rather than ending them, and Cella's sandbox limit
// puts them off too.
func TestGradingWaitsOutAnOutage(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	fs := &files{objects: map[string][]byte{}}
	cl := &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}}
	m := &minter{}
	ctx := t.Context()
	g := seedGraded(ctx, t, st, fs, 1)
	e := gradingEngine(t, st, stub, m, fs, cl, &gateway{})
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	runSession(stub, fs, stub.Sessions()[0])
	if err := e.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	fs.outage = true
	if err := e.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	trial := trialsOf(t, st, g.eid)[0]
	list, err := st.Gradings().OfTrials(ctx, []string{trial.ID})
	if err != nil || list[0].State != objects.GradingPending || list[0].Attempts != 1 || len(cl.manifests) != 0 {
		t.Fatalf("an outage ended the checks: %+v %v", list, err)
	}
	if _, err := st.Gradings().Hold(ctx, []string{list[0].ID, list[1].ID}, -time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, g := range list {
		if err := st.Gradings().Wait(ctx, g.ID, -time.Hour, g.Attempts); err != nil {
			t.Fatal(err)
		}
	}
	fs.outage, cl.refuse = false, cella.CodeQuotaExceeded
	if err := e.Grade(ctx); err != nil {
		t.Fatal(err)
	}
	if list, _ = st.Gradings().OfTrials(ctx, []string{trial.ID}); list[0].State != objects.GradingPending || len(cl.manifests) != 0 {
		t.Fatalf("a sandbox limit ended the checks: %+v", list)
	}
	if e2 := (&Engine{}); e2.Grade(ctx) != nil || (&Grading{}).Checks() || (*Grading)(nil).Judges() {
		t.Fatal("an engine with no grading graded")
	}
	if id, seq, ok := sessionRef("topos/sessions/ses_1/events@12"); !ok || id != "ses_1" || seq != 12 {
		t.Fatal("a session ref did not read")
	}
	for _, bad := range []string{"arca:x", "topos/sessions/ses_1", "topos/sessions//events@1", "topos/sessions/s/events@x"} {
		if _, _, ok := sessionRef(bad); ok {
			t.Errorf("%q read as a session ref", bad)
		}
	}
	if tail("abcdef", 3) != "def" || tail("ab", 3) != "ab" {
		t.Error("tail")
	}
}

// lapsing is an owner token source whose tokens expire on a clock of its
// own, held as the minter holds one: each ask answers the token held for
// the audience until OWNER_TOKEN_RENEWAL before its expiry, and a new
// one after. valid is a core's check of a token, refusing one past its
// expiry.
type lapsing struct {
	mu      sync.Mutex
	now     time.Time
	n       int
	expires map[string]time.Time
	held    map[string]string
}

func newLapsing() *lapsing {
	return &lapsing{now: time.Date(2026, 11, 1, 16, 0, 0, 0, time.UTC), expires: map[string]time.Time{}, held: map[string]string{}}
}

func (l *lapsing) Token(_ context.Context, r tokens.Request) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t, ok := l.held[r.Audience]; ok && l.now.Before(l.expires[t].Add(-limits.OwnerTokenRenewal)) {
		return t, nil
	}
	l.n++
	t := "tok-" + r.Audience + "-" + itoa(l.n)
	l.expires[t], l.held[r.Audience] = l.now.Add(5*time.Minute), t
	return t, nil
}

func (l *lapsing) valid(token string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	exp, ok := l.expires[token]
	return ok && l.now.Before(exp)
}

func (l *lapsing) advance(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = l.now.Add(d)
}

// TestCheckCallsCarryAFreshOwnerToken: a check that runs for minutes
// outlives the owner token the check sandbox was created with, as a
// minter's five-minute token does, so each later call on the sandbox, the
// next check's command, the kept outputs and the sandbox's delete, asks
// the source for the token again. With the token of the create the next
// check would read as not runnable, its outputs would not be kept, and
// the sandbox, refused its delete, would hold a place in the owner's
// sandbox limit until its time to live ends it.
func TestCheckCallsCarryAFreshOwnerToken(t *testing.T) {
	for _, long := range []string{"inventory", "run"} {
		t.Run("the "+long+" check runs long", func(t *testing.T) {
			st := pgtest.Open(t, false, postgres.Options{})
			stub := topostest.New(t)
			stub.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
			lp := newLapsing()
			fs := &files{objects: map[string][]byte{}, valid: lp.valid}
			cl := &sandboxes{archives: map[string][]byte{}, answers: map[string]cella.Result{}, valid: lp.valid}
			lx := &gateway{answers: []string{`{"kind":{"value":"game","rationale":"it scores"},"idea":{"value":"Snake","rationale":"a snake"}}`}}
			ctx := t.Context()
			g := seedGraded(ctx, t, st, fs, 1)
			e := gradingEngine(t, st, stub, &minter{}, fs, cl, lx)
			e.Grading.Tokens = lp
			cl.answers[g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 2}}`, Stderr: "entry: game.py", Duration: 300}
			cl.answers[g.runner] = cella.Result{ExitCode: 1, Stderr: "it ran", Duration: 900}
			// The long check takes six minutes of the trial's CHECK_TIMEOUT.
			digest := map[string]string{"inventory": g.inventory, "run": g.runner}[long]
			cl.ran = func(check string) {
				if check == digest {
					lp.advance(6 * time.Minute)
				}
			}
			if err := e.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			runSession(stub, fs, stub.Sessions()[0])
			if err := e.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if err := e.Grade(ctx); err != nil {
				t.Fatal(err)
			}
			if len(cl.execs) != 2 || len(cl.deleted) != 1 || cl.deleted[0] != "sbx_1" {
				t.Fatalf("%d checks ran and the sandboxes deleted are %v", len(cl.execs), cl.deleted)
			}
			trial := trialsOf(t, st, g.eid)[0]
			for i, raw := range trial.Checks {
				var run objects.CheckRun
				if err := json.Unmarshal(raw, &run); err != nil || run.State != objects.CheckOK || len(run.Outputs) != 2 {
					t.Fatalf("check %d's record %s %v", i, raw, err)
				}
			}
		})
	}
}
