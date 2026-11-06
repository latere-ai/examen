// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package objects

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"
)

// A raw member is left out when it is absent and written when it is there,
// even when it is an empty object, an empty list or null.
func TestRawMembersAreLeftOutOnlyWhenAbsent(t *testing.T) {
	for _, c := range []struct {
		config jsontext.Value
		want   string
	}{
		{nil, `{"image":"img"}`},
		{jsontext.Value(`{}`), `{"image":"img","config":{}}`},
		{jsontext.Value(`[]`), `{"image":"img","config":[]}`},
		{jsontext.Value(`null`), `{"image":"img","config":null}`},
	} {
		got, err := json.Marshal(Program{Image: "img", Config: c.config})
		if err != nil || string(got) != c.want {
			t.Errorf("%s: %s, %v; want %s", c.config, got, err, c.want)
		}
	}
}

// A time pointer is left out only when it is nil: one that points at the
// zero time is written.
func TestTimePointersAreLeftOutOnlyWhenNil(t *testing.T) {
	var zero time.Time
	got, err := json.Marshal(Trial{StartedAt: &zero})
	if err != nil || !strings.Contains(string(got), `"started_at":"0001-01-01T00:00:00Z"`) || strings.Contains(string(got), "ended_at") {
		t.Errorf("%s, %v", got, err)
	}
}
