package pb

import (
	"context"
	"errors"
	"fmt"
	"html"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/upload"
)

// Releases writes seller release rows (ADR-0022) for the vendor cabinet
// (ADR-0035, POST /seller/items/{id}/releases).
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

var releaseVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)

// ReleaseOwner implements authoring.Releases.
func (r *Releases) ReleaseOwner(ctx context.Context, userID, itemID string) (string, error) {
	if err := ensureMember(ctx, r.App, userID, itemID); err != nil {
		return "", err
	}
	p, err := r.App.FindRecordById("products", itemID)
	if err != nil {
		return "", authoring.ErrNotFound
	}
	return p.GetString("seller"), nil
}

// ItemReleases implements authoring.Releases (newest first).
func (r *Releases) ItemReleases(ctx context.Context, userID, itemID string) ([]authoring.Release, error) {
	if err := ensureMember(ctx, r.App, userID, itemID); err != nil {
		return nil, err
	}
	recs, err := r.App.FindRecordsByFilter("product_releases", "product = {:p}", "-created", 50, 0, dbx.Params{"p": itemID})
	if err != nil {
		return nil, err
	}
	out := make([]authoring.Release, 0, len(recs))
	for _, rec := range recs {
		out = append(out, releaseOf(rec))
	}
	return out, nil
}

func releaseOf(rec *core.Record) authoring.Release {
	return authoring.Release{ID: rec.Id, Version: rec.GetString("version"), Status: rec.GetString("status"),
		Changelog: rec.GetString("changelog"), Name: path.Base(rec.GetString("storage_key")), Size: int64(rec.GetInt("size")),
		Created: rec.GetDateTime("created").Time()}
}

// AddRelease implements authoring.Releases: a draft row for a stored file,
// inside the seller quota (Create).
func (r *Releases) AddRelease(ctx context.Context, userID, itemID string, in authoring.NewRelease) (authoring.Release, error) {
	sellerID, err := r.ReleaseOwner(ctx, userID, itemID)
	if err != nil {
		return authoring.Release{}, err
	}
	in.Version = strings.TrimSpace(in.Version)
	if !releaseVersion.MatchString(in.Version) || in.StorageKey == "" || in.Size <= 0 {
		return authoring.Release{}, authoring.ErrInvalid
	}
	if _, err := r.App.FindFirstRecordByFilter("product_releases", "product = {:p} && version = {:v}",
		dbx.Params{"p": itemID, "v": in.Version}); err == nil {
		return authoring.Release{}, authoring.ErrConflict
	}
	id, err := r.Create(ctx, NewRelease{SellerID: sellerID, ProductID: itemID, Version: in.Version,
		StorageKey: in.StorageKey, SHA256: in.SHA256, Size: in.Size}, upload.DefaultQuota)
	if errors.Is(err, upload.ErrQuota) {
		return authoring.Release{}, authoring.ErrQuota
	}
	if err != nil {
		return authoring.Release{}, err
	}
	rec, err := r.App.FindRecordById("product_releases", id)
	if err != nil {
		return authoring.Release{}, err
	}
	if c := strings.TrimSpace(in.Changelog); c != "" {
		rec.Set("changelog", html.EscapeString(c))
		if err := r.App.Save(rec); err != nil {
			return authoring.Release{}, err
		}
	}
	return releaseOf(rec), nil
}

// PublishRelease implements authoring.Releases: draft -> published. The
// guard in guards.go keeps a published file, size and checksum unchanged.
func (r *Releases) PublishRelease(ctx context.Context, userID, itemID, releaseID string) error {
	if err := ensureMember(ctx, r.App, userID, itemID); err != nil {
		return err
	}
	rec, err := r.App.FindRecordById("product_releases", releaseID)
	if err != nil || rec.GetString("product") != itemID {
		return authoring.ErrNotFound
	}
	if rec.GetString("status") != "draft" {
		return authoring.ErrInvalid
	}
	rec.Set("status", "published")
	rec.Set("released_at", pbDate(time.Now()))
	return r.App.Save(rec)
}
