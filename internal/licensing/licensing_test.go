package licensing

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	s := Signer{Key: []byte("k")}
	now := time.Unix(1_800_000_000, 0)
	tok := s.Sign(Target{EntitlementID: "e1", ReleaseID: "r1"}, now)
	e, r, err := s.Verify(tok, now.Add(9*time.Minute))
	if err != nil || e != "e1" || r != "r1" {
		t.Fatal(e, r, err)
	}
	if _, _, err := s.Verify(tok, now.Add(11*time.Minute)); !errors.Is(err, ErrTokenExpired) {
		t.Fatal("expired token accepted")
	}
	if _, _, err := (Signer{Key: []byte("other")}).Verify(tok, now); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("foreign key accepted")
	}
	b64, sig, _ := strings.Cut(tok, ".")
	if _, _, err := s.Verify(b64+"x."+sig, now); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("tampered token accepted")
	}
	if _, _, err := (Signer{}).Verify(tok, now); !errors.Is(err, ErrTokenInvalid) {
		t.Fatal("empty key must reject")
	}
}

func TestLocalPresigner(t *testing.T) {
	p := LocalPresigner{BaseURL: "http://127.0.0.1:9000/dev/", Key: []byte("k"), Now: func() time.Time { return time.Unix(100, 0) }}
	u, err := p.PresignGet(context.Background(), "placeholder/a b.zip", time.Minute)
	if err != nil || !strings.HasPrefix(u, "http://127.0.0.1:9000/dev/placeholder/a%20b.zip?expires=160&signature=") {
		t.Fatal(u, err)
	}
}
