package web

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/upload"
	"github.com/fastygo/market/internal/web/screens"
)

// releasesOn reports whether vendors upload release files themselves
// (local file storage); with S3 the operator uploads them.
func (h *Handler) releasesOn() bool {
	return h.Commerce != nil && h.Commerce.Cabinet != nil && h.Commerce.Cabinet.Releases != nil && h.Commerce.Cabinet.FilesDir != ""
}

// releaseName keeps a safe base file name: letters, digits, dot, dash and
// underscore; it must be a .zip.
func releaseName(name string) (string, bool) {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	return out, strings.HasSuffix(strings.ToLower(out), ".zip") && len(out) > 4
}

// sellerReleaseUpload stores a release archive under FilesDir and adds a
// draft release (ADR-0022 limits: size, entries, ratio, no executables).
func (h *Handler) sellerReleaseUpload(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok {
		return
	}
	id := r.PathValue("id")
	back := screens.SellerItemHref(id)
	if !h.releasesOn() {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	lim := upload.DefaultLimits
	r.Body = http.MaxBytesReader(w, r.Body, lim.MaxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		failed(w, r, back, "ui.release.too_large")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !h.Commerce.Cabinet.checkCSRF(r) {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return
	}
	rel := h.Commerce.Cabinet.Releases
	sellerID, err := rel.ReleaseOwner(r.Context(), p.UserID, id)
	if err != nil {
		failed(w, r, back, itemErrKey(err))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		failed(w, r, back, "ui.release.not_zip")
		return
	}
	defer func() { _ = f.Close() }()
	name, ok := releaseName(hdr.Filename)
	if !ok {
		failed(w, r, back, "ui.release.not_zip")
		return
	}
	key, sum, size, errKey := h.storeRelease(sellerID, id, name, f, lim)
	if errKey != "" {
		failed(w, r, back, errKey)
		return
	}
	_, err = rel.AddRelease(r.Context(), p.UserID, id, authoring.NewRelease{Version: r.FormValue("version"),
		Changelog: r.FormValue("changelog"), StorageKey: key, SHA256: sum, Size: size})
	if err != nil {
		_ = os.Remove(filepath.Join(h.Commerce.Cabinet.FilesDir, filepath.FromSlash(key)))
		switch {
		case errors.Is(err, authoring.ErrInvalid):
			failed(w, r, back, "ui.release.invalid")
		case errors.Is(err, authoring.ErrConflict):
			failed(w, r, back, "ui.release.duplicate")
		default:
			failed(w, r, back, itemErrKey(err))
		}
		return
	}
	done(w, r, back, "ui.release.uploaded")
}

// storeRelease copies the upload to a temp file, checks the archive and
// moves it to <FilesDir>/<seller>/<item>/<sha prefix>/<name>.
func (h *Handler) storeRelease(sellerID, itemID, name string, src io.Reader, lim upload.Limits) (key, sum string, size int64, errKey string) {
	base := h.Commerce.Cabinet.FilesDir
	dir := filepath.Join(base, sellerID, itemID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		h.Log.Error("release dir", "err", err)
		return "", "", 0, "ui.form.failed"
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		h.Log.Error("release temp", "err", err)
		return "", "", 0, "ui.form.failed"
	}
	keep := false
	defer func() {
		_ = tmp.Close()
		if !keep {
			_ = os.Remove(tmp.Name())
		}
	}()
	hs := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hs), io.LimitReader(src, lim.MaxFileBytes+1))
	if err != nil {
		return "", "", 0, "ui.form.failed"
	}
	if n > lim.MaxFileBytes {
		return "", "", 0, "ui.release.too_large"
	}
	if err := upload.CheckZip(tmp, n, lim); err != nil {
		return "", "", 0, "ui.release.bad_zip"
	}
	sum = hex.EncodeToString(hs.Sum(nil))
	key = sellerID + "/" + itemID + "/" + sum[:16] + "/" + name
	final := filepath.Join(base, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(final), 0o750); err != nil {
		return "", "", 0, "ui.form.failed"
	}
	if err := tmp.Close(); err != nil {
		return "", "", 0, "ui.form.failed"
	}
	if err := os.Rename(tmp.Name(), final); err != nil {
		h.Log.Error("release move", "err", err)
		return "", "", 0, "ui.form.failed"
	}
	keep = true
	return key, sum, n, ""
}

func (h *Handler) sellerReleasePublish(w http.ResponseWriter, r *http.Request) {
	p, _, ok := h.vendor(w, r)
	if !ok || !h.parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	back := screens.SellerItemHref(id)
	if !h.releasesOn() {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err := h.Commerce.Cabinet.Releases.PublishRelease(r.Context(), p.UserID, id, r.PathValue("rid")); err != nil {
		failed(w, r, back, itemErrKey(err))
		return
	}
	done(w, r, back, "ui.release.published")
}
