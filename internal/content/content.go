// Package content turns seller Markdown into sanitized HTML for the
// storefront (ADR-0031): goldmark in the EditorY subset, then a
// bluemonday allowlist. The Markdown is the source of truth; the HTML
// is derived in the same write transaction and may be sanitized again
// on read.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// MaxMarkdown is the UTF-8 byte cap per locale (owner t373s1450, ADR-0031).
const MaxMarkdown = 200 * 1024

// Errors.
var (
	ErrTooLarge = errors.New("content: markdown exceeds 200 KiB")
	ErrInvalid  = errors.New("content: markdown is not valid UTF-8")
)

var frontmatter = regexp.MustCompile(`(?s)^---\r?\n.*?\r?\n---\r?\n?`)

// StripFrontmatter drops a leading YAML document (EditorY SEO panel).
// Title, excerpt, slug and social fields live on the product row.
func StripFrontmatter(markdown string) string {
	s := strings.TrimPrefix(strings.ReplaceAll(markdown, "\r\n", "\n"), "\ufeff")
	if loc := frontmatter.FindStringIndex(s); loc != nil && loc[0] == 0 {
		return strings.TrimLeft(s[loc[1]:], "\n")
	}
	return s
}

// Render strips frontmatter, enforces the cap, converts Markdown and
// sanitizes the HTML. An empty body yields an empty string.
func Render(markdown string) (string, error) {
	body, err := Prepare(markdown)
	if err != nil {
		return "", err
	}
	if body == "" {
		return "", nil
	}
	var buf bytes.Buffer
	if err := markdownHTML().Convert([]byte(body), &buf); err != nil {
		return "", fmt.Errorf("content: render: %w", err)
	}
	return Sanitize(buf.String()), nil
}

// Prepare strips frontmatter and checks the cap and encoding.
func Prepare(markdown string) (string, error) {
	body := StripFrontmatter(markdown)
	if !utf8.ValidString(body) {
		return "", ErrInvalid
	}
	if len(body) > MaxMarkdown {
		return "", ErrTooLarge
	}
	return body, nil
}

func markdownHTML() goldmark.Markdown {
	return goldmark.New(
		goldmark.WithExtensions(extension.Strikethrough),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe(), html.WithXHTML()),
	)
}

// Sanitize applies the storefront allowlist (safe to call on already
// stored HTML).
func Sanitize(html string) string {
	return policy().Sanitize(html)
}

func policy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote",
		"ul", "ol", "li", "pre", "code", "hr", "br", "strong", "em", "s", "del",
		"figure", "figcaption", "img", "cite", "a")
	p.RequireParseableURLs(true)
	p.AllowRelativeURLs(true)
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(false)
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).OnElements("figure", "pre", "code")
	p.AllowAttrs("data-layout", "data-caption", "data-credit").OnElements("figure")
	p.AllowAttrs("alt", "title").OnElements("img")
	p.AllowAttrs("src").Matching(safeSrc).OnElements("img")
	p.AllowAttrs("title").OnElements("a")
	p.AllowAttrs("href").Matching(safeHref).OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^(noopener|noreferrer|noopener noreferrer)$`)).OnElements("a")
	p.AddTargetBlankToFullyQualifiedLinks(false)
	return p
}

// First-party image URLs only, so the storefront CSP can stay img-src 'self'.
var safeSrc = regexp.MustCompile(`^/(?:api/files|static)/[A-Za-z0-9._/~-]+$`)

// Links: http, https, mailto, or a same-origin path.
var safeHref = regexp.MustCompile(`^(?:https://[^\s]+|http://[^\s]+|mailto:[^\s]+|/[\w./?#&=+-]*)$`)
