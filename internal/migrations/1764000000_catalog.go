// Package migrations holds the Go migrations (ADR-0014). Collections have
// nil API rules (superuser only); the storefront reads them server-side.
package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(upCatalog, downCatalog)
}

// catalogCollections in creation order (relations point backwards).
var catalogCollections = []string{"storefronts", "sellers", "categories", "products", "product_releases", "offers"}

func text(name string, required bool) *core.TextField {
	return &core.TextField{Name: name, Required: required, Max: 2000}
}

func sel(name string, required bool, values ...string) *core.SelectField {
	return &core.SelectField{Name: name, Required: required, MaxSelect: 1, Values: values}
}

func rel(name string, to *core.Collection, required bool) *core.RelationField {
	return &core.RelationField{Name: name, CollectionId: to.Id, MaxSelect: 1, Required: required}
}

func timestamps(c *core.Collection) {
	c.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	c.Fields.Add(&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
}

func upCatalog(app core.App) error {
	storefronts := core.NewBaseCollection("storefronts")
	storefronts.Fields.Add(
		text("code", true),
		&core.JSONField{Name: "hosts"},
		sel("commerce_mode", true, "single_store", "marketplace"),
		text("region", true),
		&core.JSONField{Name: "countries"},
		text("currency", true),
		text("default_locale", true),
		&core.JSONField{Name: "locales"},
		sel("tax_display", true, "inclusive", "exclusive"),
		&core.JSONField{Name: "modules"},
		&core.JSONField{Name: "enabled_providers"},
		&core.JSONField{Name: "brand"},
		text("order_number_prefix", false),
	)
	timestamps(storefronts)
	storefronts.AddIndex("idx_storefronts_code", true, "code", "")
	if err := app.Save(storefronts); err != nil {
		return err
	}

	sellers := core.NewBaseCollection("sellers")
	sellers.Fields.Add(
		text("slug", true),
		text("display_name", true),
		&core.BoolField{Name: "is_operator"},
		text("country", false),
		text("legal_name", false),
		&core.JSONField{Name: "legal_ids"},
		&core.JSONField{Name: "tax_profile"},
		sel("sale_model", false, "principal", "agent"),
		&core.NumberField{Name: "commission_bps", OnlyInt: true},
		sel("status", true, "draft", "pending_review", "active", "suspended"),
		text("contract_version", false),
	)
	timestamps(sellers)
	sellers.AddIndex("idx_sellers_slug", true, "slug", "")
	if err := app.Save(sellers); err != nil {
		return err
	}

	categories := core.NewBaseCollection("categories", categoriesID)
	categories.Fields.Add(
		text("slug", true),
		text("title", true),
		&core.NumberField{Name: "sort", OnlyInt: true},
		&core.JSONField{Name: "attribute_schema"},
	)
	timestamps(categories)
	categories.AddIndex("idx_categories_slug", true, "slug", "")
	if err := app.Save(categories); err != nil {
		return err
	}
	// the self relation can only be validated once the collection exists
	categories.Fields.Add(&core.RelationField{Name: "parent", CollectionId: categoriesID, MaxSelect: 1})
	if err := app.Save(categories); err != nil {
		return err
	}

	products := core.NewBaseCollection("products")
	products.Fields.Add(
		rel("seller", sellers, true),
		text("slug", true),
		&core.TextField{Name: "product_type", Required: true, Max: 64},
		rel("category", categories, false),
		text("title", true),
		text("excerpt", false),
		&core.TextField{Name: "description", Max: 20000}, // plain text in phase 0; rich content as Codex blocks later
		sel("status", true, "draft", "in_review", "published", "unlisted", "archived"),
		&core.FileField{Name: "cover", MaxSelect: 1, MaxSize: 5 << 20, MimeTypes: []string{"image/jpeg", "image/png", "image/webp"}},
		&core.URLField{Name: "demo_url"},
		&core.JSONField{Name: "attributes"},
		&core.NumberField{Name: "rating_avg"},
		&core.NumberField{Name: "rating_count", OnlyInt: true},
		&core.NumberField{Name: "sales_count", OnlyInt: true},
		&core.DateField{Name: "published_at"},
	)
	timestamps(products)
	products.AddIndex("idx_products_slug", true, "slug", "")
	products.AddIndex("idx_products_seller", false, "seller", "")
	products.AddIndex("idx_products_status_category", false, "status, category", "")
	if err := app.Save(products); err != nil {
		return err
	}

	releases := core.NewBaseCollection("product_releases")
	releases.Fields.Add(
		rel("product", products, true),
		rel("seller", sellers, true),
		text("version", true),
		&core.EditorField{Name: "changelog"},
		text("storage_key", false),
		&core.NumberField{Name: "size", OnlyInt: true},
		text("sha256", false),
		sel("status", true, "draft", "published", "yanked"),
		&core.DateField{Name: "released_at"},
	)
	timestamps(releases)
	releases.AddIndex("idx_releases_product_version", true, "product, version", "")
	releases.AddIndex("idx_releases_seller", false, "seller", "")
	if err := app.Save(releases); err != nil {
		return err
	}

	offers := core.NewBaseCollection("offers")
	offers.Fields.Add(
		rel("product", products, true),
		rel("seller", sellers, true),
		text("license_type", true),
		&core.NumberField{Name: "price_minor", OnlyInt: true, Required: false, Min: ptr(0), Max: ptr(float64(1<<53 - 1))},
		text("currency", true),
		text("tax_category", true),
		&core.NumberField{Name: "support_months", OnlyInt: true},
		&core.BoolField{Name: "active"},
	)
	timestamps(offers)
	offers.AddIndex("idx_offers_product_license_currency", true, "product, license_type, currency", "")
	offers.AddIndex("idx_offers_seller", false, "seller", "")
	return app.Save(offers)
}

// categoriesID is fixed so the self relation `parent` can reference it.
const categoriesID = "pbc_categories01"

func ptr(v float64) *float64 { return &v }

func downCatalog(app core.App) error {
	for i := len(catalogCollections) - 1; i >= 0; i-- {
		c, err := app.FindCollectionByNameOrId(catalogCollections[i])
		if err != nil {
			continue
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
