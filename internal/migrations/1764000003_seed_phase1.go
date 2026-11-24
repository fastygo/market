package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Phase 1 placeholder seed (ADR-0016): the operator seller sells as
// principal and is marked as a VAT payer (placeholder tax profile, no legal
// ids); seed releases are published with placeholder storage keys so the
// download path can be exercised. Real values are entered in admin.

func init() {
	m.Register(upSeedPhase1, nil)
}

func upSeedPhase1(app core.App) error {
	seller, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		return err
	}
	seller.Set("is_operator", true)
	seller.Set("sale_model", "principal")
	seller.Set("tax_profile", map[string]any{"vat_payer": true})
	if err := app.Save(seller); err != nil {
		return err
	}
	sf, err := app.FindFirstRecordByData("storefronts", "code", "default")
	if err != nil {
		return err
	}
	sf.Set("enabled_providers", map[string]any{"card": "fake"})
	if err := app.Save(sf); err != nil {
		return err
	}
	rels, err := app.FindAllRecords("product_releases")
	if err != nil {
		return err
	}
	for _, r := range rels {
		p, err := app.FindRecordById("products", r.GetString("product"))
		if err != nil {
			return err
		}
		r.Set("storage_key", "placeholder/"+p.GetString("slug")+"-"+r.GetString("version")+".zip")
		r.Set("status", "published")
		r.Set("released_at", "2026-11-24 00:00:00.000Z")
		if err := app.Save(r); err != nil {
			return err
		}
	}
	return nil
}
