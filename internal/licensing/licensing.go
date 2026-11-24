// Package licensing delivers purchased files (ADR-0009): an account link
// checks the entitlement and the release, issues a short HMAC token for
// /dl/{token}; /dl re-checks both, logs the download and redirects to a
// pre-signed storage URL. The app never streams release files.
package licensing

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fastygo/market/internal/platform/tx"
)

// Errors.
var (
	ErrNoEntitlement = errors.New("licensing: no active entitlement or no published release")
	ErrTokenInvalid  = errors.New("licensing: download token is invalid")
	ErrTokenExpired  = errors.New("licensing: download token expired")
)

// Target is an active entitlement and the release to deliver.
type Target struct {
	EntitlementID, ReleaseID, StorageKey, Version string
}

// Store is implemented by platform/pb.
type Store interface {
	// DownloadTarget: the user's active entitlement for the product and
	// its latest published release.
	DownloadTarget(ctx context.Context, userID, productSlug string) (Target, error)
	// Recheck: entitlement still active and release still published.
	Recheck(ctx context.Context, entitlementID, releaseID string) (Target, error)
	LogDownload(ctx context.Context, t Target, ipHash, uaHash string, at time.Time) error
}

// Presigner returns a short-lived storage URL for a key.
type Presigner interface {
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// Signer issues and verifies /dl tokens.
type Signer struct {
	Key []byte
	TTL time.Duration // default 10 min
}

func (s Signer) mac(payload string) string {
	h := hmac.New(sha256.New, s.Key)
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Sign returns a token for an entitlement and release.
func (s Signer) Sign(t Target, now time.Time) string {
	ttl := s.TTL
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	payload := "v1." + t.EntitlementID + "." + t.ReleaseID + "." + strconv.FormatInt(now.Add(ttl).Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.mac(payload)
}

// Verify checks a token and returns entitlement and release ids.
func (s Signer) Verify(token string, now time.Time) (entitlementID, releaseID string, err error) {
	b64, sig, ok := strings.Cut(token, ".")
	if !ok || len(s.Key) == 0 {
		return "", "", ErrTokenInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(b64)
	if err != nil {
		return "", "", ErrTokenInvalid
	}
	payload := string(raw)
	if !hmac.Equal([]byte(sig), []byte(s.mac(payload))) {
		return "", "", ErrTokenInvalid
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", "", ErrTokenInvalid
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return "", "", ErrTokenInvalid
	}
	if now.Unix() > exp {
		return "", "", ErrTokenExpired
	}
	return parts[1], parts[2], nil
}

// Service is the download use case.
type Service struct {
	Store     Store
	Signer    Signer
	Presigner Presigner
	URLTTL    time.Duration // pre-signed URL lifetime, default 5 min
	Now       func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Link returns the /dl/{token} path for a signed-in user's product.
func (s *Service) Link(ctx context.Context, userID, productSlug string) (string, error) {
	t, err := s.Store.DownloadTarget(ctx, userID, productSlug)
	if err != nil {
		return "", err
	}
	return "/dl/" + s.Signer.Sign(t, s.now()), nil
}

// Resolve re-checks a token, logs the download and returns the pre-signed
// URL (no transaction is open during presigning).
func (s *Service) Resolve(ctx context.Context, token, ipHash, uaHash string) (string, error) {
	ent, rel, err := s.Signer.Verify(token, s.now())
	if err != nil {
		return "", err
	}
	t, err := s.Store.Recheck(ctx, ent, rel)
	if err != nil {
		return "", err
	}
	if err := s.Store.LogDownload(ctx, t, ipHash, uaHash, s.now()); err != nil {
		return "", err
	}
	ttl := s.URLTTL
	if ttl == 0 {
		ttl = 5 * time.Minute
	}
	return s.Presigner.PresignGet(ctx, t.StorageKey, ttl)
}

// LocalPresigner is the development and test Presigner: it signs URLs on a
// local base with HMAC instead of S3 credentials. Nothing serves them; it
// exists so the redirect path runs without storage.
type LocalPresigner struct {
	BaseURL string
	Key     []byte
	Now     func() time.Time
}

// PresignGet implements Presigner.
func (p LocalPresigner) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := tx.AssertNoTx(ctx); err != nil {
		return "", err
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	exp := strconv.FormatInt(now().Add(ttl).Unix(), 10)
	h := hmac.New(sha256.New, p.Key)
	h.Write([]byte(key + "\n" + exp))
	sig := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("%s/%s?expires=%s&signature=%s", strings.TrimRight(p.BaseURL, "/"), (&url.URL{Path: key}).EscapedPath(), exp, sig), nil
}

// LocalFiles serves LocalPresigner URLs from a directory (single-host
// deployments and previews without S3). It checks the signature and the
// expiry; the path is cleaned and confined to Dir.
type LocalFiles struct {
	Dir string
	Key []byte
	Now func() time.Time
}

// ServeHTTP implements http.Handler for GET /files/{key...}.
func (f LocalFiles) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(path.Clean("/"+r.PathValue("key")), "/")
	exp := r.URL.Query().Get("expires")
	h := hmac.New(sha256.New, f.Key)
	h.Write([]byte(key + "\n" + exp))
	want := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	n, err := strconv.ParseInt(exp, 10, 64)
	now := time.Now
	if f.Now != nil {
		now = f.Now
	}
	if key == "" || len(f.Key) == 0 || err != nil || !hmac.Equal([]byte(r.URL.Query().Get("signature")), []byte(want)) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if now().Unix() > n {
		http.Error(w, "link expired", http.StatusGone)
		return
	}
	full := filepath.Join(f.Dir, filepath.FromSlash(key))
	if rel, err := filepath.Rel(f.Dir, full); err != nil || strings.HasPrefix(rel, "..") {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	st, err := os.Stat(full)
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", `attachment; filename="`+strings.NewReplacer(`"`, "_", "\\", "_").Replace(path.Base(key))+`"`)
	http.ServeFile(w, r, full)
}
