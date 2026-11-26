package pb_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/notify"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/ratelimit"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

var (
	reCSRF  = regexp.MustCompile(`name="csrf"[^>]*value="([^"]+)"|value="([^"]+)"[^>]*name="csrf"`)
	reKey   = regexp.MustCompile(`name="key"[^>]*value="([^"]+)"|value="([^"]+)"[^>]*name="key"`)
	reMagic = regexp.MustCompile(`https?://[^\s"<]+/account/magic/[A-Za-z0-9_\-.]+`)
)

func field(re *regexp.Regexp, html string) string {
	m := re.FindStringSubmatch(html)
	if m == nil {
		return ""
	}
	if m[1] != "" {
		return m[1]
	}
	return m[2]
}

// TestCabinetBrowserCycle: guest buys from the product page, pays on the
// provider page, comes back, gets the purchase email, opens the magic
// link, downloads from the order page, signs out; a refund closes access.
func TestCabinetBrowserCycle(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	en, _ := i18n.Parse(dict.EN)
	mailDir, filesDir := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(filesDir, "placeholder"), 0o700); err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	dlKey := []byte("dl-key")
	users := &pb.Users{App: r.app}
	magic := account.Magic{Key: []byte("session-key")}
	note := &notify.Service{Store: &pb.Account{App: r.app}, Users: users, Magic: magic, Mailer: mail.FileSender{Dir: mailDir}, Dict: en}
	r.svc.Outbox["email.purchase"] = note.PurchaseEmail
	lic := &licensing.Service{Store: &pb.Downloads{App: r.app}, Signer: licensing.Signer{Key: dlKey}}
	repo := &pb.Catalog{App: r.app}
	commerce := &web.Commerce{Orders: r.svc, Licenses: lic, FakePay: r.gw, HashSalt: "salt",
		Files:   licensing.LocalFiles{Dir: filesDir, Key: dlKey},
		Cabinet: &web.Cabinet{Users: users, Magic: magic, Orders: &pb.Account{App: r.app}, Mailer: note, CSRFKey: []byte("csrf-key"), TermsVersion: "1", Method: "card"}}
	h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: "default", Dict: en, Commerce: commerce})
	// forward only the declared patterns, as the PocketBase router does
	outer := http.NewServeMux()
	for _, p := range web.AllPatterns() {
		outer.Handle(p, h)
	}
	srv = httptest.NewServer(outer)
	t.Cleanup(srv.Close)
	note.PublicURL = srv.URL
	r.svc.ReturnURL = func(id string) string { return srv.URL + "/checkout/return/" + id }
	lic.Presigner = licensing.LocalPresigner{BaseURL: srv.URL + "/files", Key: dlKey}

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	get := func(path string) (int, string, string) {
		t.Helper()
		res, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Request.URL.Path
	}
	post := func(path string, form url.Values) (int, string, string) {
		t.Helper()
		res, err := c.PostForm(srv.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b), res.Request.URL.Path
	}

	code, page, _ := get("/products/aurora-theme")
	csrf, key := field(reCSRF, page), field(reKey, page)
	if code != 200 || csrf == "" || key == "" || !strings.Contains(page, `data-form="checkout"`) {
		t.Fatalf("product page %d csrf=%q key=%q", code, csrf, key)
	}
	// CSRF is enforced
	if code, _, _ := post("/checkout/form", url.Values{"product": {"aurora-theme"}, "terms": {"1"}, "email": {"guest@example.com"}}); code != 403 {
		t.Fatalf("no csrf: %d", code)
	}
	code, page, path := post("/checkout/form", url.Values{"csrf": {csrf}, "key": {key}, "product": {"aurora-theme"}, "terms": {"1"}, "email": {"Guest@Example.com"}})
	if code != 200 || !strings.HasPrefix(path, "/fakepay/") || !strings.Contains(page, `data-page="fakepay"`) {
		t.Fatalf("checkout -> %d %s", code, path)
	}
	code, page, path = post(path+"/succeed", url.Values{})
	if code != 200 || !strings.HasPrefix(path, "/checkout/return/") || !strings.Contains(page, `data-state="paid"`) {
		t.Fatalf("return %d %s %s", code, path, page)
	}
	// guest was not signed in: no order link on the return page
	if strings.Contains(page, "/account/orders/") {
		t.Fatal("guest sees the order link")
	}
	for i := 0; i < 10; i++ {
		if ok, _ := r.svc.ProcessInboxOnce(ctx); !ok {
			break
		}
	}
	for i := 0; i < 10; i++ {
		if ok, _ := r.svc.ProcessOutboxOnce(ctx); !ok {
			break
		}
	}
	ents, _ := os.ReadDir(mailDir)
	if len(ents) != 1 {
		t.Fatalf("mails: %d", len(ents))
	}
	eml, _ := os.ReadFile(filepath.Join(mailDir, ents[0].Name()))
	var number string
	_ = r.app.DB().NewQuery("SELECT number FROM orders LIMIT 1").Row(&number)
	if !strings.Contains(string(eml), "To: guest@example.com") || !strings.Contains(string(eml), number) {
		t.Fatalf("mail: %s", eml)
	}
	link := reMagic.FindString(string(eml))
	if link == "" {
		t.Fatalf("no magic link in %s", eml)
	}
	code, page, path = get(strings.TrimPrefix(link, srv.URL))
	if code != 200 || path != "/account/orders/"+number || !strings.Contains(page, `data-action="download"`) {
		t.Fatalf("magic -> %d %s", code, path)
	}
	// download: entitlement -> /dl token -> signed local file
	if err := os.WriteFile(filepath.Join(filesDir, "placeholder", "aurora-theme-1.0.0.zip"), []byte("ZIPDATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	var key0 string
	_ = r.app.DB().NewQuery("SELECT storage_key FROM releases LIMIT 1").Row(&key0)
	if key0 != "placeholder/aurora-theme-1.0.0.zip" {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(filesDir, key0)), 0o700)
		_ = os.WriteFile(filepath.Join(filesDir, key0), []byte("ZIPDATA"), 0o600)
	}
	code, body, path := get("/products/aurora-theme/download")
	if code != 200 || body != "ZIPDATA" || !strings.HasPrefix(path, "/files/") {
		t.Fatalf("download %d %s %q", code, path, body)
	}
	code, page, _ = get("/account/orders")
	if code != 200 || !strings.Contains(page, `data-order="`+number+`"`) {
		t.Fatalf("orders %d", code)
	}
	// refund closes access
	var orderID string
	_ = r.app.DB().NewQuery("SELECT id FROM orders WHERE number = {:n}").Bind(dbx.Params{"n": number}).Row(&orderID)
	if _, err := r.svc.RefundOrder(ctx, orderID, ordering.ReasonBuyerRequest, "", "op@example.com"); err != nil {
		t.Fatal(err)
	}
	_, page, _ = get("/account/orders/" + number)
	if strings.Contains(page, `data-action="download"`) || !strings.Contains(page, `data-access="refunded"`) {
		t.Fatal("refunded order still offers a download")
	}
	if code, _, _ := get("/products/aurora-theme/download"); code != 403 {
		t.Fatalf("download after refund: %d", code)
	}
	// sign out
	csrf = field(reCSRF, page)
	if code, _, path := post("/account/logout", url.Values{"csrf": {csrf}}); code != 200 || path != "/account" {
		t.Fatalf("logout %d %s", code, path)
	}
	if _, page, path := get("/account/orders"); path != "/account" || !strings.Contains(page, `data-page="signin"`) {
		t.Fatalf("after logout: %s", path)
	}
	// password sign-in for an unknown password fails without detail
	csrf = field(reCSRF, page)
	if code, page, _ := post("/account/login", url.Values{"csrf": {csrf}, "email": {"guest@example.com"}, "password": {"nope"}}); code != 401 || !strings.Contains(page, en.T("ui.account.bad_credentials")) {
		t.Fatalf("bad login %d", code)
	}
	// the magic link request answers the same for unknown emails
	if code, page, _ := post("/account/link", url.Values{"csrf": {csrf}, "email": {"nobody@example.com"}}); code != 200 || !strings.Contains(page, en.T("ui.account.link_sent")) {
		t.Fatalf("link %d", code)
	}
}

func TestCabinetAuthRateLimit(t *testing.T) {
	r := newRig(t)
	en, _ := i18n.Parse(dict.EN)
	users := &pb.Users{App: r.app}
	commerce := &web.Commerce{
		AuthLimit: ratelimit.NewAuth(),
		Cabinet:   &web.Cabinet{Users: users, CSRFKey: []byte("csrf-key")},
	}
	h := web.New(web.Handler{Catalog: &pb.Catalog{App: r.app}, Storefronts: &pb.Catalog{App: r.app}, StorefrontCode: "default", Dict: en, Commerce: commerce})
	mux := http.NewServeMux()
	for _, p := range web.AllPatterns() {
		mux.Handle(p, h)
	}
	var last *httptest.ResponseRecorder
	for i := 0; i < ratelimit.DefaultAuthMax+1; i++ {
		req := httptest.NewRequest(http.MethodPost, "/account/login", strings.NewReader("email=a@example.com&password=nope"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.RemoteAddr = "192.0.2.20:1"
		last = httptest.NewRecorder()
		mux.ServeHTTP(last, req)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("want 429, got %d %s", last.Code, last.Body.String())
	}
	if last.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After")
	}
}
