// Package pbtest boots a PocketBase app for integration tests the same way
// cmd/market does: our DB connector (BEGIN IMMEDIATE, busy_timeout), the
// write-protection hooks and all migrations including the placeholder seed.
package pbtest

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	_ "github.com/fastygo/market/internal/migrations" // register migrations
	"github.com/fastygo/market/internal/platform/pb"
)

// New returns a migrated app in a temp dir, reset on cleanup.
func New(t testing.TB) core.App {
	t.Helper()
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir(), DBConnect: pb.DBConnect})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	pb.RegisterGuards(app)
	if err := app.RunAllMigrations(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	return app
}
