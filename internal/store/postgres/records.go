// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
)

// Baselines is the table of baseline records.
func (s *Store) Baselines() store.Baselines { return baselines{s} }

type baselines struct{ s *Store }

const baselineColumns = `id, owner, name, suite_owner, suite_name, experiment, candidate, coalesce(previous, ''), created_by, pinned_at, created_at`

func scanBaseline(row pgx.Row) (objects.Baseline, error) {
	var b objects.Baseline
	if err := row.Scan(&b.ID, &b.Owner, &b.Name, &b.Suite.Owner, &b.Suite.Name, &b.Experiment, &b.Candidate, &b.Previous, &b.CreatedBy, &b.PinnedAt, &b.CreatedAt); err != nil {
		return objects.Baseline{}, err
	}
	b.PinnedAt, b.CreatedAt = b.PinnedAt.UTC(), b.CreatedAt.UTC()
	return b, nil
}

func collectBaselines(rows pgx.Rows) ([]objects.Baseline, error) {
	defer rows.Close()
	var out []objects.Baseline
	for rows.Next() {
		b, err := scanBaseline(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a baseline: %w", err)
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading baselines: %w", err)
	}
	return out, nil
}

// currentBaseline is a lineage's latest record.
const currentBaseline = `SELECT ` + baselineColumns + ` FROM baselines
	WHERE owner = $1 AND suite_owner = $2 AND suite_name = $3 AND name = $4 ORDER BY id DESC LIMIT 1`

func (bs baselines) Pin(ctx context.Context, b objects.Baseline, pre store.Precondition) (objects.Baseline, bool, error) {
	var out objects.Baseline
	var made bool
	err := pgx.BeginFunc(ctx, bs.s.pool, func(tx pgx.Tx) error {
		now, err := bs.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := lock(ctx, tx, "examen.baseline:"+b.Owner+":"+b.Suite.Owner+":"+b.Suite.Name+":"+b.Name); err != nil {
			return err
		}
		cur, err := scanBaseline(tx.QueryRow(ctx, currentBaseline, b.Owner, b.Suite.Owner, b.Suite.Name, b.Name))
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: reading a baseline: %w", err)
		}
		if (pre.IfNoneMatch && found) || (pre.IfMatch != "" && cur.ID != pre.IfMatch) {
			return store.ErrPrecondition
		}
		if found && cur.Experiment == b.Experiment && cur.Candidate == b.Candidate {
			out = cur
			return nil
		}
		b.Previous, b.PinnedAt, b.CreatedAt = cur.ID, now, now
		var previous *string
		if found {
			previous = &cur.ID
		}
		if _, err := tx.Exec(ctx, `INSERT INTO baselines (id, owner, name, suite_owner, suite_name, experiment, candidate, previous, created_by, pinned_at, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)`,
			b.ID, b.Owner, b.Name, b.Suite.Owner, b.Suite.Name, b.Experiment, b.Candidate, previous, b.CreatedBy, now); err != nil {
			return fmt.Errorf("postgres: storing a baseline: %w", err)
		}
		out, made = b, true
		return nil
	})
	if err != nil {
		return objects.Baseline{}, false, err
	}
	return out, made, nil
}

func (bs baselines) ByID(ctx context.Context, id string) (objects.Baseline, error) {
	b, err := scanBaseline(bs.s.pool.QueryRow(ctx, `SELECT `+baselineColumns+` FROM baselines WHERE id = $1`, id))
	return b, one(err, "a baseline")
}

func (bs baselines) Current(ctx context.Context, owner string, suite objects.SuiteName, name string) (objects.Baseline, error) {
	b, err := scanBaseline(bs.s.pool.QueryRow(ctx, currentBaseline, owner, suite.Owner, suite.Name, name))
	return b, one(err, "a baseline")
}

func (bs baselines) History(ctx context.Context, owner string, suite objects.SuiteName, name string, p store.Page) ([]objects.Baseline, error) {
	w := &where{}
	w.add("owner = %s", owner)
	w.add("suite_owner = %s", suite.Owner)
	w.add("suite_name = %s", suite.Name)
	w.add("name = %s", name)
	tail := w.page("id", p)
	rows, err := bs.s.pool.Query(ctx, `SELECT `+baselineColumns+` FROM baselines`+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a baseline's records: %w", err)
	}
	return collectBaselines(rows)
}

func (bs baselines) List(ctx context.Context, owners []string, suite objects.SuiteName, p store.Page) ([]objects.Baseline, error) {
	w := &where{}
	w.add("owner = ANY(%s)", owners)
	w.add("suite_owner = %s", suite.Owner)
	w.add("suite_name = %s", suite.Name)
	inner := `SELECT DISTINCT ON (owner, name) ` + baselineColumns + ` FROM baselines` + w.sql() + ` ORDER BY owner, name, id DESC`
	outer := &where{args: w.args}
	tail := outer.page("id", p)
	rows, err := bs.s.pool.Query(ctx, `SELECT * FROM (`+inner+`) b`+outer.sql()+tail, outer.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing baselines: %w", err)
	}
	return collectBaselines(rows)
}

// Taxonomies is the table of taxonomy records.
func (s *Store) Taxonomies() store.Taxonomies { return taxonomies{s} }

type taxonomies struct{ s *Store }

const taxonomyColumns = `id, owner, suite_owner, suite_name, metric, version, canonical, map, source, coalesce(previous, ''), digest, created_by, created_at`

func scanTaxonomy(row pgx.Row) (objects.Taxonomy, error) {
	var t objects.Taxonomy
	var canonical, mapping, source []byte
	if err := row.Scan(&t.ID, &t.Owner, &t.Suite.Owner, &t.Suite.Name, &t.Metric, &t.Version, &canonical, &mapping, &source, &t.Previous, &t.Digest, &t.CreatedBy, &t.CreatedAt); err != nil {
		return objects.Taxonomy{}, err
	}
	t.CreatedAt = t.CreatedAt.UTC()
	if err := unmarshal(canonical, &t.Canonical); err != nil {
		return objects.Taxonomy{}, err
	}
	if err := unmarshal(mapping, &t.Map); err != nil {
		return objects.Taxonomy{}, err
	}
	return t, unmarshal(source, &t.Source)
}

const currentTaxonomy = `SELECT ` + taxonomyColumns + ` FROM taxonomies
	WHERE suite_owner = $1 AND suite_name = $2 AND metric = $3 ORDER BY version DESC LIMIT 1`

func (ts taxonomies) Append(ctx context.Context, t objects.Taxonomy, pre store.Precondition) (objects.Taxonomy, bool, error) {
	var out objects.Taxonomy
	var made bool
	err := pgx.BeginFunc(ctx, ts.s.pool, func(tx pgx.Tx) error {
		now, err := ts.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := lock(ctx, tx, "examen.taxonomy:"+t.Suite.Owner+":"+t.Suite.Name+":"+t.Metric); err != nil {
			return err
		}
		cur, err := scanTaxonomy(tx.QueryRow(ctx, currentTaxonomy, t.Suite.Owner, t.Suite.Name, t.Metric))
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: reading a taxonomy: %w", err)
		}
		if (pre.IfNoneMatch && found) || (pre.IfMatch != "" && cur.ID != pre.IfMatch) {
			return store.ErrPrecondition
		}
		if found && cur.Digest == t.Digest {
			out = cur
			return nil
		}
		t.Version, t.Previous, t.CreatedAt = cur.Version+1, cur.ID, now
		var previous *string
		if found {
			previous = &cur.ID
		}
		canonical, err := marshal(t.Canonical)
		if err != nil {
			return err
		}
		mapping, err := marshal(t.Map)
		if err != nil {
			return err
		}
		source, err := marshal(t.Source)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO taxonomies (id, owner, suite_owner, suite_name, metric, version, canonical, map, source, previous, digest, created_by, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)`,
			t.ID, t.Owner, t.Suite.Owner, t.Suite.Name, t.Metric, t.Version, canonical, mapping, source, previous, t.Digest, t.CreatedBy, now); err != nil {
			return fmt.Errorf("postgres: storing a taxonomy: %w", err)
		}
		out, made = t, true
		return nil
	})
	if err != nil {
		return objects.Taxonomy{}, false, err
	}
	return out, made, nil
}

func (ts taxonomies) ByID(ctx context.Context, id string) (objects.Taxonomy, error) {
	t, err := scanTaxonomy(ts.s.pool.QueryRow(ctx, `SELECT `+taxonomyColumns+` FROM taxonomies WHERE id = $1`, id))
	return t, one(err, "a taxonomy")
}

func (ts taxonomies) Current(ctx context.Context, suite objects.SuiteName, metric string) (objects.Taxonomy, error) {
	t, err := scanTaxonomy(ts.s.pool.QueryRow(ctx, currentTaxonomy, suite.Owner, suite.Name, metric))
	return t, one(err, "a taxonomy")
}

func (ts taxonomies) History(ctx context.Context, suite objects.SuiteName, metric string, p store.Page) ([]objects.Taxonomy, error) {
	w := &where{}
	w.add("suite_owner = %s", suite.Owner)
	w.add("suite_name = %s", suite.Name)
	w.add("metric = %s", metric)
	tail := w.page("id", p)
	rows, err := ts.s.pool.Query(ctx, `SELECT `+taxonomyColumns+` FROM taxonomies`+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a taxonomy's records: %w", err)
	}
	defer rows.Close()
	var out []objects.Taxonomy
	for rows.Next() {
		t, err := scanTaxonomy(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a taxonomy: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading taxonomies: %w", err)
	}
	return out, nil
}

// Labels counts the raw labels of a metric over the trials of the suite
// owner's experiments of the suite, reading each trial's latest generation
// of each grader.
func (ts taxonomies) Labels(ctx context.Context, suite objects.SuiteName, metric string) ([]store.LabelCount, error) {
	rows, err := ts.s.pool.Query(ctx, `SELECT value_text, count(*) FROM (
			SELECT DISTINCT ON (m.trial, m.grader) m.value_text
			FROM measurements m JOIN experiments e ON e.id = m.experiment
			WHERE e.owner = $1 AND e.suite_name = $2 AND m.metric = $3 AND m.trial IS NOT NULL
			ORDER BY m.trial, m.grader, m.generation DESC) latest
		WHERE value_text IS NOT NULL GROUP BY value_text ORDER BY count(*) DESC, value_text`, suite.Owner, suite.Name, metric)
	if err != nil {
		return nil, fmt.Errorf("postgres: counting raw labels: %w", err)
	}
	defer rows.Close()
	var out []store.LabelCount
	for rows.Next() {
		var l store.LabelCount
		if err := rows.Scan(&l.Label, &l.Count); err != nil {
			return nil, fmt.Errorf("postgres: reading a raw label: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading raw labels: %w", err)
	}
	return out, nil
}
