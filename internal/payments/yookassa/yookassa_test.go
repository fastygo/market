package yookassa_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/yookassa"
	"github.com/fastygo/market/internal/payments/yookassa/yktest"
	"github.com/fastygo/market/internal/platform/clientip"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/platform/tx"
	"github.com/fastygo/market/internal/region/ru"
	"github.com/fastygo/market/internal/taxdocs"
)

func gateway(t *testing.T, f *yktest.API, mod func(*yookassa.Config)) *yookassa.Gateway {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cfg := yookassa.Config{ShopID: "shop-1", SecretKey: "test-secret", BaseURL: srv.URL + "/v3", Receipts: true, VATCode: ru.YooKassaVATCode}
	if mod != nil {
		mod(&cfg)
	}
	g, err := yookassa.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func doc() *taxdocs.Document {
	return &taxdocs.Document{Kind: taxdocs.Receipt, Operation: "income", Currency: "RUB", Total: 199000, BuyerEmail: "buyer@example.com",
		Lines: []taxdocs.Line{{Title: "Theme", Qty: 1, Price: 199000, Amount: 199000, TaxMode: "inclusive", TaxCode: "vat22", TaxMinor: 35885, Subject: "service", Method: "full_payment"}}}
}

func create(t *testing.T, g *yookassa.Gateway) payments.CreatePaymentResult {
	t.Helper()
	res, err := g.CreatePayment(context.Background(), payments.CreatePaymentRequest{
		IdempotencyKey: "pay:user:key-1:yookassa", Amount: money.Money{Minor: 199000, Currency: "RUB"}, Method: "card",
		Description: "M-2026-000001", ReturnURL: "https://shop.example.com/checkout/return/o1", OrderID: "o1", Document: doc(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCreatePaymentRequestShapeAndIdempotence(t *testing.T) {
	f := yktest.New()
	g := gateway(t, f, nil)
	a, b := create(t, g), create(t, g)
	if a.ProviderPaymentID == "" || a.ProviderPaymentID != b.ProviderPaymentID || f.Payments() != 1 {
		t.Fatalf("not idempotent: %+v %+v", a, b)
	}
	if a.Status.State != payments.StatePending || !strings.HasPrefix(a.ConfirmationURL, "https://") {
		t.Fatalf("result %+v", a)
	}
	if f.Keys()[0] != f.Keys()[1] || len(f.Keys()[0]) != 64 || f.Keys()[0] != yookassa.IdempotenceKey("pay:user:key-1:yookassa") {
		t.Fatalf("idempotence keys %v", f.Keys())
	}
	body := f.Last()
	if body["capture"] != true || body["description"] != "M-2026-000001" {
		t.Fatalf("body %v", body)
	}
	if amt := body["amount"].(map[string]any); amt["value"] != "1990.00" || amt["currency"] != "RUB" {
		t.Fatalf("amount %v", amt)
	}
	conf := body["confirmation"].(map[string]any)
	if conf["type"] != "redirect" || conf["return_url"] != "https://shop.example.com/checkout/return/o1" {
		t.Fatalf("confirmation %v", conf)
	}
	if body["metadata"].(map[string]any)["order_id"] != "o1" || body["payment_method_data"].(map[string]any)["type"] != "bank_card" {
		t.Fatalf("metadata/method %v", body)
	}
	rc := body["receipt"].(map[string]any)
	item := rc["items"].([]any)[0].(map[string]any)
	if rc["customer"].(map[string]any)["email"] != "buyer@example.com" || item["vat_code"] != float64(11) ||
		item["payment_mode"] != "full_payment" || item["payment_subject"] != "service" || item["quantity"] != "1.00" ||
		item["amount"].(map[string]any)["value"] != "1990.00" {
		t.Fatalf("receipt %v", rc)
	}
}

func TestStatusMappingCancelRefund(t *testing.T) {
	f := yktest.New()
	g := gateway(t, f, func(c *yookassa.Config) { c.Receipts = false })
	ctx := context.Background()
	p := create(t, g)
	if f.Last()["receipt"] != nil {
		t.Fatal("receipt sent while disabled")
	}
	// a pending payment is not cancelled by the API: it expires at YooKassa
	st, err := g.CancelPayment(ctx, p.ProviderPaymentID, "cancel:1")
	if err != nil || st.State != payments.StatePending {
		t.Fatalf("cancel pending: %+v %v", st, err)
	}
	f.Set(p.ProviderPaymentID, "waiting_for_capture")
	if st, _ := g.FetchPayment(ctx, p.ProviderPaymentID); st.State != payments.StatePending {
		t.Fatalf("waiting_for_capture -> %s", st.State)
	}
	if st, err := g.CancelPayment(ctx, p.ProviderPaymentID, "cancel:1"); err != nil || st.State != payments.StateCanceled {
		t.Fatalf("cancel: %+v %v", st, err)
	}
	f.Set(p.ProviderPaymentID, "succeeded")
	st, err = g.FetchPayment(ctx, p.ProviderPaymentID)
	if err != nil || st.State != payments.StateSucceeded || st.Amount.Minor != 199000 {
		t.Fatalf("fetch: %+v %v", st, err)
	}
	rr, err := g.RefundPayment(ctx, payments.RefundRequest{IdempotencyKey: "refund:1", ProviderPaymentID: p.ProviderPaymentID, Amount: st.Amount})
	if err != nil || rr.State != payments.StateSucceeded || rr.ProviderRefundID != "rf-1" {
		t.Fatalf("refund: %+v %v", rr, err)
	}
	if st, _ := g.FetchPayment(ctx, p.ProviderPaymentID); st.State != payments.StateRefunded {
		t.Fatalf("after refund: %s", st.State)
	}
	if _, err := g.FetchPayment(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, payments.ErrNotFound) {
		t.Fatalf("unknown payment: %v", err)
	}
	f.Set(p.ProviderPaymentID, "canceled")
	if rr, err := g.RefundPayment(ctx, payments.RefundRequest{IdempotencyKey: "refund:2", ProviderPaymentID: p.ProviderPaymentID, Amount: st.Amount}); err != nil || rr.State != payments.StateFailed {
		t.Fatalf("refund of canceled: %+v %v", rr, err)
	}
}

func TestBadCredentialsArePermanentAndSecretFree(t *testing.T) {
	f := yktest.New()
	g := gateway(t, f, func(c *yookassa.Config) { c.SecretKey = "wrong-secret-value" })
	_, err := g.FetchPayment(context.Background(), "abc")
	if !errors.Is(err, payments.ErrPermanent) || strings.Contains(err.Error(), "wrong-secret-value") {
		t.Fatalf("err %v", err)
	}
	if _, err := yookassa.New(yookassa.Config{ShopID: "x"}); err == nil {
		t.Fatal("missing secret accepted")
	}
}

func TestNoNetworkInsideTx(t *testing.T) {
	g := gateway(t, yktest.New(), nil)
	if _, err := g.FetchPayment(tx.Mark(context.Background()), "abc"); !errors.Is(err, tx.ErrNetworkInTx) {
		t.Fatalf("err %v", err)
	}
}

func webhook(g *yookassa.Gateway, ip, body string) (payments.Event, error) {
	r := httptest.NewRequest("POST", "/hooks/pay/yookassa", strings.NewReader(body))
	r.RemoteAddr = ip + ":443"
	return g.ParseWebhook(context.Background(), r)
}

func TestWebhookVerification(t *testing.T) {
	f := yktest.New()
	g := gateway(t, f, func(c *yookassa.Config) { c.CheckIP = true })
	p := create(t, g)
	good := `{"type":"notification","event":"payment.succeeded","object":{"id":"` + p.ProviderPaymentID + `","status":"succeeded"}}`
	ev, err := webhook(g, "185.71.76.5", good)
	if err != nil || ev.EventID != "payment.succeeded:"+p.ProviderPaymentID || ev.ProviderPaymentID != p.ProviderPaymentID {
		t.Fatalf("good: %+v %v", ev, err)
	}
	if _, err := webhook(g, "203.0.113.9", good); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Fatalf("foreign ip: %v", err)
	}
	forged := `{"type":"notification","event":"payment.succeeded","object":{"id":"2f000000-0000-0000-0000-999999999999"}}`
	if _, err := webhook(g, "77.75.156.11", forged); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Fatalf("forged id: %v", err)
	}
	if _, err := webhook(g, "77.75.156.11", `{"type":"notification","event":"payment.succeeded","object":{"id":"../x"}}`); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Fatalf("path id: %v", err)
	}
	f.Set(p.ProviderPaymentID, "succeeded")
	if _, err := g.RefundPayment(context.Background(), payments.RefundRequest{IdempotencyKey: "r", ProviderPaymentID: p.ProviderPaymentID, Amount: money.Money{Minor: 199000, Currency: "RUB"}}); err != nil {
		t.Fatal(err)
	}
	ev, err = webhook(g, "2a02:5180::1", `{"type":"notification","event":"refund.succeeded","object":{"id":"rf-1","payment_id":"ignored"}}`)
	if err != nil || ev.ProviderPaymentID != p.ProviderPaymentID || ev.EventID != "refund.succeeded:rf-1" {
		t.Fatalf("refund event: %+v %v", ev, err)
	}
}

func TestReceiptsIssuer(t *testing.T) {
	f := yktest.New()
	g := gateway(t, f, nil)
	p := create(t, g)
	d := doc()
	d.ProviderRef = p.ProviderPaymentID
	res, err := yookassa.Receipts{G: g}.Issue(context.Background(), *d)
	if err != nil || res.Status != taxdocs.StatusIssued || res.ExternalID != "rc-1" || res.Number != "3986" {
		t.Fatalf("issue: %+v %v", res, err)
	}
	d.Kind, d.ProviderRef = taxdocs.ReceiptReturn, "rf-9"
	if _, err := (yookassa.Receipts{G: g}).Issue(context.Background(), *d); !errors.Is(err, yookassa.ErrNoReceipt) {
		t.Fatalf("missing: %v", err)
	}
}

func TestAmounts(t *testing.T) {
	for _, c := range []struct {
		m money.Money
		v string
	}{{money.Money{Minor: 5, Currency: "RUB"}, "0.05"}, {money.Money{Minor: 100, Currency: "JPY"}, "100"}, {money.Money{Minor: 123456, Currency: "EUR"}, "1234.56"}} {
		a, err := yookassa.FormatAmount(c.m)
		if err != nil || a.Value != c.v {
			t.Fatalf("%v -> %v %v", c.m, a, err)
		}
		back, err := yookassa.ParseAmount(a)
		if err != nil || back != c.m {
			t.Fatalf("%v -> %v %v", a, back, err)
		}
	}
}

// TestWebhookBehindProxy: behind the edge the IP allowlist sees the
// client address resolved by clientip; a spoofed X-Forwarded-For entry
// (YooKassa address prepended by the sender) does not pass.
func TestWebhookBehindProxy(t *testing.T) {
	f := yktest.New()
	edge, _ := clientip.ParsePrefixes("10.0.0.2/32")
	res := clientip.Resolver{Header: "X-Forwarded-For", Trusted: edge}
	g := gateway(t, f, func(c *yookassa.Config) { c.CheckIP, c.ClientIP = true, res.IP })
	p := create(t, g)
	good := `{"type":"notification","event":"payment.succeeded","object":{"id":"` + p.ProviderPaymentID + `","status":"succeeded"}}`
	send := func(remote string, xff ...string) error {
		r := httptest.NewRequest("POST", "/hooks/pay/yookassa", strings.NewReader(good))
		r.RemoteAddr = remote
		for _, v := range xff {
			r.Header.Add("X-Forwarded-For", v)
		}
		_, err := g.ParseWebhook(context.Background(), r)
		return err
	}
	if err := send("10.0.0.2:1234", "185.71.76.5"); err != nil {
		t.Fatalf("yookassa through edge: %v", err)
	}
	if err := send("10.0.0.2:1234", "185.71.76.5, 203.0.113.9"); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Fatalf("spoofed leftmost hop accepted: %v", err)
	}
	if err := send("203.0.113.9:1234", "185.71.76.5"); !errors.Is(err, payments.ErrInvalidSignature) {
		t.Fatalf("header from an untrusted peer accepted: %v", err)
	}
}
