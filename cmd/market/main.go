// Command market is the storefront binary: PocketBase (Go extend mode) with
// templ SSR routes (ADR-0001, ADR-0010) and the phase 1 money path.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/licensing"
	_ "github.com/fastygo/market/internal/migrations"
	"github.com/fastygo/market/internal/modules"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/fakegw"
	"github.com/fastygo/market/internal/payments/yookassa"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/s3"
	"github.com/fastygo/market/internal/region/ru"
	"github.com/fastygo/market/internal/taxdocs"
	"github.com/fastygo/market/internal/taxdocs/fakeissuer"
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
	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{Automigrate: false})

	code := env("MARKET_STOREFRONT", "default")
	fake := os.Getenv("MARKET_FAKE_GATEWAY") == "1"

	enabled := []string{"core", "locale-en", "product-type-plain", "region-ru", "ordering"}
	if fake {
		enabled = append(enabled, "payment-fake")
	}
	if os.Getenv("MARKET_YOOKASSA_SHOP_ID") != "" {
		enabled = append(enabled, "payment-yookassa")
	}
	storage := "storage-local"
	if os.Getenv("MARKET_S3_ENDPOINT") != "" {
		storage = "storage-s3"
	}
	enabled = append(enabled, storage)
	mods, err := registry().Resolve(enabled, "single_store")
	if err != nil {
		log.Fatal(err)
	}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		for _, m := range mods {
			se.App.Logger().Info("module", "id", m.ID, "version", m.Version)
		}
		repo := &pb.Catalog{App: se.App}
		commerce, svc := wire(se.App, fake)
		h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: code, Dict: en, Log: se.App.Logger(), Commerce: commerce})
		wrapped := apis.WrapStdHandler(h)
		for _, p := range append(append([]string{}, web.Patterns...), web.CommercePatterns...) {
			method, path, _ := strings.Cut(p, " ")
			se.Router.Route(method, path, wrapped)
		}

		// exactly one inbox and one outbox worker; reconciliation every 5 min
		ctx, stop := context.WithCancel(context.Background())
		svc.RunWorkers(ctx, 2*time.Second, func(err error) { se.App.Logger().Warn("worker", "err", err) })
		se.App.OnTerminate().BindFunc(func(e *core.TerminateEvent) error { stop(); return e.Next() })
		se.App.Cron().MustAdd("market_payment_timeouts", "* * * * *", func() {
			if n, err := svc.RequestTimeouts(ctx); err != nil || n > 0 {
				se.App.Logger().Info("payment timeouts", "requested", n, "err", err)
			}
		})
		se.App.Cron().MustAdd("market_reconcile", "*/5 * * * *", func() {
			if n, err := svc.Reconcile(ctx); err != nil || n > 0 {
				se.App.Logger().Info("reconcile", "items", n, "err", err)
			}
		})
		return se.Next()
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// wire builds the ordering and licensing services from env (ADR-0016:
// secrets only from env; nothing here has a real default).
func wire(app core.App, fake bool) (*web.Commerce, *ordering.Service) {
	gateways := map[string]payments.Gateway{}
	var fakeGW *fakegw.Gateway
	if fake {
		fakeGW = fakegw.New(secret("MARKET_FAKE_WEBHOOK_SECRET", app))
		gateways[string(fakegw.Code)] = fakeGW
		app.Logger().Warn("fake payment gateway enabled (development only)")
	}
	orders := &pb.Orders{App: app}
	var issuer taxdocs.Issuer
	if fake {
		// phase 1: receipts go to the fake register; a real
		// DocumentIssuer module replaces it with the real provider
		fi := fakeissuer.New()
		issuer, orders.TaxDocs, orders.IssuerCode = fi, ru.Receipts{}, fi.Code()
		app.Logger().Warn("fake tax document issuer enabled (development only)")
	}
	publicURL := strings.TrimRight(os.Getenv("MARKET_PUBLIC_URL"), "/")
	if gw := yooKassa(app, publicURL); gw != nil {
		gateways[string(yookassa.Code)] = gw
		if os.Getenv("MARKET_YOOKASSA_RECEIPTS") != "0" {
			// receipts are registered with the payment; the issuer only
			// confirms them (replaces the fake register)
			issuer, orders.TaxDocs, orders.IssuerCode = yookassa.Receipts{G: gw}, ru.Receipts{}, string(yookassa.Code)
		}
	}
	timeout, err := time.ParseDuration(env("MARKET_PAYMENT_TIMEOUT", "30m"))
	if err != nil {
		log.Fatal(err)
	}
	svc := &ordering.Service{
		Store: orders, Tax: ru.TaxPolicy{}, Gateways: gateways, Issuer: issuer, PaymentTimeout: timeout,
		RequiredConsents: []string{"terms"},
		ReturnURL:        func(orderID string) string { return publicURL + "/checkout/return/" + orderID },
		Outbox: map[string]ordering.OutboxHandler{
			// phase 1: log only; the mail transport module comes later
			"email.purchase": func(ctx context.Context, key string, payload json.RawMessage) error {
				var p struct {
					Number string `json:"number"`
				}
				_ = json.Unmarshal(payload, &p)
				app.Logger().Info("email.purchase", "key", key, "order", p.Number)
				return nil
			},
		},
	}
	dlKey := secret("MARKET_DOWNLOAD_SECRET", app)
	var presigner licensing.Presigner = licensing.LocalPresigner{BaseURL: env("MARKET_S3_LOCAL_BASE", "http://127.0.0.1:9000/market-dev"), Key: dlKey}
	if ep := os.Getenv("MARKET_S3_ENDPOINT"); ep != "" {
		presigner = s3.Presigner{
			Endpoint: ep, Region: os.Getenv("MARKET_S3_REGION"), Bucket: os.Getenv("MARKET_S3_BUCKET"),
			AccessKey: os.Getenv("MARKET_S3_ACCESS_KEY"), SecretKey: os.Getenv("MARKET_S3_SECRET_KEY"),
			PathStyle: os.Getenv("MARKET_S3_PATH_STYLE") != "0",
		}
	}
	ttl, err := time.ParseDuration(env("MARKET_S3_URL_TTL", "5m"))
	if err != nil {
		log.Fatal(err)
	}
	lic := &licensing.Service{Store: &pb.Downloads{App: app}, Signer: licensing.Signer{Key: dlKey}, Presigner: presigner, URLTTL: ttl}
	return &web.Commerce{Auth: auth(app), Operator: operator(app), Orders: svc, Licenses: lic, FakePay: fakeGW, HashSalt: string(dlKey)}, svc
}

// auth reads a PocketBase users auth token from the Authorization header.
func auth(app core.App) func(r *http.Request) (web.Buyer, bool) {
	return func(r *http.Request) (web.Buyer, bool) {
		tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if tok == "" {
			return web.Buyer{}, false
		}
		rec, err := app.FindAuthRecordByToken(tok, core.TokenTypeAuth)
		if err != nil || rec.Collection().Name != "users" {
			return web.Buyer{}, false
		}
		return web.Buyer{ID: rec.Id, Email: rec.Email()}, true
	}
}

// operator reads a PocketBase superuser auth token (operator commands).
func operator(app core.App) func(r *http.Request) (string, bool) {
	return func(r *http.Request) (string, bool) {
		tok := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		if tok == "" {
			return "", false
		}
		rec, err := app.FindAuthRecordByToken(tok, core.TokenTypeAuth)
		if err != nil || !rec.IsSuperuser() {
			return "", false
		}
		return rec.Email(), true
	}
}

// yooKassa builds the YooKassa adapter from env (ADR-0024); nil when no
// shop id is set. Test and live stores differ only in these values.
func yooKassa(app core.App, publicURL string) *yookassa.Gateway {
	shop := os.Getenv("MARKET_YOOKASSA_SHOP_ID")
	if shop == "" {
		return nil
	}
	if publicURL == "" {
		log.Fatal("MARKET_PUBLIC_URL is required with YooKassa (absolute return URL)")
	}
	header := os.Getenv("MARKET_CLIENT_IP_HEADER") // e.g. X-Forwarded-For behind a reverse proxy
	gw, err := yookassa.New(yookassa.Config{
		ShopID: shop, SecretKey: os.Getenv("MARKET_YOOKASSA_SECRET_KEY"), BaseURL: os.Getenv("MARKET_YOOKASSA_API_URL"),
		Receipts: os.Getenv("MARKET_YOOKASSA_RECEIPTS") != "0", RefundReceipts: os.Getenv("MARKET_YOOKASSA_REFUND_RECEIPTS") == "1",
		VATCode: ru.YooKassaVATCode, CheckIP: os.Getenv("MARKET_YOOKASSA_CHECK_IP") != "0",
		ClientIP: func(r *http.Request) string { return clientIP(r, header) },
	})
	if err != nil {
		log.Fatal(err)
	}
	app.Logger().Info("payment provider enabled", "provider", "yookassa")
	return gw
}

// clientIP is the request address; behind a reverse proxy the first entry
// of the configured forwarded header.
func clientIP(r *http.Request, header string) string {
	if header != "" {
		if v := r.Header.Get(header); v != "" {
			first, _, _ := strings.Cut(v, ",")
			return strings.TrimSpace(first)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// secret reads a key from env; when unset it uses a random per-process key
// (links and webhooks do not survive a restart) and warns.
func secret(k string, app core.App) []byte {
	if v := os.Getenv(k); v != "" {
		return []byte(v)
	}
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	app.Logger().Warn("secret not set, using a random per-process key", "env", k)
	return b
}

func registry() *modules.Registry {
	r := modules.New()
	for _, m := range []modules.Manifest{
		{ID: "core", Version: "0.1.0"},
		{ID: "locale-en", Version: "0.1.0", Provides: []string{"locale.format@1"}},
		{ID: "product-type-plain", Version: "0.1.0", Provides: []string{"product.type.plain@1"}},
		{ID: "region-ru", Version: "0.1.0", Provides: []string{"tax.rules@1"}},
		{ID: "payment-fake", Version: "0.1.0", Provides: []string{"payment.gateway@1"}},
		{ID: "payment-yookassa", Version: "0.1.0", Provides: []string{"payment.gateway@1"}},
		{ID: "storage-local", Version: "0.1.0", Provides: []string{"storage.presign@1"}},
		{ID: "storage-s3", Version: "0.1.0", Provides: []string{"storage.presign@1"}},
		{ID: "ordering", Version: "0.1.0", Requires: []string{"tax.rules@1", "storage.presign@1", "payment.gateway@1?"}},
	} {
		if err := r.Register(m); err != nil {
			log.Fatal(err)
		}
	}
	return r
}
