package migrations

import (
	_ "embed"
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// StorEEE brand step (ADR-0020, owner t237u): the storefront brand becomes
// StorEEE Market with the `storeee` skin; GoDudY is a seller, not the
// store. Data only, idempotent and safe on an existing database: a
// storefront is changed only while it still carries the seed brand
// (name "GoDudY"); a brand an operator has already edited is left alone.
// Other brand keys and every other column are kept.

func init() {
	m.Register(upStoreeeBrand, nil)
}

// storeeeBrandJSON holds the seed brand values as data (seed/), written
// once by this migration; the code reads the brand only from the
// storefront row.
//
//go:embed seed/storeee-brand.json
var storeeeBrandJSON []byte

type brandSeed struct {
	Legacy struct {
		Name string `json:"name"`
		Skin string `json:"skin"`
	} `json:"legacy"`
	Brand map[string]any `json:"brand"`
}

func upStoreeeBrand(app core.App) error {
	var seed brandSeed
	if err := json.Unmarshal(storeeeBrandJSON, &seed); err != nil {
		return err
	}
	legacyBrandName, legacyBrandSkin, storeeeBrand := seed.Legacy.Name, seed.Legacy.Skin, seed.Brand
	sfs, err := app.FindAllRecords("storefronts")
	if err != nil {
		return err
	}
	for _, sf := range sfs {
		brand := map[string]any{}
		_ = sf.UnmarshalJSONField("brand", &brand)
		if name, _ := brand["name"].(string); name != legacyBrandName {
			continue
		}
		for k, v := range storeeeBrand {
			switch k {
			case "name":
				brand[k] = v
			case "skin":
				if s, _ := brand[k].(string); s == "" || s == legacyBrandSkin {
					brand[k] = v
				}
			default:
				if s, _ := brand[k].(string); s == "" {
					brand[k] = v
				}
			}
		}
		sf.Set("brand", brand)
		if err := app.Save(sf); err != nil {
			return err
		}
	}
	return nil
}
