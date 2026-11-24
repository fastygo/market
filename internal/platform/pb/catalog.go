// Package pb holds the PocketBase adapters: repositories implementing the
// interfaces declared by domain packages (ADR-0002, ADR-0014).
package pb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/storefront"
)

// Catalog implements catalog.Reader and storefront.Reader on PocketBase.
type Catalog struct {
	App core.App
}

var (
	_ catalog.Reader    = (*Catalog)(nil)
	_ storefront.Reader = (*Catalog)(nil)
)

type productRow struct {
	ID          string         `db:"id"`
	Slug        string         `db:"slug"`
	Title       string         `db:"title"`
	Excerpt     string         `db:"excerpt"`
	Description string         `db:"description"`
	ProductType string         `db:"product_type"`
	DemoURL     string         `db:"demo_url"`
	Cover       string         `db:"cover"`
	CatSlug     sql.NullString `db:"cat_slug"`
	CatTitle    sql.NullString `db:"cat_title"`
	SellerName  string         `db:"seller_name"`
	SellerSlug  string         `db:"seller_slug"`
	PriceMinor  sql.NullInt64  `db:"price_minor"`
}

// one query, no N+1: lowest active offer in the storefront currency
const productSelect = `
SELECT p.id, p.slug, p.title, p.excerpt, p.description, p.product_type, p.demo_url, p.cover,
       c.slug AS cat_slug, c.title AS cat_title,
       s.display_name AS seller_name, s.slug AS seller_slug,
       (SELECT MIN(o.price_minor) FROM offers o
         WHERE o.product = p.id AND o.active = TRUE AND o.currency = {:currency}) AS price_minor
FROM products p
JOIN sellers s ON s.id = p.seller
LEFT JOIN categories c ON c.id = p.category
WHERE p.status = 'published'`

func (r productRow) toDomain(currency, coverURL string) catalog.Product {
	p := catalog.Product{
		ID: r.ID, Slug: r.Slug, Title: r.Title, Excerpt: r.Excerpt, Description: r.Description,
		ProductType: r.ProductType, DemoURL: r.DemoURL, CoverURL: coverURL,
		SellerName: r.SellerName, SellerSlug: r.SellerSlug,
		Category: catalog.Category{Slug: r.CatSlug.String, Title: r.CatTitle.String},
	}
	if p.ProductType == "" {
		p.ProductType = catalog.DefaultProductType
	}
	if r.PriceMinor.Valid {
		p.Price, p.HasOffer = money.Money{Minor: r.PriceMinor.Int64, Currency: currency}, true
	}
	return p
}

func (c *Catalog) coverURL(productID, file string) string {
	if file == "" {
		return ""
	}
	col, err := c.App.FindCachedCollectionByNameOrId("products")
	if err != nil {
		return ""
	}
	return "/api/files/" + col.Id + "/" + productID + "/" + file
}

// Products lists published products, newest first.
func (c *Catalog) Products(ctx context.Context, currency string, f catalog.Filter) ([]catalog.Product, error) {
	q := productSelect
	params := dbx.Params{"currency": currency}
	if f.Category != "" {
		q += ` AND c.slug = {:cat}`
		params["cat"] = f.Category
	}
	q += ` ORDER BY p.published_at DESC, p.created DESC`
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	q += fmt.Sprintf(` LIMIT %d`, limit)
	var rows []productRow
	if err := c.App.DB().NewQuery(q).WithContext(ctx).Bind(params).All(&rows); err != nil {
		return nil, fmt.Errorf("catalog: products: %w", err)
	}
	out := make([]catalog.Product, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toDomain(currency, c.coverURL(r.ID, r.Cover)))
	}
	return out, nil
}

// Product loads one published product by slug.
func (c *Catalog) Product(ctx context.Context, currency, slug string) (catalog.Product, error) {
	var r productRow
	err := c.App.DB().NewQuery(productSelect + ` AND p.slug = {:slug} LIMIT 1`).WithContext(ctx).
		Bind(dbx.Params{"currency": currency, "slug": slug}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Product{}, catalog.ErrNotFound
	}
	if err != nil {
		return catalog.Product{}, fmt.Errorf("catalog: product: %w", err)
	}
	return r.toDomain(currency, c.coverURL(r.ID, r.Cover)), nil
}

// Categories lists categories by sort order.
func (c *Catalog) Categories(ctx context.Context) ([]catalog.Category, error) {
	var rows []struct {
		Slug  string `db:"slug"`
		Title string `db:"title"`
	}
	if err := c.App.DB().NewQuery(`SELECT slug, title FROM categories ORDER BY sort, title`).WithContext(ctx).All(&rows); err != nil {
		return nil, fmt.Errorf("catalog: categories: %w", err)
	}
	out := make([]catalog.Category, 0, len(rows))
	for _, r := range rows {
		out = append(out, catalog.Category{Slug: r.Slug, Title: r.Title})
	}
	return out, nil
}

// Category loads one category by slug.
func (c *Catalog) Category(ctx context.Context, slug string) (catalog.Category, error) {
	var r struct {
		Slug  string `db:"slug"`
		Title string `db:"title"`
	}
	err := c.App.DB().NewQuery(`SELECT slug, title FROM categories WHERE slug = {:slug} LIMIT 1`).WithContext(ctx).
		Bind(dbx.Params{"slug": slug}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Category{}, catalog.ErrNotFound
	}
	if err != nil {
		return catalog.Category{}, fmt.Errorf("catalog: category: %w", err)
	}
	return catalog.Category{Slug: r.Slug, Title: r.Title}, nil
}

// Storefront loads the storefront row by code.
func (c *Catalog) Storefront(ctx context.Context, code string) (storefront.Storefront, error) {
	rec, err := c.App.FindFirstRecordByData("storefronts", "code", code)
	if err != nil {
		return storefront.Storefront{}, fmt.Errorf("storefront %q: %w", code, err)
	}
	brand := struct {
		Name string `json:"name"`
	}{}
	_ = rec.UnmarshalJSONField("brand", &brand)
	return storefront.Storefront{
		Code: rec.GetString("code"), BrandName: brand.Name, Currency: rec.GetString("currency"),
		DefaultLocale: rec.GetString("default_locale"), CommerceMode: rec.GetString("commerce_mode"),
	}, nil
}
