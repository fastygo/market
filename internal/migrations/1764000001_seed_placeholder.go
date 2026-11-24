package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Placeholder seed (ADR-0016): no real legal, personal or contact data.
// The seller is `draft` with empty legal fields; the operator enters real
// data in admin before go-live. Product texts are English placeholders.

func init() {
	m.Register(upSeed, downSeed)
}

type seedProduct struct {
	slug, title, excerpt, category string
	priceMinor                     int64
}

var seedCategories = []struct {
	slug, title string
	sort        int
}{
	{"themes", "Themes", 10},
	{"plugins", "Plugins", 20},
	{"templates", "Site templates", 30},
}

var seedProducts = []seedProduct{
	{"aurora-theme", "Aurora theme", "A fast block theme for stores and blogs.", "themes", 290000},
	{"nordic-theme", "Nordic theme", "A calm theme for portfolios.", "themes", 190000},
	{"seo-toolkit", "SEO toolkit", "Meta tags, sitemaps and schema in one plugin.", "plugins", 149000},
	{"forms-lite", "Forms lite", "Accessible forms without a page builder.", "plugins", 0},
	{"agency-site", "Agency site", "A ready agency site with case studies.", "templates", 490000},
	{"shop-starter", "Shop starter", "A digital shop template with catalog pages.", "templates", 390000},
}

func upSeed(app core.App) error {
	sfCol, err := app.FindCollectionByNameOrId("storefronts")
	if err != nil {
		return err
	}
	sf := core.NewRecord(sfCol)
	sf.Load(map[string]any{
		"code": "default", "commerce_mode": "single_store", "region": "ru",
		"countries": []string{"RU"}, "currency": "RUB", "default_locale": "en",
		"locales": []string{"en"}, "tax_display": "inclusive",
		"modules": map[string]any{}, "enabled_providers": map[string]any{},
		"brand": map[string]any{"name": "GoDudY", "skin": "godudy"}, "order_number_prefix": "GD",
	})
	if err := app.Save(sf); err != nil {
		return err
	}

	selCol, err := app.FindCollectionByNameOrId("sellers")
	if err != nil {
		return err
	}
	seller := core.NewRecord(selCol)
	seller.Load(map[string]any{
		"slug": "godudy", "display_name": "GoDudY", "is_operator": false,
		"status": "draft", "legal_ids": map[string]any{}, "tax_profile": map[string]any{},
	})
	if err := app.Save(seller); err != nil {
		return err
	}

	catCol, err := app.FindCollectionByNameOrId("categories")
	if err != nil {
		return err
	}
	cats := map[string]string{}
	for _, c := range seedCategories {
		r := core.NewRecord(catCol)
		r.Load(map[string]any{"slug": c.slug, "title": c.title, "sort": c.sort})
		if err := app.Save(r); err != nil {
			return err
		}
		cats[c.slug] = r.Id
	}

	prodCol, err := app.FindCollectionByNameOrId("products")
	if err != nil {
		return err
	}
	relCol, err := app.FindCollectionByNameOrId("product_releases")
	if err != nil {
		return err
	}
	offCol, err := app.FindCollectionByNameOrId("offers")
	if err != nil {
		return err
	}
	for _, p := range seedProducts {
		pr := core.NewRecord(prodCol)
		pr.Load(map[string]any{
			"seller": seller.Id, "slug": p.slug, "product_type": "plain",
			"category": cats[p.category], "title": p.title, "excerpt": p.excerpt,
			"description": p.excerpt + "\n\nPlaceholder description for the phase 0 skeleton.",
			"status":      "published", "demo_url": "https://example.com/demo/" + p.slug,
		})
		if err := app.Save(pr); err != nil {
			return err
		}
		rl := core.NewRecord(relCol)
		rl.Load(map[string]any{"product": pr.Id, "seller": seller.Id, "version": "1.0.0", "status": "draft"})
		if err := app.Save(rl); err != nil {
			return err
		}
		of := core.NewRecord(offCol)
		of.Load(map[string]any{
			"product": pr.Id, "seller": seller.Id, "license_type": "regular",
			"price_minor": p.priceMinor, "currency": "RUB", "tax_category": "standard",
			"support_months": 6, "active": true,
		})
		if err := app.Save(of); err != nil {
			return err
		}
	}
	return nil
}

func downSeed(app core.App) error {
	for _, name := range []string{"offers", "product_releases", "products", "categories", "sellers", "storefronts"} {
		if _, err := app.DB().NewQuery("DELETE FROM " + name).Execute(); err != nil {
			return err
		}
	}
	return nil
}
