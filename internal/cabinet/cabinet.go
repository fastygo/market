// Package cabinet is the role model of the signed-in cabinet (ADR-0035):
// one user may be a buyer, an editor, an author, a vendor (member of one
// or more sellers) and a partner at once. Access is decided here and in
// platform/pb; PocketBase API rules stay nil on every collection.
package cabinet

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Role is a cabinet role. Every signed-in user is a buyer.
type Role string

// Roles (ADR-0035).
const (
	Buyer   Role = "buyer"
	Editor  Role = "editor"
	Author  Role = "author"
	Vendor  Role = "vendor"
	Partner Role = "partner"
)

// Applyable roles are granted by an editor after an application; editor is
// granted from the CLI only (market role grant).
var Applyable = []Role{Author, Vendor, Partner}

// Errors.
var (
	ErrNotFound  = errors.New("cabinet: not found")
	ErrDenied    = errors.New("cabinet: denied")
	ErrInvalid   = errors.New("cabinet: invalid input")
	ErrDuplicate = errors.New("cabinet: already exists")
	ErrConflict  = errors.New("cabinet: version conflict")
)

// SellerRef is one seller the user belongs to (seller_members).
type SellerRef struct {
	ID, Slug, Name, Status string
	Owner                  bool
}

// PartnerRef is the user's partner account.
type PartnerRef struct {
	ID, Code, Status string
	RateBPS          int
}

// Principal is the signed-in user with every role they hold.
type Principal struct {
	UserID, Email, Name string
	Editor, Author      bool
	Sellers             []SellerRef
	Partner             *PartnerRef
}

// Has reports whether the user holds a role.
func (p Principal) Has(r Role) bool {
	switch r {
	case Buyer:
		return p.UserID != ""
	case Editor:
		return p.Editor
	case Author:
		return p.Author || p.Editor || len(p.Sellers) > 0
	case Vendor:
		return len(p.Sellers) > 0
	case Partner:
		return p.Partner != nil && p.Partner.Status == "active"
	}
	return false
}

// Roles lists the held roles in cabinet order.
func (p Principal) Roles() []Role {
	var out []Role
	for _, r := range []Role{Buyer, Vendor, Author, Partner, Editor} {
		if p.Has(r) {
			out = append(out, r)
		}
	}
	return out
}

// Seller is the active seller of a vendor (the first one; a seller switch
// can come later).
func (p Principal) Seller() (SellerRef, bool) {
	if len(p.Sellers) == 0 {
		return SellerRef{}, false
	}
	return p.Sellers[0], true
}

// Application asks for a role.
type Application struct {
	ID, UserID, Email, Role, Status string
	Message, StoreName, StoreSlug   string
	Website, DecisionNote           string
	Created, DecidedAt              time.Time
}

// Payout is a payout request (frontend and data only: no money moves).
type Payout struct {
	ID, PartyType, SellerID, PartnerID, Status string
	Amount                                     int64
	Currency, Period, Reference, Note          string
	Created                                    time.Time
}

// Balance is a seller or partner earnings summary in minor units.
type Balance struct {
	Currency                     string
	Gross, Earned, Pending, Paid int64
	Available                    int64
	Orders, Clicks               int
	ThisMonth, LastMonth         int64
}

// Sale is one sold order line of a seller.
type Sale struct {
	OrderNumber, Title, License, Status, Currency string
	Line, Commission                              int64
	At                                            time.Time
}

// Member is one seller team member.
type Member struct {
	UserID, Email, Name, Role string
}

// Store is implemented by platform/pb.
type Store interface {
	Principal(ctx context.Context, userID string) (Principal, error)
	SetName(ctx context.Context, userID, name string) error
	SetPassword(ctx context.Context, userID, password string) error

	Apply(ctx context.Context, a Application) error
	MyApplications(ctx context.Context, userID string) ([]Application, error)
	Applications(ctx context.Context, status string) ([]Application, error)
	Decide(ctx context.Context, id string, approve bool, by, note string) error
	Grant(ctx context.Context, email string, r Role, by string) error

	Balance(ctx context.Context, partyType, id string) (Balance, error)
	Payouts(ctx context.Context, partyType, id string) ([]Payout, error)
	RequestPayout(ctx context.Context, p Payout, userID string) error
	Sales(ctx context.Context, sellerID string, limit int) ([]Sale, error)

	Team(ctx context.Context, sellerID string) ([]Member, error)
	AddMember(ctx context.Context, sellerID, email string) error
	RemoveMember(ctx context.Context, sellerID, userID string) error
	StoreProfile(ctx context.Context, sellerID string) (StoreProfile, error)
	SaveStore(ctx context.Context, sellerID string, s StoreProfile) error

	PartnerByCode(ctx context.Context, code string) (PartnerRef, string, error)
	RecordClick(ctx context.Context, partnerID, landing, ipHash string) error
	SavePartner(ctx context.Context, partnerID, payoutMethod string) error
	PartnerOrders(ctx context.Context, partnerID string, limit int) ([]Sale, error)
}

// StoreProfile is the public seller profile edited by the vendor.
type StoreProfile struct {
	Slug, DisplayName, Headline, Bio, Website, Status string
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

// ValidSlug: lowercase latin, digits and dashes, 3..64 chars.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }

// Slugify makes a slug from a latin title; other letters are dropped.
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 64 {
		out = strings.Trim(out[:64], "-")
	}
	return out
}

// MinPassword is the cabinet password minimum length.
const MinPassword = 10

// RolePath is the cabinet home of a role.
func RolePath(r Role) string {
	switch r {
	case Vendor:
		return "/seller"
	case Author:
		return "/studio/articles"
	case Partner:
		return "/partner"
	case Editor:
		return "/editor"
	}
	return "/account/purchases"
}
