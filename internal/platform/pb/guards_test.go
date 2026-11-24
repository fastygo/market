package pb_test

import (
	"errors"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/pb"
)

func TestGuardsReleaseImmutableAndAgent(t *testing.T) {
	app := newApp(t)
	rl, err := app.FindFirstRecordByFilter("product_releases", "status = 'draft'")
	if err != nil {
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
	_ = core.App(app)
}
