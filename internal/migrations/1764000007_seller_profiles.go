package migrations

import (
	_ "embed"
	"encoding/json"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Seller profiles (ADR-0020, slice 1): public profile fields on `sellers`,
// the lifecycle statuses of ADR-0020 and GoDudY as an active seller with a
// pseudonymous public profile. Idempotent and safe on an existing
// database:
//   - a field is added only when missing; status values are only added;
//   - GoDudY becomes `active` only from `draft` (a suspended or closed
//     seller stays as is); headline, bio and joined_at are filled only when
//     empty; display_name is never changed once set;
//   - products whose seller is empty or points to a missing row are
//     linked to GoDudY (ADR-0005 `seller` column); products of other
//     sellers are never touched.
// No legal or KYC data is written (ADR-0016): legal_name, legal_ids and
// tax_profile stay as they are.

//go:embed seed/godudy-seller.json
var godudySellerJSON []byte

func init() {
	m.Register(upSellerProfiles, nil)
}

var sellerStatuses = []string{"draft", "pending_review", "applied", "active", "suspended", "closed", "rejected"}

func upSellerProfiles(app core.App) error {
	col, err := app.FindCollectionByNameOrId("sellers")
	if err != nil {
		return err
	}
	add := func(f core.Field) {
		if col.Fields.GetByName(f.GetName()) == nil {
			col.Fields.Add(f)
		}
	}
	add(&core.TextField{Name: "headline", Max: 160})
	add(&core.TextField{Name: "bio", Max: 5000}) // plain text, paragraphs on blank lines
	add(&core.FileField{Name: "avatar", MaxSelect: 1, MaxSize: 2 << 20, MimeTypes: []string{"image/jpeg", "image/png", "image/webp"}})
	add(&core.URLField{Name: "website"})
	add(&core.DateField{Name: "joined_at"})
	if st, ok := col.Fields.GetByName("status").(*core.SelectField); ok {
		have := map[string]bool{}
		for _, v := range st.Values {
			have[v] = true
		}
		for _, v := range sellerStatuses {
			if !have[v] {
				st.Values = append(st.Values, v)
			}
		}
	}
	if err := app.Save(col); err != nil {
		return err
	}

	var seed struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
		Headline    string `json:"headline"`
		Bio         string `json:"bio"`
	}
	if err := json.Unmarshal(godudySellerJSON, &seed); err != nil {
		return err
	}

	seller, err := app.FindFirstRecordByData("sellers", "slug", seed.Slug)
	if err != nil {
		return nil // no GoDudY row (another deployment): nothing to do
	}
	if seller.GetString("status") == "draft" {
		seller.Set("status", "active")
	}
	if seller.GetString("display_name") == "" {
		seller.Set("display_name", seed.DisplayName)
	}
	if seller.GetString("headline") == "" {
		seller.Set("headline", seed.Headline)
	}
	if seller.GetString("bio") == "" {
		seller.Set("bio", seed.Bio)
	}
	if seller.GetDateTime("joined_at").IsZero() {
		joined := seller.GetDateTime("created")
		if joined.IsZero() {
			seller.Set("joined_at", time.Now().UTC())
		} else {
			seller.Set("joined_at", joined)
		}
	}
	if err := app.Save(seller); err != nil {
		return err
	}
	_, err = app.DB().NewQuery(`UPDATE products SET seller = {:s}
		WHERE seller = '' OR seller IS NULL OR seller NOT IN (SELECT id FROM sellers)`).
		Bind(dbx.Params{"s": seller.Id}).Execute()
	return err
}
