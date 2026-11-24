// Package ordering holds the order and payment domain: pricing with
// snapshots, pure transitions, ledger posting rules and the checkout,
// apply and reconciliation use cases (ADR-0004, ADR-0006, ADR-0007). It
// declares the interfaces it needs (TaxPolicy, Store) and never imports
// PocketBase or a region pack.
package ordering

import (
	"errors"
	"fmt"

	"github.com/fastygo/market/internal/platform/money"
)

// SaleModel of a seller (ADR-0005).
type SaleModel string

// Sale models.
const (
	Principal SaleModel = "principal"
	Agent     SaleModel = "agent"
)

// Offer is what checkout reads before the write transaction.
type Offer struct {
	ID, ProductID, ProductSlug, ProductTitle, SellerID string
	LicenseType, LicenseTitle, LicenseTermsVersion     string
	TaxCategory                                        string
	Price                                              money.Money
	SupportMonths                                      int
	Active, ProductPublished                           bool
	SaleModel                                          SaleModel
	CommissionBPS                                      int
	SellerTax                                          SellerTax
}

// Line is a priced order line (the order_items snapshot).
type Line struct {
	Offer        Offer
	Qty          int
	UnitPrice    int64
	UnitDiscount int64
	LineMinor    int64
	Tax          TaxResolution
	TaxMinor     int64
	Commission   int64
}

// Totals of an order; all in one currency.
type Totals struct {
	Currency                     string
	Subtotal, Discount, Tax, Sum int64 // Sum is total_minor
}

// Pricing errors.
var (
	ErrEmptyCart        = errors.New("ordering: empty cart")
	ErrCurrencyMismatch = errors.New("ordering: offer currency differs from the storefront")
	ErrOfferInactive    = errors.New("ordering: offer is not for sale")
	ErrDiscount         = errors.New("ordering: discounts are not supported in phase 1")
)

// Price builds the lines and totals from offers and the tax resolutions
// returned by the region TaxPolicy (one per offer, same order).
func Price(currency string, display TaxDisplay, offers []Offer, res []TaxResolution) ([]Line, Totals, error) {
	t := Totals{Currency: currency}
	if len(offers) == 0 {
		return nil, t, ErrEmptyCart
	}
	if len(res) != len(offers) {
		return nil, t, fmt.Errorf("ordering: %d tax resolutions for %d lines", len(res), len(offers))
	}
	lines := make([]Line, 0, len(offers))
	for i, o := range offers {
		if !o.Active || !o.ProductPublished {
			return nil, t, ErrOfferInactive
		}
		if o.Price.Currency != currency {
			return nil, t, ErrCurrencyMismatch
		}
		if o.Price.Minor < 0 || o.Price.Minor > money.MaxMinor {
			return nil, t, ErrAmountRange
		}
		l := Line{Offer: o, Qty: 1, UnitPrice: o.Price.Minor, Tax: res[i]}
		l.LineMinor = (l.UnitPrice - l.UnitDiscount) * int64(l.Qty)
		tax, err := LineTax(l.LineMinor, l.Tax)
		if err != nil {
			return nil, t, err
		}
		l.TaxMinor = tax
		if o.SaleModel == Agent {
			l.Commission = roundHalfUp(l.LineMinor, int64(o.CommissionBPS), 10000)
		}
		t.Subtotal += l.UnitPrice * int64(l.Qty)
		t.Discount += l.UnitDiscount * int64(l.Qty)
		t.Tax += l.TaxMinor
		if l.Tax.Mode == TaxExclusive {
			t.Sum += l.LineMinor + l.TaxMinor
		} else {
			t.Sum += l.LineMinor
		}
		lines = append(lines, l)
	}
	if t.Discount != 0 {
		return nil, t, ErrDiscount
	}
	if t.Sum > money.MaxMinor {
		return nil, t, ErrAmountRange
	}
	_ = display
	return lines, t, nil
}
