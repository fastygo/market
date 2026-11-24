package pb_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/ordering"
	"github.com/fastygo/market/internal/payments"
	"github.com/fastygo/market/internal/payments/fakegw"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/tx"
	"github.com/fastygo/market/internal/region/ru"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type rig struct {
	app    core.App
	svc    *ordering.Service
	gw     *fakegw.Gateway
	clock  *clock
	buyer  string
	offer  string
	emails atomic.Int64
}

func newRig(t *testing.T) *rig {
	t.Helper()
	app := newApp(t)
	users, _ := app.FindCollectionByNameOrId("users")
	u := core.NewRecord(users)
	u.SetEmail("buyer@example.com")
	u.SetPassword("test-password-123")
	if err := app.Save(u); err != nil {
		t.Fatal(err)
	}
	prod, _ := app.FindFirstRecordByData("products", "slug", "aurora-theme")
	off, err := app.FindFirstRecordByFilter("offers", "product = {:p}", dbx.Params{"p": prod.Id})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{app: app, gw: fakegw.New([]byte("test-secret")), clock: &clock{t: time.Now().UTC()}, buyer: u.Id, offer: off.Id}
	r.svc = &ordering.Service{
		Store: &pb.Orders{App: app}, Tax: ru.TaxPolicy{},
		Gateways:         map[string]payments.Gateway{"fake": r.gw},
		RequiredConsents: []string{"terms"},
		Now:              r.clock.Now,
		Lease:            time.Minute,
		MaxAttempts:      3,
		Outbox: map[string]ordering.OutboxHandler{"email.purchase": func(ctx context.Context, key string, _ json.RawMessage) error {
			if err := tx.AssertNoTx(ctx); err != nil {
				return err
			}
			r.emails.Add(1)
			return nil
		}},
	}
	return r
}

func (r *rig) checkout(t *testing.T, key string) ordering.OrderView {
	t.Helper()
	v, err := r.svc.Checkout(context.Background(), r.req(key))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *rig) req(key string) ordering.CheckoutRequest {
	return ordering.CheckoutRequest{StorefrontCode: "default", BuyerID: r.buyer, BuyerEmail: "buyer@example.com",
		IdempotencyKey: key, OfferIDs: []string{r.offer}, Method: "card", Consents: []ordering.Consent{{Document: "terms", Version: "1"}}}
}

func (r *rig) n(t *testing.T, q string) int {
	t.Helper()
	n := 0
	if err := r.app.DB().NewQuery(q).Row(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *rig) status(t *testing.T, orderID string) (string, string) {
	t.Helper()
	v, err := r.svc.Store.OrderForBuyer(context.Background(), orderID, r.buyer)
	if err != nil {
		t.Fatal(err)
	}
	return string(v.Status), string(v.PaymentStatus)
}

// effects asserts exactly one set of paid effects.
func (r *rig) effects(t *testing.T) {
	t.Helper()
	if e, l, o := r.n(t, "SELECT COUNT(*) FROM entitlements"), r.n(t, "SELECT COUNT(*) FROM ledger_entries"), r.n(t, "SELECT COUNT(*) FROM outbox"); e != 1 || l != 3 || o != 1 {
		t.Fatalf("effects: entitlements %d ledger %d outbox %d", e, l, o)
	}
	if s := r.n(t, "SELECT COALESCE(SUM(amount_minor), 1) FROM ledger_entries"); s != 0 {
		t.Fatalf("ledger not balanced: %d", s)
	}
}

func (r *rig) webhook(t *testing.T, eventID, ppid string) bool {
	t.Helper()
	body, sig := r.gw.Webhook(eventID, ppid, payments.StateSucceeded)
	_ = sig
	dup, err := r.svc.AcceptWebhook(context.Background(), "fake", payments.Event{EventID: eventID, Type: "payment.succeeded", ProviderPaymentID: ppid, Payload: body})
	if err != nil {
		t.Fatal(err)
	}
	return dup
}

func TestCheckoutHappyPath(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	v := r.checkout(t, "k1")
	if v.Status != ordering.OrderAwaitingPayment || v.PaymentStatus != ordering.PaymentPending || v.ConfirmationURL == "" {
		t.Fatalf("after checkout: %+v", v)
	}
	if len(v.Number) != 13 || v.Number[:2] != "M-" || v.Total.Minor != 290000 {
		t.Fatalf("number/total: %+v", v)
	}
	var item struct {
		Tax  int64  `db:"tax_minor"`
		Rate int    `db:"tax_rate_bp"`
		Code string `db:"tax_code"`
	}
	_ = r.app.DB().NewQuery("SELECT tax_minor, tax_rate_bp, tax_code FROM order_items").One(&item)
	if item.Rate != 2200 || item.Tax != 52295 || item.Code != "vat22" {
		t.Fatalf("line tax snapshot %+v", item)
	}
	if r.n(t, "SELECT COUNT(*) FROM consents WHERE document = 'terms'") != 1 {
		t.Fatal("terms consent not stored")
	}
	r.gw.SetState(v.ProviderPaymentID, payments.StateSucceeded)
	r.webhook(t, "evt-1", v.ProviderPaymentID)
	if did, err := r.svc.ProcessInboxOnce(ctx); err != nil || !did {
		t.Fatal(did, err)
	}
	if o, p := r.status(t, v.OrderID); o != "paid" || p != "succeeded" {
		t.Fatalf("status %s %s", o, p)
	}
	r.effects(t)
	if did, err := r.svc.ProcessOutboxOnce(ctx); err != nil || !did || r.emails.Load() != 1 {
		t.Fatalf("outbox %v %v emails %d", did, err, r.emails.Load())
	}
	if r.n(t, "SELECT COUNT(*) FROM outbox WHERE status = 'done'") != 1 {
		t.Fatal("outbox row not done")
	}
}

// ADR-0013 #1: checkout retried with the same key -> one order, one payment.
func TestCheckoutSameKeyConcurrent(t *testing.T) {
	r := newRig(t)
	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, err := r.svc.Checkout(context.Background(), r.req("same"))
			if err != nil {
				t.Error(err)
				return
			}
			ids[i] = v.OrderID
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("different orders: %v", ids)
		}
	}
	if r.n(t, "SELECT COUNT(*) FROM orders") != 1 || r.n(t, "SELECT COUNT(*) FROM payments") != 1 || r.gw.Payments() != 1 {
		t.Fatal("same key must give one order and one payment")
	}
	r.checkout(t, "other")
	if r.n(t, "SELECT COUNT(*) FROM orders") != 2 {
		t.Fatal("a new key is a new order")
	}
}

// ADR-0013 #2: crash after CreatePayment, before storing the provider id ->
// retry or reconciliation heal it, no second provider payment.
func TestCrashAfterCreatePayment(t *testing.T) {
	for _, heal := range []string{"retry", "reconcile"} {
		t.Run(heal, func(t *testing.T) {
			r := newRig(t)
			crash := errors.New("crash")
			r.svc.AfterCreatePayment = func() error { return crash }
			if _, err := r.svc.Checkout(context.Background(), r.req("k")); !errors.Is(err, crash) {
				t.Fatalf("want crash, got %v", err)
			}
			r.svc.AfterCreatePayment = nil
			if r.n(t, "SELECT COUNT(*) FROM payments WHERE status = 'created' AND provider_payment_id = ''") != 1 {
				t.Fatal("payment must stay created without provider id")
			}
			if heal == "retry" {
				r.checkout(t, "k")
			} else {
				r.clock.Add(11 * time.Minute)
				if n, err := r.svc.Reconcile(context.Background()); err != nil || n != 1 {
					t.Fatalf("reconcile %d %v", n, err)
				}
			}
			if r.n(t, "SELECT COUNT(*) FROM payments WHERE status = 'pending' AND provider_payment_id != ''") != 1 || r.gw.Payments() != 1 {
				t.Fatal("not healed or second provider payment")
			}
			if r.n(t, "SELECT COUNT(*) FROM orders WHERE status = 'awaiting_payment'") != 1 {
				t.Fatal("order must be awaiting_payment")
			}
		})
	}
}

// ADR-0013 #3: webhook, return URL and reconciliation process the same
// payment concurrently -> one transition, one entitlement, one ledger txn.
func TestConcurrentApply(t *testing.T) {
	r := newRig(t)
	v := r.checkout(t, "k")
	ppid := v.ProviderPaymentID
	r.gw.SetState(ppid, payments.StateSucceeded)
	r.webhook(t, "evt-1", ppid)
	r.clock.Add(11 * time.Minute)
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 4; i++ {
		wg.Add(3)
		go func() { defer wg.Done(); _, err := r.svc.ProcessInboxOnce(ctx); errs <- err }()
		go func() { defer wg.Done(); _, err := r.svc.Return(ctx, r.buyer, v.OrderID); errs <- err }()
		go func() { defer wg.Done(); _, err := r.svc.Reconcile(ctx); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if o, p := r.status(t, v.OrderID); o != "paid" || p != "succeeded" {
		t.Fatalf("status %s %s", o, p)
	}
	r.effects(t)
	var ver struct {
		O int `db:"o"`
		P int `db:"p"`
	}
	_ = r.app.DB().NewQuery(`SELECT o.status_version AS o, p.status_version AS p FROM orders o JOIN payments p ON p."order" = o.id`).One(&ver)
	if ver.O != 2 || ver.P != 2 {
		t.Fatalf("one transition each expected, versions %+v", ver)
	}
}

// ADR-0013 #4: order cancelled, provider later reports succeeded -> paid.
func TestLateSuccessAfterCancel(t *testing.T) {
	r := newRig(t)
	v := r.checkout(t, "k")
	ppid := v.ProviderPaymentID
	r.gw.SetState(ppid, payments.StateCanceled)
	if _, err := r.svc.Apply(context.Background(), "fake", ppid); err != nil {
		t.Fatal(err)
	}
	if o, _ := r.status(t, v.OrderID); o != "cancelled" {
		t.Fatalf("want cancelled, got %s", o)
	}
	r.gw.SetState(ppid, payments.StateSucceeded)
	if _, err := r.svc.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}
	if o, p := r.status(t, v.OrderID); o != "paid" || p != "succeeded" {
		t.Fatalf("late success: %s %s", o, p)
	}
	r.effects(t)
}

// ADR-0013 #7: inbox and outbox workers re-run after a crash (expired
// lease) -> no double ledger entries; the email effect is re-run with the
// same idempotency key.
func TestWorkersRerunAfterExpiredLease(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	v := r.checkout(t, "k")
	ppid := v.ProviderPaymentID
	r.gw.SetState(ppid, payments.StateSucceeded)
	r.webhook(t, "evt-1", ppid)
	st := r.svc.Store
	if _, ok, err := st.ClaimEvent(ctx, r.clock.Now(), time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	// worker crashed after the claim; within the lease nobody else takes it
	if did, _ := r.svc.ProcessInboxOnce(ctx); did {
		t.Fatal("leased event must not be claimed twice")
	}
	r.clock.Add(2 * time.Minute)
	if did, err := r.svc.ProcessInboxOnce(ctx); err != nil || !did {
		t.Fatal(did, err)
	}
	r.effects(t)
	keys := map[string]int{}
	r.svc.Outbox["email.purchase"] = func(ctx context.Context, key string, _ json.RawMessage) error { keys[key]++; return nil }
	if _, ok, err := st.ClaimOutbox(ctx, r.clock.Now(), time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	r.clock.Add(2 * time.Minute)
	if did, err := r.svc.ProcessOutboxOnce(ctx); err != nil || !did {
		t.Fatal(did, err)
	}
	if len(keys) != 1 || r.n(t, "SELECT COUNT(*) FROM outbox WHERE status = 'done'") != 1 {
		t.Fatalf("outbox rerun: %v", keys)
	}
	r.effects(t)
}

// ADR-0013 #9: duplicate webhook -> accepted (2xx), processed once.
func TestDuplicateWebhook(t *testing.T) {
	r := newRig(t)
	v := r.checkout(t, "k")
	ppid := v.ProviderPaymentID
	r.gw.SetState(ppid, payments.StateSucceeded)
	if r.webhook(t, "evt-1", ppid) || !r.webhook(t, "evt-1", ppid) {
		t.Fatal("second delivery must be reported as duplicate")
	}
	for {
		did, err := r.svc.ProcessInboxOnce(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !did {
			break
		}
	}
	if r.n(t, "SELECT COUNT(*) FROM payment_events WHERE status = 'processed'") != 1 || r.gw.Calls("fetch") != 1 {
		t.Fatal("duplicate processed twice")
	}
	r.effects(t)
}

// ADR-0013 #10: paid order with missing effects -> reconciliation replays.
func TestReconcileReplaysMissingEffects(t *testing.T) {
	r := newRig(t)
	v := r.checkout(t, "k")
	ppid := v.ProviderPaymentID
	r.gw.SetState(ppid, payments.StateSucceeded)
	if _, err := r.svc.Apply(context.Background(), "fake", ppid); err != nil {
		t.Fatal(err)
	}
	// simulate a lost entitlement (raw SQL bypasses hooks)
	if _, err := r.app.DB().NewQuery("DELETE FROM entitlements").Execute(); err != nil {
		t.Fatal(err)
	}
	n, err := r.svc.Reconcile(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("reconcile %d %v", n, err)
	}
	r.effects(t)
}

// Rule zero (ADR-0007, review r2): no write transaction is open during a
// provider call, and the writer is free while the provider is slow.
func TestRuleZeroNoTxDuringProviderCalls(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	var calls atomic.Int64
	r.gw.BeforeCall = func(ctx context.Context, op string) error {
		calls.Add(1)
		if tx.Open() != 0 || tx.InTx(ctx) {
			return fmt.Errorf("%s called with a write tx open", op)
		}
		done := make(chan error, 1)
		go func() {
			done <- pb.RunTx(context.Background(), r.app, "probe", func(ctx context.Context, app core.App) error {
				_, err := app.DB().NewQuery("UPDATE storefronts SET updated = updated").Execute()
				return err
			})
		}()
		select {
		case err := <-done:
			return err
		case <-time.After(2 * time.Second):
			return fmt.Errorf("%s: writer blocked during provider call", op)
		}
	}
	v := r.checkout(t, "k")
	r.gw.SetState(v.ProviderPaymentID, payments.StateSucceeded)
	if _, err := r.svc.Return(ctx, r.buyer, v.OrderID); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 2 {
		t.Fatal("gateway not called")
	}
	r.effects(t)
}
