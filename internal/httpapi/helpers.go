// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"strconv"
	"strings"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// notFound is the 404 of each kind of object.
var notFound = map[string]string{
	objects.KindMetric: apierr.CodeMetricNotFound, objects.KindGrader: apierr.CodeGraderNotFound,
	objects.KindTask: apierr.CodeTaskNotFound, objects.KindSuite: apierr.CodeSuiteNotFound,
	objects.KindCandidate: apierr.CodeCandidateNotFound,
}

// missing is the 404 a route answers when the object a request names is
// not there for the caller: absent from the store, or present and denied
// by the authorizer (specs/004-identity-and-authorization.md). It holds
// the kind's code and the identifier as the request named it, and nothing
// about which of the two happened, so every branch of a route that builds
// it from the request answers the same bytes.
type missing struct{ code, ref string }

// err is the 404 itself. Its detail is made from the code and the
// identifier alone.
func (m missing) err() *apierr.Error {
	return apierr.New(m.code, "no "+strings.TrimSuffix(m.code, "_not_found")+" the caller may see answers to "+m.ref)
}

// onSuite is the 404 of a record of a suite's lineage, a baseline or a
// taxonomy, named by the suite and the path member given.
func onSuite(r *http.Request, code, member string) missing {
	return missing{code, r.PathValue(member) + " of the suite " + r.PathValue("suite")}
}

// storeError turns a store error into a route's: a missing row is the
// route's 404, and anything else is unclassified unless it already is a
// route error.
func storeError(err error, m missing) error {
	var e *apierr.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &e):
		return e
	case errors.Is(err, store.ErrNotFound):
		return m.err()
	}
	return apierr.Internal(err)
}

// allowed asks a question about a stored object and answers a deny with
// the route's 404, byte for byte the answer for an object that does not
// exist.
func (s *Server) allowed(r *http.Request, c access.Caller, action, objectID string, members map[string]any, m missing) (access.Decision, error) {
	d, err := s.ask(r, c, access.Ask(r, action, access.Resource(action, objectID, members)))
	if err != nil {
		return d, err
	}
	if !d.Allow {
		return d, m.err()
	}
	return d, nil
}

// permitted asks a question that names no stored object and answers the
// refusal of a deny: 403, with the authorizer's reason.
func (s *Server) permitted(r *http.Request, c access.Caller, q access.Question) (access.Decision, error) {
	d, err := s.ask(r, c, q)
	if err != nil {
		return d, err
	}
	if !d.Allow {
		return d, apierr.Denied(q.Action, d.Reason)
	}
	return d, nil
}

// contextOwner is the owner a request acts in (specs/003-api.md): the
// owner query parameter, else the owner the allow of the kind's list
// question names, else the caller. The list question is asked before a
// name sent without an owner is resolved, so a member reads by name what
// it applied in an organization's context.
func (s *Server) contextOwner(r *http.Request, c access.Caller, listAction string) (string, error) {
	if o := r.URL.Query().Get("owner"); o != "" {
		return o, nil
	}
	d, err := s.ask(r, c, access.Ask(r, listAction, access.List(listAction, "")))
	if err != nil {
		return "", err
	}
	if d.Allow && d.Limits.Owner != "" {
		return d.Limits.Owner, nil
	}
	return c.Subject, nil
}

// createOwner is whose a created object is: the allow's owner, else the
// owner the request named, else the caller.
func createOwner(r *http.Request, c access.Caller, d access.Decision) string {
	switch {
	case d.Limits.Owner != "":
		return d.Limits.Owner
	case r.URL.Query().Get("owner") != "":
		return r.URL.Query().Get("owner")
	}
	return c.Subject
}

// othersOnly sends shared and pinned on a read of another owner's object:
// both false, since this release keeps no share and an experiment pins
// only versions of its own owner.
func othersOnly(members map[string]any, owner string, c access.Caller) map[string]any {
	if owner != c.Subject {
		members[authorizer.FieldShared] = false
		members[authorizer.FieldPinned] = false
	}
	return members
}

// boolParam reads a query parameter of true or false; absent is nil.
func boolParam(r *http.Request, name string) (*bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return nil, nil
	}
	b, err := strconv.ParseBool(raw)
	if err != nil || (raw != "true" && raw != "false") {
		return nil, apierr.OnField(apierr.CodeInvalidField, name, name+" is true or false")
	}
	return &b, nil
}

// labelFilter is the one metadata.<key>=<value> a list takes, merged with
// the labels the authorizer's filter names; two of the request's are
// refused. empty says the request asks for a label the filter holds to
// another value, which nothing can match.
func labelFilter(r *http.Request, d access.Decision) (labels map[string]string, empty bool, err error) {
	out := map[string]string{}
	if d.Filter != nil {
		maps.Copy(out, d.Filter.Labels)
	}
	n := 0
	for k, vs := range r.URL.Query() {
		key, ok := strings.CutPrefix(k, "metadata.")
		if !ok {
			continue
		}
		n++
		if n > 1 || key == "" || len(vs) != 1 {
			return nil, false, apierr.OnField(apierr.CodeInvalidField, k, "a list takes one metadata.<key>=<value>")
		}
		if prev, set := out[key]; set && prev != vs[0] {
			// A label the authorizer holds the list to cannot be widened.
			return nil, true, nil
		}
		out[key] = vs[0]
	}
	return out, false, nil
}

// decodeBytes reads a JSON body already read whole: one object, no member
// the contract does not describe, nothing after it.
func decodeBytes(body []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return decodeError(err)
	}
	if dec.More() {
		return apierr.New(apierr.CodeInvalidRequest, "the body holds more than one JSON value")
	}
	return nil
}

// ifMatch reads If-Match and If-None-Match: a quoted tag that must be
// current, or * for none yet.
func ifMatch(r *http.Request) (store.Precondition, error) {
	var p store.Precondition
	if v := r.Header.Get("If-None-Match"); v != "" {
		if v != "*" {
			return p, apierr.OnField(apierr.CodeInvalidField, "If-None-Match", "If-None-Match takes * alone")
		}
		p.IfNoneMatch = true
	}
	if v := r.Header.Get("If-Match"); v != "" {
		tag := strings.Trim(v, `"`)
		if tag == "" || len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
			return p, apierr.OnField(apierr.CodeInvalidField, "If-Match", "If-Match takes one quoted tag")
		}
		p.IfMatch = tag
	}
	return p, nil
}

// precondition is the error of a precondition that does not hold.
func precondition(err error) error {
	if errors.Is(err, store.ErrPrecondition) {
		return apierr.New(apierr.CodePreconditionFailed, "If-Match or If-None-Match does not hold for what is current")
	}
	return err
}

// location is the absolute path of an object below the base path.
func (s *Server) location(path string) string { return s.BasePath + path }

// isID reports whether a path segment is an identifier of a prefix.
func isID(prefix, s string) bool { return id.Valid(prefix, s) }

// strs is a list member for an authorizer resource.
func strs(v []string) []string {
	if len(v) == 0 {
		return nil
	}
	return v
}

// ptr is a pointer to a copy of v.
func ptr[T any](v T) *T { return &v }
