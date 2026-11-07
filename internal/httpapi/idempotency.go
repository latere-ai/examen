// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"io"
	"net/http"

	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/id"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store"
)

// The headers of an idempotent request and its replay.
const (
	idempotencyHeader = "Idempotency-Key"
	replayedHeader    = "Idempotent-Replayed"
)

// idempotent answers a request that carries an Idempotency-Key
// (specs/003-api.md): the same key from the same subject within
// IDEMPOTENCY_WINDOW answers the first response again, its status and body,
// with Idempotent-Replayed, and makes nothing; the same key with a body
// whose canonical JSON differs, or sent to another route, is
// idempotency_key_reused; a repeat while the first is served is
// request_in_progress for IDEMPOTENCY_RESERVATION, which lapses so a repeat
// runs when a replica stopped mid-request. The first request is served for
// at most REQUEST_DEADLINE, shorter than the reservation, so it never ends
// after its reservation lapsed and a repeat never runs it twice. A first
// request answered with a 5xx keeps no answer, so a retry runs again.
func (s *Server) idempotent(w http.ResponseWriter, r *http.Request, c access.Caller, rt route) {
	key := r.Header.Get(idempotencyHeader)
	if !visibleASCII(key, limits.IdempotencyKeyMaxBytes) {
		s.answer(w, r, apierr.OnField(apierr.CodeInvalidField, idempotencyHeader, "an idempotency key is 1 to IDEMPOTENCY_KEY_MAX_BYTES (255) bytes of visible ASCII"))
		return
	}
	body, err := readBody(r)
	if err != nil {
		s.answer(w, r, err)
		return
	}
	k := store.IdempotencyKey{Subject: c.Subject, Key: key, Route: r.Method + " " + r.URL.Path, BodyDigest: bodyDigest(body)}
	// The key outlives a caller that hangs up mid-answer.
	ctx := context.WithoutCancel(r.Context())
	held, fresh, err := s.Store.Idempotency().Begin(ctx, k, limits.IdempotencyReservation)
	switch {
	case err != nil:
		s.log().WarnContext(ctx, "an idempotency key could not be reserved", "route", r.Pattern, "error", apierr.LogText(err))
		s.answer(w, r, apierr.New(apierr.CodeUnavailable, "the idempotency key could not be reserved: the database does not answer"))
		return
	case !fresh && (held.Route != k.Route || held.BodyDigest != k.BodyDigest):
		s.answer(w, r, apierr.OnField(apierr.CodeIdempotencyKeyReused, idempotencyHeader, "the key was first used with another body or on another route"))
		return
	case !fresh && !held.Done:
		s.answer(w, r, apierr.OnField(apierr.CodeRequestInProgress, idempotencyHeader, "the first request with this key is still being answered"))
		return
	case !fresh:
		replay := held.Body
		if rt.secret != "" && held.Status == http.StatusOK {
			// The kept answer left the credential out; the repeat is
			// answered it afresh, and no cache keeps it. The refill reads
			// the request's body again to ask what the first asked.
			r.Body = io.NopCloser(bytes.NewReader(body))
			v, err := rt.refill(s, r, c)
			if err != nil {
				s.answer(w, r, err)
				return
			}
			if replay, err = withMember(held.Body, rt.secret, v); err != nil {
				s.answer(w, r, apierr.Internal(err))
				return
			}
			w.Header().Set("Cache-Control", "no-store")
		}
		if held.ContentType != "" {
			w.Header().Set("Content-Type", held.ContentType)
		}
		w.Header().Set(replayedHeader, "true")
		w.WriteHeader(held.Status)
		if _, err := w.Write(replay); err != nil {
			s.log().WarnContext(ctx, "a replayed answer was not written whole", "route", r.Pattern, "error", apierr.LogText(err))
		}
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	rec := &recorder{ResponseWriter: w, status: http.StatusOK}
	bounded, cancel := context.WithTimeout(r.Context(), limits.RequestDeadline)
	defer cancel()
	if err := rt.handle(s, rec, r.WithContext(bounded), c); err != nil {
		s.answer(rec, r, err)
	}
	if rec.status >= http.StatusInternalServerError {
		if err := s.Store.Idempotency().Abandon(ctx, k.Subject, k.Key); err != nil {
			s.log().ErrorContext(ctx, "an idempotency key was not abandoned; a repeat waits for its reservation to lapse", "route", r.Pattern, "error", apierr.LogText(err))
		}
		return
	}
	k.Status, k.ContentType, k.Body = rec.status, rec.Header().Get("Content-Type"), rec.body.Bytes()
	if rt.secret != "" && k.Status == http.StatusOK {
		// A credential in the answer is never kept with the key.
		kept, err := withMember(k.Body, rt.secret, nil)
		if err != nil {
			s.log().ErrorContext(ctx, "an answer that carries a credential is not kept; a repeat runs the request again", "route", r.Pattern, "error", apierr.LogText(err))
			if err := s.Store.Idempotency().Abandon(ctx, k.Subject, k.Key); err != nil {
				s.log().ErrorContext(ctx, "an idempotency key was not abandoned; a repeat waits for its reservation to lapse", "route", r.Pattern, "error", apierr.LogText(err))
			}
			return
		}
		k.Body = kept
	}
	if err := s.Store.Idempotency().Finish(ctx, k, limits.IdempotencyWindow); err != nil {
		s.log().ErrorContext(ctx, "an idempotent answer was not kept; a repeat runs the request again once its reservation lapses", "route", r.Pattern, "error", apierr.LogText(err))
	}
}

// bodyDigest is the digest of a body's canonical JSON, so a repeat that
// spells the same content another way is the same request; a body that is
// not JSON is digested as its bytes, and its handler refuses it.
func bodyDigest(body []byte) string {
	if d, err := id.DigestJSON(body); err == nil {
		return d
	}
	sum := sha256.Sum256(body)
	return "bytes:" + hex.EncodeToString(sum[:])
}

// recorder keeps a copy of the answer it passes on, for the idempotency
// key.
type recorder struct {
	http.ResponseWriter
	status int
	wrote  bool
	body   bytes.Buffer
}

func (r *recorder) WriteHeader(status int) {
	if !r.wrote {
		r.status, r.wrote = status, true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

// withMember is a JSON object's body with one member set to v, or left out
// when v is nil.
func withMember(body []byte, name string, v any) ([]byte, error) {
	var m map[string]jsontext.Value
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	delete(m, name)
	if v != nil {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[name] = raw
	}
	return json.Marshal(m, json.Deterministic(true))
}
