package ru

import "fmt"

// YooKassaVATCode maps a document line tax (mode and rate code from the
// TaxPolicy) to the YooKassa receipt vat_code (API reference "vat_code",
// codes 1 to 12; 11 is the 22% rate in force from 2026-01-01). Full payment
// receipts use the plain rate, not the calculated one (20/120, 22/122).
func YooKassaVATCode(taxMode, taxCode string) (int, error) {
	switch taxMode {
	case "none", "exempt":
		return 1, nil // without VAT
	case "inclusive":
		switch taxCode {
		case "vat0":
			return 2, nil
		case "vat10":
			return 3, nil
		case "vat20":
			return 4, nil
		case "vat5":
			return 7, nil
		case "vat7":
			return 8, nil
		case "vat22":
			return 11, nil
		}
	}
	return 0, fmt.Errorf("ru: no receipt VAT code for tax mode %q code %q", taxMode, taxCode)
}
