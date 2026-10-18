// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"errors"
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

// kindRoute is a versioned kind as its routes name it: the plural of the
// path and its actions.
type kindRoute struct {
	kind, plural      string
	apply, read, list string
}

// kinds are the five versioned kinds, in the contract's order.
var kinds = []kindRoute{
	{kind: objects.KindMetric, plural: "metrics", apply: authorizer.ActionMetricApply, read: authorizer.ActionMetricRead, list: authorizer.ActionMetricList},
	{kind: objects.KindGrader, plural: "graders", apply: authorizer.ActionGraderApply, read: authorizer.ActionGraderRead, list: authorizer.ActionGraderList},
	{kind: objects.KindTask, plural: "tasks", apply: authorizer.ActionTaskApply, read: authorizer.ActionTaskRead, list: authorizer.ActionTaskList},
	{kind: objects.KindSuite, plural: "suites", apply: authorizer.ActionSuiteApply, read: authorizer.ActionSuiteRead, list: authorizer.ActionSuiteList},
	{kind: objects.KindCandidate, plural: "candidates", apply: authorizer.ActionCandidateApply, read: authorizer.ActionCandidateRead, list: authorizer.ActionCandidateList},
}

// versionRoutes are the four routes of each versioned kind.
func versionRoutes() []route {
	var out []route
	for _, k := range kinds {
		out = append(out,
			route{method: http.MethodPut, path: "/" + k.plural + "/{name}", handle: applyHandler(k)},
			route{method: http.MethodGet, path: "/" + k.plural + "/{ref}", handle: readHandler(k)},
			route{method: http.MethodGet, path: "/" + k.plural + "/{name}/versions", handle: historyHandler(k)},
			route{method: http.MethodGet, path: "/" + k.plural, handle: listHandler(k)},
		)
	}
	return out
}

// applied is a decoded and checked body of a kind: its value, rendered
// for the digest, and the fields its apply question sends.
type applied struct {
	value   any
	members map[string]any
}

// decodeKind reads an apply's body into its kind's value, with the path's
// name, normalized and held to the kind's rules.
func (s *Server) decodeKind(r *http.Request, kind, name string) (applied, error) {
	body, err := readBody(r)
	if err != nil {
		return applied{}, err
	}
	// name may be left out; when present it equals the path's.
	var head struct {
		Name *string `json:"name"`
	}
	if err := json.Unmarshal(body, &head); err != nil {
		return applied{}, decodeError(err)
	}
	if head.Name != nil && *head.Name != name {
		return applied{}, apierr.OnField(apierr.CodeInvalidField, "name", "the body's name differs from the path's")
	}
	if !objects.ValidName(kind, name) {
		return applied{}, apierr.OnField(apierr.CodeInvalidField, "name", "the name breaks its kind's rule, or has the form of an identifier")
	}
	members := map[string]any{authorizer.FieldName: name}
	switch kind {
	case objects.KindMetric:
		var m objects.Metric
		if err := decodeBytes(body, &m); err != nil {
			return applied{}, err
		}
		m.Name = name
		if err := m.Normalize(); err != nil {
			return applied{}, err
		}
		members[authorizer.FieldMetricType] = m.Type
		return applied{&m, members}, m.Validate()
	case objects.KindGrader:
		var g objects.Grader
		if err := decodeBytes(body, &g); err != nil {
			return applied{}, err
		}
		g.Name = name
		if err := g.Normalize(); err != nil {
			return applied{}, err
		}
		members[authorizer.FieldGraderKind] = g.Kind
		if g.ModelJudge != nil {
			members[authorizer.FieldModel] = g.ModelJudge.Model
		}
		if g.AgentJudge != nil {
			members[authorizer.FieldAgent] = g.AgentJudge.Agent
		}
		return applied{&g, members}, g.Validate()
	case objects.KindTask:
		var t objects.Task
		if err := decodeBytes(body, &t); err != nil {
			return applied{}, err
		}
		t.Name = name
		if err := t.Normalize(); err != nil {
			return applied{}, err
		}
		if t.Workspace != nil {
			members[authorizer.FieldWorkspaceKind] = t.Workspace.Kind
			members[authorizer.FieldRepository], members[authorizer.FieldCommit] = t.Workspace.Repository, t.Workspace.Commit
			members[authorizer.FieldPath] = t.Workspace.Path
		}
		if t.Environment != nil {
			members[authorizer.FieldImage] = t.Environment.Image
		}
		return applied{&t, members}, t.Validate()
	case objects.KindSuite:
		var su objects.Suite
		if err := decodeBytes(body, &su); err != nil {
			return applied{}, err
		}
		su.Name = name
		if err := su.Normalize(); err != nil {
			return applied{}, err
		}
		members[authorizer.FieldTaskCount], members[authorizer.FieldMetricCount] = len(su.Tasks), len(su.Metrics)
		return applied{&su, members}, su.Validate()
	}
	var c objects.Candidate
	if err := decodeBytes(body, &c); err != nil {
		return applied{}, err
	}
	c.Name = name
	members[authorizer.FieldCandidateKind] = c.Kind
	members[authorizer.FieldModel], members[authorizer.FieldReasoning] = c.Model(), c.Reasoning()
	members[authorizer.FieldCredentialAudiences] = strs(c.Audiences())
	switch {
	case c.Topos != nil:
		members[authorizer.FieldAgent] = c.Topos.Agent
	case c.Program != nil:
		members[authorizer.FieldImage] = c.Program.Image
	case c.External != nil:
		members[authorizer.FieldHarness] = c.External.Harness
	}
	return applied{&c, members}, c.Validate(s.Reserved)
}

func applyHandler(k kindRoute) func(*Server, http.ResponseWriter, *http.Request, access.Caller) error {
	return func(s *Server, w http.ResponseWriter, r *http.Request, c access.Caller) error {
		name := r.PathValue("name")
		a, err := s.decodeKind(r, k.kind, name)
		if err != nil {
			return err
		}
		pre, err := ifMatch(r)
		if err != nil {
			return err
		}
		body, err := json.Marshal(a.value)
		if err != nil {
			return apierr.Internal(err)
		}
		digest, err := id.DigestJSON(body)
		if err != nil {
			return apierr.Internal(err)
		}
		a.members[authorizer.FieldOwner] = r.URL.Query().Get("owner")
		a.members[authorizer.FieldDigest] = digest
		d, err := s.permitted(r, c, access.Ask(r, k.apply, access.Resource(k.apply, "", a.members)))
		if err != nil {
			return err
		}
		owner := createOwner(r, c, d)
		if err := s.checkPins(r, owner, a.value); err != nil {
			return err
		}
		canonical, err := id.Canonical(body)
		if err != nil {
			return apierr.Internal(err)
		}
		v, made, err := s.Store.Versions().Apply(r.Context(), objects.Version{
			ID: id.New(objects.Prefix(k.kind)), Kind: k.kind, Owner: owner, Name: name, Digest: digest, Body: canonical,
		}, pre)
		if err != nil {
			return precondition(err)
		}
		return replyVersion(w, s, k, v, made)
	}
}

// replyVersion answers a version with its tag and address.
func replyVersion(w http.ResponseWriter, s *Server, k kindRoute, v objects.Version, made bool) error {
	out, err := v.Answer()
	if err != nil {
		return apierr.Internal(err)
	}
	w.Header().Set("ETag", `"`+v.Digest+`"`)
	w.Header().Set("Location", s.location("/"+k.plural+"/"+v.Name+"@"+v.Digest))
	status := http.StatusOK
	if made {
		status = http.StatusCreated
	}
	reply(w, status, out)
	return nil
}

// checkPins holds what an apply references among Examen's own objects to
// the versions the owner has: a grader's metrics, a task's checks, a
// cell-level metric's of, and every version a suite pins, with the rules
// that read them (specs/002-objects.md). A pin that names nothing is
// reference_not_found on its member.
func (s *Server) checkPins(r *http.Request, owner string, v any) error {
	ctx := r.Context()
	vs := s.Store.Versions()
	exists := func(kind, field, digest string) (objects.Version, error) {
		ver, err := vs.ByDigest(ctx, kind, owner, digest)
		if errors.Is(err, store.ErrNotFound) {
			return ver, apierr.OnField(apierr.CodeReferenceNotFound, field, "no "+kind+" of this owner has the digest "+digest)
		}
		if err != nil {
			return ver, apierr.Internal(err)
		}
		return ver, nil
	}
	switch x := v.(type) {
	case *objects.Metric:
		if x.Of != "" && !objects.IsBuiltinMetric(x.Of) {
			_, err := exists(objects.KindMetric, "of", x.Of)
			return err
		}
	case *objects.Grader:
		for i, p := range x.Produces {
			if objects.IsBuiltinMetric(p) {
				continue
			}
			if _, err := exists(objects.KindMetric, "produces["+itoa(i)+"]", p); err != nil {
				return err
			}
		}
	case *objects.Task:
		for i, ch := range x.Checks {
			ver, err := exists(objects.KindGrader, "checks["+itoa(i)+"]", ch)
			if err != nil {
				return err
			}
			var g objects.Grader
			if err := json.Unmarshal(ver.Body, &g); err != nil {
				return apierr.Internal(err)
			}
			if g.Kind != objects.GraderCheck {
				return apierr.OnField(apierr.CodeInvalidField, "checks["+itoa(i)+"]", "a task's check is a grader of kind check, not "+g.Kind)
			}
		}
	case *objects.Suite:
		_, err := s.resolveSuite(r, owner, *x, "")
		return err
	}
	return nil
}

// resolveSuite reads every version a suite version pins in the owner's
// context and holds the suite to the rules that read them. field prefixes
// the member a refusal names, for a suite named inside another body.
func (s *Server) resolveSuite(r *http.Request, owner string, su objects.Suite, field string) (objects.Resolved, error) {
	ctx := r.Context()
	vs := s.Store.Versions()
	res := objects.Resolved{Suite: su, Tasks: map[string]objects.Task{}, Metrics: map[string]objects.Metric{}, Graders: map[string]objects.Grader{}, TaskIDs: map[string]string{}}
	read := func(kind, member, digest string, into any) error {
		ver, err := vs.ByDigest(ctx, kind, owner, digest)
		if errors.Is(err, store.ErrNotFound) {
			return apierr.OnField(apierr.CodeReferenceNotFound, field+member, "no "+kind+" of this owner has the digest "+digest)
		}
		if err != nil {
			return apierr.Internal(err)
		}
		if err := json.Unmarshal(ver.Body, into); err != nil {
			return apierr.Internal(err)
		}
		if kind == objects.KindTask {
			res.TaskIDs[digest] = ver.ID
		}
		return nil
	}
	grader := func(member, digest string) error {
		if _, done := res.Graders[digest]; done {
			return nil
		}
		var g objects.Grader
		if err := read(objects.KindGrader, member, digest, &g); err != nil {
			return err
		}
		res.Graders[digest] = g
		return nil
	}
	for i, d := range su.Tasks {
		var t objects.Task
		if err := read(objects.KindTask, "tasks["+itoa(i)+"]", d, &t); err != nil {
			return res, err
		}
		res.Tasks[d] = t
		for j, ch := range t.Checks {
			if err := grader("tasks["+itoa(i)+"].checks["+itoa(j)+"]", ch); err != nil {
				return res, err
			}
		}
	}
	for i, d := range su.Metrics {
		if b, ok := objects.Builtin(d); ok {
			res.Metrics[d] = b
			continue
		}
		if strings.HasPrefix(d, objects.BuiltinPrefix) {
			return res, apierr.OnField(apierr.CodeReferenceNotFound, field+"metrics["+itoa(i)+"]", "no built-in metric is named "+d)
		}
		var m objects.Metric
		if err := read(objects.KindMetric, "metrics["+itoa(i)+"]", d, &m); err != nil {
			return res, err
		}
		res.Metrics[d] = m
	}
	for i, m := range res.Metrics {
		if m.Of != "" {
			if _, listed := res.Metrics[m.Of]; !listed {
				return res, apierr.OnField(apierr.CodeInvalidField, field+"metrics", "the cell-level metric "+i+" summarizes a metric the suite does not list")
			}
		}
	}
	for i, d := range su.Graders {
		if err := grader("graders["+itoa(i)+"]", d); err != nil {
			return res, err
		}
	}
	for i, d := range su.Canonicalizers {
		if err := grader("canonicalizers["+itoa(i)+"]", d); err != nil {
			return res, err
		}
	}
	if su.DeclineJudge != "" {
		if err := grader("decline_judge", su.DeclineJudge); err != nil {
			return res, err
		}
	}
	if err := res.Check(); err != nil {
		var e *apierr.Error
		if errors.As(err, &e) && field != "" {
			e.Field = field + e.Field
		}
		return res, err
	}
	return res, nil
}

// resolveRef reads a version by a path's reference: an id, whoever owns
// it; name@digest, a digest, or a name in the owner's context.
func (s *Server) resolveRef(r *http.Request, c access.Caller, k kindRoute, ref string) (objects.Version, error) {
	ctx := r.Context()
	vs := s.Store.Versions()
	code := notFound[k.kind]
	if isID(objects.Prefix(k.kind), ref) {
		v, err := vs.ByID(ctx, k.kind, ref)
		return v, storeError(err, code, "no "+k.kind+" has the id "+ref)
	}
	owner, err := s.contextOwner(r, c, k.list)
	if err != nil {
		return objects.Version{}, err
	}
	name, digest, perr := objects.ParseRef("ref", ref)
	var v objects.Version
	switch {
	case perr != nil:
		v, err = vs.Current(ctx, k.kind, owner, ref)
	default:
		v, err = vs.ByDigest(ctx, k.kind, owner, digest)
		if err == nil && name != "" && v.Name != name {
			err = store.ErrNotFound
		}
	}
	return v, storeError(err, code, "no "+k.kind+" of this owner answers to "+ref)
}

// readMembers are the fields a read of a version sends.
func readMembers(k kindRoute, v objects.Version, c access.Caller) map[string]any {
	m := othersOnly(map[string]any{authorizer.FieldOwner: v.Owner, authorizer.FieldName: v.Name, authorizer.FieldDigest: v.Digest}, v.Owner, c)
	var kind struct {
		Kind string `json:"kind"`
	}
	if (k.kind == objects.KindGrader || k.kind == objects.KindCandidate) && json.Unmarshal(v.Body, &kind) == nil {
		if k.kind == objects.KindGrader {
			m[authorizer.FieldGraderKind] = kind.Kind
		} else {
			m[authorizer.FieldCandidateKind] = kind.Kind
		}
	}
	return m
}

func readHandler(k kindRoute) func(*Server, http.ResponseWriter, *http.Request, access.Caller) error {
	return func(s *Server, w http.ResponseWriter, r *http.Request, c access.Caller) error {
		ref := r.PathValue("ref")
		v, err := s.resolveRef(r, c, k, ref)
		if err != nil {
			return err
		}
		if _, err := s.allowed(r, c, k.read, v.ID, readMembers(k, v, c), notFound[k.kind], "no "+k.kind+" of this owner answers to "+ref); err != nil {
			return err
		}
		out, err := v.Answer()
		if err != nil {
			return apierr.Internal(err)
		}
		w.Header().Set("ETag", `"`+v.Digest+`"`)
		reply(w, http.StatusOK, out)
		return nil
	}
}

func historyHandler(k kindRoute) func(*Server, http.ResponseWriter, *http.Request, access.Caller) error {
	return func(s *Server, w http.ResponseWriter, r *http.Request, c access.Caller) error {
		name := r.PathValue("name")
		p, err := pageOf(r, k.plural+"/"+name+"/versions")
		if err != nil {
			return err
		}
		cur, err := s.resolveRef(r, c, k, name)
		if err != nil {
			return err
		}
		if _, err := s.allowed(r, c, k.read, cur.ID, readMembers(k, cur, c), notFound[k.kind], "no "+k.kind+" of this owner is named "+name); err != nil {
			return err
		}
		list, err := s.Store.Versions().History(r.Context(), k.kind, cur.Owner, cur.Name, store.Page{After: p.After, Limit: p.Limit})
		if err != nil {
			return apierr.Internal(err)
		}
		return replyVersions(w, k.plural+"/"+name+"/versions", p, list)
	}
}

// replyVersions answers a page of versions.
func replyVersions(w http.ResponseWriter, list string, p page, vs []objects.Version) error {
	items := make([]map[string]any, 0, len(vs))
	for _, v := range vs {
		a, err := v.Answer()
		if err != nil {
			return apierr.Internal(err)
		}
		items = append(items, a)
	}
	replyPage(w, list, p, items, func(m map[string]any) string { id, _ := m["id"].(string); return id })
	return nil
}

func listHandler(k kindRoute) func(*Server, http.ResponseWriter, *http.Request, access.Caller) error {
	return func(s *Server, w http.ResponseWriter, r *http.Request, c access.Caller) error {
		p, err := pageOf(r, k.plural)
		if err != nil {
			return err
		}
		q := r.URL.Query()
		requested := q.Get("owner")
		d, err := s.permitted(r, c, access.Ask(r, k.list, access.List(k.list, requested)))
		if err != nil {
			return err
		}
		f := store.VersionFilter{Owners: d.Owners(c, requested), Name: q.Get("name")}
		switch q.Get("versions") {
		case "":
		case "all":
			f.All = true
		default:
			return apierr.OnField(apierr.CodeInvalidField, "versions", "versions is all, or absent for each name's current version")
		}
		switch k.kind {
		case objects.KindMetric:
			f.MetricType = q.Get("type")
		case objects.KindGrader:
			f.GraderKind, f.Produces = q.Get("grader_kind"), q.Get("produces")
		case objects.KindCandidate:
			f.CandidateKind, f.Agent, f.Model = q.Get("candidate_kind"), q.Get("agent"), q.Get("model")
		case objects.KindTask:
			if ref := q.Get("suite"); ref != "" {
				f.Digests = []string{}
				_, digest, err := objects.ParseRef("suite", ref)
				if err != nil {
					return apierr.OnField(apierr.CodeInvalidField, "suite", "suite is a suite digest")
				}
				for _, o := range f.Owners {
					if v, err := s.Store.Versions().ByDigest(r.Context(), objects.KindSuite, o, digest); err == nil {
						var su objects.Suite
						if err := json.Unmarshal(v.Body, &su); err != nil {
							return apierr.Internal(err)
						}
						f.Digests = append(f.Digests, su.Tasks...)
					} else if !errors.Is(err, store.ErrNotFound) {
						return apierr.Internal(err)
					}
				}
			}
		}
		if k.kind == objects.KindTask || k.kind == objects.KindCandidate {
			labels, empty, err := labelFilter(r, d)
			if err != nil {
				return err
			}
			if empty {
				f.Owners = nil
			}
			f.Labels = labels
		}
		if len(f.Owners) == 0 {
			return replyVersions(w, k.plural, p, nil)
		}
		list, err := s.Store.Versions().List(r.Context(), k.kind, f, store.Page{After: p.After, Limit: p.Limit})
		if err != nil {
			return apierr.Internal(err)
		}
		return replyVersions(w, k.plural, p, list)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
