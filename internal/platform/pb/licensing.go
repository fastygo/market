package pb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/ordering"
)

// Downloads implements licensing.Store on PocketBase.
type Downloads struct {
	App core.App
}

var _ licensing.Store = (*Downloads)(nil)

type targetRow struct {
	Ent     string `db:"ent"`
	Rel     string `db:"rel"`
	Key     string `db:"storage_key"`
	Version string `db:"version"`
}

func (t targetRow) target() licensing.Target {
	return licensing.Target{EntitlementID: t.Ent, ReleaseID: t.Rel, StorageKey: t.Key, Version: t.Version}
}

// DownloadTarget implements licensing.Store.
func (d *Downloads) DownloadTarget(ctx context.Context, userID, slug string) (licensing.Target, error) {
	var r targetRow
	err := d.App.DB().NewQuery(`SELECT e.id AS ent, r.id AS rel, r.storage_key, r.version
		FROM entitlements e
		JOIN products p ON p.id = e.product
		JOIN product_releases r ON r.product = p.id AND r.status = 'published' AND r.storage_key != ''
		WHERE e.user = {:u} AND p.slug = {:s} AND e.status = 'active'
		ORDER BY r.released_at DESC, r.created DESC LIMIT 1`).Bind(dbx.Params{"u": userID, "s": slug}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return licensing.Target{}, licensing.ErrNoEntitlement
	}
	return r.target(), err
}

// Recheck implements licensing.Store.
func (d *Downloads) Recheck(ctx context.Context, ent, rel string) (licensing.Target, error) {
	var r targetRow
	err := d.App.DB().NewQuery(`SELECT e.id AS ent, r.id AS rel, r.storage_key, r.version
		FROM entitlements e JOIN product_releases r ON r.product = e.product
		WHERE e.id = {:e} AND r.id = {:r} AND e.status = 'active' AND r.status = 'published' AND r.storage_key != ''`).
		Bind(dbx.Params{"e": ent, "r": rel}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return licensing.Target{}, licensing.ErrNoEntitlement
	}
	return r.target(), err
}

// LogDownload implements licensing.Store.
func (d *Downloads) LogDownload(ctx context.Context, t licensing.Target, ipHash, uaHash string, at time.Time) error {
	return RunTx(ctx, d.App, "download.log", func(ctx context.Context, app core.App) error {
		_, err := newRecord(app, "downloads", map[string]any{
			"entitlement": t.EntitlementID, "release": t.ReleaseID, "at": pbDate(at), "ip_hash": ipHash, "ua_hash": uaHash,
		})
		return err
	})
}

// OfferFor implements ordering.Store: the active offer of a published
// product for a license type and currency.
func (s *Orders) OfferFor(ctx context.Context, productSlug, licenseType, currency string) (string, error) {
	var id string
	err := s.App.DB().NewQuery(`SELECT o.id FROM offers o JOIN products p ON p.id = o.product
		WHERE p.slug = {:s} AND p.status = 'published' AND o.license_type = {:l} AND o.currency = {:c} AND o.active = TRUE`).
		Bind(dbx.Params{"s": productSlug, "l": licenseType, "c": currency}).Row(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ordering.ErrNotFound
	}
	return id, err
}
