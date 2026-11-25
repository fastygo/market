package upload

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type entry struct {
	name string
	data []byte
}

func zipOf(t *testing.T, es ...entry) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	for _, e := range es {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(e.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func check(b []byte, l Limits) error { return CheckZip(bytes.NewReader(b), int64(len(b)), l) }

func TestCheckZip(t *testing.T) {
	l := DefaultLimits
	l.MaxUncompressed = 3 << 20
	l.MaxEntries = 10
	ok := zipOf(t, entry{"kit/readme.txt", []byte("hello")}, entry{"kit/design.fig", bytes.Repeat([]byte("ab9"), 1000)})
	if err := check(ok, l); err != nil {
		t.Fatalf("valid archive: %v", err)
	}
	many := make([]entry, 11)
	for i := range many {
		many[i] = entry{name: strings.Repeat("a", i+1) + ".txt"}
	}
	zeros := zipOf(t, entry{"zeros.bin", make([]byte, 2<<20)}) // ~1000:1, over the 1 MiB ratio floor
	big := l
	big.MaxRatio = 1e9
	for name, c := range map[string]struct {
		b    []byte
		l    Limits
		want error
	}{
		"not a zip":         {[]byte("plain text"), l, ErrNotZip},
		"too many entries":  {zipOf(t, many...), l, ErrZipBomb},
		"high ratio":        {zeros, l, ErrZipBomb},
		"over uncompressed": {zipOf(t, entry{"a.bin", make([]byte, 2<<20)}, entry{"b.bin", make([]byte, 2<<20)}), big, ErrZipBomb},
		"path traversal":    {zipOf(t, entry{"../../etc/passwd", []byte("x")}), l, ErrEntry},
		"absolute path":     {zipOf(t, entry{"/etc/passwd", []byte("x")}), l, ErrEntry},
		"windows drive":     {zipOf(t, entry{`C:\x.txt`, []byte("x")}), l, ErrEntry},
		"executable":        {zipOf(t, entry{"setup.EXE", []byte("MZ")}), l, ErrEntry},
	} {
		if err := check(c.b, c.l); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

// A header that declares less than the entry really expands to is
// rejected (archive/zip's own size check plus our byte count).
func TestCheckZipLyingHeader(t *testing.T) {
	real := bytes.Repeat([]byte("abcdefgh"), 4096) // 32 KiB
	var comp bytes.Buffer
	fw := zip.NewWriter(&comp)
	ew, _ := fw.Create("x")
	_, _ = ew.Write(real)
	_ = fw.Close()
	zr, _ := zip.NewReader(bytes.NewReader(comp.Bytes()), int64(comp.Len()))
	src := zr.File[0]
	raw, _ := src.OpenRaw()
	data, _ := io.ReadAll(raw)

	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "liar.bin", Method: zip.Deflate, CRC32: src.CRC32,
		CompressedSize64: uint64(len(data)), UncompressedSize64: 10})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write(data)
	_ = zw.Close()
	l := DefaultLimits
	if err := check(b.Bytes(), l); !errors.Is(err, ErrZipBomb) {
		t.Fatalf("lying header: %v", err)
	}
}

func multipartBody(t *testing.T, field, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("version", "1.0.0")
	fw, err := mw.CreateFormFile(field, name)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fw.Write(data)
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func TestReceive(t *testing.T) {
	l := DefaultLimits
	l.MaxFileBytes = 1000
	body, ct := multipartBody(t, "file", `C:\Users\me\kit.zip`, bytes.Repeat([]byte("x"), 1000))
	r := httptest.NewRequest(http.MethodPost, "/upload", body)
	r.Header.Set("Content-Type", ct)
	var dst bytes.Buffer
	got, err := Receive(httptest.NewRecorder(), r, "file", l, &dst)
	if err != nil || got.Size != 1000 || dst.Len() != 1000 || got.Name != "kit.zip" || len(got.SHA256) != 64 {
		t.Fatalf("receive: %+v %v", got, err)
	}

	body, ct = multipartBody(t, "file", "kit.zip", bytes.Repeat([]byte("x"), 1001))
	r = httptest.NewRequest(http.MethodPost, "/upload", body)
	r.Header.Set("Content-Type", ct)
	dst.Reset()
	if _, err := Receive(httptest.NewRecorder(), r, "file", l, &dst); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if dst.Len() > 1001 {
		t.Fatalf("wrote %d bytes past the limit", dst.Len())
	}

	body, ct = multipartBody(t, "other", "kit.zip", []byte("x"))
	r = httptest.NewRequest(http.MethodPost, "/upload", body)
	r.Header.Set("Content-Type", ct)
	if _, err := Receive(httptest.NewRecorder(), r, "file", l, io.Discard); !errors.Is(err, ErrNoFile) {
		t.Fatalf("missing field: %v", err)
	}
}
