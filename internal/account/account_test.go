package account_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fastygo/market/internal/account"
)

type users struct{ u account.User }

func (s *users) ByEmail(context.Context, string) (account.User, error) { return s.u, nil }
func (s *users) ByID(_ context.Context, id string) (account.User, error) {
	if id != s.u.ID {
		return account.User{}, account.ErrNotFound
	}
	return s.u, nil
}
func (s *users) Ensure(context.Context, string) (account.User, bool, error) { return s.u, false, nil }
func (s *users) CheckPassword(context.Context, string, string) (account.User, error) {
	return s.u, nil
}
func (s *users) SessionToken(context.Context, string) (string, error)      { return "t", nil }
func (s *users) FromSession(context.Context, string) (account.User, error) { return s.u, nil }
func (s *users) MarkVerified(context.Context, string) error                { return nil }

func TestMagicLink(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	m := account.Magic{Key: []byte("k"), Now: func() time.Time { return now }}
	st := &users{u: account.User{ID: "u1", Email: "a@example.com", TokenKey: "tk1"}}
	tok := m.Sign(st.u, time.Hour, "/account/orders/M-2026-000001")
	u, next, err := m.Verify(context.Background(), tok, st)
	if err != nil || u.ID != "u1" || next != "/account/orders/M-2026-000001" {
		t.Fatalf("%v %q %v", u, next, err)
	}
	if _, next, _ := m.Verify(context.Background(), m.Sign(st.u, time.Hour, "https://evil.example.com"), st); next != "/account/orders" {
		t.Fatalf("open redirect: %q", next)
	}
	st.u.TokenKey = "rotated"
	if _, _, err := m.Verify(context.Background(), tok, st); !errors.Is(err, account.ErrLinkInvalid) {
		t.Fatalf("rotated key: %v", err)
	}
	st.u.TokenKey = "tk1"
	now = now.Add(2 * time.Hour)
	if _, _, err := m.Verify(context.Background(), tok, st); !errors.Is(err, account.ErrLinkExpired) {
		t.Fatalf("expired: %v", err)
	}
	if _, _, err := m.Verify(context.Background(), tok+"x", st); !errors.Is(err, account.ErrLinkInvalid) {
		t.Fatalf("tampered: %v", err)
	}
}

func TestNormalizeEmail(t *testing.T) {
	if e, err := account.NormalizeEmail("  Buyer@Example.COM "); err != nil || e != "buyer@example.com" {
		t.Fatal(e, err)
	}
	for _, bad := range []string{"", "a", "a@b", "@example.com", "a b@example.com", "a@example.com,b@example.com"} {
		if _, err := account.NormalizeEmail(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestSafeNext(t *testing.T) {
	const fallback = "/account/orders"
	ok := []struct{ in, want string }{
		{"/account", "/account"},
		{"/account/orders", "/account/orders"},
		{"/account/orders/M-2026-000001", "/account/orders/M-2026-000001"},
		{"/account/items/abc", "/account/items/abc"},
		{"/account//orders", "/account/orders"},
		{"/account/orders?next=1", "/account/orders?next=1"},
	}
	for _, c := range ok {
		if got := account.SafeNext(c.in); got != c.want {
			t.Errorf("SafeNext(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	bad := []string{
		"", "https://evil.example.com", "http://evil.example.com", "//evil.example",
		"/account/../catalog", "/account/../../etc/passwd", "/catalog",
		"/accountfoo", "account/orders", "javascript:alert(1)",
		"/account/orders" + "\r\n" + "Location: https://evil.example",
		`/account/\evil.example`,
	}
	for _, in := range bad {
		if got := account.SafeNext(in); got != fallback {
			t.Errorf("SafeNext(%q) = %q, want fallback", in, got)
		}
	}
}
