package ordering

import (
	"errors"
	"testing"

	"github.com/fastygo/market/internal/platform/money"
)

func offer(price int64, model SaleModel, bps int) Offer {
	return Offer{ID: "o", SellerID: "s1", Price: money.Money{Minor: price, Currency: "RUB"}, Active: true, ProductPublished: true, SaleModel: model, CommissionBPS: bps}
}

var vat22 = TaxResolution{RateBP: 2200, Mode: TaxInclusive, Code: "vat22"}

func TestLineTaxVectors(t *testing.T) {
	cases := []struct {
		line int64
		r    TaxResolution
		want int64
	}{
		{290000, vat22, 52295},
		{10001, TaxResolution{RateBP: 2000, Mode: TaxExclusive}, 2000}, // 2000.2
		{5, TaxResolution{RateBP: 1000, Mode: TaxExclusive}, 1},        // 0.5 half up
		{money.MaxMinor, vat22, 1624249045936900},                      // no overflow
		{1000, TaxResolution{Mode: TaxExempt}, 0},
		{1000, TaxResolution{Mode: TaxNone}, 0},
	}
	for _, c := range cases {
		got, err := LineTax(c.line, c.r)
		if err != nil || got != c.want {
			t.Errorf("LineTax(%d, %+v) = %d %v, want %d", c.line, c.r, got, err, c.want)
		}
	}
	if _, err := LineTax(-1, vat22); !errors.Is(err, ErrAmountRange) {
		t.Error("negative line must fail")
	}
}

func TestPriceAndInvariants(t *testing.T) {
	lines, tot, err := Price("RUB", DisplayInclusive, []Offer{offer(290000, Principal, 0), offer(149000, Principal, 0)}, []TaxResolution{vat22, vat22})
	if err != nil {
		t.Fatal(err)
	}
	if tot.Subtotal != 439000 || tot.Sum != 439000 || tot.Discount != 0 {
		t.Fatalf("totals %+v", tot)
	}
	var tax int64
	for _, l := range lines {
		tax += l.TaxMinor
	}
	if tot.Tax != tax || tot.Tax != 52295+26869 {
		t.Fatalf("tax %d, sum of lines %d", tot.Tax, tax)
	}
	// exclusive adds tax to the total
	_, ex, _ := Price("EUR", DisplayExclusive, []Offer{{Price: money.Money{Minor: 10000, Currency: "EUR"}, Active: true, ProductPublished: true}}, []TaxResolution{{RateBP: 2000, Mode: TaxExclusive}})
	if ex.Sum != 12000 || ex.Tax != 2000 {
		t.Fatalf("exclusive %+v", ex)
	}
	if _, _, err := Price("EUR", DisplayInclusive, []Offer{offer(1, Principal, 0)}, []TaxResolution{vat22}); !errors.Is(err, ErrCurrencyMismatch) {
		t.Error("currency mismatch")
	}
	o := offer(1, Principal, 0)
	o.Active = false
	if _, _, err := Price("RUB", DisplayInclusive, []Offer{o}, []TaxResolution{vat22}); !errors.Is(err, ErrOfferInactive) {
		t.Error("inactive offer")
	}
	if _, _, err := Price("RUB", DisplayInclusive, nil, nil); !errors.Is(err, ErrEmptyCart) {
		t.Error("empty")
	}
}

func TestPostingRules(t *testing.T) {
	// principal, inclusive VAT
	lines, _, _ := Price("RUB", DisplayInclusive, []Offer{offer(290000, Principal, 0)}, []TaxResolution{vat22})
	es := PostPayment("p1", "RUB", lines, nil)
	if !Balanced(es) || len(es) != 3 {
		t.Fatalf("principal %+v", es)
	}
	want := map[string]int64{AccProviderClearing: 290000, AccPlatformRevenue: -(290000 - 52295), AccTaxPayable: -52295}
	for _, e := range es {
		if want[e.Account] != e.Amount || e.TxnID != "pay:p1:succeeded" {
			t.Errorf("%+v", e)
		}
	}
	// agent: 10001 at 1500 bps -> commission 1500 (1500.15), remainder to the platform
	lines, _, _ = Price("RUB", DisplayInclusive, []Offer{offer(10001, Agent, 1500)}, []TaxResolution{{Mode: TaxNone}})
	if lines[0].Commission != 1500 {
		t.Fatalf("commission %d", lines[0].Commission)
	}
	es = PostPayment("p2", "RUB", lines, func(l Line) int64 { tax, _ := LineTax(l.Commission, vat22); return tax })
	if !Balanced(es) {
		t.Fatalf("agent unbalanced %+v", es)
	}
	got := map[string]int64{}
	for _, e := range es {
		got[e.Account] = e.Amount
		if e.Account == AccSellerPayable && e.Seller != "s1" {
			t.Error("seller_payable needs the seller")
		}
	}
	if got[AccSellerPayable] != -(10001-1500) || got[AccTaxPayable] != -270 || got[AccPlatformRevenue] != -(1500-270) {
		t.Fatalf("agent postings %+v", got)
	}
	// zero exponent currency, free product: clearing row kept, still balanced
	lines, _, _ = Price("JPY", DisplayInclusive, []Offer{{Price: money.Money{Minor: 0, Currency: "JPY"}, Active: true, ProductPublished: true}}, []TaxResolution{{Mode: TaxNone}})
	es = PostPayment("p3", "JPY", lines, nil)
	if !Balanced(es) || len(es) != 1 {
		t.Fatalf("free %+v", es)
	}
}

func TestTransitions(t *testing.T) {
	ok := []struct{ from, to PaymentStatus }{
		{PaymentCreated, PaymentPending}, {PaymentPending, PaymentSucceeded}, {PaymentCanceled, PaymentSucceeded},
		{PaymentSucceeded, PaymentRefunded}, {PaymentSucceeded, PaymentDisputed}, {PaymentDisputed, PaymentSucceeded},
	}
	for _, c := range ok {
		if ch, err := PaymentTransition(c.from, c.to); err != nil || !ch {
			t.Errorf("%s -> %s: %v", c.from, c.to, err)
		}
	}
	if ch, err := PaymentTransition(PaymentSucceeded, PaymentSucceeded); err != nil || ch {
		t.Error("same status is a no-op")
	}
	for _, c := range []struct{ from, to PaymentStatus }{{PaymentSucceeded, PaymentPending}, {PaymentRefunded, PaymentSucceeded}, {PaymentSucceeded, PaymentCanceled}} {
		if _, err := PaymentTransition(c.from, c.to); !errors.Is(err, ErrTransition) {
			t.Errorf("%s -> %s must fail", c.from, c.to)
		}
	}
	type oc struct {
		from OrderStatus
		p    PaymentStatus
		to   OrderStatus
		ch   bool
	}
	for _, c := range []oc{
		{OrderPending, PaymentPending, OrderAwaitingPayment, true},
		{OrderAwaitingPayment, PaymentSucceeded, OrderPaid, true},
		{OrderCancelled, PaymentSucceeded, OrderPaid, true}, // late success
		{OrderPaid, PaymentSucceeded, OrderPaid, false},
		{OrderAwaitingPayment, PaymentCanceled, OrderCancelled, true},
		{OrderPaid, PaymentCanceled, OrderPaid, false},
	} {
		to, ch, err := OrderOnPayment(c.from, c.p)
		if err != nil || to != c.to || ch != c.ch {
			t.Errorf("%+v: got %s %v %v", c, to, ch, err)
		}
	}
}
