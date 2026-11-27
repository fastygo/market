// Package screens holds the app-layer templ pages of the storefront
// (UI8Kit layer "apps"): built only from UI8Kit primitives and blocks
// installed in ui/kit, texts from the dictionary or records.
package screens

import (
	"github.com/a-h/templ"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/fastygo/market/internal/catalog"
	"github.com/fastygo/market/internal/i18n"
	"github.com/fastygo/market/internal/media"
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
	// CSPNonce is the per-request script nonce (empty in tests that
	// skip ServeHTTP).
	CSPNonce string
	// EditorCSS and EditorJS are hashed island assets; empty on pages
	// that do not mount the editor (ADR-0031).
	EditorCSS, EditorJS string
	// AppJS is the hashed progressive-enhancement script.
	AppJS string
	// EditorProps is the JSON blob for the island (#editory-props).
	EditorProps any
	// Image overrides og:image (product cover); empty uses the brand image.
	Image string
	// FontPreloads are fingerprinted Istok Web URLs for <link rel="preload">.
	FontPreloads []string
	// LCPImage, LCPSrcset and LCPSizes preload the first visible cover.
	LCPImage  string
	LCPSrcset string
	LCPSizes  string
	// Viewer is the signed-in user (zero for guests); Workspace switches
	// to the seller footer; Flash is a one-line form result.
	Viewer     Viewer
	Workspace  bool
	Flash      string
	FlashError bool
	Toast      string
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
	img := s.Image
	if img == "" {
		img = s.BrandInfo.OGImage
	}
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

// AuthorHref is the public profile URL of a seller (ADR-0020).
func AuthorHref(slug string) string { return "/authors/" + slug }

// tpl fills {0} in a dictionary message.
func tpl(msg string, v string) string { return strings.ReplaceAll(msg, "{0}", v) }

func itemsText(s Shell, n int) string {
	if n == 1 {
		return s.T("ui.author.items_one")
	}
	return tpl(s.T("ui.author.items"), strconv.Itoa(n))
}

func joinedText(s Shell, t time.Time) string {
	return tpl(s.T("ui.author.joined"), fmtDate(t))
}

func gridLabel(s Shell, title string) string {
	if title != "" {
		return title
	}
	return s.T("ui.catalog.title")
}

// initial is the first letter of a display name for the avatar fallback.
func initial(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

func avatarClass(size string) string {
	if size == "lg" {
		return "size-24 text-4xl"
	}
	return "size-12 text-xl"
}

func avatarPx(size string) string {
	if size == "lg" {
		return "96"
	}
	return "48"
}

// filterHref keeps the search query when switching categories.
func filterHref(base, q string) string {
	if q == "" {
		return base
	}
	return base + "?q=" + url.QueryEscape(q)
}

func coverSizes(kind string) string {
	if kind == "hero" {
		return media.HeroSizes
	}
	return media.CardSizes
}

const coverPlaceholder = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCA0MDAgMzAwIiBwcmVzZXJ2ZUFzcGVjdFJhdGlvPSJ4TWlkWU1pZCBzbGljZSI+PHJlY3Qgd2lkdGg9IjQwMCIgaGVpZ2h0PSIzMDAiIGZpbGw9IiNlMmU4ZjAiLz48Y2lyY2xlIGN4PSIyOTYiIGN5PSI5NiIgcj0iMzAiIGZpbGw9IiNjYmQ1ZTEiLz48cGF0aCBkPSJNMCAzMDBWMjE0bDk2LTc2IDg4IDcwIDY0LTQ4IDE1MiA5NnY0NHoiIGZpbGw9IiNjYmQ1ZTEiLz48cGF0aCBkPSJNMCAzMDB2LTUybDEyMC02MCAxMTAgNjIgNjAtMzAgMTEwIDgwdjB6IiBmaWxsPSIjOTRhM2I4IiBvcGFjaXR5PSIwLjU1Ii8+PC9zdmc+"

func coverSrc(src string) string {
	if src == "" {
		return coverPlaceholder
	}
	return src
}

func coverAttrs(fetch string) templ.Attributes {
	if fetch == "" {
		return nil
	}
	return templ.Attributes{"fetchpriority": fetch}
}
