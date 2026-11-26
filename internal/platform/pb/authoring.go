package pb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/content"
)

// Authoring implements authoring.Store on PocketBase.
type Authoring struct {
	App core.App
}

var _ authoring.Store = (*Authoring)(nil)

type authoringRow struct {
	ID              string `db:"id"`
	SellerID        string `db:"seller"`
	Title           string `db:"title"`
	Slug            string `db:"slug"`
	Status          string `db:"status"`
	Description     string `db:"description"`
	DescriptionHTML string `db:"description_html"`
	DescriptionVers int    `db:"description_version"`
}

func (r authoringRow) toDomain() authoring.Item {
	return authoring.Item{
		ID: r.ID, SellerID: r.SellerID, Title: r.Title, Slug: r.Slug, Status: r.Status,
		Description: r.Description, DescriptionHTML: r.DescriptionHTML, Version: r.DescriptionVers,
	}
}

const itemSelect = `SELECT p.id, p.seller, p.title, p.slug, p.status, p.description,
	COALESCE(p.description_html, '') AS description_html,
	COALESCE(p.description_version, 0) AS description_version
FROM products p
WHERE p.seller IN (SELECT seller FROM seller_members WHERE user = {:u})`

func (a *Authoring) SellerMember(ctx context.Context, userID string) (bool, error) {
	var n int
	err := a.App.DB().NewQuery(`SELECT COUNT(*) FROM seller_members WHERE user = {:u}`).
		WithContext(ctx).Bind(dbx.Params{"u": userID}).Row(&n)
	if err != nil {
		return false, fmt.Errorf("authoring: member: %w", err)
	}
	return n > 0, nil
}

func (a *Authoring) List(ctx context.Context, userID string) ([]authoring.Item, error) {
	var rows []authoringRow
	err := a.App.DB().NewQuery(itemSelect + ` ORDER BY p.updated DESC`).
		WithContext(ctx).Bind(dbx.Params{"u": userID}).All(&rows)
	if err != nil {
		return nil, fmt.Errorf("authoring: list: %w", err)
	}
	out := make([]authoring.Item, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.toDomain())
	}
	return out, nil
}

func (a *Authoring) Get(ctx context.Context, userID, productID string) (authoring.Item, error) {
	var row authoringRow
	err := a.App.DB().NewQuery(itemSelect + ` AND p.id = {:id} LIMIT 1`).
		WithContext(ctx).Bind(dbx.Params{"u": userID, "id": productID}).One(&row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authoring.Item{}, authoring.ErrNotFound
		}
		return authoring.Item{}, fmt.Errorf("authoring: get: %w", err)
	}
	return row.toDomain(), nil
}

func (a *Authoring) SaveDescription(ctx context.Context, userID, productID, markdown string, version int) (authoring.Item, error) {
	body, err := content.Prepare(markdown)
	if err == content.ErrTooLarge {
		return authoring.Item{}, authoring.ErrTooLarge
	}
	if err != nil {
		return authoring.Item{}, authoring.ErrInvalid
	}
	html, err := content.Render(markdown)
	if err == content.ErrTooLarge {
		return authoring.Item{}, authoring.ErrTooLarge
	}
	if err != nil {
		return authoring.Item{}, authoring.ErrInvalid
	}
	var out authoring.Item
	err = a.App.RunInTransaction(func(txApp core.App) error {
		if err := ensureMember(ctx, txApp, userID, productID); err != nil {
			return err
		}
		rec, err := txApp.FindRecordById("products", productID)
		if err != nil {
			return authoring.ErrNotFound
		}
		current := rec.GetInt("description_version")
		if current != version {
			return authoring.ErrConflict
		}
		rec.Set("description", body)
		rec.Set("description_html", html)
		rec.Set("description_version", current+1)
		if err := txApp.Save(rec); err != nil {
			return err
		}
		out = itemFromRecord(rec)
		return nil
	})
	return out, err
}

func (a *Authoring) AddImage(ctx context.Context, userID, productID, filename string, r io.Reader) (authoring.Image, error) {
	var out authoring.Image
	data, err := io.ReadAll(io.LimitReader(r, authoring.MaxImageBytes+1))
	if err != nil {
		return out, fmt.Errorf("authoring: read image: %w", err)
	}
	if int64(len(data)) > authoring.MaxImageBytes || len(data) == 0 {
		return out, authoring.ErrTooLarge
	}
	mime, ok := authoring.DetectImage(data)
	if !ok {
		return out, authoring.ErrBadImage
	}
	name := authoring.SafeImageName(filename, mime)
	file, err := filesystem.NewFileFromBytes(data, name)
	if err != nil {
		return out, fmt.Errorf("authoring: file: %w", err)
	}
	err = a.App.RunInTransaction(func(txApp core.App) error {
		if err := ensureMember(ctx, txApp, userID, productID); err != nil {
			return err
		}
		rec, err := txApp.FindRecordById("products", productID)
		if err != nil {
			return authoring.ErrNotFound
		}
		existing := rec.GetStringSlice("images")
		if len(existing) >= authoring.MaxImages {
			return authoring.ErrQuota
		}
		if err := checkImageBytes(txApp, rec, existing, int64(len(data))); err != nil {
			return err
		}
		rec.Set("images+", file)
		if err := txApp.Save(rec); err != nil {
			return err
		}
		names := rec.GetStringSlice("images")
		stored := name
		if len(names) > 0 {
			stored = names[len(names)-1]
		}
		out = authoring.Image{Name: stored, URL: fileURL(txApp, rec, stored)}
		return nil
	})
	return out, err
}

func checkImageBytes(app core.App, rec *core.Record, names []string, add int64) error {
	fsys, err := app.NewFilesystem()
	if err != nil {
		return err
	}
	defer fsys.Close()
	var total int64
	base := rec.BaseFilesPath()
	for _, n := range names {
		attr, err := fsys.Attributes(base + "/" + n)
		if err != nil {
			continue
		}
		total += attr.Size
	}
	if total+add > authoring.MaxImageBytesTotal {
		return authoring.ErrQuota
	}
	return nil
}

func ensureMember(ctx context.Context, app core.App, userID, productID string) error {
	var n int
	err := app.DB().NewQuery(`SELECT COUNT(*) FROM seller_members m
		JOIN products p ON p.seller = m.seller
		WHERE m.user = {:u} AND p.id = {:id}`).
		WithContext(ctx).Bind(dbx.Params{"u": userID, "id": productID}).Row(&n)
	if err != nil {
		return fmt.Errorf("authoring: member check: %w", err)
	}
	if n == 0 {
		// Distinguish unknown product from a product the user cannot edit.
		_, ferr := app.FindRecordById("products", productID)
		if ferr != nil {
			return authoring.ErrNotFound
		}
		return authoring.ErrDenied
	}
	return nil
}

func itemFromRecord(rec *core.Record) authoring.Item {
	return authoring.Item{
		ID: rec.Id, SellerID: rec.GetString("seller"), Title: rec.GetString("title"),
		Slug: rec.GetString("slug"), Status: rec.GetString("status"),
		Description: rec.GetString("description"), DescriptionHTML: rec.GetString("description_html"),
		Version: rec.GetInt("description_version"),
	}
}

func fileURL(app core.App, rec *core.Record, name string) string {
	if name == "" {
		return ""
	}
	return "/api/files/" + rec.Collection().Id + "/" + rec.Id + "/" + name
}
