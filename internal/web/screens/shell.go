// Package screens holds the app-layer templ pages of the storefront
// (UI8Kit layer "apps"): built only from UI8Kit primitives and blocks
// installed in ui/kit, texts from the dictionary or records.
package screens

import (
	"strconv"
	"strings"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/storefront"
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
	// BrandInfo is the storefront brand config (ADR-0020): tagline,
	// short name, alternate names, og image.
	BrandInfo storefront.Brand
	// BaseURL is the public origin (no trailing slash); empty in
	// development, then canonical, og:url and absolute JSON-LD ids are
	// omitted.
	BaseURL string
	Path    string // request path, for the canonical link
	// Description overrides the meta description (default ui.meta.description).
	Description string
	// JSONLD is the schema.org graph of the page (nil: none).
	JSONLD any
}

// T resolves a dictionary key.
func (s Shell) T(key string) string { return s.Dict.T(key) }

func (s Shell) pageTitle() string {
	if s.Title == "" || s.Title == s.Brand {
		if s.BrandInfo.Tagline != "" {
			return s.Brand + " | " + s.BrandInfo.Tagline
		}
		return s.Brand
	}
	return s.Title + " | " + s.Brand
}

// Tagline is the brand tagline (data, ADR-0020).
func (s Shell) Tagline() string { return s.BrandInfo.Tagline }

func (s Shell) metaDescription() string {
	if s.Description != "" {
		return s.Description
	}
	return s.T("ui.meta.description")
}

// abs is an absolute URL on the public origin, or "" without one.
func (s Shell) abs(path string) string {
	if s.BaseURL == "" {
		return ""
	}
	return s.BaseURL + path
}

func (s Shell) canonical() string { return s.abs(s.Path) }

func (s Shell) ogImage() string {
	img := s.BrandInfo.OGImage
	if img == "" || strings.HasPrefix(img, "https://") || strings.HasPrefix(img, "http://") {
		return img
	}
	return s.abs(img)
}

// SiteJSONLD is the schema.org WebSite and Organization graph of the home
// page, read from the brand config (ADR-0020).
func SiteJSONLD(s Shell) map[string]any {
	org := map[string]any{"@type": "Organization", "name": s.Brand}
	site := map[string]any{"@type": "WebSite", "name": s.Brand}
	if alt := s.BrandInfo.AlternateNames; len(alt) > 0 {
		site["alternateName"], org["alternateName"] = alt, alt
	}
	if s.BrandInfo.Tagline != "" {
		org["slogan"] = s.BrandInfo.Tagline
	}
	if s.BaseURL != "" {
		org["@id"], org["url"] = s.BaseURL+"/#organization", s.BaseURL+"/"
		site["@id"], site["url"] = s.BaseURL+"/#website", s.BaseURL+"/"
		site["publisher"] = map[string]any{"@id": s.BaseURL + "/#organization"}
		site["potentialAction"] = map[string]any{
			"@type": "SearchAction", "target": s.BaseURL + "/catalog?q={search_term_string}",
			"query-input": "required name=search_term_string",
		}
		if s.BrandInfo.Logo != "" {
			org["logo"] = s.abs(s.BrandInfo.Logo)
		}
	}
	return map[string]any{"@context": "https://schema.org", "@graph": []any{site, org}}
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
