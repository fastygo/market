package money

import (
	"errors"
	"testing"
)

func TestNewValidates(t *testing.T) {
	if _, err := New(100, "XXX"); !errors.Is(err, ErrUnknownCurrency) {
		t.Fatalf("unknown currency: got %v", err)
	}
	if _, err := New(MaxMinor+1, "RUB"); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow: got %v", err)
	}
	m, err := New(199000, " rub ")
	if err != nil || m.Currency != "RUB" || m.Minor != 199000 {
		t.Fatalf("got %+v %v", m, err)
	}
}

func TestAdd(t *testing.T) {
	a, _ := New(150, "EUR")
	b, _ := New(250, "EUR")
	s, err := a.Add(b)
	if err != nil || s.Minor != 400 {
		t.Fatalf("got %+v %v", s, err)
	}
	r, _ := New(1, "RUB")
	if _, err := a.Add(r); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("mismatch: got %v", err)
	}
	big, _ := New(MaxMinor, "EUR")
	if _, err := big.Add(a); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow: got %v", err)
	}
}

func TestString(t *testing.T) {
	cases := map[Money]string{
		{Minor: 199000, Currency: "RUB"}: "1990.00 RUB",
		{Minor: -5, Currency: "EUR"}:     "-0.05 EUR",
		{Minor: 1200, Currency: "JPY"}:   "1200 JPY",
	}
	for m, want := range cases {
		if got := m.String(); got != want {
			t.Errorf("%+v: got %q want %q", m, got, want)
		}
	}
}
