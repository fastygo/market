package screens

import (
	"net/url"

	selectfield "github.com/ui8kit/codegen/generated/templ/ui/select"
	"html"
	"strconv"
	"strings"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/editorial"
	"github.com/fastygo/market/internal/platform/money"
	"github.com/fastygo/market/ui/design"
)

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Money formats minor units.
func Money(m money.Money) string { return formatMinor(m.Minor, m.Currency) }

// MinorMoney formats minor units of a currency.
func MinorMoney(minor int64, currency string) string { return formatMinor(minor, currency) }

// HomeView is the storefront home.
type HomeView struct {
	Latest      []catalog.Product
	Collections []editorial.Collection
	Author      catalog.Seller
	Works       []catalog.Product
	Articles    []editorial.Article
}

// CatalogView is a catalog page with its filters.
type CatalogView struct {
	Active                catalog.Category
	Query, Sort, Min, Max string
	Free                  bool
	Products              []catalog.Product
	Page                  int
	More                  bool
}

// ProductView is the product page.
type ProductView struct {
	P         catalog.Product
	Author    catalog.Seller
	More      []catalog.Product
	Reviews   []editorial.Review
	CanReview bool
	CSRF      string
	Download  bool
}

// CheckoutPageView is GET /checkout/{slug}.
type CheckoutPageView struct {
	P       catalog.Product
	License string
	CV      CheckoutView
}

func card(s Shell, p catalog.Product) design.ProductCardData {
	return design.ProductCardData{ID: p.ID, Title: p.Title, Label: p.Category.Title, Stack: p.Stack, Seller: p.SellerName,
		Price: priceFrom(s, p), Cover: coverSrc(p.CoverURL), Href: ProductHref(p.Slug)}
}

func cards(s Shell, ps []catalog.Product) []design.ProductCardData {
	out := make([]design.ProductCardData, 0, len(ps))
	for _, p := range ps {
		out = append(out, card(s, p))
	}
	return out
}

func priceFrom(s Shell, p catalog.Product) string {
	if !p.HasOffer {
		return ""
	}
	if p.Price.Minor == 0 {
		return s.T("ui.checkout.free")
	}
	if len(p.Offers) > 1 {
		return s.T("ui.product.from") + " " + Money(p.Price)
	}
	return Money(p.Price)
}

func homeHero(s Shell, v HomeView, q string) design.HomeHeroProps {
	h := design.HomeHeroProps{Title: s.T("ui.home.title"), Lead: s.T("ui.home.lead"), Placeholder: s.T("ui.home.placeholder"),
		Submit: s.T("ui.home.submit"), SearchLabel: s.T("ui.header.search"), Query: q, Image: "/static/art/hero.webp",
		ImageAlt: s.T("ui.home.hero_alt"), Caption: s.Brand, CaptionMeta: s.Tagline(), Href: "/catalog"}
	seen := map[string]bool{}
	for _, p := range v.Latest {
		if p.Stack != "" && !seen[p.Stack] && len(h.Tags) < 4 {
			seen[p.Stack] = true
			h.Tags = append(h.Tags, p.Stack)
		}
	}
	if len(v.Latest) > 0 && v.Latest[0].CoverURL != "" {
		p := v.Latest[0]
		h.Image, h.ImageAlt, h.Caption, h.CaptionMeta, h.Href = p.CoverURL, p.Title, p.Title, p.Stack, ProductHref(p.Slug)
	}
	return h
}

func homeCollections(s Shell, cs []editorial.Collection) []design.Collection {
	out := make([]design.Collection, 0, len(cs))
	for _, c := range cs {
		out = append(out, design.Collection{ID: c.Slug, Eyebrow: c.Eyebrow, Title: c.Title, Cta: or(c.Cta, s.T("ui.home.to_collection")),
			Href: "/collections/" + c.Slug, Image: or(c.ImageURL, "/static/art/hero.webp"), ImageAlt: c.Title, Tone: c.Tone})
	}
	return out
}

func authorPromo(s Shell, v HomeView) design.AuthorPromoProps {
	a := v.Author
	p := design.AuthorPromoProps{Title: s.T("ui.home.author_title"), Name: a.DisplayName, Initials: initials(a.DisplayName),
		Bio: or(a.Headline, a.Bio), Cta: s.T("ui.home.author_cta"), Href: AuthorHref(a.Slug), HeadingId: "author-title"}
	for i, w := range v.Works {
		if i == 2 {
			break
		}
		p.Works = append(p.Works, design.Work{Title: w.Title, Meta: w.Stack, Image: coverSrc(w.CoverURL), Href: ProductHref(w.Slug)})
	}
	return p
}

func purchaseSteps(s Shell) []design.Step {
	return []design.Step{
		{Icon: "eye", Title: s.T("ui.home.step1_title"), Text: s.T("ui.home.step1_text")},
		{Icon: "book-open", Title: s.T("ui.home.step2_title"), Text: s.T("ui.home.step2_text")},
		{Icon: "package", Title: s.T("ui.home.step3_title"), Text: s.T("ui.home.step3_text")},
	}
}

func catalogHref(v CatalogView) string {
	if v.Active.Slug != "" {
		return CategoryHref(v.Active.Slug)
	}
	return "/catalog"
}

func catalogFilters(s Shell, v CatalogView) design.CatalogFiltersProps {
	cats := []design.NavItem{{ID: "", Label: s.T("ui.catalog.all"), Href: "/catalog", Icon: "layers"}}
	cats = append(cats, navCategories(s.Categories)...)
	return design.CatalogFiltersProps{
		Categories: cats, Current: v.Active.Slug, Min: v.Min, Max: v.Max, OnlyFree: v.Free, Open: true,
		Labels: design.CatalogFiltersLabels{Filters: s.T("ui.catalog.filters"), Label: s.T("ui.a11y.filters"),
			Categories: s.T("ui.catalog.categories"), Editor: s.T("ui.catalog.editor"), Price: s.T("ui.catalog.price"),
			Min: s.T("ui.catalog.min"), Max: s.T("ui.catalog.max"), Free: s.T("ui.catalog.free"), Type: s.T("ui.catalog.type")},
	}
}

func catalogResults(s Shell, v CatalogView) design.CatalogResultsProps {
	section := or(v.Active.Title, s.T("ui.catalog.title"))
	r := design.CatalogResultsProps{
		Placeholder: strings.ReplaceAll(s.T("ui.catalog.search"), "{section}", section), Query: v.Query,
		Count: strings.ReplaceAll(s.T("ui.catalog.count"), "{n}", strconv.Itoa(len(v.Products))), Empty: s.T("ui.catalog.empty"),
		Reset: s.T("ui.catalog.reset"), Sort: v.Sort, Items: cards(s, v.Products),
		Sorts: []design.SelectItem{
			{Value: "", Label: s.T("ui.catalog.sort_new")}, {Value: "popular", Label: s.T("ui.catalog.sort_popular")},
			{Value: "rating", Label: s.T("ui.catalog.sort_rating")}, {Value: "price", Label: s.T("ui.catalog.sort_price_up")},
			{Value: "-price", Label: s.T("ui.catalog.sort_price_down")},
		},
		Labels: design.CatalogResultsLabels{Sort: s.T("ui.catalog.sort"), Remove: s.T("ui.catalog.remove"), Results: s.T("ui.catalog.results")},
	}
	base := catalogHref(v)
	if v.Query != "" {
		r.Chips = append(r.Chips, design.Option{ID: base, Label: v.Query})
	}
	if v.Free {
		r.Chips = append(r.Chips, design.Option{ID: base, Label: s.T("ui.catalog.free")})
	}
	return r
}

func licenseLabel(s Shell, l string) string {
	switch l {
	case "regular", "extended":
		return s.T("ui.license." + l)
	}
	return strings.ToUpper(l[:min(1, len(l))]) + l[min(1, len(l)):]
}

func licenses(s Shell, p catalog.Product) []design.License {
	out := make([]design.License, 0, len(p.Offers))
	for _, o := range p.Offers {
		price := Money(o.Price)
		if o.Price.Minor == 0 {
			price = s.T("ui.checkout.free")
		}
		out = append(out, design.License{ID: o.License, Label: licenseLabel(s, o.License), Text: s.T("ui.license." + o.License + "_text"), Price: price})
	}
	return out
}

func offerOf(p catalog.Product, license string) (catalog.Offer, bool) {
	for _, o := range p.Offers {
		if o.License == license {
			return o, true
		}
	}
	if len(p.Offers) > 0 {
		return p.Offers[0], true
	}
	return catalog.Offer{}, false
}

// CheckoutHref is the checkout page of a product and license.
func CheckoutHref(slug, license string) string {
	h := "/checkout/" + slug
	if license != "" {
		h += "?license=" + license
	}
	return h
}

func buyLabel(s Shell, p catalog.Product) string {
	o, ok := offerOf(p, "regular")
	if !ok {
		return s.T("ui.product.unavailable")
	}
	if o.Price.Minor == 0 {
		return s.T("ui.product.get_free")
	}
	return s.T("ui.product.buy") + " " + Money(o.Price)
}

func licensePicker(s Shell, v ProductView) design.LicensePickerProps {
	first := ""
	if o, ok := offerOf(v.P, "regular"); ok {
		first = o.License
	}
	return design.LicensePickerProps{Title: s.T("ui.product.choose"), Licenses: licenses(s, v.P), Value: first,
		Compare: s.T("ui.product.compare"), CompareHref: "/pages/licenses", Buy: buyLabel(s, v.P),
		BuyHref: CheckoutHref(v.P.Slug, first), Note: s.T("ui.product.after")}
}

func productHead(s Shell, v ProductView) design.ProductHeadProps {
	crumbs := []design.Crumb{{Label: s.T("ui.nav.home"), Href: "/"}, {Label: s.T("ui.catalog.title"), Href: "/catalog"}}
	if v.P.Category.Slug != "" {
		crumbs = append(crumbs, design.Crumb{Label: v.P.Category.Title, Href: CategoryHref(v.P.Category.Slug)})
	}
	tags := v.P.Tags
	if v.P.Stack != "" {
		tags = append([]string{v.P.Stack}, tags...)
	}
	return design.ProductHeadProps{Crumbs: crumbs, CrumbsLabel: s.T("ui.a11y.breadcrumbs"), Current: v.P.Title, Title: v.P.Title,
		Lead: v.P.Excerpt, Seller: v.P.SellerName, SellerHref: AuthorHref(v.P.SellerSlug), Tags: tags,
		SaveLabel: s.T("ui.product.save"), SavedLabel: s.T("ui.product.saved")}
}

func gallery(s Shell, p catalog.Product) design.ProductGalleryProps {
	imgs := []design.GalleryImage{{Src: coverSrc(p.CoverURL), Alt: p.Title}}
	for _, src := range p.Images {
		imgs = append(imgs, design.GalleryImage{Src: src, Alt: p.Title})
	}
	g := design.ProductGalleryProps{Images: imgs, Label: s.T("ui.a11y.gallery"), ThumbLabel: s.T("ui.product.thumb")}
	if p.DemoURL != "" {
		g.Link, g.LinkHref = s.T("ui.product.demo"), p.DemoURL
	}
	return g
}

func specRows(s Shell, p catalog.Product) []design.Row {
	rows := []design.Row{}
	if p.Category.Title != "" {
		rows = append(rows, design.Row{Label: s.T("ui.product.spec_category"), Value: p.Category.Title})
	}
	if p.Stack != "" {
		rows = append(rows, design.Row{Label: s.T("ui.product.spec_stack"), Value: p.Stack})
	}
	if len(p.Releases) > 0 {
		rows = append(rows, design.Row{Label: s.T("ui.product.spec_version"), Value: p.Releases[0].Version})
	}
	if !p.PublishedAt.IsZero() {
		rows = append(rows, design.Row{Label: s.T("ui.product.spec_published"), Value: fmtDate(p.PublishedAt)})
	}
	if p.RatingCount > 0 {
		rows = append(rows, design.Row{Label: s.T("ui.product.spec_rating"), Value: strconv.FormatFloat(p.Rating, 'f', 1, 64) + " / 5 (" + strconv.Itoa(p.RatingCount) + ")"})
	}
	if o, ok := offerOf(p, "regular"); ok && o.SupportMonths > 0 {
		rows = append(rows, design.Row{Label: s.T("ui.product.spec_support"), Value: strings.ReplaceAll(s.T("ui.product.months"), "{n}", strconv.Itoa(o.SupportMonths))})
	}
	return rows
}

func updates(s Shell, p catalog.Product) []design.Update {
	out := make([]design.Update, 0, len(p.Releases))
	for _, r := range p.Releases {
		out = append(out, design.Update{Version: r.Version, Date: fmtDate(r.At), Text: or(stripTags(r.Changelog), s.T("ui.product.no_changelog"))})
	}
	return out
}

func files(s Shell, p catalog.Product) []design.FileItem {
	if len(p.Releases) == 0 {
		return nil
	}
	r := p.Releases[0]
	return []design.FileItem{{Icon: "package", Name: p.Slug + "-" + r.Version + ".zip", Text: sizeText(s, r.Size), Kind: "zip"}}
}

func sizeText(s Shell, n int64) string {
	var num, key string
	switch {
	case n >= 1<<20:
		num, key = strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64), "ui.size.mb"
	case n >= 1<<10:
		num, key = strconv.FormatInt(n>>10, 10), "ui.size.kb"
	case n > 0:
		num, key = strconv.FormatInt(n, 10), "ui.size.b"
	default:
		return ""
	}
	if l, _ := moneyLocale.Load().(string); l == "ru" {
		num = strings.Replace(num, ".", ",", 1)
	}
	return strings.ReplaceAll(s.T(key), "{n}", num)
}

func stripTags(h string) string {
	var b strings.Builder
	in := false
	for _, r := range h {
		switch {
		case r == '<':
			in = true
		case r == '>':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}

func stars(n int) string { return strings.Repeat("\u2605", n) + strings.Repeat("\u2606", 5-n) }

func reviewRows(s Shell, rs []editorial.Review) []Row {
	out := make([]Row, 0, len(rs))
	for _, r := range rs {
		row := Row{Title: or(r.AuthorName, s.T("ui.review.buyer")), Meta: stars(r.Rating) + " \u00b7 " + fmtDate(r.Created)}
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

func authorCard(s Shell, a catalog.Seller) design.AuthorCardProps {
	return design.AuthorCardProps{Title: s.T("ui.product.author"), Name: a.DisplayName, Initials: initials(a.DisplayName),
		Role: or(a.Headline, s.T("ui.role.author")), Cta: s.T("ui.home.author_cta"), Href: AuthorHref(a.Slug)}
}

func authorHero(s Shell, a catalog.Seller, ps []catalog.Product) design.AuthorHeroProps {
	h := design.AuthorHeroProps{Name: a.DisplayName, Initials: initials(a.DisplayName), Bio: or(a.Bio, a.Headline),
		Badge: itemsText(s, a.ItemsCount)}
	if !a.JoinedAt.IsZero() {
		h.Tags = append(h.Tags, joinedText(s, a.JoinedAt))
	}
	if a.Headline != "" {
		h.Tags = append(h.Tags, a.Headline)
	}
	for i, p := range ps {
		if i == 3 {
			break
		}
		h.Images = append(h.Images, design.GalleryImage{Src: coverSrc(p.CoverURL), Alt: p.Title})
	}
	return h
}

func checkoutItem(s Shell, v CheckoutPageView) design.CheckoutItemProps {
	meta := []string{v.P.SellerName}
	if v.P.Stack != "" {
		meta = append(meta, v.P.Stack)
	}
	return design.CheckoutItemProps{Title: s.T("ui.checkout.item"), Cover: coverSrc(v.P.CoverURL), Name: v.P.Title, Meta: meta,
		License: s.T("ui.checkout.license"), Licenses: licenses(s, v.P), Value: v.License,
		Compare: s.T("ui.product.compare"), CompareHref: "/pages/licenses"}
}

func orderSummary(s Shell, v CheckoutPageView) design.OrderSummaryProps {
	o, _ := offerOf(v.P, v.License)
	total := Money(o.Price)
	pay := s.T("ui.checkout.pay") + " " + total
	if o.Price.Minor == 0 {
		total, pay = s.T("ui.checkout.free"), s.T("ui.product.get_free")
	}
	return design.OrderSummaryProps{Title: s.T("ui.checkout.summary"),
		Rows: []design.Row{
			{Label: v.P.Title, Value: total},
			{Label: s.T("ui.checkout.license"), Value: licenseLabel(s, o.License)},
			{Label: s.T("ui.checkout.delivery"), Value: s.T("ui.checkout.delivery_value"), Tone: "muted"},
		},
		TotalLabel: s.T("ui.checkout.total"), Total: total, Pay: pay, Note: s.T("ui.checkout.note"),
		Agree: design.OrderSummaryAgree{Start: s.T("ui.checkout.agree_start"), Terms: s.T("ui.checkout.agree_terms"),
			And: s.T("ui.checkout.agree_and"), License: s.T("ui.checkout.agree_license"), TermsHref: "/pages/terms", LicenseHref: "/pages/licenses"},
	}
}

func ratingOptions() []selectfield.SelectOptionItem {
	out := []selectfield.SelectOptionItem{}
	for n := 5; n >= 1; n-- {
		out = append(out, selectfield.SelectOptionItem{Value: strconv.Itoa(n), Label: stars(n)})
	}
	return out
}

func pageHref(v CatalogView, page int) string {
	q := url.Values{}
	if v.Query != "" {
		q.Set("q", v.Query)
	}
	if v.Sort != "" {
		q.Set("sort", v.Sort)
	}
	if v.Min != "" {
		q.Set("min", v.Min)
	}
	if v.Max != "" {
		q.Set("max", v.Max)
	}
	if v.Free {
		q.Set("free", "1")
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	if len(q) == 0 {
		return catalogHref(v)
	}
	return catalogHref(v) + "?" + q.Encode()
}
