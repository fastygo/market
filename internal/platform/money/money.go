// Package money holds amounts as int64 minor units plus an ISO 4217 code
// (ADR-0006). No floats, no conversion between currencies.
package money

import (
	"errors"
	"fmt"
	"strings"
)

// MaxMinor is the largest amount PocketBase can store exactly (2^53-1).
const MaxMinor int64 = 1<<53 - 1

// Errors returned by the package.
var (
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	ErrUnknownCurrency  = errors.New("money: unknown currency")
	ErrOverflow         = errors.New("money: amount out of range")
)

// exponents is the minor-unit exponent per ISO 4217 code. Adding a currency
// is a data change here, never a branch in domain code.
var exponents = map[string]int{
	"RUB": 2, "EUR": 2, "USD": 2, "GBP": 2, "KZT": 2, "BYN": 2,
	"JPY": 0, "KRW": 0,
}

// Money is an amount in minor units of one currency.
type Money struct {
	Minor    int64
	Currency string
}

// New validates the currency code and the range.
func New(minor int64, currency string) (Money, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if _, ok := exponents[currency]; !ok {
		return Money{}, fmt.Errorf("%w: %q", ErrUnknownCurrency, currency)
	}
	if minor > MaxMinor || minor < -MaxMinor {
		return Money{}, ErrOverflow
	}
	return Money{Minor: minor, Currency: currency}, nil
}

// Exponent returns the number of minor digits of a currency.
func Exponent(currency string) (int, bool) {
	e, ok := exponents[strings.ToUpper(currency)]
	return e, ok
}

// Add sums two amounts of the same currency.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, ErrCurrencyMismatch
	}
	s := m.Minor + o.Minor
	if s > MaxMinor || s < -MaxMinor {
		return Money{}, ErrOverflow
	}
	return Money{Minor: s, Currency: m.Currency}, nil
}

// IsZero reports a zero amount.
func (m Money) IsZero() bool { return m.Minor == 0 }

// String is a technical representation ("1990.00 RUB"), not for display;
// display formatting is locale-aware in the web layer.
func (m Money) String() string {
	e, ok := exponents[m.Currency]
	if !ok || e == 0 {
		return fmt.Sprintf("%d %s", m.Minor, m.Currency)
	}
	sign := ""
	v := m.Minor
	if v < 0 {
		sign, v = "-", -v
	}
	p := int64(1)
	for i := 0; i < e; i++ {
		p *= 10
	}
	return fmt.Sprintf("%s%d.%0*d %s", sign, v/p, e, v%p, m.Currency)
}
