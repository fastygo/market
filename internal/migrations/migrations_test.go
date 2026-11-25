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
