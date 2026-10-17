// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package access

import (
	"latere.ai/x/pkg/authz"

	"latere.ai/x/examen/authorizer"
)

// Resource is the resource of an action: its kind, the id for an action
// that sends one, and the members the action sends that are known. A
// member Examen does not know is left out, never sent empty: an empty
// string, a nil and an empty list are each left out, and a false or a zero
// is sent, since either is a known answer.
func Resource(action, id string, members map[string]any) authz.Resource {
	fields := map[string]any{}
	for _, name := range authorizer.Fields(action) {
		v, ok := members[name]
		if !ok || unknown(v) {
			continue
		}
		fields[name] = v
	}
	if !authorizer.SendsID(action) {
		id = ""
	}
	return authz.NewResource(authorizer.Kind(action), id, fields)
}

// unknown reports whether a member's value says nothing: an empty string,
// a nil, or an empty list.
func unknown(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case []string:
		return len(x) == 0
	}
	return false
}

// List is the resource of a list action, naming the owner whose objects it
// lists when the request names one.
func List(action, owner string) authz.Resource {
	return Resource(action, "", map[string]any{authorizer.FieldOwner: owner})
}
