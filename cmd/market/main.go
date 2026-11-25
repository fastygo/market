// Command market is the storefront binary: PocketBase (Go extend mode) with
// templ SSR routes (ADR-0001, ADR-0010) and the phase 1 money path.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/licensing"
	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/mail/pbmail"
	"github.com/fastygo/market/internal/mail/resend"
	"github.com/fastygo/market/internal/mail/unisender"
	_ "github.com/fastygo/market/internal/migrations"
	"github.com/fastygo/market/internal/modules"
	"github.com/fastygo/market/internal/notify"
	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/fakegw"
	"github.com/fastygo/market/internal/payments/yookassa"
	"github.com/fastygo/market/internal/platform/clientip"
	"github.com/fastygo/market/internal/platform/metrics"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/region/ru"
	"github.com/fastygo/market/internal/taxdocs"
	"github.com/fastygo/market/internal/taxdocs/fakeissuer"
	"github.com/fastygo/market/internal/web"
	"github.com/fastygo/market/ui/dict"
)

var dictEN *i18n.Dict

func main() {
	// Memory budget (slo.md): soft limit 512 MiB unless GOMEMLIMIT is set.
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(512 << 20)
	}

	en, err := i18n.Parse(dict.EN)
	if err != nil {
		log.Fatal(err)
	}
	dictEN = en

	app := pocketbase.NewWithConfig(pocketbase.Config{DBConnect: pb.DBConnect})
	pb.RegisterGuards(app)

	// metrics (slo.md): statement timers on both SQLite pools, write tx
	// stats; the endpoint itself is opt-in on a loopback listener
	met := metrics.New()
	pb.TxStats, pb.TxLockWait = met.Tx, met.TxLockWait
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if err := e.Next(); err != nil {
			return err
		}
		pools := map[string]*dbx.DB{}
		if db, ok := e.App.ConcurrentDB().(*dbx.DB); ok {
			pools["concurrent"] = db
		}
		if db, ok := e.App.NonconcurrentDB().(*dbx.DB); ok {
			pools["nonconcurrent"] = db
		}
		met.InstrumentDB(pools)
		return nil
	})
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
		cfg, err := loadConfig(os.Getenv, se.App.DataDir())
		for _, w := range cfg.Warnings {
			log.Printf("WARNING config: %s", w) // stderr: the app logger is not on the console in production
			se.App.Logger().Warn("config: " + w)
		}
		if err != nil {
			return err
		}
		if cfg.Dev {
			log.Print("WARNING: DEVELOPMENT MODE (MARKET_FAKE_GATEWAY=1 or MARKET_DEV=1): defaults and fake providers are on; never use this in production")
			se.App.Logger().Warn("DEVELOPMENT MODE: defaults and fake providers are on; never use this in production")
		}
		storageMode := "local files " + cfg.FilesDir
		if cfg.S3 != nil {
			storageMode = "s3 " + cfg.S3.Bucket + "/" + cfg.S3.Prefix
		}
		se.App.Logger().Info("release storage", "mode", storageMode)
		repo := &pb.Catalog{App: se.App}
		commerce, svc := wire(se.App, cfg, fake, func(ctx context.Context) string {
			sf, err := repo.Storefront(ctx, code)
			if err != nil {
				return ""
			}
			return sf.BrandName
		})
		if sf, err := svc.Store.Storefront(context.Background(), code); err != nil {
			se.App.Logger().Warn("storefront for gateway check", "code", code, "err", err)
		} else {
			for _, w := range ordering.MissingGateways(sf.Providers, svc.Gateways) {
				log.Printf("WARNING storefront %s: %s", code, w)
				se.App.Logger().Warn("storefront method has no gateway", "storefront", code, "detail", w)
			}
		}
		h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: code, Dict: en, Log: se.App.Logger(), Commerce: commerce,
			BaseURL: cfg.PublicURL, Observe: met})
		if addr := os.Getenv("MARKET_METRICS_ADDR"); addr != "" {
			srv, err := met.Listen(addr)
			if err != nil {
				return err
			}
			se.App.Logger().Info("metrics and pprof on loopback", "addr", addr)
			se.App.OnTerminate().BindFunc(func(e *core.TerminateEvent) error {
				_ = srv.Close()
				return e.Next()
			})
		}
		wrapped := apis.WrapStdHandler(h)
		for _, p := range web.AllPatterns() {
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
func wire(app core.App, cfg config, fake bool, brand func(context.Context) string) (*web.Commerce, *ordering.Service) {
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
	publicURL, ip := cfg.PublicURL, cfg.ClientIP
	if gw := yooKassa(app, publicURL, ip); gw != nil {
		gateways[string(yookassa.Code)] = gw
		if os.Getenv("MARKET_YOOKASSA_RECEIPTS") != "0" {
			// receipts are registered with the payment; the issuer only
			// confirms them (replaces the fake register)
			issuer, orders.TaxDocs, orders.IssuerCode = yookassa.Receipts{G: gw}, ru.Receipts{}, string(yookassa.Code)
		}
	}
	svc := &ordering.Service{
		Store: orders, Tax: ru.TaxPolicy{}, Gateways: gateways, Issuer: issuer, PaymentTimeout: cfg.PaymentTimeout,
		RequiredConsents: []string{"terms"},
		ReturnURL:        func(orderID string) string { return publicURL + "/checkout/return/" + orderID },
	}
	sessionKey := cfg.SessionKey
	users := &pb.Users{App: app}
	magic := account.Magic{Key: sessionKey}
	mailer := mailSender(app)
	note := &notify.Service{Store: &pb.Account{App: app}, Users: users, Magic: magic, Mailer: mailer, Dict: dictEN,
		PublicURL: publicURL, Brand: brand}
	svc.Outbox = map[string]ordering.OutboxHandler{
		"email.purchase": func(ctx context.Context, key string, payload json.RawMessage) error {
			err := note.PurchaseEmail(ctx, key, payload)
			app.Logger().Info("email.purchase", "key", key, "mailer", mailer.Code(), "err", err)
			return err
		},
	}
	dlKey := cfg.DownloadKey
	// release files: S3 (pre-signed URLs) or local files served on /files
	// with the same HMAC-signed, expiring links
	var presigner licensing.Presigner
	var files http.Handler
	if cfg.S3 != nil {
		presigner = *cfg.S3
	} else {
		presigner = licensing.LocalPresigner{BaseURL: publicURL + "/files", Key: dlKey}
		files = licensing.LocalFiles{Dir: cfg.FilesDir, Key: dlKey}
	}
	lic := &licensing.Service{Store: &pb.Downloads{App: app}, Signer: licensing.Signer{Key: dlKey}, Presigner: presigner, URLTTL: cfg.URLTTL}
	cab := &web.Cabinet{Users: users, Magic: magic, Orders: &pb.Account{App: app}, Mailer: note, CSRFKey: sessionKey,
		Secure: strings.HasPrefix(publicURL, "https://"), AllowedOrigin: origin(publicURL),
		TermsVersion: env("MARKET_TERMS_VERSION", "1"), Method: env("MARKET_CHECKOUT_METHOD", "card")}
	return &web.Commerce{Auth: auth(app), Operator: operator(app), Orders: svc, Licenses: lic, FakePay: fakeGW,
		HashSalt: string(dlKey), Cabinet: cab, Files: files, ClientIP: ip.IP}, svc
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

// yooKassa builds the YooKassa adapter from env (ADR-0025); nil when no
// shop id is set. Test and live stores differ only in these values.
func yooKassa(app core.App, publicURL string, ip clientip.Resolver) *yookassa.Gateway {
	shop := os.Getenv("MARKET_YOOKASSA_SHOP_ID")
	if shop == "" {
		return nil
	}
	if publicURL == "" {
		log.Fatal("MARKET_PUBLIC_URL is required with YooKassa (absolute return URL)")
	}
	gw, err := yookassa.New(yookassa.Config{
		ShopID: shop, SecretKey: os.Getenv("MARKET_YOOKASSA_SECRET_KEY"), BaseURL: os.Getenv("MARKET_YOOKASSA_API_URL"),
		Receipts: os.Getenv("MARKET_YOOKASSA_RECEIPTS") != "0", RefundReceipts: os.Getenv("MARKET_YOOKASSA_REFUND_RECEIPTS") == "1",
		VATCode: ru.YooKassaVATCode, CheckIP: os.Getenv("MARKET_YOOKASSA_CHECK_IP") != "0",
		ClientIP: ip.IP,
	})
	if err != nil {
		log.Fatal(err)
	}
	app.Logger().Info("payment provider enabled", "provider", "yookassa")
	return gw
}

// mailSender builds the mail transport from env (ADR-0026). The default
// is the PocketBase mailer (admin UI Mail settings); while SMTP is
// disabled, messages are written as .eml files under pb_data/mail.
func mailSender(app core.App) mail.Sender {
	from, name := os.Getenv("MARKET_MAIL_FROM"), os.Getenv("MARKET_MAIL_FROM_NAME")
	fileSender := func() mail.Sender {
		dir := env("MARKET_MAIL_DIR", filepath.Join(app.DataDir(), "mail"))
		return mail.FileSender{Dir: dir, From: from}
	}
	switch p := env("MARKET_MAIL_PROVIDER", "pocketbase"); p {
	case "pocketbase":
		// PocketBase mailer with the admin UI Mail settings (ADR-0026); falls
		// back to files while SMTP is disabled so development still works.
		return &pbmail.Sender{
			Client: app.NewMailClient,
			Settings: func() pbmail.Settings {
				st := app.Settings()
				return pbmail.Settings{SMTPEnabled: st.SMTP.Enabled, SenderName: st.Meta.SenderName, SenderAddress: st.Meta.SenderAddress}
			},
			From: from, FromName: name, Sendmail: os.Getenv("MARKET_MAIL_SENDMAIL") == "1",
			Fallback: fileSender(), Log: app.Logger(),
		}
	case "unisender":
		s, err := unisender.New(unisender.Sender{APIKey: os.Getenv("MARKET_MAIL_API_KEY"), BaseURL: os.Getenv("MARKET_MAIL_API_URL"), From: from, FromName: name})
		if err != nil {
			log.Fatal(err)
		}
		return s
	case "resend":
		if name != "" {
			from = name + " <" + from + ">"
		}
		s, err := resend.New(resend.Sender{APIKey: os.Getenv("MARKET_MAIL_API_KEY"), BaseURL: os.Getenv("MARKET_MAIL_API_URL"), From: from})
		if err != nil {
			log.Fatal(err)
		}
		return s
	case "file":
		app.Logger().Warn("MARKET_MAIL_PROVIDER=file: emails are written to files (development only)")
		return fileSender()
	default:
		log.Fatalf("unknown MARKET_MAIL_PROVIDER %q", p)
		return nil
	}
}

func origin(u string) string {
	scheme, rest, ok := strings.Cut(u, "://")
	if !ok {
		return ""
	}
	host, _, _ := strings.Cut(rest, "/")
	return scheme + "://" + host
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// secret reads a development-only key from env (fake gateway webhooks);
// when unset it uses a random per-process key and warns.
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
