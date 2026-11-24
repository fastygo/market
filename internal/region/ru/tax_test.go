package ru

import (
	"errors"
	"testing"
	"time"

	"github.com/fastygo/market/internal/ordering"
)

func b(v bool) *bool { return &v }

func TestRateTableEffectiveDates(t *testing.T) {
	p := TaxPolicy{}
	cases := []struct {
		at   time.Time
		rate int
		code string
	}{
		{time.Date(2025, 12, 31, 20, 59, 59, 0, time.UTC), 2000, "vat20"}, // 23:59:59 Moscow
		{time.Date(2025, 12, 31, 21, 0, 0, 0, time.UTC), 2200, "vat22"},   // 00:00 Moscow, 1 Jan 2026
		{time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC), 2200, "vat22"},
	}
	for _, c := range cases {
		r, err := p.RateAt(c.at)
		if err != nil || r.RateBP != c.rate || r.Code != c.code {
			t.Errorf("%s: got %+v %v", c.at, r, err)
		}
	}
	if _, err := p.RateAt(time.Date(2018, 6, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, ErrNoRate) {
		t.Errorf("want ErrNoRate, got %v", err)
	}
}

func TestResolveLines(t *testing.T) {
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	in := []ordering.TaxLineInput{
		{Category: "standard", Seller: ordering.SellerTax{VATPayer: b(true)}},
		{Category: "standard", Seller: ordering.SellerTax{VATPayer: b(false)}},
		{Category: "software_exempt", Seller: ordering.SellerTax{VATPayer: b(true)}},
		{Category: "none"},
	}
	got, err := TaxPolicy{}.ResolveLines(at, ordering.DisplayInclusive, in)
	if err != nil {
		t.Fatal(err)
	}
	want := []ordering.TaxResolution{
		{RateBP: 2200, Mode: ordering.TaxInclusive, Code: "vat22"},
		{Mode: ordering.TaxNone, Code: "none"},
		{Mode: ordering.TaxExempt, Code: "none"},
		{Mode: ordering.TaxNone, Code: "none"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %+v want %+v", i, got[i], want[i])
		}
	}
	// inclusive extraction vectors (ADR-0006): 22/122 half up
	vec := []struct{ line, tax int64 }{{290000, 52295}, {12200, 2200}, {1, 0}, {3, 1}, {100, 18}, {0, 0}}
	for _, v := range vec {
		tax, _ := ordering.LineTax(v.line, want[0])
		if tax != v.tax {
			t.Errorf("tax(%d) = %d, want %d", v.line, tax, v.tax)
		}
	}
	if _, err := (TaxPolicy{}).ResolveLines(at, ordering.DisplayInclusive, []ordering.TaxLineInput{{Category: "standard"}}); !errors.Is(err, ErrIncompleteProfile) {
		t.Errorf("want incomplete profile, got %v", err)
	}
	if _, err := (TaxPolicy{}).ResolveLines(at, ordering.DisplayInclusive, []ordering.TaxLineInput{{Category: "books"}}); !errors.Is(err, ErrUnknownCategory) {
		t.Errorf("want unknown category, got %v", err)
	}
	if _, err := (TaxPolicy{}).ResolveLines(at, ordering.DisplayExclusive, in); !errors.Is(err, ErrExclusive) {
		t.Errorf("want exclusive error, got %v", err)
	}
}
