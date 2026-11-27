package pb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/taxdocs"
)

// Orders implements ordering.Store on PocketBase. Every write method is one
// RunTx; no method calls the network.
type Orders struct {
	App core.App
	// TaxDocs is the region document policy (nil: no documents) and
	// IssuerCode names the configured DocumentIssuer.
	TaxDocs    taxdocs.Policy
	IssuerCode string
}

var _ ordering.Store = (*Orders)(nil)

// Store errors.
var (
	ErrProviderID = errors.New("pb: payment already has a different provider id")
)

func pbDate(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000Z") }

func ms(t time.Time) int64 { return t.UnixMilli() }

func newRecord(app core.App, collection string, data map[string]any) (*core.Record, error) {
	col, err := app.FindCachedCollectionByNameOrId(collection)
	if err != nil {
		return nil, err
	}
	r := core.NewRecord(col)
	r.Load(data)
	if err := app.Save(r); err != nil {
		return nil, fmt.Errorf("%s: %w", collection, err)
	}
	return r, nil
}

// exec runs a conditional update and fails with ErrConflict when no row
// matched.
func exec(app core.App, q string, p dbx.Params) error {
	res, err := app.DB().NewQuery(q).Bind(p).Execute()
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrConflict
	}
	return nil
}

func count(app core.App, q string, p dbx.Params) (int, error) {
	n := 0
	err := app.DB().NewQuery(q).Bind(p).Row(&n)
	return n, err
}

func uuid4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// Storefront implements ordering.Store.
func (s *Orders) Storefront(ctx context.Context, code string) (ordering.Storefront, error) {
	r, err := s.App.FindFirstRecordByData("storefronts", "code", code)
	if err != nil {
		return ordering.Storefront{}, ordering.ErrNotFound
	}
	providers := map[string]string{}
	_ = r.UnmarshalJSONField("enabled_providers", &providers)
	return ordering.Storefront{
		ID: r.Id, Code: code, Region: r.GetString("region"), Currency: r.GetString("currency"),
		OrderPrefix: r.GetString("order_number_prefix"), Mode: r.GetString("commerce_mode"),
		Display: ordering.TaxDisplay(r.GetString("tax_display")), Providers: providers,
	}, nil
}

type offerRow struct {
	ID            string `db:"id"`
	Product       string `db:"product"`
	Seller        string `db:"seller"`
	LicenseType   string `db:"license_type"`
	PriceMinor    int64  `db:"price_minor"`
	Currency      string `db:"currency"`
	TaxCategory   string `db:"tax_category"`
	SupportMonths int    `db:"support_months"`
	Active        bool   `db:"active"`
	Slug          string `db:"slug"`
	Title         string `db:"title"`
	Status        string `db:"status"`
	SaleModel     string `db:"sale_model"`
	CommissionBPS int    `db:"commission_bps"`
	TaxProfile    string `db:"tax_profile"`
	Releases      int    `db:"releases"`
}

// Offers implements ordering.Store (read, before any write tx).
func (s *Orders) Offers(ctx context.Context, ids []string) ([]ordering.Offer, error) {
	if len(ids) == 0 || len(ids) > 20 {
		return nil, ordering.ErrNotFound
	}
	p := dbx.Params{}
	ph := make([]string, len(ids))
	for i, id := range ids {
		k := fmt.Sprintf("o%d", i)
		p[k], ph[i] = id, "{:"+k+"}"
	}
	var rows []offerRow
	err := s.App.DB().NewQuery(`SELECT o.id, o.product, o.seller, o.license_type, o.price_minor, o.currency, o.tax_category,
		o.support_months, o.active, p.slug, p.title, p.status, COALESCE(s.sale_model, '') AS sale_model, COALESCE(s.commission_bps, 0) AS commission_bps,
		COALESCE(s.tax_profile, '{}') AS tax_profile,
		(SELECT COUNT(*) FROM product_releases r WHERE r.product = o.product AND r.status = 'published' AND r.storage_key != '') AS releases
		FROM offers o JOIN products p ON p.id = o.product JOIN sellers s ON s.id = o.seller
		WHERE o.id IN (` + strings.Join(ph, ",") + `)`).Bind(p).All(&rows)
	if err != nil {
		return nil, err
	}
	byID := map[string]offerRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]ordering.Offer, 0, len(ids))
	for _, id := range ids {
		r, ok := byID[id]
		if !ok {
			return nil, ordering.ErrNotFound
		}
		var prof struct {
			VATPayer *bool `json:"vat_payer"`
		}
		_ = json.Unmarshal([]byte(r.TaxProfile), &prof)
		model := ordering.SaleModel(r.SaleModel)
		if model == "" {
			model = ordering.Principal
		}
		out = append(out, ordering.Offer{
			ID: r.ID, ProductID: r.Product, ProductSlug: r.Slug, ProductTitle: r.Title, SellerID: r.Seller,
			LicenseType: r.LicenseType, LicenseTitle: r.LicenseType, TaxCategory: r.TaxCategory,
			Price: money.Money{Minor: r.PriceMinor, Currency: r.Currency}, SupportMonths: r.SupportMonths,
			Active:           r.Active && r.Releases > 0, // nothing to deliver: not for sale
			ProductPublished: r.Status == "published", SaleModel: model,
			CommissionBPS: r.CommissionBPS, SellerTax: ordering.SellerTax{VATPayer: prof.VATPayer},
		})
	}
	return out, nil
}

type viewRow struct {
	ID         string         `db:"id"`
	Number     string         `db:"number"`
	Status     string         `db:"status"`
	Buyer      string         `db:"buyer"`
	Total      int64          `db:"total_minor"`
	Currency   string         `db:"currency"`
	PaymentID  sql.NullString `db:"pid"`
	PaymentKey sql.NullString `db:"pkey"`
	PStatus    sql.NullString `db:"pstatus"`
	Provider   sql.NullString `db:"provider"`
	Method     sql.NullString `db:"method"`
	PPID       sql.NullString `db:"ppid"`
	ConfURL    sql.NullString `db:"conf_url"`
}

func orderView(app core.App, orderID string) (ordering.OrderView, error) {
	var r viewRow
	err := app.DB().NewQuery(`SELECT o.id, o.number, o.status, o.buyer, o.total_minor, o.currency,
		p.id AS pid, p.idempotency_key AS pkey, p.status AS pstatus, p.provider, p.method,
		p.provider_payment_id AS ppid, p.confirmation_url AS conf_url
		FROM orders o LEFT JOIN payments p ON p."order" = o.id
		WHERE o.id = {:id} ORDER BY p.created DESC LIMIT 1`).Bind(dbx.Params{"id": orderID}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return ordering.OrderView{}, ordering.ErrNotFound
	}
	if err != nil {
		return ordering.OrderView{}, err
	}
	return ordering.OrderView{
		OrderID: r.ID, Number: r.Number, Status: ordering.OrderStatus(r.Status), BuyerID: r.Buyer,
		Total: money.Money{Minor: r.Total, Currency: r.Currency}, PaymentID: r.PaymentID.String,
		PaymentKey: r.PaymentKey.String, PaymentStatus: ordering.PaymentStatus(r.PStatus.String),
		Provider: r.Provider.String, Method: r.Method.String, ProviderPaymentID: r.PPID.String,
		ConfirmationURL: r.ConfURL.String,
	}, nil
}

// PlaceOrder implements ordering.Store: tx 1 of the checkout protocol.
func (s *Orders) PlaceOrder(ctx context.Context, o ordering.NewOrder) (ordering.OrderView, error) {
	var v ordering.OrderView
	err := RunTx(ctx, s.App, "checkout.place", func(ctx context.Context, app core.App) error {
		var existing string
		err := app.DB().NewQuery("SELECT id FROM orders WHERE idempotency_key = {:k}").Bind(dbx.Params{"k": o.OrderKey}).Row(&existing)
		if err == nil {
			v, err = orderView(app, existing)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if o.PaidImmediately {
			for _, l := range o.Lines {
				var already string
				err := app.DB().NewQuery(`SELECT o.id FROM entitlements e
					JOIN order_items i ON i.id = e.order_item
					JOIN orders o ON o.id = i."order"
					WHERE e.user = {:u} AND e.product = {:p} AND e.status = 'active'
					ORDER BY e.granted_at DESC LIMIT 1`).Bind(dbx.Params{"u": o.BuyerID, "p": l.Offer.ProductID}).Row(&already)
				if err == nil && already != "" {
					v, err = orderView(app, already)
					return err
				}
				if err != nil && !errors.Is(err, sql.ErrNoRows) {
					return err
				}
			}
		}
		year := o.Now.UTC().Format("2006")
		prefix := o.Storefront.OrderPrefix + "-" + year + "-"
		n, err := count(app, "SELECT COUNT(*) FROM orders WHERE number LIKE {:p}", dbx.Params{"p": prefix + "%"})
		if err != nil {
			return err
		}
		orderFields := map[string]any{
			"number": fmt.Sprintf("%s%06d", prefix, n+1), "buyer": o.BuyerID, "buyer_email": o.BuyerEmail,
			"status": string(ordering.OrderPending), "currency": o.Totals.Currency, "storefront": o.Storefront.ID,
			"region": o.Storefront.Region, "subtotal_minor": o.Totals.Subtotal, "discount_minor": o.Totals.Discount,
			"tax_minor": o.Totals.Tax, "total_minor": o.Totals.Sum, "tax_display": string(o.Storefront.Display),
			"idempotency_key": o.OrderKey, "terms_version": o.TermsVersion, "snapshot_digest": o.Digest, "status_version": 0,
		}
		if len(o.Referral) > 0 {
			orderFields["referral"] = o.Referral
		}
		if o.PaidImmediately {
			orderFields["status"] = string(ordering.OrderPaid)
			orderFields["paid_at"] = pbDate(o.Now)
		}
		ord, err := newRecord(app, "orders", orderFields)
		if err != nil {
			return err
		}
		for _, l := range o.Lines {
			if _, err := newRecord(app, "order_items", map[string]any{
				"order": ord.Id, "seller": l.Offer.SellerID, "product": l.Offer.ProductID, "offer": l.Offer.ID,
				"license_type": l.Offer.LicenseType, "title_snapshot": l.Offer.ProductTitle, "product_slug": l.Offer.ProductSlug,
				"license_title": l.Offer.LicenseTitle, "unit_price_minor": l.UnitPrice, "unit_discount_minor": l.UnitDiscount,
				"qty": l.Qty, "line_minor": l.LineMinor, "tax_category": l.Offer.TaxCategory, "tax_rate_bp": l.Tax.RateBP,
				"tax_mode": string(l.Tax.Mode), "tax_code": l.Tax.Code, "tax_minor": l.TaxMinor, "sale_model": string(l.Offer.SaleModel),
				"support_months": l.Offer.SupportMonths, "license_terms_version": l.Offer.LicenseTermsVersion,
				"currency": o.Totals.Currency, "commission_minor": l.Commission,
			}); err != nil {
				return err
			}
		}
		payFields := map[string]any{
			"order": ord.Id, "seller": o.SellerID, "provider": o.Provider, "method": o.Method,
			"status": string(ordering.PaymentCreated), "amount_minor": o.Totals.Sum, "currency": o.Totals.Currency,
			"idempotency_key": o.PaymentKey, "attempt": 1, "legal_entity": o.LegalEntity,
			"status_version": 0, "next_reconcile_at": ms(o.Now.Add(10 * time.Minute)),
		}
		if o.PaidImmediately {
			payFields["status"] = string(ordering.PaymentSucceeded)
			// unique per order: (provider, provider_payment_id) is a
			// unique index, a constant would allow one free order per shop
			payFields["provider_payment_id"] = "free:" + ord.Id
			payFields["next_reconcile_at"] = 0
		}
		if _, err := newRecord(app, "payments", payFields); err != nil {
			return err
		}
		for _, c := range o.Consents {
			if _, err := newRecord(app, "consents", map[string]any{
				"user": o.BuyerID, "document": c.Document, "version": c.Version,
				"accepted_at": pbDate(o.Now), "ip_hash": o.IPHash, "order_key": o.OrderKey,
			}); err != nil {
				return err
			}
		}
		if o.PaidImmediately {
			if _, err := deliveryEffects(app, ord.Id, o.Now); err != nil {
				return err
			}
		}
		v, err = orderView(app, ord.Id)
		return err
	})
	return v, err
}

// StartNextAttempt implements ordering.Store (ADR-0033).
func (s *Orders) StartNextAttempt(ctx context.Context, orderID, provider, method, sellerID, legalEntity string, now time.Time) (ordering.OrderView, error) {
	var v ordering.OrderView
	err := RunTx(ctx, s.App, "checkout.retry", func(ctx context.Context, app core.App) error {
		cur, err := orderView(app, orderID)
		if err != nil {
			return err
		}
		if !ordering.CanRetryAttempt(cur.Status, cur.PaymentStatus) {
			v = cur
			return nil
		}
		to, err := ordering.ReopenAfterFailedAttempt(cur.Status)
		if err != nil {
			return err
		}
		var n int
		var base string
		var amount int64
		var currency, seller string
		err = app.DB().NewQuery(`SELECT COUNT(*),
			(SELECT idempotency_key FROM payments WHERE "order" = {:o} ORDER BY created ASC LIMIT 1),
			(SELECT amount_minor FROM payments WHERE "order" = {:o} ORDER BY created DESC LIMIT 1),
			(SELECT currency FROM payments WHERE "order" = {:o} ORDER BY created DESC LIMIT 1),
			(SELECT seller FROM payments WHERE "order" = {:o} ORDER BY created DESC LIMIT 1)
			FROM payments WHERE "order" = {:o}`).Bind(dbx.Params{"o": orderID}).Row(&n, &base, &amount, &currency, &seller)
		if err != nil {
			return err
		}
		attempt := n + 1
		key := base
		if attempt > 1 {
			key = fmt.Sprintf("%s:%d", base, attempt)
		}
		if sellerID == "" {
			sellerID = seller
		}
		if cur.Status != to {
			if err := exec(app, `UPDATE orders SET status = {:to}, status_version = status_version + 1, updated = {:now},
				cancelled_at = '', cancel_reason = ''
				WHERE id = {:id} AND status = {:from}`,
				dbx.Params{"to": string(to), "now": pbDate(now), "id": orderID, "from": string(cur.Status)}); err != nil {
				return err
			}
		}
		if _, err := newRecord(app, "payments", map[string]any{
			"order": orderID, "seller": sellerID, "provider": provider, "method": method,
			"status": string(ordering.PaymentCreated), "amount_minor": amount, "currency": currency,
			"idempotency_key": key, "attempt": attempt, "legal_entity": legalEntity,
			"status_version": 0, "next_reconcile_at": ms(now.Add(10 * time.Minute)),
		}); err != nil {
			return err
		}
		v, err = orderView(app, orderID)
		return err
	})
	return v, err
}

type payRow struct {
	ID       string `db:"id"`
	Order    string `db:"order"`
	Status   string `db:"status"`
	Version  int    `db:"status_version"`
	PPID     string `db:"provider_payment_id"`
	Amount   int64  `db:"amount_minor"`
	Currency string `db:"currency"`
	OStatus  string `db:"ostatus"`
	OVersion int    `db:"oversion"`
}

const paySelect = `SELECT p.id, p."order" AS "order", p.status, p.status_version, p.provider_payment_id,
	p.amount_minor, p.currency, o.status AS ostatus, o.status_version AS oversion
	FROM payments p JOIN orders o ON o.id = p."order" `

// AttachProvider implements ordering.Store: tx 2 of the checkout protocol.
func (s *Orders) AttachProvider(ctx context.Context, paymentID, ppid, url string) (ordering.OrderView, error) {
	var v ordering.OrderView
	err := RunTx(ctx, s.App, "checkout.attach", func(ctx context.Context, app core.App) error {
		var r payRow
		if err := app.DB().NewQuery(paySelect + "WHERE p.id = {:id}").Bind(dbx.Params{"id": paymentID}).One(&r); err != nil {
			return err
		}
		if r.PPID != "" && r.PPID != ppid {
			return ErrProviderID
		}
		now := time.Now()
		if r.Status == string(ordering.PaymentCreated) {
			if err := exec(app, `UPDATE payments SET status = 'pending', provider_payment_id = {:ppid}, confirmation_url = {:url},
				status_version = status_version + 1, updated = {:now}
				WHERE id = {:id} AND status = 'created' AND status_version = {:v}`,
				dbx.Params{"ppid": ppid, "url": url, "now": pbDate(now), "id": r.ID, "v": r.Version}); err != nil {
				return err
			}
			if _, err := moveOrder(app, r.Order, ordering.PaymentPending, now); err != nil {
				return err
			}
		}
		var err error
		v, err = orderView(app, r.Order)
		return err
	})
	return v, err
}

// OrderForBuyer implements ordering.Store.
func (s *Orders) OrderForBuyer(ctx context.Context, orderID, buyerID string) (ordering.OrderView, error) {
	v, err := orderView(s.App, orderID)
	if err != nil {
		return v, err
	}
	if v.BuyerID != buyerID {
		return ordering.OrderView{}, ordering.ErrNotFound
	}
	return v, nil
}

// Order implements ordering.Store: an order by id without an owner check
// (the public return page shows only its status).
func (s *Orders) Order(ctx context.Context, orderID string) (ordering.OrderView, error) {
	return orderView(s.App, orderID)
}

// AcceptEvent implements ordering.Store: one short insert into the inbox.
func (s *Orders) AcceptEvent(ctx context.Context, provider string, ev payments.Event, now time.Time) (bool, error) {
	dup := false
	err := RunTx(ctx, s.App, "inbox.accept", func(ctx context.Context, app core.App) error {
		n, err := count(app, "SELECT COUNT(*) FROM payment_events WHERE provider = {:p} AND event_id = {:e}", dbx.Params{"p": provider, "e": ev.EventID})
		if err != nil || n > 0 {
			dup = n > 0
			return err
		}
		payload := json.RawMessage(ev.Payload)
		if !json.Valid(payload) {
			b, _ := json.Marshal(string(ev.Payload))
			payload = b
		}
		_, err = newRecord(app, "payment_events", map[string]any{
			"provider": provider, "event_id": ev.EventID, "type": ev.Type, "provider_payment_id": ev.ProviderPaymentID,
			"payload": payload, "verified": true, "status": "received", "attempts": 0, "locked_until": 0, "received_at": pbDate(now),
		})
		return err
	})
	return dup, err
}

// ClaimEvent implements ordering.Store: received (or expired processing)
// -> processing with a lease, in its own short tx.
func (s *Orders) ClaimEvent(ctx context.Context, now time.Time, lease time.Duration) (ordering.InboxItem, bool, error) {
	var it ordering.InboxItem
	found := false
	err := RunTx(ctx, s.App, "inbox.claim", func(ctx context.Context, app core.App) error {
		var r struct {
			ID       string `db:"id"`
			Provider string `db:"provider"`
			PPID     string `db:"provider_payment_id"`
			Attempts int    `db:"attempts"`
		}
		p := dbx.Params{"now": ms(now)}
		err := app.DB().NewQuery(`SELECT id, provider, provider_payment_id, attempts FROM payment_events
			WHERE status = 'received' OR (status = 'processing' AND locked_until < {:now})
			ORDER BY created LIMIT 1`).Bind(p).One(&r)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		p["id"], p["until"] = r.ID, ms(now.Add(lease))
		if err := exec(app, `UPDATE payment_events SET status = 'processing', locked_until = {:until}, attempts = attempts + 1
			WHERE id = {:id} AND (status = 'received' OR (status = 'processing' AND locked_until < {:now}))`, p); err != nil {
			return err
		}
		it, found = ordering.InboxItem{ID: r.ID, Provider: r.Provider, ProviderPaymentID: r.PPID, Attempts: r.Attempts + 1}, true
		return nil
	})
	return it, found, err
}

// FinishEvent implements ordering.Store.
func (s *Orders) FinishEvent(ctx context.Context, id string, procErr error, now time.Time, maxAttempts int) error {
	return RunTx(ctx, s.App, "inbox.finish", func(ctx context.Context, app core.App) error {
		p := dbx.Params{"id": id, "now": pbDate(now), "max": maxAttempts, "err": ""}
		if procErr == nil {
			return exec(app, `UPDATE payment_events SET status = 'processed', processed_at = {:now}, error = '', locked_until = 0
				WHERE id = {:id} AND status = 'processing'`, p)
		}
		p["err"] = truncate(procErr.Error(), 500)
		return exec(app, `UPDATE payment_events SET status = CASE WHEN attempts >= {:max} THEN 'dead' ELSE 'received' END,
			error = {:err}, locked_until = 0 WHERE id = {:id} AND status = 'processing'`, p)
	})
}

// ClaimOutbox implements ordering.Store.
func (s *Orders) ClaimOutbox(ctx context.Context, now time.Time, lease time.Duration) (ordering.OutboxItem, bool, error) {
	var it ordering.OutboxItem
	found := false
	err := RunTx(ctx, s.App, "outbox.claim", func(ctx context.Context, app core.App) error {
		var r struct {
			ID       string `db:"id"`
			Topic    string `db:"topic"`
			Key      string `db:"idempotency_key"`
			Payload  string `db:"payload"`
			Attempts int    `db:"attempts"`
		}
		p := dbx.Params{"now": ms(now)}
		err := app.DB().NewQuery(`SELECT id, topic, idempotency_key, payload, attempts FROM outbox
			WHERE (status = 'pending' AND next_attempt_at <= {:now}) OR (status = 'processing' AND locked_until < {:now})
			ORDER BY next_attempt_at LIMIT 1`).Bind(p).One(&r)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		p["id"], p["until"] = r.ID, ms(now.Add(lease))
		if err := exec(app, `UPDATE outbox SET status = 'processing', locked_until = {:until}, attempts = attempts + 1
			WHERE id = {:id} AND ((status = 'pending' AND next_attempt_at <= {:now}) OR (status = 'processing' AND locked_until < {:now}))`, p); err != nil {
			return err
		}
		it, found = ordering.OutboxItem{ID: r.ID, Topic: r.Topic, Key: r.Key, Payload: json.RawMessage(r.Payload), Attempts: r.Attempts + 1}, true
		return nil
	})
	return it, found, err
}

// FinishOutbox implements ordering.Store.
func (s *Orders) FinishOutbox(ctx context.Context, id string, procErr error, now time.Time, maxAttempts int, backoff time.Duration) error {
	return RunTx(ctx, s.App, "outbox.finish", func(ctx context.Context, app core.App) error {
		p := dbx.Params{"id": id, "max": maxAttempts}
		if procErr == nil {
			return exec(app, `UPDATE outbox SET status = 'done', last_error = '', locked_until = 0 WHERE id = {:id} AND status = 'processing'`, p)
		}
		p["err"], p["next"] = truncate(procErr.Error(), 500), ms(now.Add(backoff))
		return exec(app, `UPDATE outbox SET status = CASE WHEN attempts >= {:max} THEN 'dead' ELSE 'pending' END,
			last_error = {:err}, locked_until = 0, next_attempt_at = {:next} WHERE id = {:id} AND status = 'processing'`, p)
	})
}

// ReconcileCandidates implements ordering.Store (read only).
func (s *Orders) ReconcileCandidates(ctx context.Context, now time.Time, olderThan time.Duration) ([]ordering.Candidate, error) {
	var rows []struct {
		Kind     string `db:"kind"`
		Order    string `db:"order_id"`
		Payment  string `db:"payment_id"`
		Key      string `db:"pkey"`
		Provider string `db:"provider"`
		Method   string `db:"method"`
		PPID     string `db:"ppid"`
		Amount   int64  `db:"amount_minor"`
		Currency string `db:"currency"`
	}
	p := dbx.Params{"before": pbDate(now.Add(-olderThan)), "recent": pbDate(now.Add(-7 * 24 * time.Hour))}
	err := s.App.DB().NewQuery(`
		SELECT CASE WHEN p.provider_payment_id = '' THEN 'attach' ELSE 'fetch' END AS kind,
		       p."order" AS order_id, p.id AS payment_id, p.idempotency_key AS pkey, p.provider, p.method,
		       p.provider_payment_id AS ppid, p.amount_minor, p.currency
		FROM payments p WHERE p.status IN ('created', 'pending') AND p.created < {:before}
		UNION ALL
		SELECT 'fetch', p."order", p.id, p.idempotency_key, p.provider, p.method, p.provider_payment_id, p.amount_minor, p.currency
		FROM payments p JOIN orders o ON o.id = p."order"
		WHERE p.status IN ('canceled', 'failed') AND p.provider_payment_id != '' AND o.status = 'cancelled' AND p.updated > {:recent}
		UNION ALL
		SELECT 'replay', o.id, p.id, '', p.provider, p.method, p.provider_payment_id, p.amount_minor, p.currency
		FROM orders o JOIN payments p ON p."order" = o.id AND p.status = 'succeeded'
		WHERE o.status IN ('paid', 'refund_pending') AND (
		  NOT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.txn_id = 'pay:' || p.id || ':succeeded')
		  OR (o.status = 'paid' AND EXISTS (SELECT 1 FROM order_items i WHERE i."order" = o.id AND NOT EXISTS (SELECT 1 FROM entitlements e WHERE e.order_item = i.id))))
		LIMIT 500`).Bind(p).All(&rows)
	if err != nil {
		return nil, err
	}
	out := make([]ordering.Candidate, len(rows))
	for i, r := range rows {
		out[i] = ordering.Candidate{Kind: r.Kind, OrderID: r.Order, PaymentID: r.Payment, PaymentKey: r.Key,
			Provider: r.Provider, Method: r.Method, ProviderPaymentID: r.PPID, Amount: money.Money{Minor: r.Amount, Currency: r.Currency}}
	}
	// refunds still pending at the provider, documents pending at an
	// async register (the first attempts belong to the outbox)
	var extra []struct {
		Kind  string `db:"kind"`
		ID    string `db:"id"`
		Order string `db:"order_id"`
	}
	err = s.App.DB().NewQuery(`
		SELECT 'refund' AS kind, r.id, r."order" AS order_id FROM refunds r WHERE r.status = 'pending' AND r.updated < {:before}
		UNION ALL
		SELECT 'taxdoc', d.id, d."order" FROM tax_documents d WHERE d.status = 'pending' AND d.external_id != '' AND d.updated < {:before}
		LIMIT 500`).Bind(p).All(&extra)
	for _, e := range extra {
		c := ordering.Candidate{Kind: e.Kind, OrderID: e.Order}
		if e.Kind == ordering.ReconcileRefund {
			c.RefundID = e.ID
		} else {
			c.DocID = e.ID
		}
		out = append(out, c)
	}
	return out, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
