// Package media stores resized WebP derivatives of product covers next to
// the original PocketBase file (which is never overwritten). Variants are
// addressed by a content hash so a new cover gets a new URL and the old
// one can stay cached as immutable.
package media

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/HugoSmits86/nativewebp"
	_ "golang.org/x/image/webp"
)

// Widths is the srcset ladder (CSS pixels; the browser picks a density).
var Widths = []int{400, 800, 1200}

// CardSizes is the sizes attribute for catalog and grid cards
// (lg: 4 columns in a max-w-6xl row, sm: 2 columns, else full width).
const CardSizes = "(min-width: 1024px) 256px, (min-width: 640px) 45vw, 92vw"

// HeroSizes is the sizes attribute for the product-page cover.
const HeroSizes = "(min-width: 768px) 32rem, 92vw"

const metaName = "meta.json"

// Source is one original cover file on disk.
type Source struct {
	ProductID string
	Name      string // original filename (png/jpeg/webp)
	Path      string
}

type metaFile struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
	Widths []int  `json:"widths"`
}

// Store is a directory of per-product WebP variants.
type Store struct {
	Dir string
	mu  sync.Mutex
}

// NewStore uses dir (created on first write). A nil or empty store serves no variants.
func NewStore(dir string) *Store {
	if dir == "" {
		return nil
	}
	return &Store{Dir: dir}
}

// Srcset is the WebP srcset for a product cover, or "" if no current variants exist.
func (s *Store) Srcset(productID, sourceName string) string {
	if s == nil || productID == "" || sourceName == "" {
		return ""
	}
	m, err := s.readMeta(productID)
	if err != nil || m.Source != sourceName || m.SHA256 == "" {
		return ""
	}
	var parts []string
	for _, w := range m.Widths {
		parts = append(parts, fmt.Sprintf("%s %dw", VariantURL(productID, m.SHA256, w), w))
	}
	return strings.Join(parts, ", ")
}

// VariantURL is the public path of one width.
func VariantURL(productID, hash string, width int) string {
	return "/media/covers/" + productID + "/" + hash + "/w" + strconv.Itoa(width) + ".webp"
}

func (s *Store) readMeta(productID string) (metaFile, error) {
	var m metaFile
	b, err := os.ReadFile(filepath.Join(s.Dir, productID, metaName))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// Derive writes WebP variants for src when they are missing or stale.
// It is idempotent and never changes the original file.
func (s *Store) Derive(src Source) error {
	if s == nil {
		return nil
	}
	if src.ProductID == "" || src.Path == "" {
		return nil
	}
	sum, err := hashFile(src.Path)
	if err != nil {
		return fmt.Errorf("media: hash %s: %w", src.Path, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, err := s.readMeta(src.ProductID); err == nil && m.SHA256 == sum && m.Source == src.Name && filesExist(s.Dir, src.ProductID, sum, m.Widths) {
		return nil
	}
	img, err := openImage(src.Path)
	if err != nil {
		return fmt.Errorf("media: open %s: %w", src.Path, err)
	}
	srcW := img.Bounds().Dx()
	dir := filepath.Join(s.Dir, src.ProductID, sum)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var made []int
	for _, w := range Widths {
		if srcW < w && w != Widths[0] {
			continue
		}
		if srcW < w {
			// narrower than the smallest ladder step: write one variant at native width
			if err := writeWebP(filepath.Join(dir, "w"+strconv.Itoa(srcW)+".webp"), img); err != nil {
				return err
			}
			made = append(made, srcW)
			break
		}
		out := img
		if srcW > w {
			out = resizeWidth(img, w)
		}
		if err := writeWebP(filepath.Join(dir, "w"+strconv.Itoa(w)+".webp"), out); err != nil {
			return err
		}
		made = append(made, w)
	}
	m := metaFile{Source: src.Name, SHA256: sum, Widths: made}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, src.ProductID, metaName), append(b, '\n'), 0o644)
}

func filesExist(root, productID, hash string, widths []int) bool {
	if len(widths) == 0 {
		return false
	}
	for _, w := range widths {
		if _, err := os.Stat(filepath.Join(root, productID, hash, "w"+strconv.Itoa(w)+".webp")); err != nil {
			return false
		}
	}
	return true
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)[:6]), nil
}

func writeWebP(path string, img image.Image) error {
	var buf bytes.Buffer
	if err := encodeWebP(&buf, img); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// encodeWebP prefers lossy cwebp (quality 80) when the CLI is on PATH, and
// falls back to a pure-Go lossless encoder so the binary stays CGO-free.
func encodeWebP(w io.Writer, img image.Image) error {
	if cwebp, err := exec.LookPath("cwebp"); err == nil {
		if err := encodeCWebP(cwebp, w, img); err == nil {
			return nil
		}
	}
	return nativewebp.Encode(w, img, &nativewebp.Options{CompressionLevel: nativewebp.BestCompression})
}

func encodeCWebP(bin string, w io.Writer, img image.Image) error {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return err
	}
	cmd := exec.Command(bin, "-quiet", "-q", "80", "-o", "-", "--", "-")
	cmd.Stdin = &pngBuf
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cwebp: %w: %s", err, stderr.String())
	}
	if out.Len() < 16 {
		return fmt.Errorf("cwebp: empty output")
	}
	_, err := w.Write(out.Bytes())
	return err
}

// Handler serves GET /media/covers/{id}/{hash}/{file} with a long immutable cache.
func (s *Store) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s == nil {
			http.NotFound(w, r)
			return
		}
		id, hash, file := r.PathValue("id"), r.PathValue("hash"), r.PathValue("file")
		if !safeID(id) || !safeHash(hash) {
			http.NotFound(w, r)
			return
		}
		width, ok := parseVariantFile(file)
		if !ok {
			http.NotFound(w, r)
			return
		}
		p := filepath.Join(s.Dir, id, hash, "w"+strconv.Itoa(width)+".webp")
		b, err := os.ReadFile(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	})
}

func parseVariantFile(file string) (int, bool) {
	if !strings.HasPrefix(file, "w") || !strings.HasSuffix(file, ".webp") {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(file, "w"), ".webp"))
	if err != nil || n < 16 || n > 4000 {
		return 0, false
	}
	return n, true
}

func safeID(s string) bool {
	if n := len(s); n < 10 || n > 20 {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

func safeHash(s string) bool {
	if len(s) != 12 {
		return false
	}
	for _, r := range s {
		if r >= 'a' && r <= 'f' || r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}
