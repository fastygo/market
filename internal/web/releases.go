package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/upload"
	"github.com/fastygo/market/internal/web/screens"
)

// Release uploads (ADR-0036): the vendor uploads a .zip on the item page;
// it is streamed to a temp file on disk (hashing as it goes, never held in
// memory), checked (size, zip bomb, unsafe paths) and inspected (file
// listing, executable and secret flags), stored under
// <seller-slug>/<item-slug>/<item-slug>-<version>.zip in local files or
// S3, and added in review. A moderator (editor role) publishes or rejects
// it; buyers only ever download published releases.

// releasesOn reports whether vendors upload release files.
func (h *Handler) releasesOn() bool {
	c := h.Commerce
	return c != nil && c.Cabinet != nil && c.Cabinet.Releases != nil && c.Cabinet.ReleaseFiles != nil
}

// releaseUploadTime is how long one release upload may take (the server
// read and write deadlines are extended for this request only).
const releaseUploadTime = 30 * time.Minute

// releaseFieldMax caps each small form field of an upload.
const releaseFieldMax = 16 << 10

var releaseVersionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,31}$`)

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

// keySegment is one safe storage key segment: lower-case letters, digits,
// dot, dash and underscore, no leading or trailing dot or dash, at most 64
// characters; fallback when nothing is left.
func keySegment(s, fallback string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if len(out) > 64 {
		out = strings.Trim(out[:64], ".-")
	}
	if out == "" {
		return fallback
	}
	return out
}

// releaseKey is <seller-slug>/<item-slug>/<item-slug>-<version>.zip; the
// file name is also what the buyer's browser saves.
func releaseKey(t authoring.ReleaseTarget, itemID, version string) string {
	seller := keySegment(t.SellerSlug, keySegment(t.SellerID, "seller"))
	item := keySegment(t.ItemSlug, keySegment(itemID, "item"))
	return seller + "/" + item + "/" + item + "-" + keySegment(version, "0") + ".zip"
}

// busyKeys refuses a second upload of the same key while the first one is
// stored (one process per pb_data, HANDOFF.md), so a failed duplicate can
// never delete the object of the winner.
var busyKeys = struct {
	sync.Mutex
	m map[string]bool
}{m: map[string]bool{}}

func claimKey(k string) bool {
	busyKeys.Lock()
	defer busyKeys.Unlock()
	if busyKeys.m[k] {
		return false
	}
	busyKeys.m[k] = true
	return true
}

func freeKey(k string) {
	busyKeys.Lock()
	delete(busyKeys.m, k)
	busyKeys.Unlock()
}

// versionChecker is the optional cheap duplicate check of the store.
type versionChecker interface {
	VersionTaken(ctx context.Context, itemID, version string) bool
}

func (h *Handler) uploadDir() string {
	if d := h.Commerce.Cabinet.UploadDir; d != "" {
		return d
	}
	return os.TempDir()
}

// readField reads a small multipart field (at most releaseFieldMax).
func readField(p *multipart.Part) (string, bool) {
	b, err := io.ReadAll(io.LimitReader(p, releaseFieldMax+1))
	if err != nil || len(b) > releaseFieldMax {
		return "", false
	}
	return string(b), true
}

// sellerReleaseUpload receives one release archive (POST multipart:
// csrf, version, changelog, then file; the browser sends fields in form
// order, so the file is only read after the CSRF token, the membership
// and the version are checked).
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
	c := h.Commerce.Cabinet
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(releaseUploadTime))
	_ = rc.SetWriteDeadline(time.Now().Add(releaseUploadTime + time.Minute))
	lim := upload.DefaultLimits
	r.Body = http.MaxBytesReader(w, r.Body, lim.MaxFileBytes+(1<<20))
	mr, err := r.MultipartReader()
	if err != nil {
		failed(w, r, back, "ui.release.not_zip")
		return
	}
	fields := map[string]string{}
	var (
		tmp     *os.File
		size    int64
		sum     string
		target  authoring.ReleaseTarget
		version string
	)
	defer func() {
		if tmp != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	csrfOK := func() bool { return c.checkCSRFToken(r, fields["csrf"]) }
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			failed(w, r, back, tooLargeKey(err))
			return
		}
		if part.FileName() == "" {
			v, ok := readField(part)
			if !ok {
				failed(w, r, back, "ui.release.invalid")
				return
			}
			fields[part.FormName()] = v
			continue
		}
		if part.FormName() != "file" || tmp != nil {
			_, _ = io.Copy(io.Discard, part)
			continue
		}
		if !csrfOK() {
			h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
			return
		}
		version = strings.TrimSpace(fields["version"])
		if !releaseVersionRe.MatchString(version) {
			failed(w, r, back, "ui.release.invalid")
			return
		}
		if target, err = c.Releases.ReleaseTarget(r.Context(), p.UserID, id); err != nil {
			failed(w, r, back, itemErrKey(err))
			return
		}
		if vc, ok := c.Releases.(versionChecker); ok && vc.VersionTaken(r.Context(), id, version) {
			failed(w, r, back, "ui.release.duplicate")
			return
		}
		if _, ok := releaseName(part.FileName()); !ok {
			failed(w, r, back, "ui.release.not_zip")
			return
		}
		if err := os.MkdirAll(h.uploadDir(), 0o750); err != nil {
			h.Log.Error("release upload dir", "err", err)
			failed(w, r, back, "ui.form.failed")
			return
		}
		if tmp, err = os.CreateTemp(h.uploadDir(), ".release-*"); err != nil {
			h.Log.Error("release temp", "err", err)
			failed(w, r, back, "ui.form.failed")
			return
		}
		hs := sha256.New()
		n, err := io.Copy(io.MultiWriter(tmp, hs), io.LimitReader(part, lim.MaxFileBytes+1))
		if err != nil {
			failed(w, r, back, tooLargeKey(err))
			return
		}
		if n > lim.MaxFileBytes {
			failed(w, r, back, "ui.release.too_large")
			return
		}
		size, sum = n, hex.EncodeToString(hs.Sum(nil))
	}
	if !csrfOK() {
		h.notice(w, r, http.StatusForbidden, "ui.account.csrf")
		return
	}
	if tmp == nil || size == 0 {
		failed(w, r, back, "ui.release.not_zip")
		return
	}
	rep, err := upload.Inspect(tmp, size, lim)
	if err != nil {
		failed(w, r, back, "ui.release.bad_zip")
		return
	}
	manifest, _ := json.Marshal(rep)
	key := releaseKey(target, id, version)
	if !claimKey(key) {
		failed(w, r, back, "ui.release.duplicate")
		return
	}
	defer freeKey(key)
	if err := c.ReleaseFiles.Put(r.Context(), key, tmp, size, sum); err != nil {
		if errors.Is(err, ErrObjectExists) {
			failed(w, r, back, "ui.release.duplicate")
			return
		}
		h.Log.Error("release store", "mode", c.ReleaseFiles.Mode(), "key", key, "err", err)
		failed(w, r, back, "ui.release.store_failed")
		return
	}
	_, err = c.Releases.AddRelease(r.Context(), p.UserID, id, authoring.NewRelease{Version: version,
		Changelog: fields["changelog"], StorageKey: key, SHA256: sum, Size: size, Manifest: manifest, Flags: len(rep.Flags)})
	if err != nil {
		// the row is the reference to the object: without it the object
		// is an orphan
		if derr := c.ReleaseFiles.Delete(r.Context(), key); derr != nil {
			h.Log.Error("release orphan delete", "key", key, "err", derr)
		}
		switch {
		case errors.Is(err, authoring.ErrInvalid):
			failed(w, r, back, "ui.release.invalid")
		case errors.Is(err, authoring.ErrConflict):
			failed(w, r, back, "ui.release.duplicate")
		case errors.Is(err, authoring.ErrQuota):
			failed(w, r, back, "ui.release.quota")
		default:
			h.Log.Error("release add", "err", err)
			failed(w, r, back, itemErrKey(err))
		}
		return
	}
	h.Log.Info("release uploaded", "item", id, "version", version, "size", size, "flags", len(rep.Flags), "mode", c.ReleaseFiles.Mode())
	done(w, r, back, "ui.release.uploaded")
}

func tooLargeKey(err error) string {
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		return "ui.release.too_large"
	}
	return "ui.form.failed"
}

// sellerReleaseYank withdraws a published release.
func (h *Handler) sellerReleaseYank(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Commerce.Cabinet.Releases.YankRelease(r.Context(), p.UserID, id, r.PathValue("rid")); err != nil {
		if errors.Is(err, authoring.ErrLastRelease) {
			failed(w, r, back, "ui.release.last_published")
			return
		}
		failed(w, r, back, itemErrKey(err))
		return
	}
	done(w, r, back, "ui.release.yanked")
}

// sellerReleaseDelete removes an unpublished release and its file.
func (h *Handler) sellerReleaseDelete(w http.ResponseWriter, r *http.Request) {
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
	key, err := h.Commerce.Cabinet.Releases.DeleteRelease(r.Context(), p.UserID, id, r.PathValue("rid"))
	if err != nil {
		failed(w, r, back, itemErrKey(err))
		return
	}
	if key != "" {
		if err := h.Commerce.Cabinet.ReleaseFiles.Delete(r.Context(), key); err != nil {
			h.Log.Error("release file delete", "key", key, "err", err)
		}
	}
	done(w, r, back, "ui.release.deleted")
}

// ---- moderation (editor role) ----

func (h *Handler) moderationOn(w http.ResponseWriter, r *http.Request) bool {
	if h.Commerce == nil || h.Commerce.Cabinet == nil || h.Commerce.Cabinet.Moderation == nil {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return false
	}
	return true
}

func (h *Handler) editorReleases(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok || !h.moderationOn(w, r) {
		return
	}
	st := r.URL.Query().Get("status")
	if st == "" {
		st = authoring.ReleaseInReview
	}
	list, err := h.Commerce.Cabinet.Moderation.ReleaseQueue(r.Context(), st)
	if errors.Is(err, authoring.ErrInvalid) {
		st = authoring.ReleaseInReview
		list, err = h.Commerce.Cabinet.Moderation.ReleaseQueue(r.Context(), st)
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	s, ok := h.editorShell(w, r, "ui.editor.releases")
	if ok {
		h.render(w, r, http.StatusOK, screens.EditorReleasesPage(s, screens.ReleaseQueueView{Releases: list, Status: st}))
	}
}

func (h *Handler) editorRelease(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireRole(w, r, cabinet.Editor); !ok || !h.moderationOn(w, r) {
		return
	}
	rel, err := h.Commerce.Cabinet.Moderation.ReleaseForReview(r.Context(), r.PathValue("id"))
	if errors.Is(err, authoring.ErrNotFound) {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var rep upload.Report
	if len(rel.Manifest) > 0 {
		_ = json.Unmarshal(rel.Manifest, &rep)
	}
	s, ok := h.editorShell(w, r, "ui.editor.release_review")
	if ok {
		h.render(w, r, http.StatusOK, screens.EditorReleasePage(s, screens.ReleaseReviewView{R: rel, Report: rep, CSRF: s.Viewer.CSRF}))
	}
}

func (h *Handler) editorReleaseDecide(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Editor)
	if !ok || !h.moderationOn(w, r) || !h.parseForm(w, r) {
		return
	}
	id := r.PathValue("id")
	back := screens.EditorReleaseHref(id)
	decision, note := r.PostFormValue("status"), r.PostFormValue("note")
	err := h.Commerce.Cabinet.Moderation.ModerateRelease(r.Context(), id, decision, note, p.Email)
	if err != nil {
		key := "ui.editor.failed"
		if errors.Is(err, authoring.ErrInvalid) && decision == authoring.ReleaseRejected && strings.TrimSpace(note) == "" {
			key = "ui.release.note_required"
		}
		if !errors.Is(err, authoring.ErrInvalid) {
			h.Log.Warn("release moderate", "id", id, "err", err)
		}
		failed(w, r, back, key)
		return
	}
	h.Log.Info("release moderated", "id", id, "decision", decision, "by", p.UserID)
	done(w, r, "/editor/releases", "ui.release.decided_"+decision)
}

func (h *Handler) editorReleaseFile(w http.ResponseWriter, r *http.Request) {
	p, ok := h.requireRole(w, r, cabinet.Editor)
	if !ok || !h.moderationOn(w, r) || h.Commerce.Cabinet.ReleaseFiles == nil {
		return
	}
	rel, err := h.Commerce.Cabinet.Moderation.ReleaseForReview(r.Context(), r.PathValue("id"))
	if err != nil || rel.StorageKey == "" {
		h.notice(w, r, http.StatusNotFound, "ui.error.not_found")
		return
	}
	h.Log.Info("release file opened by moderator", "id", rel.ID, "by", p.UserID)
	h.Commerce.Cabinet.ReleaseFiles.Serve(w, r, rel.StorageKey)
}
