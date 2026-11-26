package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(upPaymentAttempts, nil)
}

// upPaymentAttempts is expand-only (ADR-0033): the payments collection
// is the payment-attempt table (one row per provider call). Existing
// rows already hold provider_payment_id; this migration adds an attempt
// number (backfilled to 1), an optional legal_entity, and a unique
// (order, attempt) index. It does not rename the collection.
func upPaymentAttempts(app core.App) error {
	payments, err := app.FindCollectionByNameOrId("payments")
	if err != nil {
		return err
	}
	if payments.Fields.GetByName("attempt") == nil {
		payments.Fields.Add(&core.NumberField{Name: "attempt", OnlyInt: true, Min: ptr(1)})
	}
	if payments.Fields.GetByName("legal_entity") == nil {
		payments.Fields.Add(text("legal_entity", false))
	}
	if err := app.Save(payments); err != nil {
		return err
	}
	if _, err := app.DB().NewQuery("UPDATE payments SET attempt = 1 WHERE attempt IS NULL OR attempt = 0").Execute(); err != nil {
		return err
	}
	payments, err = app.FindCollectionByNameOrId("payments")
	if err != nil {
		return err
	}
	payments.AddIndex("idx_payments_order_attempt", true, "`order`, attempt", "")
	return app.Save(payments)
}
