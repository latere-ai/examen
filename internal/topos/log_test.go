// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package topos

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
	"time"
)

// A log line is the bytes a log digest names: the payload as the agent
// wrote it, whether it repeats a member, spells a character as an escape
// or holds bytes that are not UTF-8; <, > and & escaped; no whitespace.
func TestLogLineBytes(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 600, time.UTC)
	for _, c := range []struct{ payload, want string }{
		{"{ \"a\" : \"<x>&\\u00e9\" ,\n \"a\":1.50}", `{"id":"e1","seq":1,"type":"agent.message","time":"2026-01-02T03:04:05.0000006Z","payload":{"a":"\u003cx\u003e\u0026\u00e9","a":1.50}}`},
		{"{\"s\":\"a\xffb\"}", "{\"id\":\"e1\",\"seq\":1,\"type\":\"agent.message\",\"time\":\"2026-01-02T03:04:05.0000006Z\",\"payload\":{\"s\":\"a\xffb\"}}"},
		{`null`, `{"id":"e1","seq":1,"type":"agent.message","time":"2026-01-02T03:04:05.0000006Z","payload":null}`},
	} {
		got, err := json.Marshal(Event{ID: "e1", Seq: 1, Type: EventAgentMessage, Time: at, Payload: jsontext.Value(c.payload)}, logJSON)
		if err != nil || string(got) != c.want {
			t.Errorf("%q: %q, %v; want %q", c.payload, got, err, c.want)
		}
	}
}

// A payload an agent wrote is read whatever its shape: a repeated member
// keeps its last value and a name matches in any letter case.
func TestPayloadFromAnAgent(t *testing.T) {
	events := []Event{{Seq: 1, Type: EventSessionError, Time: time.Unix(1, 0).UTC(), Payload: jsontext.Value(`{"Code":"x","code":"quota_exceeded"}`)}}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(events[0].Payload, &p, foreignJSON); err != nil || p.Code != "quota_exceeded" {
		t.Fatalf("%+v %v", p, err)
	}
}
