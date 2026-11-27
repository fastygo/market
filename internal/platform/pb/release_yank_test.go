package pb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

// The only published release of an item with buyers cannot be yanked; once
// a newer one is published it can.
func TestYankKeepsBuyersServed(t *testing.T) {
	app := pbtest.New(t)
	ctx := context.Background()
	users, _ := app.FindCollectionByNameOrId("users")
	u := core.NewRecord(users)
	u.SetEmail("yank@example.com")
	u.SetPassword("test-password-123")
	if err := app.Save(u); err != nil {
		t.Fatal(err)
	}
	seller, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	members, _ := app.FindCollectionByNameOrId("seller_members")
	m := core.NewRecord(members)
	m.Set("seller", seller.Id)
	m.Set("user", u.Id)
	m.Set("role", "staff")
	if err := app.Save(m); err != nil {
		t.Fatal(err)
	}
	it, err := (&pb.Authoring{App: app}).Create(ctx, u.Id, seller.Id, authoring.Meta{Title: "Yank Kit"})
	if err != nil {
		t.Fatal(err)
	}
	rels := &pb.Releases{App: app}
	publish := func(v string) string {
		t.Helper()
		r, err := rels.AddRelease(ctx, u.Id, it.ID, authoring.NewRelease{Version: v, StorageKey: "godudy/yank-kit/yank-kit-" + v + ".zip", SHA256: "ab", Size: 3})
		if err != nil {
			t.Fatal(err)
		}
		if err := rels.ModerateRelease(ctx, r.ID, "published", "", "editor@example.com"); err != nil {
			t.Fatal(err)
		}
		return r.ID
	}
	first := publish("1.0.0")
	if _, err := app.DB().NewQuery(`INSERT INTO entitlements (id, user, seller, order_item, product, license_type, purchase_code, status)
		VALUES ('enttestyank0001', {:u}, {:s}, 'oi0', {:p}, 'regular', 'PC-1', 'active')`).
		Bind(dbx.Params{"u": u.Id, "s": seller.Id, "p": it.ID}).Execute(); err != nil {
		t.Fatal(err)
	}
	if err := rels.YankRelease(ctx, u.Id, it.ID, first); !errors.Is(err, authoring.ErrLastRelease) {
		t.Fatalf("yank the last release with buyers: %v", err)
	}
	publish("1.1.0")
	if err := rels.YankRelease(ctx, u.Id, it.ID, first); err != nil {
		t.Fatalf("yank with a newer release: %v", err)
	}
	if err := rels.YankRelease(ctx, u.Id, it.ID, first); !errors.Is(err, authoring.ErrInvalid) {
		t.Fatalf("yank twice: %v", err)
	}
	// a yanked release is immutable and never deleted
	if _, err := rels.DeleteRelease(ctx, u.Id, it.ID, first); !errors.Is(err, authoring.ErrInvalid) {
		t.Fatalf("delete yanked: %v", err)
	}
	q, err := rels.ReleaseQueue(ctx, "yanked")
	if err != nil || len(q) != 1 || q[0].ID != first {
		t.Fatalf("yanked queue %+v %v", q, err)
	}
	if _, err := rels.ReleaseQueue(ctx, "nope"); !errors.Is(err, authoring.ErrInvalid) {
		t.Fatalf("bad status: %v", err)
	}
}
