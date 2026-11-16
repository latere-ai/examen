// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// errRead is the error a failing result reports.
var errRead = errors.New("the connection was lost mid-read")

// failingRows is a result that fails. With scan set it holds one row whose
// scan fails, as a column the reader cannot decode leaves it; otherwise it
// holds no row and reports an error at the end, as a connection lost
// mid-read leaves it.
type failingRows struct {
	scan bool
	read bool
}

func (r *failingRows) Next() bool {
	if r.scan && !r.read {
		r.read = true
		return true
	}
	return false
}

func (r *failingRows) Err() error {
	if r.scan {
		return nil
	}
	return errRead
}

func (r *failingRows) Scan(...any) error                            { return errRead }
func (r *failingRows) Values() ([]any, error)                       { return nil, errRead }
func (r *failingRows) Close()                                       {}
func (r *failingRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *failingRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *failingRows) RawValues() [][]byte                          { return nil }
func (r *failingRows) Conn() *pgx.Conn                              { return nil }
func (r *failingRows) TypeMap() *pgtype.Map                         { return nil }

// TestAFailedReadIsReported: a list read whose row cannot be scanned, or
// whose result fails after its last row, answers the read's error and no
// partial list, whichever kind of record it reads.
func TestAFailedReadIsReported(t *testing.T) {
	collect := map[string]func(pgx.Rows) (int, error){
		"cells":  func(r pgx.Rows) (int, error) { out, err := collectCells(r); return len(out), err },
		"trials": func(r pgx.Rows) (int, error) { out, err := collectTrials(r); return len(out), err },
		"measurements": func(r pgx.Rows) (int, error) {
			out, err := collectMeasurements(r)
			return len(out), err
		},
		"comparisons": func(r pgx.Rows) (int, error) {
			out, err := collectComparisons(r)
			return len(out), err
		},
		"gradings": func(r pgx.Rows) (int, error) {
			out, err := collectGradings(r, "gradings")
			return len(out), err
		},
		"baselines": func(r pgx.Rows) (int, error) { out, err := collectBaselines(r); return len(out), err },
		"versions":  func(r pgx.Rows) (int, error) { out, err := collectVersions(r); return len(out), err },
	}
	for name, read := range collect {
		for _, scan := range []bool{true, false} {
			n, err := read(&failingRows{scan: scan})
			if !errors.Is(err, errRead) || n != 0 {
				t.Errorf("%s (failing scan %v): %d records, %v; want none and the read's error", name, scan, n, err)
			}
		}
	}
}
