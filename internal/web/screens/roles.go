package screens

import (
	"github.com/a-h/templ"
	selectfield "github.com/ui8kit/codegen/generated/templ/ui/select"

	"github.com/fastygo/market/internal/cabinet"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/ui/design"
)

// ArticleForm is the article editor state.
type ArticleForm struct {
	A    editorial.Article
	New  bool
	CSRF string
}

// PartnerView is the partner overview.
type PartnerView struct {
	Partner      cabinet.PartnerRef
	Link         string
	Balance      cabinet.Balance
	Orders       []cabinet.Sale
	PayoutMethod string
	CSRF         string
}

// EditorHome is the editor queue overview.
type EditorHome struct {
	Counts editorial.Counts
}

// ModView is a moderation list.
type ModView struct {
	Products     []editorial.ProductReview
	Articles     []editorial.Article
	Reviews      []editorial.Review
	Applications []cabinet.Application
	Pages        []editorial.Page
	Collections  []editorial.Collection
	Payouts      []cabinet.Payout
	Status       string
	CSRF         string
}

// PageForm edits a legal or info page.
type PageForm struct {
	P    editorial.Page
	New  bool
	CSRF string
}

// CollectionForm edits a collection.
type CollectionForm struct {
	C        editorial.Collection
	Products string // slugs or ids, one per line
	New      bool
	CSRF     string
}

// ArticleHref is the author editor of an article.
func ArticleHref(id string) string { return "/studio/articles/" + id }

func articleRows(s Shell, as []editorial.Article, edit bool) []Row {
	out := make([]Row, 0, len(as))
	for _, a := range as {
		r := Row{Title: a.Title, Meta: s.T("ui.article.kind_"+a.Kind) + " \u00b7 " + a.AuthorName + " \u00b7 " + fmtDate(a.Updated),
			Badge: s.T("ui.status." + a.Status), Tone: statusTone(a.Status)}
		if edit {
			r.Href = ArticleHref(a.ID)
		} else if a.Status == "published" {
			r.Href = "/blog/" + a.Slug
		}
		if a.Excerpt != "" {
			r.Lines = append(r.Lines, a.Excerpt)
		}
		if a.ReviewNote != "" {
			r.Lines = append(r.Lines, s.T("ui.seller.review_note")+": "+a.ReviewNote)
		}
		out = append(out, r)
	}
	return out
}

func kindOptions(s Shell) []selectfield.SelectOptionItem {
	return []selectfield.SelectOptionItem{
		{Value: "article", Label: s.T("ui.article.kind_article")},
		{Value: "guide", Label: s.T("ui.article.kind_guide")},
		{Value: "news", Label: s.T("ui.article.kind_news")},
	}
}

func editorStats(s Shell, c editorial.Counts) []design.Stat {
	return []design.Stat{
		{Label: s.T("ui.editor.q_products"), Value: itoa(c.Products)},
		{Label: s.T("ui.editor.q_releases"), Value: itoa(c.Releases)},
		{Label: s.T("ui.editor.q_articles"), Value: itoa(c.Articles)},
		{Label: s.T("ui.editor.q_reviews"), Value: itoa(c.Reviews)},
		{Label: s.T("ui.editor.q_applications"), Value: itoa(c.Applications)},
	}
}

func editorTasks(s Shell, c editorial.Counts) []design.Task {
	return []design.Task{
		{Icon: "package", Title: s.T("ui.editor.products"), Text: tpl(s.T("ui.editor.waiting"), itoa(c.Products)), Cta: s.T("ui.common.open"), Href: "/editor/products"},
		{Icon: "archive", Title: s.T("ui.editor.releases"), Text: tpl(s.T("ui.editor.waiting"), itoa(c.Releases)), Cta: s.T("ui.common.open"), Href: "/editor/releases"},
		{Icon: "newspaper", Title: s.T("ui.editor.articles"), Text: tpl(s.T("ui.editor.waiting"), itoa(c.Articles)), Cta: s.T("ui.common.open"), Href: "/editor/articles"},
		{Icon: "message-square", Title: s.T("ui.editor.reviews"), Text: tpl(s.T("ui.editor.waiting"), itoa(c.Reviews)), Cta: s.T("ui.common.open"), Href: "/editor/reviews"},
		{Icon: "badge-check", Title: s.T("ui.editor.applications"), Text: tpl(s.T("ui.editor.waiting"), itoa(c.Applications)), Cta: s.T("ui.common.open"), Href: "/editor/applications"},
		{Icon: "scale", Title: s.T("ui.editor.pages"), Text: s.T("ui.editor.pages_text"), Cta: s.T("ui.common.open"), Href: "/editor/pages"},
	}
}

func productModRows(s Shell, ps []editorial.ProductReview) []Row {
	out := make([]Row, 0, len(ps))
	for _, p := range ps {
		r := Row{Title: p.Title, Href: SellerItemHref(p.ID), Meta: p.SellerName + " \u00b7 " + fmtDate(p.Updated), Badge: s.T("ui.status." + p.Status), Tone: statusTone(p.Status)}
		if p.Status == "published" {
			r.Href = ProductHref(p.Slug)
		}
		if p.ReviewNote != "" {
			r.Lines = append(r.Lines, p.ReviewNote)
		}
		out = append(out, r)
	}
	return out
}

func appModRows(s Shell, as []cabinet.Application) []Row {
	out := appRows(s, as)
	for i, a := range as {
		out[i].Title = a.Email + " \u00b7 " + s.T("ui.role."+a.Role)
		if a.Message != "" {
			out[i].Lines = append(out[i].Lines, a.Message)
		}
		if a.Website != "" {
			out[i].Lines = append(out[i].Lines, a.Website)
		}
	}
	return out
}

func pageRows(s Shell, ps []editorial.Page) []Row {
	out := make([]Row, 0, len(ps))
	for _, p := range ps {
		out = append(out, Row{Title: p.Title, Href: "/editor/pages/" + p.Slug, Meta: "/pages/" + p.Slug + " \u00b7 " + s.T("ui.page.kind_"+p.Kind) + " \u00b7 " + s.T("ui.page.version") + " " + p.Version,
			Badge: s.T("ui.status." + p.Status), Tone: statusTone(p.Status)})
	}
	return out
}

func collectionRows(s Shell, cs []editorial.Collection) []Row {
	out := make([]Row, 0, len(cs))
	for _, c := range cs {
		out = append(out, Row{Title: c.Title, Href: "/editor/collections/" + c.Slug, Meta: "/collections/" + c.Slug + " \u00b7 " + itoa(len(c.ProductIDs)) + " " + s.T("ui.collections.items_short"),
			Badge: s.T("ui.status." + c.Status), Tone: statusTone(c.Status)})
	}
	return out
}

func payoutModRows(s Shell, ps []cabinet.Payout) []Row {
	out := payoutRows(s, ps)
	for i, p := range ps {
		out[i].Meta = s.T("ui.finance.party_"+p.PartyType) + " \u00b7 " + out[i].Meta
	}
	return out
}

func statusOptions(s Shell, values ...string) []selectfield.SelectOptionItem {
	out := make([]selectfield.SelectOptionItem, 0, len(values))
	for _, v := range values {
		out = append(out, selectfield.SelectOptionItem{Value: v, Label: s.T("ui.status." + v)})
	}
	return out
}

func toneOptions(s Shell) []selectfield.SelectOptionItem {
	return []selectfield.SelectOptionItem{{Value: "muted", Label: s.T("ui.collections.tone_muted")}, {Value: "accent", Label: s.T("ui.collections.tone_accent")}}
}

func pageKindOptions(s Shell) []selectfield.SelectOptionItem {
	return []selectfield.SelectOptionItem{{Value: "legal", Label: s.T("ui.page.kind_legal")}, {Value: "info", Label: s.T("ui.page.kind_info")}}
}

func modActions(s Shell, csrf, action string, ids []string, buttons [][2]string, note bool) func(int) templ.Component {
	return func(i int) templ.Component { return modForm(s, csrf, action, ids[i], buttons, note) }
}

func productIDs(ps []editorial.ProductReview) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func articleIDs(as []editorial.Article) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}

func reviewIDs(rs []editorial.Review) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.ID
	}
	return out
}

func appIDs(as []cabinet.Application) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID
	}
	return out
}

func payoutIDs(ps []cabinet.Payout) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.ID
	}
	return out
}

func statusTabs(s Shell, base, current string, values ...string) []design.TabItem {
	out := []design.TabItem{{ID: "", Label: s.T("ui.editor.all"), Href: base}}
	for _, v := range values {
		out = append(out, design.TabItem{ID: v, Label: s.T("ui.status." + v), Href: base + "?status=" + v})
	}
	return out
}

func pickStr(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}

func bps(n int) string {
	if n%100 == 0 {
		return itoa(n/100) + "%"
	}
	return itoa(n/100) + "." + itoa(n%100/10) + "%"
}

func actTone(status string) string {
	switch status {
	case "published", "approved", "paid":
		return "dark"
	}
	return "outline"
}
