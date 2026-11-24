// Package screens holds the app-layer templ pages of the storefront
// (UI8Kit layer "apps"): built only from UI8Kit primitives and blocks
// installed in ui/kit, texts from the dictionary or records.
package screens

import (
	"strconv"
	"strings"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
)

// Shell is the data every page needs: dictionary, brand and navigation.
type Shell struct {
	Dict       *i18n.Dict
	Lang       string
	Brand      string
	Year       int
	Title      string
	Current    string // "home" | "catalog"
	CSSHref    string
	Categories []catalog.Category
}

// T resolves a dictionary key.
func (s Shell) T(key string) string { return s.Dict.T(key) }

func (s Shell) pageTitle() string {
	if s.Title == "" || s.Title == s.Brand {
		return s.Brand
	}
	return s.Title + " | " + s.Brand
}

func (s Shell) copyright() string {
	return "\u00a9 " + strconv.Itoa(s.Year) + " " + s.Brand + ". " + s.T("ui.site.rights")
}

func current(on bool) string {
	if on {
		return "page"
	}
	return ""
}

// CategoryHref is the catalog URL of a category.
func CategoryHref(slug string) string { return "/catalog/" + slug }

// ProductHref is the URL of a product page.
func ProductHref(slug string) string { return "/products/" + slug }

// cards maps products to the UI8Kit product-card data shape.
func cards(ps []catalog.Product) []any {
	out := make([]any, 0, len(ps))
	for _, p := range ps {
		out = append(out, map[string]any{
			"name": p.Title, "href": ProductHref(p.Slug), "image": p.CoverURL,
			"category": p.Category.Title, "summary": p.Excerpt,
			"price": float64(p.Price.Minor), "currency": p.Price.Currency, "inStock": p.HasOffer,
		})
	}
	return out
}

func cardLabels(s Shell) map[string]any {
	return map[string]any{
		"add": s.T("ui.card.add"), "soldOut": s.T("ui.card.sold_out"), "was": s.T("ui.card.was"),
		"ratingLabel": s.T("ui.card.rating"), "reviews": s.T("ui.card.reviews"), "empty": s.T("ui.catalog.empty"),
	}
}

// paragraphs splits plain text on blank lines (product descriptions are
// plain text in phase 0; rich content comes as Codex blocks later).
func paragraphs(s string) []string {
	var out []string
	for _, p := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
