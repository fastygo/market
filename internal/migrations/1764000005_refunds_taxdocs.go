package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// refunds (full only in phase 1), tax_documents (single source of truth of
// document state) and alerts (operator attention flags). Written only by Go
// code; REST writes are denied by the guards.

func init() {
	m.Register(upRefundsTaxdocs, downRefundsTaxdocs)
}

func upRefundsTaxdocs(app core.App) error {
	find := func(name string) (*core.Collection, error) { return app.FindCollectionByNameOrId(name) }
	orders, err := find("orders")
	if err != nil {
		return err
	}
	payments, err := find("payments")
	if err != nil {
		return err
	}
	sellers, err := find("sellers")
	if err != nil {
		return err
	}

	refunds := core.NewBaseCollection("refunds")
	refunds.Fields.Add(
		rel("payment", payments, true), rel("order", orders, true), rel("seller", sellers, true),
		num("amount_minor"), text("currency", true), text("provider", true),
		sel("reason_code", true, "buyer_request", "not_completable", "dispute_lost", "operator", "provider_initiated"),
		text("note", false), text("idempotency_key", true), text("provider_refund_id", false),
		sel("status", true, "pending", "succeeded", "failed"), text("requested_by", false),
		num("attempts"), text("last_error", false),
	)
	timestamps(refunds)
	refunds.AddIndex("idx_refunds_idempotency_key", true, "idempotency_key", "")
	refunds.AddIndex("idx_refunds_provider_id", true, "provider, provider_refund_id", "provider_refund_id != ''")
	refunds.AddIndex("idx_refunds_one_live", true, "payment", "status != 'failed'")
	refunds.AddIndex("idx_refunds_seller", false, "seller", "")
	if err := app.Save(refunds); err != nil {
		return err
	}

	docs := core.NewBaseCollection("tax_documents")
	docs.Fields.Add(
		rel("payment", payments, false), rel("refund", refunds, false), rel("order", orders, true), rel("seller", sellers, true),
		text("region", true), sel("kind", true, "receipt", "receipt_return", "invoice", "credit_note"), text("operation", false),
		text("issuer", true), text("external_id", false), text("number", false),
		sel("status", true, "pending", "issued", "failed", "not_required"),
		&core.JSONField{Name: "lines", MaxSize: 64 << 10}, &core.JSONField{Name: "attrs"},
		num("total_minor"), text("currency", true), text("buyer_email", false), num("attempts"), text("last_error", false), date("issued_at"),
	)
	timestamps(docs)
	docs.AddIndex("idx_tax_documents_issuer_external", true, "issuer, external_id", "external_id != ''")
	docs.AddIndex("idx_tax_documents_payment_kind", true, "payment, kind", "refund = ''")
	docs.AddIndex("idx_tax_documents_refund_kind", true, "refund, kind", "refund != ''")
	docs.AddIndex("idx_tax_documents_status", false, "status", "")
	docs.AddIndex("idx_tax_documents_seller", false, "seller", "")
	if err := app.Save(docs); err != nil {
		return err
	}

	alerts := core.NewBaseCollection("alerts")
	alerts.Fields.Add(text("kind", true), text("ref_type", true), text("ref_id", true), text("message", false), &core.BoolField{Name: "resolved"})
	timestamps(alerts)
	alerts.AddIndex("idx_alerts_ref", true, "kind, ref_type, ref_id", "")
	return app.Save(alerts)
}

func downRefundsTaxdocs(app core.App) error {
	for _, name := range []string{"alerts", "tax_documents", "refunds"} {
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
