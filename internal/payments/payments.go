// Package payments declares the provider-neutral payment Gateway port
// (ADR-0007, ADR-0033). Adapters live in one package per provider and
// run in-process in the monolith; there is no payment microservice.
// payments never imports ordering.
package payments

import (
	"context"
	"errors"
	"net/http"

	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/taxdocs"
)

// ProviderCode names an adapter, e.g. "fake".
type ProviderCode string

// Method is a provider-neutral payment method code from config, e.g. "card".
type Method string

// State is the normalized provider payment status.
type State string

// Normalized states (same names as ordering.PaymentStatus).
const (
	StatePending   State = "pending"
	StateSucceeded State = "succeeded"
	StateCanceled  State = "canceled"
	StateFailed    State = "failed"
	StateRefunded  State = "refunded"
	StateDisputed  State = "disputed"
)

// Status is what the provider reports for a payment (source of truth).
type Status struct {
	ProviderPaymentID string
	State             State
	Amount            money.Money
}

// CreatePaymentRequest is sent once per payment key; the provider returns
// the same payment for the same IdempotencyKey.
type CreatePaymentRequest struct {
	IdempotencyKey string
	Amount         money.Money
	Method         Method
	Description    string
	ReturnURL      string // absolute URL the provider sends the buyer back to
	OrderID        string // provider metadata only; never trusted on the way back
	// Document is the sale tax document (region pack, taxdocs.Build) for
	// providers that register it with the payment (RU: receipts sent with
	// the payment); nil when the region needs none.
	Document *taxdocs.Document
}

// CreatePaymentResult carries the provider id and the buyer redirect.
type CreatePaymentResult struct {
	ProviderPaymentID string
	ConfirmationURL   string
	Status            Status
}

// RefundRequest is a full refund in phase 1.
type RefundRequest struct {
	IdempotencyKey    string
	ProviderPaymentID string
	Amount            money.Money
	// Document is the return tax document for providers that register it
	// with the refund; nil when the region needs none or the provider
	// issues it automatically.
	Document *taxdocs.Document
}

// RefundResult of a refund call: State is succeeded, pending (provider
// confirms later; the same key is retried) or failed.
type RefundResult struct {
	ProviderRefundID string
	State            State
}

// Event is a verified webhook notification; its body is never trusted for
// the status (FetchPayment confirms).
type Event struct {
	EventID           string
	Type              string
	ProviderPaymentID string
	Payload           []byte
}

// Gateway is the port every provider adapter implements.
type Gateway interface {
	Code() ProviderCode
	Methods() []Method
	CreatePayment(ctx context.Context, req CreatePaymentRequest) (CreatePaymentResult, error)
	FetchPayment(ctx context.Context, providerPaymentID string) (Status, error)
	CancelPayment(ctx context.Context, providerPaymentID, idempotencyKey string) (Status, error)
	RefundPayment(ctx context.Context, req RefundRequest) (RefundResult, error)
	ParseWebhook(ctx context.Context, r *http.Request) (Event, error)
}

// Receipts is the tax-document issuer used with a provider (RU: 54-FZ).
// The port lives in taxdocs so that package stays independent of
// payments. YooKassa receipts (yookassa.Receipts) are the first
// implementation.
type Receipts = taxdocs.Issuer

// Errors shared by adapters.
var (
	ErrNotFound         = errors.New("payments: payment not found at the provider")
	ErrInvalidSignature = errors.New("payments: webhook signature is invalid")
	// ErrPermanent wraps provider answers that a retry cannot fix (bad
	// credentials, invalid request); the outbox marks the effect dead at
	// once (ordering.IsPermanent), refunds and tax documents fail with an
	// alert.
	ErrPermanent = errors.New("payments: provider rejected the request")
)
