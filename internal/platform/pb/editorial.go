package pb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/content"
	"github.com/fastygo/market/internal/editorial"
)

// Editorial implements editorial.Store (ADR-0035).
type Editorial struct {
	App core.App
	Now func() time.Time
}

var _ editorial.Store = (*Editorial)(nil)

func (e *Editorial) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func renderMD(md string) (string, string, error) {
	body, err := content.Prepare(md)
	if err != nil {
		return "", "", editorial.ErrInvalid
	}
	html, err := content.Render(md)
	if err != nil {
		return "", "", editorial.ErrInvalid
	}
	return body, html, nil
}

func articleOf(app core.App, r *core.Record) editorial.Article {
	a := editorial.Article{ID: r.Id, Slug: r.GetString("slug"), Title: r.GetString("title"), Excerpt: r.GetString("excerpt"),
		Body: r.GetString("body"), BodyHTML: r.GetString("body_html"), Kind: r.GetString("kind"), Status: r.GetString("status"),
		ReviewNote: r.GetString("review_note"), AuthorID: r.GetString("author"), SellerID: r.GetString("seller"),
		Version: r.GetInt("version"), PublishedAt: r.GetDateTime("published_at").Time(), Updated: r.GetDateTime("updated").Time()}
	if f := r.GetString("cover"); f != "" {
		a.CoverURL = fileURL(app, r, f)
	}
	if u, err := app.FindRecordById("users", a.AuthorID); err == nil {
		a.AuthorName = u.GetString("name")
		if a.AuthorName == "" {
			a.AuthorName = strings.SplitN(u.Email(), "@", 2)[0]
		}
	}
	if a.SellerID != "" {
		if s, err := app.FindRecordById("sellers", a.SellerID); err == nil {
			a.AuthorName = s.GetString("display_name")
		}
	}
	return a
}

func (e *Editorial) articles(filter string, p dbx.Params, sort string, limit int) ([]editorial.Article, error) {
	recs, err := e.App.FindRecordsByFilter("articles", filter, sort, limit, 0, p)
	if err != nil {
		return nil, fmt.Errorf("editorial: articles: %w", err)
	}
	out := make([]editorial.Article, 0, len(recs))
	for _, r := range recs {
		out = append(out, articleOf(e.App, r))
	}
	return out, nil
}

// PublishedArticles implements editorial.Store.
func (e *Editorial) PublishedArticles(ctx context.Context, limit int) ([]editorial.Article, error) {
	return e.articles("status = 'published'", nil, "-published_at", limit)
}

// Article implements editorial.Store (published only).
func (e *Editorial) Article(ctx context.Context, slug string) (editorial.Article, error) {
	r, err := e.App.FindFirstRecordByFilter("articles", "slug = {:s} && status = 'published'", dbx.Params{"s": slug})
	if err != nil {
		return editorial.Article{}, editorial.ErrNotFound
	}
	return articleOf(e.App, r), nil
}

// MyArticles implements editorial.Store.
func (e *Editorial) MyArticles(ctx context.Context, userID string) ([]editorial.Article, error) {
	return e.articles("author = {:u}", dbx.Params{"u": userID}, "-updated", 500)
}

// GetArticle implements editorial.Store (any status; callers check access).
func (e *Editorial) GetArticle(ctx context.Context, id string) (editorial.Article, error) {
	r, err := e.App.FindRecordById("articles", id)
	if err != nil {
		return editorial.Article{}, editorial.ErrNotFound
	}
	return articleOf(e.App, r), nil
}

// SaveArticle implements editorial.Store: create (ID empty) or update the
// author's own article; submit sends it to review. A published article
// goes back to review when its author edits it.
func (e *Editorial) SaveArticle(ctx context.Context, a editorial.Article, userID string, submit bool) (editorial.Article, error) {
	a.Title = strings.TrimSpace(a.Title)
	if a.Title == "" || len(a.Title) > 200 || len(a.Excerpt) > 500 {
		return a, editorial.ErrInvalid
	}
	if a.Slug == "" {
		a.Slug = cabinet.Slugify(a.Title)
	}
	if !cabinet.ValidSlug(a.Slug) {
		return a, editorial.ErrInvalid
	}
	switch a.Kind {
	case "article", "news", "guide":
	default:
		a.Kind = "article"
	}
	body, html, err := renderMD(a.Body)
	if err != nil {
		return a, err
	}
	var out editorial.Article
	err = e.App.RunInTransaction(func(tx core.App) error {
		var r *core.Record
		if a.ID == "" {
			col, err := tx.FindCachedCollectionByNameOrId("articles")
			if err != nil {
				return err
			}
			r = core.NewRecord(col)
			r.Set("author", userID)
			r.Set("status", editorial.Draft)
			if a.SellerID != "" {
				r.Set("seller", a.SellerID)
			}
		} else {
			found, err := tx.FindRecordById("articles", a.ID)
			if err != nil {
				return editorial.ErrNotFound
			}
			if found.GetString("author") != userID {
				return editorial.ErrDenied
			}
			if found.GetInt("version") != a.Version {
				return editorial.ErrConflict
			}
			r = found
		}
		if other, err := tx.FindFirstRecordByData("articles", "slug", a.Slug); err == nil && other.Id != r.Id {
			return editorial.ErrInvalid
		}
		r.Set("slug", a.Slug)
		r.Set("title", a.Title)
		r.Set("excerpt", strings.TrimSpace(a.Excerpt))
		r.Set("kind", a.Kind)
		r.Set("body", body)
		r.Set("body_html", html)
		r.Set("version", r.GetInt("version")+1)
		switch {
		case submit:
			r.Set("status", editorial.InReview)
		case r.GetString("status") == editorial.Published || r.GetString("status") == editorial.Rejected:
			r.Set("status", editorial.Draft)
		}
		if err := tx.Save(r); err != nil {
			return err
		}
		out = articleOf(tx, r)
		return nil
	})
	return out, err
}

// Articles implements editorial.Store ("" lists all).
func (e *Editorial) Articles(ctx context.Context, status string) ([]editorial.Article, error) {
	if status == "" {
		return e.articles("id != ''", nil, "-updated", 500)
	}
	return e.articles("status = {:s}", dbx.Params{"s": status}, "-updated", 500)
}

// ModerateArticle implements editorial.Store.
func (e *Editorial) ModerateArticle(ctx context.Context, id, status, note string) error {
	switch status {
	case editorial.Published, editorial.Rejected, editorial.Archived, editorial.Draft:
	default:
		return editorial.ErrInvalid
	}
	r, err := e.App.FindRecordById("articles", id)
	if err != nil {
		return editorial.ErrNotFound
	}
	r.Set("status", status)
	r.Set("review_note", strings.TrimSpace(note))
	if status == editorial.Published && r.GetDateTime("published_at").IsZero() {
		r.Set("published_at", pbDate(e.now()))
	}
	return e.App.Save(r)
}

func pageOf(r *core.Record) editorial.Page {
	return editorial.Page{ID: r.Id, Slug: r.GetString("slug"), Title: r.GetString("title"), Body: r.GetString("body"),
		BodyHTML: r.GetString("body_html"), Kind: r.GetString("kind"), Version: r.GetString("version"),
		Status: r.GetString("status"), Updated: r.GetDateTime("updated").Time()}
}

// Page implements editorial.Store (published only).
func (e *Editorial) Page(ctx context.Context, slug string) (editorial.Page, error) {
	r, err := e.App.FindFirstRecordByFilter("pages", "slug = {:s} && status = 'published'", dbx.Params{"s": slug})
	if err != nil {
		return editorial.Page{}, editorial.ErrNotFound
	}
	return pageOf(r), nil
}

// Pages implements editorial.Store (all statuses).
func (e *Editorial) Pages(ctx context.Context) ([]editorial.Page, error) {
	recs, err := e.App.FindRecordsByFilter("pages", "id != ''", "kind,slug", 500, 0)
	if err != nil {
		return nil, err
	}
	out := make([]editorial.Page, 0, len(recs))
	for _, r := range recs {
		out = append(out, pageOf(r))
	}
	return out, nil
}

// SavePage implements editorial.Store: create or update by slug.
func (e *Editorial) SavePage(ctx context.Context, p editorial.Page, by string) error {
	p.Title = strings.TrimSpace(p.Title)
	if !cabinet.ValidSlug(p.Slug) || p.Title == "" || len(p.Title) > 200 {
		return editorial.ErrInvalid
	}
	if p.Kind != "legal" {
		p.Kind = "info"
	}
	if p.Status != "draft" {
		p.Status = "published"
	}
	body, html, err := renderMD(p.Body)
	if err != nil {
		return err
	}
	r, err := e.App.FindFirstRecordByData("pages", "slug", p.Slug)
	if err != nil {
		col, cerr := e.App.FindCachedCollectionByNameOrId("pages")
		if cerr != nil {
			return cerr
		}
		r = core.NewRecord(col)
		r.Set("slug", p.Slug)
	}
	r.Set("title", p.Title)
	r.Set("body", body)
	r.Set("body_html", html)
	r.Set("kind", p.Kind)
	r.Set("version", strings.TrimSpace(p.Version))
	r.Set("status", p.Status)
	r.Set("updated_by", by)
	return e.App.Save(r)
}

func collectionOf(app core.App, r *core.Record) editorial.Collection {
	c := editorial.Collection{ID: r.Id, Slug: r.GetString("slug"), Title: r.GetString("title"), Eyebrow: r.GetString("eyebrow"),
		Lead: r.GetString("lead"), Cta: r.GetString("cta"), Tone: r.GetString("tone"), Status: r.GetString("status"),
		ProductIDs: r.GetStringSlice("products"), Sort: r.GetInt("sort")}
	if f := r.GetString("image"); f != "" {
		c.ImageURL = fileURL(app, r, f)
	}
	return c
}

func (e *Editorial) collections(filter string) ([]editorial.Collection, error) {
	recs, err := e.App.FindRecordsByFilter("collections", filter, "sort,title", 200, 0)
	if err != nil {
		return nil, err
	}
	out := make([]editorial.Collection, 0, len(recs))
	for _, r := range recs {
		out = append(out, collectionOf(e.App, r))
	}
	return out, nil
}

// PublishedCollections implements editorial.Store.
func (e *Editorial) PublishedCollections(ctx context.Context) ([]editorial.Collection, error) {
	return e.collections("status = 'published'")
}

// Collections implements editorial.Store.
func (e *Editorial) Collections(ctx context.Context) ([]editorial.Collection, error) {
	return e.collections("id != ''")
}

// Collection implements editorial.Store (published only).
func (e *Editorial) Collection(ctx context.Context, slug string) (editorial.Collection, error) {
	r, err := e.App.FindFirstRecordByFilter("collections", "slug = {:s} && status = 'published'", dbx.Params{"s": slug})
	if err != nil {
		return editorial.Collection{}, editorial.ErrNotFound
	}
	return collectionOf(e.App, r), nil
}

// SaveCollection implements editorial.Store: create or update by slug;
// products are given by id or slug.
func (e *Editorial) SaveCollection(ctx context.Context, c editorial.Collection) error {
	c.Title = strings.TrimSpace(c.Title)
	if c.Slug == "" {
		c.Slug = cabinet.Slugify(c.Title)
	}
	if !cabinet.ValidSlug(c.Slug) || c.Title == "" {
		return editorial.ErrInvalid
	}
	var ids []string
	for _, ref := range c.ProductIDs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if p, err := e.App.FindRecordById("products", ref); err == nil {
			ids = append(ids, p.Id)
		} else if p, err := e.App.FindFirstRecordByData("products", "slug", ref); err == nil {
			ids = append(ids, p.Id)
		}
	}
	r, err := e.App.FindFirstRecordByData("collections", "slug", c.Slug)
	if err != nil {
		col, cerr := e.App.FindCachedCollectionByNameOrId("collections")
		if cerr != nil {
			return cerr
		}
		r = core.NewRecord(col)
		r.Set("slug", c.Slug)
	}
	if c.Tone != "accent" {
		c.Tone = "muted"
	}
	if c.Status != "published" {
		c.Status = "draft"
	}
	r.Load(map[string]any{"title": c.Title, "eyebrow": c.Eyebrow, "lead": c.Lead, "cta": c.Cta, "tone": c.Tone,
		"status": c.Status, "products": ids, "sort": c.Sort})
	return e.App.Save(r)
}

type reviewRow struct {
	ID      string `db:"id"`
	Product string `db:"product"`
	PTitle  string `db:"ptitle"`
	PSlug   string `db:"pslug"`
	User    string `db:"user"`
	Name    string `db:"author_name"`
	Rating  int    `db:"rating"`
	Body    string `db:"body"`
	Status  string `db:"status"`
	Reply   string `db:"reply"`
	Created string `db:"created"`
}

const reviewSelect = `SELECT r.id, r.product, p.title AS ptitle, p.slug AS pslug, r.user, r.author_name, r.rating, r.body, r.status,
	r.reply, r.created FROM reviews r JOIN products p ON p.id = r.product`

func reviewsOf(rows []reviewRow) []editorial.Review {
	out := make([]editorial.Review, 0, len(rows))
	for _, r := range rows {
		out = append(out, editorial.Review{ID: r.ID, ProductID: r.Product, ProductTitle: r.PTitle, ProductSlug: r.PSlug, UserID: r.User,
			AuthorName: r.Name, Rating: r.Rating, Body: r.Body, Status: r.Status, Reply: r.Reply, Created: pbTime(r.Created)})
	}
	return out
}

// ProductReviews implements editorial.Store (published only).
func (e *Editorial) ProductReviews(ctx context.Context, productID string) ([]editorial.Review, error) {
	var rows []reviewRow
	err := e.App.DB().NewQuery(reviewSelect + ` WHERE r.product = {:p} AND r.status = 'published' ORDER BY r.created DESC LIMIT 50`).
		WithContext(ctx).Bind(dbx.Params{"p": productID}).All(&rows)
	return reviewsOf(rows), err
}

// AddReview implements editorial.Store: only a buyer with a paid order of
// the product; one review per product, pending moderation.
func (e *Editorial) AddReview(ctx context.Context, r editorial.Review) error {
	if r.Rating < 1 || r.Rating > 5 || len(r.Body) > 5000 {
		return editorial.ErrInvalid
	}
	n, err := count(e.App, `SELECT COUNT(*) FROM order_items i JOIN orders o ON o.id = i.`+"`order`"+`
		WHERE i.product = {:p} AND o.buyer = {:u} AND o.status IN ('paid', 'fulfilled')`, dbx.Params{"p": r.ProductID, "u": r.UserID})
	if err != nil {
		return err
	}
	if n == 0 {
		return editorial.ErrDenied
	}
	rec, err := e.App.FindFirstRecordByFilter("reviews", "product = {:p} && user = {:u}", dbx.Params{"p": r.ProductID, "u": r.UserID})
	if err != nil {
		col, cerr := e.App.FindCachedCollectionByNameOrId("reviews")
		if cerr != nil {
			return cerr
		}
		rec = core.NewRecord(col)
		rec.Set("product", r.ProductID)
		rec.Set("user", r.UserID)
	}
	rec.Load(map[string]any{"rating": r.Rating, "body": strings.TrimSpace(r.Body), "status": "pending", "author_name": r.AuthorName})
	return e.App.Save(rec)
}

// Reviews implements editorial.Store ("" lists all).
func (e *Editorial) Reviews(ctx context.Context, status string) ([]editorial.Review, error) {
	var rows []reviewRow
	err := e.App.DB().NewQuery(reviewSelect + ` WHERE ({:s} = '' OR r.status = {:s}) ORDER BY r.created DESC LIMIT 500`).
		WithContext(ctx).Bind(dbx.Params{"s": status}).All(&rows)
	return reviewsOf(rows), err
}

// ModerateReview implements editorial.Store and keeps the product rating.
func (e *Editorial) ModerateReview(ctx context.Context, id, status, reply string) error {
	if status != "published" && status != "rejected" && status != "pending" {
		return editorial.ErrInvalid
	}
	return e.App.RunInTransaction(func(tx core.App) error {
		r, err := tx.FindRecordById("reviews", id)
		if err != nil {
			return editorial.ErrNotFound
		}
		r.Set("status", status)
		if reply = strings.TrimSpace(reply); reply != r.GetString("reply") {
			r.Set("reply", reply)
			r.Set("reply_at", pbDate(e.now()))
		}
		if err := tx.Save(r); err != nil {
			return err
		}
		var agg struct {
			N   int     `db:"n"`
			Avg float64 `db:"avg"`
		}
		if err := tx.DB().NewQuery(`SELECT COUNT(*) AS n, COALESCE(AVG(rating), 0) AS avg FROM reviews WHERE product = {:p} AND status = 'published'`).
			Bind(dbx.Params{"p": r.GetString("product")}).One(&agg); err != nil {
			return err
		}
		_, err = tx.DB().NewQuery(`UPDATE products SET rating_avg = {:a}, rating_count = {:n} WHERE id = {:p}`).
			Bind(dbx.Params{"a": agg.Avg, "n": agg.N, "p": r.GetString("product")}).Execute()
		return err
	})
}

// ProductQueue implements editorial.Store: products waiting for review.
func (e *Editorial) ProductQueue(ctx context.Context) ([]editorial.ProductReview, error) {
	var rows []struct {
		ID      string `db:"id"`
		Slug    string `db:"slug"`
		Title   string `db:"title"`
		Seller  string `db:"seller"`
		Status  string `db:"status"`
		Note    string `db:"review_note"`
		Updated string `db:"updated"`
	}
	err := e.App.DB().NewQuery(`SELECT p.id, p.slug, p.title, s.display_name AS seller, p.status, COALESCE(p.review_note, '') AS review_note,
		p.updated FROM products p JOIN sellers s ON s.id = p.seller
		WHERE p.status IN ('in_review', 'published', 'unlisted') ORDER BY CASE p.status WHEN 'in_review' THEN 0 ELSE 1 END, p.updated DESC LIMIT 500`).
		WithContext(ctx).All(&rows)
	out := make([]editorial.ProductReview, 0, len(rows))
	for _, r := range rows {
		out = append(out, editorial.ProductReview{ID: r.ID, Slug: r.Slug, Title: r.Title, SellerName: r.Seller, Status: r.Status,
			ReviewNote: r.Note, Updated: pbTime(r.Updated)})
	}
	return out, err
}

// ModerateProduct implements editorial.Store: publish, send back to the
// draft with a note, unlist or archive.
func (e *Editorial) ModerateProduct(ctx context.Context, id, status, note string) error {
	switch status {
	case "published", "draft", "unlisted", "archived":
	default:
		return editorial.ErrInvalid
	}
	r, err := e.App.FindRecordById("products", id)
	if err != nil {
		return editorial.ErrNotFound
	}
	if status == "published" {
		s, err := e.App.FindRecordById("sellers", r.GetString("seller"))
		if err != nil {
			return editorial.ErrNotFound
		}
		if !HasVATStatus(s) {
			return editorial.ErrTaxProfile
		}
	}
	r.Set("status", status)
	r.Set("review_note", strings.TrimSpace(note))
	if status == "published" && r.GetDateTime("published_at").IsZero() {
		r.Set("published_at", pbDate(e.now()))
	}
	return e.App.RunInTransaction(func(tx core.App) error {
		if err := tx.Save(r); err != nil {
			return err
		}
		if status != "published" {
			return nil
		}
		// the first published product opens a newly approved store
		s, err := tx.FindRecordById("sellers", r.GetString("seller"))
		if err != nil || s.GetString("status") != "pending_review" {
			return nil
		}
		s.Set("status", "active")
		return tx.Save(s)
	})
}

// Counts implements editorial.Store.
func (e *Editorial) Counts(ctx context.Context) (editorial.Counts, error) {
	var c editorial.Counts
	var err error
	for _, q := range []struct {
		dst *int
		sql string
	}{
		{&c.Products, `SELECT COUNT(*) FROM products WHERE status = 'in_review'`},
		{&c.Articles, `SELECT COUNT(*) FROM articles WHERE status = 'in_review'`},
		{&c.Reviews, `SELECT COUNT(*) FROM reviews WHERE status = 'pending'`},
		{&c.Applications, `SELECT COUNT(*) FROM role_applications WHERE status = 'pending'`},
	} {
		if *q.dst, err = count(e.App, q.sql, nil); err != nil {
			return c, err
		}
	}
	return c, nil
}

// Messages implements editorial.Store (database locales, ADR-0018).
func (e *Editorial) Messages(ctx context.Context, locale string) (map[string]string, error) {
	var rows []struct {
		Key   string `db:"key"`
		Value string `db:"value"`
	}
	err := e.App.DB().NewQuery("SELECT `key`, value FROM ui_messages WHERE locale = {:l}").WithContext(ctx).
		Bind(dbx.Params{"l": locale}).All(&rows)
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Value
	}
	return out, err
}

// ImportMessages implements editorial.Store: upsert every key.
func (e *Editorial) ImportMessages(ctx context.Context, locale string, m map[string]string) (int, error) {
	if locale == "" || len(locale) > 16 {
		return 0, editorial.ErrInvalid
	}
	n := 0
	err := e.App.RunInTransaction(func(tx core.App) error {
		col, err := tx.FindCachedCollectionByNameOrId("ui_messages")
		if err != nil {
			return err
		}
		for k, v := range m {
			r, err := tx.FindFirstRecordByFilter("ui_messages", "locale = {:l} && key = {:k}", dbx.Params{"l": locale, "k": k})
			if err != nil {
				r = core.NewRecord(col)
				r.Set("locale", locale)
				r.Set("key", k)
			}
			r.Set("value", v)
			if err := tx.Save(r); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}
