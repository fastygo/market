// Package pb holds the PocketBase adapters: repositories implementing the
// interfaces declared by domain packages (ADR-0002, ADR-0014).
package pb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

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

// One query, no N+1: the lowest active offer in the storefront currency
// is a correlated subquery, evaluated only for the rows returned.
//
// `products p CROSS JOIN sellers s` pins products as the outer loop
// (SQLite treats CROSS JOIN as a join-order hint): with the stale planner
// stats a bulk import leaves until the nightly PRAGMA optimize, SQLite
// otherwise picked `SCAN sellers` x `SCAN products` and sorted the whole
// catalog (2.4 s per page at 10k products, load test 2026-11-25). Filters
// on category and seller compare ids (slug resolved by an uncorrelated
// subquery) so the composite indexes of migration 1764000008 apply.
const productSelect = `
SELECT p.id, p.slug, p.title, p.excerpt, p.description, p.product_type, p.demo_url, p.cover,
       c.slug AS cat_slug, c.title AS cat_title,
       s.display_name AS seller_name, s.slug AS seller_slug,
       (SELECT MIN(o.price_minor) FROM offers o
         WHERE o.product = p.id AND o.active = TRUE AND o.currency = {:currency}) AS price_minor
FROM products p
CROSS JOIN sellers s ON s.id = p.seller
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

// listSelect is productSelect without the description (up to 20 KB a
// row; only the product page shows it), so list pages do not read or
// sort it.
var listSelect = strings.Replace(productSelect, "p.description,", "'' AS description,", 1)

// Products lists published products, newest first.
func (c *Catalog) Products(ctx context.Context, currency string, f catalog.Filter) ([]catalog.Product, error) {
	q := listSelect
	params := dbx.Params{"currency": currency}
	if f.Category != "" {
		q += ` AND p.category = (SELECT id FROM categories WHERE slug = {:cat})`
		params["cat"] = f.Category
	}
	if f.Seller != "" {
		q += ` AND p.seller = (SELECT id FROM sellers WHERE slug = {:seller})`
		params["seller"] = f.Seller
	}
	if f.Exclude != "" {
		q += ` AND p.slug != {:exclude}`
		params["exclude"] = f.Exclude
	}
	if words, sub := searchMatch(f.Query); strings.TrimSpace(f.Query) != "" && words == "" && sub == "" {
		q += ` AND 0` // only punctuation: nothing to search for
	} else if words != "" || sub != "" {
		// FTS5 (migration 1764000009): word prefixes OR a substring
		var parts []string
		if words != "" {
			parts = append(parts, `SELECT id FROM products_fts WHERE products_fts MATCH {:qw}`)
			params["qw"] = words
		}
		if sub != "" {
			parts = append(parts, `SELECT id FROM products_fts_tri WHERE products_fts_tri MATCH {:qs}`)
			params["qs"] = sub
		}
		q += ` AND p.id IN (` + strings.Join(parts, " UNION ") + `)`
	}
	q += ` ORDER BY p.published_at DESC, p.created DESC`
	limit := f.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	q += fmt.Sprintf(` LIMIT %d`, limit)
	if f.Offset > 0 && f.Offset <= catalog.PageSize*(catalog.MaxPage-1) {
		q += fmt.Sprintf(` OFFSET %d`, f.Offset)
	}
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
	var brand storefront.Brand
	_ = rec.UnmarshalJSONField("brand", &brand)
	return storefront.Storefront{
		Code: rec.GetString("code"), BrandName: brand.Name, Skin: brand.Skin, Currency: rec.GetString("currency"),
		DefaultLocale: rec.GetString("default_locale"), CommerceMode: rec.GetString("commerce_mode"), Brand: brand,
	}, nil
}

// searchMatch builds the FTS5 queries for a storefront search: words is
// every token as a quoted prefix term ("dash"* "кабин"*, all must match,
// any order) for products_fts; sub is the whole query as one quoted
// phrase for the trigram table (a substring, like LIKE '%q%'), only when
// it has 3+ characters (trigram cannot match shorter strings). Quotes are
// doubled, so user input is never FTS5 syntax.
func searchMatch(q string) (words, sub string) {
	q = strings.ToValidUTF8(strings.TrimSpace(q), "")
	if r := []rune(q); len(r) > 100 {
		q = string(r[:100])
	}
	if q == "" {
		return "", ""
	}
	quote := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
	var terms []string
	for _, tok := range strings.Fields(q) {
		if strings.IndexFunc(tok, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
			continue // punctuation only: no token for unicode61
		}
		terms = append(terms, quote(tok)+"*")
	}
	words = strings.Join(terms, " ")
	if utf8.RuneCountInString(q) >= 3 {
		sub = quote(q)
	}
	return words, sub
}

// Seller loads an active seller's public profile by slug (ADR-0020); other
// statuses are not public yet and read as not found.
func (c *Catalog) Seller(ctx context.Context, slug string) (catalog.Seller, error) {
	var r struct {
		ID          string `db:"id"`
		Slug        string `db:"slug"`
		DisplayName string `db:"display_name"`
		Headline    string `db:"headline"`
		Bio         string `db:"bio"`
		Avatar      string `db:"avatar"`
		Website     string `db:"website"`
		JoinedAt    string `db:"joined_at"`
		Items       int    `db:"items"`
	}
	err := c.App.DB().NewQuery(`SELECT s.id, s.slug, s.display_name, s.headline, s.bio, s.avatar, s.website, s.joined_at,
		(SELECT COUNT(*) FROM products p WHERE p.seller = s.id AND p.status = 'published') AS items
		FROM sellers s WHERE s.slug = {:slug} AND s.status = 'active' LIMIT 1`).WithContext(ctx).
		Bind(dbx.Params{"slug": slug}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Seller{}, catalog.ErrNotFound
	}
	if err != nil {
		return catalog.Seller{}, fmt.Errorf("catalog: seller: %w", err)
	}
	out := catalog.Seller{ID: r.ID, Slug: r.Slug, DisplayName: r.DisplayName, Headline: r.Headline, Bio: r.Bio,
		Website: r.Website, ItemsCount: r.Items}
	if dt, err := types.ParseDateTime(r.JoinedAt); err == nil {
		out.JoinedAt = dt.Time()
	}
	if r.Avatar != "" {
		if col, err := c.App.FindCachedCollectionByNameOrId("sellers"); err == nil {
			out.AvatarURL = "/api/files/" + col.Id + "/" + r.ID + "/" + r.Avatar
		}
	}
	return out, nil
}
