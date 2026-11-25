package pb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/internal/taxdocs"
)

// This file is the apply side of the money path: one transaction per
// provider answer, with every in-tx effect (ADR-0007 "Effects").

func loadPay(app core.App, where string, p dbx.Params) (payRow, error) {
	var r payRow
	err := app.DB().NewQuery(paySelect + where).Bind(p).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ordering.ErrNotFound
	}
	return r, err
}

func orderStatus(app core.App, orderID string) (ordering.OrderStatus, error) {
	var s string
	err := app.DB().NewQuery("SELECT status FROM orders WHERE id = {:id}").Bind(dbx.Params{"id": orderID}).Row(&s)
	return ordering.OrderStatus(s), err
}

// moveOrder applies the order side of a payment status (conditional on the
// current status_version, read in the same tx).
func moveOrder(app core.App, orderID string, p ordering.PaymentStatus, now time.Time) (ordering.OrderStatus, error) {
	var o struct {
		Status string `db:"status"`
		V      int    `db:"status_version"`
	}
	if err := app.DB().NewQuery("SELECT status, status_version FROM orders WHERE id = {:id}").Bind(dbx.Params{"id": orderID}).One(&o); err != nil {
		return "", err
	}
	to, changed, err := ordering.OrderOnPayment(ordering.OrderStatus(o.Status), p)
	if err != nil || !changed {
		return to, err
	}
	return to, setOrder(app, orderID, o.Status, o.V, to, now)
}

func setOrder(app core.App, orderID, from string, version int, to ordering.OrderStatus, now time.Time) error {
	set := ""
	switch to {
	case ordering.OrderPaid:
		set = ", paid_at = {:now}"
	case ordering.OrderCancelled:
		set = ", cancelled_at = {:now}, cancel_reason = CASE WHEN cancel_reason = 'timeout_requested' THEN cancel_reason ELSE 'provider_canceled' END"
	}
	return exec(app, `UPDATE orders SET status = {:to}, status_version = status_version + 1, updated = {:now}`+set+
		` WHERE id = {:id} AND status = {:from} AND status_version = {:v}`,
		dbx.Params{"id": orderID, "from": from, "v": version, "to": string(to), "now": pbDate(now)})
}

func setPayment(app core.App, paymentID string, to ordering.PaymentStatus, now time.Time) (bool, error) {
	var p struct {
		Status string `db:"status"`
		V      int    `db:"status_version"`
	}
	if err := app.DB().NewQuery("SELECT status, status_version FROM payments WHERE id = {:id}").Bind(dbx.Params{"id": paymentID}).One(&p); err != nil {
		return false, err
	}
	changed, err := ordering.PaymentTransition(ordering.PaymentStatus(p.Status), to)
	if err != nil || !changed {
		return false, err
	}
	return true, exec(app, `UPDATE payments SET status = {:to}, status_version = status_version + 1, updated = {:now}
		WHERE id = {:id} AND status = {:from} AND status_version = {:v}`,
		dbx.Params{"to": string(to), "now": pbDate(now), "id": paymentID, "from": p.Status, "v": p.V})
}

func setEntitlements(app core.App, orderID, to string, from []string, now time.Time) error {
	p := dbx.Params{"o": orderID, "to": to, "now": pbDate(now)}
	in := ""
	for i, f := range from {
		k := fmt.Sprintf("f%d", i)
		p[k] = f
		if in != "" {
			in += ","
		}
		in += "{:" + k + "}"
	}
	_, err := app.DB().NewQuery(`UPDATE entitlements SET status = {:to}, updated = {:now}
		WHERE order_item IN (SELECT id FROM order_items WHERE "order" = {:o}) AND status IN (` + in + `)`).Bind(p).Execute()
	return err
}

func alert(app core.App, kind, refType, refID, msg string) error {
	n, err := count(app, "SELECT COUNT(*) FROM alerts WHERE kind = {:k} AND ref_type = {:t} AND ref_id = {:id}", dbx.Params{"k": kind, "t": refType, "id": refID})
	if err != nil || n > 0 {
		return err
	}
	app.Logger().Warn("alert", "kind", kind, "ref_type", refType, "ref_id", refID, "message", msg)
	_, err = newRecord(app, "alerts", map[string]any{"kind": kind, "ref_type": refType, "ref_id": refID, "message": truncate(msg, 500)})
	return err
}

func outbox(app core.App, topic, key string, payload any, now time.Time) error {
	n, err := count(app, "SELECT COUNT(*) FROM outbox WHERE idempotency_key = {:k}", dbx.Params{"k": key})
	if err != nil || n > 0 {
		return err
	}
	_, err = newRecord(app, "outbox", map[string]any{"topic": topic, "idempotency_key": key, "payload": payload,
		"status": "pending", "attempts": 0, "next_attempt_at": ms(now)})
	return err
}

// ApplyPayment implements ordering.Store: the provider status (from
// FetchPayment or CancelPayment) moves the payment and the order and writes
// the effects in the same transaction.
func (s *Orders) ApplyPayment(ctx context.Context, provider string, st payments.Status, now time.Time) (ordering.Applied, error) {
	var a ordering.Applied
	err := RunTx(ctx, s.App, "payment.apply", func(ctx context.Context, app core.App) error {
		r, err := loadPay(app, "WHERE p.provider = {:pr} AND p.provider_payment_id = {:id}", dbx.Params{"pr": provider, "id": st.ProviderPaymentID})
		if err != nil {
			return err
		}
		prev := ordering.PaymentStatus(r.Status)
		to := ordering.PaymentStatus(st.State)
		a = ordering.Applied{OrderID: r.Order, Payment: prev, Order: ordering.OrderStatus(r.OStatus)}
		changed, err := setPayment(app, r.ID, to, now)
		if err != nil {
			return err
		}
		if changed {
			a.Payment, a.Changed = to, true
		}
		n := 0
		switch a.Payment {
		case ordering.PaymentSucceeded:
			n, err = s.onSucceeded(app, r, st, now)
		case ordering.PaymentDisputed:
			if changed {
				err = s.onDisputed(app, r, now)
			}
		case ordering.PaymentRefunded:
			if changed {
				err = s.onRefunded(app, r, prev, now)
			}
		case ordering.PaymentCanceled, ordering.PaymentFailed:
			_, err = moveOrder(app, r.Order, a.Payment, now)
		case ordering.PaymentPending:
			_, err = moveOrder(app, r.Order, a.Payment, now)
		}
		if err != nil {
			return err
		}
		a.EffectsWritten = n
		o, err := orderStatus(app, r.Order)
		if o != a.Order {
			a.Order, a.Changed = o, true
		}
		return err
	})
	return a, err
}

// onSucceeded: money arrived. Sale effects (ledger, receipt) always; the
// order becomes paid with delivery (entitlements, email) when it can be
// completed, otherwise refund_pending with an automatic full refund.
func (s *Orders) onSucceeded(app core.App, r payRow, st payments.Status, now time.Time) (int, error) {
	o, err := orderStatus(app, r.Order)
	if err != nil {
		return 0, err
	}
	switch o {
	case ordering.OrderPending, ordering.OrderAwaitingPayment, ordering.OrderCancelled:
		amountOK := st.Amount.Currency == "" || (st.Amount.Minor == r.Amount && st.Amount.Currency == r.Currency)
		ps, err := productStates(app, r.Order)
		if err != nil {
			return 0, err
		}
		if ok, why := ordering.Completable(amountOK, ps); !ok {
			var v int
			if err := app.DB().NewQuery("SELECT status_version FROM orders WHERE id = {:id}").Bind(dbx.Params{"id": r.Order}).Row(&v); err != nil {
				return 0, err
			}
			if err := setOrder(app, r.Order, string(o), v, ordering.OrderRefundPending, now); err != nil {
				return 0, err
			}
			amount := r.Amount
			if !amountOK && st.Amount.Currency == r.Currency {
				amount = st.Amount.Minor
			}
			if _, err := requestRefundTx(app, r, ordering.ReasonNotCompletable, why, "system", amount, now); err != nil {
				return 0, err
			}
			if err := alert(app, "not_completable", "order", r.Order, why); err != nil {
				return 0, err
			}
			return s.saleEffects(app, r, now)
		}
		if _, err := moveOrder(app, r.Order, ordering.PaymentSucceeded, now); err != nil {
			return 0, err
		}
	case ordering.OrderDisputed: // dispute won
		if _, err := moveOrder(app, r.Order, ordering.PaymentSucceeded, now); err != nil {
			return 0, err
		}
		if err := setEntitlements(app, r.Order, ordering.EntitlementActive, []string{ordering.EntitlementDisputed}, now); err != nil {
			return 0, err
		}
	}
	n, err := s.saleEffects(app, r, now)
	if err != nil {
		return n, err
	}
	if o, err = orderStatus(app, r.Order); err != nil || o != ordering.OrderPaid {
		return n, err
	}
	m, err := deliveryEffects(app, r.Order, now)
	return n + m, err
}

func (s *Orders) onDisputed(app core.App, r payRow, now time.Time) error {
	if _, err := moveOrder(app, r.Order, ordering.PaymentDisputed, now); err != nil {
		return err
	}
	if err := setEntitlements(app, r.Order, ordering.EntitlementDisputed, []string{ordering.EntitlementActive}, now); err != nil {
		return err
	}
	return alert(app, "dispute_opened", "payment", r.ID, "chargeback opened; downloads suspended")
}

// onRefunded: the provider reports the money returned. A pending refund
// row is confirmed; from disputed without a refund it is a lost dispute;
// otherwise a refund made outside the engine is recorded.
func (s *Orders) onRefunded(app core.App, r payRow, prev ordering.PaymentStatus, now time.Time) error {
	var refID string
	err := app.DB().NewQuery("SELECT id FROM refunds WHERE payment = {:p} AND status = 'pending'").Bind(dbx.Params{"p": r.ID}).Row(&refID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if refID != "" {
		return s.completeRefund(app, r, refID, "", now)
	}
	reason := ordering.ReasonProviderInitiated
	if prev == ordering.PaymentDisputed {
		reason = ordering.ReasonDisputeLost
	}
	ref, err := insertRefund(app, r, reason, "reported by the provider", "provider", r.Amount, "succeeded", now)
	if err != nil {
		return err
	}
	return s.completeRefund(app, r, ref, "", now)
}

// completeRefund writes the refund effects: refund succeeded, payment and
// order refunded, entitlements refunded (revoked for a lost dispute),
// ledger reversal of the sale txn, return tax document.
func (s *Orders) completeRefund(app core.App, r payRow, refundID, providerRefundID string, now time.Time) error {
	var ref struct {
		Reason string `db:"reason_code"`
	}
	if err := app.DB().NewQuery("SELECT reason_code FROM refunds WHERE id = {:id}").Bind(dbx.Params{"id": refundID}).One(&ref); err != nil {
		return err
	}
	if _, err := app.DB().NewQuery(`UPDATE refunds SET status = 'succeeded', provider_refund_id = CASE WHEN {:prid} != '' THEN {:prid} ELSE provider_refund_id END,
		updated = {:now} WHERE id = {:id} AND status IN ('pending', 'succeeded')`).
		Bind(dbx.Params{"id": refundID, "prid": providerRefundID, "now": pbDate(now)}).Execute(); err != nil {
		return err
	}
	if _, err := setPayment(app, r.ID, ordering.PaymentRefunded, now); err != nil {
		return err
	}
	if _, err := moveOrder(app, r.Order, ordering.PaymentRefunded, now); err != nil {
		return err
	}
	entTo, txn := ordering.EntitlementRefunded, ordering.RefundTxnID(r.ID, refundID)
	if ref.Reason == ordering.ReasonDisputeLost {
		n, err := count(app, "SELECT COUNT(DISTINCT txn_id) FROM ledger_entries WHERE ref_type = 'dispute' AND txn_id LIKE {:p}", dbx.Params{"p": "pay:" + r.ID + ":dispute:%"})
		if err != nil {
			return err
		}
		entTo, txn = ordering.EntitlementRevoked, ordering.DisputeTxnID(r.ID, n+1)
	}
	if err := setEntitlements(app, r.Order, entTo, []string{ordering.EntitlementActive, ordering.EntitlementDisputed}, now); err != nil {
		return err
	}
	if err := reverseSale(app, r.ID, txn, refundID, ref.Reason == ordering.ReasonDisputeLost); err != nil {
		return err
	}
	if s.TaxDocs != nil {
		if kind, op, ok := s.TaxDocs.ForRefund(); ok {
			return s.insertTaxDoc(app, kind, op, r, refundID, now)
		}
	}
	return nil
}

func reverseSale(app core.App, paymentID, txn, refundID string, dispute bool) error {
	n, err := count(app, "SELECT COUNT(*) FROM ledger_entries WHERE txn_id = {:t}", dbx.Params{"t": txn})
	if err != nil || n > 0 {
		return err
	}
	var rows []struct {
		Account  string `db:"account"`
		Seller   string `db:"seller"`
		Amount   int64  `db:"amount_minor"`
		Currency string `db:"currency"`
	}
	if err := app.DB().NewQuery("SELECT account, seller, amount_minor, currency FROM ledger_entries WHERE txn_id = {:t}").
		Bind(dbx.Params{"t": ordering.PaymentTxnID(paymentID)}).All(&rows); err != nil {
		return err
	}
	sale := make([]ordering.Entry, len(rows))
	for i, e := range rows {
		sale[i] = ordering.Entry{Account: e.Account, Seller: e.Seller, Amount: e.Amount, Currency: e.Currency}
	}
	refType := "refund"
	if dispute {
		refType = "dispute"
	}
	for _, e := range ordering.Reverse(sale, txn, refType, refundID) {
		if _, err := newRecord(app, "ledger_entries", map[string]any{"txn_id": e.TxnID, "account": e.Account, "seller": e.Seller,
			"amount_minor": e.Amount, "currency": e.Currency, "ref_type": e.RefType, "ref_id": e.RefID}); err != nil {
			return err
		}
	}
	return nil
}

func productStates(app core.App, orderID string) ([]ordering.ProductState, error) {
	var rows []struct {
		Status string `db:"status"`
		Rel    int    `db:"rel"`
	}
	err := app.DB().NewQuery(`SELECT COALESCE(p.status, 'missing') AS status,
		(SELECT COUNT(*) FROM product_releases r WHERE r.product = i.product AND r.status = 'published' AND r.storage_key != '') AS rel
		FROM order_items i LEFT JOIN products p ON p.id = i.product WHERE i."order" = {:o}`).Bind(dbx.Params{"o": orderID}).All(&rows)
	out := make([]ordering.ProductState, len(rows))
	for i, r := range rows {
		out[i] = ordering.ProductState{Status: r.Status, HasPublishedRel: r.Rel > 0}
	}
	return out, err
}

type itemRow struct {
	ID            string `db:"id"`
	Seller        string `db:"seller"`
	Product       string `db:"product"`
	LicenseType   string `db:"license_type"`
	Title         string `db:"title_snapshot"`
	Qty           int    `db:"qty"`
	UnitPrice     int64  `db:"unit_price_minor"`
	LineMinor     int64  `db:"line_minor"`
	TaxMinor      int64  `db:"tax_minor"`
	TaxMode       string `db:"tax_mode"`
	TaxCode       string `db:"tax_code"`
	SaleModel     string `db:"sale_model"`
	Commission    int64  `db:"commission_minor"`
	SupportMonths int    `db:"support_months"`
}

type orderRow struct {
	Buyer    string `db:"buyer"`
	Email    string `db:"buyer_email"`
	Currency string `db:"currency"`
	Number   string `db:"number"`
	Region   string `db:"region"`
	Total    int64  `db:"total_minor"`
}

func loadOrder(app core.App, orderID string) (orderRow, []itemRow, error) {
	var o orderRow
	if err := app.DB().NewQuery("SELECT buyer, buyer_email, currency, number, region, total_minor FROM orders WHERE id = {:id}").Bind(dbx.Params{"id": orderID}).One(&o); err != nil {
		return o, nil, err
	}
	var items []itemRow
	err := app.DB().NewQuery(`SELECT id, seller, product, license_type, title_snapshot, qty, unit_price_minor, line_minor, tax_minor,
		tax_mode, tax_code, sale_model, commission_minor, support_months FROM order_items WHERE "order" = {:id} ORDER BY id`).Bind(dbx.Params{"id": orderID}).All(&items)
	return o, items, err
}

// saleEffects: the payment's ledger txn and its sale tax document; each
// guarded by its unique key, so a replay is a no-op.
func (s *Orders) saleEffects(app core.App, r payRow, now time.Time) (int, error) {
	ord, items, err := loadOrder(app, r.Order)
	if err != nil {
		return 0, err
	}
	written := 0
	txn := ordering.PaymentTxnID(r.ID)
	n, err := count(app, "SELECT COUNT(*) FROM ledger_entries WHERE txn_id = {:t}", dbx.Params{"t": txn})
	if err != nil {
		return 0, err
	}
	if n == 0 {
		lines := make([]ordering.Line, len(items))
		for i, it := range items {
			lines[i] = ordering.Line{LineMinor: it.LineMinor, TaxMinor: it.TaxMinor, Commission: it.Commission,
				Tax:   ordering.TaxResolution{Mode: ordering.TaxMode(it.TaxMode)},
				Offer: ordering.Offer{SellerID: it.Seller, SaleModel: ordering.SaleModel(it.SaleModel)}}
		}
		entries := ordering.PostPayment(r.ID, ord.Currency, lines, nil)
		if !ordering.Balanced(entries) {
			return 0, fmt.Errorf("pb: unbalanced ledger txn %s", txn)
		}
		for _, e := range entries {
			if _, err := newRecord(app, "ledger_entries", map[string]any{"txn_id": e.TxnID, "account": e.Account, "seller": e.Seller,
				"amount_minor": e.Amount, "currency": e.Currency, "ref_type": e.RefType, "ref_id": e.RefID}); err != nil {
				return written, err
			}
			written++
		}
	}
	if s.TaxDocs != nil {
		if kind, op, ok := s.TaxDocs.ForSale(); ok {
			before, _ := count(app, "SELECT COUNT(*) FROM tax_documents WHERE payment = {:p}", dbx.Params{"p": r.ID})
			if err := s.insertTaxDoc(app, kind, op, r, "", now); err != nil {
				return written, err
			}
			after, _ := count(app, "SELECT COUNT(*) FROM tax_documents WHERE payment = {:p}", dbx.Params{"p": r.ID})
			written += after - before
		}
	}
	return written, nil
}

// deliveryEffects: entitlements and the purchase email (paid orders only).
func deliveryEffects(app core.App, orderID string, now time.Time) (int, error) {
	ord, items, err := loadOrder(app, orderID)
	if err != nil {
		return 0, err
	}
	written := 0
	for _, it := range items {
		n, err := count(app, "SELECT COUNT(*) FROM entitlements WHERE order_item = {:id}", dbx.Params{"id": it.ID})
		if err != nil {
			return written, err
		}
		if n > 0 {
			continue
		}
		data := map[string]any{
			"user": ord.Buyer, "seller": it.Seller, "order_item": it.ID, "product": it.Product,
			"license_type": it.LicenseType, "purchase_code": uuid4(), "status": ordering.EntitlementActive,
			"support_months_snapshot": it.SupportMonths, "granted_at": pbDate(now),
		}
		if it.SupportMonths > 0 {
			data["support_until"] = pbDate(now.AddDate(0, it.SupportMonths, 0))
		}
		if _, err := newRecord(app, "entitlements", data); err != nil {
			return written, err
		}
		written++
	}
	before, _ := count(app, "SELECT COUNT(*) FROM outbox", nil)
	if err := outbox(app, "email.purchase", "email:order:"+orderID+":paid",
		map[string]any{"order_id": orderID, "number": ord.Number, "email": ord.Email}, now); err != nil {
		return written, err
	}
	after, _ := count(app, "SELECT COUNT(*) FROM outbox", nil)
	return written + after - before, nil
}

// insertTaxDoc writes a pending document (sale when refundID is empty) and
// its outbox issue command.
func (s *Orders) insertTaxDoc(app core.App, kind taxdocs.Kind, op string, r payRow, refundID string, now time.Time) error {
	q, key := "SELECT COUNT(*) FROM tax_documents WHERE payment = {:p} AND kind = {:k} AND refund = ''", "taxdoc:payment:"+r.ID
	if refundID != "" {
		q, key = "SELECT COUNT(*) FROM tax_documents WHERE refund = {:r} AND kind = {:k}", "taxdoc:refund:"+refundID
	}
	n, err := count(app, q, dbx.Params{"p": r.ID, "r": refundID, "k": string(kind)})
	if err != nil || n > 0 {
		return err
	}
	ord, items, err := loadOrder(app, r.Order)
	if err != nil {
		return err
	}
	src := make([]taxdocs.SourceLine, len(items))
	seller := ""
	for i, it := range items {
		src[i] = taxdocs.SourceLine{Title: it.Title, Qty: it.Qty, Price: it.UnitPrice, Amount: it.LineMinor, TaxMode: it.TaxMode,
			TaxCode: it.TaxCode, TaxMinor: it.TaxMinor, Agent: it.SaleModel == string(ordering.Agent)}
		seller = it.Seller
	}
	doc, err := taxdocs.Build(s.TaxDocs, kind, op, ord.Currency, ord.Email, ord.Total, src)
	if err != nil {
		return err
	}
	issuer := s.IssuerCode
	if issuer == "" {
		issuer = "none"
	}
	rec, err := newRecord(app, "tax_documents", map[string]any{
		"payment": r.ID, "refund": refundID, "order": r.Order, "seller": seller, "region": ord.Region,
		"kind": string(kind), "operation": op, "issuer": issuer, "status": string(taxdocs.StatusPending),
		"lines": doc.Lines, "total_minor": doc.Total, "currency": doc.Currency, "buyer_email": doc.BuyerEmail, "attempts": 0,
	})
	if err != nil {
		return err
	}
	return outbox(app, ordering.TopicTaxDoc, key, map[string]any{"doc_id": rec.Id}, now)
}

// SaleDocument implements ordering.Store: the sale tax document built from
// the order snapshot (read only), sent with the payment to providers that
// register it inline. nil when the region needs no document.
func (s *Orders) SaleDocument(ctx context.Context, orderID string) (*taxdocs.Document, error) {
	if s.TaxDocs == nil {
		return nil, nil
	}
	kind, op, ok := s.TaxDocs.ForSale()
	if !ok {
		return nil, nil
	}
	ord, items, err := loadOrder(s.App, orderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ordering.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	src := make([]taxdocs.SourceLine, len(items))
	for i, it := range items {
		src[i] = taxdocs.SourceLine{Title: it.Title, Qty: it.Qty, Price: it.UnitPrice, Amount: it.LineMinor, TaxMode: it.TaxMode,
			TaxCode: it.TaxCode, TaxMinor: it.TaxMinor, Agent: it.SaleModel == string(ordering.Agent)}
	}
	doc, err := taxdocs.Build(s.TaxDocs, kind, op, ord.Currency, ord.Email, ord.Total, src)
	if err != nil {
		return nil, err
	}
	doc.IdempotencyKey = "taxdoc:order:" + orderID
	return &doc, nil
}

// ReplayEffects implements ordering.Store (reconciliation step 3).
func (s *Orders) ReplayEffects(ctx context.Context, orderID string, now time.Time) (ordering.Applied, error) {
	var a ordering.Applied
	err := RunTx(ctx, s.App, "payment.replay", func(ctx context.Context, app core.App) error {
		r, err := loadPay(app, `WHERE p."order" = {:o} AND p.status = 'succeeded'`, dbx.Params{"o": orderID})
		if err != nil {
			return err
		}
		a = ordering.Applied{OrderID: orderID, Order: ordering.OrderStatus(r.OStatus), Payment: ordering.PaymentSucceeded}
		n, err := s.saleEffects(app, r, now)
		if err != nil || a.Order != ordering.OrderPaid {
			a.EffectsWritten = n
			return err
		}
		m, err := deliveryEffects(app, orderID, now)
		a.EffectsWritten = n + m
		return err
	})
	return a, err
}

// insertRefund writes a refunds row; the key is unique per attempt.
func insertRefund(app core.App, r payRow, reason, note, by string, amount int64, status string, now time.Time) (string, error) {
	n, err := count(app, "SELECT COUNT(*) FROM refunds WHERE payment = {:p}", dbx.Params{"p": r.ID})
	if err != nil {
		return "", err
	}
	var prov, seller string
	if err := app.DB().NewQuery("SELECT provider, seller FROM payments WHERE id = {:id}").Bind(dbx.Params{"id": r.ID}).Row(&prov, &seller); err != nil {
		return "", err
	}
	rec, err := newRecord(app, "refunds", map[string]any{
		"payment": r.ID, "order": r.Order, "seller": seller, "amount_minor": amount, "currency": r.Currency,
		"provider": prov, "reason_code": reason, "note": truncate(note, 1000), "idempotency_key": fmt.Sprintf("refund:%s:%d", r.ID, n+1),
		"status": status, "requested_by": by, "attempts": 0,
	})
	if err != nil {
		return "", err
	}
	return rec.Id, nil
}

// requestRefundTx returns the live refund of the payment or creates one
// with its outbox command (in the caller's transaction).
func requestRefundTx(app core.App, r payRow, reason, note, by string, amount int64, now time.Time) (string, error) {
	var id string
	err := app.DB().NewQuery("SELECT id FROM refunds WHERE payment = {:p} AND status != 'failed'").Bind(dbx.Params{"p": r.ID}).Row(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if id, err = insertRefund(app, r, reason, note, by, amount, "pending", now); err != nil {
		return "", err
	}
	return id, outbox(app, ordering.TopicRefund, "refund:"+id, map[string]any{"refund_id": id}, now)
}

// RequestRefund implements ordering.Store.
func (s *Orders) RequestRefund(ctx context.Context, orderID, reason, note, by string, now time.Time) (ordering.RefundRef, error) {
	var id string
	err := RunTx(ctx, s.App, "refund.request", func(ctx context.Context, app core.App) error {
		r, err := loadPay(app, `WHERE p."order" = {:o} AND p.status IN ('succeeded', 'disputed')`, dbx.Params{"o": orderID})
		if errors.Is(err, ordering.ErrNotFound) {
			return ordering.ErrNotRefundable
		}
		if err != nil {
			return err
		}
		switch ordering.OrderStatus(r.OStatus) {
		case ordering.OrderPaid, ordering.OrderFulfilled, ordering.OrderDisputed, ordering.OrderRefundPending:
		default:
			return ordering.ErrNotRefundable
		}
		id, err = requestRefundTx(app, r, reason, note, by, r.Amount, now)
		return err
	})
	if err != nil {
		return ordering.RefundRef{}, err
	}
	return s.Refund(ctx, id)
}

// Refund implements ordering.Store.
func (s *Orders) Refund(ctx context.Context, id string) (ordering.RefundRef, error) {
	var r struct {
		ID       string `db:"id"`
		Order    string `db:"order_id"`
		Payment  string `db:"payment"`
		Provider string `db:"provider"`
		PPID     string `db:"ppid"`
		Key      string `db:"idempotency_key"`
		Status   string `db:"status"`
		Reason   string `db:"reason_code"`
		Amount   int64  `db:"amount_minor"`
		Currency string `db:"currency"`
	}
	err := s.App.DB().NewQuery(`SELECT r.id, r."order" AS order_id, r.payment, r.provider, p.provider_payment_id AS ppid,
		r.idempotency_key, r.status, r.reason_code, r.amount_minor, r.currency
		FROM refunds r JOIN payments p ON p.id = r.payment WHERE r.id = {:id}`).Bind(dbx.Params{"id": id}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return ordering.RefundRef{}, ordering.ErrNotFound
	}
	return ordering.RefundRef{ID: r.ID, OrderID: r.Order, PaymentID: r.Payment, Provider: r.Provider, ProviderPaymentID: r.PPID,
		Key: r.Key, Status: r.Status, Reason: r.Reason, Amount: money.Money{Minor: r.Amount, Currency: r.Currency}}, err
}

// ApplyRefund implements ordering.Store.
func (s *Orders) ApplyRefund(ctx context.Context, id string, res payments.RefundResult, callErr error, now time.Time) (ordering.Applied, error) {
	var a ordering.Applied
	err := RunTx(ctx, s.App, "refund.apply", func(ctx context.Context, app core.App) error {
		var ref struct {
			Payment string `db:"payment"`
			Status  string `db:"status"`
		}
		if err := app.DB().NewQuery("SELECT payment, status FROM refunds WHERE id = {:id}").Bind(dbx.Params{"id": id}).One(&ref); err != nil {
			return err
		}
		if ref.Status != "pending" {
			return nil
		}
		p := dbx.Params{"id": id, "now": pbDate(now), "prid": res.ProviderRefundID, "err": ""}
		switch {
		case ordering.IsPermanent(callErr):
			// the provider rejected the refund for good: no retry
			p["err"] = truncate(callErr.Error(), 500)
			if err := exec(app, `UPDATE refunds SET status = 'failed', attempts = attempts + 1, updated = {:now},
				last_error = {:err} WHERE id = {:id}`, p); err != nil {
				return err
			}
			return alert(app, "refund_failed", "refund", id, truncate(callErr.Error(), 500))
		case callErr != nil || res.State == payments.StatePending || res.State == "":
			if callErr != nil {
				p["err"] = truncate(callErr.Error(), 500)
			}
			return exec(app, `UPDATE refunds SET attempts = attempts + 1, last_error = {:err}, updated = {:now},
				provider_refund_id = CASE WHEN {:prid} != '' THEN {:prid} ELSE provider_refund_id END WHERE id = {:id}`, p)
		case res.State == payments.StateSucceeded:
			r, err := loadPay(app, "WHERE p.id = {:id}", dbx.Params{"id": ref.Payment})
			if err != nil {
				return err
			}
			if err := s.completeRefund(app, r, id, res.ProviderRefundID, now); err != nil {
				return err
			}
			o, err := orderStatus(app, r.Order)
			a = ordering.Applied{OrderID: r.Order, Order: o, Payment: ordering.PaymentRefunded, Changed: true}
			return err
		default:
			if err := exec(app, `UPDATE refunds SET status = 'failed', attempts = attempts + 1, updated = {:now},
				last_error = 'provider rejected the refund' WHERE id = {:id}`, p); err != nil {
				return err
			}
			return alert(app, "refund_failed", "refund", id, "provider rejected the refund")
		}
	})
	return a, err
}

// RequestTimeoutCancels implements ordering.Store.
func (s *Orders) RequestTimeoutCancels(ctx context.Context, now time.Time, olderThan time.Duration) (int, error) {
	n := 0
	err := RunTx(ctx, s.App, "timeout.request", func(ctx context.Context, app core.App) error {
		var rows []struct {
			Order    string `db:"order_id"`
			Payment  string `db:"payment_id"`
			Provider string `db:"provider"`
			PPID     string `db:"ppid"`
		}
		if err := app.DB().NewQuery(`SELECT o.id AS order_id, p.id AS payment_id, p.provider, p.provider_payment_id AS ppid
			FROM orders o JOIN payments p ON p."order" = o.id
			WHERE o.status = 'awaiting_payment' AND o.cancel_reason = '' AND o.created < {:before}
			  AND p.status = 'pending' AND p.provider_payment_id != '' LIMIT 200`).
			Bind(dbx.Params{"before": pbDate(now.Add(-olderThan))}).All(&rows); err != nil {
			return err
		}
		for _, r := range rows {
			if err := exec(app, "UPDATE orders SET cancel_reason = 'timeout_requested', updated = {:now} WHERE id = {:id} AND cancel_reason = ''",
				dbx.Params{"id": r.Order, "now": pbDate(now)}); err != nil {
				return err
			}
			if err := outbox(app, ordering.TopicCancel, "cancel:"+r.Payment,
				map[string]any{"Provider": r.Provider, "ProviderPaymentID": r.PPID}, now); err != nil {
				return err
			}
			n++
		}
		return nil
	})
	return n, err
}

// TaxDocument implements ordering.Store.
func (s *Orders) TaxDocument(ctx context.Context, id string) (ordering.TaxDoc, error) {
	var r struct {
		ID       string `db:"id"`
		Kind     string `db:"kind"`
		Op       string `db:"operation"`
		Status   string `db:"status"`
		Ext      string `db:"external_id"`
		Lines    string `db:"lines"`
		Total    int64  `db:"total_minor"`
		Currency string `db:"currency"`
		Email    string `db:"buyer_email"`
		Ref      string `db:"provider_ref"`
	}
	err := s.App.DB().NewQuery(`SELECT d.id, d.kind, d.operation, d.status, d.external_id, d.lines, d.total_minor, d.currency, d.buyer_email,
		CASE WHEN d.refund != '' THEN COALESCE(r.provider_refund_id, '') ELSE COALESCE(p.provider_payment_id, '') END AS provider_ref
		FROM tax_documents d LEFT JOIN payments p ON p.id = d.payment LEFT JOIN refunds r ON r.id = d.refund
		WHERE d.id = {:id}`).Bind(dbx.Params{"id": id}).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return ordering.TaxDoc{}, ordering.ErrNotFound
	}
	if err != nil {
		return ordering.TaxDoc{}, err
	}
	d := taxdocs.Document{ID: r.ID, Kind: taxdocs.Kind(r.Kind), Operation: r.Op, Currency: r.Currency, Total: r.Total,
		BuyerEmail: r.Email, IdempotencyKey: "taxdoc:" + r.ID, ProviderRef: r.Ref}
	if err := json.Unmarshal([]byte(r.Lines), &d.Lines); err != nil {
		return ordering.TaxDoc{}, err
	}
	return ordering.TaxDoc{ID: r.ID, Status: r.Status, ExternalID: r.Ext, Doc: d}, nil
}

// ApplyTaxDoc implements ordering.Store.
func (s *Orders) ApplyTaxDoc(ctx context.Context, id string, res taxdocs.Result, callErr error, now time.Time, maxAttempts int) (bool, error) {
	retry := false
	err := RunTx(ctx, s.App, "taxdoc.apply", func(ctx context.Context, app core.App) error {
		var d struct {
			Status   string `db:"status"`
			Attempts int    `db:"attempts"`
		}
		if err := app.DB().NewQuery("SELECT status, attempts FROM tax_documents WHERE id = {:id}").Bind(dbx.Params{"id": id}).One(&d); err != nil {
			return err
		}
		if d.Status != string(taxdocs.StatusPending) {
			return nil
		}
		p := dbx.Params{"id": id, "now": pbDate(now)}
		if callErr != nil || res.Status == taxdocs.StatusFailed {
			msg := "register rejected the document"
			if callErr != nil {
				msg = callErr.Error()
			}
			p["err"] = truncate(msg, 500)
			if d.Attempts+1 >= maxAttempts || res.Status == taxdocs.StatusFailed || ordering.IsPermanent(callErr) {
				if err := exec(app, `UPDATE tax_documents SET status = 'failed', attempts = attempts + 1, last_error = {:err}, updated = {:now} WHERE id = {:id}`, p); err != nil {
					return err
				}
				return alert(app, "taxdoc_failed", "tax_document", id, msg)
			}
			retry = true
			return exec(app, `UPDATE tax_documents SET attempts = attempts + 1, last_error = {:err}, updated = {:now} WHERE id = {:id}`, p)
		}
		attrs, _ := json.Marshal(res.Attrs)
		p["ext"], p["num"], p["attrs"] = res.ExternalID, res.Number, string(attrs)
		if res.Status == taxdocs.StatusIssued {
			return exec(app, `UPDATE tax_documents SET status = 'issued', external_id = {:ext}, number = {:num}, attrs = {:attrs},
				issued_at = {:now}, attempts = attempts + 1, last_error = '', updated = {:now} WHERE id = {:id}`, p)
		}
		return exec(app, `UPDATE tax_documents SET external_id = {:ext}, attempts = attempts + 1, updated = {:now} WHERE id = {:id}`, p)
	})
	return retry, err
}
