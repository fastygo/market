package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"github.com/fastygo/market/internal/content"
)

func init() {
	m.Register(upDescriptionHTML, nil)
}

// upDescriptionHTML is expand-only (ADR-0031): raise the Markdown cap,
// add description_html and description_version, add a public images
// field, and create seller_members. Existing description bodies are
// rendered into description_html when that column is still empty.
func upDescriptionHTML(app core.App) error {
	products, err := app.FindCollectionByNameOrId("products")
	if err != nil {
		return err
	}
	if f, ok := products.Fields.GetByName("description").(*core.TextField); ok {
		if f.Max < content.MaxMarkdown {
			f.Max = content.MaxMarkdown
		}
	}
	addProduct := func(f core.Field) {
		if products.Fields.GetByName(f.GetName()) == nil {
			products.Fields.Add(f)
		}
	}
	addProduct(&core.TextField{Name: "description_html", Max: 1_000_000})
	addProduct(&core.NumberField{Name: "description_version", OnlyInt: true, Min: ptr(0)})
	addProduct(&core.FileField{
		Name:      "images",
		MaxSelect: 50,
		MaxSize:   5 << 20,
		MimeTypes: []string{"image/jpeg", "image/png", "image/webp", "image/gif"},
	})
	if err := app.Save(products); err != nil {
		return err
	}

	if _, err := app.FindCollectionByNameOrId("seller_members"); err != nil {
		sellers, err := app.FindCollectionByNameOrId("sellers")
		if err != nil {
			return err
		}
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		members := core.NewBaseCollection("seller_members")
		members.Fields.Add(
			rel("seller", sellers, true),
			rel("user", users, true),
			sel("role", true, "owner", "staff"),
		)
		timestamps(members)
		members.AddIndex("idx_seller_members_seller_user", true, "seller, user", "")
		members.AddIndex("idx_seller_members_user", false, "user", "")
		if err := app.Save(members); err != nil {
			return err
		}
	}

	records, err := app.FindAllRecords("products")
	if err != nil {
		return fmt.Errorf("description_html backfill: list: %w", err)
	}
	for _, rec := range records {
		if rec.GetString("description_html") != "" {
			continue
		}
		src := rec.GetString("description")
		if src == "" {
			continue
		}
		html, err := content.Render(src)
		if err != nil {
			// Keep the plain text; the cabinet can fix the body later.
			continue
		}
		rec.Set("description_html", html)
		if err := app.Save(rec); err != nil {
			return fmt.Errorf("description_html backfill %s: %w", rec.Id, err)
		}
	}
	return nil
}
