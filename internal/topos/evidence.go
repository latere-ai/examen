// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"latere.ai/x/examen/internal/id"
)

// EventWorkSaved is the event a session records each save of the copy of
// its working directory in its folder by: the files a turn's end wrote and
// removed (Topos spec 073).
const EventWorkSaved = "work.saved"

// WorkFile is one file of a session's kept copy of its working directory:
// its path relative to the working directory, its size, its permission
// bits and the hex SHA-256 of its bytes, as work.saved records it.
type WorkFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
	SHA256 string `json:"sha256"`
}

// workSaved is what the evidence reads of a work.saved.
type workSaved struct {
	Whole   bool       `json:"whole"`
	Written []WorkFile `json:"written"`
	Removed []string   `json:"removed"`
	Unsaved []string   `json:"unsaved"`
}

// Evidence is what a trial's graders read of its session's log: the log as
// JSONL through its last event and its SHA-256, the agent's final message,
// and the kept copy of its working directory as the log's saves fold it,
// in ascending bytewise order of path. Unvouched counts the paths a save
// changed and did not write, whose files the copy no longer vouches for
// and the copy leaves out.
type Evidence struct {
	Log          []byte
	LogDigest    string
	FinalMessage string
	Work         []WorkFile
	Unvouched    int
}

// EvidenceOf reads a session's log as its trial's evidence. The log is
// rendered as Read renders it, so the digest is the one the trial's
// transcript evidence names.
func EvidenceOf(events []Event) (Evidence, error) {
	var ev Evidence
	work := map[string]WorkFile{}
	unvouched := map[string]bool{}
	for _, e := range events {
		line, err := json.Marshal(e, logJSON)
		if err != nil {
			return ev, fmt.Errorf("topos: encode event %d: %w", e.Seq, err)
		}
		ev.Log = append(append(ev.Log, line...), '\n')
		switch e.Type {
		case EventAgentMessage:
			if e.Thread != "" {
				continue
			}
			var p struct {
				Message struct {
					Blocks []struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"blocks"`
				} `json:"message"`
			}
			if err := json.Unmarshal(e.Payload, &p, foreignJSON); err != nil {
				return ev, fmt.Errorf("topos: decode agent.message %d: %w", e.Seq, err)
			}
			var text []string
			for _, b := range p.Message.Blocks {
				if b.Type == "text" && b.Text != "" {
					text = append(text, b.Text)
				}
			}
			ev.FinalMessage = strings.Join(text, "\n\n")
		case EventWorkSaved:
			var w workSaved
			if err := json.Unmarshal(e.Payload, &w, foreignJSON); err != nil {
				return ev, fmt.Errorf("topos: decode work.saved %d: %w", e.Seq, err)
			}
			if w.Whole {
				clear(work)
				clear(unvouched)
			}
			for _, f := range w.Written {
				work[f.Path] = f
				delete(unvouched, f.Path)
			}
			for _, p := range w.Removed {
				delete(work, p)
				delete(unvouched, p)
			}
			for _, p := range w.Unsaved {
				delete(work, p)
				unvouched[p] = true
			}
		}
	}
	sum := sha256.Sum256(ev.Log)
	ev.LogDigest = "sha256:" + hex.EncodeToString(sum[:])
	for _, f := range work {
		ev.Work = append(ev.Work, f)
	}
	slices.SortFunc(ev.Work, func(a, b WorkFile) int { return strings.Compare(a.Path, b.Path) })
	ev.Unvouched = len(unvouched)
	return ev, nil
}

// WorkDigest is the digest of a kept copy of a working directory as a
// folder (id.FolderDigest).
func WorkDigest(files []WorkFile) string {
	list := make([]id.FolderFile, len(files))
	for i, f := range files {
		list[i] = id.FolderFile{Path: f.Path, SHA256: f.SHA256}
	}
	return id.FolderDigest(list)
}
