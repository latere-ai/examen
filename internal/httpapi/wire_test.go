// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"errors"
	"testing"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/objects"
)

// A comparison's key is compared with the keys already recorded, so its
// bytes are fixed: members in order, a nil list null, <, > and & escaped.
func TestComparisonKeyBytes(t *testing.T) {
	d := objects.DeclaredComparison{Variable: "model", Levels: []string{"b<", "a&"}, Where: map[string]string{"z": "1", "a": "2"}, By: []string{"task"}}
	got, err := comparisonKey(d, "md")
	const want = `{"variable":"model","levels":["a\u0026","b\u003c"],"where":{"a":"2","z":"1"},"by":["task"],"metric":"md"}`
	if err != nil || got != want {
		t.Fatalf("%s, %v; want %s", got, err, want)
	}
}

// A body's errors name the member and its path, and a second value is its
// own refusal.
func TestDecodeErrorsNameTheMember(t *testing.T) {
	var v struct {
		Name string `json:"name"`
		In   struct {
			Count int `json:"count"`
		} `json:"in"`
	}
	for _, c := range []struct{ body, code, field string }{
		{`{"in":{"count":"x"}}`, apierr.CodeInvalidRequest, "in.count"},
		{`{"in":{"size":1}}`, apierr.CodeUnknownField, "size"},
		{`{"name":"a"}{"name":"b"}`, apierr.CodeInvalidRequest, ""},
		{`{"name":"a"} x`, apierr.CodeInvalidRequest, ""},
	} {
		err := decodeBytes([]byte(c.body), &v)
		var e *apierr.Error
		if !errors.As(err, &e) || e.Code != c.code || e.Field != c.field {
			t.Errorf("%s: %v", c.body, err)
		}
	}
}
