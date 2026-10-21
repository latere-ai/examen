// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"time"

	"github.com/jackc/pgx/v5"

	"latere.ai/x/examen/internal/objects"
	"latere.ai/x/examen/internal/store"
	"latere.ai/x/examen/internal/usage"
)

// Usage keeps what each experiment's external Key was used for.
func (s *Store) Usage() store.Usage { return usageTable{s} }

type usageTable struct{ s *Store }

// Hold records the experiment's Key, the newest the key source answered.
func (u usageTable) Hold(ctx context.Context, experiment, owner, keyID string) error {
	if _, err := u.s.pool.Exec(ctx, `INSERT INTO experiment_usage (experiment, owner, key_id, created_at)
		VALUES ($1, $2, $3, coalesce($4::timestamptz, now()))
		ON CONFLICT (experiment) DO UPDATE SET key_id = excluded.key_id WHERE experiment_usage.key_id <> excluded.key_id`,
		experiment, owner, keyID, u.s.at()); err != nil {
		return fmt.Errorf("postgres: recording an experiment's key: %w", err)
	}
	return nil
}

// Unreleased answers the Keys of ended experiments not yet released.
func (u usageTable) Unreleased(ctx context.Context, experiment string, limit int) ([]store.KeyHold, error) {
	rows, err := u.s.pool.Query(ctx, `SELECT x.experiment, x.owner, x.key_id FROM experiment_usage x JOIN experiments e ON e.id = x.experiment
		WHERE x.released_at IS NULL AND e.state IN ('complete', 'canceled') AND ($1 = '' OR x.experiment = $1)
		ORDER BY x.created_at, x.experiment LIMIT $2`, experiment, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the keys to release: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.KeyHold, error) {
		var h store.KeyHold
		err := row.Scan(&h.Experiment, &h.Owner, &h.KeyID)
		return h, err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the keys to release: %w", err)
	}
	return out, nil
}

// Released records that an experiment's Key was released.
func (u usageTable) Released(ctx context.Context, experiment string) error {
	if _, err := u.s.pool.Exec(ctx, `UPDATE experiment_usage SET released_at = coalesce($2::timestamptz, now()) WHERE experiment = $1 AND released_at IS NULL`,
		experiment, u.s.at()); err != nil {
		return fmt.Errorf("postgres: recording a released key: %w", err)
	}
	return nil
}

// Due answers the experiments whose records are due a read.
func (u usageTable) Due(ctx context.Context, before time.Time, limit int) ([]store.UsageRead, error) {
	rows, err := u.s.pool.Query(ctx, `SELECT x.experiment, x.owner, e.state, x.created_at, x.read_to, x.ended_at
		FROM experiment_usage x JOIN experiments e ON e.id = x.experiment
		WHERE NOT x.done AND (x.read_at IS NULL OR x.read_at <= $1)
		ORDER BY x.read_at NULLS FIRST, x.experiment LIMIT $2`, before, limit)
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the experiments due a usage read: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (store.UsageRead, error) {
		var r store.UsageRead
		err := row.Scan(&r.Experiment, &r.Owner, &r.State, &r.CreatedAt, &r.ReadTo, &r.EndedAt)
		r.CreatedAt, r.ReadTo, r.EndedAt = r.CreatedAt.UTC(), ptr(r.ReadTo), ptr(r.EndedAt)
		return r, err
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: reading the experiments due a usage read: %w", err)
	}
	return out, nil
}

// keepRecord keeps one record once by its request id. The trial is the
// one its label names in the experiment, else the one that submitted its
// request id. A record read again unchanged returns no row, so only a new
// or a changed one unsettles its trial.
const keepRecord = `INSERT INTO usage_records (experiment, request_id, trial, at, label, sent_model, served, provider, status, error,
		tokens, cost_micro, currency, priced, record, read_at)
	VALUES ($1, $2,
		coalesce((SELECT id FROM trials WHERE id = $3 AND experiment = $1),
			(SELECT id FROM trials WHERE experiment = $1 AND observed->'lux_requests' ? $2 ORDER BY id LIMIT 1)),
		$4, $3, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	ON CONFLICT (experiment, request_id) DO UPDATE SET trial = coalesce(excluded.trial, usage_records.trial), at = excluded.at,
		label = excluded.label, sent_model = excluded.sent_model, served = excluded.served, provider = excluded.provider,
		status = excluded.status, error = excluded.error, tokens = excluded.tokens, cost_micro = excluded.cost_micro,
		currency = excluded.currency, priced = excluded.priced, record = excluded.record, read_at = excluded.read_at
	WHERE usage_records.record IS DISTINCT FROM excluded.record OR usage_records.trial IS DISTINCT FROM coalesce(excluded.trial, usage_records.trial)
	RETURNING trial`

// Read keeps a window's records and moves the experiment's read.
func (u usageTable) Read(ctx context.Context, experiment string, records []store.UsageRecord, to time.Time, done bool) error {
	return pgx.BeginFunc(ctx, u.s.pool, func(tx pgx.Tx) error {
		now, err := u.s.txNow(ctx, tx)
		if err != nil {
			return err
		}
		touched := map[string]bool{}
		for _, r := range records {
			var trial *string
			err := tx.QueryRow(ctx, keepRecord, experiment, r.RequestID, r.Label, r.At.UTC(), r.SentModel, r.Served, r.Provider, r.Status, r.Error,
				string(r.Tokens), r.CostMicro, r.Currency, r.Priced, string(r.Record), now).Scan(&trial)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				continue
			case err != nil:
				return fmt.Errorf("postgres: keeping a usage record: %w", err)
			}
			if trial != nil {
				touched[*trial] = true
			}
		}
		for trial := range touched {
			if _, err := tx.Exec(ctx, `UPDATE trials SET usage_settled_at = NULL WHERE id = $1`, trial); err != nil {
				return fmt.Errorf("postgres: unsettling a trial's cost: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE experiment_usage x SET read_to = $2, read_at = $3, done = $4,
			ended_at = coalesce(x.ended_at, (SELECT CASE WHEN e.state IN ('complete', 'canceled') THEN $3::timestamptz END FROM experiments e WHERE e.id = x.experiment))
			WHERE experiment = $1`, experiment, to.UTC(), now, done); err != nil {
			return fmt.Errorf("postgres: moving an experiment's usage read: %w", err)
		}
		return nil
	})
}

// Settle settles the cost of the experiment's done trials that ended by
// before and are not settled.
func (u usageTable) Settle(ctx context.Context, experiment string, before time.Time) (int, error) {
	var ids []string
	rows, err := u.s.pool.Query(ctx, `SELECT id FROM trials WHERE experiment = $1 AND state = 'done' AND ended_at IS NOT NULL
		AND ended_at <= $2 AND usage_settled_at IS NULL ORDER BY id`, experiment, before)
	if err != nil {
		return 0, fmt.Errorf("postgres: reading the trials to settle: %w", err)
	}
	if ids, err = pgx.CollectRows(rows, pgx.RowTo[string]); err != nil {
		return 0, fmt.Errorf("postgres: reading the trials to settle: %w", err)
	}
	if len(ids) == 0 {
		return 0, nil
	}
	x := experiments(u)
	settled := 0
	err = x.begin(ctx, experiment, func(c *change) error {
		for _, tid := range ids {
			ok, err := settleTrial(ctx, c, tid)
			if err != nil {
				return err
			}
			if ok {
				settled++
			}
		}
		if settled > 0 {
			return c.dirty(ctx)
		}
		return nil
	})
	return settled, err
}

// settleTrial claims the records of the request ids the trial submitted,
// reads every record of it, writes its layers, and, for a finished trial
// with records, appends a generation of the built-in grader with its cost
// and tokens beside the values it had. It answers whether it wrote a
// generation.
func settleTrial(ctx context.Context, c *change, tid string) (bool, error) {
	t, err := scanTrial(c.tx.QueryRow(ctx, `SELECT `+trialColumns+` FROM trials WHERE id = $1 FOR UPDATE`, tid))
	if err != nil {
		return false, fmt.Errorf("postgres: reading a trial to settle: %w", err)
	}
	if len(t.Observed.LuxRequests) > 0 {
		if _, err := c.tx.Exec(ctx, `UPDATE usage_records SET trial = $2 WHERE experiment = $1 AND trial IS NULL AND request_id = ANY($3)`,
			t.Experiment, t.ID, t.Observed.LuxRequests); err != nil {
			return false, fmt.Errorf("postgres: claiming a trial's records: %w", err)
		}
	}
	rows, err := c.tx.Query(ctx, `SELECT sent_model, served, status, tokens, cost_micro, currency, priced FROM usage_records WHERE trial = $1 ORDER BY at, request_id`, t.ID)
	if err != nil {
		return false, fmt.Errorf("postgres: reading a trial's records: %w", err)
	}
	calls, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (usage.Call, error) {
		var call usage.Call
		var tokens []byte
		if err := row.Scan(&call.SentModel, &call.Served, &call.Status, &tokens, &call.Cost, &call.Currency, &call.Priced); err != nil {
			return call, err
		}
		return call, json.Unmarshal(tokens, &call.Tokens)
	})
	if err != nil {
		return false, fmt.Errorf("postgres: reading a trial's records: %w", err)
	}
	l := usage.Of(calls)
	observed, err := marshal(l.Observe(t.Observed))
	if err != nil {
		return false, err
	}
	var sent *string
	if len(calls) > 0 {
		if sent, err = marshalNullable(l.Sent); err != nil {
			return false, err
		}
	}
	if _, err := c.tx.Exec(ctx, `UPDATE trials SET observed = $2, sent = $3, usage_settled_at = $4 WHERE id = $1`, t.ID, observed, sent, c.now); err != nil {
		return false, fmt.Errorf("postgres: writing a trial's layers: %w", err)
	}
	if len(calls) == 0 || !t.Finished() {
		return false, nil
	}
	values := objects.BuiltinValues(t)
	maps.Copy(values, l.Builtin())
	var builtin []store.Measured
	for _, m := range objects.Builtins() {
		if v, ok := values[objects.BuiltinDigest(m.Name)]; ok {
			builtin = append(builtin, store.Measured{Metric: objects.BuiltinDigest(m.Name), Value: v})
		}
	}
	_, err = c.grading(ctx, t, objects.GraderBuiltin, builtin)
	return true, err
}

// Summary answers the experiment's usage, nil when its runs hold no Key.
func (u usageTable) Summary(ctx context.Context, experiment string) (*objects.Usage, error) {
	var out objects.Usage
	var readTo *time.Time
	err := u.s.pool.QueryRow(ctx, `SELECT x.read_to,
		count(r.request_id),
		coalesce(sum(r.cost_micro) FILTER (WHERE r.status <> 'refused' AND r.priced AND r.currency = $2), 0),
		count(r.request_id) FILTER (WHERE r.status <> 'refused' AND NOT (r.priced AND r.currency = $2)),
		count(r.request_id) FILTER (WHERE r.trial IS NULL),
		coalesce(sum(r.cost_micro) FILTER (WHERE r.trial IS NULL AND r.status <> 'refused' AND r.priced AND r.currency = $2), 0),
		count(r.request_id) FILTER (WHERE r.trial IS NULL AND r.status <> 'refused' AND NOT (r.priced AND r.currency = $2))
		FROM experiment_usage x LEFT JOIN usage_records r ON r.experiment = x.experiment
		WHERE x.experiment = $1 GROUP BY x.read_to`, experiment, usage.Currency).Scan(&readTo,
		&out.Requests, &out.CostUSDMicro, &out.UnpricedRequests,
		&out.Unattributed.Requests, &out.Unattributed.CostUSDMicro, &out.Unattributed.UnpricedRequests)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("postgres: reading an experiment's usage: %w", err)
	}
	out.ReadTo = ptr(readTo)
	return &out, nil
}

// KeyID answers the id of the experiment's Key.
func (u usageTable) KeyID(ctx context.Context, experiment string) (string, error) {
	var id string
	err := u.s.pool.QueryRow(ctx, `SELECT key_id FROM experiment_usage WHERE experiment = $1`, experiment).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("postgres: reading an experiment's key: %w", err)
	}
	return id, nil
}
