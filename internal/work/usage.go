// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package work

import (
	"context"
	"encoding/json/v2"
	"errors"
	"time"

	"latere.ai/x/examen/internal/apierr"
	"latere.ai/x/examen/internal/keys"
	"latere.ai/x/examen/internal/limits"
	"latere.ai/x/examen/internal/store"
)

// The usage job (specs/005-execution.md, "Cost and the sent and observed
// layers", "Attribution and cost"): every USAGE_EVERY each experiment whose
// runs hold an external Key has Lux's records of the Key's calls read
// through the key source, kept once each and attributed to the trial whose
// request label examen.trial names it, or that submitted its request id; a
// call no trial claims counts toward the experiment alone. A done trial
// that ended USAGE_SETTLE_DELAY before the end of what was read has its
// cost, sent and observed layers settled from its records.
const (
	// usageSkew is how long before an experiment's creation its first read
	// starts, for the clocks of Examen and Lux to differ.
	usageSkew = time.Minute
	// usageSpan is the longest window one read asks for, within Lux's own
	// bound of 90 days.
	usageSpan = 89 * 24 * time.Hour
)

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// Release releases the external Keys of the experiments that ended, or of
// the one named: an experiment's Key stops serving with it. A key source
// that does not answer leaves the Key for the next tick; the key source's
// own sweep ends it too.
func (e *Engine) Release(ctx context.Context, experiment string) error {
	if e.Keys == nil {
		return nil
	}
	held, err := e.Store.Usage().Unreleased(ctx, experiment, batch)
	if err != nil {
		return err
	}
	var errs []error
	for _, h := range held {
		if err := e.Keys.Release(ctx, h.KeyID); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := e.Store.Usage().Released(ctx, h.Experiment); err != nil {
			errs = append(errs, err)
			continue
		}
		e.log().InfoContext(ctx, "an ended experiment's key was released", "experiment", h.Experiment, "key_id", h.KeyID)
	}
	return errors.Join(errs...)
}

// Usage reads the records of every experiment due a read, and settles the
// cost of its trials that are due.
func (e *Engine) Usage(ctx context.Context) error {
	if e.Keys == nil {
		return nil
	}
	now := e.now().UTC()
	due, err := e.Store.Usage().Due(ctx, now.Add(-limits.UsageEvery), batch)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range due {
		if err := e.readUsage(ctx, d, now); err != nil {
			e.log().WarnContext(ctx, "an experiment's usage was not read; the next tick reads it", "experiment", d.Experiment, "error", apierr.LogText(err))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// readUsage reads one experiment's window up to now, keeps its records,
// and settles its trials that ended USAGE_SETTLE_DELAY before now.
func (e *Engine) readUsage(ctx context.Context, d store.UsageRead, now time.Time) error {
	from := d.CreatedAt.Add(-usageSkew)
	if d.ReadTo != nil && d.ReadTo.Add(-limits.UsageLookback).After(from) {
		from = d.ReadTo.Add(-limits.UsageLookback)
	}
	if earliest := now.Add(-usageSpan); earliest.After(from) {
		from = earliest
	}
	var records []store.UsageRecord
	cursor := ""
	for {
		page, err := e.Keys.Usage(ctx, keys.UsageRequest{Owner: d.Owner, Experiment: d.Experiment, From: from, To: now, Cursor: cursor})
		if err != nil {
			return err
		}
		for _, r := range page.Records {
			kept, err := keptRecord(r)
			if err != nil {
				return err
			}
			records = append(records, kept)
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	// Reads end once one covers the experiment's end with the lookback
	// past it, so a call still running when it ended is kept too.
	done := d.EndedAt != nil && !now.Before(d.EndedAt.Add(limits.UsageLookback))
	if err := e.Store.Usage().Read(ctx, d.Experiment, records, now, done); err != nil {
		return err
	}
	n, err := e.Store.Usage().Settle(ctx, d.Experiment, now.Add(-limits.UsageSettleDelay))
	if n > 0 {
		e.log().InfoContext(ctx, "trials' cost settled from their records", "experiment", d.Experiment, "trials", n)
	}
	return err
}

// keptRecord is a record as the store keeps it.
func keptRecord(r keys.Record) (store.UsageRecord, error) {
	tokens, err := json.Marshal(r.Tokens)
	if err != nil {
		return store.UsageRecord{}, err
	}
	return store.UsageRecord{
		RequestID: r.RequestID, At: r.At, Label: r.Labels[keys.LabelTrial], SentModel: r.SentModel, Served: r.Served(),
		Provider: r.Provider, Status: r.Status, Error: r.Error, Tokens: tokens, CostMicro: r.Cost, Currency: r.Currency,
		Priced: r.Priced, Record: r.Raw,
	}, nil
}
