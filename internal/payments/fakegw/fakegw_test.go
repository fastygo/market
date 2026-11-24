package fakegw

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/platform/tx"
)

func TestFakeGateway(t *testing.T) {
	g := New([]byte("k"))
	ctx := context.Background()
	a, err := g.CreatePayment(ctx, payments.CreatePaymentRequest{IdempotencyKey: "pay:1", Amount: money.Money{Minor: 100, Currency: "RUB"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := g.CreatePayment(ctx, payments.CreatePaymentRequest{IdempotencyKey: "pay:1"})
	if a.ProviderPaymentID != b.ProviderPaymentID || g.Payments() != 1 {
		t.Fatal("create must be idempotent by key")
	}
	if _, err := g.FetchPayment(tx.Mark(ctx), a.ProviderPaymentID); !errors.Is(err, tx.ErrNetworkInTx) {
		t.Fatal("calls inside a write tx must fail")
	}
	g.SetState(a.ProviderPaymentID, payments.StateSucceeded)
	st, _ := g.FetchPayment(ctx, a.ProviderPaymentID)
	if st.State != payments.StateSucceeded {
		t.Fatal(st)
	}
	body, sig := g.Webhook("e1", a.ProviderPaymentID, payments.StateSucceeded)
	r := httptest.NewRequest("POST", "/hooks/pay/fake", bytes.NewReader(body))
	r.Header.Set(SignatureHeader, sig)
	ev, err := g.ParseWebhook(ctx, r)
	if err != nil || ev.EventID != "e1" || ev.ProviderPaymentID != a.ProviderPaymentID {
		t.Fatal(ev, err)
	}
	r = httptest.NewRequest("POST", "/hooks/pay/fake", bytes.NewReader(body))
	r.Header.Set(SignatureHeader, "00")
	if _, err := g.ParseWebhook(ctx, r); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Fatal("bad signature must fail")
	}
}

func TestFakeRefundIdempotent(t *testing.T) {
	g := New([]byte("k"))
	ctx := context.Background()
	a, _ := g.CreatePayment(ctx, payments.CreatePaymentRequest{IdempotencyKey: "pay:2", Amount: money.Money{Minor: 100, Currency: "RUB"}})
	req := payments.RefundRequest{IdempotencyKey: "refund:p:1", ProviderPaymentID: a.ProviderPaymentID}
	if r, _ := g.RefundPayment(ctx, req); r.State != payments.StateFailed {
		t.Fatal("a pending payment cannot be refunded")
	}
	g.SetState(a.ProviderPaymentID, payments.StateSucceeded)
	r1, err := g.RefundPayment(ctx, req)
	r2, _ := g.RefundPayment(ctx, req)
	if err != nil || r1.State != payments.StateSucceeded || r1 != r2 {
		t.Fatal("refund must be idempotent by key", r1, r2, err)
	}
	if st, _ := g.FetchPayment(ctx, a.ProviderPaymentID); st.State != payments.StateRefunded {
		t.Fatal(st)
	}
}
