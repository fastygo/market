package migrations_test

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

func brandOf(t *testing.T, app core.App, code string) map[string]any {
	t.Helper()
	sf, err := app.FindFirstRecordByData("storefronts", "code", code)
	if err != nil {
		t.Fatal(err)
	}
	b := map[string]any{}
	if err := sf.UnmarshalJSONField("brand", &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func setBrand(t *testing.T, app core.App, code string, b map[string]any) {
	t.Helper()
	sf, err := app.FindFirstRecordByData("storefronts", "code", code)
	if err != nil {
		t.Fatal(err)
	}
	sf.Set("brand", b)
	if err := app.Save(sf); err != nil {
		t.Fatal(err)
	}
}

// rerun forgets the applied data migrations from file prefix on and runs
// all pending migrations again, as an upgrade of an older database does.
func rerun(t *testing.T, app core.App, files ...string) {
	t.Helper()
	for _, f := range files {
		if _, err := app.DB().NewQuery("DELETE FROM _migrations WHERE file = {:f}").Bind(dbx.Params{"f": f}).Execute(); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
}

func TestFreshDatabaseHasStoreeeBrand(t *testing.T) {
	app := pbtest.New(t)
	b := brandOf(t, app, "default")
	if b["name"] != "StorEEE Market" || b["skin"] != "storeee" || b["short_name"] != "StorEEE" ||
		b["tagline"] != "Easy to find, Easy to learn, Easy to Sell" {
		t.Fatalf("brand %v", b)
	}
}

// An existing database (production) still has the GoDudY seed brand and
// operator keys the seed never had; the upgrade changes name and skin,
// adds short name and tagline, and keeps every other key.
func TestUpgradeFromGoDudYBrand(t *testing.T) {
	app := pbtest.New(t)
	setBrand(t, app, "default", map[string]any{"name": "GoDudY", "skin": "godudy", "logo": "/static/logo.svg"})
	rerun(t, app, "1764000006_storeee_brand.go")
	b := brandOf(t, app, "default")
	if b["name"] != "StorEEE Market" || b["skin"] != "storeee" || b["logo"] != "/static/logo.svg" ||
		b["tagline"] != "Easy to find, Easy to learn, Easy to Sell" {
		t.Fatalf("brand %v", b)
	}
	// idempotent: a second run changes nothing
	rerun(t, app, "1764000006_storeee_brand.go")
	if b2 := brandOf(t, app, "default"); len(b2) != len(b) || b2["name"] != b["name"] {
		t.Fatalf("second run changed the brand: %v", b2)
	}
}

func TestUpgradeKeepsEditedBrand(t *testing.T) {
	app := pbtest.New(t)
	edited := map[string]any{"name": "Other Market", "skin": "godudy", "tagline": "Own tagline"}
	setBrand(t, app, "default", edited)
	rerun(t, app, "1764000006_storeee_brand.go")
	b := brandOf(t, app, "default")
	if b["name"] != "Other Market" || b["skin"] != "godudy" || b["tagline"] != "Own tagline" {
		t.Fatalf("operator brand overwritten: %v", b)
	}
}

// downgradeSellers puts the sellers schema and GoDudY back to the state
// before 1764000007 (production on 2026-11-25): no profile fields, the
// old status list, GoDudY `draft`, plus one product with a dangling seller.
func downgradeSellers(t *testing.T, app core.App, status string) {
	t.Helper()
	col, err := app.FindCollectionByNameOrId("sellers")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"headline", "bio", "avatar", "website", "joined_at"} {
		col.Fields.RemoveByName(f)
	}
	col.Fields.GetByName("status").(*core.SelectField).Values = []string{"draft", "pending_review", "active", "suspended"}
	if err := app.Save(col); err != nil {
		t.Fatal(err)
	}
	s, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	s.Set("status", status)
	if err := app.Save(s); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB().NewQuery("UPDATE products SET seller = 'gone' WHERE slug = 'forms-lite'").Execute(); err != nil {
		t.Fatal(err)
	}
}

func TestFreshDatabaseHasGoDudYSeller(t *testing.T) {
	app := pbtest.New(t)
	s, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	if s.GetString("status") != "active" || s.GetString("display_name") != "GoDudY" || s.GetString("headline") == "" ||
		s.GetString("bio") == "" || s.GetDateTime("joined_at").IsZero() {
		t.Fatalf("seller %v", s.FieldsData())
	}
	// ADR-0016: no legal or KYC data in seeds
	if s.GetString("legal_name") != "" || s.GetString("legal_ids") != "{}" && s.GetString("legal_ids") != "" {
		t.Fatalf("legal data in seed: %q %q", s.GetString("legal_name"), s.GetString("legal_ids"))
	}
	n := 0
	if err := app.DB().NewQuery("SELECT COUNT(*) FROM products WHERE seller != {:s}").Bind(dbx.Params{"s": s.Id}).Row(&n); err != nil || n != 0 {
		t.Fatalf("products not linked to GoDudY: %d %v", n, err)
	}
}

func TestUpgradeSellerProfiles(t *testing.T) {
	app := pbtest.New(t)
	downgradeSellers(t, app, "draft")
	rerun(t, app, "1764000007_seller_profiles.go")
	s, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	if s.GetString("status") != "active" || s.GetString("headline") == "" || s.GetDateTime("joined_at").IsZero() {
		t.Fatalf("seller %v", s.FieldsData())
	}
	p, err := app.FindFirstRecordByData("products", "slug", "forms-lite")
	if err != nil || p.GetString("seller") != s.Id {
		t.Fatalf("dangling product not linked: %v %v", p, err)
	}
	// operator edits survive a second run
	s.Set("headline", "Edited")
	if err := app.Save(s); err != nil {
		t.Fatal(err)
	}
	rerun(t, app, "1764000007_seller_profiles.go")
	s, _ = app.FindFirstRecordByData("sellers", "slug", "godudy")
	if s.GetString("headline") != "Edited" || s.GetString("status") != "active" {
		t.Fatalf("second run changed the seller: %v", s.FieldsData())
	}
	col, _ := app.FindCollectionByNameOrId("sellers")
	vals := col.Fields.GetByName("status").(*core.SelectField).Values
	if len(vals) != 7 {
		t.Fatalf("status values %v", vals)
	}
}

func TestUpgradeKeepsSuspendedSeller(t *testing.T) {
	app := pbtest.New(t)
	downgradeSellers(t, app, "suspended")
	rerun(t, app, "1764000007_seller_profiles.go")
	s, _ := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if s.GetString("status") != "suspended" {
		t.Fatalf("status %q", s.GetString("status"))
	}
}

func TestUsersAPILock(t *testing.T) {
	app := pbtest.New(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	if users.CreateRule != nil {
		t.Fatalf("CreateRule = %v, want nil (server-only create)", *users.CreateRule)
	}
	if users.PasswordAuth.Enabled {
		t.Fatal("PasswordAuth should be disabled for the public API")
	}
	if users.OTP.Enabled || users.OAuth2.Enabled {
		t.Fatal("OTP and OAuth2 should stay disabled")
	}
}

func TestUsersAPILockUpgrade(t *testing.T) {
	app := pbtest.New(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	empty := ""
	users.CreateRule = &empty
	users.PasswordAuth.Enabled = true
	if err := app.Save(users); err != nil {
		t.Fatal(err)
	}
	rerun(t, app, "1764000011_users_api_lock.go")
	users, err = app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	if users.CreateRule != nil || users.PasswordAuth.Enabled {
		t.Fatal("upgrade did not lock the users API")
	}
}

func TestPaymentAttemptFields(t *testing.T) {
	app := pbtest.New(t)
	col, err := app.FindCollectionByNameOrId("payments")
	if err != nil {
		t.Fatal(err)
	}
	if col.Fields.GetByName("attempt") == nil || col.Fields.GetByName("legal_entity") == nil {
		t.Fatal("payments must have attempt and legal_entity (ADR-0033)")
	}
	col.Fields.RemoveByName("attempt")
	col.Fields.RemoveByName("legal_entity")
	col.RemoveIndex("idx_payments_order_attempt")
	if err := app.Save(col); err != nil {
		t.Fatal(err)
	}
	rerun(t, app, "1764000012_payment_attempts.go")
	col, err = app.FindCollectionByNameOrId("payments")
	if err != nil {
		t.Fatal(err)
	}
	if col.Fields.GetByName("attempt") == nil || col.Fields.GetByName("legal_entity") == nil {
		t.Fatal("upgrade did not restore attempt fields")
	}
}

// ADR-0035: the roles and content migration is expand-only, server-only
// (nil API rules) and safe to run again on a database that has it.
func TestRolesContentMigration(t *testing.T) {
	app := pbtest.New(t)
	rerun(t, app, "1764000013_roles_content.go")
	for _, name := range []string{"user_roles", "role_applications", "partners", "referral_clicks", "payouts",
		"articles", "pages", "collections", "reviews", "ui_messages"} {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatal(err)
		}
		if c.ListRule != nil || c.ViewRule != nil || c.CreateRule != nil || c.UpdateRule != nil || c.DeleteRule != nil {
			t.Fatalf("%s has API rules", name)
		}
	}
	n := 0
	if err := app.DB().NewQuery("SELECT COUNT(*) FROM pages WHERE slug IN ('terms','privacy','offer','refund')").Row(&n); err != nil || n != 4 {
		t.Fatalf("legal pages %d %v", n, err)
	}
	orders, _ := app.FindCollectionByNameOrId("orders")
	products, _ := app.FindCollectionByNameOrId("products")
	if orders.Fields.GetByName("referral") == nil || products.Fields.GetByName("review_note") == nil {
		t.Fatal("expand fields missing")
	}
}
