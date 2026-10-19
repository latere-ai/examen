// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres_test

import (
	"context"
	"testing"
	"time"

	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/store/postgres"
	"latere.ai/x/examen/internal/store/postgres/pgtest"
)

// TestUsageFailsWhole: a database that does not answer fails every read
// and write of an experiment's usage with an error, and none of them
// answers a figure it did not read.
func TestUsageFailsWhole(t *testing.T) {
	st := pgtest.Open(t, false, postgres.Options{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	u := st.Usage()
	rec := store.UsageRecord{RequestID: "req_1", At: time.Now(), Status: "ok", Tokens: []byte(`{}`), Record: []byte(`{}`)}
	if err := u.Hold(ctx, "exp_1", owner, "key-1"); err == nil {
		t.Error("Hold")
	}
	if _, err := u.Unreleased(ctx, "", 10); err == nil {
		t.Error("Unreleased")
	}
	if err := u.Released(ctx, "exp_1"); err == nil {
		t.Error("Released")
	}
	if _, err := u.Due(ctx, time.Now(), 10); err == nil {
		t.Error("Due")
	}
	if err := u.Read(ctx, "exp_1", []store.UsageRecord{rec}, time.Now(), false); err == nil {
		t.Error("Read")
	}
	if _, err := u.Settle(ctx, "exp_1", time.Now()); err == nil {
		t.Error("Settle")
	}
	if got, err := u.Summary(ctx, "exp_1"); err == nil || got != nil {
		t.Errorf("Summary: %+v %v", got, err)
	}
	if id, err := u.KeyID(ctx, "exp_1"); err == nil || id != "" {
		t.Errorf("KeyID: %q %v", id, err)
	}
}
