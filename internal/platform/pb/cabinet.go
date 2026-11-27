package pb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/fastygo/market/internal/account"
	"github.com/fastygo/market/internal/cabinet"
)

// Cabinet implements cabinet.Store (ADR-0035).
type Cabinet struct {
	App core.App
	Now func() time.Time
}

var _ cabinet.Store = (*Cabinet)(nil)

func (c *Cabinet) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func pbTime(s string) time.Time {
	for _, l := range []string{"2006-01-02 15:04:05.000Z", "2006-01-02 15:04:05Z", time.RFC3339} {
		if t, err := time.Parse(l, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// Principal implements cabinet.Store.
func (c *Cabinet) Principal(ctx context.Context, userID string) (cabinet.Principal, error) {
	u, err := c.App.FindRecordById("users", userID)
	if err != nil {
		return cabinet.Principal{}, cabinet.ErrNotFound
	}
	p := cabinet.Principal{UserID: u.Id, Email: u.Email(), Name: u.GetString("name")}
	var roles []struct {
		Role string `db:"role"`
	}
	if err := c.App.DB().NewQuery(`SELECT role FROM user_roles WHERE user = {:u} AND status = 'active'`).
		WithContext(ctx).Bind(dbx.Params{"u": userID}).All(&roles); err != nil {
		return p, fmt.Errorf("cabinet: roles: %w", err)
	}
	for _, r := range roles {
		switch cabinet.Role(r.Role) {
		case cabinet.Editor:
			p.Editor = true
		case cabinet.Author:
			p.Author = true
		}
	}
	var sellers []struct {
		ID     string `db:"id"`
		Slug   string `db:"slug"`
		Name   string `db:"display_name"`
		Status string `db:"status"`
		Role   string `db:"role"`
	}
	if err := c.App.DB().NewQuery(`SELECT s.id, s.slug, s.display_name, s.status, m.role FROM seller_members m
		JOIN sellers s ON s.id = m.seller WHERE m.user = {:u} AND s.status != 'suspended' ORDER BY m.created`).
		WithContext(ctx).Bind(dbx.Params{"u": userID}).All(&sellers); err != nil {
		return p, fmt.Errorf("cabinet: sellers: %w", err)
	}
	for _, s := range sellers {
		p.Sellers = append(p.Sellers, cabinet.SellerRef{ID: s.ID, Slug: s.Slug, Name: s.Name, Status: s.Status, Owner: s.Role == "owner"})
	}
	if pr, err := c.App.FindFirstRecordByData("partners", "user", userID); err == nil {
		p.Partner = &cabinet.PartnerRef{ID: pr.Id, Code: pr.GetString("code"), Status: pr.GetString("status"), RateBPS: pr.GetInt("rate_bps"),
			PayoutMethod: pr.GetString("payout_method")}
	}
	return p, nil
}

// SetName implements cabinet.Store.
func (c *Cabinet) SetName(ctx context.Context, userID, name string) error {
	u, err := c.App.FindRecordById("users", userID)
	if err != nil {
		return cabinet.ErrNotFound
	}
	name = strings.TrimSpace(name)
	if len(name) > 120 {
		return cabinet.ErrInvalid
	}
	u.Set("name", name)
	return c.App.Save(u)
}

// SetPassword implements cabinet.Store.
func (c *Cabinet) SetPassword(ctx context.Context, userID, password string) error {
	if len(password) < cabinet.MinPassword || len(password) > 128 {
		return cabinet.ErrInvalid
	}
	u, err := c.App.FindRecordById("users", userID)
	if err != nil {
		return cabinet.ErrNotFound
	}
	u.SetPassword(password)
	return c.App.Save(u)
}

type appRow struct {
	ID        string `db:"id"`
	User      string `db:"user"`
	Email     string `db:"email"`
	Role      string `db:"role"`
	Status    string `db:"status"`
	Message   string `db:"message"`
	StoreName string `db:"store_name"`
	StoreSlug string `db:"store_slug"`
	Website   string `db:"website"`
	Note      string `db:"decision_note"`
	Created   string `db:"created"`
	DecidedAt string `db:"decided_at"`
}

func (r appRow) domain() cabinet.Application {
	return cabinet.Application{ID: r.ID, UserID: r.User, Email: r.Email, Role: r.Role, Status: r.Status, Message: r.Message,
		StoreName: r.StoreName, StoreSlug: r.StoreSlug, Website: r.Website, DecisionNote: r.Note,
		Created: pbTime(r.Created), DecidedAt: pbTime(r.DecidedAt)}
}

const appSelect = `SELECT a.id, a.user, u.email, a.role, a.status, a.message, a.store_name, a.store_slug, a.website,
	a.decision_note, a.created, COALESCE(a.decided_at, '') AS decided_at
FROM role_applications a JOIN users u ON u.id = a.user`

// Apply implements cabinet.Store: one pending application per role.
func (c *Cabinet) Apply(ctx context.Context, a cabinet.Application) error {
	ok := false
	for _, r := range cabinet.Applyable {
		ok = ok || string(r) == a.Role
	}
	a.Message = strings.TrimSpace(a.Message)
	if !ok || len(a.Message) > 2000 {
		return cabinet.ErrInvalid
	}
	if a.Role == string(cabinet.Vendor) {
		a.StoreName = strings.TrimSpace(a.StoreName)
		if a.StoreSlug == "" {
			a.StoreSlug = cabinet.Slugify(a.StoreName)
		}
		if a.StoreName == "" || !cabinet.ValidSlug(a.StoreSlug) {
			return cabinet.ErrInvalid
		}
		if _, err := c.App.FindFirstRecordByData("sellers", "slug", a.StoreSlug); err == nil {
			return cabinet.ErrDuplicate
		}
	}
	if a.Website != "" && !strings.HasPrefix(a.Website, "https://") && !strings.HasPrefix(a.Website, "http://") {
		return cabinet.ErrInvalid
	}
	return c.App.RunInTransaction(func(tx core.App) error {
		n, err := count(tx, `SELECT COUNT(*) FROM role_applications WHERE user = {:u} AND role = {:r} AND status = 'pending'`,
			dbx.Params{"u": a.UserID, "r": a.Role})
		if err != nil {
			return err
		}
		if n > 0 {
			return cabinet.ErrDuplicate
		}
		_, err = newRecord(tx, "role_applications", map[string]any{"user": a.UserID, "role": a.Role, "status": "pending",
			"message": a.Message, "store_name": a.StoreName, "store_slug": a.StoreSlug, "website": a.Website})
		return err
	})
}

// MyApplications implements cabinet.Store.
func (c *Cabinet) MyApplications(ctx context.Context, userID string) ([]cabinet.Application, error) {
	var rows []appRow
	err := c.App.DB().NewQuery(appSelect + ` WHERE a.user = {:u} ORDER BY a.created DESC`).WithContext(ctx).
		Bind(dbx.Params{"u": userID}).All(&rows)
	return appsOf(rows), err
}

// Applications implements cabinet.Store ("" lists all).
func (c *Cabinet) Applications(ctx context.Context, status string) ([]cabinet.Application, error) {
	var rows []appRow
	err := c.App.DB().NewQuery(appSelect + ` WHERE ({:s} = '' OR a.status = {:s}) ORDER BY a.created DESC LIMIT 500`).
		WithContext(ctx).Bind(dbx.Params{"s": status}).All(&rows)
	return appsOf(rows), err
}

func appsOf(rows []appRow) []cabinet.Application {
	out := make([]cabinet.Application, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.domain())
	}
	return out
}

// Decide implements cabinet.Store: approval grants the role in the same
// transaction (author: user_roles; vendor: a seller in pending_review
// with the user as owner; partner: a partner code).
func (c *Cabinet) Decide(ctx context.Context, id string, approve bool, by, note string) error {
	return c.App.RunInTransaction(func(tx core.App) error {
		a, err := tx.FindRecordById("role_applications", id)
		if err != nil {
			return cabinet.ErrNotFound
		}
		if a.GetString("status") != "pending" {
			return cabinet.ErrConflict
		}
		status := "rejected"
		if approve {
			status = "approved"
			if err := grant(tx, a.GetString("user"), cabinet.Role(a.GetString("role")), by, a.GetString("store_name"), a.GetString("store_slug"), a.GetString("website")); err != nil {
				return err
			}
		}
		a.Set("status", status)
		a.Set("decided_by", by)
		a.Set("decided_at", pbDate(c.now()))
		a.Set("decision_note", strings.TrimSpace(note))
		return tx.Save(a)
	})
}

// Grant implements cabinet.Store (CLI and seed): the user must exist.
func (c *Cabinet) Grant(ctx context.Context, email string, r cabinet.Role, by string) error {
	e, err := account.NormalizeEmail(email)
	if err != nil {
		return cabinet.ErrInvalid
	}
	u, err := c.App.FindAuthRecordByEmail("users", e)
	if err != nil {
		return cabinet.ErrNotFound
	}
	return c.App.RunInTransaction(func(tx core.App) error {
		name := strings.SplitN(e, "@", 2)[0]
		return grant(tx, u.Id, r, by, name, cabinet.Slugify(name)+"-store", "")
	})
}

func grant(tx core.App, userID string, r cabinet.Role, by, storeName, storeSlug, website string) error {
	switch r {
	case cabinet.Editor, cabinet.Author:
		if rec, err := tx.FindFirstRecordByFilter("user_roles", "user = {:u} && role = {:r}", dbx.Params{"u": userID, "r": string(r)}); err == nil {
			rec.Set("status", "active")
			return tx.Save(rec)
		}
		_, err := newRecord(tx, "user_roles", map[string]any{"user": userID, "role": string(r), "status": "active", "granted_by": by})
		return err
	case cabinet.Vendor:
		if !cabinet.ValidSlug(storeSlug) {
			return cabinet.ErrInvalid
		}
		if _, err := tx.FindFirstRecordByData("sellers", "slug", storeSlug); err == nil {
			return cabinet.ErrDuplicate
		}
		s, err := newRecord(tx, "sellers", map[string]any{"slug": storeSlug, "display_name": storeName, "status": "pending_review",
			"website": website, "joined_at": pbDate(time.Now()), "sale_model": "principal", "commission_bps": 5000,
			"tax_profile": map[string]any{}, "legal_ids": map[string]any{}})
		if err != nil {
			return err
		}
		_, err = newRecord(tx, "seller_members", map[string]any{"seller": s.Id, "user": userID, "role": "owner"})
		return err
	case cabinet.Partner:
		if p, err := tx.FindFirstRecordByData("partners", "user", userID); err == nil {
			p.Set("status", "active")
			return tx.Save(p)
		}
		code := ""
		for i := 0; i < 5 && code == ""; i++ {
			try := strings.ToLower(strings.TrimRight(newRef(), "-_"))
			try = strings.Map(func(r rune) rune {
				if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
					return r
				}
				return 'x'
			}, try)
			if len(try) > 10 {
				try = try[:10]
			}
			if _, err := tx.FindFirstRecordByData("partners", "code", try); err != nil {
				code = try
			}
		}
		_, err := newRecord(tx, "partners", map[string]any{"user": userID, "code": code, "status": "active", "rate_bps": 1000,
			"level_path": []string{}})
		return err
	}
	return cabinet.ErrInvalid
}

func newRef() string { return strings.ReplaceAll(uuid4(), "-", "") }

// Balance implements cabinet.Store. Seller: paid lines minus commission;
// partner: rate of paid referred orders. Payout requests reduce the
// available amount. No money moves (ADR-0035).
func (c *Cabinet) Balance(ctx context.Context, partyType, id string) (cabinet.Balance, error) {
	b := cabinet.Balance{Currency: "RUB"}
	now := c.now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	prev := month.AddDate(0, -1, 0)
	var rows []struct {
		Line   int64  `db:"line"`
		Comm   int64  `db:"comm"`
		Cur    string `db:"currency"`
		PaidAt string `db:"paid_at"`
		Order  string `db:"oid"`
	}
	var err error
	switch partyType {
	case "seller":
		err = c.App.DB().NewQuery(`SELECT i.line_minor AS line, i.commission_minor AS comm, i.currency, COALESCE(o.paid_at, '') AS paid_at, o.id AS oid
			FROM order_items i JOIN orders o ON o.id = i.` + "`order`" + `
			WHERE i.seller = {:id} AND o.status IN ('paid', 'fulfilled')`).WithContext(ctx).Bind(dbx.Params{"id": id}).All(&rows)
	case "partner":
		p, perr := c.App.FindRecordById("partners", id)
		if perr != nil {
			return b, cabinet.ErrNotFound
		}
		rate := int64(p.GetInt("rate_bps"))
		err = c.App.DB().NewQuery(`SELECT o.total_minor AS line, 0 AS comm, o.currency, COALESCE(o.paid_at, '') AS paid_at, o.id AS oid
			FROM orders o WHERE json_extract(o.referral, '$.partner') = {:id} AND o.status IN ('paid', 'fulfilled')`).
			WithContext(ctx).Bind(dbx.Params{"id": id}).All(&rows)
		for i := range rows {
			rows[i].Comm = rows[i].Line - rows[i].Line*rate/10000
		}
		b.Clicks, _ = count(c.App, `SELECT COUNT(*) FROM referral_clicks WHERE partner = {:id}`, dbx.Params{"id": id})
	default:
		return b, cabinet.ErrInvalid
	}
	if err != nil {
		return b, fmt.Errorf("cabinet: balance: %w", err)
	}
	orders := map[string]bool{}
	for _, r := range rows {
		if r.Cur != "" {
			b.Currency = r.Cur
		}
		net := r.Line - r.Comm
		b.Gross += r.Line
		b.Earned += net
		orders[r.Order] = true
		t := pbTime(r.PaidAt)
		switch {
		case !t.Before(month):
			b.ThisMonth += net
		case !t.Before(prev):
			b.LastMonth += net
		}
	}
	b.Orders = len(orders)
	ps, err := c.Payouts(ctx, partyType, id)
	if err != nil {
		return b, err
	}
	for _, p := range ps {
		switch p.Status {
		case "paid":
			b.Paid += p.Amount
		case "requested", "approved":
			b.Pending += p.Amount
		}
	}
	b.Available = b.Earned - b.Paid - b.Pending
	if b.Available < 0 {
		b.Available = 0
	}
	return b, nil
}

// Payouts implements cabinet.Store.
func (c *Cabinet) Payouts(ctx context.Context, partyType, id string) ([]cabinet.Payout, error) {
	field := "seller"
	if partyType == "partner" {
		field = "partner"
	}
	var rows []struct {
		ID        string `db:"id"`
		Status    string `db:"status"`
		Amount    int64  `db:"amount_minor"`
		Currency  string `db:"currency"`
		Period    string `db:"period"`
		Reference string `db:"reference"`
		Note      string `db:"note"`
		Created   string `db:"created"`
	}
	err := c.App.DB().NewQuery(`SELECT id, status, amount_minor, currency, period, reference, note, created FROM payouts
		WHERE party_type = {:t} AND ` + field + ` = {:id} ORDER BY created DESC`).WithContext(ctx).
		Bind(dbx.Params{"t": partyType, "id": id}).All(&rows)
	out := make([]cabinet.Payout, 0, len(rows))
	for _, r := range rows {
		out = append(out, cabinet.Payout{ID: r.ID, PartyType: partyType, Status: r.Status, Amount: r.Amount, Currency: r.Currency,
			Period: r.Period, Reference: r.Reference, Note: r.Note, Created: pbTime(r.Created)})
	}
	return out, err
}

// RequestPayout implements cabinet.Store: a request up to the available
// balance; an operator pays it outside the system and marks it paid.
func (c *Cabinet) RequestPayout(ctx context.Context, p cabinet.Payout, userID string) error {
	id := p.SellerID
	if p.PartyType == "partner" {
		id = p.PartnerID
	}
	b, err := c.Balance(ctx, p.PartyType, id)
	if err != nil {
		return err
	}
	if p.Amount <= 0 || p.Amount > b.Available {
		return cabinet.ErrInvalid
	}
	f := map[string]any{"party_type": p.PartyType, "amount_minor": p.Amount, "currency": b.Currency, "status": "requested",
		"requested_by": userID, "period": c.now().UTC().Format("2006-01"), "note": strings.TrimSpace(p.Note)}
	if p.PartyType == "partner" {
		f["partner"] = p.PartnerID
	} else {
		f["seller"] = p.SellerID
	}
	_, err = newRecord(c.App, "payouts", f)
	return err
}

type saleRow struct {
	Number  string `db:"number"`
	Title   string `db:"title"`
	License string `db:"license"`
	Status  string `db:"status"`
	Cur     string `db:"currency"`
	Line    int64  `db:"line"`
	Comm    int64  `db:"comm"`
	At      string `db:"at"`
}

func salesOf(rows []saleRow) []cabinet.Sale {
	out := make([]cabinet.Sale, 0, len(rows))
	for _, r := range rows {
		out = append(out, cabinet.Sale{OrderNumber: r.Number, Title: r.Title, License: r.License, Status: r.Status,
			Currency: r.Cur, Line: r.Line, Commission: r.Comm, At: pbTime(r.At)})
	}
	return out
}

// Sales implements cabinet.Store.
func (c *Cabinet) Sales(ctx context.Context, sellerID string, limit int) ([]cabinet.Sale, error) {
	var rows []saleRow
	err := c.App.DB().NewQuery(`SELECT o.number, i.title_snapshot AS title, i.license_type AS license, o.status, i.currency,
		i.line_minor AS line, i.commission_minor AS comm, o.created AS at
		FROM order_items i JOIN orders o ON o.id = i.` + "`order`" + ` WHERE i.seller = {:s} ORDER BY o.created DESC LIMIT {:n}`).
		WithContext(ctx).Bind(dbx.Params{"s": sellerID, "n": limit}).All(&rows)
	return salesOf(rows), err
}

// PartnerOrders implements cabinet.Store: referred orders, no buyer data.
func (c *Cabinet) PartnerOrders(ctx context.Context, partnerID string, limit int) ([]cabinet.Sale, error) {
	var rows []saleRow
	err := c.App.DB().NewQuery(`SELECT o.number, '' AS title, '' AS license, o.status, o.currency, o.total_minor AS line,
		0 AS comm, o.created AS at FROM orders o WHERE json_extract(o.referral, '$.partner') = {:p} ORDER BY o.created DESC LIMIT {:n}`).
		WithContext(ctx).Bind(dbx.Params{"p": partnerID, "n": limit}).All(&rows)
	return salesOf(rows), err
}

// Team implements cabinet.Store.
func (c *Cabinet) Team(ctx context.Context, sellerID string) ([]cabinet.Member, error) {
	var rows []struct {
		User  string `db:"user"`
		Email string `db:"email"`
		Name  string `db:"name"`
		Role  string `db:"role"`
	}
	err := c.App.DB().NewQuery(`SELECT m.user, u.email, u.name, m.role FROM seller_members m JOIN users u ON u.id = m.user
		WHERE m.seller = {:s} ORDER BY m.role DESC, u.email`).WithContext(ctx).Bind(dbx.Params{"s": sellerID}).All(&rows)
	out := make([]cabinet.Member, 0, len(rows))
	for _, r := range rows {
		out = append(out, cabinet.Member{UserID: r.User, Email: r.Email, Name: r.Name, Role: r.Role})
	}
	return out, err
}

// AddMember implements cabinet.Store: staff by email; the account is
// created when missing (it signs in with a magic link).
func (c *Cabinet) AddMember(ctx context.Context, sellerID, email string) error {
	u, _, err := (&Users{App: c.App}).Ensure(ctx, email)
	if errors.Is(err, account.ErrInvalidEmail) {
		return cabinet.ErrInvalid
	}
	if err != nil {
		return err
	}
	if _, err := c.App.FindFirstRecordByFilter("seller_members", "seller = {:s} && user = {:u}", dbx.Params{"s": sellerID, "u": u.ID}); err == nil {
		return cabinet.ErrDuplicate
	}
	_, err = newRecord(c.App, "seller_members", map[string]any{"seller": sellerID, "user": u.ID, "role": "staff"})
	return err
}

// RemoveMember implements cabinet.Store: owners stay.
func (c *Cabinet) RemoveMember(ctx context.Context, sellerID, userID string) error {
	m, err := c.App.FindFirstRecordByFilter("seller_members", "seller = {:s} && user = {:u}", dbx.Params{"s": sellerID, "u": userID})
	if err != nil {
		return cabinet.ErrNotFound
	}
	if m.GetString("role") == "owner" {
		return cabinet.ErrDenied
	}
	return c.App.Delete(m)
}

// StoreProfile implements cabinet.Store.
func (c *Cabinet) StoreProfile(ctx context.Context, sellerID string) (cabinet.StoreProfile, error) {
	s, err := c.App.FindRecordById("sellers", sellerID)
	if err != nil {
		return cabinet.StoreProfile{}, cabinet.ErrNotFound
	}
	return cabinet.StoreProfile{Slug: s.GetString("slug"), DisplayName: s.GetString("display_name"), Headline: s.GetString("headline"),
		Bio: s.GetString("bio"), Website: s.GetString("website"), Status: s.GetString("status")}, nil
}

// SaveStore implements cabinet.Store (public profile fields only).
func (c *Cabinet) SaveStore(ctx context.Context, sellerID string, p cabinet.StoreProfile) error {
	s, err := c.App.FindRecordById("sellers", sellerID)
	if err != nil {
		return cabinet.ErrNotFound
	}
	p.DisplayName = strings.TrimSpace(p.DisplayName)
	if p.DisplayName == "" || len(p.Headline) > 200 || len(p.Bio) > 2000 {
		return cabinet.ErrInvalid
	}
	if p.Website != "" && !strings.HasPrefix(p.Website, "https://") && !strings.HasPrefix(p.Website, "http://") {
		return cabinet.ErrInvalid
	}
	s.Set("display_name", p.DisplayName)
	s.Set("headline", strings.TrimSpace(p.Headline))
	s.Set("bio", strings.TrimSpace(p.Bio))
	s.Set("website", p.Website)
	return c.App.Save(s)
}

// PartnerByCode implements cabinet.Store: active partners only; returns
// the partner and its user id.
func (c *Cabinet) PartnerByCode(ctx context.Context, code string) (cabinet.PartnerRef, string, error) {
	p, err := c.App.FindFirstRecordByData("partners", "code", strings.ToLower(strings.TrimSpace(code)))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && p.GetString("status") != "active") {
		return cabinet.PartnerRef{}, "", cabinet.ErrNotFound
	}
	if err != nil {
		return cabinet.PartnerRef{}, "", err
	}
	return cabinet.PartnerRef{ID: p.Id, Code: p.GetString("code"), Status: p.GetString("status"), RateBPS: p.GetInt("rate_bps")},
		p.GetString("user"), nil
}

// RecordClick implements cabinet.Store.
func (c *Cabinet) RecordClick(ctx context.Context, partnerID, landing, ipHash string) error {
	if len(landing) > 300 {
		landing = landing[:300]
	}
	_, err := newRecord(c.App, "referral_clicks", map[string]any{"partner": partnerID, "landing": landing, "ip_hash": ipHash})
	return err
}

// SavePartner implements cabinet.Store.
func (c *Cabinet) SavePartner(ctx context.Context, partnerID, payoutMethod string) error {
	p, err := c.App.FindRecordById("partners", partnerID)
	if err != nil {
		return cabinet.ErrNotFound
	}
	if len(payoutMethod) > 2000 {
		return cabinet.ErrInvalid
	}
	p.Set("payout_method", strings.TrimSpace(payoutMethod))
	return c.App.Save(p)
}

// AllPayouts implements cabinet.Store (editor queue).
func (c *Cabinet) AllPayouts(ctx context.Context, status string) ([]cabinet.Payout, error) {
	var rows []struct {
		ID        string `db:"id"`
		Party     string `db:"party_type"`
		Seller    string `db:"seller"`
		Partner   string `db:"partner"`
		Status    string `db:"status"`
		Amount    int64  `db:"amount_minor"`
		Currency  string `db:"currency"`
		Period    string `db:"period"`
		Reference string `db:"reference"`
		Note      string `db:"note"`
		Created   string `db:"created"`
	}
	q := `SELECT id, party_type, seller, partner, status, amount_minor, currency, period, reference, note, created FROM payouts`
	params := dbx.Params{}
	if status != "" {
		q += ` WHERE status = {:s}`
		params["s"] = status
	}
	err := c.App.DB().NewQuery(q + ` ORDER BY created DESC LIMIT 500`).WithContext(ctx).Bind(params).All(&rows)
	out := make([]cabinet.Payout, 0, len(rows))
	for _, r := range rows {
		out = append(out, cabinet.Payout{ID: r.ID, PartyType: r.Party, SellerID: r.Seller, PartnerID: r.Partner, Status: r.Status,
			Amount: r.Amount, Currency: r.Currency, Period: r.Period, Reference: r.Reference, Note: r.Note, Created: pbTime(r.Created)})
	}
	return out, err
}

// MarkPayout implements cabinet.Store: approved, paid or rejected; no
// money moves, the operator records what was done outside the system.
func (c *Cabinet) MarkPayout(ctx context.Context, id, status, note string) error {
	switch status {
	case "approved", "paid", "rejected":
	default:
		return cabinet.ErrInvalid
	}
	p, err := c.App.FindRecordById("payouts", id)
	if err != nil {
		return cabinet.ErrNotFound
	}
	if st := p.GetString("status"); st == "paid" || st == "rejected" || st == "cancelled" {
		return cabinet.ErrInvalid
	}
	p.Set("status", status)
	if note = strings.TrimSpace(note); note != "" {
		if len(note) > 500 {
			note = note[:500]
		}
		p.Set("note", note)
	}
	if status == "paid" {
		p.Set("paid_at", c.now())
	}
	return c.App.Save(p)
}
