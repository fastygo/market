package payments_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/fakegw"
	"github.com/fastygo/market/internal/platform/money"
)

func TestParseRules(t *testing.T) {
	rules, err := payments.ParseRules("currency=RUB,country=RU,entity=it_garage,method=card:yookassa; currency=EUR:stripe")
	if err != nil || len(rules) != 2 {
		t.Fatalf("%v %+v", err, rules)
	}
	r0 := rules[0]
	if r0.Currency != "RUB" || r0.Country != "RU" || r0.Entity != "it_garage" || r0.Method != "card" || r0.Provider != "yookassa" {
		t.Fatalf("%+v", r0)
	}
	if rules[1].Currency != "EUR" || rules[1].Provider != "stripe" || rules[1].Country != "" {
		t.Fatalf("%+v", rules[1])
	}
	if _, err := payments.ParseRules("currency=RUB"); err == nil {
		t.Fatal("missing provider must fail")
	}
	if _, err := payments.ParseRules("foo=bar:yookassa"); err == nil {
		t.Fatal("unknown field must fail")
	}
}

func TestRegistrySelectAndFilter(t *testing.T) {
	reg := payments.NewRegistry()
	gw := fakegw.New([]byte("s"))
	reg.Add(gw)
	reg.SetRules([]payments.Rule{
		{Currency: "RUB", Entity: "it_garage", Provider: "yookassa"},
		{Currency: "RUB", Provider: fakegw.Code},
	})
	// first rule points at a provider that is not enabled; skip to the next
	g, code, ok := reg.Select(payments.Criteria{Currency: "RUB", Entity: "it_garage", Method: "card"})
	if !ok || code != fakegw.Code || g == nil {
		t.Fatalf("want fake fallback, got %s ok=%v", code, ok)
	}
	if _, _, ok := reg.Select(payments.Criteria{Currency: "EUR"}); ok {
		t.Fatal("no matching rule")
	}
	if missing := reg.Filter([]string{"yookassa"}); len(missing) != 1 || missing[0] != "yookassa" {
		t.Fatalf("missing %v", missing)
	}
	if _, ok := reg.Gateway(fakegw.Code); ok {
		t.Fatal("fake must be filtered out")
	}
}

func TestHandleWebhookAndGetStatus(t *testing.T) {
	gw := fakegw.New([]byte("secret"))
	res, err := gw.CreatePayment(context.Background(), payments.CreatePaymentRequest{
		IdempotencyKey: "k", Amount: money.Money{Minor: 100, Currency: "RUB"},
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := payments.GetStatus(context.Background(), gw, res.ProviderPaymentID)
	if err != nil || st.State != payments.StatePending || st.ProviderPaymentID != res.ProviderPaymentID {
		t.Fatalf("%+v %v", st, err)
	}
	body, sig := gw.Webhook("evt-1", res.ProviderPaymentID, payments.StateSucceeded)
	req, _ := http.NewRequest("POST", "/webhooks/fake", strings.NewReader(string(body)))
	req.Header.Set(fakegw.SignatureHeader, sig)
	var got payments.Event
	ev, dup, err := payments.HandleWebhook(context.Background(), gw, req, func(_ context.Context, provider string, e payments.Event) (bool, error) {
		if provider != string(fakegw.Code) {
			t.Fatalf("provider %s", provider)
		}
		got = e
		return false, nil
	})
	if err != nil || dup || ev.EventID != "evt-1" || got.ProviderPaymentID != res.ProviderPaymentID {
		t.Fatalf("%+v dup=%v err=%v", ev, dup, err)
	}
	bad, _ := http.NewRequest("POST", "/webhooks/fake", strings.NewReader(string(body)))
	if _, _, err := payments.HandleWebhook(context.Background(), gw, bad, func(context.Context, string, payments.Event) (bool, error) {
		t.Fatal("accept must not run")
		return false, nil
	}); err != payments.ErrInvalidSignature {
		t.Fatalf("want invalid signature, got %v", err)
	}
}

func TestParseProviders(t *testing.T) {
	got := payments.ParseProviders(" YooKassa, fake ,")
	if len(got) != 2 || got[0] != "yookassa" || got[1] != "fake" {
		t.Fatalf("%v", got)
	}
	if payments.ParseProviders("  ") != nil && len(payments.ParseProviders("")) != 0 {
		t.Fatal("empty")
	}
}
