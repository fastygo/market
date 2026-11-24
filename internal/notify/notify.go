// Package notify builds and sends buyer emails (ADR-0026): the purchase
// email (outbox topic "email.purchase", retried by the outbox with the
// outbox key as the provider idempotency key) and the sign-in link. Texts
// come from the dictionary; links point to the public URL of the
// storefront and open the buyer cabinet through a magic link.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"strings"
	"time"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/mail"
	"github.com/fastygo/market/internal/platform/money"
)

// Item is a purchased line.
type Item struct{ Title, Slug string }

// Purchase is what the purchase email needs.
type Purchase struct {
	OrderID, Number, BuyerID, Email string
	Total                           money.Money
	Items                           []Item
}

// Purchases reads an order for its email.
type Purchases interface {
	Purchase(ctx context.Context, orderID string) (Purchase, error)
}

// Service sends buyer emails.
type Service struct {
	Store     Purchases
	Users     account.Users
	Magic     account.Magic
	Mailer    mail.Sender
	Dict      *i18n.Dict
	PublicURL string
	Brand     func(ctx context.Context) string
	// LinkTTL of the cabinet link in the purchase email (default 72 h);
	// SignInTTL of a requested sign-in link (default 20 min).
	LinkTTL, SignInTTL time.Duration
}

// ErrNoPublicURL: links need an absolute base.
var ErrNoPublicURL = errors.New("notify: MARKET_PUBLIC_URL is not set")

func (s *Service) brand(ctx context.Context) string {
	if s.Brand != nil {
		if b := s.Brand(ctx); b != "" {
			return b
		}
	}
	return "Store"
}

func (s *Service) t(key string, kv ...string) string {
	v := s.Dict.T(key)
	for i := 0; i+1 < len(kv); i += 2 {
		v = strings.ReplaceAll(v, "{"+kv[i]+"}", kv[i+1])
	}
	return v
}

func (s *Service) link(u account.User, ttl time.Duration, next string) string {
	return strings.TrimRight(s.PublicURL, "/") + "/account/magic/" + s.Magic.Sign(u, ttl, next)
}

// PurchaseEmail is the outbox handler of "email.purchase".
func (s *Service) PurchaseEmail(ctx context.Context, key string, payload json.RawMessage) error {
	if s.PublicURL == "" {
		return ErrNoPublicURL
	}
	var p struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || p.OrderID == "" {
		return errors.New("notify: bad purchase payload")
	}
	pu, err := s.Store.Purchase(ctx, p.OrderID)
	if err != nil {
		return err
	}
	u, err := s.Users.ByID(ctx, pu.BuyerID)
	if err != nil {
		return err
	}
	ttl := s.LinkTTL
	if ttl == 0 {
		ttl = 72 * time.Hour
	}
	brand := s.brand(ctx)
	orderURL := s.link(u, ttl, "/account/orders/"+pu.Number)
	titles := make([]string, len(pu.Items))
	for i, it := range pu.Items {
		titles[i] = it.Title
	}
	d := view{
		Brand: brand, Heading: s.t("mail.purchase.heading", "number", pu.Number),
		Lead:   s.t("mail.purchase.lead"),
		Items:  titles,
		Total:  s.t("mail.purchase.total", "amount", pu.Total.String()),
		Action: s.t("mail.purchase.action"), URL: orderURL,
		Note: s.t("mail.purchase.note", "hours", "72"),
	}
	return s.send(ctx, mail.Message{To: pu.Email, Subject: s.t("mail.purchase.subject", "number", pu.Number, "brand", brand),
		IdempotencyKey: key, Tags: []string{"purchase"}}, d)
}

// SignInEmail sends a sign-in link to an existing account.
func (s *Service) SignInEmail(ctx context.Context, u account.User, next string) error {
	if s.PublicURL == "" {
		return ErrNoPublicURL
	}
	ttl := s.SignInTTL
	if ttl == 0 {
		ttl = 20 * time.Minute
	}
	brand := s.brand(ctx)
	d := view{Brand: brand, Heading: s.t("mail.signin.heading", "brand", brand), Lead: s.t("mail.signin.lead"),
		Action: s.t("mail.signin.action"), URL: s.link(u, ttl, next), Note: s.t("mail.signin.note", "minutes", "20")}
	return s.send(ctx, mail.Message{To: u.Email, Subject: s.t("mail.signin.subject", "brand", brand), Tags: []string{"signin"}}, d)
}

type view struct {
	Brand, Heading, Lead, Total, Action, URL, Note string
	Items                                          []string
}

var htmlTpl = template.Must(template.New("mail").Parse(`<!doctype html><html><body style="margin:0;padding:24px;background:#f6f6f6;font-family:Arial,sans-serif;color:#1a1a1a">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0"><tr><td align="center">
<table role="presentation" width="560" cellpadding="0" cellspacing="0" style="background:#ffffff;border-radius:12px;padding:32px">
<tr><td style="font-size:14px;color:#666">{{.Brand}}</td></tr>
<tr><td style="font-size:22px;font-weight:bold;padding:12px 0">{{.Heading}}</td></tr>
<tr><td style="font-size:15px;line-height:22px">{{.Lead}}</td></tr>
{{range .Items}}<tr><td style="font-size:15px;padding:4px 0">&#8226; {{.}}</td></tr>{{end}}
{{if .Total}}<tr><td style="font-size:15px;padding:8px 0;font-weight:bold">{{.Total}}</td></tr>{{end}}
<tr><td style="padding:20px 0"><a href="{{.URL}}" style="background:#ea580c;color:#ffffff;text-decoration:none;padding:12px 20px;border-radius:8px;font-weight:bold;display:inline-block">{{.Action}}</a></td></tr>
<tr><td style="font-size:13px;color:#666;line-height:20px">{{.Note}}<br>{{.URL}}</td></tr>
</table></td></tr></table></body></html>`))

func (s *Service) send(ctx context.Context, m mail.Message, d view) error {
	var b bytes.Buffer
	if err := htmlTpl.Execute(&b, d); err != nil {
		return err
	}
	m.HTML = b.String()
	var t strings.Builder
	t.WriteString(d.Heading + "\n\n" + d.Lead + "\n")
	for _, it := range d.Items {
		t.WriteString("- " + it + "\n")
	}
	if d.Total != "" {
		t.WriteString(d.Total + "\n")
	}
	t.WriteString("\n" + d.Action + ": " + d.URL + "\n\n" + d.Note + "\n\n" + d.Brand + "\n")
	m.Text = t.String()
	return s.Mailer.Send(ctx, m)
}
