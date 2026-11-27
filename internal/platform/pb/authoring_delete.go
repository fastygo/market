package pb

import (
	"context"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/authoring"
)

// itemUses counts the rows that keep an item alive: order lines (any order
// status), licenses and reviews.
const itemUses = `SELECT
	(SELECT COUNT(*) FROM order_items WHERE product = {:p}) +
	(SELECT COUNT(*) FROM entitlements WHERE product = {:p}) +
	(SELECT COUNT(*) FROM reviews WHERE product = {:p})`

func itemDeletion(ctx context.Context, app core.App, userID, productID string) (authoring.Deletion, error) {
	if err := ensureMember(ctx, app, userID, productID); err != nil {
		return authoring.Deletion{}, err
	}
	rec, err := app.FindRecordById("products", productID)
	if err != nil {
		return authoring.Deletion{}, authoring.ErrNotFound
	}
	if !authoring.Deletable(rec.GetString("status")) {
		return authoring.Deletion{Reason: authoring.DeleteStatus}, nil
	}
	n, err := count(app, itemUses, dbx.Params{"p": productID})
	if err != nil {
		return authoring.Deletion{}, err
	}
	if n > 0 {
		return authoring.Deletion{Reason: authoring.DeleteInUse}, nil
	}
	return authoring.Deletion{Allowed: true}, nil
}

// ItemDeletion implements authoring.Store.
func (a *Authoring) ItemDeletion(ctx context.Context, userID, productID string) (authoring.Deletion, error) {
	return itemDeletion(ctx, a.App, userID, productID)
}

// DeleteItem implements authoring.Store. Everything happens in one
// transaction; PocketBase removes the cover and gallery files after the
// commit and clears the item from editorial collections.
func (a *Authoring) DeleteItem(ctx context.Context, userID, productID string) ([]string, error) {
	var keys []string
	err := a.App.RunInTransaction(func(tx core.App) error {
		d, err := itemDeletion(ctx, tx, userID, productID)
		if err != nil {
			return err
		}
		switch d.Reason {
		case authoring.DeleteStatus:
			return authoring.ErrConflict
		case authoring.DeleteInUse:
			return authoring.ErrInUse
		}
		rels, err := tx.FindRecordsByFilter("product_releases", "product = {:p}", "", 0, 0, dbx.Params{"p": productID})
		if err != nil {
			return err
		}
		for _, r := range rels {
			if k := r.GetString("storage_key"); k != "" {
				keys = append(keys, k)
			}
			if err := tx.Delete(r); err != nil {
				return err
			}
		}
		offers, err := tx.FindRecordsByFilter("offers", "product = {:p}", "", 0, 0, dbx.Params{"p": productID})
		if err != nil {
			return err
		}
		for _, o := range offers {
			if err := tx.Delete(o); err != nil {
				return err
			}
		}
		rec, err := tx.FindRecordById("products", productID)
		if err != nil {
			return authoring.ErrNotFound
		}
		return tx.Delete(rec)
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}
