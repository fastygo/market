package pb_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

// End to end over HTTP: checkout -> fake pay -> inbox -> entitlement ->
// download link -> /dl -> pre-signed redirect; plus the re-check of
// ADR-0013 #6 (a token issued before the entitlement stops is rejected).
func TestHTTPCheckoutToDownload(t *testing.T) {
	r := newRig(t)
	en, err := i18n.Parse(dict.EN)
	if err != nil {
		t.Fatal(err)
	}
	r.svc.ReturnURL = func(id string) string { return "/checkout/return/" + id }
	lic := &licensing.Service{Store: &pb.Downloads{App: r.app}, Signer: licensing.Signer{Key: []byte("dl")},
		Presigner: licensing.LocalPresigner{BaseURL: "https://files.example.com", Key: []byte("s3")}, Now: r.clock.Now}
	cat := &pb.Catalog{App: r.app}
	h := web.New(web.Handler{Catalog: cat, Storefronts: cat, StorefrontCode: "default", Dict: en, Commerce: &web.Commerce{
		Auth: func(req *http.Request) (web.Buyer, bool) {
			if req.Header.Get("Authorization") == "Bearer buyer" {
				return web.Buyer{ID: r.buyer, Email: "buyer@example.com"}, true
			}
			return web.Buyer{}, false
		},
		Orders: r.svc, Licenses: lic, FakePay: r.gw, HashSalt: "salt",
	}})
	do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	auth := map[string]string{"Authorization": "Bearer buyer", "Idempotency-Key": "web-1"}

	if w := do("GET", "/products/aurora-theme/download", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous download: %d", w.Code)
	}
	if w := do("GET", "/products/aurora-theme/download", "", auth); w.Code != http.StatusForbidden {
		t.Fatalf("download before purchase: %d", w.Code)
	}
	if w := do("POST", "/checkout", `{"product":"aurora-theme","method":"card"}`, auth); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing consent: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/checkout", `{"product":"aurora-theme","method":"card","consents":{"terms":"1"}}`, map[string]string{"Authorization": "Bearer buyer"}); w.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency key: %d", w.Code)
	}
	w := do("POST", "/checkout", `{"product":"aurora-theme","method":"card","consents":{"terms":"1"}}`, auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"awaiting_payment"`) {
		t.Fatalf("checkout: %d %s", w.Code, w.Body)
	}
	var orderID, ppid string
	_ = r.app.DB().NewQuery(`SELECT "order" FROM payments`).Row(&orderID)
	_ = r.app.DB().NewQuery(`SELECT provider_payment_id FROM payments`).Row(&ppid)
	if w := do("POST", "/fakepay/"+ppid+"/succeed", "", nil); w.Code != http.StatusOK {
		t.Fatalf("fakepay: %d", w.Code)
	}
	if did, err := r.svc.ProcessInboxOnce(context.Background()); err != nil || !did {
		t.Fatal(did, err)
	}
	if w := do("GET", "/checkout/return/"+orderID+"?status=forged", "", auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"status":"paid"`) {
		t.Fatalf("return: %d %s", w.Code, w.Body)
	}
	w = do("GET", "/products/aurora-theme/download", "", auth)
	if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/dl/") {
		t.Fatalf("download link: %d %s", w.Code, w.Header().Get("Location"))
	}
	dl := w.Header().Get("Location")
	w = do("GET", dl, "", nil)
	if loc := w.Header().Get("Location"); w.Code != http.StatusFound || !strings.HasPrefix(loc, "https://files.example.com/placeholder/aurora-theme-1.0.0.zip?expires=") {
		t.Fatalf("dl: %d %s", w.Code, loc)
	}
	if r.n(t, "SELECT COUNT(*) FROM downloads") != 1 {
		t.Fatal("download not logged")
	}
	if w := do("GET", "/dl/garbage.token", "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("forged token: %d", w.Code)
	}
	r.clock.Add(11 * time.Minute)
	if w := do("GET", dl, "", nil); w.Code != http.StatusGone {
		t.Fatalf("expired token: %d", w.Code)
	}
	r.clock.Add(-11 * time.Minute)
	if _, err := r.app.DB().NewQuery("UPDATE entitlements SET status = 'refunded'").Execute(); err != nil {
		t.Fatal(err)
	}
	if w := do("GET", dl, "", nil); w.Code != http.StatusForbidden {
		t.Fatalf("token after refund: %d", w.Code)
	}
	// webhook endpoint: signature required, duplicates accepted
	body, sig := r.gw.Webhook("evt-x", ppid, "succeeded")
	if w := do("POST", "/hooks/pay/fake", string(body), map[string]string{"X-Fake-Signature": "bad"}); w.Code != http.StatusBadRequest {
		t.Fatalf("bad signature: %d", w.Code)
	}
	for i := 0; i < 2; i++ {
		if w := do("POST", "/hooks/pay/fake", string(body), map[string]string{"X-Fake-Signature": sig}); w.Code != http.StatusOK {
			t.Fatalf("webhook: %d", w.Code)
		}
	}
	if r.n(t, "SELECT COUNT(*) FROM payment_events WHERE event_id = 'evt-x'") != 1 {
		t.Fatal("duplicate webhook stored twice")
	}
}
