// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"archive/tar"
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/origo"
	"latere.ai/x/examen/internal/origo/origotest"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
	"latere.ai/x/examen/internal/topos/topostest"
)

const originCommit = "cccccccccccccccccccccccccccccccccccccccc"

func textOf(s string) *string { return &s }

// archiveOf reads the tar a check sandbox received: each entry's bytes and
// mode by name.
func archiveOf(t *testing.T, raw []byte) (map[string]string, map[string]int64) {
	t.Helper()
	data, modes := map[string]string{}, map[string]int64{}
	tr := tar.NewReader(bytes.NewReader(raw))
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return data, modes
		}
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		data[h.Name], modes[h.Name] = string(b), h.Mode
	}
}

// referenced is a bench whose task holds a reference of each form and
// whose Origo is a stub, with the references' bytes in their stores.
type referenced struct {
	*bench
	origo *origotest.Stub
	refs  []objects.FileSource
}

const (
	textBody  = "SECRET text answer\n"
	arcaBody  = "SECRET,arca,table\n1,2,3\n"
	origoBody = `{"SECRET":"origo key"}` + "\n"
)

// newReferenced seeds a task with a text, an Arca and an Origo reference;
// change may alter them (and the stores) before the trial is graded.
func newReferenced(t *testing.T, st store.Store, change func(r *referenced)) *referenced {
	t.Helper()
	r := &referenced{origo: origotest.New(t, "acme/gold", originCommit, "tok-origo")}
	r.origo.Put("gold/key.json", []byte(origoBody))
	arcaPath := "files/evals/reference/table.csv"
	r.refs = []objects.FileSource{
		{Name: "notes.txt", Text: textOf(textBody)},
		{Name: "table.csv", Arca: &objects.ArcaRef{Path: arcaPath, Digest: "sha256:" + sha([]byte(arcaBody))}},
		{Name: "key.json", Origo: &objects.OrigoRef{Repository: "acme/gold", Commit: originCommit, Path: "gold/key.json"}},
	}
	if change != nil {
		change(r)
	}
	r.bench = newBenchRefs(t, st, runSession, r.refs)
	r.fs.put(arcaPath, []byte(arcaBody))
	r.e.Grading.Origo, r.e.Grading.OrigoAudience = origo.New(r.origo.URL, r.origo.Client()), "origo"
	r.cl.answers[r.g.inventory] = cella.Result{Stdout: `{"metrics": {"entry_found": true, "size": 2}}`}
	r.cl.answers[r.g.runner] = cella.Result{}
	return r
}

// TestChecksReceiveTheTasksReferences: the tar the check sandbox receives
// holds each reference of the task at examen/reference/<name>, read-only,
// whichever form it is in: text as it stands, an Arca path read as the
// owner, an Origo path at its commit read with an owner token for Origo's
// audience; and the checks run.
func TestChecksReceiveTheTasksReferences(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	r := newReferenced(t, st, nil)
	r.grade(t)
	if len(r.cl.execs) != 2 {
		t.Fatalf("%d checks ran, want 2: %+v", len(r.cl.execs), record[objects.CheckRun](t, r.gradings(t)[0]))
	}
	data, modes := archiveOf(t, r.cl.archives["sbx_1"])
	for name, want := range map[string]string{"notes.txt": textBody, "table.csv": arcaBody, "key.json": origoBody} {
		at := "examen/reference/" + name
		if data[at] != want {
			t.Errorf("%s holds %q, want %q", at, data[at], want)
		}
		if modes[at]&0o222 != 0 || modes[at]&0o400 == 0 {
			t.Errorf("%s has mode %o, want read-only", at, modes[at])
		}
	}
	if env := r.cl.execs[0]["env"].(map[string]any); env["EXAMEN_REFERENCE"] != "/workspace/examen/reference" {
		t.Errorf("EXAMEN_REFERENCE is %v", env["EXAMEN_REFERENCE"])
	}
	if len(r.origo.Tokens) == 0 {
		t.Fatal("Origo was not called")
	}
	for _, tok := range r.origo.Tokens {
		if tok != "tok-origo" {
			t.Errorf("Origo was called with %q", tok)
		}
	}
	for _, a := range r.m.asks {
		if a.Audience == "origo" && (a.Owner != owner || a.Experiment != r.g.eid) {
			t.Errorf("the Origo token was asked as %+v", a)
		}
	}
}

// TestAReferenceThatCannotBeDeliveredEndsTheChecks: a reference that is
// larger than REFERENCE_MAX_BYTES, whose digest is not the one the task
// pins, or that its store refuses ends every check of the trial not run,
// with a reason that names the reference and none of its bytes, and no
// reference reaches the check sandbox, those that could be read included;
// a store that does not answer leaves the checks to be tried again.
func TestAReferenceThatCannotBeDeliveredEndsTheChecks(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	half := bytes.Repeat([]byte("S"), limits.ReferenceMaxBytes/2+1)
	huge := bytes.Repeat([]byte("S"), limits.ReferenceMaxBytes+1)
	for _, c := range []struct {
		name   string
		change func(r *referenced)
		prefix string
		ref    string
	}{
		{"a digest that is not the pinned one", func(r *referenced) {
			r.refs[1].Arca.Digest = "sha256:" + sha([]byte("another table"))
		}, "reference_changed", `"table.csv"`},
		{"an Arca reference over the limit", func(r *referenced) {
			r.refs[1].Arca.Digest = "sha256:" + sha(huge)
		}, "reference_over_limit", `"table.csv"`},
		{"an Origo reference over the limit", func(r *referenced) {
			r.origo.Put("gold/key.json", huge)
		}, "reference_over_limit", `"key.json"`},
		{"references that are each under the limit and over it together", func(r *referenced) {
			r.refs[1].Arca.Digest = "sha256:" + sha(half)
			r.origo.Put("gold/key.json", half)
		}, "reference_over_limit", `"key.json"`},
		{"an Origo path the commit lacks", func(r *referenced) {
			r.refs[2].Origo.Path = "gold/missing.json"
		}, "reference_unreadable", `"key.json"`},
		{"an Origo that refuses the owner token", func(r *referenced) {
			r.origo.Token = "another"
		}, "reference_unreadable", `"key.json"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newReferenced(t, st, c.change)
			switch c.name {
			case "an Arca reference over the limit":
				r.fs.put("files/evals/reference/table.csv", huge)
			case "references that are each under the limit and over it together":
				r.fs.put("files/evals/reference/table.csv", half)
			}
			r.grade(t)
			if len(r.cl.execs) != 0 {
				t.Fatalf("%d checks ran", len(r.cl.execs))
			}
			list := r.gradings(t)
			for _, g := range list[:2] {
				run := record[objects.CheckRun](t, g)
				if run.State != objects.CheckNotRun || !strings.HasPrefix(run.Reason, c.prefix+": ") || !strings.Contains(run.Reason, "the reference "+c.ref) || strings.Contains(run.Reason, "SECRET") {
					t.Errorf("the check's record %+v", run)
				}
			}
			data, _ := archiveOf(t, r.cl.archives["sbx_1"])
			for name := range data {
				if strings.HasPrefix(name, "examen/reference/") && name != "examen/reference/" {
					t.Errorf("%s was delivered", name)
				}
			}
		})
	}

	r := newReferenced(t, st, nil)
	r.origo.Status = 503
	r.grade(t)
	if g := r.gradings(t)[0]; g.State != objects.GradingPending || g.Attempts != 1 || len(r.cl.execs) != 0 || len(r.cl.manifests) != 0 {
		t.Fatalf("an Origo that did not answer: %+v after %d sandboxes", g, len(r.cl.manifests))
	}
}

// leaks lists what the sent text holds of the references: a reference's
// name, its path, or any of the bytes given.
func leaks(sent string, refs []objects.FileSource, bodies ...string) []string {
	var out []string
	for _, f := range refs {
		needles := append([]string{f.Name}, bodies...)
		if f.Origo != nil {
			needles = append(needles, f.Origo.Path)
		}
		if f.Arca != nil {
			needles = append(needles, f.Arca.Path)
		}
		for _, n := range needles {
			if strings.Contains(sent, n) {
				out = append(out, f.Name+": "+n)
			}
		}
	}
	return out
}

// noReference fails the test when what the system under test is sent
// holds a reference.
func noReference(t *testing.T, where, sent string, refs []objects.FileSource, bodies ...string) {
	t.Helper()
	for _, l := range leaks(sent, refs, bodies...) {
		t.Errorf("%s carries a reference (%s)", where, l)
	}
}

// TestASessionIsSentNoReference: the body posted to Topos to start a
// session of a task that holds references of each form carries neither
// a reference's name, nor its path, nor its bytes: references reach the
// task's checks alone, never the system under test.
func TestASessionIsSentNoReference(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	stub := topostest.New(t)
	stub.Agent("helper", topostest.Version{Version: 3, Digest: agentDigest})
	e := toposEngine(st, stub, &minter{}, &toposGate{})
	refs := []objects.FileSource{
		{Name: "notes.txt", Text: textOf(textBody)},
		{Name: "table.csv", Arca: &objects.ArcaRef{Path: "files/evals/reference/table.csv", Digest: "sha256:" + sha([]byte(arcaBody))}},
		{Name: "key.json", Origo: &objects.OrigoRef{Repository: "acme/seed", Commit: originCommit, Path: "gold/key.json"}},
	}
	seedToposRefs(t.Context(), t, st, 1, refs)
	if err := e.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	sessions := stub.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("%d sessions", len(sessions))
	}
	sent, err := jsonText(stub.Body(sessions[0]))
	if err != nil {
		t.Fatal(err)
	}
	noReference(t, "the session's body", sent, refs, textBody, arcaBody, origoBody, "SECRET")
	if !strings.Contains(sent, "Do something you want.") {
		t.Fatalf("the session's body is not the create: %s", sent)
	}
}

// TestTheLeakCheckSeesAReference: the check that a session or a lease is
// sent no reference finds one in a body that holds a name, a path or
// bytes, and none in a body that holds none.
func TestTheLeakCheckSeesAReference(t *testing.T) {
	refs := []objects.FileSource{{Name: "notes.txt", Text: textOf(textBody)},
		{Name: "key.json", Origo: &objects.OrigoRef{Repository: "acme/gold", Commit: originCommit, Path: "gold/key.json"}}}
	for _, leak := range []string{`{"inputs":[{"name":"notes.txt"}]}`, `{"message":"SECRET text"}`, `{"file":"gold/key.json"}`} {
		if len(leaks(leak, refs, "SECRET")) == 0 {
			t.Errorf("a body holding a reference passed: %s", leak)
		}
	}
	if l := leaks(`{"message":"Do something you want."}`, refs, "SECRET"); len(l) != 0 {
		t.Errorf("a body holding none was found to: %v", l)
	}
}

// jsonText renders a decoded body as the text the leak check searches.
func jsonText(v any) (string, error) {
	raw, err := json.Marshal(v)
	return string(raw), err
}
