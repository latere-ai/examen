// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

func request(t *testing.T, target, body string) *http.Request {
	t.Helper()
	return httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
}

// A body is one JSON object with no member the contract does not
// describe; the errors name the member.
func TestDecode(t *testing.T) {
	var v struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := decode(request(t, "/", `{"name":"a","count":2}`), &v); err != nil || v.Name != "a" || v.Count != 2 {
		t.Fatalf("%+v %v", v, err)
	}
	for _, c := range []struct{ body, code, field string }{
		{`{"name":"a","colour":"red"}`, apierr.CodeUnknownField, "colour"},
		{`{"count":"two"}`, apierr.CodeInvalidRequest, "count"},
		{`not json`, apierr.CodeInvalidRequest, ""},
		{`{"name":"a"} {"name":"b"}`, apierr.CodeInvalidRequest, ""},
		{``, apierr.CodeInvalidRequest, ""},
	} {
		err := decode(request(t, "/", c.body), &v)
		var e *apierr.Error
		if !errors.As(err, &e) || e.Code != c.code || e.Field != c.field || e.Detail == "" {
			t.Errorf("%q: %v", c.body, err)
		}
	}
	r := request(t, "/", "{}")
	r.Body = errBody{}
	var e *apierr.Error
	if err := decode(r, &v); !errors.As(err, &e) || e.Code != apierr.CodeInvalidRequest {
		t.Fatalf("a body that cannot be read: %v", err)
	}
}

type errBody struct{}

func (errBody) Read([]byte) (int, error) { return 0, errors.New("the connection dropped") }
func (errBody) Close() error             { return nil }

// limit is DEFAULT_PAGE_SIZE when absent and MAX_PAGE_SIZE at most, a
// larger one invalid_field; a cursor another list issued, or none issued,
// is invalid_cursor.
func TestPageOf(t *testing.T) {
	get := func(q string) (page, error) {
		return pageOf(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/things?"+q, nil), "things")
	}
	if p, err := get(""); err != nil || p.Limit != limits.DefaultPageSize || p.After != "" {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := get("limit=200"); err != nil || p.Limit != limits.MaxPageSize {
		t.Fatalf("%+v %v", p, err)
	}
	for _, q := range []string{"limit=201", "limit=0", "limit=-1", "limit=x"} {
		var e *apierr.Error
		if _, err := get(q); !errors.As(err, &e) || e.Code != apierr.CodeInvalidField || e.Field != "limit" {
			t.Errorf("%s: %v", q, err)
		}
	}
	if p, err := get("cursor=" + cursorFor("things", "t9")); err != nil || p.After != "t9" {
		t.Fatalf("a cursor of the list: %+v %v", p, err)
	}
	for _, c := range []string{
		cursorFor("others", "t9"), cursorFor("things", ""), "not-base64!",
		base64.RawURLEncoding.EncodeToString([]byte("no colon")),
	} {
		var e *apierr.Error
		if _, err := get("cursor=" + c); !errors.As(err, &e) || e.Code != apierr.CodeInvalidCursor || e.Field != "cursor" {
			t.Errorf("%q: %v", c, err)
		}
	}
}

// A list answers its items, never null, and the cursor of the next page,
// absent on the last; walking the cursors visits every item once.
func TestPaging(t *testing.T) {
	h := newHarness(t, &fakeStore{}, []route{listing(5)}, nil)
	var seen []string
	next := ""
	for range 10 {
		target := "/v1/things?limit=2"
		if next != "" {
			target += "&cursor=" + next
		}
		a := h.do(http.MethodGet, target, "t-alice", nil)
		var b struct {
			Items      []thing `json:"items"`
			NextCursor *string `json:"next_cursor"`
		}
		if err := json.Unmarshal(a.body, &b); err != nil || a.status != http.StatusOK {
			t.Fatalf("%d %s", a.status, a.body)
		}
		for _, it := range b.Items {
			seen = append(seen, it.ID)
		}
		if b.NextCursor == nil {
			break
		}
		next = *b.NextCursor
	}
	if strings.Join(seen, ",") != "ta,tb,tc,td,te" {
		t.Fatalf("the walk visited %v", seen)
	}
	empty := newHarness(t, &fakeStore{}, []route{listing(0)}, nil)
	if a := empty.do(http.MethodGet, "/v1/things", "t-alice", nil); a.status != http.StatusOK || strings.TrimSpace(string(a.body)) != `{"items":[]}` {
		t.Fatalf("an empty list: %d %s", a.status, a.body)
	}
	if a := empty.do(http.MethodGet, "/v1/things?limit=500", "t-alice", nil); a.status != http.StatusBadRequest {
		t.Fatalf("a limit past the largest: %d", a.status)
	}
}
