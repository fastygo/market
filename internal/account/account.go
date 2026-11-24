// Package account is the buyer account core (ADR-0027): the Users port
// over the PocketBase users collection and the signed magic sign-in link.
// The cabinet is separate from the PocketBase admin (/_/), which stays for
// superusers only.
package account

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/fastygo/market/internal/platform/money"
)

// User is a buyer account.
type User struct {
	ID, Email string
	// TokenKey is PocketBase's per-user token key; signing links with it
	// makes a password change or a "sign out everywhere" revoke them.
	TokenKey string
	Verified bool
}

// Errors.
var (
	ErrNotFound      = errors.New("account: user not found")
	ErrCredentials   = errors.New("account: wrong email or password")
	ErrLinkInvalid   = errors.New("account: sign-in link is invalid")
	ErrLinkExpired   = errors.New("account: sign-in link expired")
	ErrInvalidEmail  = errors.New("account: invalid email")
	ErrOrderNotFound = errors.New("account: order not found")
)

// Order is one order in the buyer cabinet.
type Order struct {
	ID, Number, Status string
	Total              money.Money
	Created            time.Time
	Items              []Item
}

// Item is an order line with its entitlement status.
type Item struct {
	Title, Slug, License string
	Entitlement          string // active, refunded, disputed, revoked; "" before payment
}

// Users is implemented by platform/pb over the users auth collection.
type Users interface {
	ByEmail(ctx context.Context, email string) (User, error)
	ByID(ctx context.Context, id string) (User, error)
	// Ensure returns the user with this email, creating an unverified
	// account with a random password (guest checkout).
	Ensure(ctx context.Context, email string) (User, bool, error)
	CheckPassword(ctx context.Context, email, password string) (User, error)
	// SessionToken issues a PocketBase auth token (the cookie value).
	SessionToken(ctx context.Context, userID string) (string, error)
	FromSession(ctx context.Context, token string) (User, error)
	// MarkVerified: a magic link proved the mailbox.
	MarkVerified(ctx context.Context, userID string) error
}

// NormalizeEmail lowercases and trims; it rejects obviously bad input.
func NormalizeEmail(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	at := strings.LastIndexByte(s, '@')
	if at < 1 || at == len(s)-1 || len(s) > 254 || strings.ContainsAny(s, " \t\r\n,;<>\"") || !strings.Contains(s[at:], ".") {
		return "", ErrInvalidEmail
	}
	return s, nil
}

// Magic signs and verifies sign-in links: v1|user|expiry|next, MAC over
// the payload and the user's token key.
type Magic struct {
	Key []byte
	Now func() time.Time
}

func (m Magic) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m Magic) mac(payload, tokenKey string) string {
	h := hmac.New(sha256.New, m.Key)
	h.Write([]byte(payload + "|" + tokenKey))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// SafeNext keeps redirects inside the cabinet.
func SafeNext(next string) string {
	if strings.HasPrefix(next, "/account") && !strings.HasPrefix(next, "//") && !strings.ContainsAny(next, "\\\r\n|") {
		return next
	}
	return "/account/orders"
}

// Sign returns the token for a user, valid for ttl, landing on next.
func (m Magic) Sign(u User, ttl time.Duration, next string) string {
	payload := "v1|" + u.ID + "|" + strconv.FormatInt(m.now().Add(ttl).Unix(), 10) + "|" + SafeNext(next)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + m.mac(payload, u.TokenKey)
}

// Verify checks a token; lookup loads the user by id.
func (m Magic) Verify(ctx context.Context, token string, users Users) (User, string, error) {
	b64, sig, ok := strings.Cut(token, ".")
	if !ok || len(m.Key) == 0 {
		return User{}, "", ErrLinkInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return User{}, "", ErrLinkInvalid
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 || parts[0] != "v1" {
		return User{}, "", ErrLinkInvalid
	}
	u, err := users.ByID(ctx, parts[1])
	if err != nil {
		return User{}, "", ErrLinkInvalid
	}
	if !hmac.Equal([]byte(sig), []byte(m.mac(string(raw), u.TokenKey))) {
		return User{}, "", ErrLinkInvalid
	}
	exp, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return User{}, "", ErrLinkInvalid
	}
	if m.now().Unix() > exp {
		return User{}, "", ErrLinkExpired
	}
	return u, SafeNext(parts[3]), nil
}
