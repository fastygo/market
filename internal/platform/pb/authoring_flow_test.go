package pb_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/content"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

func TestDescriptionHTMLBackfillAndCap(t *testing.T) {
	app := pbtest.New(t)
	col, err := app.FindCollectionByNameOrId("products")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := col.Fields.GetByName("description").(*core.TextField)
	if f == nil || f.Max < content.MaxMarkdown {
		t.Fatalf("description max %v", f)
	}
	if col.Fields.GetByName("description_html") == nil || col.Fields.GetByName("description_version") == nil {
		t.Fatal("missing description fields")
	}
	if _, err := app.FindCollectionByNameOrId("seller_members"); err != nil {
		t.Fatal(err)
	}
	prod, err := app.FindFirstRecordByData("products", "slug", "aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	if prod.GetString("description") != "" && prod.GetString("description_html") == "" {
		t.Fatal("backfill left description_html empty")
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAuthoringCabinetFlow(t *testing.T) {
	app := pbtest.New(t)
	users, _ := app.FindCollectionByNameOrId("users")
	u := core.NewRecord(users)
	u.SetEmail("seller@example.com")
	u.SetPassword("test-password-123")
	if err := app.Save(u); err != nil {
		t.Fatal(err)
	}
	prod, err := app.FindFirstRecordByData("products", "slug", "aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	members, _ := app.FindCollectionByNameOrId("seller_members")
	m := core.NewRecord(members)
	m.Set("seller", prod.GetString("seller"))
	m.Set("user", u.Id)
	m.Set("role", "owner")
	if err := app.Save(m); err != nil {
		t.Fatal(err)
	}

	en, _ := i18n.Parse(dict.EN)
	cabUsers := &pb.Users{App: app}
	cab := &web.Cabinet{
		Users: cabUsers, Magic: account.Magic{Key: []byte("session-key")},
		Orders: &pb.Account{App: app}, CSRFKey: []byte("csrf-key"),
		Items: &pb.Authoring{App: app}, TermsVersion: "1", Method: "card",
	}
	repo := &pb.Catalog{App: app}
	h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: "default", Dict: en, Commerce: &web.Commerce{Cabinet: cab}})
	outer := http.NewServeMux()
	for _, p := range web.AllPatterns() {
		outer.Handle(p, h)
	}
	srv := httptest.NewServer(outer)
	t.Cleanup(srv.Close)
	cab.AllowedOrigin = srv.URL

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	// sign in (follow one redirect by hand)
	get := func(path string) (int, string, *http.Response) {
		t.Helper()
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res
	}
	code, body, _ := get("/account")
	if code != 200 {
		t.Fatalf("signin page %d", code)
	}
	token := field(reCSRF, body)
	res, err := c.PostForm(srv.URL+"/account/login", url.Values{
		"csrf": {token}, "email": {"seller@example.com"}, "password": {"test-password-123"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login %d", res.StatusCode)
	}

	// a non-member cannot see the item
	// (we are a member; first check the list and edit page)
	code, body, _ = get("/account/items")
	if code != 200 || !strings.Contains(body, "aurora-theme") {
		t.Fatalf("items %d %s", code, body)
	}
	if strings.Contains(body, "/static/editor/") {
		t.Fatal("items list loaded the editor island")
	}
	id := prod.Id
	code, body, rec := get("/account/items/" + id)
	if code != 200 || !strings.Contains(body, `id="description"`) {
		t.Fatalf("edit %d %s", code, body)
	}
	csrf := field(reCSRF, body)
	if csrf == "" {
		t.Fatal("no csrf on edit page")
	}

	// form POST
	res, err = c.PostForm(srv.URL+"/account/items/"+id, url.Values{
		"csrf": {csrf}, "version": {"0"}, "description": {"# Hello\n\nSafe **text**."},
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("save %d", res.StatusCode)
	}
	prod, _ = app.FindRecordById("products", id)
	if prod.GetInt("description_version") != 1 {
		t.Fatalf("version %d", prod.GetInt("description_version"))
	}
	if !strings.Contains(prod.GetString("description_html"), "<strong>text</strong>") {
		t.Fatalf("html %s", prod.GetString("description_html"))
	}

	// storefront shows the stored HTML
	code, page, rec := get("/products/aurora-theme")
	if code != 200 || !strings.Contains(page, "<strong>text</strong>") {
		t.Fatalf("storefront %d %s", code, page)
	}
	if strings.Contains(page, "/static/editor/") {
		t.Fatal("storefront loaded the editor")
	}
	if csp := rec.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'self'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("csp %q", csp)
	}

	// If-Match conflict
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/account/items/"+id, strings.NewReader(`{"description":"stale"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("If-Match", `"v0"`)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("conflict %d %s", res.StatusCode, b)
	}

	// cap
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/account/items/"+id, strings.NewReader(`{"description":"`+strings.Repeat("a", content.MaxMarkdown+1)+`","version":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("If-Match", `"v1"`)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("cap %d", res.StatusCode)
	}

	// CSRF missing on autosave
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/account/items/"+id, strings.NewReader(`{"description":"x","version":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", srv.URL)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf %d", res.StatusCode)
	}

	// autosave ok
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/account/items/"+id, strings.NewReader(`{"description":"Updated **ok**."}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("If-Match", `"v1"`)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	json.NewDecoder(res.Body).Decode(&payload)
	res.Body.Close()
	if res.StatusCode != 200 || payload["version"] != float64(2) {
		t.Fatalf("autosave %d %v", res.StatusCode, payload)
	}

	// XSS is stripped in stored HTML
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/account/items/"+id, strings.NewReader(`{"description":"<script>alert(1)</script>Hello"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("If-Match", `"v2"`)
	res, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("xss save %d", res.StatusCode)
	}
	prod, _ = app.FindRecordById("products", id)
	html := prod.GetString("description_html")
	if strings.Contains(html, "<script") || strings.Contains(html, "alert(1)") {
		t.Fatalf("stored html not sanitized: %s", html)
	}

	// image upload: allow png, reject svg
	upload := func(name string, data []byte) (int, string) {
		t.Helper()
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		part, err := w.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
		w.Close()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/account/items/"+id+"/images", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Origin", srv.URL)
		res, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}
	code, out := upload("pic.png", pngBytes(t))
	if code != 200 || !strings.Contains(out, "/api/files/") {
		t.Fatalf("png upload %d %s", code, out)
	}
	code, out = upload("evil.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"></svg>`))
	if code != http.StatusUnsupportedMediaType {
		t.Fatalf("svg upload %d %s", code, out)
	}
	code, _ = upload("big.png", append(pngBytes(t), bytes.Repeat([]byte("x"), authoring.MaxImageBytes)...))
	if code != http.StatusRequestEntityTooLarge && code != http.StatusUnsupportedMediaType {
		t.Fatalf("oversize %d", code)
	}

}
