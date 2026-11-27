package screens

import (
	"github.com/a-h/templ"
	"strconv"
	"strings"

	"github.com/fastygo/market/internal/authoring"
	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/ui/design"

	selectfield "github.com/ui8kit/codegen/generated/templ/ui/select"
)

// SellerHome is the vendor "today" screen.
type SellerHome struct {
	Seller  cabinet.SellerRef
	Items   []authoring.Item
	Balance cabinet.Balance
	Sales   []cabinet.Sale
	Reviews int
}

// ItemForm is the product create or edit screen.
type ItemForm struct {
	Item       authoring.Item
	Categories []authoring.Category
	New        bool
	CSRF       string
	// Releases of the item; ReleasesOn is false when files go to S3 and
	// the operator uploads them.
	Releases   []authoring.Release
	ReleasesOn bool
}

// FinanceView is the sales and payouts screen of a seller or a partner.
type FinanceView struct {
	Party   string // seller | partner
	Balance cabinet.Balance
	Sales   []cabinet.Sale
	Payouts []cabinet.Payout
	CSRF    string
}

// TeamView is the seller team.
type TeamView struct {
	Members []cabinet.Member
	Owner   bool
	CSRF    string
}

// StoreView is the public seller profile form.
type StoreView struct {
	Store cabinet.StoreProfile
	CSRF  string
}

// SellerItemHref is the cabinet page of a product.
func SellerItemHref(id string) string { return "/seller/items/" + id }

func itemStatus(s Shell, st string) string { return s.T("ui.status." + st) }

func sellerItems(s Shell, items []authoring.Item) []design.SellerItem {
	out := make([]design.SellerItem, 0, len(items))
	for _, it := range items {
		note := it.Meta.Stack
		if it.Meta.Price > 0 {
			note = strings.TrimPrefix(note+" \u00b7 "+MinorMoney(it.Meta.Price, it.Meta.Currency), " \u00b7 ")
		}
		out = append(out, design.SellerItem{ID: it.ID, Title: it.Title, Note: note, Status: it.Status, StatusLabel: itemStatus(s, it.Status),
			Updated: fmtDate(it.Updated), Image: coverSrc(it.CoverURL), Href: SellerItemHref(it.ID)})
	}
	return out
}

func countStatus(items []authoring.Item, st string) int {
	n := 0
	for _, it := range items {
		if it.Status == st {
			n++
		}
	}
	return n
}

func resume(s Shell, v SellerHome) design.HighlightCardProps {
	for _, it := range v.Items {
		if it.Status == "draft" {
			return design.HighlightCardProps{Eyebrow: s.T("ui.seller.resume"), Title: it.Title, Text: or(it.ReviewNote, s.T("ui.seller.resume_text")),
				Cta: s.T("ui.seller.open_draft"), Href: SellerItemHref(it.ID), HeadingId: "resume-title"}
		}
	}
	title, text := s.T("ui.seller.first_item"), s.T("ui.seller.first_item_text")
	if len(v.Items) > 0 {
		title, text = s.T("ui.seller.next_item"), s.T("ui.seller.next_item_text")
	}
	return design.HighlightCardProps{Eyebrow: s.T("ui.seller.resume"), Title: title, Text: text,
		Cta: s.T("ui.seller.new_item"), Href: "/seller/items/new", HeadingId: "resume-title"}
}

func attention(s Shell, v SellerHome) []design.Task {
	var out []design.Task
	if n := countStatus(v.Items, "in_review"); n > 0 {
		out = append(out, design.Task{Icon: "clock", Title: tpl(s.T("ui.seller.in_review_n"), strconv.Itoa(n)), Text: s.T("ui.seller.in_review_text"), Cta: s.T("ui.common.open"), Href: "/seller/items"})
	}
	if n := countStatus(v.Items, "draft"); n > 0 {
		out = append(out, design.Task{Icon: "pencil", Title: tpl(s.T("ui.seller.drafts_n"), strconv.Itoa(n)), Text: s.T("ui.seller.drafts_text"), Cta: s.T("ui.common.open"), Href: "/seller/items"})
	}
	if v.Reviews > 0 {
		out = append(out, design.Task{Icon: "message-square", Title: tpl(s.T("ui.seller.reviews_n"), strconv.Itoa(v.Reviews)), Text: s.T("ui.seller.reviews_text"), Cta: s.T("ui.common.open"), Href: "/seller/reviews"})
	}
	if v.Seller.Status == "pending_review" {
		out = append(out, design.Task{Icon: "shield-check", Title: s.T("ui.seller.store_review"), Text: s.T("ui.seller.store_review_text"), Cta: s.T("ui.common.open"), Href: "/seller/store"})
	}
	if len(out) == 0 {
		out = append(out, design.Task{Icon: "circle-check", Title: s.T("ui.seller.all_clear"), Text: s.T("ui.seller.all_clear_text"), Cta: s.T("ui.seller.new_item"), Href: "/seller/items/new"})
	}
	return out
}

func statusPlan(s Shell, items []authoring.Item) []design.PlanItem {
	var out []design.PlanItem
	for _, st := range []string{"published", "in_review", "draft", "unlisted", "archived"} {
		out = append(out, design.PlanItem{Time: strconv.Itoa(countStatus(items, st)), Task: itemStatus(s, st)})
	}
	return out
}

func saleRows(s Shell, sales []cabinet.Sale, partner bool) []design.TableRow {
	out := make([]design.TableRow, 0, len(sales))
	for _, x := range sales {
		r := design.TableRow{"number": x.OrderNumber, "date": fmtDate(x.At), "status": s.T(orderStatusKey(x.Status)),
			"amount": MinorMoney(x.Line, x.Currency)}
		if !partner {
			r["item"] = x.Title + " \u00b7 " + licenseLabel(s, x.License)
			r["net"] = MinorMoney(x.Line-x.Commission, x.Currency)
		}
		out = append(out, r)
	}
	return out
}

func payoutRows(s Shell, ps []cabinet.Payout) []Row {
	out := make([]Row, 0, len(ps))
	for _, p := range ps {
		r := Row{Title: MinorMoney(p.Amount, p.Currency), Meta: fmtDate(p.Created) + " \u00b7 " + p.Period, Badge: s.T("ui.status." + p.Status), Tone: statusTone(p.Status)}
		if p.Note != "" {
			r.Lines = append(r.Lines, p.Note)
		}
		if p.Reference != "" {
			r.Lines = append(r.Lines, p.Reference)
		}
		out = append(out, r)
	}
	return out
}

func balanceStats(s Shell, b cabinet.Balance, partner bool) []design.Stat {
	st := []design.Stat{
		{Label: s.T("ui.finance.this_month"), Value: MinorMoney(b.ThisMonth, b.Currency)},
		{Label: s.T("ui.finance.available"), Value: MinorMoney(b.Available, b.Currency)},
		{Label: s.T("ui.finance.pending"), Value: MinorMoney(b.Pending, b.Currency)},
		{Label: s.T("ui.finance.paid"), Value: MinorMoney(b.Paid, b.Currency)},
	}
	if partner {
		st = append(st, design.Stat{Label: s.T("ui.partner.clicks"), Value: strconv.Itoa(b.Clicks)}, design.Stat{Label: s.T("ui.partner.orders"), Value: strconv.Itoa(b.Orders)})
	}
	return st
}

func splitRows(s Shell, b cabinet.Balance) []design.Row {
	return []design.Row{
		{Label: s.T("ui.finance.gross"), Value: MinorMoney(b.Gross, b.Currency)},
		{Label: s.T("ui.finance.commission"), Value: MinorMoney(b.Gross-b.Earned, b.Currency), Tone: "muted"},
		{Label: s.T("ui.finance.earned"), Value: MinorMoney(b.Earned, b.Currency)},
	}
}

func categoryOptions(s Shell, cs []authoring.Category) []selectfield.SelectOptionItem {
	out := []selectfield.SelectOptionItem{{Value: "", Label: s.T("ui.seller.no_category")}}
	for _, c := range cs {
		out = append(out, selectfield.SelectOptionItem{Value: c.ID, Label: c.Title})
	}
	return out
}

// minorInput shows minor units as a whole-unit amount.
func minorInput(v int64) string {
	if v == 0 {
		return ""
	}
	if v%100 == 0 {
		return strconv.FormatInt(v/100, 10)
	}
	return strconv.FormatFloat(float64(v)/100, 'f', 2, 64)
}

func itemActions(it authoring.Item) []string {
	switch it.Status {
	case "draft":
		return []string{authoring.ActSubmit, authoring.ActArchive}
	case "in_review":
		return []string{authoring.ActWithdraw, authoring.ActArchive}
	case "published":
		return []string{authoring.ActUnlist, authoring.ActArchive}
	case "unlisted":
		return []string{authoring.ActRelist, authoring.ActArchive}
	}
	return nil
}

func memberRows(s Shell, ms []cabinet.Member) []Row {
	out := make([]Row, 0, len(ms))
	for _, m := range ms {
		out = append(out, Row{Title: or(m.Name, m.Email), Meta: m.Email, Badge: s.T("ui.team." + m.Role), Tone: tone(m.Role == "owner")})
	}
	return out
}

func reviewModRows(s Shell, rs []editorial.Review) []Row {
	out := make([]Row, 0, len(rs))
	for _, r := range rs {
		row := Row{Title: r.ProductTitle, Href: ProductHref(r.ProductSlug), Meta: stars(r.Rating) + " \u00b7 " + or(r.AuthorName, s.T("ui.review.buyer")) + " \u00b7 " + fmtDate(r.Created),
			Badge: s.T("ui.status." + r.Status), Tone: statusTone(r.Status)}
		if r.Body != "" {
			row.Lines = append(row.Lines, r.Body)
		}
		if r.Reply != "" {
			row.Lines = append(row.Lines, s.T("ui.review.reply")+": "+r.Reply)
		}
		out = append(out, row)
	}
	return out
}

func itoa(n int) string { return strconv.Itoa(n) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func itemFormAction(v ItemForm) string {
	if v.New {
		return "/seller/items/new"
	}
	return SellerItemHref(v.Item.ID)
}

func actVariant(a string) string {
	if a == authoring.ActSubmit || a == authoring.ActRelist {
		return "dark"
	}
	return "outline"
}

func itemOptions(items []authoring.Item) []selectfield.SelectOptionItem {
	out := make([]selectfield.SelectOptionItem, 0, len(items))
	for _, it := range items {
		out = append(out, selectfield.SelectOptionItem{Value: it.ID, Label: it.Title})
	}
	return out
}

func financeSection(v FinanceView) Section {
	if v.Party == "partner" {
		return Section{Kind: "partner", Current: "payouts"}
	}
	return Section{Kind: "seller", Current: "finance"}
}

func financeAction(v FinanceView) string {
	if v.Party == "partner" {
		return "/partner/payouts"
	}
	return "/seller/finance"
}

func teamActions(s Shell, v TeamView) func(int) templ.Component {
	return func(i int) templ.Component {
		if !v.Owner || v.Members[i].Role == "owner" {
			return nil
		}
		return teamRemove(s, v, i)
	}
}

func actLabel(s Shell, a string) string { return s.T("ui.seller.act_" + a) }

func releaseRows(s Shell, rs []authoring.Release) []Row {
	out := make([]Row, 0, len(rs))
	for _, r := range rs {
		meta := r.Name + " \u00b7 " + sizeText(r.Size) + " \u00b7 " + fmtDate(r.Created)
		row := Row{Title: r.Version, Meta: meta, Badge: itemStatus(s, r.Status), Tone: statusTone(r.Status)}
		if c := stripTags(r.Changelog); c != "" {
			row.Lines = []string{c}
		}
		out = append(out, row)
	}
	return out
}

func releaseActions(s Shell, v ItemForm) func(i int) templ.Component {
	return func(i int) templ.Component {
		if i >= len(v.Releases) || v.Releases[i].Status != "draft" {
			return nil
		}
		return ReleasePublish(SellerItemHref(v.Item.ID)+"/releases/"+v.Releases[i].ID+"/publish", v.CSRF, s.T("ui.release.publish"))
	}
}
