// Package storefront holds the deployment's storefront config (ADR-0015,
// ADR-0018, ADR-0020). Phase 0 reads one row by code.
package storefront

import "context"

// Storefront is the subset of the `storefronts` row the web layer needs.
// Brand values (name, tagline, skin, ...) are data in `storefronts.brand`,
// never code (ADR-0020).
type Storefront struct {
	Code          string
	BrandName     string
	Skin          string // UI8Kit skin of the storefront (ADR-0018), e.g. "storeee"
	Currency      string
	DefaultLocale string
	CommerceMode  string
	Brand         Brand
}

// Brand is the decoded `storefronts.brand` json (ADR-0020). Name and Skin
// are mirrored in Storefront.BrandName and Storefront.Skin.
type Brand struct {
	Name           string   `json:"name"`
	ShortName      string   `json:"short_name"`
	Tagline        string   `json:"tagline"`
	AlternateNames []string `json:"alternate_names"`
	Logo           string   `json:"logo"`
	Favicon        string   `json:"favicon"`
	Skin           string   `json:"skin"`
	OGImage        string   `json:"og_image"`
}

// Reader loads a storefront by code.
type Reader interface {
	Storefront(ctx context.Context, code string) (Storefront, error)
}
