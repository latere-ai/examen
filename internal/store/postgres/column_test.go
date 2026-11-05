// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"testing"
)

// TestAValueThatCannotRenderIsRefused: a column value JSON cannot render is
// refused with an error, so a row is never written with a column it lost,
// and a nullable column refuses it the same way rather than storing NULL.
func TestAValueThatCannotRenderIsRefused(t *testing.T) {
	if s, err := marshal(make(chan int)); err == nil {
		t.Fatalf("a channel rendered as %q", s)
	}
	if s, err := marshalNullable(make(chan int)); err == nil || s != nil {
		t.Fatalf("a nullable column took a channel as %v, %v", s, err)
	}
	if err := unmarshal([]byte("{"), &struct{}{}); err == nil {
		t.Fatal("a column that is not JSON was read")
	}
}

// TestTheSchemaCheckOfADatabaseThatDoesNotAnswer: the schema check over a
// connection of its own answers ErrUnreachable when nothing listens, the
// error a caller reads as a database that is down rather than one that is
// behind.
func TestTheSchemaCheckOfADatabaseThatDoesNotAnswer(t *testing.T) {
	_, err := schemaAt(context.Background(), "postgres://examen@127.0.0.1:1/examen?connect_timeout=2&sslmode=disable", 1)
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("the schema check of a closed port answered %v", err)
	}
}
