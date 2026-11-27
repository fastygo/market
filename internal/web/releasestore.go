package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fastygo/market/internal/platform/s3"
)

// ErrObjectExists: the storage key is taken; a release file is never
// overwritten.
var ErrObjectExists = errors.New("release storage: object exists")

// ReleaseStore keeps release archives (ADR-0036): local files under
// MARKET_FILES_DIR or S3 under MARKET_S3_PREFIX. Keys are relative and
// already safe (releaseKey).
type ReleaseStore interface {
	// Put stores size bytes of f (hex SHA-256 sum) under key and fails
	// with ErrObjectExists when the key is taken.
	Put(ctx context.Context, key string, f *os.File, size int64, sum string) error
	Delete(ctx context.Context, key string) error
	// Serve sends the object to a moderator: the file itself or a short
	// pre-signed redirect.
	Serve(w http.ResponseWriter, r *http.Request, key string)
	Mode() string
}

// LocalReleaseStore keeps release files in Dir (served to buyers on
// /files with signed links, licensing.LocalFiles).
type LocalReleaseStore struct{ Dir string }

func (l LocalReleaseStore) path(key string) (string, error) {
	if _, err := s3.ObjectKey("", key); err != nil {
		return "", err
	}
	return filepath.Join(l.Dir, filepath.FromSlash(strings.Trim(key, "/"))), nil
}

// Mode implements ReleaseStore.
func (l LocalReleaseStore) Mode() string { return "local" }

// Put implements ReleaseStore: a hard link when the temp file is on the
// same file system (no copy), otherwise an exclusive create and copy.
func (l LocalReleaseStore) Put(_ context.Context, key string, f *os.File, size int64, _ string) error {
	dst, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	if err := os.Link(f.Name(), dst); err == nil {
		return nil
	} else if errors.Is(err, os.ErrExist) {
		return ErrObjectExists
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if errors.Is(err, os.ErrExist) {
		return ErrObjectExists
	}
	if err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	n, err := io.Copy(out, f)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n != size {
		err = fmt.Errorf("release storage: wrote %d of %d bytes", n, size)
	}
	if err != nil {
		_ = os.Remove(dst)
	}
	return err
}

// Delete implements ReleaseStore.
func (l LocalReleaseStore) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Serve implements ReleaseStore.
func (l LocalReleaseStore) Serve(w http.ResponseWriter, r *http.Request, key string) {
	p, err := l.path(key)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachment(path.Base(key)))
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// ObjectStore is the S3 client the store needs (s3.Presigner).
type ObjectStore interface {
	Exists(ctx context.Context, key string) (bool, error)
	Put(ctx context.Context, key string, body io.Reader, size int64, sum, contentType, disposition string) error
	Delete(ctx context.Context, key string) error
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// S3ReleaseStore keeps release files in S3 (Prefix + key). The access key
// needs s3:PutObject, s3:DeleteObject, s3:GetObject (HEAD) on the prefix.
type S3ReleaseStore struct {
	S3 ObjectStore
	// PutTimeout bounds one upload to the storage (default 30 min).
	PutTimeout time.Duration
}

// Mode implements ReleaseStore.
func (s S3ReleaseStore) Mode() string { return "s3" }

// Put implements ReleaseStore: HEAD first (never overwrite), then one
// signed PUT streamed from the temp file.
func (s S3ReleaseStore) Put(ctx context.Context, key string, f *os.File, size int64, sum string) error {
	t := s.PutTimeout
	if t <= 0 {
		t = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), t)
	defer cancel()
	ok, err := s.S3.Exists(ctx, key)
	if err != nil {
		return err
	}
	if ok {
		return ErrObjectExists
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	return s.S3.Put(ctx, key, io.LimitReader(f, size), size, sum, "application/zip", attachment(path.Base(key)))
}

// Delete implements ReleaseStore.
func (s S3ReleaseStore) Delete(ctx context.Context, key string) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	return s.S3.Delete(ctx, key)
}

// Serve implements ReleaseStore: a 2-minute pre-signed GET.
func (s S3ReleaseStore) Serve(w http.ResponseWriter, r *http.Request, key string) {
	u, err := s.S3.PresignGet(r.Context(), key, 2*time.Minute)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func attachment(name string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": name})
}
