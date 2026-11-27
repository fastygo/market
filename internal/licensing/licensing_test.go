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

type oneTarget struct {
	t    Target
	logs int
}

func (s *oneTarget) DownloadTarget(context.Context, string, string) (Target, error) { return s.t, nil }
func (s *oneTarget) Recheck(context.Context, string, string) (Target, error)        { return s.t, nil }
func (s *oneTarget) LogDownload(context.Context, Target, string, string, time.Time) error {
	s.logs++
	return nil
}

type checkedPresigner struct{ present map[string]bool }

func (p checkedPresigner) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	return "https://s3.example/" + key, nil
}
func (p checkedPresigner) Exists(_ context.Context, key string) (bool, error) {
	return p.present[key], nil
}

// TestResolveMissingFile: a published release whose object is not in
// storage resolves to ErrFileMissing and logs no download.
func TestResolveMissingFile(t *testing.T) {
	ctx := context.Background()
	for _, present := range []bool{false, true} {
		st := &oneTarget{t: Target{EntitlementID: "e1", ReleaseID: "r1", StorageKey: "products/x/1.0.0/x.zip"}}
		s := &Service{Store: st, Signer: Signer{Key: []byte("k")}, Presigner: checkedPresigner{present: map[string]bool{"products/x/1.0.0/x.zip": present}}}
		link, err := s.Link(ctx, "u1", "x")
		if err != nil {
			t.Fatal(err)
		}
		u, err := s.Resolve(ctx, strings.TrimPrefix(link, "/dl/"), "", "")
		if present && (err != nil || u == "" || st.logs != 1) {
			t.Fatalf("present: %q %v logs %d", u, err, st.logs)
		}
		if !present && (!errors.Is(err, ErrFileMissing) || st.logs != 0) {
			t.Fatalf("missing: %v logs %d", err, st.logs)
		}
	}
}
