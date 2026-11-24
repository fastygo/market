package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Phase 1 money and fulfilment collections (architecture "Domain model").
// All are written only by Go code: API create/update/delete rules are nil
// and the Go guards deny REST writes even for superusers (ADR-0005).
// Lease and schedule times (locked_until, next_attempt_at,
// next_reconcile_at) are unix milliseconds so SQL comparisons stay simple.

func init() {
	m.Register(upOrdering, downOrdering)
}

var orderingCollections = []string{"ledger_entries", "outbox", "downloads", "entitlements", "payment_events", "payments", "order_items", "orders", "consents"}

func num(name string) *core.NumberField { return &core.NumberField{Name: name, OnlyInt: true} }

func date(name string) *core.DateField { return &core.DateField{Name: name} }

func strPtr(s string) *string { return &s }

func upOrdering(app core.App) error {
	find := func(name string) (*core.Collection, error) { return app.FindCollectionByNameOrId(name) }
	users, err := find("users")
	if err != nil {
		return err
	}
	users.Fields.Add(text("locale", false), text("pd_consent_version", false), &core.BoolField{Name: "marketing_opt_in"})
	if err := app.Save(users); err != nil {
		return err
	}
	storefronts, err := find("storefronts")
	if err != nil {
		return err
	}
	sellers, err := find("sellers")
	if err != nil {
		return err
	}
	products, err := find("products")
	if err != nil {
		return err
	}
	offers, err := find("offers")
	if err != nil {
		return err
	}
	releases, err := find("product_releases")
	if err != nil {
		return err
	}

	consents := core.NewBaseCollection("consents")
	consents.Fields.Add(rel("user", users, false), text("email", false), text("document", true),
		text("version", true), date("accepted_at"), text("ip_hash", false), text("order_key", false))
	timestamps(consents)
	consents.AddIndex("idx_consents_user", false, "user, document", "")
	if err := app.Save(consents); err != nil {
		return err
	}

	orders := core.NewBaseCollection("orders")
	orders.Fields.Add(
		text("number", true), rel("buyer", users, true), text("buyer_email", false),
		sel("status", true, "pending", "awaiting_payment", "paid", "fulfilled", "cancelled", "refund_pending", "refunded", "disputed"),
		text("currency", true), rel("storefront", storefronts, true), text("region", true),
		num("subtotal_minor"), num("discount_minor"), num("tax_minor"), num("total_minor"),
		sel("tax_display", true, "inclusive", "exclusive"), text("buyer_country", false),
		text("idempotency_key", true), text("terms_version", false), text("snapshot_digest", false),
		num("status_version"), date("paid_at"), date("cancelled_at"),
		sel("cancel_reason", false, "timeout_requested", "provider_canceled", "buyer", "operator"),
	)
	timestamps(orders)
	orders.AddIndex("idx_orders_number", true, "number", "")
	orders.AddIndex("idx_orders_idempotency_key", true, "idempotency_key", "")
	orders.AddIndex("idx_orders_buyer_status", false, "buyer, status", "")
	orders.ListRule = strPtr("buyer = @request.auth.id")
	orders.ViewRule = strPtr("buyer = @request.auth.id")
	if err := app.Save(orders); err != nil {
		return err
	}

	items := core.NewBaseCollection("order_items")
	items.Fields.Add(
		rel("order", orders, true), rel("seller", sellers, true), rel("product", products, true), rel("offer", offers, true),
		text("license_type", true), text("title_snapshot", true), text("product_slug", true), text("license_title", false),
		num("unit_price_minor"), num("unit_discount_minor"), num("qty"), num("line_minor"),
		text("tax_category", true), num("tax_rate_bp"),
		sel("tax_mode", true, "inclusive", "exclusive", "exempt", "reverse_charge", "none"),
		text("tax_code", false), num("tax_minor"), sel("sale_model", true, "principal", "agent"),
		num("support_months"), text("license_terms_version", false), text("currency", true),
		num("commission_minor"), &core.JSONField{Name: "agent_info"},
	)
	timestamps(items)
	items.AddIndex("idx_order_items_order_offer", true, "`order`, offer", "")
	items.AddIndex("idx_order_items_seller", false, "seller", "")
	if err := app.Save(items); err != nil {
		return err
	}

	payments := core.NewBaseCollection("payments")
	payments.Fields.Add(
		rel("order", orders, true), rel("seller", sellers, true), text("provider", true), text("method", true),
		text("provider_payment_id", false),
		sel("status", true, "created", "pending", "succeeded", "canceled", "failed", "refunded", "disputed"),
		num("amount_minor"), text("currency", true), text("idempotency_key", true), &core.URLField{Name: "confirmation_url"},
		num("status_version"), num("next_reconcile_at"), &core.JSONField{Name: "raw"},
	)
	timestamps(payments)
	payments.AddIndex("idx_payments_idempotency_key", true, "idempotency_key", "")
	payments.AddIndex("idx_payments_provider_id", true, "provider, provider_payment_id", "provider_payment_id != ''")
	payments.AddIndex("idx_payments_one_succeeded", true, "`order`", "status = 'succeeded'")
	payments.AddIndex("idx_payments_status_reconcile", false, "status, next_reconcile_at", "")
	payments.AddIndex("idx_payments_seller", false, "seller", "")
	if err := app.Save(payments); err != nil {
		return err
	}

	events := core.NewBaseCollection("payment_events")
	events.Fields.Add(
		text("provider", true), text("event_id", true), text("type", false), text("provider_payment_id", false),
		&core.JSONField{Name: "payload", MaxSize: 64 << 10}, &core.BoolField{Name: "verified"},
		sel("status", true, "received", "processing", "processed", "ignored", "dead"),
		num("locked_until"), num("attempts"), date("received_at"), date("processed_at"), text("error", false),
	)
	timestamps(events)
	events.AddIndex("idx_payment_events_provider_event", true, "provider, event_id", "")
	events.AddIndex("idx_payment_events_status_locked", false, "status, locked_until", "")
	if err := app.Save(events); err != nil {
		return err
	}

	ents := core.NewBaseCollection("entitlements")
	ents.Fields.Add(
		rel("user", users, true), rel("seller", sellers, true), rel("order_item", items, true), rel("product", products, true),
		text("license_type", true), text("purchase_code", true),
		sel("status", true, "active", "revoked", "refunded", "disputed"),
		num("support_months_snapshot"), date("support_until"), date("granted_at"),
	)
	timestamps(ents)
	ents.AddIndex("idx_entitlements_order_item", true, "order_item", "")
	ents.AddIndex("idx_entitlements_purchase_code", true, "purchase_code", "")
	ents.AddIndex("idx_entitlements_user", false, "user", "")
	ents.AddIndex("idx_entitlements_seller", false, "seller", "")
	ents.ListRule = strPtr("user = @request.auth.id")
	ents.ViewRule = strPtr("user = @request.auth.id")
	if err := app.Save(ents); err != nil {
		return err
	}

	downloads := core.NewBaseCollection("downloads")
	downloads.Fields.Add(rel("entitlement", ents, true), rel("release", releases, true), date("at"),
		text("ip_hash", false), text("ua_hash", false))
	timestamps(downloads)
	downloads.AddIndex("idx_downloads_entitlement", false, "entitlement", "")
	if err := app.Save(downloads); err != nil {
		return err
	}

	outbox := core.NewBaseCollection("outbox")
	outbox.Fields.Add(text("topic", true), text("idempotency_key", true), &core.JSONField{Name: "payload", MaxSize: 64 << 10},
		sel("status", true, "pending", "processing", "done", "dead"), num("locked_until"), num("attempts"),
		num("next_attempt_at"), text("last_error", false))
	timestamps(outbox)
	outbox.AddIndex("idx_outbox_idempotency_key", true, "idempotency_key", "")
	outbox.AddIndex("idx_outbox_status_next", false, "status, next_attempt_at", "")
	if err := app.Save(outbox); err != nil {
		return err
	}

	ledger := core.NewBaseCollection("ledger_entries")
	ledger.Fields.Add(text("txn_id", true), text("account", true), rel("seller", sellers, false),
		num("amount_minor"), text("currency", true), text("ref_type", true), text("ref_id", true))
	ledger.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
	ledger.AddIndex("idx_ledger_txn_account", true, "txn_id, account", "")
	ledger.AddIndex("idx_ledger_account_currency_created", false, "account, currency, created", "")
	ledger.AddIndex("idx_ledger_seller_currency_created", false, "seller, currency, created", "")
	ledger.AddIndex("idx_ledger_ref", false, "ref_type, ref_id", "")
	return app.Save(ledger)
}

func downOrdering(app core.App) error {
	for _, name := range orderingCollections {
		c, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
