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

// A draft or archived item nobody bought or reviewed is deleted with its
// offers and releases (keys returned for file cleanup) and drops out of
// editorial collections; anything else is refused and left untouched.
func TestDeleteItem(t *testing.T) {
	app := pbtest.New(t)
	ctx := context.Background()
	users, _ := app.FindCollectionByNameOrId("users")
	newUser := func(email string) *core.Record {
		u := core.NewRecord(users)
		u.SetEmail(email)
		u.SetPassword("test-password-123")
		if err := app.Save(u); err != nil {
			t.Fatal(err)
		}
		return u
	}
	u, outsider := newUser("del@example.com"), newUser("other@example.com")
	seller, err := app.FindFirstRecordByData("sellers", "slug", "godudy")
	if err != nil {
		t.Fatal(err)
	}
	members, _ := app.FindCollectionByNameOrId("seller_members")
	m := core.NewRecord(members)
	m.Load(map[string]any{"seller": seller.Id, "user": u.Id, "role": "staff"})
	if err := app.Save(m); err != nil {
		t.Fatal(err)
	}
	a, rels := &pb.Authoring{App: app}, &pb.Releases{App: app}
	newItem := func(title string, price int64) authoring.Item {
		t.Helper()
		it, err := a.Create(ctx, u.Id, seller.Id, authoring.Meta{Title: title, Price: price, Extended: price * 3})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}

	it := newItem("Delete Kit", 1000)
	for _, v := range []string{"1.0.0", "1.1.0"} {
		r, err := rels.AddRelease(ctx, u.Id, it.ID, authoring.NewRelease{Version: v, StorageKey: "godudy/delete-kit/delete-kit-" + v + ".zip", SHA256: "ab", Size: 3})
		if err != nil {
			t.Fatal(err)
		}
		if v == "1.0.0" {
			if err := rels.ModerateRelease(ctx, r.ID, "published", "", "editor@example.com"); err != nil {
				t.Fatal(err)
			}
		}
	}
	cols, _ := app.FindCollectionByNameOrId("collections")
	col := core.NewRecord(cols)
	col.Load(map[string]any{"slug": "del-col", "title": "Del", "status": "draft", "products": []string{it.ID}})
	if err := app.Save(col); err != nil {
		t.Fatal(err)
	}

	if d, err := a.ItemDeletion(ctx, u.Id, it.ID); err != nil || !d.Allowed {
		t.Fatalf("draft without buyers: %+v %v", d, err)
	}
	if _, err := a.DeleteItem(ctx, outsider.Id, it.ID); !errors.Is(err, authoring.ErrDenied) {
		t.Fatalf("outsider delete: %v", err)
	}
	keys, err := a.DeleteItem(ctx, u.Id, it.ID)
	if err != nil || len(keys) != 2 {
		t.Fatalf("delete: %v %v", keys, err)
	}
	if _, err := app.FindRecordById("products", it.ID); err == nil {
		t.Fatal("product still there")
	}
	for _, tbl := range []string{"offers", "product_releases"} {
		var n int
		_ = app.DB().NewQuery("SELECT COUNT(*) FROM " + tbl + " WHERE product = {:p}").Bind(dbx.Params{"p": it.ID}).Row(&n)
		if n != 0 {
			t.Fatalf("%s rows left: %d", tbl, n)
		}
	}
	col, _ = app.FindRecordById("collections", col.Id)
	if len(col.GetStringSlice("products")) != 0 {
		t.Fatalf("collection still lists the item: %v", col.GetStringSlice("products"))
	}
	if _, err := a.DeleteItem(ctx, u.Id, it.ID); !errors.Is(err, authoring.ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	// in review: archive first
	busy := newItem("Busy Kit", 1000)
	if _, err := a.Act(ctx, u.Id, busy.ID, authoring.ActSubmit); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.ItemDeletion(ctx, u.Id, busy.ID); d.Allowed || d.Reason != authoring.DeleteStatus {
		t.Fatalf("in review deletion: %+v", d)
	}
	if _, err := a.DeleteItem(ctx, u.Id, busy.ID); !errors.Is(err, authoring.ErrConflict) {
		t.Fatalf("delete in review: %v", err)
	}
	// archived with a review: in use, archive stays the way
	if _, err := a.Act(ctx, u.Id, busy.ID, authoring.ActArchive); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.ItemDeletion(ctx, u.Id, busy.ID); !d.Allowed {
		t.Fatalf("archived, no buyers: %+v", d)
	}
	if _, err := app.DB().NewQuery(`INSERT INTO reviews (id, product, user, rating, status) VALUES ('revtestdel00001', {:p}, {:u}, 5, 'pending')`).
		Bind(dbx.Params{"p": busy.ID, "u": outsider.Id}).Execute(); err != nil {
		t.Fatal(err)
	}
	if d, _ := a.ItemDeletion(ctx, u.Id, busy.ID); d.Allowed || d.Reason != authoring.DeleteInUse {
		t.Fatalf("reviewed deletion: %+v", d)
	}
	if _, err := a.DeleteItem(ctx, u.Id, busy.ID); !errors.Is(err, authoring.ErrInUse) {
		t.Fatalf("delete reviewed: %v", err)
	}
	if _, err := app.FindRecordById("products", busy.ID); err != nil {
		t.Fatal("refused delete removed the product")
	}

	// archived with a license: in use
	sold := newItem("Sold Kit", 1000)
	if _, err := a.Act(ctx, u.Id, sold.ID, authoring.ActArchive); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB().NewQuery(`INSERT INTO entitlements (id, user, seller, order_item, product, license_type, purchase_code, status)
		VALUES ('enttestdel00001', {:u}, {:s}, 'oi0', {:p}, 'regular', 'PC-DEL', 'active')`).
		Bind(dbx.Params{"u": outsider.Id, "s": seller.Id, "p": sold.ID}).Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteItem(ctx, u.Id, sold.ID); !errors.Is(err, authoring.ErrInUse) {
		t.Fatalf("delete sold: %v", err)
	}
}
