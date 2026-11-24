package pb_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/yookassa"
	"github.com/fastygo/market/internal/payments/yookassa/yktest"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/region/ru"
)

// TestYooKassaFullCycle runs the real adapter against the in-memory
// YooKassa: checkout with a receipt, buyer pays, notification -> inbox ->
// paid, receipt looked up and issued, purchase email queued, full refund.
func TestYooKassaFullCycle(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	api := yktest.New()
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	gw, err := yookassa.New(yookassa.Config{ShopID: "shop-1", SecretKey: "test-secret", BaseURL: srv.URL + "/v3", Receipts: true, VATCode: ru.YooKassaVATCode})
	if err != nil {
		t.Fatal(err)
	}
	sf, _ := r.app.FindFirstRecordByData("storefronts", "code", "default")
	sf.Set("enabled_providers", map[string]any{"card": "yookassa"})
	if err := r.app.Save(sf); err != nil {
		t.Fatal(err)
	}
	r.svc.Store = &pb.Orders{App: r.app, TaxDocs: ru.Receipts{}, IssuerCode: "yookassa"}
	r.svc.Gateways = map[string]payments.Gateway{"yookassa": gw}
	r.svc.Issuer = yookassa.Receipts{G: gw}
	r.svc.ReturnURL = func(id string) string { return "https://shop.example.com/checkout/return/" + id }

	v := r.checkout(t, "yk-1")
	if v.Provider != "yookassa" || v.PaymentStatus != ordering.PaymentPending || !strings.HasPrefix(v.ConfirmationURL, "https://") {
		t.Fatalf("checkout: %+v", v)
	}
	body := api.Last()
	rc, ok := body["receipt"].(map[string]any)
	if !ok || rc["customer"].(map[string]any)["email"] != "buyer@example.com" || len(rc["items"].([]any)) != 1 {
		t.Fatalf("receipt not sent: %v", body)
	}
	if body["metadata"].(map[string]any)["order_id"] != v.OrderID ||
		body["confirmation"].(map[string]any)["return_url"] != "https://shop.example.com/checkout/return/"+v.OrderID {
		t.Fatalf("body %v", body)
	}

	// buyer paid on the YooKassa page; the notification arrives
	api.Set(v.ProviderPaymentID, "succeeded")
	req := httptest.NewRequest("POST", "/hooks/pay/yookassa", strings.NewReader(
		`{"type":"notification","event":"payment.succeeded","object":{"id":"`+v.ProviderPaymentID+`","status":"succeeded"}}`))
	ev, err := gw.ParseWebhook(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if dup, err := r.svc.AcceptWebhook(ctx, "yookassa", ev); err != nil || dup {
		t.Fatalf("accept: %v %v", dup, err)
	}
	if ok, err := r.svc.ProcessInboxOnce(ctx); !ok || err != nil {
		t.Fatalf("inbox: %v %v", ok, err)
	}
	if o, p := r.status(t, v.OrderID); o != "paid" || p != "succeeded" {
		t.Fatalf("after webhook: %s %s", o, p)
	}
	for i := 0; i < 5; i++ {
		if ok, _ := r.svc.ProcessOutboxOnce(ctx); !ok {
			break
		}
	}
	var doc struct {
		Status string `db:"status"`
		Number string `db:"number"`
		Ext    string `db:"external_id"`
	}
	if err := r.app.DB().NewQuery("SELECT status, number, external_id FROM tax_documents WHERE \"order\" = {:o}").Bind(dbx.Params{"o": v.OrderID}).One(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "issued" || doc.Number != "3986" || doc.Ext != "rc-1" {
		t.Fatalf("receipt: %+v", doc)
	}
	if r.emails.Load() != 1 {
		t.Fatalf("emails %d", r.emails.Load())
	}

	ref, err := r.svc.RefundOrder(ctx, v.OrderID, "buyer_request", "", "operator@example.com")
	if err != nil || ref.Status != "succeeded" {
		t.Fatalf("refund: %+v %v", ref, err)
	}
	if o, p := r.status(t, v.OrderID); o != "refunded" || p != "refunded" {
		t.Fatalf("after refund: %s %s", o, p)
	}
}
