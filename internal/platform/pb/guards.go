package pb

import (
	"errors"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// LockedCollections are written only by Go code (domain commands, workers);
// no REST API write is allowed, not even by a superuser (ADR-0005,
// architecture "Write protection").
var LockedCollections = []string{
	"orders", "order_items", "payments", "payment_events", "refunds", "tax_documents",
	"ledger_entries", "entitlements", "downloads", "outbox", "payouts", "audit_log", "consents", "alerts",
}

// Guard errors.
var (
	ErrLockedCollection = errors.New("collection is written only by domain commands")
	ErrLedgerAppendOnly = errors.New("ledger_entries are append-only")
	ErrReleaseImmutable = errors.New("published release files are immutable (yank and publish a new version)")
	ErrAgentSingleStore = errors.New("sale_model agent requires the marketplace commerce mode")
)

// RegisterGuards binds the Go write-protection hooks. They run for every
// write path (REST API, admin UI, Go code) unless noted.
func RegisterGuards(app core.App) {
	deny := func(e *core.RecordRequestEvent) error {
		return e.ForbiddenError(ErrLockedCollection.Error(), nil)
	}
	app.OnRecordCreateRequest(LockedCollections...).Bind(&hook.Handler[*core.RecordRequestEvent]{Id: "market_lock_create", Func: deny})
	app.OnRecordUpdateRequest(LockedCollections...).Bind(&hook.Handler[*core.RecordRequestEvent]{Id: "market_lock_update", Func: deny})
	app.OnRecordDeleteRequest(LockedCollections...).Bind(&hook.Handler[*core.RecordRequestEvent]{Id: "market_lock_delete", Func: deny})

	// Model level, any path: the ledger is append-only.
	app.OnRecordUpdate("ledger_entries").BindFunc(func(e *core.RecordEvent) error { return ErrLedgerAppendOnly })
	app.OnRecordDelete("ledger_entries").BindFunc(func(e *core.RecordEvent) error { return ErrLedgerAppendOnly })

	// Published or yanked releases keep their file, checksum and size, and
	// never go back to draft.
	app.OnRecordUpdate("product_releases").BindFunc(func(e *core.RecordEvent) error {
		// the stored row, not Original(): a record reused across saves keeps
		// its pre-save original
		old, err := e.App.FindRecordById(e.Record.Collection(), e.Record.Id)
		if err != nil {
			return err
		}
		if s := old.GetString("status"); s == "published" || s == "yanked" {
			for _, f := range []string{"storage_key", "sha256", "size", "version", "product"} {
				if old.GetString(f) != e.Record.GetString(f) {
					return ErrReleaseImmutable
				}
			}
			if e.Record.GetString("status") == "draft" {
				return ErrReleaseImmutable
			}
		}
		return e.Next()
	})

	// ADR-0005 invariant: agent sale model only in marketplace mode.
	app.OnRecordValidate("sellers").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetString("sale_model") == "agent" {
			n := 0
			if err := e.App.DB().NewQuery("SELECT COUNT(*) FROM storefronts WHERE commerce_mode = 'marketplace'").Row(&n); err != nil {
				return err
			}
			if n == 0 {
				return ErrAgentSingleStore
			}
		}
		return e.Next()
	})
}
