// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"latere.ai/x/examen/internal/id"
)

func event(seq uint64, typ, thread string, payload any) Event {
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return Event{ID: "evt_" + string(rune('a'+seq)), Seq: seq, Type: typ, Thread: thread, Time: time.Date(2026, 10, 19, 12, 0, int(seq), 0, time.UTC), Payload: raw}
}

func text(s string) map[string]any {
	return map[string]any{"message": map[string]any{"role": "assistant", "blocks": []map[string]any{{"type": "thinking", "text": "hm"}, {"type": "text", "text": s}}}}
}

// TestEvidenceOf folds a session's log into what its graders read: the log
// as Read renders it with its digest, the last answer of the session's own
// thread, and the kept copy of the working directory as its saves leave
// it, in path order.
func TestEvidenceOf(t *testing.T) {
	log := []Event{
		event(1, EventAgentMessage, "", text("Looking around.")),
		event(2, EventWorkSaved, "", map[string]any{"written": []WorkFile{{Path: "b.py", Size: 1, SHA256: "bb"}, {Path: "a.py", Size: 2, SHA256: "aa"}, {Path: "old.txt", SHA256: "oo"}}}),
		event(3, EventAgentMessage, "thr_1", text("a helper's answer")),
		event(4, EventWorkSaved, "", map[string]any{"written": []WorkFile{{Path: "a.py", Size: 3, SHA256: "a2"}}, "removed": []string{"old.txt"}, "unsaved": []string{"b.py"}}),
		event(5, EventAgentMessage, "", text("I made a game.")),
	}
	ev, err := EvidenceOf(log)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Read(Session{Status: StatusEnded, StopReason: StopCompleted}, log)
	if err != nil {
		t.Fatal(err)
	}
	if string(ev.Log) != string(r.Log) || ev.LogDigest != r.LogDigest {
		t.Fatal("the evidence's log is not the one Read renders")
	}
	if ev.FinalMessage != "I made a game." {
		t.Fatalf("the final message %q", ev.FinalMessage)
	}
	if len(ev.Work) != 1 || ev.Work[0] != (WorkFile{Path: "a.py", Size: 3, SHA256: "a2"}) || ev.Unvouched != 1 {
		t.Fatalf("the kept copy %+v, %d unvouched", ev.Work, ev.Unvouched)
	}
	whole, err := EvidenceOf(append(log, event(6, EventWorkSaved, "", map[string]any{"whole": true, "written": []WorkFile{{Path: "z.py", SHA256: "zz"}, {Path: "c.py", SHA256: "cc"}}})))
	if err != nil || len(whole.Work) != 2 || whole.Work[0].Path != "c.py" || whole.Unvouched != 0 {
		t.Fatalf("a whole save replaces the copy: %+v %v", whole.Work, err)
	}
	if WorkDigest(whole.Work) != id.FolderDigest([]id.FolderFile{{Path: "z.py", SHA256: "zz"}, {Path: "c.py", SHA256: "cc"}}) ||
		!strings.HasPrefix(WorkDigest(nil), "sha256:") {
		t.Fatal("the work digest is not the folder digest of its files")
	}
	for _, bad := range []Event{event(7, EventWorkSaved, "", "x"), event(7, EventAgentMessage, "", "x")} {
		if _, err := EvidenceOf([]Event{bad}); err == nil {
			t.Errorf("a %s that does not decode was read", bad.Type)
		}
	}
}
