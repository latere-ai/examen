// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/arca"
	"latere.ai/x/examen/internal/cella"
	"latere.ai/x/examen/internal/grade"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/objects"
	tokens "latere.ai/x/examen/internal/owner"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/topos"
)

// checkFilesLimit bounds the bytes of one check grader's files.
const checkFilesLimit = 64 << 20

// stderrTail is how much of a check's stderr its record keeps.
const stderrTail = 2 << 10

// quotaWait is how long a trial's checks wait for a place in the owner's
// sandbox limit, which the runs of the experiment may hold.
const quotaWait = 2 * time.Minute

// sandboxPaths are where the check sandbox holds what examend writes into
// it, under its workspace: Cella writes only there, and the image's root
// is read-only.
type sandboxPaths struct{ root string }

func (p sandboxPaths) at(rel string) string     { return path.Join(p.root, rel) }
func (p sandboxPaths) workspace() string        { return p.at("work") }
func (p sandboxPaths) examen(rel string) string { return p.at("examen/" + rel) }

// checkGrader is one check of a trial: its grading and its grader.
type checkGrader struct {
	row    objects.Grading
	grader objects.Grader
	files  []workFile
	// notRun is why the check does not run, "" for one that runs.
	notRun string
}

// runChecks runs a trial's checks in order in one check sandbox
// (specs/005-execution.md, "The check sandbox"): a fresh Cella sandbox
// created as the owner with no network, into which examend writes the
// run's kept working directory as its workspace/ output with the output
// manifest, the session's log and final message, and each check's files;
// each check runs through Cella's exec with spec 006's environment under
// CHECK_TIMEOUT for all of them, and the sandbox is deleted.
func (e *Engine) runChecks(ctx context.Context, rows []objects.Grading) error {
	slices.SortFunc(rows, func(a, b objects.Grading) int { return a.Position - b.Position })
	first := rows[0]
	endAll := func(state, reason string, checks []checkGrader) error {
		var errs []error
		for _, c := range checks {
			errs = append(errs, e.Store.Gradings().End(ctx, store.GradingEnd{ID: c.row.ID, State: state,
				Record: objects.CheckRun{Grader: c.row.Grader, State: state, Reason: reason}}))
		}
		return errors.Join(errs...)
	}
	checks := make([]checkGrader, len(rows))
	for i, r := range rows {
		checks[i].row = r
	}
	wait := func(cause error) error {
		var errs []error
		for _, c := range checks {
			errs = append(errs, e.later(ctx, c.row, func(reason string) store.GradingEnd {
				return store.GradingEnd{ID: c.row.ID, State: objects.CheckNotRun, Record: objects.CheckRun{Grader: c.row.Grader, State: objects.CheckNotRun, Reason: reason}}
			}, cause))
		}
		return errors.Join(errs...)
	}
	r, err := e.runOfTrial(ctx, first.Trial, tokens.PurposeTrial)
	why, ended := signInEnded(err)
	switch {
	case errors.Is(err, errEvidenceChanged):
		return endAll(objects.CheckNotRun, "evidence_changed: "+err.Error(), checks)
	case ended:
		return endAll(objects.CheckNotRun, why, checks)
	case outage(err):
		return wait(err)
	case err != nil:
		return err
	}
	for i := range checks {
		g, _, err := e.graderOf(ctx, r.exp.Owner, checks[i].row.Grader)
		if err != nil {
			return err
		}
		checks[i].grader = g
	}
	work, err := e.readWork(ctx, r, tokens.PurposeTrial)
	why, ended = signInEnded(err)
	switch {
	case errors.Is(err, errEvidenceChanged):
		return endAll(objects.CheckNotRun, "evidence_changed: "+err.Error(), checks)
	case ended:
		return endAll(objects.CheckNotRun, why, checks)
	case outage(err):
		return wait(err)
	case err != nil:
		return err
	}
	for i := range checks {
		files, why, err := e.checkFiles(ctx, r, checks[i].grader)
		if reason, ended := signInEnded(err); ended {
			return endAll(objects.CheckNotRun, reason, checks)
		}
		switch {
		case outage(err):
			return wait(err)
		case err != nil:
			return err
		}
		checks[i].files, checks[i].notRun = files, why
	}
	g := e.Grading
	token, err := g.Tokens.Token(ctx, tokens.Request{Owner: r.exp.Owner, Subject: r.exp.CreatedBy, Audience: g.CellaAudience, Experiment: r.exp.ID, Purpose: tokens.PurposeTrial})
	why, ended = signInEnded(err)
	switch {
	case ended:
		return endAll(objects.CheckNotRun, why, checks)
	case outage(err):
		return wait(err)
	case err != nil:
		return err
	}
	labels := map[string]string{}
	if l, ok := g.Tokens.(tokens.Labeler); ok {
		if labels, err = l.Labels(ctx, tokens.Request{Owner: r.exp.Owner, Subject: r.exp.CreatedBy, Audience: g.CellaAudience, Experiment: r.exp.ID, Purpose: tokens.PurposeTrial}); err != nil {
			return wait(err)
		}
	}
	labels["examen/experiment"], labels["examen/trial"] = strings.ToLower(r.exp.ID), strings.ToLower(r.trial.ID)
	sb, err := g.Cella.Create(ctx, token, cella.Spec{Image: g.Image, Environment: g.Environment, TTL: holdChecks, Labels: labels}, sandboxStart)
	var refused *cella.RefusedError
	switch {
	case errors.As(err, &refused) && refused.Code == cella.CodeQuotaExceeded:
		var errs []error
		for _, c := range checks {
			errs = append(errs, e.Store.Gradings().Wait(ctx, c.row.ID, quotaWait, c.row.Attempts))
		}
		e.log().InfoContext(ctx, "a trial's checks wait for a place in the owner's sandbox limit", "trial", r.trial.ID)
		return errors.Join(errs...)
	case errors.As(err, &refused):
		return endAll(objects.CheckNotRun, "Cella refused the check sandbox: "+refused.Error(), checks)
	case outage(err):
		return wait(err)
	case err != nil:
		return err
	}
	defer func() {
		if err := g.Cella.Delete(context.WithoutCancel(ctx), token, sb.ID); err != nil {
			e.log().WarnContext(ctx, "a check sandbox was not deleted; its time to live ends it", "trial", r.trial.ID, "sandbox", sb.ID, "error", apierr.LogText(err))
		}
	}()
	if sb.Phase != cella.PhaseRunning {
		return wait(fmt.Errorf("%w: the check sandbox is %s: %s", cella.ErrUnavailable, sb.Phase, sb.Reason))
	}
	paths := sandboxPaths{root: sb.Workspace}
	archive, err := checkArchive(r, work, checks)
	if err != nil {
		return err
	}
	if err := g.Cella.PutTar(ctx, token, sb.ID, paths.root, archive); err != nil {
		if outage(err) {
			return wait(err)
		}
		return endAll(objects.CheckNotRun, "the run's files were not written into the check sandbox: "+apierr.LogText(err), checks)
	}
	deadline := e.now().Add(limits.CheckTimeout)
	for _, c := range checks {
		run := objects.CheckRun{Grader: c.row.Grader, Image: sb.Image}
		end := store.GradingEnd{ID: c.row.ID, Record: &run}
		left := deadline.Sub(e.now())
		switch {
		case c.notRun != "":
			run.State, run.Reason = objects.CheckNotRun, c.notRun
		case left <= time.Second:
			run.State, run.Reason = objects.CheckNotRun, "CHECK_TIMEOUT had run out before the check started"
		default:
			res, err := g.Cella.Run(ctx, token, sb.ID, cella.Exec{
				Command: c.grader.Check.Command, Env: checkEnv(r, c, paths), Workdir: path.Join(paths.workspace(), c.grader.Check.Workdir), Timeout: left,
			})
			if outage(err) {
				return wait(err)
			}
			exit := grade.Exit{Code: res.ExitCode, Stdout: res.Stdout}
			if err != nil {
				exit.NotRunnable = true
				run.Reason = apierr.LogText(err)
			}
			exit.TimedOut = err == nil && res.ExitCode == 124 && time.Duration(res.Duration)*time.Millisecond >= left-2*time.Second
			exit.NotRunnable = exit.NotRunnable || ((res.ExitCode == 126 || res.ExitCode == 127) && strings.TrimSpace(res.Stdout) == "")
			read := grade.ReadCheck(produced(c.grader, r.metrics), exit)
			run.State = read.State
			if read.Reason != "" {
				run.Reason = strings.TrimPrefix(strings.Join([]string{run.Reason, read.Reason}, "; "), "; ")
			}
			if err == nil {
				code := res.ExitCode
				run.ExitCode = &code
			}
			if res.Truncated {
				run.Reason = strings.TrimPrefix(run.Reason+"; an output past a mebibyte was cut by Cella", "; ")
			}
			sum := sha256.Sum256([]byte(res.Stdout))
			run.Duration = float64(res.Duration) / 1000
			run.StdoutSize, run.StdoutSHA256 = int64(len(res.Stdout)), "sha256:"+hex.EncodeToString(sum[:])
			run.StderrTail = tail(res.Stderr, stderrTail)
			end.Values = grade.Applying(read.Values, r.metrics, r.trial.Outcome)
		}
		end.State = run.State
		if err := e.Store.Gradings().End(ctx, end); err != nil {
			return err
		}
	}
	return nil
}

// tail is the last n bytes of s, from a rune's start.
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[len(s)-n:]
	for i := 0; i < len(s) && i < 4; i++ {
		if s[i]&0xC0 != 0x80 {
			return s[i:]
		}
	}
	return s
}

// checkEnv is a check's environment (specs/006-grading.md): the EXAMEN_
// variables and the grader's own. The trial's model key is in none of it.
func checkEnv(r gradedRun, c checkGrader, p sandboxPaths) map[string]string {
	env := map[string]string{}
	maps.Copy(env, c.grader.Check.Env)
	env["EXAMEN_TRIAL"] = r.trial.ID
	env["EXAMEN_TASK"] = r.taskDigest
	env["EXAMEN_CHECK"] = c.row.Grader
	env["EXAMEN_WORKSPACE"] = p.workspace()
	env["EXAMEN_TRANSCRIPT"] = p.examen("transcript.jsonl")
	env["EXAMEN_FINAL_MESSAGE"] = p.examen("final_message.txt")
	env["EXAMEN_INPUTS"] = p.examen("inputs")
	env["EXAMEN_OUTPUTS"] = p.examen("outputs")
	env["EXAMEN_OUTPUTS_MANIFEST"] = p.examen("outputs.manifest.json")
	env["EXAMEN_REFERENCE"] = p.examen("reference")
	if c.grader.Check.Files != nil {
		env["EXAMEN_CHECK_FILES"] = p.examen("check-files/" + graderDir(c.row.Grader))
	}
	return env
}

// graderDir is the directory a check's files are written to: the hex of
// its grader's digest.
func graderDir(digest string) string { return strings.TrimPrefix(digest, id.DigestPrefix) }

// manifestFile is one entry of a trial's output manifest
// (specs/005-execution.md): a file of an output, its size and SHA-256,
// and whether its bytes were kept.
type manifestFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Kept   bool   `json:"kept"`
}

// archive is a tar stream being written, keeping the first error a write
// met, after which it writes nothing.
type archive struct {
	tw   *tar.Writer
	dirs map[string]bool
	err  error
}

// dir writes a directory and each one above it not yet written.
func (a *archive) dir(p string) {
	for _, d := range parents(p) {
		if a.err != nil || a.dirs[d] {
			continue
		}
		a.dirs[d] = true
		a.err = a.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: d + "/", Mode: 0o755, ModTime: time.Unix(0, 0).UTC()})
	}
}

// file writes a regular file with its mode, and the directories above it.
func (a *archive) file(p string, mode int64, data []byte) {
	a.dir(path.Dir(p))
	if a.err == nil {
		a.err = a.tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: p, Mode: mode, Size: int64(len(data)), ModTime: time.Unix(0, 0).UTC()})
	}
	if a.err == nil {
		_, a.err = a.tw.Write(data)
	}
}

// checkArchive is the tar stream written below the check sandbox's
// workspace: the kept working directory under examen/outputs/workspace/,
// the output manifest, the log and the final message, each check's files,
// and the empty directories of the inputs, the references and the
// workspace the checks run in.
func checkArchive(r gradedRun, work []workFile, checks []checkGrader) ([]byte, error) {
	var buf bytes.Buffer
	a := &archive{tw: tar.NewWriter(&buf), dirs: map[string]bool{}}
	for _, d := range []string{"work", "examen/inputs", "examen/reference", "examen/outputs/workspace"} {
		a.dir(d)
	}
	manifest := make([]manifestFile, 0, len(work))
	for _, f := range work {
		manifest = append(manifest, manifestFile{Path: "workspace/" + f.Path, Size: f.Size, SHA256: "sha256:" + f.SHA256, Kept: f.Data != nil})
		if f.Data != nil {
			a.file("examen/outputs/workspace/"+f.Path, int64(fs.FileMode(f.Mode).Perm()|0o600), f.Data)
		}
	}
	raw, err := json.Marshal(map[string]any{"files": manifest})
	if err != nil {
		return nil, fmt.Errorf("work: render the output manifest: %w", err)
	}
	a.file("examen/outputs.manifest.json", 0o644, raw)
	a.file("examen/transcript.jsonl", 0o644, r.ev.Log)
	a.file("examen/final_message.txt", 0o644, []byte(r.ev.FinalMessage))
	for _, c := range checks {
		base := "examen/check-files/" + graderDir(c.row.Grader)
		if c.grader.Check.Files != nil {
			a.dir(base)
		}
		for _, f := range c.files {
			a.file(base+"/"+f.Path, int64(fs.FileMode(f.Mode).Perm()|0o400)&^0o222, f.Data)
		}
	}
	if a.err == nil {
		a.err = a.tw.Close()
	}
	if a.err != nil {
		return nil, fmt.Errorf("work: write the check archive: %w", a.err)
	}
	return buf.Bytes(), nil
}

// parents are a relative path and each directory above it, outermost
// first.
func parents(p string) []string {
	if p == "." || p == "" {
		return nil
	}
	var out []string
	for d := p; d != "." && d != "/" && d != ""; d = path.Dir(d) {
		out = append(out, d)
	}
	slices.Reverse(out)
	return out
}

// checkFiles reads a check grader's files, an Arca folder of the owner's
// pinned by its content digest (specs/006-grading.md), each file's
// SHA-256 computed as it is read. A folder whose digest is not the pinned
// one, or whose files pass checkFilesLimit, is a check that does not run,
// with why.
func (e *Engine) checkFiles(ctx context.Context, r gradedRun, g objects.Grader) ([]workFile, string, error) {
	ref := g.Check.Files
	if ref == nil {
		return nil, "", nil
	}
	gr := e.Grading
	token, err := gr.Tokens.Token(ctx, tokens.Request{Owner: r.exp.Owner, Subject: r.exp.CreatedBy, Audience: gr.ArcaAudience, Experiment: r.exp.ID, Purpose: tokens.PurposeTrial})
	if err != nil {
		return nil, "", err
	}
	folder := strings.Trim(ref.Path, "/")
	entries, err := gr.Arca.List(ctx, token, r.exp.Owner, folder)
	var refused *arca.RefusedError
	if errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusForbidden) {
		return nil, "files_changed: the check's files are not readable at " + folder + ": " + refused.Error(), nil
	}
	if err != nil {
		return nil, "", err
	}
	var out []workFile
	var list []id.FolderFile
	var total int64
	for _, en := range entries {
		rel, ok := strings.CutPrefix(en.Path, folder+"/")
		if !ok || rel == "" {
			continue
		}
		if total += en.Size; total > checkFilesLimit {
			return nil, "files_over_limit: the check's files hold more than 64 MiB", nil
		}
		data, err := gr.Arca.Read(ctx, token, r.exp.Owner, en.Path, checkFilesLimit)
		if err != nil {
			return nil, "", err
		}
		sum := hexSHA(data)
		mode := uint32(0o644)
		if isScript(data) {
			mode = 0o755
		}
		out = append(out, workFile{WorkFile: workFileOf(rel, data, sum, mode), Data: data})
		list = append(list, id.FolderFile{Path: rel, SHA256: sum})
	}
	if got := id.FolderDigest(list); got != ref.Digest {
		return nil, "files_changed: the check's files hold the digest " + got + ", and the grader pins " + ref.Digest, nil
	}
	return out, "", nil
}

// isScript reports whether a file starts with a shebang, so a check's
// script is executable where it is written.
func isScript(data []byte) bool { return bytes.HasPrefix(data, []byte("#!")) }

// workFileOf is a file read for a check: its path, size, mode and digest.
func workFileOf(rel string, data []byte, sum string, mode uint32) topos.WorkFile {
	return topos.WorkFile{Path: rel, Size: int64(len(data)), Mode: mode, SHA256: sum}
}

func hexSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
