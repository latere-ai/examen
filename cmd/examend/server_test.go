// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"log/slog"
	"net/http"
	"testing"

	"latere.ai/x/examen/internal/limits"
)

// The API's server holds a client to READ_HEADER_TIMEOUT for its headers
// and READ_TIMEOUT for its whole request, sets no write timeout, which
// would cut a stream, and closes an idle connection after IDLE_TIMEOUT.
func TestTheServerBoundsItsReads(t *testing.T) {
	s := newHTTPServer(http.NotFoundHandler(), slog.New(slog.DiscardHandler))
	if s.ReadHeaderTimeout != limits.ReadHeaderTimeout || s.ReadTimeout != limits.ReadTimeout ||
		s.IdleTimeout != limits.IdleTimeout || s.WriteTimeout != 0 {
		t.Fatalf("read header %v, read %v, idle %v, write %v", s.ReadHeaderTimeout, s.ReadTimeout, s.IdleTimeout, s.WriteTimeout)
	}
}
