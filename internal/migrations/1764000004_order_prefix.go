package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Order numbers use the ADR prefix `M` (M-2026-000123); the placeholder
// seed had `GD`. Only the untouched seed value is changed.

func init() {
	m.Register(func(app core.App) error {
		_, err := app.DB().NewQuery("UPDATE storefronts SET order_number_prefix = 'M' WHERE code = 'default' AND order_number_prefix = 'GD'").Execute()
		return err
	}, nil)
}
