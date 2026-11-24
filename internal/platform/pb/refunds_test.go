package pb_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/region/ru"
	"github.com/fastygo/market/internal/taxdocs/fakeissuer"
)

// docsRig is the checkout rig with RU receipts and the fake issuer.
func docsRig(t *testing.T) (*rig, *fakeissuer.Issuer) {
	t.Helper()
	r := newRig(t)
	iss := fakeissuer.New()
	r.svc.Store = &pb.Orders{App: r.app, TaxDocs: ru.Receipts{}, IssuerCode: iss.Code()}
	r.svc.Issuer = iss
	r.svc.TaxDocMaxAttempts = 3
	return r, iss
}

// paid runs checkout and the provider success; returns order and provider ids.
func (r *rig) paid(t *testing.T, key string) (string, string) {
	t.Helper()
	v := r.checkout(t, key)
	ppid := r.ppid(t, v.OrderID)
	r.gw.SetState(ppid, payments.StateSucceeded)
	if _, err := r.svc.Apply(context.Background(), "fake", ppid); err != nil {
		t.Fatal(err)
	}
	if o, _ := r.status(t, v.OrderID); o != "paid" {
		t.Fatalf("order %s", o)
	}
	return v.OrderID, ppid
}

func (r *rig) ppid(t *testing.T, orderID string) string {
	t.Helper()
	var id string
	if err := r.app.DB().NewQuery(`SELECT provider_payment_id FROM payments WHERE "order" = '` + orderID + `'`).Row(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (r *rig) str(t *testing.T, q string) string {
	t.Helper()
	s := ""
	if err := r.app.DB().NewQuery(q).Row(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// drain runs the outbox until it is empty, moving the clock past backoff.
func (r *rig) drain(t *testing.T) {
	t.Helper()
	for i := 0; i < 20; i++ {
		did, _ := r.svc.ProcessOutboxOnce(context.Background())
		if !did && r.n(t, "SELECT COUNT(*) FROM outbox WHERE status IN ('pending', 'processing')") == 0 {
			return
		}
		r.clock.Add(time.Hour)
	}
	t.Fatal("outbox did not drain")
}

func (r *rig) balanced(t *testing.T) {
	t.Helper()
	if s := r.n(t, "SELECT COALESCE(SUM(amount_minor), 0) FROM ledger_entries"); s != 0 {
		t.Fatalf("ledger not balanced: %d", s)
	}
	if n := r.n(t, "SELECT COUNT(*) FROM (SELECT txn_id FROM ledger_entries GROUP BY txn_id HAVING SUM(amount_minor) != 0)"); n != 0 {
		t.Fatalf("%d unbalanced txns", n)
	}
}

// ADR-0013 #6 (full): refund command -> provider refund, entitlement
// refunded, ledger reversal, return receipt; repeated command is a no-op.
func TestRefundCommand(t *testing.T) {
	r, iss := docsRig(t)
	ctx := context.Background()
	orderID, _ := r.paid(t, "k")
	r.drain(t)
	if r.str(t, "SELECT status FROM tax_documents WHERE kind = 'receipt'") != "issued" {
		t.Fatal("sale receipt not issued")
	}
	dl := &pb.Downloads{App: r.app}
	tgt, err := dl.DownloadTarget(ctx, r.buyer, "aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.RefundOrder(ctx, orderID, "bogus", "", "op"); !errors.Is(err, ordering.ErrReason) {
		t.Fatalf("want ErrReason, got %v", err)
	}
	ref, err := r.svc.RefundOrder(ctx, orderID, ordering.ReasonBuyerRequest, "buyer asked", "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Status != "succeeded" {
		t.Fatalf("refund %+v", ref)
	}
	if o, p := r.status(t, orderID); o != "refunded" || p != "refunded" {
		t.Fatalf("status %s %s", o, p)
	}
	if r.str(t, "SELECT status FROM entitlements") != ordering.EntitlementRefunded {
		t.Fatal("entitlement not refunded")
	}
	// a download token issued before the refund is rejected on re-check
	if _, err := dl.Recheck(ctx, tgt.EntitlementID, tgt.ReleaseID); !errors.Is(err, licensing.ErrNoEntitlement) {
		t.Fatalf("recheck after refund: %v", err)
	}
	if r.n(t, "SELECT COUNT(*) FROM ledger_entries WHERE ref_type = 'refund'") != 3 {
		t.Fatal("no ledger reversal")
	}
	r.balanced(t)
	if r.str(t, "SELECT provider || '/' || reason_code || '/' || note FROM refunds") != "fake/buyer_request/buyer asked" {
		t.Fatal("refund row fields")
	}
	if r.gw.Calls("refund") != 1 {
		t.Fatal("one provider refund call")
	}
	// the outbox copy of the command and a second command are no-ops
	r.drain(t)
	if _, err := r.svc.RefundOrder(ctx, orderID, ordering.ReasonOperator, "", "op"); !errors.Is(err, ordering.ErrNotRefundable) {
		t.Fatalf("second refund: %v", err)
	}
	if r.gw.Calls("refund") != 1 || r.n(t, "SELECT COUNT(*) FROM refunds") != 1 {
		t.Fatal("refund repeated")
	}
	if r.str(t, "SELECT status || '/' || operation FROM tax_documents WHERE kind = 'receipt_return'") != "issued/income_return" {
		t.Fatal("return receipt not issued")
	}
	if iss.Calls() != 2 {
		t.Fatalf("issuer calls %d", iss.Calls())
	}
	// a late provider event changes nothing
	if _, err := r.svc.Apply(ctx, "fake", r.ppid(t, orderID)); err != nil {
		t.Fatal(err)
	}
	if r.n(t, "SELECT COUNT(*) FROM ledger_entries") != 6 {
		t.Fatal("ledger changed on replayed event")
	}
}

// A refund whose provider call fails stays pending and is retried by the
// outbox with the same key.
func TestRefundRetriedByOutbox(t *testing.T) {
	r, _ := docsRig(t)
	ctx := context.Background()
	orderID, _ := r.paid(t, "k")
	failOnce(r, "refund")
	if _, err := r.svc.RefundOrder(ctx, orderID, ordering.ReasonOperator, "", "op"); err == nil {
		t.Fatal("want provider error")
	}
	if r.str(t, "SELECT status FROM refunds") != "pending" {
		t.Fatal("refund must stay pending")
	}
	r.drain(t)
	if o, _ := r.status(t, orderID); o != "refunded" {
		t.Fatalf("order %s", o)
	}
	if r.n(t, "SELECT COUNT(*) FROM ledger_entries WHERE ref_type = 'refund'") != 3 {
		t.Fatal("reversal written once")
	}
}

// ADR-0013 #8: chargeback -> downloads suspended; won -> restored; lost ->
// revoked with a dispute ledger txn and a return receipt.
func TestDispute(t *testing.T) {
	for _, outcome := range []string{"won", "lost"} {
		t.Run(outcome, func(t *testing.T) {
			r, _ := docsRig(t)
			ctx := context.Background()
			orderID, ppid := r.paid(t, "k")
			r.gw.SetState(ppid, payments.StateDisputed)
			if _, err := r.svc.Apply(ctx, "fake", ppid); err != nil {
				t.Fatal(err)
			}
			if o, p := r.status(t, orderID); o != "disputed" || p != "disputed" {
				t.Fatalf("status %s %s", o, p)
			}
			if r.str(t, "SELECT status FROM entitlements") != ordering.EntitlementDisputed {
				t.Fatal("entitlement not suspended")
			}
			dl := &pb.Downloads{App: r.app}
			if _, err := dl.DownloadTarget(ctx, r.buyer, "aurora-theme"); !errors.Is(err, licensing.ErrNoEntitlement) {
				t.Fatalf("downloads must stop during a dispute: %v", err)
			}
			if r.n(t, "SELECT COUNT(*) FROM alerts WHERE kind = 'dispute_opened'") != 1 {
				t.Fatal("no dispute alert")
			}
			want := ordering.EntitlementActive
			if outcome == "won" {
				r.gw.SetState(ppid, payments.StateSucceeded)
			} else {
				r.gw.SetState(ppid, payments.StateRefunded)
				want = ordering.EntitlementRevoked
			}
			if _, err := r.svc.Apply(ctx, "fake", ppid); err != nil {
				t.Fatal(err)
			}
			if r.str(t, "SELECT status FROM entitlements") != want {
				t.Fatalf("entitlement %s", r.str(t, "SELECT status FROM entitlements"))
			}
			r.balanced(t)
			if _, err := dl.DownloadTarget(ctx, r.buyer, "aurora-theme"); (err == nil) != (outcome == "won") {
				t.Fatalf("downloads after %s: %v", outcome, err)
			}
			if outcome == "won" {
				if o, _ := r.status(t, orderID); o != "paid" || r.n(t, "SELECT COUNT(*) FROM ledger_entries") != 3 {
					t.Fatal("won: order paid, sale ledger only")
				}
				return
			}
			if o, _ := r.status(t, orderID); o != "refunded" {
				t.Fatalf("lost: order %s", o)
			}
			if r.n(t, "SELECT COUNT(*) FROM ledger_entries WHERE txn_id = 'pay:"+r.str(t, "SELECT id FROM payments")+":dispute:1'") != 3 {
				t.Fatal("no dispute ledger txn")
			}
			if r.str(t, "SELECT reason_code || '/' || status FROM refunds") != "dispute_lost/succeeded" {
				t.Fatal("dispute refund row")
			}
			if r.n(t, "SELECT COUNT(*) FROM tax_documents WHERE kind = 'receipt_return'") != 1 {
				t.Fatal("no return receipt")
			}
		})
	}
}

// Paid but not completable (product archived meanwhile) -> refund_pending
// and an automatic full refund; no delivery.
func TestAutoRefundNotCompletable(t *testing.T) {
	r, _ := docsRig(t)
	ctx := context.Background()
	v := r.checkout(t, "k")
	if _, err := r.app.DB().NewQuery("UPDATE products SET status = 'archived' WHERE slug = 'aurora-theme'").Execute(); err != nil {
		t.Fatal(err)
	}
	ppid := r.ppid(t, v.OrderID)
	r.gw.SetState(ppid, payments.StateSucceeded)
	if _, err := r.svc.Apply(ctx, "fake", ppid); err != nil {
		t.Fatal(err)
	}
	if o, _ := r.status(t, v.OrderID); o != "refund_pending" {
		t.Fatalf("order %s", o)
	}
	if r.n(t, "SELECT COUNT(*) FROM entitlements") != 0 || r.n(t, "SELECT COUNT(*) FROM alerts WHERE kind = 'not_completable'") != 1 {
		t.Fatal("no delivery and an alert expected")
	}
	r.drain(t)
	if o, p := r.status(t, v.OrderID); o != "refunded" || p != "refunded" {
		t.Fatalf("status %s %s", o, p)
	}
	if r.str(t, "SELECT reason_code FROM refunds") != ordering.ReasonNotCompletable {
		t.Fatal("reason")
	}
	r.balanced(t)
	if r.n(t, "SELECT COUNT(*) FROM tax_documents WHERE status = 'issued'") != 2 {
		t.Fatal("sale and return receipts expected")
	}
}

// Timeout cron: cancel requested at the provider, order cancelled only on
// its confirmation; a payment that succeeded meanwhile becomes paid.
func TestTimeoutCancel(t *testing.T) {
	r, _ := docsRig(t)
	ctx := context.Background()
	cancelled := r.checkout(t, "a")
	late := r.checkout(t, "b")
	if n, err := r.svc.RequestTimeouts(ctx); err != nil || n != 0 {
		t.Fatalf("too early: %d %v", n, err)
	}
	r.clock.Add(31 * time.Minute)
	failOnce(r, "cancel")
	if n, err := r.svc.RequestTimeouts(ctx); err != nil || n != 2 {
		t.Fatalf("timeouts %d %v", n, err)
	}
	r.gw.SetState(r.ppid(t, late.OrderID), payments.StateSucceeded)
	if did, _ := r.svc.ProcessOutboxOnce(ctx); !did {
		t.Fatal("outbox idle")
	}
	if r.n(t, "SELECT COUNT(*) FROM orders WHERE status = 'cancelled'") != 0 {
		t.Fatal("cancelled without provider confirmation")
	}
	r.drain(t)
	if o, p := r.status(t, cancelled.OrderID); o != "cancelled" || p != "canceled" {
		t.Fatalf("cancelled: %s %s", o, p)
	}
	if r.str(t, "SELECT cancel_reason FROM orders WHERE id = '"+cancelled.OrderID+"'") != "timeout_requested" {
		t.Fatal("cancel reason")
	}
	if o, _ := r.status(t, late.OrderID); o != "paid" {
		t.Fatalf("late: %s", o)
	}
	if n, _ := r.svc.RequestTimeouts(ctx); n != 0 {
		t.Fatal("requested twice")
	}
}

// ADR-0013 #5: the register fails -> the document stays pending and the
// outbox retries; at the attempt cap it is failed with an alert. Money
// and delivery do not wait for documents.
func TestTaxDocRetryAndAlert(t *testing.T) {
	t.Run("recovers", func(t *testing.T) {
		r, iss := docsRig(t)
		iss.FailTimes = 2
		r.paid(t, "k")
		if r.n(t, "SELECT COUNT(*) FROM entitlements WHERE status = 'active'") != 1 {
			t.Fatal("delivery must not wait for the receipt")
		}
		r.drain(t)
		if r.str(t, "SELECT status || '/' || attempts FROM tax_documents") != "issued/3" {
			t.Fatalf("doc %s", r.str(t, "SELECT status || '/' || attempts FROM tax_documents"))
		}
		if r.str(t, "SELECT number FROM tax_documents") == "" {
			t.Fatal("number not stored")
		}
	})
	t.Run("fails", func(t *testing.T) {
		r, iss := docsRig(t)
		iss.AlwaysFail = true
		r.paid(t, "k")
		r.drain(t)
		if r.str(t, "SELECT status FROM tax_documents") != "failed" || r.n(t, "SELECT COUNT(*) FROM alerts WHERE kind = 'taxdoc_failed'") != 1 {
			t.Fatal("failed document must raise an alert")
		}
		if iss.Calls() != 3 {
			t.Fatalf("calls %d", iss.Calls())
		}
	})
	t.Run("async", func(t *testing.T) {
		r, iss := docsRig(t)
		iss.Async = true
		r.paid(t, "k")
		r.drain(t)
		if r.str(t, "SELECT status FROM tax_documents") != "pending" {
			t.Fatal("async register: pending")
		}
		r.clock.Add(11 * time.Minute)
		if _, err := r.svc.Reconcile(context.Background()); err != nil {
			t.Fatal(err)
		}
		if r.str(t, "SELECT status FROM tax_documents") != "issued" {
			t.Fatal("reconcile must fetch the document")
		}
	})
}

// failOnce makes the next provider call of op fail.
func failOnce(r *rig, op string) {
	left := 1
	r.gw.BeforeCall = func(_ context.Context, o string) error {
		if o == op && left > 0 {
			left--
			return errors.New("provider unavailable")
		}
		return nil
	}
}
