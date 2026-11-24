// Package catalog is the read side of products for the storefront (phase 0).
// It knows one generic product; the product type is a code (ADR-0019) and no
// code here branches on it.
package catalog

import (
	"context"
	"errors"

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

// Filter narrows a product list.
type Filter struct {
	Category string
	Limit    int
}

// Reader is declared here (consumer side) and implemented by platform/pb.
type Reader interface {
	Categories(ctx context.Context) ([]Category, error)
	Category(ctx context.Context, slug string) (Category, error)
	Products(ctx context.Context, currency string, f Filter) ([]Product, error)
	Product(ctx context.Context, currency, slug string) (Product, error)
}
