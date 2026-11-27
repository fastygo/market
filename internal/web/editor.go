package web

import (
	"errors"
	"github.com/a-h/templ"
	"net/http"
	"strconv"
	"strings"

	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/content"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/web/screens"
)

const articleFormMax = content.MaxMarkdown + (64 << 10)

func (h *Handler) mountEditorial(m *http.ServeMux) {
	m.HandleFunc("GET /studio/articles", h.studioArticles)
	m.HandleFunc("GET /studio/articles/new", h.studioArticle)
	m.HandleFunc("POST /studio/articles/new", h.studioArticleSave)
	m.HandleFunc("GET /studio/articles/{id}", h.studioArticle)
	m.HandleFunc("POST /studio/articles/{id}", h.studioArticleSave)
	m.HandleFunc("GET /editor", h.editorHome)
	for _, q := range []string{"products", "articles", "reviews", "applications", "payouts"} {
		m.HandleFunc("GET /editor/"+q, h.editorQueue)
		m.HandleFunc("POST /editor/"+q, h.editorDecide)
	}
	m.HandleFunc("GET /editor/pages", h.editorPages)
	m.HandleFunc("GET /editor/pages/new", h.editorPage)
	m.HandleFunc("POST /editor/pages/new", h.editorPageSave)
	m.HandleFunc("GET /editor/pages/{slug}", h.editorPage)
	m.HandleFunc("POST /editor/pages/{slug}", h.editorPageSave)
	m.HandleFunc("GET /editor/collections", h.editorCollections)
	m.HandleFunc("GET /editor/collections/new", h.editorCollection)
	m.HandleFunc("POST /editor/collections/new", h.editorCollectionSave)
	m.HandleFunc("GET /editor/collections/{slug}", h.editorCollection)
	m.HandleFunc("POST /editor/collections/{slug}", h.editorCollectionSave)
}

// ---- author studio ----

func (h *Handler) studioArticles(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Author)
	if !ok {
		return
	}
	as, err := h.Editorial.MyArticles(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.studio.title"), false)
	if ok {
		h.render(w, r, http.StatusOK, screens.StudioArticlesPage(s, as))
	}
}

func (h *Handler) studioArticle(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Author)
	if !ok {
		return
	}
	v := screens.ArticleForm{New: r.PathValue("id") == "", A: editorial.Article{Kind: "article", Status: "draft"}}
	title := h.Dict.T("ui.studio.new")
	if !v.New {
		a, err := h.Editorial.GetArticle(r.Context(), r.PathValue("id"))
		if err != nil || (a.AuthorID != p.UserID && !p.Editor) {
			h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
			return
		}
		v.A, title = a, a.Title
	}
	s, ok := h.cabinetShell(w, r, title, false)
	if !ok {
		return
	}
	v.CSRF = s.Viewer.CSRF
	h.render(w, r, http.StatusOK, screens.StudioArticlePage(s, v))
}

func (h *Handler) studioArticleSave(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Author)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, articleFormMax)
	if err := r.ParseForm(); err != nil || !h.Commerce.Cabinet.checkCSRF(r) {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return
	}
	version, _ := strconv.Atoi(r.PostFormValue("version"))
	a := editorial.Article{ID: r.PathValue("id"), Title: r.PostFormValue("title"), Slug: r.PostFormValue("slug"),
		Kind: r.PostFormValue("kind"), Excerpt: r.PostFormValue("excerpt"), Body: r.PostFormValue("body"), Version: version}
	if len(p.Sellers) > 0 {
		a.SellerID = p.Sellers[0].ID
	}
	back := "/studio/articles/new"
	if a.ID != "" {
		back = screens.ArticleHref(a.ID)
	}
	saved, err := h.Editorial.SaveArticle(r.Context(), a, p.UserID, r.PostFormValue("submit") == "1")
	switch {
	case errors.Is(err, editorial.ErrConflict):
		failed(w, r, back, "ui.seller.conflict")
	case errors.Is(err, editorial.ErrInvalid):
		failed(w, r, back, "ui.studio.invalid")
	case errors.Is(err, editorial.ErrDenied), errors.Is(err, editorial.ErrNotFound):
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
	case err != nil:
		h.Log.Error("article save", "err", err)
		failed(w, r, back, "ui.form.failed")
	case r.PostFormValue("submit") == "1":
		done(w, r, screens.ArticleHref(saved.ID), "ui.studio.submitted")
	default:
		done(w, r, screens.ArticleHref(saved.ID), "ui.form.saved")
	}
}

// ---- partner ----

func (h *Handler) partner(w http.ResponseWriter, r *http.Request) (cabinet.Principal, bool) {
	p, ok := h.requireRole(w, r, cabinet.Partner)
	if ok && p.Partner == nil {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return p, false
	}
	return p, ok
}

func (h *Handler) partnerHome(w http.ResponseWriter, r *http.Request) {
	p, ok := h.partner(w, r)
	if !ok {
		return
	}
	c := h.Commerce.Cabinet
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		if err := c.Roles.SavePartner(r.Context(), p.Partner.ID, r.PostFormValue("payout_method")); err != nil {
			failed(w, r, "/partner", errKey(err))
			return
		}
		done(w, r, "/partner", "ui.form.saved")
		return
	}
	v := screens.PartnerView{Partner: *p.Partner, PayoutMethod: p.Partner.PayoutMethod}
	base := strings.TrimRight(h.BaseURL, "/")
	if base == "" {
		base = "http://" + r.Host
	}
	v.Link = base + "/?ref=" + p.Partner.Code
	var err error
	if v.Balance, err = c.Roles.Balance(r.Context(), "partner", p.Partner.ID); err != nil {
		h.fail(w, r, err)
		return
	}
	v.Orders, _ = c.Roles.PartnerOrders(r.Context(), p.Partner.ID, 20)
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.partner.title"), false)
	if !ok {
		return
	}
	v.CSRF = s.Viewer.CSRF
	h.render(w, r, http.StatusOK, screens.PartnerPage(s, v))
}

func (h *Handler) partnerPayouts(w http.ResponseWriter, r *http.Request) {
	p, ok := h.partner(w, r)
	if ok {
		h.finance(w, r, p, "partner", p.Partner.ID, "/partner/payouts")
	}
}

// ---- editor ----

func (h *Handler) editorShell(w http.ResponseWriter, r *http.Request, titleKey string) (screens.Shell, bool) {
	return h.cabinetShell(w, r, h.Dict.T(titleKey), false)
}

func (h *Handler) editorHome(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok {
		return
	}
	n, err := h.Editorial.Counts(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.editorShell(w, r, "ui.editor.title")
	if ok {
		h.render(w, r, http.StatusOK, screens.EditorHomePage(s, screens.EditorHome{Counts: n}))
	}
}

func queueOf(path string) string { return strings.TrimPrefix(path, "/editor/") }

func (h *Handler) editorQueue(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok {
		return
	}
	q := queueOf(r.URL.Path)
	st := r.URL.Query().Get("status")
	if len(st) > 20 {
		st = ""
	}
	v := screens.ModView{Status: st}
	var err error
	ctx := r.Context()
	switch q {
	case "products":
		v.Products, err = h.Editorial.ProductQueue(ctx)
	case "articles":
		v.Articles, err = h.Editorial.Articles(ctx, or(st, "in_review"))
		v.Status = or(st, "in_review")
	case "reviews":
		v.Reviews, err = h.Editorial.Reviews(ctx, or(st, "pending"))
		v.Status = or(st, "pending")
	case "applications":
		v.Applications, err = h.Commerce.Cabinet.Roles.Applications(ctx, or(st, "pending"))
		v.Status = or(st, "pending")
	case "payouts":
		v.Payouts, err = h.Commerce.Cabinet.Roles.AllPayouts(ctx, st)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.editorShell(w, r, "ui.editor."+q)
	if !ok {
		return
	}
	v.CSRF = s.Viewer.CSRF
	var page = map[string]func(screens.Shell, screens.ModView) templ.Component{
		"products": screens.EditorProductsPage, "articles": screens.EditorArticlesPage, "reviews": screens.EditorReviewsPage,
		"applications": screens.EditorApplicationsPage, "payouts": screens.EditorPayoutsPage,
	}
	h.render(w, r, http.StatusOK, page[q](s, v))
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (h *Handler) editorDecide(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Editor)
	if !ok || !h.parseForm(w, r) {
		return
	}
	q := queueOf(r.URL.Path)
	back := "/editor/" + q
	if ref := r.Referer(); ref != "" && strings.Contains(ref, back+"?") {
		back = back + "?" + strings.SplitN(ref, "?", 2)[1]
	}
	id, status, note := r.PostFormValue("id"), r.PostFormValue("status"), r.PostFormValue("note")
	ctx := r.Context()
	var err error
	switch q {
	case "products":
		err = h.Editorial.ModerateProduct(ctx, id, status, note)
	case "articles":
		err = h.Editorial.ModerateArticle(ctx, id, status, note)
	case "reviews":
		err = h.Editorial.ModerateReview(ctx, id, status, note)
	case "applications":
		err = h.Commerce.Cabinet.Roles.Decide(ctx, id, status == "approved", p.UserID, note)
	case "payouts":
		err = h.Commerce.Cabinet.Roles.MarkPayout(ctx, id, status, note)
	}
	if err != nil {
		if !errors.Is(err, editorial.ErrInvalid) && !errors.Is(err, cabinet.ErrInvalid) {
			h.Log.Warn("editor decide", "queue", q, "err", err)
		}
		key := "ui.editor.failed"
		switch {
		case errors.Is(err, editorial.ErrTaxProfile):
			key = "ui.editor.vat_missing"
		case errors.Is(err, editorial.ErrNoRelease):
			key = "ui.editor.release_missing"
		}
		failed(w, r, back, key)
		return
	}
	done(w, r, back, "ui.editor.done")
}

func (h *Handler) editorPages(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok {
		return
	}
	ps, err := h.Editorial.Pages(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.editorShell(w, r, "ui.editor.pages")
	if ok {
		h.render(w, r, http.StatusOK, screens.EditorPagesPage(s, screens.ModView{Pages: ps}))
	}
}

func (h *Handler) editorPage(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok {
		return
	}
	v := screens.PageForm{New: r.PathValue("slug") == "", P: editorial.Page{Kind: "legal", Version: "1", Status: "draft"}}
	if !v.New {
		pg, err := h.Editorial.Page(r.Context(), r.PathValue("slug"))
		if err != nil {
			h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
			return
		}
		v.P = pg
	}
	s, ok := h.editorShell(w, r, "ui.editor.pages")
	if !ok {
		return
	}
	v.CSRF = s.Viewer.CSRF
	h.render(w, r, http.StatusOK, screens.EditorPageEdit(s, v))
}

func (h *Handler) editorPageSave(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Editor)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, articleFormMax)
	if err := r.ParseForm(); err != nil || !h.Commerce.Cabinet.checkCSRF(r) {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		slug = strings.TrimSpace(r.PostFormValue("slug"))
	}
	pg := editorial.Page{Slug: slug, Title: r.PostFormValue("title"), Body: r.PostFormValue("body"), Kind: r.PostFormValue("kind"),
		Version: r.PostFormValue("version"), Status: r.PostFormValue("status")}
	back := "/editor/pages/new"
	if r.PathValue("slug") != "" {
		back = "/editor/pages/" + slug
	}
	if err := h.Editorial.SavePage(r.Context(), pg, p.UserID); err != nil {
		failed(w, r, back, "ui.form.invalid")
		return
	}
	done(w, r, "/editor/pages/"+slug, "ui.form.saved")
}

func (h *Handler) editorCollections(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok {
		return
	}
	cs, err := h.Editorial.Collections(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.editorShell(w, r, "ui.editor.collections")
	if ok {
		h.render(w, r, http.StatusOK, screens.EditorCollectionsPage(s, screens.ModView{Collections: cs}))
	}
}

// productSlugs maps published product ids to slugs for the collection form.
func (h *Handler) productSlugs(r *http.Request) map[string]string {
	byID := map[string]string{}
	sf, err := h.Storefronts.Storefront(r.Context(), h.StorefrontCode)
	if err != nil {
		return byID
	}
	ps, err := h.Catalog.Products(r.Context(), sf.Currency, catalog.Filter{Limit: 5000})
	if err != nil {
		return byID
	}
	for _, p := range ps {
		byID[p.ID] = p.Slug
	}
	return byID
}

func (h *Handler) editorCollection(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok {
		return
	}
	v := screens.CollectionForm{New: r.PathValue("slug") == "", C: editorial.Collection{Tone: "muted", Status: "draft"}}
	if !v.New {
		c, err := h.Editorial.Collection(r.Context(), r.PathValue("slug"))
		if err != nil {
			h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
			return
		}
		v.C = c
		byID := h.productSlugs(r)
		var lines []string
		for _, id := range c.ProductIDs {
			lines = append(lines, or(byID[id], id))
		}
		v.Products = strings.Join(lines, "\n")
	}
	s, ok := h.editorShell(w, r, "ui.editor.collections")
	if !ok {
		return
	}
	v.CSRF = s.Viewer.CSRF
	h.render(w, r, http.StatusOK, screens.EditorCollectionEdit(s, v))
}

func (h *Handler) editorCollectionSave(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok || !h.parseForm(w, r) {
		return
	}
	slug := r.PathValue("slug")
	if slug == "" {
		slug = strings.TrimSpace(r.PostFormValue("slug"))
		if slug == "" {
			slug = cabinet.Slugify(r.PostFormValue("title"))
		}
	}
	ids := strings.Fields(r.PostFormValue("products")) // slugs or ids, resolved by the store
	sort, _ := strconv.Atoi(r.PostFormValue("sort"))
	c := editorial.Collection{Slug: slug, Title: r.PostFormValue("title"), Eyebrow: r.PostFormValue("eyebrow"), Lead: r.PostFormValue("lead"),
		Cta: r.PostFormValue("cta"), Tone: r.PostFormValue("tone"), Status: r.PostFormValue("status"), ProductIDs: ids, Sort: sort}
	if old, err := h.Editorial.Collection(r.Context(), slug); err == nil {
		c.ID = old.ID
	}
	back := "/editor/collections/new"
	if r.PathValue("slug") != "" {
		back = "/editor/collections/" + slug
	}
	if err := h.Editorial.SaveCollection(r.Context(), c); err != nil {
		failed(w, r, back, "ui.form.invalid")
		return
	}
	done(w, r, "/editor/collections/"+slug, "ui.form.saved")
}
