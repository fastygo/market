package pb

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
)

// fullItem is the cabinet view of a product: card data, prices of the
// regular and extended offers, cover and gallery.
func fullItem(app core.App, rec *core.Record) authoring.Item {
	it := itemFromRecord(rec)
	attrs := map[string]any{}
	_ = rec.UnmarshalJSONField("attributes", &attrs)
	str := func(k string) string {
		v, _ := attrs[k].(string)
		return v
	}
	it.Meta = authoring.Meta{Title: it.Title, Slug: it.Slug, Excerpt: rec.GetString("excerpt"), Category: rec.GetString("category"),
		DemoURL: rec.GetString("demo_url"), Stack: str("stack"), Tags: str("tags"), Currency: "RUB"}
	offers, _ := app.FindRecordsByFilter("offers", "product = {:p}", "license_type", 20, 0, dbx.Params{"p": rec.Id})
	for _, o := range offers {
		if !o.GetBool("active") {
			continue
		}
		it.Meta.Currency = o.GetString("currency")
		switch o.GetString("license_type") {
		case "regular":
			it.Meta.Price = int64(o.GetInt("price_minor"))
		case "extended":
			it.Meta.Extended = int64(o.GetInt("price_minor"))
		}
	}
	it.CoverURL = fileURL(app, rec, rec.GetString("cover"))
	it.ReviewNote = rec.GetString("review_note")
	it.Updated = rec.GetDateTime("updated").Time()
	for _, n := range rec.GetStringSlice("images") {
		it.Images = append(it.Images, authoring.Image{Name: n, URL: fileURL(app, rec, n)})
	}
	return it
}

func validMeta(m *authoring.Meta) error {
	m.Title = strings.TrimSpace(m.Title)
	m.Excerpt = strings.TrimSpace(m.Excerpt)
	if m.Slug == "" {
		m.Slug = cabinet.Slugify(m.Title)
	}
	if m.Title == "" || len(m.Title) > 200 || len(m.Excerpt) > 500 || !cabinet.ValidSlug(m.Slug) ||
		m.Price < 0 || m.Extended < 0 || len(m.Stack) > 120 || len(m.Tags) > 300 {
		return authoring.ErrInvalid
	}
	if m.DemoURL != "" && !strings.HasPrefix(m.DemoURL, "https://") && !strings.HasPrefix(m.DemoURL, "http://") {
		return authoring.ErrInvalid
	}
	if m.Currency == "" {
		m.Currency = "RUB"
	}
	return nil
}

func applyMeta(tx core.App, rec *core.Record, m authoring.Meta) error {
	if other, err := tx.FindFirstRecordByData("products", "slug", m.Slug); err == nil && other.Id != rec.Id {
		return authoring.ErrInvalid
	}
	if m.Category != "" {
		if _, err := tx.FindRecordById("categories", m.Category); err != nil {
			return authoring.ErrInvalid
		}
	}
	attrs := map[string]any{}
	_ = rec.UnmarshalJSONField("attributes", &attrs)
	attrs["stack"], attrs["tags"] = strings.TrimSpace(m.Stack), strings.TrimSpace(m.Tags)
	rec.Set("title", m.Title)
	rec.Set("slug", m.Slug)
	rec.Set("excerpt", m.Excerpt)
	rec.Set("category", m.Category)
	rec.Set("demo_url", m.DemoURL)
	rec.Set("attributes", attrs)
	if err := tx.Save(rec); err != nil {
		return err
	}
	for lic, price := range map[string]int64{"regular": m.Price, "extended": m.Extended} {
		o, err := tx.FindFirstRecordByFilter("offers", "product = {:p} && license_type = {:l} && currency = {:c}",
			dbx.Params{"p": rec.Id, "l": lic, "c": m.Currency})
		if err != nil {
			if lic == "extended" && price == 0 {
				continue
			}
			col, cerr := tx.FindCachedCollectionByNameOrId("offers")
			if cerr != nil {
				return cerr
			}
			o = core.NewRecord(col)
			o.Load(map[string]any{"product": rec.Id, "seller": rec.GetString("seller"), "license_type": lic, "currency": m.Currency,
				"tax_category": "standard", "support_months": 6})
		}
		o.Set("price_minor", price)
		o.Set("active", lic == "regular" || price > 0)
		if err := tx.Save(o); err != nil {
			return err
		}
	}
	return nil
}

// Create implements authoring.Store.
func (a *Authoring) Create(ctx context.Context, userID, sellerID string, m authoring.Meta) (authoring.Item, error) {
	if err := validMeta(&m); err != nil {
		return authoring.Item{}, err
	}
	var out authoring.Item
	err := a.App.RunInTransaction(func(tx core.App) error {
		n, err := count(tx, `SELECT COUNT(*) FROM seller_members WHERE user = {:u} AND seller = {:s}`, dbx.Params{"u": userID, "s": sellerID})
		if err != nil {
			return err
		}
		if n == 0 {
			return authoring.ErrDenied
		}
		col, err := tx.FindCachedCollectionByNameOrId("products")
		if err != nil {
			return err
		}
		rec := core.NewRecord(col)
		rec.Load(map[string]any{"seller": sellerID, "product_type": "plain", "status": "draft", "description_version": 0})
		if err := applyMeta(tx, rec, m); err != nil {
			return err
		}
		out = fullItem(tx, rec)
		return nil
	})
	return out, err
}

// SaveMeta implements authoring.Store.
func (a *Authoring) SaveMeta(ctx context.Context, userID, productID string, m authoring.Meta) (authoring.Item, error) {
	if err := validMeta(&m); err != nil {
		return authoring.Item{}, err
	}
	var out authoring.Item
	err := a.App.RunInTransaction(func(tx core.App) error {
		if err := ensureMember(ctx, tx, userID, productID); err != nil {
			return err
		}
		rec, err := tx.FindRecordById("products", productID)
		if err != nil {
			return authoring.ErrNotFound
		}
		if err := applyMeta(tx, rec, m); err != nil {
			return err
		}
		out = fullItem(tx, rec)
		return nil
	})
	return out, err
}

// SetCover implements authoring.Store (replaces the cover file).
func (a *Authoring) SetCover(ctx context.Context, userID, productID, filename string, r io.Reader) (authoring.Image, error) {
	var out authoring.Image
	data, err := io.ReadAll(io.LimitReader(r, authoring.MaxImageBytes+1))
	if err != nil {
		return out, fmt.Errorf("authoring: read cover: %w", err)
	}
	if int64(len(data)) > authoring.MaxImageBytes || len(data) == 0 {
		return out, authoring.ErrTooLarge
	}
	mime, ok := authoring.DetectImage(data)
	if !ok {
		return out, authoring.ErrBadImage
	}
	file, err := filesystem.NewFileFromBytes(data, authoring.SafeImageName(filename, mime))
	if err != nil {
		return out, err
	}
	err = a.App.RunInTransaction(func(tx core.App) error {
		if err := ensureMember(ctx, tx, userID, productID); err != nil {
			return err
		}
		rec, err := tx.FindRecordById("products", productID)
		if err != nil {
			return authoring.ErrNotFound
		}
		rec.Set("cover", file)
		if err := tx.Save(rec); err != nil {
			return err
		}
		out = authoring.Image{Name: rec.GetString("cover"), URL: fileURL(tx, rec, rec.GetString("cover"))}
		return nil
	})
	return out, err
}

// Act implements authoring.Store: seller status moves. Publishing is the
// editor's decision (editorial.ModerateProduct).
func (a *Authoring) Act(ctx context.Context, userID, productID, action string) (authoring.Item, error) {
	from := map[string][]string{
		authoring.ActSubmit:   {"draft"},
		authoring.ActWithdraw: {"in_review"},
		authoring.ActUnlist:   {"published"},
		authoring.ActRelist:   {"unlisted"},
		authoring.ActArchive:  {"draft", "in_review", "published", "unlisted"},
	}
	to := map[string]string{authoring.ActSubmit: "in_review", authoring.ActWithdraw: "draft", authoring.ActUnlist: "unlisted",
		authoring.ActRelist: "in_review", authoring.ActArchive: "archived"}
	allowed, ok := from[action]
	if !ok {
		return authoring.Item{}, authoring.ErrInvalid
	}
	var out authoring.Item
	err := a.App.RunInTransaction(func(tx core.App) error {
		if err := ensureMember(ctx, tx, userID, productID); err != nil {
			return err
		}
		rec, err := tx.FindRecordById("products", productID)
		if err != nil {
			return authoring.ErrNotFound
		}
		okFrom := false
		for _, s := range allowed {
			okFrom = okFrom || rec.GetString("status") == s
		}
		if !okFrom {
			return authoring.ErrConflict
		}
		if action == authoring.ActSubmit && strings.TrimSpace(rec.GetString("title")) == "" {
			return authoring.ErrInvalid
		}
		rec.Set("status", to[action])
		if err := tx.Save(rec); err != nil {
			return err
		}
		out = fullItem(tx, rec)
		return nil
	})
	return out, err
}

// Categories implements authoring.Store.
func (a *Authoring) Categories(ctx context.Context) ([]authoring.Category, error) {
	recs, err := a.App.FindRecordsByFilter("categories", "id != ''", "sort,title", 500, 0)
	if err != nil {
		return nil, err
	}
	out := make([]authoring.Category, 0, len(recs))
	for _, r := range recs {
		out = append(out, authoring.Category{ID: r.Id, Title: r.GetString("title")})
	}
	return out, nil
}
