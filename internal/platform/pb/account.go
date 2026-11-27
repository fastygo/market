package pb

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/notify"
	"github.com/fastygo/market/internal/platform/money"
)

// Users implements account.Users over the PocketBase users collection.
type Users struct{ App core.App }

var _ account.Users = (*Users)(nil)

func toUser(r *core.Record) account.User {
	return account.User{ID: r.Id, Email: r.Email(), TokenKey: r.TokenKey(), Verified: r.Verified()}
}

func (u *Users) find(email string) (*core.Record, error) {
	e, err := account.NormalizeEmail(email)
	if err != nil {
		return nil, err
	}
	r, err := u.App.FindAuthRecordByEmail("users", e)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, account.ErrNotFound
	}
	return r, err
}

// ByEmail implements account.Users.
func (u *Users) ByEmail(ctx context.Context, email string) (account.User, error) {
	r, err := u.find(email)
	if err != nil {
		return account.User{}, err
	}
	return toUser(r), nil
}

// ByID implements account.Users.
func (u *Users) ByID(ctx context.Context, id string) (account.User, error) {
	r, err := u.App.FindRecordById("users", id)
	if err != nil {
		return account.User{}, account.ErrNotFound
	}
	return toUser(r), nil
}

// Ensure implements account.Users.
func (u *Users) Ensure(ctx context.Context, email string) (account.User, bool, error) {
	r, err := u.find(email)
	if err == nil {
		return toUser(r), false, nil
	}
	if !errors.Is(err, account.ErrNotFound) {
		return account.User{}, false, err
	}
	col, err := u.App.FindCollectionByNameOrId("users")
	if err != nil {
		return account.User{}, false, err
	}
	e, _ := account.NormalizeEmail(email)
	rec := core.NewRecord(col)
	rec.SetEmail(e)
	rec.SetRandomPassword()
	if err := u.App.Save(rec); err != nil {
		// a concurrent request created it first
		if again, ferr := u.find(e); ferr == nil {
			return toUser(again), false, nil
		}
		return account.User{}, false, err
	}
	return toUser(rec), true, nil
}

// CheckPassword implements account.Users.
func (u *Users) CheckPassword(ctx context.Context, email, password string) (account.User, error) {
	r, err := u.find(email)
	if err != nil || password == "" || !r.ValidatePassword(password) {
		return account.User{}, account.ErrCredentials
	}
	return toUser(r), nil
}

// SessionToken implements account.Users.
func (u *Users) SessionToken(ctx context.Context, userID string) (string, error) {
	r, err := u.App.FindRecordById("users", userID)
	if err != nil {
		return "", account.ErrNotFound
	}
	return r.NewAuthToken()
}

// FromSession implements account.Users.
func (u *Users) FromSession(ctx context.Context, token string) (account.User, error) {
	r, err := u.App.FindAuthRecordByToken(token, core.TokenTypeAuth)
	if err != nil || r.Collection().Name != "users" {
		return account.User{}, account.ErrNotFound
	}
	return toUser(r), nil
}

// MarkVerified implements account.Users.
func (u *Users) MarkVerified(ctx context.Context, userID string) error {
	r, err := u.App.FindRecordById("users", userID)
	if err != nil || r.Verified() {
		return err
	}
	r.SetVerified(true)
	return u.App.Save(r)
}

// Account reads the buyer cabinet (read only, scoped by buyer).
type Account struct{ App core.App }

type accOrderRow struct {
	ID       string `db:"id"`
	Number   string `db:"number"`
	Status   string `db:"status"`
	Total    int64  `db:"total_minor"`
	Currency string `db:"currency"`
	Created  string `db:"created"`
}

func (r accOrderRow) order() account.Order {
	t, _ := time.Parse("2006-01-02 15:04:05.000Z", r.Created)
	return account.Order{ID: r.ID, Number: r.Number, Status: r.Status, Total: money.Money{Minor: r.Total, Currency: r.Currency}, Created: t}
}

// Orders lists a buyer's orders, newest first.
func (a *Account) Orders(ctx context.Context, buyerID string) ([]account.Order, error) {
	var rows []accOrderRow
	err := a.App.DB().NewQuery(`SELECT id, number, status, total_minor, currency, created FROM orders
		WHERE buyer = {:b} ORDER BY created DESC LIMIT 100`).Bind(dbx.Params{"b": buyerID}).All(&rows)
	if err != nil {
		return nil, err
	}
	out := make([]account.Order, len(rows))
	for i, r := range rows {
		out[i] = r.order()
		if out[i].Items, err = a.items(out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Order returns one of the buyer's orders by its number, with lines.
func (a *Account) Order(ctx context.Context, buyerID, number string) (account.Order, error) {
	return a.load("buyer = {:b} AND number = {:n}", dbx.Params{"b": buyerID, "n": number})
}

// OrderByID returns an order by id (purchase email; no owner scope).
func (a *Account) OrderByID(ctx context.Context, id string) (account.Order, string, string, error) {
	var owner struct {
		Buyer string `db:"buyer"`
		Email string `db:"buyer_email"`
	}
	if err := a.App.DB().NewQuery("SELECT buyer, buyer_email FROM orders WHERE id = {:id}").Bind(dbx.Params{"id": id}).One(&owner); err != nil {
		return account.Order{}, "", "", err
	}
	o, err := a.load("id = {:id}", dbx.Params{"id": id})
	return o, owner.Buyer, owner.Email, err
}

func (a *Account) load(where string, p dbx.Params) (account.Order, error) {
	var r accOrderRow
	err := a.App.DB().NewQuery(`SELECT id, number, status, total_minor, currency, created FROM orders WHERE ` + where + ` LIMIT 1`).Bind(p).One(&r)
	if errors.Is(err, sql.ErrNoRows) {
		return account.Order{}, account.ErrOrderNotFound
	}
	if err != nil {
		return account.Order{}, err
	}
	o := r.order()
	o.Items, err = a.items(o.ID)
	return o, err
}

// items are the lines of an order with their entitlement status.
func (a *Account) items(orderID string) ([]account.Item, error) {
	var items []struct {
		Title   string         `db:"title_snapshot"`
		Slug    string         `db:"product_slug"`
		License string         `db:"license_type"`
		Ent     sql.NullString `db:"ent"`
		Stack   string         `db:"stack"`
	}
	err := a.App.DB().NewQuery(`SELECT i.title_snapshot, i.product_slug, i.license_type, e.status AS ent,
		COALESCE((SELECT CASE WHEN json_valid(p.attributes) THEN json_extract(p.attributes, '$.stack') END FROM products p WHERE p.slug = i.product_slug LIMIT 1), '') AS stack
		FROM order_items i LEFT JOIN entitlements e ON e.order_item = i.id
		WHERE i."order" = {:o} ORDER BY i.id`).Bind(dbx.Params{"o": orderID}).All(&items)
	var out []account.Item
	for _, it := range items {
		out = append(out, account.Item{Title: it.Title, Slug: it.Slug, License: it.License, Stack: it.Stack, Entitlement: it.Ent.String})
	}
	return out, err
}

// Purchase implements notify.Purchases.
func (a *Account) Purchase(ctx context.Context, orderID string) (notify.Purchase, error) {
	o, buyer, email, err := a.OrderByID(ctx, orderID)
	if err != nil {
		return notify.Purchase{}, err
	}
	p := notify.Purchase{OrderID: o.ID, Number: o.Number, BuyerID: buyer, Email: email, Total: o.Total}
	for _, it := range o.Items {
		p.Items = append(p.Items, notify.Item{Title: it.Title, Slug: it.Slug})
	}
	return p, nil
}
