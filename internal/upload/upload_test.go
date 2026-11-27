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
	deny := l
	deny.DenyExt = []string{".exe"}
	for name, c := range map[string]struct {
		b    []byte
		l    Limits
		want error
	}{
		"not a zip":          {[]byte("plain text"), l, ErrNotZip},
		"too many entries":   {zipOf(t, many...), l, ErrZipBomb},
		"high ratio":         {zeros, l, ErrZipBomb},
		"over uncompressed":  {zipOf(t, entry{"a.bin", make([]byte, 2<<20)}, entry{"b.bin", make([]byte, 2<<20)}), big, ErrZipBomb},
		"path traversal":     {zipOf(t, entry{"../../etc/passwd", []byte("x")}), l, ErrEntry},
		"absolute path":      {zipOf(t, entry{"/etc/passwd", []byte("x")}), l, ErrEntry},
		"windows drive":      {zipOf(t, entry{`C:\x.txt`, []byte("x")}), l, ErrEntry},
		"denied executable":  {zipOf(t, entry{"setup.EXE", []byte("MZ")}), deny, ErrEntry},
		"flagged executable": {zipOf(t, entry{"setup.EXE", []byte("MZ")}), l, nil},
	} {
		if err := check(c.b, c.l); !errors.Is(err, c.want) || (c.want == nil && err != nil) {
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

// Executables and secrets are flagged, never refused; the listing keeps
// every file with its size.
func TestInspectFlags(t *testing.T) {
	pe := make([]byte, 0x80)
	copy(pe, "MZ")
	pe[0x3c] = 0x40
	copy(pe[0x40:], "PE\x00\x00")
	b := zipOf(t,
		entry{"kit/readme.txt", []byte("MZ is not a PE header")},
		entry{"kit/tool.bin", append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 16)...)},
		entry{"kit/setup.exe", pe},
		entry{"kit/run.sh", []byte("echo hi")},
		entry{"kit/.env", []byte("X=1")},
		entry{"kit/config.js", []byte(`const k = "AKIA` + strings.Repeat("Q", 16) + `"; password = "hunter2hunter2"`)},
		entry{"kit/key.txt", []byte("-----BEGIN RSA " + "PRIVATE KEY-----\nx")},
	)
	rep, err := Inspect(bytes.NewReader(b), int64(len(b)), DefaultLimits)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 7 || len(rep.Entries) != 7 {
		t.Fatalf("entries %+v", rep)
	}
	got := map[string]bool{}
	for _, f := range rep.Flags {
		got[f.Kind+"/"+f.Rule+"/"+f.Path] = true
	}
	for _, want := range []string{
		"binary/elf/kit/tool.bin", "binary/pe/kit/setup.exe", "executable/exe/kit/setup.exe",
		"executable/sh/kit/run.sh", "secret/env-file/kit/.env", "secret/aws-access-key/kit/config.js",
		"secret/password-assignment/kit/config.js", "secret/private-key/kit/key.txt",
	} {
		if !got[want] {
			t.Errorf("missing flag %s in %v", want, rep.Flags)
		}
	}
	if got["binary/pe/kit/readme.txt"] {
		t.Error("text starting with MZ flagged as PE")
	}
}
