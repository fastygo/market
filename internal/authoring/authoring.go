// Package authoring is the seller item write side for the cabinet
// (ADR-0028, ADR-0031). Storefront reads stay in catalog.
package authoring

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"
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
	Meta                              Meta
	CoverURL, ReviewNote              string
	Images                            []Image
	Updated                           time.Time
}

// Meta is the product card data edited in the cabinet (ADR-0035). Prices
// are minor units in Currency; zero Extended hides that license.
type Meta struct {
	Title, Slug, Excerpt, Category, DemoURL string
	Stack, Tags, Currency                   string
	Price, Extended                         int64
}

// Category is a selectable product category.
type Category struct{ ID, Title string }

// Status actions of the seller.
const (
	ActSubmit   = "submit"   // draft -> in_review
	ActWithdraw = "withdraw" // in_review -> draft
	ActUnlist   = "unlist"   // published -> unlisted
	ActRelist   = "relist"   // unlisted -> in_review
	ActArchive  = "archive"  // any -> archived
)

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
	// Create makes a draft product of the seller (the user must be a member).
	Create(ctx context.Context, userID, sellerID string, m Meta) (Item, error)
	SaveMeta(ctx context.Context, userID, productID string, m Meta) (Item, error)
	SetCover(ctx context.Context, userID, productID, filename string, r io.Reader) (Image, error)
	Act(ctx context.Context, userID, productID, action string) (Item, error)
	Categories(ctx context.Context) ([]Category, error)
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

// Release is a versioned file of an item (ADR-0022). Drafts are private;
// a published release is what buyers download and cannot be changed.
type Release struct {
	ID, Version, Status, Changelog, Name string
	Size                                 int64
	Created                              time.Time
}

// NewRelease is a received, checked file to attach to an item.
type NewRelease struct {
	Version, Changelog, StorageKey, SHA256 string
	Size                                   int64
}

// Releases is the seller side of release files; every call checks that the
// user is a member of the item's store.
type Releases interface {
	ItemReleases(ctx context.Context, userID, itemID string) ([]Release, error)
	AddRelease(ctx context.Context, userID, itemID string, in NewRelease) (Release, error)
	PublishRelease(ctx context.Context, userID, itemID, releaseID string) error
	// ReleaseOwner returns the store of an item the user may edit.
	ReleaseOwner(ctx context.Context, userID, itemID string) (sellerID string, err error)
}
