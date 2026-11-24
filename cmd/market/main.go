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
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

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
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/s3"
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
		commerce, svc := wire(se.App, fake, func(ctx context.Context) string {
			sf, err := repo.Storefront(ctx, code)
			if err != nil {
				return ""
			}
			return sf.BrandName
		})
		h := web.New(web.Handler{Catalog: repo, Storefronts: repo, StorefrontCode: code, Dict: en, Log: se.App.Logger(), Commerce: commerce})
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
func wire(app core.App, fake bool, brand func(context.Context) string) (*web.Commerce, *ordering.Service) {
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
	}
	sessionKey := secret("MARKET_SESSION_SECRET", app)
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
	dlKey := secret("MARKET_DOWNLOAD_SECRET", app)
	var presigner licensing.Presigner = licensing.LocalPresigner{BaseURL: env("MARKET_S3_LOCAL_BASE", "http://127.0.0.1:9000/market-dev"), Key: dlKey}
	var files http.Handler
	if dir := os.Getenv("MARKET_FILES_DIR"); dir != "" && os.Getenv("MARKET_S3_ENDPOINT") == "" {
		// single-host releases: the app serves signed local URLs
		presigner = licensing.LocalPresigner{BaseURL: publicURL + "/files", Key: dlKey}
		files = licensing.LocalFiles{Dir: dir, Key: dlKey}
	}
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
	cab := &web.Cabinet{Users: users, Magic: magic, Orders: &pb.Account{App: app}, Mailer: note, CSRFKey: sessionKey,
		Secure: strings.HasPrefix(publicURL, "https://"), AllowedOrigin: origin(publicURL),
		TermsVersion: env("MARKET_TERMS_VERSION", "1"), Method: env("MARKET_CHECKOUT_METHOD", "card")}
	return &web.Commerce{Auth: auth(app), Operator: operator(app), Orders: svc, Licenses: lic, FakePay: fakeGW,
		HashSalt: string(dlKey), Cabinet: cab, Files: files}, svc
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
