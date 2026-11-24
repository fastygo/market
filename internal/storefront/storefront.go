// Package storefront holds the deployment's storefront config (ADR-0015,
// ADR-0018). Phase 0 reads one row by code.
package storefront

import "context"

// Storefront is the subset of the `storefronts` row the web layer needs.
type Storefront struct {
	Code          string
	BrandName     string
	Skin          string // UI8Kit skin of the storefront (ADR-0018), e.g. "godudy"
	Currency      string
	DefaultLocale string
	CommerceMode  string
}

// Reader loads a storefront by code.
type Reader interface {
	Storefront(ctx context.Context, code string) (Storefront, error)
}
