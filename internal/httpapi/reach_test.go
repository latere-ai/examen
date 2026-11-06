// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// probe is one request of another owner's to a route: its query and its
// body.
type probe struct {
	query string
	body  any
}

// reach is what alice holds in the store the probes run against, and how
// each route is sent a request that names it.
type reach struct {
	owner string
	// refs are the values of a path parameter, by the segment before it
	// and the parameter's name.
	refs map[[2]string][]string
	// probes are what a route is sent, by its method and path. A route
	// with a parameter in its path and none listed is sent no body in
	// alice's context; one with none in its path must be listed.
	probes map[string][]probe
}

// stepClock is a clock a minute later at each reading, so the probes never
// reach a subject's request rate.
func stepClock() func() time.Time {
	var n atomic.Int64
	start := time.Now()
	return func() time.Time { return start.Add(time.Duration(n.Add(1)) * time.Minute) }
}

// first is a member of the first item a list answers.
func (x *client) first(path, member string) string {
	x.t.Helper()
	items, _ := x.must(http.StatusOK, http.MethodGet, path, "t-alice", nil)["items"].([]any)
	if len(items) == 0 {
		x.t.Fatalf("%s lists nothing", path)
	}
	v, _ := items[0].(map[string]any)[member].(string)
	return v
}

// reachOf builds alice's objects: a version of every kind, a complete
// imported experiment with its cells, trials, measurements and
// comparisons, a running experiment, a baseline and a taxonomy.
func (x *client) reachOf() reach {
	x.t.Helper()
	f := x.fixture()
	applied := map[string]map[string]any{
		"metrics":    {"type": "binary", "direction": "none", "aggregate": "rate"},
		"graders":    {"kind": "model_judge", "produces": []string{f.quality}, "model_judge": map[string]any{"model": "claude-haiku-5-5", "prompt": "rate"}},
		"tasks":      {"instruction": "Build a calculator."},
		"suites":     {"tasks": []string{f.taskA, f.taskB}, "metrics": []string{f.quality, f.theme, "builtin:completed"}, "primary_metric": f.quality},
		"candidates": {"kind": "external", "external": map[string]any{"harness": "goalless", "version": "2.1.4", "model": "claude-haiku-5-5", "reasoning": "high"}},
	}
	x.apply("graders", "judge", applied["graders"])
	names := map[string]string{"metrics": "quality", "graders": "judge", "tasks": "todo-app", "suites": "goalless", "candidates": "harness-high"}
	eid := x.completeImport(f)
	create := map[string]any{"suite": f.suite, "candidates": []string{f.high, f.low}, "trials": 2, "budget": map[string]any{"name": "study"}}
	xid := x.must(http.StatusCreated, http.MethodPost, "/v1/experiments", "t-alice", create)["id"].(string)
	pin := map[string]any{"experiment": eid, "candidate": f.high}
	bid := x.must(http.StatusCreated, http.MethodPut, "/v1/suites/goalless/baselines/main", "t-alice", pin)["id"].(string)
	taxonomy := map[string]any{"canonical": []string{"repair"}, "map": map[string]string{"Fixes Bugs": "repair"}}
	tid := x.must(http.StatusCreated, http.MethodPut, "/v1/suites/goalless/taxonomies/theme", "t-alice", taxonomy)["id"].(string)
	comparison := map[string]any{"experiment": eid, "candidate": f.high, "reference_candidate": f.low}
	cid := x.must(http.StatusCreated, http.MethodPost, "/v1/comparisons", "t-alice", comparison)["id"].(string)

	owner := "owner=" + url.QueryEscape(alice)
	w := reach{owner: owner, refs: map[[2]string][]string{
		{"experiments", "id"}:    {eid, xid},
		{"cells", "id"}:          {x.first("/v1/experiments/"+eid+"/cells", "id"), x.first("/v1/experiments/"+xid+"/cells", "id")},
		{"trials", "id"}:         {x.first("/v1/experiments/"+eid+"/trials", "id"), x.first("/v1/experiments/"+xid+"/trials", "id")},
		{"measurements", "id"}:   {x.first("/v1/experiments/"+eid+"/measurements", "id")},
		{"comparisons", "id"}:    {cid},
		{"baselines", "id"}:      {bid},
		{"taxonomies", "id"}:     {tid},
		{"suites", "suite"}:      {"goalless"},
		{"baselines", "name"}:    {"main"},
		{"taxonomies", "metric"}: {"theme", f.theme},
	}, probes: map[string][]probe{
		"PUT /suites/{suite}/baselines/{name}":    {{owner, pin}, {"", pin}},
		"PUT /suites/{suite}/taxonomies/{metric}": {{owner, taxonomy}, {"", taxonomy}},
		"POST /experiments/{id}/imports":          {{owner, pastRun(f.taskA, f.high, "probe", nil)}, {owner, map[string]any{}}},
		"POST /experiments/{id}/work/lease":       {{owner, map[string]any{"candidate": f.high}}, {owner, map[string]any{"candidate": "not-a-reference"}}, {owner, map[string]any{}}},
		"POST /trials/{id}/submit":                {{owner, map[string]any{"lease": "lea_probe", "outcome": "completed"}}, {owner, map[string]any{"outcome": 1}}},
		"POST /experiments":                       {{owner, create}, {"", create}},
		"GET /experiments":                        {{owner, nil}, {"suite=" + f.suite + "&candidate=" + f.high, nil}, {"baseline=" + bid, nil}, {"suite=goalless", nil}},
		"POST /comparisons":                       {{"", comparison}},
		"GET /comparisons":                        {{owner, nil}, {"experiment=" + eid, nil}, {"suite=" + f.suite, nil}},
	}}
	for _, k := range kinds {
		name := names[k.plural]
		v := x.must(http.StatusOK, http.MethodGet, "/v1/"+k.plural+"/"+name, "t-alice", nil)
		vid, digest := v["id"].(string), v["digest"].(string)
		w.refs[[2]string{k.plural, "ref"}] = []string{vid, name, digest, name + "@" + digest}
		w.refs[[2]string{k.plural, "name"}] = []string{name, vid}
		w.probes["PUT /"+k.plural+"/{name}"] = []probe{{owner, applied[k.plural]}}
		list := []probe{{owner, nil}, {"name=" + name, nil}}
		if k.plural == "tasks" {
			list = append(list, probe{"suite=" + f.suite, nil})
		}
		w.probes["GET /"+k.plural] = list
	}
	return w
}

// paths fills a route's path with every combination of alice's values for
// its parameters, and reports whether it has one.
func (w reach) paths(t *testing.T, rt route) ([]string, bool) {
	t.Helper()
	out := []string{""}
	named := false
	segs := strings.Split(strings.TrimPrefix(rt.path, "/"), "/")
	for i, seg := range segs {
		param, ok := strings.CutPrefix(seg, "{")
		if !ok {
			for j := range out {
				out[j] += "/" + seg
			}
			continue
		}
		named = true
		param = strings.TrimSuffix(param, "}")
		before := ""
		if i > 0 {
			before = segs[i-1]
		}
		values, known := w.refs[[2]string{before, param}]
		if !known {
			t.Fatalf("%s %s: alice holds nothing for {%s} after %q; add the object it names to reachOf", rt.method, rt.path, param, before)
		}
		var next []string
		for _, p := range out {
			for _, v := range values {
				next = append(next, p+"/"+url.PathEscape(v))
			}
		}
		out = next
	}
	return out, named
}

// variants are a probe and the requests beside it that a route must refuse
// alike whatever exists: a cursor no list issued and a limit out of range
// on a read, and a body that does not parse on a write.
func variants(method string, p probe) []probe {
	with := func(q string) string {
		if p.query == "" {
			return q
		}
		return p.query + "&" + q
	}
	if method == http.MethodGet {
		return []probe{p, {with("cursor=x"), nil}, {with("limit=0"), nil}}
	}
	return []probe{p, {p.query, "{"}}
}

// sent is one request of the test: the route it goes to, its path below
// the base path, and the probe.
type sent struct {
	rt   route
	path string
	p    probe
}

func (s sent) String() string {
	body := ""
	switch b := s.p.body.(type) {
	case nil:
	case string:
		body = " " + b
	default:
		body = fmt.Sprintf(" %v", b)
	}
	return s.rt.method + " " + s.target() + body
}

func (s sent) target() string {
	if s.p.query == "" {
		return "/v1" + s.path
	}
	return "/v1" + s.path + "?" + s.p.query
}

// send sends a probe as a bearer, under a fixed request id so two stores'
// answers compare header for header. A stream is read for a moment and
// cut.
func (x *client) send(s sent, bearer string) answer {
	x.t.Helper()
	ctx := x.t.Context()
	if strings.HasSuffix(s.rt.path, "/stream") {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
	}
	return x.doCtx(ctx, s.rt.method, s.target(), bearer, s.p.body, "X-Request-Id", "req_probe")
}

func same(a, b answer) bool {
	return a.status == b.status && bytes.Equal(a.body, b.body) && maps.EqualFunc(a.header, b.header, slices.Equal)
}

// sameRead reports whether two reads of one object answer the same: an
// object's members may come in any order, since a response built from a map
// is not sorted.
func sameRead(a, b answer) bool {
	return a.status == b.status && maps.EqualFunc(a.header, b.header, slices.Equal) && sameJSON(a.body, b.body)
}

// sameJSON compares two bodies as JSON in canonical form, or byte for byte
// when either is not JSON.
func sameJSON(a, b []byte) bool {
	ca, cb := jsontext.Value(bytes.Clone(a)), jsontext.Value(bytes.Clone(b))
	if ca.Canonicalize() != nil || cb.Canonicalize() != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(ca, cb)
}

// Another owner's token reaches nothing, and cannot tell what exists: on
// every route served, each request bob sends that names alice's objects
// by id, name, digest or reference, in the path, the query or the body, is
// answered byte for byte as the same request against a store that holds
// nothing, status, headers and body; nothing of alice's changes; and on
// each route alice, sending one of the same requests, is answered
// otherwise, so the probes name something that exists. A route added
// later is probed by its path's parameters, or fails here until its
// probes are listed.
func TestAnotherOwnersTokenReachesNothing(t *testing.T) {
	tweak := func(s *Server) {
		s.Now = stepClock()
		s.StreamPoll = 10 * time.Millisecond
	}
	full, empty := newAPI(t, tweak), newAPI(t, tweak)
	w := full.reachOf()

	var base, all []sent
	for _, rt := range routes {
		paths, named := w.paths(t, rt)
		probes, listed := w.probes[rt.method+" "+rt.path]
		switch {
		case !listed && !named:
			t.Errorf("%s %s names nothing in its path: list the probes that name alice's objects in its query or body", rt.method, rt.path)
			continue
		case !listed:
			probes = []probe{{query: w.owner}}
		}
		for _, path := range paths {
			for _, p := range probes {
				base = append(base, sent{rt, path, p})
				for _, v := range variants(rt.method, p) {
					all = append(all, sent{rt, path, v})
				}
			}
		}
	}

	reads := func() map[string]answer {
		out := map[string]answer{}
		for _, s := range base {
			if s.rt.method == http.MethodGet && !strings.HasSuffix(s.rt.path, "/stream") {
				out[s.String()] = full.send(s, "t-alice")
			}
		}
		return out
	}
	before := reads()

	refused := map[string]answer{}
	for _, s := range all {
		got, want := full.send(s, "t-bob"), empty.send(s, "t-bob")
		refused[s.String()] = got
		if !same(got, want) {
			t.Errorf("%s: alice's object and nothing are answered differently\n  exists:  %d %v %s\n  missing: %d %v %s",
				s, got.status, got.header, got.body, want.status, want.header, want.body)
		}
	}

	for k, a := range reads() {
		if b := before[k]; !sameRead(a, b) {
			t.Errorf("%s: bob's requests changed what alice reads\n  before: %d %s\n  after:  %d %s", k, b.status, b.body, a.status, a.body)
		}
	}

	// Last, since alice's writes change her objects: each route has a
	// probe that names something of hers. A probe of a request's form
	// alone, such as a malformed reference, answers her as it does bob.
	reached := map[string]bool{}
	for _, s := range base {
		key := s.rt.method + " " + s.rt.path
		if !reached[key] && !same(full.send(s, "t-alice"), refused[s.String()]) {
			reached[key] = true
		}
	}
	for _, rt := range routes {
		if key := rt.method + " " + rt.path; !reached[key] {
			t.Errorf("%s: alice is answered as bob is to every probe, so none names anything of hers", key)
		}
	}
}
