package upload

import (
	"bytes"
	"path"
	"regexp"
	"strings"
)

// Flag kinds reported by Inspect. Flags never reject an upload: they are
// hints for the release moderator (ADR-0036).
const (
	FlagExecutable = "executable" // extension in Limits.FlagExt
	FlagBinary     = "binary"     // ELF, PE or Mach-O magic bytes
	FlagSecret     = "secret"     // a secret-looking pattern or file
)

// Flag is one finding inside an archive.
type Flag struct {
	Kind string `json:"kind"`
	Rule string `json:"rule"`
	Path string `json:"path"`
}

// Entry is one file of an archive (directories are skipped).
type Entry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Report is what Inspect found. Entries and Flags are capped (MaxListed,
// MaxFlags); Files and Bytes count everything.
type Report struct {
	Entries []Entry `json:"entries"`
	Flags   []Flag  `json:"flags"`
	Files   int     `json:"files"`
	Bytes   int64   `json:"bytes"`
}

// Caps of a Report and of the content scan.
const (
	MaxListed = 500
	MaxFlags  = 100
	// scanBytes is how much of each entry is searched for secrets; the
	// rest is only counted. Keeps memory per upload bounded.
	scanBytes = 2 << 20
)

func (r *Report) addEntry(name string, size int64) {
	r.Files++
	r.Bytes += size
	if len(r.Entries) < MaxListed {
		r.Entries = append(r.Entries, Entry{Name: name, Size: size})
	}
}

func (r *Report) flag(kind, rule, p string) {
	for _, f := range r.Flags {
		if f.Kind == kind && f.Rule == rule && f.Path == p {
			return
		}
	}
	if len(r.Flags) < MaxFlags {
		r.Flags = append(r.Flags, Flag{Kind: kind, Rule: rule, Path: p})
	}
}

// secretFiles are file names that usually hold credentials.
var secretFiles = map[string]string{
	".env": "env-file", ".npmrc": "npmrc", ".netrc": "netrc", ".pgpass": "pgpass",
	"id_rsa": "ssh-key", "id_ed25519": "ssh-key", "id_ecdsa": "ssh-key", "id_dsa": "ssh-key",
	"credentials.json": "credentials-file", ".htpasswd": "htpasswd",
}

func (r *Report) flagName(name string, l Limits) {
	base := path.Base(name)
	ext := strings.ToLower(path.Ext(base))
	for _, e := range l.FlagExt {
		if ext == e {
			r.flag(FlagExecutable, strings.TrimPrefix(ext, "."), name)
			break
		}
	}
	lb := strings.ToLower(base)
	if rule, ok := secretFiles[lb]; ok {
		r.flag(FlagSecret, rule, name)
	} else if strings.HasPrefix(lb, ".env.") && !strings.HasSuffix(lb, ".example") && !strings.HasSuffix(lb, ".sample") {
		r.flag(FlagSecret, "env-file", name)
	}
	if ext == ".pem" || ext == ".key" || ext == ".p12" || ext == ".pfx" || ext == ".keystore" || ext == ".jks" {
		r.flag(FlagSecret, "key-file", name)
	}
}

// secretRules are common credential shapes. A match is a hint, not proof:
// the moderator opens the file.
var secretRules = []struct {
	name string
	re   *regexp.Regexp
}{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"aws-access-key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{40,}`)},
	{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"npm-token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`)},
	{"slack-token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"stripe-key", regexp.MustCompile(`\b[sr]k_live_[A-Za-z0-9]{16,}`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"password-assignment", regexp.MustCompile(`(?i)\b(?:password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token)\b["']?\s*[:=]\s*["']?[^\s"'<>{}$]{8,}`)},
}

// scanner is the io.Writer an entry is decompressed into: it keeps the
// first scanBytes for the secret scan and the first bytes for magic
// detection, and discards the rest.
type scanner struct {
	buf []byte
}

func newScanner(declared int64) *scanner {
	c := declared
	if c > scanBytes || c < 0 {
		c = scanBytes
	}
	return &scanner{buf: make([]byte, 0, c)}
}

func (s *scanner) Write(p []byte) (int, error) {
	if room := scanBytes - len(s.buf); room > 0 {
		if room > len(p) {
			room = len(p)
		}
		s.buf = append(s.buf, p[:room]...)
	}
	return len(p), nil
}

var magics = []struct {
	rule  string
	bytes []byte
}{
	{"elf", []byte{0x7f, 'E', 'L', 'F'}},
	{"mach-o", []byte{0xfe, 0xed, 0xfa, 0xce}},
	{"mach-o", []byte{0xfe, 0xed, 0xfa, 0xcf}},
	{"mach-o", []byte{0xce, 0xfa, 0xed, 0xfe}},
	{"mach-o", []byte{0xcf, 0xfa, 0xed, 0xfe}},
}

func (s *scanner) report(r *Report, name string) {
	b := s.buf
	for _, m := range magics {
		if bytes.HasPrefix(b, m.bytes) {
			r.flag(FlagBinary, m.rule, name)
		}
	}
	if isPE(b) {
		r.flag(FlagBinary, "pe", name)
	}
	// binary content: only the private key block is worth looking for
	head := b
	if len(head) > 8000 {
		head = head[:8000]
	}
	binary := bytes.IndexByte(head, 0) >= 0
	for _, rule := range secretRules {
		if binary && rule.name != "private-key" {
			continue
		}
		if rule.re.Match(b) {
			r.flag(FlagSecret, rule.name, name)
		}
	}
	s.buf = nil
}

// isPE checks the MZ header and the PE signature it points to, so a text
// file starting with "MZ" is not flagged.
func isPE(b []byte) bool {
	if len(b) < 0x40 || b[0] != 'M' || b[1] != 'Z' {
		return false
	}
	off := int(b[0x3c]) | int(b[0x3d])<<8 | int(b[0x3e])<<16 | int(b[0x3f])<<24
	return off > 0 && off+4 <= len(b) && bytes.Equal(b[off:off+4], []byte{'P', 'E', 0, 0})
}
