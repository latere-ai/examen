// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package goalless_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"latere.ai/x/pkg/authz"
	"latere.ai/x/pkg/authz/stub"

	"latere.ai/x/examen/authorizer"
)

// The ceilings hostedDecider holds a create to, low enough that an import
// meets the one on active experiments.
const (
	hostedMaxParallelism       = 16
	hostedMaxTrials            = 2000
	hostedMaxActiveExperiments = 4
)

// hostedDecider is the authorizer a hosted installation answers with, as
// far as an import and the live smoke test meet it. experiment.create is
// allowed only when no person's token acts for the experiment's work,
// which holds when every candidate is external and the suite names no
// check or judge grader: otherwise it is denied sign_in_not_kept, since a
// command line's sign-in is not kept for unattended work. It needs a
// spending cap and no Budget's name, which it denies budget, and its allow
// assigns the Budget it makes from the cap and holds the create to the
// executors it runs and to its ceilings. Every other question is allowed.
func hostedDecider(t *testing.T) *stub.Server {
	t.Helper()
	return stub.New(t, stub.WithVocabulary(authorizer.Vocabulary()), stub.WithAction(authorizer.ActionExperimentCreate, func(req authz.Request) any {
		res := req.Resource
		executors := strs(res.Fields[authorizer.FieldExecutors])
		kinds, listed := res.Fields[authorizer.FieldGraderKinds]
		ownerless := listed && len(executors) > 0 &&
			!slices.ContainsFunc(executors, func(e string) bool { return e != "external" }) &&
			!slices.ContainsFunc(strs(kinds), func(k string) bool { return k == "check" || k == "model_judge" || k == "agent_judge" })
		switch {
		case !ownerless:
			return map[string]any{"allow": false, "reason": "sign_in_not_kept"}
		case res.String(authorizer.FieldBudget) != "" || res.Fields[authorizer.FieldBudgetCapUSDMicro] == nil:
			return map[string]any{"allow": false, "reason": "budget"}
		}
		sum := sha256.Sum256([]byte(res.ID))
		return map[string]any{"allow": true, "limits": map[string]any{
			"budget": "budget-" + hex.EncodeToString(sum[:16]), "executors": []string{"external"},
			"max_parallelism": hostedMaxParallelism, "max_trials": hostedMaxTrials, "max_active_experiments": hostedMaxActiveExperiments,
		}}
	}))
}

// strs reads a field the envelope decoded as a list of strings.
func strs(v any) []string {
	var out []string
	if l, ok := v.([]any); ok {
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// Remote mode, against a hosted local examend through the variables an
// operator sets: the first run creates every experiment and imports every
// run; a second, with the bearer in a file, finds them all and imports
// nothing.
func TestRemoteMode(t *testing.T) {
	s, err := readStudy(synthetic(t))
	if err != nil {
		t.Fatal(err)
	}
	local := startServer(t, serverOptions{hosted: true})
	t.Setenv("EXAMEN_E2E_URL", local.base)
	t.Setenv("EXAMEN_E2E_TOKEN", local.token)
	first := target(t).importAndCheck(s, nil)
	t.Log(first.String())
	if !first.remote || len(first.exps) != 4 || first.imported != len(s.runs) || first.present != 0 || first.cells == 0 || first.comparisons == 0 {
		t.Fatalf("the first run: %+v", first)
	}
	for _, e := range first.exps {
		if e.found {
			t.Errorf("the first run found experiment %v", e.studies)
		}
	}

	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(local.token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EXAMEN_E2E_TOKEN", "")
	t.Setenv("EXAMEN_E2E_TOKEN_FILE", tokenFile)
	second := target(t).importAndCheck(s, nil)
	t.Log(second.String())
	if second.imported != 0 || second.present != len(s.runs) || second.cells != first.cells || second.comparisons != first.comparisons {
		t.Fatalf("the second run: %+v", second)
	}
	for i, e := range second.exps {
		if !e.found || e.id != first.exps[i].id {
			t.Errorf("the second run's experiment %v is %s, found %v; the first made %s", e.studies, e.id, e.found, first.exps[i].id)
		}
	}
	if !strings.Contains(second.String(), "0 created, 4 found") {
		t.Errorf("the report:\n%s", second)
	}
}

// remoteSettings are the variables a remote-mode run reads, written by
// TestLocalTarget.
const remoteSettings = "EXAMEN_E2E_URL=%s\nEXAMEN_E2E_TOKEN_FILE=%s\nEXAMEN_E2E_PROBES_URL=%s\n"

// TestLocalTarget serves a hosted local examend for a remote-mode run and
// the live smoke test to be rehearsed against, until the file it writes
// their variables to is removed. It is skipped unless EXAMEN_E2E_SERVE
// names that file; see README.md.
func TestLocalTarget(t *testing.T) {
	file := os.Getenv("EXAMEN_E2E_SERVE")
	if file == "" {
		t.Skip("EXAMEN_E2E_SERVE names no file to write a local target's variables to")
	}
	local := startServer(t, serverOptions{hosted: true})
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(local.token), 0o600); err != nil {
		t.Fatal(err)
	}
	origin := strings.TrimSuffix(local.base, hostedBasePath)
	settings := fmt.Sprintf(remoteSettings, local.base, tokenFile, origin)
	if err := os.WriteFile(file, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Logf("serving %s until %s is removed", local.base, file)
	deadline, bounded := t.Deadline()
	for {
		if _, err := os.Stat(file); errors.Is(err, fs.ErrNotExist) {
			return
		} else if err != nil {
			t.Fatal(err)
		}
		if bounded && time.Until(deadline) < time.Minute {
			t.Logf("the test's deadline is near; %s stops serving", local.base)
			return
		}
		time.Sleep(time.Second)
	}
}
