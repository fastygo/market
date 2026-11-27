package screens

import (
	"strings"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/ui/design"
)

// Viewer is the signed-in user as the page frame shows it (ADR-0035):
// the header account menu and the mobile menu list one entry per role.
type Viewer struct {
	Signed                          bool
	Email, Name, CSRF               string
	Vendor, Author, Partner, Editor bool
	SellerName                      string
}

// DisplayName is the name, else the mailbox part of the email.
func (v Viewer) DisplayName() string {
	if v.Name != "" {
		return v.Name
	}
	return strings.SplitN(v.Email, "@", 2)[0]
}

func initials(name string) string { return initial(name) }

// categoryIcons are cycled over the categories in sort order.
var categoryIcons = []string{"monitor", "panels-top-left", "pen-tool", "layout-grid", "square-terminal", "box"}

func navCategories(cats []catalog.Category) []design.NavItem {
	out := make([]design.NavItem, 0, len(cats))
	for i, c := range cats {
		out = append(out, design.NavItem{ID: c.Slug, Label: c.Title, Href: CategoryHref(c.Slug), Icon: categoryIcons[i%len(categoryIcons)]})
	}
	return out
}

func siteBrand(s Shell) design.Brand {
	return design.Brand{Name: s.Brand, Logo: "/static/art/brand.svg", Tagline: s.Tagline(), Copyright: s.copyright()}
}

func siteNav(s Shell) []design.NavItem {
	return []design.NavItem{
		{ID: "catalog", Label: s.T("ui.nav.catalog"), Href: "/catalog", Menu: true},
		{ID: "collections", Label: s.T("ui.nav.collections"), Href: "/collections"},
		{ID: "blog", Label: s.T("ui.nav.blog"), Href: "/blog"},
		{ID: "sell", Label: s.T("ui.nav.sell"), Href: "/pages/sell"},
	}
}

func menuLinks(s Shell) []design.MenuLink {
	v := s.Viewer
	out := []design.MenuLink{
		{Label: s.T("ui.menu.purchases"), Icon: "package", Href: "/account/purchases"},
		{Label: s.T("ui.menu.profile"), Icon: "user", Href: "/account/profile"},
	}
	if v.Vendor {
		out = append(out, design.MenuLink{Label: s.T("ui.menu.seller"), Icon: "store", Href: "/seller"})
	}
	if v.Author || v.Vendor || v.Editor {
		out = append(out, design.MenuLink{Label: s.T("ui.menu.articles"), Icon: "newspaper", Href: "/studio/articles"})
	}
	if v.Partner {
		out = append(out, design.MenuLink{Label: s.T("ui.menu.partner"), Icon: "handshake", Href: "/partner"})
	}
	if v.Editor {
		out = append(out, design.MenuLink{Label: s.T("ui.menu.editor"), Icon: "shield-check", Href: "/editor"})
	}
	if !v.Vendor || !v.Partner || !v.Author {
		out = append(out, design.MenuLink{Label: s.T("ui.menu.roles"), Icon: "badge-check", Href: "/account/roles"})
	}
	return out
}

func viewerUser(s Shell) design.User {
	v := s.Viewer
	role := s.T("ui.role.buyer")
	switch {
	case v.Editor:
		role = s.T("ui.role.editor")
	case v.Vendor:
		role = v.SellerName
	case v.Partner:
		role = s.T("ui.role.partner")
	case v.Author:
		role = s.T("ui.role.author")
	}
	return design.User{Name: v.DisplayName(), Initials: initials(v.DisplayName()), Role: role}
}

func actor(s Shell) string {
	if !s.Viewer.Signed {
		return "guest"
	}
	if s.Viewer.Vendor {
		return "vendor"
	}
	return "buyer"
}

func headerProps(s Shell) design.SiteHeaderProps {
	return design.SiteHeaderProps{
		Brand: siteBrand(s), HomeLabel: s.T("ui.a11y.home"), Nav: siteNav(s), NavLabel: s.T("ui.site.nav"),
		Categories: navCategories(s.Categories), Actor: actor(s), User: viewerUser(s), MenuLinks: menuLinks(s),
		AccountOpen: true, CatalogOpen: true, Theme: "light", SearchHref: "/catalog",
		Labels: design.SiteHeaderLabels{Search: s.T("ui.header.search"), Signin: s.T("ui.header.signin"), Menu: s.T("ui.header.menu"),
			Account: s.T("ui.header.account"), Signout: s.T("ui.header.signout"), ThemeDark: s.T("ui.header.theme_dark"),
			ThemeLight: s.T("ui.header.theme_light")},
	}
}

func mobileProps(s Shell) design.MobileMenuProps {
	return design.MobileMenuProps{
		Brand: siteBrand(s), Nav: siteNav(s), Categories: navCategories(s.Categories), SectionNav: nil, Current: s.Current,
		Actor: actor(s), User: viewerUser(s), MenuLinks: menuLinks(s), Theme: "light",
		Labels: design.MobileMenuLabels{Menu: s.T("ui.header.menu"), Close: s.T("ui.header.close"), Catalog: s.T("ui.nav.catalog"),
			Signin: s.T("ui.header.signin"), Signout: s.T("ui.header.signout"), ThemeDark: s.T("ui.header.theme_dark"),
			ThemeLight: s.T("ui.header.theme_light"), Section: s.T("ui.a11y.account_nav")},
	}
}

func footerProps(s Shell) design.SiteFooterProps {
	var catLinks []design.LinkItem
	for i, c := range s.Categories {
		if i == 3 {
			break
		}
		catLinks = append(catLinks, design.LinkItem{Label: c.Title, Href: CategoryHref(c.Slug)})
	}
	catLinks = append(catLinks, design.LinkItem{Label: s.T("ui.catalog.all"), Href: "/catalog"})
	return design.SiteFooterProps{
		Brand: siteBrand(s), HomeLabel: s.T("ui.a11y.home"), Label: s.T("ui.a11y.footer_nav"),
		Columns: []design.FooterColumn{
			{Title: s.T("ui.footer.catalog"), Links: catLinks},
			{Title: s.T("ui.footer.buyers"), Links: []design.LinkItem{
				{Label: s.T("ui.footer.help"), Href: "/pages/help"},
				{Label: s.T("ui.footer.licenses"), Href: "/pages/licenses"},
				{Label: s.T("ui.footer.refund"), Href: "/pages/refund"},
				{Label: s.T("ui.footer.blog"), Href: "/blog"},
			}},
			{Title: s.T("ui.footer.sellers"), Links: []design.LinkItem{
				{Label: s.T("ui.footer.sell"), Href: "/pages/sell"},
				{Label: s.T("ui.footer.rules"), Href: "/pages/rules"},
				{Label: s.T("ui.footer.partners"), Href: "/account/roles"},
				{Label: s.T("ui.footer.contacts"), Href: "/pages/contacts"},
			}},
		},
		Legal: []design.LinkItem{
			{Label: s.T("ui.footer.privacy"), Href: "/pages/privacy"},
			{Label: s.T("ui.footer.terms"), Href: "/pages/terms"},
			{Label: s.T("ui.footer.offer"), Href: "/pages/offer"},
		},
	}
}

func sellerFooterProps(s Shell) design.SellerFooterProps {
	return design.SellerFooterProps{Brand: siteBrand(s), Label: s.T("ui.a11y.footer_nav"), Links: []design.LinkItem{
		{Label: s.T("ui.footer.seller_help"), Href: "/pages/sell"},
		{Label: s.T("ui.footer.rules"), Href: "/pages/rules"},
	}}
}

// Section is the cabinet section of a page and the current entry.
type Section struct {
	Kind    string // account | seller | studio | partner | editor
	Current string
}

func sectionNav(s Shell, sec Section) (string, []design.NavItem, []design.NavItem) {
	n := func(id, icon, href string) design.NavItem {
		return design.NavItem{ID: id, Label: s.T("ui.nav." + sec.Kind + "." + id), Icon: icon, Href: href}
	}
	switch sec.Kind {
	case "seller":
		return s.T("ui.section.seller"), []design.NavItem{
			n("today", "clock", "/seller"), n("items", "package", "/seller/items"), n("media", "image", "/seller/media"),
			n("journal", "book-open", "/studio/articles"), n("finance", "wallet", "/seller/finance"),
			n("reviews", "message-square", "/seller/reviews"), n("team", "users", "/seller/team"),
		}, []design.NavItem{n("store", "store", "/seller/store")}
	case "studio":
		return s.T("ui.section.studio"), []design.NavItem{
			n("articles", "newspaper", "/studio/articles"), n("new", "plus", "/studio/articles/new"),
		}, []design.NavItem{n("blog", "book-open", "/blog")}
	case "partner":
		return s.T("ui.section.partner"), []design.NavItem{
			n("overview", "chart-column", "/partner"), n("payouts", "wallet", "/partner/payouts"),
		}, []design.NavItem{n("rules", "file-text", "/pages/rules")}
	case "editor":
		return s.T("ui.section.editor"), []design.NavItem{
			n("queue", "inbox", "/editor"), n("products", "package", "/editor/products"), n("releases", "archive", "/editor/releases"),
			n("articles", "newspaper", "/editor/articles"),
			n("reviews", "message-square", "/editor/reviews"), n("applications", "badge-check", "/editor/applications"),
			n("pages", "scale", "/editor/pages"), n("collections", "layers", "/editor/collections"),
		}, []design.NavItem{n("payouts", "wallet", "/editor/payouts")}
	}
	return s.T("ui.section.account"), []design.NavItem{
		n("purchases", "package", "/account/purchases"), n("saved", "bookmark", "/account/saved"),
		n("profile", "user", "/account/profile"), n("security", "key-round", "/account/security"),
		n("roles", "badge-check", "/account/roles"),
	}, nil
}

func sellerSidebar(s Shell, sec Section) design.SellerSidebarProps {
	title, items, settings := sectionNav(s, sec)
	u := viewerUser(s)
	if sec.Kind == "seller" && s.Viewer.SellerName != "" {
		u = design.User{Name: s.Viewer.SellerName, Initials: initials(s.Viewer.SellerName), Role: s.T("ui.role.seller_cabinet")}
	}
	return design.SellerSidebarProps{User: u, Title: title, Items: items, Settings: settings, Current: sec.Current,
		Label: s.T("ui.a11y.section_nav")}
}

func accountSidebar(s Shell, sec Section) design.AccountSidebarProps {
	_, items, _ := sectionNav(s, sec)
	u := viewerUser(s)
	u.Role = s.Viewer.Email
	return design.AccountSidebarProps{User: u, Items: items, Current: sec.Current, Label: s.T("ui.a11y.account_nav")}
}
