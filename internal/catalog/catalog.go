// Package catalog is the read side of products for the storefront (phase 0).
// It knows one generic product; the product type is a code (ADR-0019) and no
// code here branches on it.
package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/fastygo/market/internal/platform/money"
)

// ErrNotFound is returned when a published product or category is missing.
var ErrNotFound = errors.New("catalog: not found")

// DefaultProductType is the plain product page with Download and Preview.
const DefaultProductType = "plain"

// Category is a catalog category.
type Category struct {
	Slug  string
	Title string
}

// Product is the storefront view of a published product with its lowest
// active offer in the storefront currency.
type Product struct {
	ID          string
	Slug        string
	Title       string
	Excerpt     string
	Description string
	ProductType string
	DemoURL     string
	CoverURL    string
	Category    Category
	SellerName  string
	SellerSlug  string
	Price       money.Money
	HasOffer    bool
}

// Seller is the public author profile (ADR-0020): only `display_name`
// and public profile fields, never legal data. Only `active` sellers are
// public.
type Seller struct {
	ID          string
	Slug        string
	DisplayName string
	Headline    string
	Bio         string // plain text, paragraphs on blank lines
	AvatarURL   string
	Website     string
	JoinedAt    time.Time
	ItemsCount  int // published products
}

// Filter narrows a product list.
type Filter struct {
	Category string
	Seller   string // seller slug
	Query    string // search in title, excerpt, category and author name
	Exclude  string // product slug left out (more from this author)
	Limit    int
	Offset   int // numbered storefront pages; capped by MaxPage
}

// PageSize and MaxPage bound the numbered catalog pages: OFFSET is cheap
// at this size (the list reads in index order), and the cap keeps a
// crawler from walking arbitrarily deep pages. Keyset pagination on
// (created, id) is for an API or an endless feed, which do not exist yet.
const (
	PageSize = 100
	MaxPage  = 100
)

// Reader is declared here (consumer side) and implemented by platform/pb.
type Reader interface {
	Categories(ctx context.Context) ([]Category, error)
	Category(ctx context.Context, slug string) (Category, error)
	Products(ctx context.Context, currency string, f Filter) ([]Product, error)
	Product(ctx context.Context, currency, slug string) (Product, error)
	// Seller loads an active seller's public profile by slug.
	Seller(ctx context.Context, slug string) (Seller, error)
}
