package pb_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pocketbase/dbx"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/platform/pb"
	"github.com/fastygo/market/internal/platform/pb/pbtest"
)

func TestRoleApplicationsGrantRoles(t *testing.T) {
	app := pbtest.New(t)
	ctx := context.Background()
	users := &pb.Users{App: app}
	c := &pb.Cabinet{App: app}
	u, _, err := users.Ensure(ctx, "maker@example.com")
	if err != nil {
		t.Fatal(err)
	}
	p, err := c.Principal(ctx, u.ID)
	if err != nil || !p.Has(cabinet.Buyer) || p.Has(cabinet.Vendor) || p.Has(cabinet.Editor) {
		t.Fatalf("fresh principal %+v %v", p, err)
	}
	if err := c.Apply(ctx, cabinet.Application{UserID: u.ID, Role: "editor"}); !errors.Is(err, cabinet.ErrInvalid) {
		t.Fatalf("editor is not applyable: %v", err)
	}
	if err := c.Apply(ctx, cabinet.Application{UserID: u.ID, Role: "vendor", StoreName: "Maker Studio"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, cabinet.Application{UserID: u.ID, Role: "vendor", StoreName: "Maker Studio"}); !errors.Is(err, cabinet.ErrDuplicate) {
		t.Fatalf("second pending application: %v", err)
	}
	if err := c.Apply(ctx, cabinet.Application{UserID: u.ID, Role: "partner"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Apply(ctx, cabinet.Application{UserID: u.ID, Role: "author", Message: "I write guides"}); err != nil {
		t.Fatal(err)
	}
	apps, err := c.Applications(ctx, "pending")
	if err != nil || len(apps) != 3 {
		t.Fatalf("pending %d %v", len(apps), err)
	}
	for _, a := range apps {
		if err := c.Decide(ctx, a.ID, true, "editor@example.com", "ok"); err != nil {
			t.Fatalf("decide %s: %v", a.Role, err)
		}
		if err := c.Decide(ctx, a.ID, true, "editor@example.com", ""); !errors.Is(err, cabinet.ErrConflict) {
			t.Fatalf("decided twice: %v", err)
		}
	}
	p, err = c.Principal(ctx, u.ID)
	if err != nil || !p.Has(cabinet.Vendor) || !p.Has(cabinet.Partner) || !p.Has(cabinet.Author) || p.Has(cabinet.Editor) {
		t.Fatalf("granted principal %+v %v", p, err)
	}
	s, _ := p.Seller()
	if s.Slug != "maker-studio" || !s.Owner || s.Status != "pending_review" {
		t.Fatalf("seller %+v", s)
	}
	if !cabinet.ValidSlug(p.Partner.Code[:4] + "x") {
		t.Fatalf("partner code %q", p.Partner.Code)
	}
	if err := c.Grant(ctx, "maker@example.com", cabinet.Editor, "cli"); err != nil {
		t.Fatal(err)
	}
	if p, _ = c.Principal(ctx, u.ID); !p.Has(cabinet.Editor) || len(p.Roles()) != 5 {
		t.Fatalf("roles %v", p.Roles())
	}

	// vendor product flow: create, meta, submit, editor publishes
	items := &pb.Authoring{App: app}
	it, err := items.Create(ctx, u.ID, s.ID, authoring.Meta{Title: "Night Theme", Price: 150000, Extended: 900000, Stack: "Hugo"})
	if err != nil || it.Slug != "night-theme" || it.Status != "draft" || it.Meta.Price != 150000 || it.Meta.Extended != 900000 {
		t.Fatalf("create %+v %v", it, err)
	}
	if _, err := items.Create(ctx, u.ID, "nosuchseller", authoring.Meta{Title: "X y z"}); !errors.Is(err, authoring.ErrDenied) {
		t.Fatalf("foreign seller: %v", err)
	}
	if it, err = items.Act(ctx, u.ID, it.ID, authoring.ActSubmit); err != nil || it.Status != "in_review" {
		t.Fatalf("submit %+v %v", it, err)
	}
	ed := &pb.Editorial{App: app}
	q, err := ed.ProductQueue(ctx)
	if err != nil || len(q) == 0 || q[0].ID != it.ID {
		t.Fatalf("queue %+v %v", q, err)
	}
	if err := ed.ModerateProduct(ctx, it.ID, "published", ""); err != nil {
		t.Fatal(err)
	}
	// a store opened through an application can sell: checkout reads its offers
	offers, err := app.FindAllRecords("offers", dbx.HashExp{"product": it.ID})
	if err != nil || len(offers) == 0 {
		t.Fatalf("offers %d %v", len(offers), err)
	}
	ids := make([]string, len(offers))
	for i, o := range offers {
		ids[i] = o.Id
	}
	if got, err := (&pb.Orders{App: app}).Offers(ctx, ids); err != nil || len(got) != len(ids) {
		t.Fatalf("checkout offers of an applied store: %d %v", len(got), err)
	}

	// articles: author writes, editor publishes
	a, err := ed.SaveArticle(ctx, editorial.Article{Title: "How to choose a theme", Body: "# Hello\n\nText"}, u.ID, true)
	if err != nil || a.Status != editorial.InReview || a.BodyHTML == "" {
		t.Fatalf("article %+v %v", a, err)
	}
	if _, err := ed.Article(ctx, a.Slug); !errors.Is(err, editorial.ErrNotFound) {
		t.Fatal("unpublished article is public")
	}
	if err := ed.ModerateArticle(ctx, a.ID, editorial.Published, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := ed.Article(ctx, a.Slug); err != nil || got.PublishedAt.IsZero() {
		t.Fatalf("published %+v %v", got, err)
	}
	other, _, _ := users.Ensure(ctx, "other@example.com")
	if _, err := ed.SaveArticle(ctx, editorial.Article{ID: a.ID, Title: "Hijack", Version: a.Version}, other.ID, false); !errors.Is(err, editorial.ErrDenied) {
		t.Fatalf("foreign edit: %v", err)
	}

	// legal pages are seeded and editable
	pg, err := ed.Page(ctx, "terms")
	if err != nil || pg.Kind != "legal" {
		t.Fatalf("terms %+v %v", pg, err)
	}
	if err := ed.SavePage(ctx, editorial.Page{Slug: "terms", Title: "Terms", Body: "Updated **terms**", Kind: "legal", Version: "2"}, "editor"); err != nil {
		t.Fatal(err)
	}
	if pg, _ = ed.Page(ctx, "terms"); pg.Version != "2" || pg.BodyHTML == "" {
		t.Fatalf("saved page %+v", pg)
	}

	// reviews need a paid order
	if err := ed.AddReview(ctx, editorial.Review{ProductID: it.ID, UserID: other.ID, Rating: 5}); !errors.Is(err, editorial.ErrDenied) {
		t.Fatalf("review without purchase: %v", err)
	}

	// payouts cannot exceed the balance
	if err := c.RequestPayout(ctx, cabinet.Payout{PartyType: "seller", SellerID: s.ID, Amount: 100}, u.ID); !errors.Is(err, cabinet.ErrInvalid) {
		t.Fatalf("payout over balance: %v", err)
	}
	pr, owner, err := c.PartnerByCode(ctx, p.Partner.Code)
	if err != nil || owner != u.ID || pr.ID != p.Partner.ID {
		t.Fatalf("partner by code %+v %s %v", pr, owner, err)
	}
	if err := c.RecordClick(ctx, pr.ID, "/catalog", "h"); err != nil {
		t.Fatal(err)
	}
	if b, err := c.Balance(ctx, "partner", pr.ID); err != nil || b.Clicks != 1 {
		t.Fatalf("partner balance %+v %v", b, err)
	}
	if err := c.AddMember(ctx, s.ID, "staff@example.com"); err != nil {
		t.Fatal(err)
	}
	if team, _ := c.Team(ctx, s.ID); len(team) != 2 {
		t.Fatalf("team %+v", team)
	}
	if err := c.RemoveMember(ctx, s.ID, u.ID); !errors.Is(err, cabinet.ErrDenied) {
		t.Fatalf("owner removed: %v", err)
	}
	n, err := ed.ImportMessages(ctx, "xx", map[string]string{"ui.nav.home": "Home XX"})
	if m, _ := ed.Messages(ctx, "xx"); err != nil || n != 1 || m["ui.nav.home"] != "Home XX" {
		t.Fatalf("messages %v %v", m, err)
	}
}
