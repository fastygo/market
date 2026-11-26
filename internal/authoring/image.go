package authoring

import (
	"bytes"
	"path"
	"strings"
	"unicode"
)

// DetectImage returns a safe image MIME type from a leading sniffer.
// It accepts JPEG, PNG, GIF and WebP only; SVG and HTML are rejected
// even when the name claims otherwise.
func DetectImage(head []byte) (mime string, ok bool) {
	if len(head) >= 3 && head[0] == 0xff && head[1] == 0xd8 && head[2] == 0xff {
		return "image/jpeg", true
	}
	if bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")) {
		return "image/png", true
	}
	if bytes.HasPrefix(head, []byte("GIF87a")) || bytes.HasPrefix(head, []byte("GIF89a")) {
		return "image/gif", true
	}
	if len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP" {
		return "image/webp", true
	}
	return "", false
}

// SafeImageName is a base name with a matching extension for mime.
func SafeImageName(name, mime string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
	}
	name = strings.Trim(b.String(), ".")
	if name == "" {
		name = "image"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	ext := map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/gif":  ".gif",
		"image/webp": ".webp",
	}[mime]
	if ext == "" {
		ext = ".img"
	}
	if !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	return name
}
