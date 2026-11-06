// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"latere.ai/x/pkg/httpjson"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

// readBody reads a request body whole, within the bound the server holds
// every body to.
func readBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, apierr.Newf(apierr.CodeRequestTooLarge, "the body is longer than MAX_REQUEST_BYTES (%d)", limits.MaxRequestBytes)
	case err != nil:
		return nil, apierr.New(apierr.CodeInvalidRequest, "the body could not be read: "+err.Error())
	}
	return body, nil
}

// decode reads a JSON body into v: one object, no member the contract does
// not describe, nothing after it. The errors are the contract's.
func decode(r *http.Request, v any) error {
	body, err := readBody(r)
	if err != nil {
		return err
	}
	return decodeBytes(body, v)
}

// decodeBytes reads a JSON body already read whole: one object, no member
// the contract does not describe, nothing after it.
func decodeBytes(body []byte, v any) error {
	dec := jsontext.NewDecoder(bytes.NewReader(body))
	if err := json.UnmarshalDecode(dec, v, json.RejectUnknownMembers(true)); err != nil {
		return decodeError(err)
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return apierr.New(apierr.CodeInvalidRequest, "the body holds more than one JSON value")
	}
	return nil
}

// decodeError names the member a body's error is about.
func decodeError(err error) error {
	var sem *json.SemanticError
	if !errors.As(err, &sem) {
		return apierr.New(apierr.CodeInvalidRequest, "the body is not JSON: "+err.Error())
	}
	if sem.Err != nil && strings.HasPrefix(sem.Err.Error(), "unknown object member name") {
		var name string
		for tok := range sem.JSONPointer.Tokens() {
			name = tok
		}
		return apierr.OnField(apierr.CodeUnknownField, name, "the contract describes no member "+name)
	}
	var path []string
	for tok := range sem.JSONPointer.Tokens() {
		path = append(path, tok)
	}
	want := "a different type"
	if sem.GoType != nil {
		want = sem.GoType.String()
	}
	return apierr.OnField(apierr.CodeInvalidRequest, strings.Join(path, "."), "the member has the JSON type "+sem.JSONKind.String()+", not "+want)
}

// reply writes a value as the JSON answer with its status.
func reply[T any](w http.ResponseWriter, status int, v T) { httpjson.Write(w, status, v) }

// page is where a list resumes and how much of it to answer.
type page struct {
	// Limit is the items in the page.
	Limit int
	// After is the id of the last item of the page before, and empty on
	// the first page.
	After string
}

// pageOf reads limit and cursor for a list: DEFAULT_PAGE_SIZE items when
// limit is absent and MAX_PAGE_SIZE at most, a larger or a non-positive one
// refused; a cursor another list issued, or none issued, is invalid_cursor.
func pageOf(r *http.Request, list string) (page, error) {
	p := page{Limit: limits.DefaultPageSize}
	q := r.URL.Query()
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > limits.MaxPageSize {
			return p, apierr.OnField(apierr.CodeInvalidField, "limit", "limit is a whole number from 1 to MAX_PAGE_SIZE ("+strconv.Itoa(limits.MaxPageSize)+")")
		}
		p.Limit = n
	}
	if raw := q.Get("cursor"); raw != "" {
		after, ok := readCursor(raw, list)
		if !ok {
			return p, apierr.OnField(apierr.CodeInvalidCursor, "cursor", "the cursor was not issued by the list of "+list)
		}
		p.After = after
	}
	return p, nil
}

// A cursor is the list that issued it and the id it resumes after,
// joined by a colon and encoded, so a client treats it as opaque and a
// cursor of one list is refused by another.

// cursorFor is the cursor that resumes a list after an id.
func cursorFor(list, after string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(list + ":" + after))
}

// readCursor reads a cursor of a list, and false for one it did not issue.
func readCursor(raw, list string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return "", false
	}
	issuer, after, ok := strings.Cut(string(b), ":")
	if !ok || issuer != list || after == "" {
		return "", false
	}
	return after, true
}

// listBody is the envelope of a page: items, never null, and the cursor of
// the next page, absent on the last.
type listBody[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// replyPage answers a page of a list read one item past its limit: the
// items within the limit, and the cursor of the next page when there is
// one. idOf names an item's id.
func replyPage[T any](w http.ResponseWriter, list string, p page, items []T, idOf func(T) string) {
	body := listBody[T]{Items: items}
	if len(items) > p.Limit {
		body.Items = items[:p.Limit]
		body.NextCursor = cursorFor(list, idOf(body.Items[p.Limit-1]))
	}
	if body.Items == nil {
		body.Items = []T{}
	}
	reply(w, http.StatusOK, body)
}
