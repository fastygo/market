package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(upReleaseReview, nil)
}

// ReleaseStatuses of product_releases after ADR-0036: a vendor upload
// starts in review; only a moderator publishes or rejects it. draft stays
// valid for rows written before this migration and for the demo seed.
var ReleaseStatuses = []string{"draft", "review", "published", "rejected", "yanked"}

// upReleaseReview is expand-only (ADR-0036): product_releases gets the
// review and rejected statuses, the moderator decision (review_note,
// reviewed_by, reviewed_at), the archive report written at upload
// (manifest: entries, sizes, flags) and flag_count for the queue. Drafts
// that waited for a self-publish move to review. Idempotent.
func upReleaseReview(app core.App) error {
	c, err := app.FindCollectionByNameOrId("product_releases")
	if err != nil {
		return err
	}
	if f, ok := c.Fields.GetByName("status").(*core.SelectField); ok {
		f.Values = ReleaseStatuses
	}
	if c.Fields.GetByName("review_note") == nil {
		c.Fields.Add(text("review_note", false))
	}
	if c.Fields.GetByName("reviewed_by") == nil {
		c.Fields.Add(text("reviewed_by", false))
	}
	if c.Fields.GetByName("reviewed_at") == nil {
		c.Fields.Add(&core.DateField{Name: "reviewed_at"})
	}
	if c.Fields.GetByName("manifest") == nil {
		c.Fields.Add(&core.JSONField{Name: "manifest", MaxSize: 1 << 20})
	}
	if c.Fields.GetByName("flag_count") == nil {
		c.Fields.Add(&core.NumberField{Name: "flag_count", OnlyInt: true})
	}
	c.AddIndex("idx_releases_status", false, "status", "")
	if err := app.Save(c); err != nil {
		return err
	}
	_, err = app.DB().NewQuery("UPDATE product_releases SET status = 'review' WHERE status = 'draft' AND storage_key != ''").Execute()
	return err
}
