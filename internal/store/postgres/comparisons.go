// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

const comparisonColumns = `id, owner, experiment, candidate, coalesce(baseline, ''), coalesce(reference_candidate, ''), coalesce(task, ''),
	coalesce(reference_task, ''), automatic, declared, exploratory, state, final, variable, declared_provenance, computed, declaration, created_at, updated_at`

func scanComparison(row pgx.Row) (objects.Comparison, error) {
	var c objects.Comparison
	var variable, declared, computed, declaration []byte
	if err := row.Scan(&c.ID, &c.Owner, &c.Experiment, &c.Candidate, &c.Baseline, &c.ReferenceCandidate, &c.Task, &c.ReferenceTask,
		&c.Automatic, &c.DeclaredIndex, &c.Exploratory, &c.State, &c.Final, &variable, &declared, &computed, &declaration, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return objects.Comparison{}, err
	}
	c.CreatedAt, c.UpdatedAt = c.CreatedAt.UTC(), c.UpdatedAt.UTC()
	for _, f := range []struct {
		raw []byte
		v   any
	}{{variable, &c.Variable}, {declared, &c.DeclaredProvenance}, {computed, &c.Computed}, {declaration, &c.Declaration}} {
		if err := unmarshal(f.raw, f.v); err != nil {
			return objects.Comparison{}, err
		}
	}
	if c.Confounds == nil {
		c.Confounds = []string{}
	}
	if c.Results == nil {
		c.Results = []objects.ResultRow{}
	}
	return c, nil
}

func collectComparisons(rows pgx.Rows) ([]objects.Comparison, error) {
	defer rows.Close()
	var out []objects.Comparison
	for rows.Next() {
		c, err := scanComparison(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a comparison: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading comparisons: %w", err)
	}
	return out, nil
}

// nullable is a string column that is NULL when empty.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// insertComparison stores a comparison, dirty, so the next computation
// reads it.
func insertComparison(ctx context.Context, tx pgx.Tx, c objects.Comparison, now time.Time) error {
	if c.Variable == nil {
		c.Variable = []string{}
	}
	if c.DeclaredProvenance == nil {
		c.DeclaredProvenance = []string{}
	}
	variable, err := marshal(c.Variable)
	if err != nil {
		return err
	}
	declared, err := marshal(c.DeclaredProvenance)
	if err != nil {
		return err
	}
	computed, err := marshal(c.Computed)
	if err != nil {
		return err
	}
	var declaration *string
	if c.Declaration != nil {
		raw, err := marshal(c.Declaration)
		if err != nil {
			return err
		}
		declaration = &raw
	}
	if _, err := tx.Exec(ctx, `INSERT INTO comparisons (id, owner, experiment, candidate, baseline, reference_candidate, task, reference_task,
			automatic, declared, exploratory, state, final, dirty, variable, declared_provenance, computed, declaration, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, true, $14, $15, $16, $17, $18, $18)`,
		c.ID, c.Owner, c.Experiment, c.Candidate, nullable(c.Baseline), nullable(c.ReferenceCandidate), nullable(c.Task), nullable(c.ReferenceTask),
		c.Automatic, c.DeclaredIndex, c.Exploratory, c.State, c.Final, variable, declared, computed, declaration, now); err != nil {
		return fmt.Errorf("postgres: storing a comparison: %w", err)
	}
	return nil
}

func (x experiments) Comparisons(ctx context.Context, f store.ComparisonFilter, p store.Page) ([]objects.Comparison, error) {
	w := &where{}
	if f.Owners != nil {
		w.add("c.owner = ANY(%s)", f.Owners)
	}
	if f.Experiment != "" {
		w.add("c.experiment = %s", f.Experiment)
	}
	if f.Suite != "" {
		w.add("e.suite = %s", f.Suite)
	}
	if f.SuiteName != "" {
		w.add("e.suite_name = %s", f.SuiteName)
	}
	if f.Candidate != "" {
		w.add("c.candidate = %s", f.Candidate)
	}
	if f.Baseline != "" {
		w.add("c.baseline = %s", f.Baseline)
	}
	if f.ReferenceCandidate != "" {
		w.add("c.reference_candidate = %s", f.ReferenceCandidate)
	}
	for _, b := range []struct {
		column string
		v      *bool
	}{{"c.automatic", f.Automatic}, {"c.exploratory", f.Exploratory}, {"c.final", f.Final}, {"(c.declared IS NOT NULL)", f.Declared}} {
		if b.v != nil {
			w.add(b.column+" = %s", *b.v)
		}
	}
	if f.SingleVariable != nil {
		w.add("coalesce((c.computed->>'single_variable')::boolean, false) = %s", *f.SingleVariable)
	}
	tail := w.page("c.id", p)
	rows, err := x.s.pool.Query(ctx, `SELECT `+comparisonColumnsOfC+` FROM comparisons c JOIN experiments e ON e.id = c.experiment`+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing comparisons: %w", err)
	}
	return collectComparisons(rows)
}

// comparisonColumnsOfC are the comparison columns of the alias c.
const comparisonColumnsOfC = `c.id, c.owner, c.experiment, c.candidate, coalesce(c.baseline, ''), coalesce(c.reference_candidate, ''), coalesce(c.task, ''),
	coalesce(c.reference_task, ''), c.automatic, c.declared, c.exploratory, c.state, c.final, c.variable, c.declared_provenance, c.computed, c.declaration, c.created_at, c.updated_at`

func (x experiments) Comparison(ctx context.Context, cid string) (objects.Comparison, error) {
	c, err := scanComparison(x.s.pool.QueryRow(ctx, `SELECT `+comparisonColumns+` FROM comparisons WHERE id = $1`, cid))
	return c, one(err, "a comparison")
}

func (x experiments) CreateComparison(ctx context.Context, c objects.Comparison) error {
	return pgx.BeginFunc(ctx, x.s.pool, func(tx pgx.Tx) error {
		now, err := x.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		return insertComparison(ctx, tx, c, now)
	})
}

func (x experiments) Dirty(ctx context.Context, limit int) ([]objects.Comparison, error) {
	rows, err := x.s.pool.Query(ctx, `SELECT `+comparisonColumns+` FROM comparisons WHERE dirty ORDER BY updated_at LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading comparisons to compute: %w", err)
	}
	return collectComparisons(rows)
}

func (x experiments) Computed(ctx context.Context, cid, state string, computed objects.Computed, inputs []string) error {
	c, err := x.Comparison(ctx, cid)
	if err != nil {
		return err
	}
	return x.begin(ctx, c.Experiment, func(ch *change) error {
		if state == objects.ComparisonComplete {
			at := ch.now
			computed.ComputedAt = &at
		}
		raw, err := marshal(computed)
		if err != nil {
			return err
		}
		variable, declared := c.Variable, c.DeclaredProvenance
		if computed.VariableFields != nil {
			variable = computed.VariableFields
		}
		if computed.Declared != nil {
			declared = computed.Declared
		}
		rawVariable, err := marshal(variable)
		if err != nil {
			return err
		}
		rawDeclared, err := marshal(declared)
		if err != nil {
			return err
		}
		if _, err := ch.tx.Exec(ctx, `UPDATE comparisons SET state = $2, computed = $3, variable = $4, declared_provenance = $5, dirty = false, updated_at = $6 WHERE id = $1`,
			cid, state, raw, rawVariable, rawDeclared, ch.now); err != nil {
			return fmt.Errorf("postgres: storing a comparison's results: %w", err)
		}
		// A provisional comparison replaces what it read; a final one's
		// inputs never change after this.
		if _, err := ch.tx.Exec(ctx, `DELETE FROM comparison_inputs WHERE comparison = $1`, cid); err != nil {
			return fmt.Errorf("postgres: replacing a comparison's inputs: %w", err)
		}
		if len(inputs) > 0 {
			if _, err := ch.tx.Exec(ctx, `INSERT INTO comparison_inputs (comparison, measurement) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`, cid, inputs); err != nil {
				return fmt.Errorf("postgres: storing a comparison's inputs: %w", err)
			}
		}
		data := map[string]any{
			"id": c.ID, "candidate": c.Candidate, "baseline": c.Baseline, "reference_candidate": c.ReferenceCandidate,
			"automatic": c.Automatic, "final": c.Final,
		}
		if c.DeclaredIndex != nil {
			data["declared"] = *c.DeclaredIndex
		}
		return ch.j.add(ctx, objects.EventComparison, data)
	})
}

func (x experiments) Clean(ctx context.Context, cid string, seen time.Time) error {
	if _, err := x.s.pool.Exec(ctx, `UPDATE comparisons SET dirty = false WHERE id = $1 AND updated_at <= $2`, cid, seen); err != nil {
		return fmt.Errorf("postgres: clearing a comparison's mark: %w", err)
	}
	return nil
}

func (x experiments) Declare(ctx context.Context, eid string, correct func(objects.Experiment, []objects.Comparison) (objects.DeclaredAnalysis, error)) error {
	return x.begin(ctx, eid, func(ch *change) error {
		if len(ch.e.Comparisons) == 0 && ch.e.Independence == nil {
			return nil
		}
		// The newest record of each declared comparison: a later grading
		// or a retried cell makes a new one, which the analysis reads.
		rows, err := ch.tx.Query(ctx, `SELECT DISTINCT ON (declared) `+comparisonColumns+` FROM comparisons
			WHERE experiment = $1 AND declared IS NOT NULL ORDER BY declared, created_at DESC, id DESC`, ch.e.ID)
		if err != nil {
			return fmt.Errorf("postgres: reading declared comparisons: %w", err)
		}
		family, err := collectComparisons(rows)
		if err != nil {
			return err
		}
		a, err := correct(ch.e, family)
		if err != nil {
			return err
		}
		at := ch.now
		a.ComputedAt = &at
		raw, err := marshal(a)
		if err != nil {
			return err
		}
		if _, err := ch.tx.Exec(ctx, `UPDATE experiments SET declared = $2 WHERE id = $1`, ch.e.ID, raw); err != nil {
			return fmt.Errorf("postgres: storing a declared analysis: %w", err)
		}
		return nil
	})
}

func (x experiments) DeclaredAnalysis(ctx context.Context, eid string) (objects.DeclaredAnalysis, bool, error) {
	var raw []byte
	err := x.s.pool.QueryRow(ctx, `SELECT declared FROM experiments WHERE id = $1`, eid).Scan(&raw)
	if err := one(err, "an experiment"); err != nil {
		return objects.DeclaredAnalysis{}, false, err
	}
	if raw == nil {
		return objects.DeclaredAnalysis{}, false, nil
	}
	var a objects.DeclaredAnalysis
	if err := unmarshal(raw, &a); err != nil {
		return objects.DeclaredAnalysis{}, false, err
	}
	return a, true, nil
}
