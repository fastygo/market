package pb

import (
	"context"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/upload"
)

// Releases writes seller release rows (ADR-0022). Not wired to a route
// yet: the seller cabinet (ADR-0028) is still Proposed.
type Releases struct {
	App core.App
}

// NewRelease is a received and checked release file.
type NewRelease struct {
	SellerID, ProductID, Version, StorageKey, SHA256 string
	Size                                             int64
}

// Create checks the seller's storage quota and inserts the draft release
// in one write transaction. BEGIN IMMEDIATE (pb.DBConnect) serializes
// writers, so two parallel uploads of one seller cannot both pass the
// check. Yanked releases still count: their files stay in storage.
func (r *Releases) Create(ctx context.Context, in NewRelease, q upload.Quota) (string, error) {
	var id string
	err := RunTx(ctx, r.App, "release.create", func(ctx context.Context, tx core.App) error {
		var used struct {
			Bytes int64 `db:"bytes"`
			Files int   `db:"files"`
		}
		if err := tx.DB().NewQuery(`SELECT COALESCE(SUM(size), 0) AS bytes, COUNT(*) AS files
			FROM product_releases WHERE seller = {:s}`).Bind(dbx.Params{"s": in.SellerID}).One(&used); err != nil {
			return err
		}
		if used.Files+1 > q.MaxFiles || used.Bytes+in.Size > q.MaxBytes {
			return fmt.Errorf("%w: %d files, %d bytes used of %d files, %d bytes", upload.ErrQuota,
				used.Files, used.Bytes, q.MaxFiles, q.MaxBytes)
		}
		rec, err := newRecord(tx, "product_releases", map[string]any{
			"product": in.ProductID, "seller": in.SellerID, "version": in.Version, "status": "draft",
			"storage_key": in.StorageKey, "size": in.Size, "sha256": in.SHA256,
		})
		if err != nil {
			return err
		}
		id = rec.Id
		return nil
	})
	return id, err
}
