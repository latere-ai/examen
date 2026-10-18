// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"latere.ai/x/examen/authorizer"
	"latere.ai/x/examen/internal/access"
	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/limits"
)

// streamBatch is how many events one poll of the log reads.
const streamBatch = 200

// acquire takes one of the subject's streams on this replica, and false
// when it holds MAX_STREAMS_PER_SUBJECT already.
func (s *Server) acquire(subject string) bool {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams == nil {
		s.streams = map[string]int{}
	}
	if s.streams[subject] >= limits.MaxStreamsPerSubject {
		return false
	}
	s.streams[subject]++
	return true
}

// release gives a stream back.
func (s *Server) release(subject string) {
	s.streamsMu.Lock()
	defer s.streamsMu.Unlock()
	if s.streams[subject]--; s.streams[subject] <= 0 {
		delete(s.streams, subject)
	}
}

// frame writes one frame of a stream: its id when it has one, its event
// and its data.
func frame(w io.Writer, seq int64, event string, data []byte) error {
	idLine := ""
	if seq > 0 {
		idLine = "id: " + strconv.FormatInt(seq, 10) + "\n"
	}
	_, err := fmt.Fprintf(w, "%sevent: %s\ndata: %s\n\n", idLine, event, data)
	return err
}

// stream serves the experiment's event log as server-sent events
// (specs/003-api.md): a snapshot first unless the client resumes after an
// event, every event after it in order, polled every STREAM_POLL_INTERVAL
// on any replica, a keepalive comment after STREAM_KEEPALIVE of silence,
// and an end frame once the experiment is complete or canceled and every
// event is sent. It closes at its bearer's expiry, and with an error frame
// when experiment.read is denied while it is open.
func (s *Server) stream(w http.ResponseWriter, r *http.Request, c access.Caller) error {
	e, err := s.readExperiment(r, c, r.PathValue("id"))
	if err != nil {
		return err
	}
	latest, err := s.Store.Experiments().LatestSeq(r.Context(), e.ID)
	if err != nil {
		return apierr.Internal(err)
	}
	after, resumed := latest, false
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 || n > latest {
			return apierr.OnField(apierr.CodeInvalidCursor, "Last-Event-ID", "Last-Event-ID is a sequence this experiment's log issued")
		}
		after, resumed = n, true
	}
	if !s.acquire(c.Subject) {
		return apierr.Newf(apierr.CodeTooManyStreams, "the subject holds MAX_STREAMS_PER_SUBJECT (%d) streams on this replica", limits.MaxStreamsPerSubject)
	}
	defer s.release(c.Subject)

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flush := func() bool { return rc.Flush() == nil }
	if !resumed {
		snapshot, err := json.Marshal(e)
		if err != nil || frame(w, latest, "snapshot", snapshot) != nil || !flush() {
			return nil
		}
	} else if !flush() {
		return nil
	}

	poll, keepalive := limits.StreamPollInterval, limits.StreamKeepalive
	if s.StreamPoll > 0 {
		poll = s.StreamPoll
	}
	if s.StreamKeepalive > 0 {
		keepalive = s.StreamKeepalive
	}
	var expiry <-chan time.Time
	if !c.Expires.IsZero() {
		t := time.NewTimer(time.Until(c.Expires))
		defer t.Stop()
		expiry = t.C
	}
	tick := time.NewTicker(poll)
	defer tick.Stop()
	quiet := time.Now()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-expiry:
			// The bearer lapsed: nothing more is sent, and the client
			// reconnects with a fresh one and Last-Event-ID.
			return nil
		case <-tick.C:
		}
		events, err := s.Store.Experiments().Events(r.Context(), e.ID, after, streamBatch)
		if err != nil {
			return s.streamError(w, r, apierr.New(apierr.CodeUnavailable, "the event log could not be read"))
		}
		if len(events) > 0 {
			d, err := s.ask(r, c, access.Ask(r, authorizer.ActionExperimentRead, access.Resource(authorizer.ActionExperimentRead, e.ID, experimentMembers(e, c))))
			switch {
			case err != nil:
				return s.streamError(w, r, err)
			case !d.Allow:
				return s.streamError(w, r, apierr.New(apierr.CodeExperimentNotFound, "experiment.read was denied while the stream was open"))
			}
			for _, ev := range events {
				if frame(w, ev.Seq, ev.Type, ev.Data) != nil {
					return nil
				}
				after = ev.Seq
			}
			if !flush() {
				return nil
			}
			quiet = time.Now()
			if len(events) == streamBatch {
				continue
			}
		}
		now, err := s.Store.Experiments().Get(r.Context(), e.ID)
		if err != nil {
			return s.streamError(w, r, apierr.New(apierr.CodeUnavailable, "the experiment could not be read"))
		}
		if now.Finished() {
			last, err := s.Store.Experiments().LatestSeq(r.Context(), e.ID)
			if err == nil && last == after {
				end, err := json.Marshal(map[string]string{"state": now.State})
				if err != nil || frame(w, 0, "end", end) != nil {
					return nil
				}
				flush()
				return nil
			}
		}
		if time.Since(quiet) >= keepalive {
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil || !flush() {
				return nil
			}
			quiet = time.Now()
		}
	}
}

// streamError sends an error frame in the envelope and ends the stream.
func (s *Server) streamError(w http.ResponseWriter, r *http.Request, err error) error {
	e := apierr.Classify(err)
	if e.Code == apierr.CodeInternal {
		s.log().ErrorContext(r.Context(), "a stream failed", "route", r.Pattern, "error", apierr.LogText(e.Cause()))
	}
	_, body := e.Envelope()
	data, mErr := json.Marshal(map[string]any{"error": body})
	if mErr != nil {
		// The stream is already answered; what is left is to say why it
		// ends without its error frame.
		s.log().ErrorContext(r.Context(), "an error frame could not be rendered", "error", mErr.Error())
		return nil
	}
	if frame(w, 0, "error", data) == nil {
		if ferr := http.NewResponseController(w).Flush(); ferr != nil {
			s.log().DebugContext(r.Context(), "an error frame was not flushed", "error", ferr.Error())
		}
	}
	return nil
}
