package pb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
	"github.com/fastygo/market/internal/platform/tx"
)

func TestRunTxFlagsAndImmediate(t *testing.T) {
	app := pbtest.New(t)
	ctx := context.Background()
	err := pb.RunTx(ctx, app, "t", func(ctx context.Context, txApp core.App) error {
		if !tx.InTx(ctx) || tx.Open() != 1 {
			t.Fatalf("in tx flag %v open %d", tx.InTx(ctx), tx.Open())
		}
		// nested call reuses the transaction
		return pb.RunTx(ctx, app, "nested", func(ctx context.Context, inner core.App) error {
			if inner != txApp {
				t.Fatal("nested tx must reuse txApp")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.Open() != 0 {
		t.Fatal("counter not released")
	}
	var mode struct {
		Timeout int `db:"timeout"`
	}
	if err := app.NonconcurrentDB().NewQuery("PRAGMA busy_timeout").One(&mode); err != nil {
		t.Fatal(err)
	}
	if mode.Timeout != int(pb.BusyTimeout.Milliseconds()) {
		t.Fatalf("busy_timeout = %d", mode.Timeout)
	}
	boom := errors.New("boom")
	if err := pb.RunTx(ctx, app, "t", func(context.Context, core.App) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
}
