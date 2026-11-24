// Package taxdocs is the region-neutral tax document core (ADR-0004,
// ADR-0015): the document DTO, its pure builder, the status values and the
// DocumentIssuer port that region packs implement (RU: 54-FZ receipts
// through a cloud register). It imports neither ordering nor payments.
package taxdocs

import (
	"context"
	"errors"
)

// Kind of a document.
type Kind string

// Document kinds.
const (
	Receipt       Kind = "receipt"        // RU 54-FZ: operation "income"
	ReceiptReturn Kind = "receipt_return" // RU 54-FZ: operation "income_return"
	Invoice       Kind = "invoice"
	CreditNote    Kind = "credit_note"
)

// Status of a document row.
type Status string

// Statuses.
const (
	StatusPending     Status = "pending"
	StatusIssued      Status = "issued"
	StatusFailed      Status = "failed"
	StatusNotRequired Status = "not_required"
)

// Line of a document; values are the order line snapshot, so the document
// total equals the payment amount.
type Line struct {
	Title    string `json:"title"`
	Qty      int    `json:"qty"`
	Price    int64  `json:"price_minor"`
	Amount   int64  `json:"amount_minor"`
	TaxMode  string `json:"tax_mode"`
	TaxCode  string `json:"tax_code"`
	TaxMinor int64  `json:"tax_minor"`
	Subject  string `json:"subject"` // region payment subject code
	Method   string `json:"method"`  // region payment method code
	Agent    bool   `json:"agent"`
}

// Document is what the issuer receives.
type Document struct {
	ID             string `json:"id"`
	Kind           Kind   `json:"kind"`
	Operation      string `json:"operation"` // region code, e.g. "income"
	Currency       string `json:"currency"`
	Total          int64  `json:"total_minor"`
	BuyerEmail     string `json:"buyer_email"`
	Lines          []Line `json:"lines"`
	IdempotencyKey string `json:"idempotency_key"`
}

// Result of Issue or Fetch.
type Result struct {
	ExternalID string
	Number     string
	Status     Status // issued, pending (async register) or failed
	Attrs      map[string]any
}

// Issuer is the DocumentIssuer port; adapters call the network, so they
// are only used outside write transactions (outbox worker, reconciliation).
type Issuer interface {
	Code() string
	Issue(ctx context.Context, d Document) (Result, error)
	Fetch(ctx context.Context, externalID string) (Result, error)
}

// Policy is the region rule: which document an event needs and how its
// lines are coded. Pure; used inside the apply transaction.
type Policy interface {
	// ForSale and ForRefund return the kind and operation; ok=false means
	// the region requires no document.
	ForSale() (kind Kind, operation string, ok bool)
	ForRefund() (kind Kind, operation string, ok bool)
	// LineCodes returns the payment subject and method codes of a line.
	LineCodes(agent bool) (subject, method string)
}

// SourceLine is the order line snapshot used to build documents.
type SourceLine struct {
	Title    string
	Qty      int
	Price    int64
	Amount   int64
	TaxMode  string
	TaxCode  string
	TaxMinor int64
	Agent    bool
}

// ErrMismatch is returned when the lines do not add up to the total.
var ErrMismatch = errors.New("taxdocs: line amounts do not add up to the total")

// Build is the pure builder: one document line per order line; the sum of
// line amounts must equal the payment total.
func Build(p Policy, kind Kind, operation, currency, buyerEmail string, total int64, src []SourceLine) (Document, error) {
	d := Document{Kind: kind, Operation: operation, Currency: currency, Total: total, BuyerEmail: buyerEmail}
	var sum int64
	for _, s := range src {
		subject, method := p.LineCodes(s.Agent)
		amount := s.Amount
		if s.TaxMode == "exclusive" {
			amount += s.TaxMinor
		}
		d.Lines = append(d.Lines, Line{Title: s.Title, Qty: s.Qty, Price: s.Price, Amount: amount, TaxMode: s.TaxMode,
			TaxCode: s.TaxCode, TaxMinor: s.TaxMinor, Subject: subject, Method: method, Agent: s.Agent})
		sum += amount
	}
	if sum != total {
		return d, ErrMismatch
	}
	return d, nil
}
