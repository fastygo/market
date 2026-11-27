package pb_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

// TestSignupsClosed: with MARKET_SIGNUPS closed the sign-in page has no
// register form and a register post creates no account; open restores it.
func TestSignupsClosed(t *testing.T) {
	for _, open := range []bool{false, true} {
		r := newRig(t)
		en, _ := i18n.Parse(dict.EN)
		users := &pb.Users{App: r.app}
		commerce := &web.Commerce{Orders: r.svc, HashSalt: "salt",
			Cabinet: &web.Cabinet{Users: users, Magic: account.Magic{Key: []byte("k")}, Orders: &pb.Account{App: r.app},
				CSRFKey: []byte("csrf-key"), TermsVersion: "1", Method: "card", SignupsOpen: open}}
		repo := &pb.Catalog{App: r.app}
		h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: "default", Dict: en, Commerce: commerce})
		outer := http.NewServeMux()
		for _, p := range web.AllPatterns() {
			outer.Handle(p, h)
		}
		srv := httptest.NewServer(outer)
		jar, _ := cookiejar.New(nil)
		c := &http.Client{Jar: jar}
		res, err := c.Get(srv.URL + "/account?register=1")
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		page := string(b)
		if hasForm := strings.Contains(page, `action="/account/register"`); hasForm != open {
			t.Fatalf("open=%v: register form shown=%v", open, hasForm)
		}
		res, err = c.PostForm(srv.URL+"/account/register", url.Values{"csrf": {field(reCSRF, page)}, "email": {"new@example.com"}, "terms": {"1"}})
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		_, err = users.ByEmail(context.Background(), "new@example.com")
		if created := err == nil; created != open {
			t.Fatalf("open=%v: status %d, account created=%v", open, res.StatusCode, created)
		}
		if !open && res.StatusCode != http.StatusForbidden {
			t.Fatalf("closed register status %d", res.StatusCode)
		}
		srv.Close()
	}
}

func TestAddSellerMember(t *testing.T) {
	app := pbtest.New(t)
	ctx := context.Background()
	users := &pb.Users{App: app}
	c := &pb.Cabinet{App: app}
	for _, e := range []string{"owner@example.com", "staff@example.com"} {
		if _, _, err := users.Ensure(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Grant(ctx, "owner@example.com", cabinet.Vendor, ""); err != nil {
		t.Fatal(err)
	}
	if err := c.AddSellerMember(ctx, "owner-store", "staff@example.com", "boss"); !errors.Is(err, cabinet.ErrInvalid) {
		t.Fatalf("bad role: %v", err)
	}
	if err := c.AddSellerMember(ctx, "no-such-store", "staff@example.com", "staff"); !errors.Is(err, cabinet.ErrNotFound) {
		t.Fatalf("missing store: %v", err)
	}
	if err := c.AddSellerMember(ctx, "owner-store", "nobody@example.com", "staff"); !errors.Is(err, cabinet.ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := c.AddSellerMember(ctx, "owner-store", "Staff@Example.com", "staff"); err != nil {
			t.Fatal(err)
		}
	}
	u, _ := users.ByEmail(ctx, "staff@example.com")
	p, err := c.Principal(ctx, u.ID)
	if err != nil || !p.Has(cabinet.Vendor) || len(p.Sellers) != 1 {
		t.Fatalf("staff principal %+v %v", p, err)
	}
}

// TestOrderPayURL: an unpaid order links back to the provider page of its
// pending payment; non-https URLs and paid orders get no link.
func TestOrderPayURL(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	v := r.checkout(t, "pay-url")
	acc := &pb.Account{App: r.app}
	o, _, _, err := acc.OrderByID(ctx, v.OrderID)
	if err != nil || o.Status != "awaiting_payment" || o.PayURL != "" {
		t.Fatalf("fake gateway (relative URL): %+v %v", o, err)
	}
	if _, err := r.app.DB().NewQuery(`UPDATE payments SET confirmation_url = 'https://pay.example/confirm?id=1' WHERE "order" = {:o}`).
		Bind(dbx.Params{"o": v.OrderID}).Execute(); err != nil {
		t.Fatal(err)
	}
	if o, _, _, err = acc.OrderByID(ctx, v.OrderID); err != nil || o.PayURL != "https://pay.example/confirm?id=1" {
		t.Fatalf("pending order: %+v %v", o, err)
	}
}

// TestGuestCheckoutExistingEmail: a guest who registered (unverified,
// not signed in) buys with the same email: the order goes to the existing
// account, the provider page and the return page work.
func TestGuestCheckoutExistingEmail(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	en, _ := i18n.Parse(dict.EN)
	users := &pb.Users{App: r.app}
	existing, _, err := users.Ensure(ctx, "owner@example.com")
	if err != nil {
		t.Fatal(err)
	}
	commerce := &web.Commerce{Orders: r.svc, FakePay: r.gw, HashSalt: "salt",
		Cabinet: &web.Cabinet{Users: users, Magic: account.Magic{Key: []byte("k")}, Orders: &pb.Account{App: r.app},
			CSRFKey: []byte("csrf-key"), TermsVersion: "1", Method: "card"}}
	repo := &pb.Catalog{App: r.app}
	h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: "default", Dict: en, Commerce: commerce})
	outer := http.NewServeMux()
	for _, p := range web.AllPatterns() {
		outer.Handle(p, h)
	}
	srv := httptest.NewServer(outer)
	t.Cleanup(srv.Close)
	r.svc.ReturnURL = func(id string) string { return srv.URL + "/checkout/return/" + id }
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	res, err := c.Get(srv.URL + "/checkout/aurora-theme")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	page := string(b)
	res, err = c.PostForm(srv.URL+"/checkout/form", url.Values{"csrf": {field(reCSRF, page)}, "key": {field(reKey, page)},
		"product": {"aurora-theme"}, "terms": {"1"}, "email": {"Owner@Example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Request.URL.Path, "/fakepay/") {
		t.Fatalf("checkout -> %d %s", res.StatusCode, res.Request.URL.Path)
	}
	res, err = c.PostForm(srv.URL+res.Request.URL.Path+"/succeed", url.Values{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Request.URL.Path, "/checkout/return/") || !strings.Contains(string(b), `data-state="paid"`) {
		t.Fatalf("return %d %s", res.StatusCode, res.Request.URL.Path)
	}
	if n := r.n(t, "SELECT COUNT(*) FROM orders WHERE buyer = '"+existing.ID+"'"); n != 1 {
		t.Fatalf("orders of the existing account: %d", n)
	}
	if n := r.n(t, "SELECT COUNT(*) FROM users WHERE email = 'owner@example.com'"); n != 1 {
		t.Fatalf("users with the email: %d", n)
	}
}
