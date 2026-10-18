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

// Versions is the table of versions and their names' heads.
func (s *Store) Versions() store.Versions { return versions{s} }

type versions struct{ s *Store }

const versionColumns = `v.id, v.kind, v.owner, v.name, v.digest, v.body, v.created_at, coalesce(h.version = v.id, false)`

// versionFrom joins each version to its name's head, which says whether it
// is current.
const versionFrom = ` FROM versions v LEFT JOIN heads h ON h.kind = v.kind AND h.owner = v.owner AND h.name = v.name`

func scanVersion(row pgx.Row) (objects.Version, error) {
	var v objects.Version
	if err := row.Scan(&v.ID, &v.Kind, &v.Owner, &v.Name, &v.Digest, &v.Body, &v.CreatedAt, &v.Current); err != nil {
		return objects.Version{}, err
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return v, nil
}

func collectVersions(rows pgx.Rows) ([]objects.Version, error) {
	defer rows.Close()
	var out []objects.Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: reading a version: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: reading versions: %w", err)
	}
	return out, nil
}

// Apply stores a version unless its digest already is one of its name,
// and makes it current, under the lock of the name's head.
func (vs versions) Apply(ctx context.Context, in objects.Version, pre store.Precondition) (objects.Version, bool, error) {
	var out objects.Version
	var made bool
	err := pgx.BeginFunc(ctx, vs.s.pool, func(tx pgx.Tx) error {
		now, err := vs.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		if err := lock(ctx, tx, "examen.head:"+in.Kind+":"+in.Owner+":"+in.Name); err != nil {
			return err
		}
		var current string
		err = tx.QueryRow(ctx, `SELECT v.digest FROM heads h JOIN versions v ON v.id = h.version
			WHERE h.kind = $1 AND h.owner = $2 AND h.name = $3`, in.Kind, in.Owner, in.Name).Scan(&current)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("postgres: reading a name's current version: %w", err)
		}
		if (pre.IfNoneMatch && current != "") || (pre.IfMatch != "" && current != pre.IfMatch) {
			return store.ErrPrecondition
		}
		out, err = scanVersion(tx.QueryRow(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.kind = $1 AND v.owner = $2 AND v.digest = $3`, in.Kind, in.Owner, in.Digest))
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			made = true
			if _, err := tx.Exec(ctx, `INSERT INTO versions (id, kind, owner, name, digest, body, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				in.ID, in.Kind, in.Owner, in.Name, in.Digest, string(in.Body), now); err != nil {
				return fmt.Errorf("postgres: storing a version: %w", err)
			}
			out = in
			out.CreatedAt = now
		case err != nil:
			return fmt.Errorf("postgres: reading a version: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO heads (kind, owner, name, version, updated_at) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (kind, owner, name) DO UPDATE SET version = excluded.version, updated_at = excluded.updated_at`,
			in.Kind, in.Owner, in.Name, out.ID, now); err != nil {
			return fmt.Errorf("postgres: making a version current: %w", err)
		}
		out.Current = true
		return nil
	})
	if err != nil {
		return objects.Version{}, false, err
	}
	return out, made, nil
}

func (vs versions) ByID(ctx context.Context, kind, id string) (objects.Version, error) {
	v, err := scanVersion(vs.s.pool.QueryRow(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.kind = $1 AND v.id = $2`, kind, id))
	return v, one(err, "a version")
}

func (vs versions) ByDigest(ctx context.Context, kind, owner, digest string) (objects.Version, error) {
	v, err := scanVersion(vs.s.pool.QueryRow(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.kind = $1 AND v.owner = $2 AND v.digest = $3`, kind, owner, digest))
	return v, one(err, "a version")
}

func (vs versions) Current(ctx context.Context, kind, owner, name string) (objects.Version, error) {
	v, err := scanVersion(vs.s.pool.QueryRow(ctx, `SELECT `+versionColumns+` FROM heads h JOIN versions v ON v.id = h.version
		WHERE h.kind = $1 AND h.owner = $2 AND h.name = $3`, kind, owner, name))
	return v, one(err, "a name's current version")
}

func (vs versions) History(ctx context.Context, kind, owner, name string, p store.Page) ([]objects.Version, error) {
	w := &where{}
	w.add("v.kind = %s", kind)
	w.add("v.owner = %s", owner)
	w.add("v.name = %s", name)
	tail := w.page("v.id", p)
	rows, err := vs.s.pool.Query(ctx, `SELECT `+versionColumns+versionFrom+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing a name's versions: %w", err)
	}
	return collectVersions(rows)
}

func (vs versions) List(ctx context.Context, kind string, f store.VersionFilter, p store.Page) ([]objects.Version, error) {
	w := &where{}
	w.add("v.kind = %s", kind)
	w.add("v.owner = ANY(%s)", f.Owners)
	if !f.All {
		w.conds = append(w.conds, "h.version = v.id")
	}
	if f.Name != "" {
		w.add("v.name = %s", f.Name)
	}
	if f.MetricType != "" {
		w.add("v.body->>'type' = %s", f.MetricType)
	}
	if f.GraderKind != "" || f.CandidateKind != "" {
		w.add("v.body->>'kind' = %s", f.GraderKind+f.CandidateKind)
	}
	if f.Produces != "" {
		w.add("v.body->'produces' ? %s", f.Produces)
	}
	if f.Agent != "" {
		w.add("v.body->'topos'->>'agent' = %s", f.Agent)
	}
	if f.Model != "" {
		w.add("coalesce(v.body->'topos'->>'model', v.body->'program'->>'model', v.body->'external'->>'model') = %s", f.Model)
	}
	if f.Digests != nil {
		w.add("v.digest = ANY(%s)", f.Digests)
	}
	if len(f.Labels) > 0 {
		b, err := labels(f.Labels)
		if err != nil {
			return nil, err
		}
		w.add("coalesce(v.body->'metadata', '{}'::jsonb) @> %s::jsonb", b)
	}
	tail := w.page("v.id", p)
	rows, err := vs.s.pool.Query(ctx, `SELECT `+versionColumns+versionFrom+w.sql()+tail, w.args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing versions: %w", err)
	}
	return collectVersions(rows)
}

func (vs versions) Many(ctx context.Context, ids []string) (map[string]objects.Version, error) {
	rows, err := vs.s.pool.Query(ctx, `SELECT `+versionColumns+versionFrom+` WHERE v.id = ANY($1)`, ids)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading versions: %w", err)
	}
	list, err := collectVersions(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]objects.Version, len(list))
	for _, v := range list {
		out[v.ID] = v
	}
	return out, nil
}
