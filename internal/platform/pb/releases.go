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
	// Status defaults to review (ADR-0036); Manifest is the archive
	// report JSON and Flags its flag count.
	Status   string
	Manifest []byte
	Flags    int
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
		status := in.Status
		if status == "" {
			status = authoring.ReleaseInReview
		}
		data := map[string]any{
			"product": in.ProductID, "seller": in.SellerID, "version": in.Version, "status": status,
			"storage_key": in.StorageKey, "size": in.Size, "sha256": in.SHA256, "flag_count": in.Flags,
		}
		if len(in.Manifest) > 0 {
			data["manifest"] = string(in.Manifest)
		}
		rec, err := newRecord(tx, "product_releases", data)
		if err != nil {
			return err
		}
		id = rec.Id
		return nil
	})
	return id, err
}

var releaseVersion = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)

// ValidVersion reports whether v is a release version (1.0.0, 2.1-beta).
func ValidVersion(v string) bool { return releaseVersion.MatchString(strings.TrimSpace(v)) }

// ReleaseTarget implements authoring.Releases.
func (r *Releases) ReleaseTarget(ctx context.Context, userID, itemID string) (authoring.ReleaseTarget, error) {
	if err := ensureMember(ctx, r.App, userID, itemID); err != nil {
		return authoring.ReleaseTarget{}, err
	}
	p, err := r.App.FindRecordById("products", itemID)
	if err != nil {
		return authoring.ReleaseTarget{}, authoring.ErrNotFound
	}
	t := authoring.ReleaseTarget{SellerID: p.GetString("seller"), ItemSlug: p.GetString("slug")}
	if s, err := r.App.FindRecordById("sellers", t.SellerID); err == nil {
		t.SellerSlug = s.GetString("slug")
	}
	return t, nil
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
		ReviewNote: rec.GetString("review_note"), ReviewedBy: rec.GetString("reviewed_by"), Flags: rec.GetInt("flag_count"),
		Created: rec.GetDateTime("created").Time(), ReviewedAt: rec.GetDateTime("reviewed_at").Time()}
}

// VersionTaken reports whether the item already has the version (cheap
// pre-check before a large upload; the unique index is the real guard).
func (r *Releases) VersionTaken(ctx context.Context, itemID, version string) bool {
	_, err := r.App.FindFirstRecordByFilter("product_releases", "product = {:p} && version = {:v}",
		dbx.Params{"p": itemID, "v": strings.TrimSpace(version)})
	return err == nil
}

// AddRelease implements authoring.Releases: a row in review for a stored
// file, inside the seller quota (Create).
func (r *Releases) AddRelease(ctx context.Context, userID, itemID string, in authoring.NewRelease) (authoring.Release, error) {
	t, err := r.ReleaseTarget(ctx, userID, itemID)
	if err != nil {
		return authoring.Release{}, err
	}
	in.Version = strings.TrimSpace(in.Version)
	if !releaseVersion.MatchString(in.Version) || in.StorageKey == "" || in.Size <= 0 {
		return authoring.Release{}, authoring.ErrInvalid
	}
	if r.VersionTaken(ctx, itemID, in.Version) {
		return authoring.Release{}, authoring.ErrConflict
	}
	id, err := r.Create(ctx, NewRelease{SellerID: t.SellerID, ProductID: itemID, Version: in.Version,
		StorageKey: in.StorageKey, SHA256: in.SHA256, Size: in.Size, Manifest: in.Manifest, Flags: in.Flags}, upload.DefaultQuota)
	if errors.Is(err, upload.ErrQuota) {
		return authoring.Release{}, authoring.ErrQuota
	}
	if err != nil {
		// the unique (product, version) index lost a race
		if r.VersionTaken(ctx, itemID, in.Version) {
			return authoring.Release{}, authoring.ErrConflict
		}
		return authoring.Release{}, err
	}
	rec, err := r.App.FindRecordById("product_releases", id)
	if err != nil {
		return authoring.Release{}, err
	}
	if c := strings.TrimSpace(in.Changelog); c != "" {
		if len(c) > 4000 {
			c = c[:4000]
		}
		rec.Set("changelog", html.EscapeString(c))
		if err := r.App.Save(rec); err != nil {
			return authoring.Release{}, err
		}
	}
	return releaseOf(rec), nil
}

func (r *Releases) itemRelease(ctx context.Context, userID, itemID, releaseID string) (*core.Record, error) {
	if err := ensureMember(ctx, r.App, userID, itemID); err != nil {
		return nil, err
	}
	rec, err := r.App.FindRecordById("product_releases", releaseID)
	if err != nil || rec.GetString("product") != itemID {
		return nil, authoring.ErrNotFound
	}
	return rec, nil
}

// YankRelease implements authoring.Releases: published -> yanked. The
// file stays (quota counts it) so the action is reversible by an operator.
func (r *Releases) YankRelease(ctx context.Context, userID, itemID, releaseID string) error {
	rec, err := r.itemRelease(ctx, userID, itemID, releaseID)
	if err != nil {
		return err
	}
	if rec.GetString("status") != authoring.ReleasePublished {
		return authoring.ErrInvalid
	}
	return RunTx(ctx, r.App, "release.yank", func(ctx context.Context, tx core.App) error {
		others, err := count(tx, `SELECT COUNT(*) FROM product_releases WHERE product = {:p} AND status = 'published'
			AND storage_key != '' AND id != {:r}`, dbx.Params{"p": itemID, "r": releaseID})
		if err != nil {
			return err
		}
		if others == 0 {
			buyers, err := count(tx, `SELECT COUNT(*) FROM entitlements WHERE product = {:p} AND status = 'active'`, dbx.Params{"p": itemID})
			if err != nil {
				return err
			}
			if buyers > 0 {
				return authoring.ErrLastRelease
			}
		}
		rec, err := tx.FindRecordById("product_releases", releaseID)
		if err != nil {
			return err
		}
		rec.Set("status", authoring.ReleaseYanked)
		return tx.Save(rec)
	})
}

// DeleteRelease implements authoring.Releases: an unpublished release is
// removed; published and yanked ones never are (buyers may hold them).
func (r *Releases) DeleteRelease(ctx context.Context, userID, itemID, releaseID string) (string, error) {
	rec, err := r.itemRelease(ctx, userID, itemID, releaseID)
	if err != nil {
		return "", err
	}
	switch rec.GetString("status") {
	case authoring.ReleaseInReview, authoring.ReleaseRejected, authoring.ReleaseDraft:
	default:
		return "", authoring.ErrInvalid
	}
	key := rec.GetString("storage_key")
	if err := r.App.Delete(rec); err != nil {
		return "", err
	}
	return key, nil
}

// ReleaseQueue implements authoring.ReleaseModeration.
func (r *Releases) ReleaseQueue(ctx context.Context, status string) ([]authoring.ReleaseReview, error) {
	switch status {
	case "":
		status = authoring.ReleaseInReview
	case authoring.ReleaseInReview, authoring.ReleasePublished, authoring.ReleaseRejected, authoring.ReleaseYanked, authoring.ReleaseDraft:
	default:
		return nil, authoring.ErrInvalid
	}
	order := "r.created DESC"
	if status == authoring.ReleaseInReview {
		order = "r.created ASC" // oldest waits longest
	}
	return r.reviews(ctx, "r.status = {:s}", dbx.Params{"s": status}, order, 200)
}

// ReleaseForReview implements authoring.ReleaseModeration.
func (r *Releases) ReleaseForReview(ctx context.Context, id string) (authoring.ReleaseReview, error) {
	rs, err := r.reviews(ctx, "r.id = {:id}", dbx.Params{"id": id}, "r.created", 1)
	if err != nil {
		return authoring.ReleaseReview{}, err
	}
	if len(rs) == 0 {
		return authoring.ReleaseReview{}, authoring.ErrNotFound
	}
	return rs[0], nil
}

func (r *Releases) reviews(ctx context.Context, where string, params dbx.Params, order string, limit int) ([]authoring.ReleaseReview, error) {
	var rows []struct {
		ID         string `db:"id"`
		Item       string `db:"item"`
		Title      string `db:"title"`
		Slug       string `db:"slug"`
		Seller     string `db:"seller"`
		SellerSlug string `db:"seller_slug"`
		Version    string `db:"version"`
		Status     string `db:"status"`
		Changelog  string `db:"changelog"`
		Key        string `db:"storage_key"`
		SHA        string `db:"sha256"`
		Note       string `db:"review_note"`
		By         string `db:"reviewed_by"`
		At         string `db:"reviewed_at"`
		Size       int64  `db:"size"`
		Flags      int    `db:"flag_count"`
		Manifest   string `db:"manifest"`
		Created    string `db:"created"`
	}
	err := r.App.DB().NewQuery(`SELECT r.id, p.id AS item, p.title, p.slug, s.display_name AS seller, s.slug AS seller_slug,
		r.version, r.status, COALESCE(r.changelog, '') AS changelog, r.storage_key, COALESCE(r.sha256, '') AS sha256,
		COALESCE(r.review_note, '') AS review_note, COALESCE(r.reviewed_by, '') AS reviewed_by,
		COALESCE(r.reviewed_at, '') AS reviewed_at, COALESCE(r.size, 0) AS size, COALESCE(r.flag_count, 0) AS flag_count,
		COALESCE(r.manifest, '') AS manifest, r.created
		FROM product_releases r JOIN products p ON p.id = r.product JOIN sellers s ON s.id = r.seller
		WHERE ` + where + ` ORDER BY ` + order + fmt.Sprintf(" LIMIT %d", limit)).WithContext(ctx).Bind(params).All(&rows)
	out := make([]authoring.ReleaseReview, 0, len(rows))
	for _, x := range rows {
		m := []byte(nil)
		if x.Manifest != "" && x.Manifest != "null" {
			m = []byte(x.Manifest)
		}
		out = append(out, authoring.ReleaseReview{ID: x.ID, ItemID: x.Item, ItemTitle: x.Title, ItemSlug: x.Slug,
			SellerName: x.Seller, SellerSlug: x.SellerSlug, Version: x.Version, Status: x.Status, Changelog: x.Changelog,
			Name: path.Base(x.Key), StorageKey: x.Key, SHA256: x.SHA, ReviewNote: x.Note, ReviewedBy: x.By,
			Size: x.Size, Flags: x.Flags, Manifest: m, Created: pbTime(x.Created), ReviewedAt: pbTime(x.At)})
	}
	return out, err
}

// ModerateRelease implements authoring.ReleaseModeration: review ->
// published (released_at now) or review -> rejected (note required). by
// is the moderator's e-mail, kept with the time on the row.
func (r *Releases) ModerateRelease(ctx context.Context, id, decision, note, by string) error {
	note = strings.TrimSpace(note)
	if len(note) > 1000 {
		note = note[:1000]
	}
	switch decision {
	case authoring.ReleasePublished:
	case authoring.ReleaseRejected:
		if note == "" {
			return authoring.ErrInvalid
		}
	default:
		return authoring.ErrInvalid
	}
	return RunTx(ctx, r.App, "release.moderate", func(ctx context.Context, tx core.App) error {
		rec, err := tx.FindRecordById("product_releases", id)
		if err != nil {
			return authoring.ErrNotFound
		}
		if s := rec.GetString("status"); s != authoring.ReleaseInReview && s != authoring.ReleaseDraft {
			return authoring.ErrInvalid
		}
		if rec.GetString("storage_key") == "" {
			return authoring.ErrInvalid
		}
		now := pbDate(time.Now())
		rec.Set("status", decision)
		rec.Set("review_note", note)
		rec.Set("reviewed_by", by)
		rec.Set("reviewed_at", now)
		if decision == authoring.ReleasePublished {
			rec.Set("released_at", now)
		}
		return tx.Save(rec)
	})
}
