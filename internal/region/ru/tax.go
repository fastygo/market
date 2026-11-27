// Package ru is the RU region pack in Go form (phase 1, ADR-0008,
// ADR-0015): TaxPolicy with a dated VAT table. Its test vectors are the
// future contract tests for the goja plugin form (ADR-0017).
package ru

import (
	"errors"
	"fmt"
	"time"

	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/taxdocs"
)

// VATRate is one row of the dated standard rate table.
type VATRate struct {
	From   time.Time // inclusive, Moscow calendar date
	RateBP int
	Code   string // document code of the rate
}

// msk is fixed UTC+3 (no tzdata dependency); the tax date is the Moscow
// calendar date of the order.
var msk = time.FixedZone("MSK", 3*60*60)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, msk) }

// StandardVAT is the standard VAT rate table, newest last.
var StandardVAT = []VATRate{
	{From: day(2019, 1, 1), RateBP: 2000, Code: "vat20"},
	{From: day(2026, 1, 1), RateBP: 2200, Code: "vat22"},
}

// Tax categories known to the RU pack.
const (
	CategoryStandard       = "standard"
	CategorySoftwareExempt = "software_exempt"
	CategoryNone           = "none"
)

// Errors.
var (
	ErrUnknownCategory   = errors.New("ru: unknown tax category")
	ErrIncompleteProfile = fmt.Errorf("ru: seller tax profile does not say whether it pays VAT: %w", ordering.ErrTaxProfile)
	ErrNoRate            = errors.New("ru: no VAT rate for the date")
	ErrExclusive         = errors.New("ru: B2C prices are tax inclusive")
)

// TaxPolicy implements ordering.TaxPolicy for RU.
type TaxPolicy struct {
	Table []VATRate // defaults to StandardVAT
}

var _ ordering.TaxPolicy = TaxPolicy{}

// RateAt returns the standard rate in force at t.
func (p TaxPolicy) RateAt(t time.Time) (VATRate, error) {
	table := p.Table
	if table == nil {
		table = StandardVAT
	}
	d := t.In(msk)
	var found *VATRate
	for i := range table {
		if !d.Before(table[i].From) {
			found = &table[i]
		}
	}
	if found == nil {
		return VATRate{}, fmt.Errorf("%w: %s", ErrNoRate, d.Format("2006-01-02"))
	}
	return *found, nil
}

// ResolveLines maps each line's category and seller tax profile to a line
// tax (ADR-0008 table): standard -> VAT inclusive at the dated rate for VAT
// payers, none otherwise; software_exempt -> exempt for VAT payers, none
// otherwise; none -> none.
func (p TaxPolicy) ResolveLines(at time.Time, display ordering.TaxDisplay, lines []ordering.TaxLineInput) ([]ordering.TaxResolution, error) {
	if display != ordering.DisplayInclusive {
		return nil, ErrExclusive
	}
	none := ordering.TaxResolution{Mode: ordering.TaxNone, Code: "none"}
	out := make([]ordering.TaxResolution, len(lines))
	for i, l := range lines {
		if l.Category == CategoryNone {
			out[i] = none
			continue
		}
		if l.Category != CategoryStandard && l.Category != CategorySoftwareExempt {
			return nil, fmt.Errorf("%w: %q", ErrUnknownCategory, l.Category)
		}
		if l.Seller.VATPayer == nil {
			return nil, ErrIncompleteProfile
		}
		if !*l.Seller.VATPayer {
			out[i] = none
			continue
		}
		if l.Category == CategorySoftwareExempt {
			out[i] = ordering.TaxResolution{Mode: ordering.TaxExempt, Code: "none"}
			continue
		}
		r, err := p.RateAt(at)
		if err != nil {
			return nil, err
		}
		out[i] = ordering.TaxResolution{RateBP: r.RateBP, Mode: ordering.TaxInclusive, Code: r.Code}
	}
	return out, nil
}

// Receipts is the RU taxdocs.Policy: every sale needs a 54-FZ receipt
// (operation "income") and every refund a return receipt ("income_return"),
// paid in full at once. The payment subject code for digital goods is a
// placeholder until the operator confirms it with the register provider.
type Receipts struct{}

// ForSale implements taxdocs.Policy.
func (Receipts) ForSale() (taxdocs.Kind, string, bool) { return taxdocs.Receipt, "income", true }

// ForRefund implements taxdocs.Policy.
func (Receipts) ForRefund() (taxdocs.Kind, string, bool) {
	return taxdocs.ReceiptReturn, "income_return", true
}

// LineCodes implements taxdocs.Policy.
func (Receipts) LineCodes(agent bool) (string, string) { return "service", "full_payment" }
