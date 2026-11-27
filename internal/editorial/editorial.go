// Package editorial is the content side of the cabinet (ADR-0035):
// articles written by authors and vendors and moderated by editors, the
// legal and info pages edited by editors, curated collections, product
// reviews and product moderation.
package editorial

import (
	"context"
	"errors"
	"time"
)

// Errors.
var (
	ErrNotFound = errors.New("editorial: not found")
	ErrDenied   = errors.New("editorial: denied")
	ErrInvalid  = errors.New("editorial: invalid input")
	ErrConflict = errors.New("editorial: version conflict")
)

// Article statuses.
const (
	Draft     = "draft"
	InReview  = "in_review"
	Published = "published"
	Rejected  = "rejected"
	Archived  = "archived"
)

// Article is a blog post, guide or news item.
type Article struct {
	ID, Slug, Title, Excerpt, Body, BodyHTML string
	Kind, Status, ReviewNote                 string
	AuthorID, AuthorName, SellerID           string
	CoverURL                                 string
	Version                                  int
	PublishedAt, Updated                     time.Time
}

// Page is a legal or info page.
type Page struct {
	ID, Slug, Title, Body, BodyHTML, Kind, Version, Status string
	Updated                                                time.Time
}

// Collection is a curated product list.
type Collection struct {
	ID, Slug, Title, Eyebrow, Lead, Cta, Tone, Status, ImageURL string
	ProductIDs                                                  []string
	Sort                                                        int
}

// Review is a buyer review of a product.
type Review struct {
	ID, ProductID, ProductTitle, ProductSlug, UserID, AuthorName string
	Rating                                                       int
	Body, Status, Reply                                          string
	Created                                                      time.Time
}

// ProductReview is a product waiting for moderation.
type ProductReview struct {
	ID, Slug, Title, SellerName, Status, ReviewNote string
	Updated                                         time.Time
}

// Counts summarises the editor queues.
type Counts struct {
	Products, Articles, Reviews, Applications int
}

// Store is implemented by platform/pb.
type Store interface {
	PublishedArticles(ctx context.Context, limit int) ([]Article, error)
	Article(ctx context.Context, slug string) (Article, error)
	MyArticles(ctx context.Context, userID string) ([]Article, error)
	GetArticle(ctx context.Context, id string) (Article, error)
	SaveArticle(ctx context.Context, a Article, userID string, submit bool) (Article, error)
	Articles(ctx context.Context, status string) ([]Article, error)
	ModerateArticle(ctx context.Context, id, status, note string) error

	Page(ctx context.Context, slug string) (Page, error)
	Pages(ctx context.Context) ([]Page, error)
	SavePage(ctx context.Context, p Page, by string) error

	PublishedCollections(ctx context.Context) ([]Collection, error)
	Collection(ctx context.Context, slug string) (Collection, error)
	Collections(ctx context.Context) ([]Collection, error)
	SaveCollection(ctx context.Context, c Collection) error

	ProductReviews(ctx context.Context, productID string) ([]Review, error)
	AddReview(ctx context.Context, r Review) error
	Reviews(ctx context.Context, status string) ([]Review, error)
	ModerateReview(ctx context.Context, id, status, reply string) error

	ProductQueue(ctx context.Context) ([]ProductReview, error)
	ModerateProduct(ctx context.Context, id, status, note string) error
	Counts(ctx context.Context) (Counts, error)

	Messages(ctx context.Context, locale string) (map[string]string, error)
	ImportMessages(ctx context.Context, locale string, m map[string]string) (int, error)
}
