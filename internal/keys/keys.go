// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: Apache-2.0

// Package keys is the key source of specs/004-identity-and-authorization.md:
// where the Lux Keys an experiment's model calls go through come from, what
// is left to spend through them, and the records of the calls made with
// them. The `endpoint` source asks the installation's key endpoint, which
// keeps its own budgets and Keys; with no source configured, no Key is
// leased and no record is read.
//
// A Key's value is held in memory for the request that hands it on and
// never written to Postgres, an event, a log line or an error.
package keys

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// The purposes a Key is leased for. PurposeExternal is the one Key of an
// experiment that its external harness's model calls go through, which a
// lease of an external trial hands on (spec 005); the source answers the
// same Key for every ask of an experiment while it runs.
const (
	PurposeTrial    = "trial"
	PurposeJudge    = "judge"
	PurposeReport   = "report"
	PurposeExternal = "external"
)

// LabelTrial is the request label that marks a model call as one trial's:
// Lux-Labels: examen.trial=<trial id>.
const LabelTrial = "examen.trial"

// LabelExperiment is the label a Key carries naming its experiment.
const LabelExperiment = "examen.experiment"

// Source leases the Lux Keys an experiment's calls go through, reads its
// Budget's figures, and reads the records of the calls made with its Key.
type Source interface {
	// Lease answers one Key for work of one experiment.
	Lease(ctx context.Context, r Request) (Lease, error)
	// Release ends a lease: the Key stops serving. Releasing a Key
	// already released is no error.
	Release(ctx context.Context, keyID string) error
	// Budget answers the figures of an experiment's Budget.
	Budget(ctx context.Context, r Request) (Budget, error)
	// Usage answers one page of the records of the model calls made with
	// an experiment's external Key.
	Usage(ctx context.Context, r UsageRequest) (UsagePage, error)
}

// Request names whose work a Key is for and what it may call.
type Request struct {
	Owner      string            // the experiment's owner
	Subject    string            // on whose behalf: the caller, or the experiment's created_by
	Experiment string            // the exp_ id
	Purpose    string            // PurposeTrial, PurposeJudge, PurposeReport or PurposeExternal
	Models     []string          // the only models the Key may call; empty for PurposeExternal, whose Key names none
	Surface    string            // the one Lux surface the Key may call; empty for PurposeExternal
	Labels     map[string]string // the Key's labels
	Budget     string            // the experiment's Budget, for Budget
	TTL        time.Duration     // the longest the work can last; zero for PurposeExternal, whose Key ends with the experiment
}

// Lease is one Key and what is left to spend through it.
type Lease struct {
	Value     string    // the Key's value, held in memory only
	ID        string    // the Key's id at the key source
	Expires   time.Time // when the Key stops serving; zero for a Key that ends with its experiment
	Remaining int64     // micro-units: the smallest remainder across every Budget the Key draws on
	Currency  string
}

// String keeps a Key's value out of every log line and error that formats
// a Lease.
func (l Lease) String() string { return "keys.Lease{" + l.ID + "}" }

// GoString is String, for %#v.
func (l Lease) GoString() string { return l.String() }

// Budget is what an experiment's answer shows of its Budget.
type Budget struct {
	Name      string
	Cap       int64 // micro-units: the Budget's amount
	Spent     int64 // micro-units: spent in its current window
	Remaining int64 // micro-units: the smallest remainder across every Budget the experiment's Keys draw on
	Currency  string
}

// UsageRequest asks for the records of the calls made with an
// experiment's external Key between From and To, a page at a time.
type UsageRequest struct {
	Owner      string
	Experiment string
	From, To   time.Time
	Cursor     string
}

// UsagePage is one page of records, and the cursor of the next; an empty
// NextCursor is the last page.
type UsagePage struct {
	Records    []Record
	NextCursor string
}

// Record is one model call as Lux recorded it (Lux's request record): the
// request the caller sent and what served it. Raw is the record as the
// source answered it, kept so a figure traces to what it was read from.
type Record struct {
	RequestID string
	At        time.Time
	// Labels are the request's own labels, Lux-Labels as the caller sent
	// them.
	Labels map[string]string
	// SentModel is the model the request named, as the Lux Model it
	// resolved to by its exact name; "" when it named none Lux holds.
	// Lux records no reasoning level a request carried.
	SentModel string
	// Provider and Upstream are what served it: the provider and the
	// provider's own model name.
	Provider string
	Upstream string
	// Status is ok, refused or failed, and Error its code.
	Status string
	Error  string
	Tokens Tokens
	// Cost is in micro-units of Currency; Priced is false when Lux could
	// not price the call, whose cost is then unknown.
	Cost     int64
	Currency string
	Priced   bool
	Raw      json.RawMessage
}

// The statuses of a record.
const (
	StatusOK      = "ok"
	StatusRefused = "refused"
	StatusFailed  = "failed"
)

// Served is the model that served the call: the provider's own name for
// it, else the Lux Model's.
func (r Record) Served() string {
	if r.Upstream != "" {
		return r.Upstream
	}
	return r.SentModel
}

// Tokens are a call's token counts. Input excludes CachedInput, and
// Output includes Reasoning, as Lux counts them.
type Tokens struct {
	Input       int64 `json:"input"`
	Output      int64 `json:"output"`
	CachedInput int64 `json:"cachedInput"`
	CacheWrite  int64 `json:"cacheWrite"`
	Reasoning   int64 `json:"reasoning"`
	Estimated   bool  `json:"estimated"`
}

// wireRecord is what ParseRecord reads of Lux's record.
type wireRecord struct {
	ID    string    `json:"id"`
	At    time.Time `json:"at"`
	Model struct {
		Name string `json:"name"`
	} `json:"model"`
	Provider struct {
		Name string `json:"name"`
	} `json:"provider"`
	UpstreamModel string `json:"upstreamModel"`
	Status        string `json:"status"`
	Error         string `json:"error"`
	Tokens        Tokens `json:"tokens"`
	Cost          struct {
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Priced   bool   `json:"priced"`
	} `json:"cost"`
	RequestLabels map[string]string `json:"requestLabels"`
}

// ParseRecord reads one of Lux's request records. A record with no id, no
// time or a status Lux does not write is an error.
func ParseRecord(raw json.RawMessage) (Record, error) {
	var w wireRecord
	if err := json.Unmarshal(raw, &w); err != nil {
		return Record{}, err
	}
	switch {
	case w.ID == "":
		return Record{}, errors.New("a record has no id")
	case w.At.IsZero():
		return Record{}, errors.New("a record has no time")
	case w.Status != StatusOK && w.Status != StatusRefused && w.Status != StatusFailed:
		return Record{}, errors.New("a record's status is " + w.Status)
	}
	return Record{
		RequestID: w.ID, At: w.At.UTC(), Labels: w.RequestLabels, SentModel: w.Model.Name,
		Provider: w.Provider.Name, Upstream: w.UpstreamModel, Status: w.Status, Error: w.Error, Tokens: w.Tokens,
		Cost: w.Cost.Amount, Currency: w.Cost.Currency, Priced: w.Cost.Priced, Raw: raw,
	}, nil
}

// The errors of a key source.
var (
	// ErrBudgetExhausted is the source's 402: no Budget the Key would
	// draw on has money left.
	ErrBudgetExhausted = errors.New("keys: no budget the key would draw on has money left")
	// ErrUnavailable is no usable answer: an outage, a status the
	// contract does not name, a body that does not parse, or no answer
	// within the timeout after one retry on a connection failure.
	ErrUnavailable = errors.New("keys: the key source did not answer")
	// ErrNoKey is a Usage or a Release for an experiment the source holds
	// no Key of.
	ErrNoKey = errors.New("keys: the key source holds no key of the experiment")
)

// RefusedError is the source's 403: it will not answer for this request,
// for Reason.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "keys: the key source refused: " + e.Reason }

// ReasonKeyRefused is the reason of a 403 that names none.
const ReasonKeyRefused = "key_refused"
