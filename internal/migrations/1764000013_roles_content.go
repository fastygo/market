package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"github.com/fastygo/market/internal/content"
)

func init() {
	m.Register(upRolesContent, nil)
}

// Seed pages (ADR-0035): placeholders the editor replaces in the cabinet.
// English only in the repo (ADR-0016); the owner writes the real texts.
var seedPages = []struct{ slug, title, kind string }{
	{"terms", "Terms of use", "legal"},
	{"privacy", "Privacy policy", "legal"},
	{"offer", "Public offer", "legal"},
	{"refund", "Refund policy", "legal"},
	{"licenses", "Licenses", "legal"},
	{"about", "About the market", "info"},
	{"help", "Help for buyers", "info"},
	{"sell", "Sell on the market", "info"},
	{"rules", "Publishing rules", "info"},
	{"contacts", "Contacts", "info"},
}

const seedPageBody = "This page is a draft. The editor replaces this text in the cabinet (Editor, Pages)."

// upRolesContent is expand-only (ADR-0035): new collections for cabinet
// roles, applications, partners and payouts, editorial content, reviews
// and database locales, plus orders.referral (ADR-0023) and
// products.review_note. No existing field changes; every collection is
// server-only (all API rules nil: the SSR cabinet is the only writer).
func upRolesContent(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	sellers, err := app.FindCollectionByNameOrId("sellers")
	if err != nil {
		return err
	}
	products, err := app.FindCollectionByNameOrId("products")
	if err != nil {
		return err
	}
	orders, err := app.FindCollectionByNameOrId("orders")
	if err != nil {
		return err
	}
	create := func(name string, build func(c *core.Collection)) (*core.Collection, error) {
		if c, err := app.FindCollectionByNameOrId(name); err == nil {
			return c, nil
		}
		c := core.NewBaseCollection(name)
		build(c)
		timestamps(c)
		return c, app.Save(c)
	}
	img := func(name string) *core.FileField {
		return &core.FileField{Name: name, MaxSelect: 1, MaxSize: 5 << 20, MimeTypes: []string{"image/jpeg", "image/png", "image/webp"}}
	}

	if _, err := create("user_roles", func(c *core.Collection) {
		c.Fields.Add(rel("user", users, true), sel("role", true, "editor", "author"), sel("status", true, "active", "suspended"),
			text("granted_by", false), text("note", false))
		c.AddIndex("idx_user_roles_user_role", true, "user, role", "")
	}); err != nil {
		return err
	}
	if _, err := create("role_applications", func(c *core.Collection) {
		c.Fields.Add(rel("user", users, true), sel("role", true, "author", "vendor", "partner"),
			sel("status", true, "pending", "approved", "rejected", "withdrawn"),
			text("message", false), text("store_name", false), text("store_slug", false), &core.URLField{Name: "website"},
			text("decided_by", false), &core.DateField{Name: "decided_at"}, text("decision_note", false))
		c.AddIndex("idx_role_applications_user_role", false, "user, role", "")
		c.AddIndex("idx_role_applications_status", false, "status", "")
	}); err != nil {
		return err
	}
	partners, err := create("partners", func(c *core.Collection) {
		c.Fields.Add(rel("user", users, true), &core.TextField{Name: "code", Required: true, Max: 32, Pattern: `^[a-z0-9][a-z0-9-]{2,31}$`},
			&core.JSONField{Name: "level_path"}, sel("status", true, "active", "suspended", "closed"),
			&core.NumberField{Name: "rate_bps", OnlyInt: true, Min: ptr(0), Max: ptr(10000)}, text("payout_method", false))
		c.AddIndex("idx_partners_user", true, "user", "")
		c.AddIndex("idx_partners_code", true, "code", "")
	})
	if err != nil {
		return err
	}
	if partners.Fields.GetByName("parent") == nil {
		partners.Fields.Add(&core.RelationField{Name: "parent", CollectionId: partners.Id, MaxSelect: 1})
		if err := app.Save(partners); err != nil {
			return err
		}
	}
	if _, err := create("referral_clicks", func(c *core.Collection) {
		c.Fields.Add(rel("partner", partners, true), text("landing", false), text("ip_hash", false))
		c.AddIndex("idx_referral_clicks_partner_created", false, "partner, created", "")
	}); err != nil {
		return err
	}
	if _, err := create("payouts", func(c *core.Collection) {
		c.Fields.Add(sel("party_type", true, "seller", "partner"), rel("seller", sellers, false), rel("partner", partners, false),
			&core.NumberField{Name: "amount_minor", OnlyInt: true, Min: ptr(0)}, text("currency", true),
			sel("status", true, "requested", "approved", "paid", "rejected", "cancelled"),
			rel("requested_by", users, false), text("period", false), text("reference", false), text("note", false),
			&core.DateField{Name: "paid_at"})
		c.AddIndex("idx_payouts_seller", false, "seller", "")
		c.AddIndex("idx_payouts_partner", false, "partner", "")
	}); err != nil {
		return err
	}
	if _, err := create("articles", func(c *core.Collection) {
		c.Fields.Add(text("slug", true), text("title", true), text("excerpt", false),
			&core.TextField{Name: "body", Max: content.MaxMarkdown}, &core.TextField{Name: "body_html", Max: 1_000_000}, img("cover"),
			rel("author", users, true), rel("seller", sellers, false), sel("kind", true, "article", "news", "guide"),
			sel("status", true, "draft", "in_review", "published", "rejected", "archived"),
			&core.DateField{Name: "published_at"}, text("review_note", false), &core.NumberField{Name: "version", OnlyInt: true, Min: ptr(0)})
		c.AddIndex("idx_articles_slug", true, "slug", "")
		c.AddIndex("idx_articles_status_published", false, "status, published_at", "")
		c.AddIndex("idx_articles_author", false, "author", "")
	}); err != nil {
		return err
	}
	pages, err := create("pages", func(c *core.Collection) {
		c.Fields.Add(text("slug", true), text("title", true), &core.TextField{Name: "body", Max: content.MaxMarkdown},
			&core.TextField{Name: "body_html", Max: 1_000_000}, sel("kind", true, "legal", "info"),
			text("version", false), sel("status", true, "draft", "published"), text("updated_by", false))
		c.AddIndex("idx_pages_slug", true, "slug", "")
	})
	if err != nil {
		return err
	}
	for _, p := range seedPages {
		if _, err := app.FindFirstRecordByData("pages", "slug", p.slug); err == nil {
			continue
		}
		r := core.NewRecord(pages)
		html, _ := content.Render(seedPageBody)
		r.Load(map[string]any{"slug": p.slug, "title": p.title, "body": seedPageBody, "body_html": html,
			"kind": p.kind, "version": "1", "status": "published", "updated_by": "seed"})
		if err := app.Save(r); err != nil {
			return err
		}
	}
	if _, err := create("collections", func(c *core.Collection) {
		c.Fields.Add(text("slug", true), text("title", true), text("eyebrow", false), text("lead", false), text("cta", false), img("image"),
			sel("tone", false, "muted", "accent"),
			&core.RelationField{Name: "products", CollectionId: products.Id, MaxSelect: 200},
			sel("status", true, "draft", "published"), &core.NumberField{Name: "sort", OnlyInt: true})
		c.AddIndex("idx_collections_slug", true, "slug", "")
	}); err != nil {
		return err
	}
	if _, err := create("reviews", func(c *core.Collection) {
		c.Fields.Add(rel("product", products, true), rel("user", users, true), rel("order", orders, false),
			&core.NumberField{Name: "rating", OnlyInt: true, Min: ptr(1), Max: ptr(5), Required: true},
			&core.TextField{Name: "body", Max: 5000}, sel("status", true, "pending", "published", "rejected"),
			&core.TextField{Name: "reply", Max: 5000}, &core.DateField{Name: "reply_at"}, text("author_name", false))
		c.AddIndex("idx_reviews_product_user", true, "product, user", "")
		c.AddIndex("idx_reviews_status", false, "status", "")
	}); err != nil {
		return err
	}
	if _, err := create("ui_messages", func(c *core.Collection) {
		c.Fields.Add(&core.TextField{Name: "locale", Required: true, Max: 16}, &core.TextField{Name: "key", Required: true, Max: 200},
			&core.TextField{Name: "value", Max: 5000})
		c.AddIndex("idx_ui_messages_locale_key", true, "locale, `key`", "")
	}); err != nil {
		return err
	}
	if orders.Fields.GetByName("referral") == nil {
		orders.Fields.Add(&core.JSONField{Name: "referral"})
		if err := app.Save(orders); err != nil {
			return err
		}
	}
	if products.Fields.GetByName("review_note") == nil {
		products.Fields.Add(text("review_note", false))
		if err := app.Save(products); err != nil {
			return err
		}
	}
	return nil
}
