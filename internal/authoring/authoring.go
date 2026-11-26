// Package authoring is the seller item write side for the cabinet
// (ADR-0028, ADR-0031). Storefront reads stay in catalog.
package authoring

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
)

// Errors.
var (
	ErrNotFound = errors.New("authoring: not found")
	ErrDenied   = errors.New("authoring: not a member of this seller")
	ErrConflict = errors.New("authoring: version conflict")
	ErrTooLarge = errors.New("authoring: markdown exceeds the cap")
	ErrInvalid  = errors.New("authoring: invalid content")
	ErrBadImage = errors.New("authoring: image type not allowed")
	ErrQuota    = errors.New("authoring: image quota exceeded")
)

// Item is one product the seller can edit.
type Item struct {
	ID, SellerID, Title, Slug, Status string
	Description, DescriptionHTML      string
	Version                           int
}

// Image is a file stored on the product and served at a public URL.
type Image struct {
	URL, Name string
}

// Image limits (ADR-0031): 5 MiB per file, 50 files or 100 MiB per product.
const (
	MaxImages          = 50
	MaxImageBytes      = 5 << 20
	MaxImageBytesTotal = 100 << 20
)

// Store is implemented by platform/pb.
type Store interface {
	// SellerMember reports whether the user belongs to at least one seller.
	SellerMember(ctx context.Context, userID string) (bool, error)
	List(ctx context.Context, userID string) ([]Item, error)
	Get(ctx context.Context, userID, productID string) (Item, error)
	// SaveDescription writes Markdown and the derived HTML in one
	// transaction. version is the If-Match value the client last saw.
	SaveDescription(ctx context.Context, userID, productID, markdown string, version int) (Item, error)
	AddImage(ctx context.Context, userID, productID, filename string, r io.Reader) (Image, error)
}

// ETag is the document version as an HTTP ETag / If-Match token.
func ETag(version int) string {
	return `"v` + strconv.Itoa(version) + `"`
}

// ParseETag accepts `"vN"`, `vN`, or `N`.
func ParseETag(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if len(s) > 0 && (s[0] == 'v' || s[0] == 'V') {
		s = s[1:]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 1_000_000_000 {
		return 0, false
	}
	return n, true
}
