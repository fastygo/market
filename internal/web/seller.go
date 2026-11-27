package web

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/web/screens"
)

// mountRoles mounts the profile, roles and the four role workspaces.
func (h *Handler) mountRoles(m *http.ServeMux) {
	m.HandleFunc("GET /account/profile", h.accountProfile)
	m.HandleFunc("POST /account/profile", h.accountProfile)
	m.HandleFunc("GET /account/security", h.accountSecurity)
	m.HandleFunc("POST /account/security", h.accountSecurity)
	m.HandleFunc("GET /account/roles", h.accountRoles)
	m.HandleFunc("POST /account/roles", h.accountRoles)
	if h.Commerce.Cabinet.Items != nil {
		m.HandleFunc("GET /seller", h.sellerToday)
		m.HandleFunc("GET /seller/items", h.sellerItems)
		m.HandleFunc("GET /seller/items/new", h.sellerItemNew)
		m.HandleFunc("POST /seller/items/new", h.sellerItemCreate)
		m.HandleFunc("GET /seller/items/{id}", h.sellerItem)
		m.HandleFunc("POST /seller/items/{id}", h.sellerItemSave)
		m.HandleFunc("POST /seller/items/{id}/status", h.sellerItemStatus)
		m.HandleFunc("POST /seller/items/{id}/cover", h.sellerItemUpload)
		m.HandleFunc("POST /seller/items/{id}/gallery", h.sellerItemUpload)
		m.HandleFunc("POST /seller/items/{id}/releases", h.sellerReleaseUpload)
		m.HandleFunc("POST /seller/items/{id}/releases/{rid}/publish", h.sellerReleasePublish)
		m.HandleFunc("GET /seller/media", h.sellerMedia)
		m.HandleFunc("POST /seller/media", h.sellerMediaUpload)
		m.HandleFunc("GET /seller/finance", h.sellerFinance)
		m.HandleFunc("POST /seller/finance", h.sellerFinance)
		m.HandleFunc("GET /seller/reviews", h.sellerReviews)
		m.HandleFunc("GET /seller/team", h.sellerTeam)
		m.HandleFunc("POST /seller/team", h.sellerTeam)
		m.HandleFunc("POST /seller/team/remove", h.sellerTeamRemove)
		m.HandleFunc("GET /seller/store", h.sellerStore)
		m.HandleFunc("POST /seller/store", h.sellerStore)
	}
	m.HandleFunc("GET /partner", h.partnerHome)
	m.HandleFunc("POST /partner", h.partnerHome)
	m.HandleFunc("GET /partner/payouts", h.partnerPayouts)
	m.HandleFunc("POST /partner/payouts", h.partnerPayouts)
	if h.Editorial != nil {
		h.mountEditorial(m)
	}
}

// vendor is the signed-in member's first seller.
func (h *Handler) vendor(w http.ResponseWriter, r *http.Request) (cabinet.Principal, cabinet.SellerRef, bool) {
	p, ok := h.requireRole(w, r, cabinet.Vendor)
	if !ok || len(p.Sellers) == 0 {
		return p, cabinet.SellerRef{}, false
	}
	return p, p.Sellers[0], true
}

func (h *Handler) sellerShell(w http.ResponseWriter, r *http.Request, titleKey string) (screens.Shell, bool) {
	return h.cabinetShell(w, r, h.Dict.T(titleKey), true)
}

func (h *Handler) sellerToday(w http.ResponseWriter, r *http.Request) {
	p, sel, ok := h.vendor(w, r)
	if !ok {
		return
	}
	c := h.Commerce.Cabinet
	items, err := c.Items.List(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	v := screens.SellerHome{Seller: sel, Items: items}
	v.Balance, _ = c.Roles.Balance(r.Context(), "seller", sel.ID)
	v.Sales, _ = c.Roles.Sales(r.Context(), sel.ID, 8)
	v.Reviews = len(h.reviewsOf(r, items))
	s, ok := h.sellerShell(w, r, "ui.nav.seller.today")
	if ok {
		h.render(w, r, http.StatusOK, screens.SellerTodayPage(s, v))
	}
}

func (h *Handler) sellerItems(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	items, err := h.Commerce.Cabinet.Items.List(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.sellerShell(w, r, "ui.nav.seller.items")
	if ok {
		h.render(w, r, http.StatusOK, screens.SellerItemsPage(s, items))
	}
}

func (h *Handler) itemForm(w http.ResponseWriter, r *http.Request, it authoring.Item, isNew bool) {
	cats, err := h.Commerce.Cabinet.Items.Categories(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	title := it.Title
	if isNew {
		title = h.Dict.T("ui.seller.new_item")
	}
	s, ok := h.cabinetShell(w, r, title, true)
	if !ok {
		return
	}
	if !isNew {
		assets := loadEditorAssets()
		s.EditorCSS, s.EditorJS = assets.CSS, assets.JS
		s.EditorProps = h.editorProps(w, r, it)
	}
	form := screens.ItemForm{Item: it, Categories: cats, New: isNew, CSRF: s.Viewer.CSRF, ReleasesOn: h.releasesOn()}
	if !isNew && form.ReleasesOn {
		p, _ := h.principal(r)
		if form.Releases, err = h.Commerce.Cabinet.Releases.ItemReleases(r.Context(), p.UserID, it.ID); err != nil {
			h.fail(w, r, err)
			return
		}
	}
	h.render(w, r, http.StatusOK, screens.SellerItemPage(s, form))
}

func (h *Handler) sellerItemNew(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.vendor(w, r); ok {
		h.itemForm(w, r, authoring.Item{Status: "draft"}, true)
	}
}

func metaOf(r *http.Request) (authoring.Meta, bool) {
	m := authoring.Meta{Title: r.PostFormValue("title"), Slug: r.PostFormValue("slug"), Excerpt: r.PostFormValue("excerpt"),
		Category: r.PostFormValue("category"), DemoURL: r.PostFormValue("demo_url"), Stack: r.PostFormValue("stack"),
		Tags: r.PostFormValue("tags")}
	ok := true
	if v := r.PostFormValue("price"); v != "" {
		m.Price, ok = parseMinor(v)
	}
	if v := r.PostFormValue("extended"); v != "" && ok {
		m.Extended, ok = parseMinor(v)
	}
	return m, ok
}

func (h *Handler) sellerItemCreate(w http.ResponseWriter, r *http.Request) {
	p, sel, ok := h.vendor(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	m, ok := metaOf(r)
	if !ok {
		failed(w, r, "/seller/items/new", "ui.seller.bad_price")
		return
	}
	it, err := h.Commerce.Cabinet.Items.Create(r.Context(), p.UserID, sel.ID, m)
	if err != nil {
		failed(w, r, "/seller/items/new", itemErrKey(err))
		return
	}
	done(w, r, screens.SellerItemHref(it.ID), "ui.seller.created")
}

func itemErrKey(err error) string {
	switch {
	case errors.Is(err, authoring.ErrInvalid):
		return "ui.seller.invalid"
	case errors.Is(err, authoring.ErrDenied), errors.Is(err, authoring.ErrNotFound):
		return "ui.error.not_found"
	case errors.Is(err, authoring.ErrBadImage):
		return "ui.seller.bad_image"
	case errors.Is(err, authoring.ErrTooLarge), errors.Is(err, authoring.ErrQuota):
		return "ui.seller.too_large"
	case errors.Is(err, authoring.ErrConflict):
		return "ui.seller.conflict"
	}
	return "ui.form.failed"
}

func (h *Handler) sellerItem(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	it, ok := h.loadItem(w, r, Buyer{ID: p.UserID})
	if ok {
		h.itemForm(w, r, it, false)
	}
}

func (h *Handler) sellerItemSave(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	back := screens.SellerItemHref(r.PathValue("id"))
	m, ok := metaOf(r)
	if !ok {
		failed(w, r, back, "ui.seller.bad_price")
		return
	}
	if _, err := h.Commerce.Cabinet.Items.SaveMeta(r.Context(), p.UserID, r.PathValue("id"), m); err != nil {
		failed(w, r, back, itemErrKey(err))
		return
	}
	done(w, r, back, "ui.seller.saved")
}

func (h *Handler) sellerItemStatus(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	back := screens.SellerItemHref(r.PathValue("id"))
	if _, err := h.Commerce.Cabinet.Items.Act(r.Context(), p.UserID, r.PathValue("id"), r.PostFormValue("action")); err != nil {
		failed(w, r, back, itemErrKey(err))
		return
	}
	done(w, r, back, "ui.seller.status_changed")
}

// upload reads one multipart image (CSRF checked after parsing).
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) (string, io.Reader, func(), bool) {
	r.Body = http.MaxBytesReader(w, r.Body, authoring.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(authoring.MaxImageBytes + (1 << 20)); err != nil {
		h.notice(w, r, http.StatusRequestEntityTooLarge, "ui.seller.too_large")
		return "", nil, nil, false
	}
	if !h.Commerce.Cabinet.checkCSRF(r) {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return "", nil, nil, false
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		h.notice(w, r, http.StatusBadRequest, "ui.seller.invalid")
		return "", nil, nil, false
	}
	return hdr.Filename, io.LimitReader(f, authoring.MaxImageBytes+1), func() { _ = f.Close() }, true
}

func (h *Handler) sellerItemUpload(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	name, body, closeFn, ok := h.upload(w, r)
	if !ok {
		return
	}
	defer closeFn()
	id := r.PathValue("id")
	var err error
	if strings.HasSuffix(r.URL.Path, "/cover") {
		_, err = h.Commerce.Cabinet.Items.SetCover(r.Context(), p.UserID, id, name, body)
	} else {
		_, err = h.Commerce.Cabinet.Items.AddImage(r.Context(), p.UserID, id, name, body)
	}
	if err != nil {
		failed(w, r, screens.SellerItemHref(id), itemErrKey(err))
		return
	}
	done(w, r, screens.SellerItemHref(id), "ui.seller.uploaded")
}

func (h *Handler) sellerMedia(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	items, err := h.Commerce.Cabinet.Items.List(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.sellerShell(w, r, "ui.nav.seller.media")
	if ok {
		h.render(w, r, http.StatusOK, screens.SellerMediaPage(s, items, s.Viewer.CSRF))
	}
}

func (h *Handler) sellerMediaUpload(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	name, body, closeFn, ok := h.upload(w, r)
	if !ok {
		return
	}
	defer closeFn()
	if _, err := h.Commerce.Cabinet.Items.AddImage(r.Context(), p.UserID, r.PostFormValue("item"), name, body); err != nil {
		failed(w, r, "/seller/media", itemErrKey(err))
		return
	}
	done(w, r, "/seller/media", "ui.seller.uploaded")
}

// finance renders or posts a payout request of a seller or a partner.
func (h *Handler) finance(w http.ResponseWriter, r *http.Request, p cabinet.Principal, party, id, back string) {
	c := h.Commerce.Cabinet
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		amount, ok := parseMinor(r.PostFormValue("amount"))
		if !ok {
			failed(w, r, back, "ui.finance.bad_amount")
			return
		}
		po := cabinet.Payout{PartyType: party, Amount: amount, Note: r.PostFormValue("note")}
		if party == "partner" {
			po.PartnerID = id
		} else {
			po.SellerID = id
		}
		if err := c.Roles.RequestPayout(r.Context(), po, p.UserID); err != nil {
			failed(w, r, back, "ui.finance.bad_amount")
			return
		}
		done(w, r, back, "ui.finance.requested")
		return
	}
	v := screens.FinanceView{Party: party}
	var err error
	if v.Balance, err = c.Roles.Balance(r.Context(), party, id); err != nil {
		h.fail(w, r, err)
		return
	}
	v.Payouts, _ = c.Roles.Payouts(r.Context(), party, id)
	if party == "partner" {
		v.Sales, _ = c.Roles.PartnerOrders(r.Context(), id, 50)
	} else {
		v.Sales, _ = c.Roles.Sales(r.Context(), id, 50)
	}
	s, ok := h.cabinetShell(w, r, h.Dict.T("ui.finance.title"), party == "seller")
	if !ok {
		return
	}
	v.CSRF = s.Viewer.CSRF
	h.render(w, r, http.StatusOK, screens.FinancePage(s, v))
}

func (h *Handler) sellerFinance(w http.ResponseWriter, r *http.Request) {
	p, sel, ok := h.vendor(w, r)
	if ok {
		h.finance(w, r, p, "seller", sel.ID, "/seller/finance")
	}
}

func (h *Handler) reviewsOf(r *http.Request, items []authoring.Item) []editorial.Review {
	if h.Editorial == nil {
		return nil
	}
	var out []editorial.Review
	for _, it := range items {
		rs, err := h.Editorial.ProductReviews(r.Context(), it.ID)
		if err != nil {
			continue
		}
		for i := range rs {
			rs[i].ProductTitle, rs[i].ProductSlug = it.Title, it.Slug
		}
		out = append(out, rs...)
	}
	return out
}

func (h *Handler) sellerReviews(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	items, err := h.Commerce.Cabinet.Items.List(r.Context(), p.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.sellerShell(w, r, "ui.nav.seller.reviews")
	if ok {
		h.render(w, r, http.StatusOK, screens.SellerReviewsPage(s, h.reviewsOf(r, items)))
	}
}

func (h *Handler) sellerTeam(w http.ResponseWriter, r *http.Request) {
	p, sel, ok := h.vendor(w, r)
	if !ok {
		return
	}
	c := h.Commerce.Cabinet
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		if !sel.Owner {
			failed(w, r, "/seller/team", "ui.error.forbidden")
			return
		}
		if err := c.Roles.AddMember(r.Context(), sel.ID, r.PostFormValue("email")); err != nil {
			failed(w, r, "/seller/team", "ui.team.not_found")
			return
		}
		done(w, r, "/seller/team", "ui.team.added")
		return
	}
	ms, err := c.Roles.Team(r.Context(), sel.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.sellerShell(w, r, "ui.nav.seller.team")
	if ok {
		h.render(w, r, http.StatusOK, screens.SellerTeamPage(s, screens.TeamView{Members: ms, Owner: sel.Owner, CSRF: s.Viewer.CSRF}))
	}
	_ = p
}

func (h *Handler) sellerTeamRemove(w http.ResponseWriter, r *http.Request) {
	_, sel, ok := h.vendor(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	if !sel.Owner {
		failed(w, r, "/seller/team", "ui.error.forbidden")
		return
	}
	if err := h.Commerce.Cabinet.Roles.RemoveMember(r.Context(), sel.ID, r.PostFormValue("user")); err != nil {
		failed(w, r, "/seller/team", errKey(err))
		return
	}
	done(w, r, "/seller/team", "ui.team.removed")
}

func (h *Handler) sellerStore(w http.ResponseWriter, r *http.Request) {
	_, sel, ok := h.vendor(w, r)
	if !ok {
		return
	}
	c := h.Commerce.Cabinet
	if r.Method == http.MethodPost {
		if !h.parseForm(w, r) {
			return
		}
		if !sel.Owner {
			failed(w, r, "/seller/store", "ui.error.forbidden")
			return
		}
		err := c.Roles.SaveStore(r.Context(), sel.ID, cabinet.StoreProfile{DisplayName: r.PostFormValue("display_name"),
			Headline: r.PostFormValue("headline"), Bio: r.PostFormValue("bio"), Website: r.PostFormValue("website")})
		if err != nil {
			failed(w, r, "/seller/store", errKey(err))
			return
		}
		done(w, r, "/seller/store", "ui.form.saved")
		return
	}
	st, err := c.Roles.StoreProfile(r.Context(), sel.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.sellerShell(w, r, "ui.nav.seller.store")
	if ok {
		h.render(w, r, http.StatusOK, screens.SellerStorePage(s, screens.StoreView{Store: st, CSRF: s.Viewer.CSRF}))
	}
}
