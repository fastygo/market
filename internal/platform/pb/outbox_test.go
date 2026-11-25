package pb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/taxdocs"
)

// emailCalls replaces the purchase mail handler with one returning err.
func (r *rig) emailFails(err error) *int {
	calls := 0
	r.svc.Outbox["email.purchase"] = func(context.Context, string, json.RawMessage) error { calls++; return err }
	return &calls
}

// A provider ErrPermanent is not retried: the outbox row is dead after
// one attempt and the worker reports it.
func TestOutboxPermanentMailNotRetried(t *testing.T) {
	r := newRig(t)
	calls := r.emailFails(fmt.Errorf("%w: 550 mailbox unavailable", mail.ErrPermanent))
	r.paid(t, "k")
	_, err := r.svc.ProcessOutboxOnce(context.Background())
	if err == nil || !strings.Contains(err.Error(), "permanently") || !ordering.IsPermanent(err) {
		t.Fatalf("worker must report the permanent failure: %v", err)
	}
	r.drain(t)
	if *calls != 1 {
		t.Fatalf("permanent error retried: %d calls", *calls)
	}
	if s := r.str(t, "SELECT status || '/' || attempts FROM outbox WHERE topic = 'email.purchase'"); s != "dead/1" {
		t.Fatalf("outbox row %s", s)
	}
	if !strings.Contains(r.str(t, "SELECT last_error FROM outbox WHERE topic = 'email.purchase'"), "550") {
		t.Fatal("last_error not stored")
	}
}

// Transient errors (SMTP down, auth failure, missing sender: fixable in
// the admin UI) keep the backoff and the attempt cap.
func TestOutboxTransientMailRetried(t *testing.T) {
	r := newRig(t)
	calls := r.emailFails(errors.New("pbmail: no sender address"))
	r.paid(t, "k")
	r.drain(t)
	if *calls != 3 {
		t.Fatalf("transient error must be retried up to MaxAttempts: %d calls", *calls)
	}
	if s := r.str(t, "SELECT status || '/' || attempts FROM outbox WHERE topic = 'email.purchase'"); s != "dead/3" {
		t.Fatalf("outbox row %s", s)
	}
	// a transient failure that recovers ends done
	r2 := newRig(t)
	n := 0
	r2.svc.Outbox["email.purchase"] = func(context.Context, string, json.RawMessage) error {
		n++
		if n == 1 {
			return errors.New("dial tcp: i/o timeout")
		}
		return nil
	}
	r2.paid(t, "k")
	r2.drain(t)
	if s := r2.str(t, "SELECT status || '/' || attempts FROM outbox WHERE topic = 'email.purchase'"); s != "done/2" {
		t.Fatalf("outbox row %s", s)
	}
}

// A refund the provider rejects with ErrPermanent is failed with an
// alert at once; neither the command nor the outbox calls again.
func TestRefundPermanentFailsWithAlert(t *testing.T) {
	r, _ := docsRig(t)
	ctx := context.Background()
	orderID, _ := r.paid(t, "k")
	calls := 0
	r.gw.BeforeCall = func(_ context.Context, op string) error {
		if op == "refund" {
			calls++
			return fmt.Errorf("%w: refund amount exceeds payment", payments.ErrPermanent)
		}
		return nil
	}
	if _, err := r.svc.RefundOrder(ctx, orderID, ordering.ReasonOperator, "", "op"); !errors.Is(err, payments.ErrPermanent) {
		t.Fatalf("want permanent provider error, got %v", err)
	}
	if r.str(t, "SELECT status FROM refunds") != "failed" || r.n(t, "SELECT COUNT(*) FROM alerts WHERE kind = 'refund_failed'") != 1 {
		t.Fatal("permanent refund error must fail the refund with an alert")
	}
	r.drain(t)
	if calls != 1 {
		t.Fatalf("permanent refund retried: %d calls", calls)
	}
	if o, _ := r.status(t, orderID); o != "paid" {
		t.Fatalf("order %s", o)
	}
}

type permIssuer struct {
	taxdocs.Issuer
	calls int
}

func (p *permIssuer) Issue(context.Context, taxdocs.Document) (taxdocs.Result, error) {
	p.calls++
	return taxdocs.Result{}, fmt.Errorf("%w: receipt needs the buyer email", payments.ErrPermanent)
}

// A tax document rejected with ErrPermanent is failed with an alert on
// the first attempt instead of after the cap.
func TestTaxDocPermanentFailsAtOnce(t *testing.T) {
	r, iss := docsRig(t)
	p := &permIssuer{Issuer: iss}
	r.svc.Issuer = p
	r.paid(t, "k")
	r.drain(t)
	if p.calls != 1 {
		t.Fatalf("permanent issuer error retried: %d calls", p.calls)
	}
	if r.str(t, "SELECT status || '/' || attempts FROM tax_documents") != "failed/1" || r.n(t, "SELECT COUNT(*) FROM alerts WHERE kind = 'taxdoc_failed'") != 1 {
		t.Fatal("failed document must raise an alert")
	}
}
