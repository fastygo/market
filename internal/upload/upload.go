// Package upload is the seller file intake (ADR-0022 releases, ADR-0036
// vendor uploads on POST /seller/items/{id}/releases). Limits are enforced
// while the file is received, not after:
//
//   - Receive streams one multipart file through http.MaxBytesReader and
//     a hard per-file limit into the caller's writer (temp file or S3
//     multipart), hashing as it goes; nothing is buffered in memory.
//   - CheckZip rejects zip bombs and unsafe entries before the release is
//     stored: entry count, total uncompressed size, compression ratio,
//     decompressed bytes counted for real (headers can lie), path
//     traversal, and executable types only when Limits.DenyExt lists
//     them (the default flags them for the moderator instead, Inspect).
//   - The per-seller quota (bytes and files) is checked in the same write
//     transaction that inserts the release row: pb.Releases.Create.
package upload

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// Limits for one upload.
type Limits struct {
	MaxFileBytes    int64   // one file as uploaded (compressed)
	MaxEntries      int     // files and dirs inside the zip
	MaxUncompressed int64   // sum of all entries after decompression
	MaxRatio        float64 // uncompressed / compressed, per entry and in total
	// DenyExt rejects entries with these extensions; FlagExt only reports
	// them (Inspect) so a moderator decides (ADR-0036).
	DenyExt []string
	FlagExt []string
}

// DefaultLimits suit design files (.fig, .zip with sources).
var DefaultLimits = Limits{
	MaxFileBytes:    512 << 20,
	MaxEntries:      20000,
	MaxUncompressed: 2 << 30,
	MaxRatio:        100,
	FlagExt:         []string{".exe", ".dll", ".msi", ".bat", ".cmd", ".com", ".scr", ".ps1", ".vbs", ".jar", ".apk", ".app", ".sh", ".so", ".dylib", ".bin", ".elf"},
}

// Quota is the per-seller storage allowance.
type Quota struct {
	MaxBytes int64
	MaxFiles int
}

// DefaultQuota per seller.
var DefaultQuota = Quota{MaxBytes: 5 << 30, MaxFiles: 500}

var (
	ErrTooLarge = errors.New("upload: file too large")
	ErrNoFile   = errors.New("upload: no file in the request")
	ErrNotZip   = errors.New("upload: not a zip archive")
	ErrZipBomb  = errors.New("upload: archive expands too much")
	ErrEntry    = errors.New("upload: archive entry not allowed")
	ErrQuota    = errors.New("upload: seller storage quota exceeded")
)

// Received describes a stored file.
type Received struct {
	Name   string // client file name (base name only)
	Size   int64
	SHA256 string
}

// ratioFloor: entries (and archives) expanding to less than this are not
// ratio-checked.
const ratioFloor = 1 << 20

// formOverhead allows the multipart framing and small fields around the file.
const formOverhead = 1 << 20

// Receive reads the multipart field `field` from r and copies the file to
// dst, at most l.MaxFileBytes. The whole body is capped by
// http.MaxBytesReader, so a client cannot stream more than that even
// outside the file part.
func Receive(w http.ResponseWriter, r *http.Request, field string, l Limits, dst io.Writer) (Received, error) {
	r.Body = http.MaxBytesReader(w, r.Body, l.MaxFileBytes+formOverhead)
	mr, err := r.MultipartReader()
	if err != nil {
		return Received{}, fmt.Errorf("upload: %w", err)
	}
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			return Received{}, ErrNoFile
		}
		if err != nil {
			return Received{}, tooLarge(err)
		}
		if part.FormName() != field || part.FileName() == "" {
			_, _ = io.Copy(io.Discard, io.LimitReader(part, formOverhead))
			continue
		}
		h := sha256.New()
		n, err := io.Copy(io.MultiWriter(dst, h), io.LimitReader(part, l.MaxFileBytes+1))
		if err != nil {
			return Received{}, tooLarge(err)
		}
		if n > l.MaxFileBytes {
			return Received{}, ErrTooLarge
		}
		return Received{Name: path.Base(strings.ReplaceAll(part.FileName(), `\`, "/")), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
	}
}

func tooLarge(err error) error {
	var mb *http.MaxBytesError
	if errors.As(err, &mb) {
		return ErrTooLarge
	}
	return fmt.Errorf("upload: %w", err)
}

// CheckZip validates an archive of `size` bytes (Inspect without the
// report).
func CheckZip(ra io.ReaderAt, size int64, l Limits) error {
	_, err := Inspect(ra, size, l)
	return err
}

// Inspect validates an archive of `size` bytes and reports its entries
// and content flags. Declared sizes are checked first (cheap); then every
// entry is decompressed into a counter with a hard limit, because a
// crafted header can declare less than the entry really expands to. The
// same pass looks for executables (extension and ELF, PE or Mach-O magic)
// and secret patterns; those are flags for a moderator, never a refusal.
func Inspect(ra io.ReaderAt, size int64, l Limits) (Report, error) {
	var rep Report
	zr, err := zip.NewReader(ra, size)
	if err != nil {
		return rep, ErrNotZip
	}
	if len(zr.File) > l.MaxEntries {
		return rep, fmt.Errorf("%w: %d entries (max %d)", ErrZipBomb, len(zr.File), l.MaxEntries)
	}
	var declared uint64
	for _, f := range zr.File {
		if err := checkName(f.Name, l); err != nil {
			return rep, err
		}
		declared += f.UncompressedSize64
		if declared > uint64(l.MaxUncompressed) {
			return rep, fmt.Errorf("%w: more than %d bytes uncompressed", ErrZipBomb, l.MaxUncompressed)
		}
		// ratio only matters for big entries: small text files compress
		// well and cannot blow up the disk
		if f.UncompressedSize64 > ratioFloor && f.CompressedSize64 > 0 &&
			float64(f.UncompressedSize64)/float64(f.CompressedSize64) > l.MaxRatio {
			return rep, fmt.Errorf("%w: %s ratio over %.0f", ErrZipBomb, f.Name, l.MaxRatio)
		}
	}
	if declared > ratioFloor && size > 0 && float64(declared)/float64(size) > l.MaxRatio {
		return rep, fmt.Errorf("%w: total ratio over %.0f", ErrZipBomb, l.MaxRatio)
	}
	budget := l.MaxUncompressed
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := strings.ReplaceAll(f.Name, `\`, "/")
		rep.addEntry(name, int64(f.UncompressedSize64))
		rep.flagName(name, l)
		rc, err := f.Open()
		if err != nil {
			return rep, fmt.Errorf("%w: %s: %v", ErrNotZip, f.Name, err)
		}
		// at most what the header declared (+1 to detect a lie) and
		// never more than the remaining total budget
		limit := int64(f.UncompressedSize64) + 1
		if limit > budget+1 {
			limit = budget + 1
		}
		sc := newScanner(int64(f.UncompressedSize64))
		n, err := io.Copy(sc, io.LimitReader(rc, limit))
		_ = rc.Close()
		// archive/zip itself fails with ErrFormat once an entry yields
		// more than its header declares; the count is the second guard
		if n > int64(f.UncompressedSize64) || n > budget || errors.Is(err, zip.ErrFormat) {
			return rep, fmt.Errorf("%w: %s expands beyond its header", ErrZipBomb, f.Name)
		}
		if err != nil {
			return rep, fmt.Errorf("%w: %s: %v", ErrNotZip, f.Name, err)
		}
		sc.report(&rep, name)
		budget -= n
	}
	return rep, nil
}

func checkName(name string, l Limits) error {
	n := strings.ReplaceAll(name, `\`, "/")
	if n == "" || strings.HasPrefix(n, "/") || strings.ContainsRune(n, 0) || (len(n) > 1 && n[1] == ':') {
		return fmt.Errorf("%w: %q", ErrEntry, name)
	}
	for _, seg := range strings.Split(n, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: %q (path traversal)", ErrEntry, name)
		}
	}
	ext := strings.ToLower(path.Ext(n))
	for _, d := range l.DenyExt {
		if ext == d {
			return fmt.Errorf("%w: %q (executable type)", ErrEntry, name)
		}
	}
	return nil
}
