package pb_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
	"github.com/fastygo/market/internal/platform/s3"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

// fakeS3 is a path-style bucket: it checks that every PUT is signed in
// the Authorization header and that the signed payload hash matches the
// body, the way a real storage rejects a corrupted upload.
type fakeS3 struct {
	mu         sync.Mutex
	objects    map[string][]byte
	puts       int
	failDelete bool
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-access/") ||
			!strings.Contains(r.Header.Get("Authorization"), "SignedHeaders=content-disposition;content-type;host;x-amz-content-sha256;x-amz-date") {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		b, _ := io.ReadAll(r.Body)
		sum := sha256.Sum256(b)
		if hex.EncodeToString(sum[:]) != r.Header.Get("x-amz-content-sha256") || int64(len(b)) != r.ContentLength {
			http.Error(w, "XAmzContentSHA256Mismatch", http.StatusBadRequest)
			return
		}
		f.objects[r.URL.Path] = b
		f.puts++
	case http.MethodHead, http.MethodGet:
		if r.URL.Query().Get("X-Amz-Signature") == "" {
			http.Error(w, "unsigned", http.StatusForbidden)
			return
		}
		b, ok := f.objects[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			_, _ = w.Write(b)
		}
	case http.MethodDelete:
		if f.failDelete {
			http.Error(w, "InternalError", http.StatusInternalServerError)
			return
		}
		delete(f.objects, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	}
}

func releaseZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for n, c := range files {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(c))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestReleaseUploadModerationFlow drives the vendor upload and the editor
// moderation through the real routes, with local files and with S3.
func TestReleaseUploadModerationFlow(t *testing.T) {
	for _, mode := range []string{"local", "s3"} {
		t.Run(mode, func(t *testing.T) { releaseFlow(t, mode) })
	}
}

func releaseFlow(t *testing.T, mode string) {
	app := pbtest.New(t)
	users, _ := app.FindCollectionByNameOrId("users")
	newUser := func(email string) *core.Record {
		u := core.NewRecord(users)
		u.SetEmail(email)
		u.SetPassword("test-password-123")
		u.SetVerified(true)
		if err := app.Save(u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	seller, editor, other, admin := newUser("vendor@example.com"), newUser("editor@example.com"), newUser("other@example.com"), newUser("root@example.com")
	prod, err := app.FindFirstRecordByData("products", "slug", "aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	sel, _ := app.FindRecordById("sellers", prod.GetString("seller"))
	members, _ := app.FindCollectionByNameOrId("seller_members")
	addMember := func(sellerID, userID string) {
		m := core.NewRecord(members)
		m.Set("seller", sellerID)
		m.Set("user", userID)
		m.Set("role", "owner")
		if err := app.Save(m); err != nil {
			t.Fatal(err)
		}
	}
	addMember(sel.Id, seller.Id)
	// other is a vendor of another store: a vendor, but not of this item
	sellers, _ := app.FindCollectionByNameOrId("sellers")
	os2 := core.NewRecord(sellers)
	for k, v := range map[string]any{"slug": "other-store", "display_name": "Other", "status": "active", "kind": "individual", "sale_model": "principal"} {
		os2.Set(k, v)
	}
	if err := app.Save(os2); err != nil {
		t.Fatal(err)
	}
	addMember(os2.Id, other.Id)
	roles, _ := app.FindCollectionByNameOrId("user_roles")
	er := core.NewRecord(roles)
	er.Set("user", editor.Id)
	er.Set("role", "editor")
	er.Set("status", "active")
	if err := app.Save(er); err != nil {
		t.Fatal(err)
	}
	// a PocketBase superuser with a market account moderates too
	sus, _ := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	su := core.NewRecord(sus)
	su.SetEmail("root@example.com")
	su.SetPassword("test-password-123")
	if err := app.Save(su); err != nil {
		t.Fatal(err)
	}

	var store web.ReleaseStore
	var bucket *fakeS3
	filesDir := t.TempDir()
	uploadDir := filepath.Join(filesDir, ".uploads")
	objectPath := func(key string) string { return filepath.Join(filesDir, filepath.FromSlash(key)) }
	has := func(key string) bool {
		if bucket != nil {
			bucket.mu.Lock()
			defer bucket.mu.Unlock()
			_, ok := bucket.objects["/bkt/storeee/"+key]
			return ok
		}
		_, err := os.Stat(objectPath(key))
		return err == nil
	}
	if mode == "s3" {
		bucket = &fakeS3{objects: map[string][]byte{}}
		srv := httptest.NewServer(bucket)
		t.Cleanup(srv.Close)
		store = web.S3ReleaseStore{S3: s3.Presigner{Endpoint: srv.URL, Region: "ru-1", Bucket: "bkt", AccessKey: "test-access",
			SecretKey: "test-secret", PathStyle: true, Prefix: "storeee/"}}
		uploadDir = t.TempDir()
	} else {
		store = web.LocalReleaseStore{Dir: filesDir}
	}

	en, _ := i18n.Parse(dict.EN)
	rels := &pb.Releases{App: app}
	cab := &web.Cabinet{
		Users: &pb.Users{App: app}, Magic: account.Magic{Key: []byte("session-key")},
		Orders: &pb.Account{App: app}, CSRFKey: []byte("csrf-key"),
		Items: &pb.Authoring{App: app}, Roles: &pb.Cabinet{App: app}, TermsVersion: "1", Method: "card",
		Releases: rels, Moderation: rels, ReleaseFiles: store, UploadDir: uploadDir,
	}
	repo := &pb.Catalog{App: app}
	h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: "default", Dict: en,
		Commerce: &web.Commerce{Cabinet: cab}, Editorial: &pb.Editorial{App: app}})
	outer := http.NewServeMux()
	for _, p := range web.AllPatterns() {
		outer.Handle(p, h)
	}
	srv := httptest.NewServer(outer)
	t.Cleanup(srv.Close)
	cab.AllowedOrigin = srv.URL

	type client struct {
		c     *http.Client
		token string
	}
	login := func(email string) *client {
		t.Helper()
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		res, err := c.Get(srv.URL + "/account")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		tok := field(reCSRF, string(b))
		res, err = c.PostForm(srv.URL+"/account/login", url.Values{"csrf": {tok}, "email": {email}, "password": {"test-password-123"}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("login %s: %d", email, res.StatusCode)
		}
		return &client{c: c, token: tok}
	}
	get := func(cl *client, path string) (int, string, http.Header) {
		t.Helper()
		res, err := cl.c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if tok := field(reCSRF, string(b)); tok != "" {
			cl.token = tok
		}
		return res.StatusCode, string(b), res.Header
	}
	post := func(cl *client, path string, v url.Values) (int, string) {
		t.Helper()
		v.Set("csrf", cl.token)
		res, err := cl.c.PostForm(srv.URL+path, v)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode, res.Header.Get("Location")
	}
	upload := func(cl *client, itemID, version string, data []byte, csrf bool) (int, string) {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		if csrf {
			_ = mw.WriteField("csrf", cl.token)
		}
		_ = mw.WriteField("version", version)
		_ = mw.WriteField("changelog", "Fixes & docs")
		fw, _ := mw.CreateFormFile("file", "My Kit.zip")
		_, _ = fw.Write(data)
		_ = mw.Close()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/seller/items/"+itemID+"/releases", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		res, err := cl.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode, res.Header.Get("Location")
	}
	tempLeft := func() int {
		m, _ := filepath.Glob(filepath.Join(uploadDir, ".release-*"))
		return len(m)
	}

	vendor := login("vendor@example.com")
	item := "/seller/items/" + prod.Id
	code, body, _ := get(vendor, item)
	if code != 200 || !strings.Contains(body, `name="file"`) || !strings.Contains(body, item+"/releases") {
		t.Fatalf("item page %d: no release form", code)
	}
	if code, body, _ := get(vendor, "/seller/items/new"); code != 200 || !strings.Contains(body, "Save the card first") {
		t.Fatalf("new item hint %d", code)
	}

	kit := releaseZip(t, map[string]string{
		"kit/readme.md":   "# Kit",
		"kit/setup.exe":   "MZ",
		"kit/config.json": `{"api_key": "abcdefgh12345678"}`,
	})
	key := sel.GetString("slug") + "/aurora-theme/aurora-theme-9.0.0.zip"

	// no CSRF token: refused before the file is stored
	if code, _ := upload(vendor, prod.Id, "9.0.0", kit, false); code != http.StatusForbidden {
		t.Fatalf("upload without csrf: %d", code)
	}
	if has(key) || tempLeft() != 0 {
		t.Fatal("refused upload left a file")
	}
	// not a zip / bad version
	if _, loc := upload(vendor, prod.Id, "9.0.0", []byte("plain"), true); !strings.Contains(loc, "err=ui.release.bad_zip") {
		t.Fatalf("bad zip: %s", loc)
	}
	if _, loc := upload(vendor, prod.Id, "../9", kit, true); !strings.Contains(loc, "err=ui.release.invalid") {
		t.Fatalf("bad version: %s", loc)
	}
	// a vendor of another store
	oc := login("other@example.com")
	get(oc, "/seller")
	if _, loc := upload(oc, prod.Id, "9.0.0", kit, true); !strings.Contains(loc, "err=ui.error.not_found") {
		t.Fatalf("foreign item: %s", loc)
	}
	if has(key) {
		t.Fatal("foreign upload stored a file")
	}

	// the real upload: stored, in review, flagged
	code, loc := upload(vendor, prod.Id, "9.0.0", kit, true)
	if code != http.StatusSeeOther || !strings.Contains(loc, "ok=ui.release.uploaded") {
		t.Fatalf("upload: %d %s", code, loc)
	}
	if !has(key) || tempLeft() != 0 {
		t.Fatalf("object %s stored=%v, temp files %d", key, has(key), tempLeft())
	}
	rec, err := app.FindFirstRecordByData("product_releases", "storage_key", key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(kit)
	if rec.GetString("status") != "review" || rec.GetString("sha256") != hex.EncodeToString(sum[:]) || rec.GetInt("size") != len(kit) ||
		rec.GetInt("flag_count") < 2 || !strings.Contains(rec.GetString("manifest"), "kit/setup.exe") {
		t.Fatalf("release row %v", rec.FieldsData())
	}
	// same version again: refused, the stored object stays
	if _, loc := upload(vendor, prod.Id, "9.0.0", kit, true); !strings.Contains(loc, "err=ui.release.duplicate") || !has(key) {
		t.Fatalf("duplicate: %s stored=%v", loc, has(key))
	}
	_, body, _ = get(vendor, item)
	if !strings.Contains(body, "In review") || !strings.Contains(body, "Waiting for a moderator.") {
		t.Fatal("vendor does not see the review status")
	}
	// the vendor cannot moderate
	if code, _, _ := get(vendor, "/editor/releases"); code != http.StatusForbidden {
		t.Fatalf("vendor on the queue: %d", code)
	}

	// editor: queue, detail, file, reject without note, approve
	ed := login("editor@example.com")
	code, body, _ = get(ed, "/editor/releases")
	if code != 200 || !strings.Contains(body, "/editor/releases/"+rec.Id) {
		t.Fatalf("queue %d", code)
	}
	code, body, _ = get(ed, "/editor/releases/"+rec.Id)
	for _, want := range []string{rec.GetString("sha256"), "kit/setup.exe", "kit/config.json", "password-assignment", "Approve and publish", "/editor/releases/" + rec.Id + "/file"} {
		if code != 200 || !strings.Contains(body, want) {
			t.Fatalf("detail %d: missing %q", code, want)
		}
	}
	res, err := ed.c.Get(srv.URL + "/editor/releases/" + rec.Id + "/file")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if mode == "local" && (res.StatusCode != 200 || !bytes.Equal(got, kit)) {
		t.Fatalf("moderator file %d (%d bytes)", res.StatusCode, len(got))
	}
	if mode == "s3" && (res.StatusCode != http.StatusSeeOther || !strings.Contains(res.Header.Get("Location"), "/bkt/storeee/"+key+"?")) {
		t.Fatalf("moderator file %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if _, loc := post(ed, "/editor/releases/"+rec.Id, url.Values{"status": {"rejected"}}); !strings.Contains(loc, "err=ui.release.note_required") {
		t.Fatalf("reject without note: %s", loc)
	}
	if _, loc := post(ed, "/editor/releases/"+rec.Id, url.Values{"status": {"published"}}); !strings.Contains(loc, "ok=ui.release.decided_published") {
		t.Fatalf("approve: %s", loc)
	}
	rec, _ = app.FindRecordById("product_releases", rec.Id)
	if rec.GetString("status") != "published" || rec.GetString("reviewed_by") != "editor@example.com" || rec.GetDateTime("reviewed_at").IsZero() || rec.GetDateTime("released_at").IsZero() {
		t.Fatalf("approved row %v", rec.FieldsData())
	}

	// second version: the superuser rejects it with a note, the vendor
	// sees the note and deletes it (file removed)
	key2 := sel.GetString("slug") + "/aurora-theme/aurora-theme-9.1.0.zip"
	if _, loc := upload(vendor, prod.Id, "9.1.0", kit, true); !strings.Contains(loc, "ok=") || !has(key2) {
		t.Fatalf("upload 9.1.0: %s", loc)
	}
	r2, _ := app.FindFirstRecordByData("product_releases", "storage_key", key2)
	root := login("root@example.com")
	get(root, "/editor/releases/"+r2.Id)
	if _, loc := post(root, "/editor/releases/"+r2.Id, url.Values{"status": {"rejected"}, "note": {"Remove setup.exe"}}); !strings.Contains(loc, "ok=ui.release.decided_rejected") {
		t.Fatalf("superuser reject: %s", loc)
	}
	_, body, _ = get(vendor, item)
	if !strings.Contains(body, "Rejected") || !strings.Contains(body, "Remove setup.exe") {
		t.Fatal("vendor does not see the rejection note")
	}
	if _, loc := post(vendor, item+"/releases/"+r2.Id+"/delete", url.Values{}); !strings.Contains(loc, "ok=ui.release.deleted") || has(key2) {
		t.Fatalf("delete rejected: %s stored=%v", loc, has(key2))
	}
	// a published release cannot be deleted
	if _, loc := post(vendor, item+"/releases/"+rec.Id+"/delete", url.Values{}); !strings.Contains(loc, "err=") || !has(key) {
		t.Fatalf("delete published: %s", loc)
	}

	// the row insert fails (quota full): the stored object is removed
	big := core.NewRecord(rec.Collection())
	for k, v := range map[string]any{"product": prod.Id, "seller": sel.Id, "version": "0.0.1-quota", "status": "yanked",
		"storage_key": "x/y.zip", "size": 6 << 30, "sha256": "00"} {
		big.Set(k, v)
	}
	if err := app.Save(big); err != nil {
		t.Fatal(err)
	}
	key3 := sel.GetString("slug") + "/aurora-theme/aurora-theme-9.2.0.zip"
	if _, loc := upload(vendor, prod.Id, "9.2.0", kit, true); !strings.Contains(loc, "err=ui.release.quota") || has(key3) || tempLeft() != 0 {
		t.Fatalf("quota: %s stored=%v temp=%d", loc, has(key3), tempLeft())
	}

	if err := app.Delete(big); err != nil {
		t.Fatal(err)
	}

	// item delete: a published item is refused; a new draft with a release
	// in review is deleted with its object after an explicit confirm
	_, body, _ = get(vendor, item)
	if !strings.Contains(body, `data-delete-blocked="status"`) || strings.Contains(body, item+"/delete") {
		t.Fatal("published item offers delete")
	}
	if _, loc := post(vendor, item+"/delete", url.Values{"confirm": {"1"}}); !strings.Contains(loc, "err=ui.seller.delete_status") {
		t.Fatalf("delete published item: %s", loc)
	}
	get(vendor, "/seller/items/new")
	code, loc = post(vendor, "/seller/items/new", url.Values{"title": {"Throwaway Kit"}})
	if code != http.StatusSeeOther || !strings.Contains(loc, "ok=ui.seller.created") {
		t.Fatalf("create: %d %s", code, loc)
	}
	tmp, err := app.FindFirstRecordByData("products", "slug", "throwaway-kit")
	if err != nil {
		t.Fatal(err)
	}
	titem := "/seller/items/" + tmp.Id
	tkey := sel.GetString("slug") + "/throwaway-kit/throwaway-kit-0.1.0.zip"
	get(vendor, titem)
	if _, loc := upload(vendor, tmp.Id, "0.1.0", kit, true); !strings.Contains(loc, "ok=") || !has(tkey) {
		t.Fatalf("upload to throwaway: %s", loc)
	}
	if _, body, _ := get(vendor, titem); !strings.Contains(body, titem+"/delete") {
		t.Fatal("draft item has no delete link")
	}
	code, body, _ = get(vendor, titem+"/delete")
	if code != 200 || !strings.Contains(body, `name="confirm"`) || !strings.Contains(body, "1 releases") {
		t.Fatalf("confirm page %d", code)
	}
	if code, _, _ := get(oc, titem+"/delete"); code != http.StatusNotFound {
		t.Fatalf("foreign confirm page: %d", code)
	}
	if _, loc := post(oc, titem+"/delete", url.Values{"confirm": {"1"}}); !strings.Contains(loc, "err=ui.error.not_found") {
		t.Fatalf("foreign delete: %s", loc)
	}
	get(vendor, titem+"/delete")
	if _, loc := post(vendor, titem+"/delete", url.Values{}); !strings.Contains(loc, "err=ui.seller.delete_confirm") || !has(tkey) {
		t.Fatalf("delete without confirm: %s", loc)
	}
	if _, loc := post(vendor, titem+"/delete", url.Values{"confirm": {"1"}}); !strings.Contains(loc, "ok=ui.seller.item_deleted") || has(tkey) {
		t.Fatalf("delete: %s stored=%v", loc, has(tkey))
	}
	if _, err := app.FindRecordById("products", tmp.Id); err == nil {
		t.Fatal("deleted item still there")
	}
	if n, _ := app.CountRecords("product_releases", dbx.HashExp{"product": tmp.Id}); n != 0 {
		t.Fatalf("release rows left: %d", n)
	}
	// storage refuses the delete: the rows are gone anyway (nothing points
	// at a missing file), the object is an orphan in the log
	if bucket != nil {
		get(vendor, "/seller/items/new")
		post(vendor, "/seller/items/new", url.Values{"title": {"Orphan Kit"}})
		ok2, err := app.FindFirstRecordByData("products", "slug", "orphan-kit")
		if err != nil {
			t.Fatal(err)
		}
		okey := sel.GetString("slug") + "/orphan-kit/orphan-kit-0.1.0.zip"
		get(vendor, "/seller/items/"+ok2.Id)
		if _, loc := upload(vendor, ok2.Id, "0.1.0", kit, true); !strings.Contains(loc, "ok=") || !has(okey) {
			t.Fatalf("upload orphan kit: %s", loc)
		}
		bucket.mu.Lock()
		bucket.failDelete = true
		bucket.mu.Unlock()
		get(vendor, "/seller/items/"+ok2.Id+"/delete")
		if _, loc := post(vendor, "/seller/items/"+ok2.Id+"/delete", url.Values{"confirm": {"1"}}); !strings.Contains(loc, "ok=ui.seller.item_deleted_files_left") {
			t.Fatalf("delete with storage down: %s", loc)
		}
		if n, _ := app.CountRecords("product_releases", dbx.HashExp{"product": ok2.Id}); n != 0 || !has(okey) {
			t.Fatalf("rows %d, orphan stored=%v", n, has(okey))
		}
	}
	_ = admin
}
