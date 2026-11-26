package media

import (
	"bytes"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/disintegration/imaging"
)

func TestDeriveIdempotentAndSrcset(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	srcPath := filepath.Join(t.TempDir(), "cover.png")
	writePNG(t, srcPath, 900, 600)
	src := Source{ProductID: "abcde0123456789", Name: "cover.png", Path: srcPath}
	if err := s.Derive(src); err != nil {
		t.Fatal(err)
	}
	ss := s.Srcset(src.ProductID, "cover.png")
	if !strings.Contains(ss, "400w") || !strings.Contains(ss, "800w") || strings.Contains(ss, "1200w") {
		t.Fatalf("srcset = %q, want 400 and 800 only (source is 900px)", ss)
	}
	if err := s.Derive(src); err != nil {
		t.Fatal(err)
	}
	ss2 := s.Srcset(src.ProductID, "cover.png")
	if ss != ss2 {
		t.Fatalf("second derive changed srcset:\n%s\n%s", ss, ss2)
	}
	// a different filename is stale until re-derived
	if s.Srcset(src.ProductID, "other.png") != "" {
		t.Fatal("srcset must be empty when the source name does not match meta")
	}
}

func TestHandlerImmutable(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	srcPath := filepath.Join(t.TempDir(), "cover.png")
	writePNG(t, srcPath, 400, 300)
	src := Source{ProductID: "abcde0123456789", Name: "cover.png", Path: srcPath}
	if err := s.Derive(src); err != nil {
		t.Fatal(err)
	}
	ss := s.Srcset(src.ProductID, "cover.png")
	path := strings.Fields(ss)[0] // first URL
	mux := http.NewServeMux()
	mux.Handle("GET /media/covers/{id}/{hash}/{file}", s.Handler())
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != 200 {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/webp" {
		t.Fatalf("content-type %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache-control %q", cc)
	}
	if rec.Body.Len() < 16 {
		t.Fatal("empty body")
	}
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/media/covers/../../../etc/passwd", nil))
	if rec.Code == 200 {
		t.Fatalf("traversal served a file: %d", rec.Code)
	}
}

func TestResizeNoUpscale(t *testing.T) {
	img := imaging.New(400, 300, color.NRGBA{R: 20, G: 40, B: 60, A: 255})
	out := imaging.Resize(img, 400, 0, imaging.Lanczos)
	if out.Bounds().Dx() != 400 {
		t.Fatalf("width %d", out.Bounds().Dx())
	}
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := imaging.New(w, h, color.NRGBA{R: 200, G: 80, B: 40, A: 255})
	// a little structure so compression is realistic
	for x := 0; x < w; x += 17 {
		img.Set(x, h/2, color.NRGBA{A: 255, B: 200})
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
