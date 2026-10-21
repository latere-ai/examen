// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

package keys

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"
)

// Memory is a key source held in memory, for a test: one Key per
// experiment, as an installation's key endpoint answers its external Key,
// and one per judge, by the grader its label examen.grader names,
// the records a test adds, and the Budget figures a test sets. Err, when
// set, is answered by every call.
type Memory struct {
	mu       sync.Mutex
	keys     map[string]Lease // by experiment, purpose and grader
	released map[string]bool  // by Key id
	records  map[string][]Record
	budgets  map[string]Budget
	asked    []Request
	Err      error
	// PageSize, when above zero, is the most records one page answers,
	// the rest behind its cursor.
	PageSize int
}

// NewMemory is an empty source.
func NewMemory() *Memory {
	return &Memory{keys: map[string]Lease{}, released: map[string]bool{}, records: map[string][]Record{}, budgets: map[string]Budget{}}
}

// Lease answers the experiment's one Key, made on the first ask.
func (m *Memory) Lease(_ context.Context, r Request) (Lease, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.asked = append(m.asked, r)
	if m.Err != nil {
		return Lease{}, m.Err
	}
	if r.Purpose != PurposeExternal && r.Purpose != PurposeJudge {
		return Lease{}, &RefusedError{Reason: "purpose_not_served"}
	}
	key := r.Experiment
	if r.Purpose == PurposeJudge {
		key += "\x00judge\x00" + r.Labels["examen.grader"]
	}
	l, ok := m.keys[key]
	if !ok || m.released[l.ID] {
		n := len(m.keys) + len(m.released) + 1
		l = Lease{Value: fmt.Sprintf("lux_value%04d", n), ID: fmt.Sprintf("key-%04d", n), Remaining: 1_000_000, Currency: "USD"}
		m.keys[key] = l
	}
	return l, nil
}

// Release ends a Key.
func (m *Memory) Release(_ context.Context, keyID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return m.Err
	}
	m.released[keyID] = true
	return nil
}

// Budget answers the figures a test set for the experiment.
func (m *Memory) Budget(_ context.Context, r Request) (Budget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return Budget{}, m.Err
	}
	b, ok := m.budgets[r.Experiment]
	if !ok {
		return Budget{}, ErrNoKey
	}
	return b, nil
}

// Usage answers the records a test added for the experiment that started
// in [From, To), in one page.
func (m *Memory) Usage(_ context.Context, r UsageRequest) (UsagePage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Err != nil {
		return UsagePage{}, m.Err
	}
	if _, ok := m.keys[r.Experiment]; !ok {
		return UsagePage{}, ErrNoKey
	}
	var in []Record
	for _, rec := range m.records[r.Experiment] {
		if !rec.At.Before(r.From) && rec.At.Before(r.To) {
			in = append(in, rec)
		}
	}
	start := 0
	if r.Cursor != "" {
		if _, err := fmt.Sscanf(r.Cursor, "at-%d", &start); err != nil || start > len(in) {
			return UsagePage{}, fmt.Errorf("%w: a cursor this source did not answer", ErrUnavailable)
		}
	}
	end := len(in)
	page := UsagePage{}
	if m.PageSize > 0 && start+m.PageSize < end {
		end = start + m.PageSize
		page.NextCursor = fmt.Sprintf("at-%d", end)
	}
	page.Records = append([]Record{}, in[start:end]...)
	return page, nil
}

// SetBudget sets the figures the experiment's Budget answers.
func (m *Memory) SetBudget(experiment string, b Budget) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.budgets[experiment] = b
}

// AddRecord adds a record of a call made with the experiment's Key.
func (m *Memory) AddRecord(experiment string, r Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.records[experiment] = append(m.records[experiment], r)
}

// Released reports whether a Key was released.
func (m *Memory) Released(keyID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.released[keyID]
}

// Asked answers every lease asked, in order.
func (m *Memory) Asked() []Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.asked)
}

// KeyOf answers the experiment's current Key.
func (m *Memory) KeyOf(experiment string) (Lease, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.keys[experiment]
	return l, ok
}

// Cache answers an experiment's external Key from memory for at most TTL
// after the source answered it, so a burst of leases asks the source once.
// The value is held in this process's memory alone.
type Cache struct {
	Source Source
	TTL    time.Duration
	Now    func() time.Time
	mu     sync.Mutex
	held   map[string]cachedLease
}

type cachedLease struct {
	lease Lease
	at    time.Time
}

// External answers the experiment's external Key.
func (c *Cache) External(ctx context.Context, r Request) (Lease, error) {
	now := time.Now()
	if c.Now != nil {
		now = c.Now()
	}
	c.mu.Lock()
	held, ok := c.held[r.Experiment]
	c.mu.Unlock()
	if ok && now.Sub(held.at) < c.TTL && (held.lease.Expires.IsZero() || held.lease.Expires.After(now)) {
		return held.lease, nil
	}
	l, err := c.Source.Lease(ctx, r)
	if err != nil {
		return Lease{}, err
	}
	c.mu.Lock()
	if c.held == nil {
		c.held = map[string]cachedLease{}
	}
	c.held[r.Experiment] = cachedLease{lease: l, at: now}
	c.mu.Unlock()
	return l, nil
}

// Forget drops the experiment's Key, once it is released.
func (c *Cache) Forget(experiment string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.held, experiment)
}
