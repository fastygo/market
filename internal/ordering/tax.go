package ordering

import (
	"errors"
	"math/big"
	"time"
)

// TaxMode of a line (ADR-0006).
type TaxMode string

// Tax modes.
const (
	TaxInclusive     TaxMode = "inclusive"
	TaxExclusive     TaxMode = "exclusive"
	TaxExempt        TaxMode = "exempt"
	TaxReverseCharge TaxMode = "reverse_charge"
	TaxNone          TaxMode = "none"
)

// TaxDisplay is the storefront price convention.
type TaxDisplay string

// Display conventions.
const (
	DisplayInclusive TaxDisplay = "inclusive"
	DisplayExclusive TaxDisplay = "exclusive"
)

// SellerTax is the part of a seller's tax_profile the policy needs.
// VATPayer is nil when the profile does not say (incomplete data).
type SellerTax struct {
	VATPayer *bool
}

// TaxLineInput is one line for TaxPolicy.ResolveLines.
type TaxLineInput struct {
	Category string // offers.tax_category, region-defined
	Seller   SellerTax
}

// TaxResolution is what a line snapshots in its tax_* columns.
type TaxResolution struct {
	RateBP int
	Mode   TaxMode
	Code   string // region document code
}

// TaxPolicy is declared here and implemented by the region pack
// (internal/region/ru in phase 1). Batch: one call per checkout.
type TaxPolicy interface {
	ResolveLines(at time.Time, display TaxDisplay, lines []TaxLineInput) ([]TaxResolution, error)
}

// ErrAmountRange is returned for negative or too large amounts.
var ErrAmountRange = errors.New("ordering: amount out of range")

// LineTax computes the tax of one line amount, half up (ADR-0006):
// inclusive: line * rate / (10000 + rate); exclusive: line * rate / 10000;
// exempt, none, reverse_charge: 0.
func LineTax(line int64, r TaxResolution) (int64, error) {
	if line < 0 || r.RateBP < 0 {
		return 0, ErrAmountRange
	}
	var den int64
	switch r.Mode {
	case TaxInclusive:
		den = 10000 + int64(r.RateBP)
	case TaxExclusive:
		den = 10000
	default:
		return 0, nil
	}
	if r.RateBP == 0 {
		return 0, nil
	}
	return roundHalfUp(line, int64(r.RateBP), den), nil
}

// roundHalfUp returns round(a*b/den) half up for non-negative inputs,
// without overflow (math/big).
func roundHalfUp(a, b, den int64) int64 {
	n := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	n.Mul(n, big.NewInt(2))
	n.Add(n, big.NewInt(den))
	n.Quo(n, big.NewInt(2*den))
	return n.Int64()
}
