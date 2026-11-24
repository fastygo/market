// Command market is the storefront binary: PocketBase (Go extend mode) with
// templ SSR routes (ADR-0001, ADR-0010).
package main

import (
	"log"
	"os"
	"runtime/debug"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	"github.com/fastygo/market/internal/i18n"
	_ "github.com/fastygo/market/internal/migrations"
	"github.com/fastygo/market/internal/modules"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

func main() {
	// Memory budget (slo.md): soft limit 512 MiB unless GOMEMLIMIT is set.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(512 << 20)
	}

	en, err := i18n.Parse(dict.EN)
	if err != nil {
		log.Fatal(err)
	}

	app := pocketbase.NewWithConfig(pocketbase.Config{DBConnect: pb.DBConnect})
	pb.RegisterGuards(app)

	mods, err := registry().Resolve(enabledModules, "single_store")
	if err != nil {
		log.Fatal(err)
	}
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{Automigrate: false})

	code := os.Getenv("MARKET_STOREFRONT")
	if code == "" {
		code = "default"
	}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		repo := &pb.Catalog{App: se.App}
		h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: code, Dict: en, Log: se.App.Logger()})
		wrapped := apis.WrapStdHandler(h)
		for _, p := range web.Patterns {
			method, path := "GET", p[len("GET "):]
			se.Router.Route(method, path, wrapped)
		}
		return se.Next()
	})

	for _, m := range mods {
		app.Logger().Info("module", "id", m.ID, "version", m.Version)
	}

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// enabledModules are the Go modules wired in phase 0; per-storefront module
// config (storefronts.modules) is read in phase 1.
var enabledModules = []string{"core", "locale-en", "product-type-plain"}

func registry() *modules.Registry {
	r := modules.New()
	for _, m := range []modules.Manifest{
		{ID: "core", Version: "0.1.0"},
		{ID: "locale-en", Version: "0.1.0", Provides: []string{"locale.format@1"}},
		{ID: "product-type-plain", Version: "0.1.0", Provides: []string{"product.type.plain@1"}},
	} {
		if err := r.Register(m); err != nil {
			log.Fatal(err)
		}
	}
	return r
}
