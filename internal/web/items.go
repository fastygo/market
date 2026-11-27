package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/content"
	"github.com/fastygo/market/internal/web/screens"
)

const itemFormMax = content.MaxMarkdown + (64 << 10) // cap plus form overhead

// accountItems and accountItemEdit moved to the vendor workspace; the
// old URLs redirect (the EditorY save and upload API stays here).
func (h *Handler) accountItems(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/seller/items", http.StatusMovedPermanently)
}

func (h *Handler) accountItemEdit(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, screens.SellerItemHref(r.PathValue("id")), http.StatusMovedPermanently)
}

func (h *Handler) editorProps(w http.ResponseWriter, r *http.Request, it authoring.Item) map[string]any {
	id := it.ID
	return map[string]any{
		"labels":       EditorLabels(h.Dict.T),
		"title":        it.Title,
		"csrf":         h.Commerce.Cabinet.csrf(w, r),
		"version":      it.Version,
		"saveUrl":      screens.ItemEditHref(id),
		"uploadUrl":    screens.ItemEditHref(id) + "/images",
		"conflict":     h.Dict.T("ui.seller.conflict_short"),
		"uploadFailed": h.Dict.T("ui.seller.upload_failed"),
		"uploading":    h.Dict.T("ui.seller.uploading"),
	}
}

func (h *Handler) loadItem(w http.ResponseWriter, r *http.Request, b Buyer) (authoring.Item, bool) {
	it, err := h.Commerce.Cabinet.Items.Get(r.Context(), b.ID, r.PathValue("id"))
	if errors.Is(err, authoring.ErrNotFound) || errors.Is(err, authoring.ErrDenied) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return authoring.Item{}, false
	}
	if err != nil {
		h.fail(w, r, err)
		return authoring.Item{}, false
	}
	return it, true
}

func (h *Handler) accountItemSave(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, itemFormMax)
	if err := r.ParseForm(); err != nil || !h.Commerce.Cabinet.checkCSRF(r) {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return
	}
	version, ok := authoring.ParseETag(r.PostFormValue("version"))
	if !ok {
		// hidden field is a bare integer
		version, ok = authoring.ParseETag(strings.TrimSpace(r.PostFormValue("version")))
	}
	if !ok && r.PostFormValue("version") == "0" {
		ok, version = true, 0
	}
	it, err := h.Commerce.Cabinet.Items.SaveDescription(r.Context(), b.ID, r.PathValue("id"), r.PostFormValue("description"), version)
	if !h.itemWriteErr(w, r, err) {
		return
	}
	done(w, r, screens.SellerItemHref(it.ID), "ui.seller.saved")
}

func (h *Handler) accountItemAutosave(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	if !h.apiCSRF(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, itemFormMax)
	var body struct {
		Description string `json:"description"`
		Version     int    `json:"version"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_json"})
		return
	}
	if v, ok := authoring.ParseETag(r.Header.Get("If-Match")); ok {
		body.Version = v
	}
	it, err := h.Commerce.Cabinet.Items.SaveDescription(r.Context(), b.ID, r.PathValue("id"), body.Description, body.Version)
	if err != nil {
		h.itemAPIErr(w, r, b, err)
		return
	}
	w.Header().Set("ETag", authoring.ETag(it.Version))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": it.Version, "etag": authoring.ETag(it.Version)})
}

func (h *Handler) accountItemImage(w http.ResponseWriter, r *http.Request) {
	b, ok := h.requireBuyer(w, r)
	if !ok {
		return
	}
	if !h.apiCSRF(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, authoring.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(authoring.MaxImageBytes + (1 << 20)); err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no_file"})
		return
	}
	defer f.Close()
	img, err := h.Commerce.Cabinet.Items.AddImage(r.Context(), b.ID, r.PathValue("id"), hdr.Filename, io.LimitReader(f, authoring.MaxImageBytes+1))
	if err != nil {
		h.itemAPIErr(w, r, b, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": img.URL, "name": img.Name})
}

// apiCSRF requires a same-origin request and the CSRF header or form token.
func (h *Handler) apiCSRF(w http.ResponseWriter, r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" || o == "null" || !h.Commerce.Cabinet.checkCSRF(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "csrf"})
		return false
	}
	return true
}

func (h *Handler) itemWriteErr(w http.ResponseWriter, r *http.Request, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, authoring.ErrNotFound), errors.Is(err, authoring.ErrDenied):
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
	case errors.Is(err, authoring.ErrConflict):
		h.notice(w, r, http.StatusConflict, "ui.seller.conflict")
	case errors.Is(err, authoring.ErrTooLarge):
		h.notice(w, r, http.StatusRequestEntityTooLarge, "ui.seller.too_large")
	case errors.Is(err, authoring.ErrInvalid):
		h.notice(w, r, http.StatusBadRequest, "ui.seller.invalid")
	default:
		h.fail(w, r, err)
	}
	return false
}

func (h *Handler) itemAPIErr(w http.ResponseWriter, r *http.Request, b Buyer, err error) {
	switch {
	case errors.Is(err, authoring.ErrNotFound), errors.Is(err, authoring.ErrDenied):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
	case errors.Is(err, authoring.ErrConflict):
		it, _ := h.Commerce.Cabinet.Items.Get(r.Context(), b.ID, r.PathValue("id"))
		w.Header().Set("ETag", authoring.ETag(it.Version))
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "conflict", "version": it.Version, "etag": authoring.ETag(it.Version),
			"description": it.Description,
		})
	case errors.Is(err, authoring.ErrTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "too_large"})
	case errors.Is(err, authoring.ErrBadImage):
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "bad_image"})
	case errors.Is(err, authoring.ErrQuota):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "quota"})
	case errors.Is(err, authoring.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid"})
	default:
		h.Log.Error("web", "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
	}
}
