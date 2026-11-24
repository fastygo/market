package pb_test

import (
	"errors"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/pb"
)

func TestGuardsReleaseImmutableAndAgent(t *testing.T) {
	app := newApp(t)
	pub, err := app.FindFirstRecordByFilter("product_releases", "status = 'published'")
	if err != nil {
		t.Fatal(err)
	}
	rl := core.NewRecord(pub.Collection())
	rl.Load(map[string]any{"product": pub.GetString("product"), "seller": pub.GetString("seller"), "version": "2.0.0", "status": "draft"})
	if err := app.Save(rl); err != nil {
		t.Fatal(err)
	}
	rl.Set("storage_key", "releases/x/1.0.0.zip")
	rl.Set("sha256", "00")
	rl.Set("size", 10)
	rl.Set("status", "published")
	if err := app.Save(rl); err != nil {
		t.Fatalf("publishing a draft must work: %v", err)
	}
	rl.Set("sha256", "11")
	if err := app.Save(rl); !errors.Is(err, pb.ErrReleaseImmutable) {
		t.Fatalf("want immutable, got %v", err)
	}
	rl.Set("sha256", "00")
	rl.Set("status", "draft")
	if err := app.Save(rl); !errors.Is(err, pb.ErrReleaseImmutable) {
		t.Fatalf("want immutable, got %v", err)
	}
	rl.Set("status", "yanked")
	if err := app.Save(rl); err != nil {
		t.Fatalf("yank must work: %v", err)
	}

	seller, _ := app.FindFirstRecordByData("sellers", "slug", "godudy")
	seller.Set("sale_model", "agent")
	if err := app.Save(seller); err == nil {
		t.Fatal("agent seller must be rejected in single_store")
	}
	seller.Set("sale_model", "principal")
	if err := app.Save(seller); err != nil {
		t.Fatal(err)
	}
}

func TestGuardsDenyAPIWritesToLockedCollections(t *testing.T) {
	app := newApp(t)
	for _, name := range []string{"orders", "payments", "ledger_entries", "entitlements", "outbox", "payment_events", "downloads", "order_items", "consents"} {
		col, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatal(err)
		}
		if col.CreateRule != nil || col.UpdateRule != nil || col.DeleteRule != nil {
			t.Errorf("%s: write rules must be nil", name)
		}
		e := &core.RecordRequestEvent{RequestEvent: &core.RequestEvent{App: app}, Record: core.NewRecord(col)}
		e.Collection = col
		reached := false
		err = app.OnRecordCreateRequest().Trigger(e, func(*core.RecordRequestEvent) error { reached = true; return nil })
		if err == nil || reached {
			t.Errorf("%s: API create must be denied", name)
		}
		err = app.OnRecordDeleteRequest().Trigger(e, func(*core.RecordRequestEvent) error { reached = true; return nil })
		if err == nil || reached {
			t.Errorf("%s: API delete must be denied", name)
		}
	}
	// unlocked collections pass
	col, _ := app.FindCollectionByNameOrId("products")
	e := &core.RecordRequestEvent{RequestEvent: &core.RequestEvent{App: app}, Record: core.NewRecord(col)}
	e.Collection = col
	if err := app.OnRecordCreateRequest().Trigger(e, func(*core.RecordRequestEvent) error { return nil }); err != nil {
		t.Errorf("products must not be locked: %v", err)
	}
}

func TestLedgerAppendOnly(t *testing.T) {
	app := newApp(t)
	col, _ := app.FindCollectionByNameOrId("ledger_entries")
	r := core.NewRecord(col)
	r.Load(map[string]any{"txn_id": "t1", "account": "provider_clearing", "amount_minor": 1, "currency": "RUB", "ref_type": "test", "ref_id": "x"})
	if err := app.Save(r); err != nil {
		t.Fatal(err)
	}
	r.Set("amount_minor", 2)
	if err := app.Save(r); !errors.Is(err, pb.ErrLedgerAppendOnly) {
		t.Fatalf("update: %v", err)
	}
	if err := app.Delete(r); !errors.Is(err, pb.ErrLedgerAppendOnly) {
		t.Fatalf("delete: %v", err)
	}
}
