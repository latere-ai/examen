// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package analysis

import (
	"encoding/json/v2"
	"testing"
)

// A delta that is an object writes its labels in order whatever the options
// of the call that writes it, since the bytes are stored and compared.
func TestDeltaBytes(t *testing.T) {
	d := Delta{PerLabel: map[string]float64{"c": 0.5, "a": -1, "b": 0.25}}
	i := DeltaInterval{PerLabel: map[string]Interval{"b": {1, 2}, "a": {0, 3}}}
	for range 20 {
		got, err := json.Marshal(struct {
			D Delta         `json:"d"`
			I DeltaInterval `json:"i"`
			N Delta         `json:"n"`
		}{d, i, Delta{Value: 1.50}})
		if err != nil {
			t.Fatal(err)
		}
		const want = `{"d":{"a":-1,"b":0.25,"c":0.5},"i":{"a":[0,3],"b":[1,2]},"n":1.5}`
		if string(got) != want {
			t.Fatalf("%s, want %s", got, want)
		}
	}
}
