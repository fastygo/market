package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"sync"
)

type nonceKey struct{}

func withNonce(r *http.Request) (*http.Request, string) {
	if v, ok := r.Context().Value(nonceKey{}).(string); ok && v != "" {
		return r, v
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return r, ""
	}
	n := base64.RawURLEncoding.EncodeToString(b)
	return r.WithContext(context.WithValue(r.Context(), nonceKey{}, n)), n
}

func nonceFrom(ctx context.Context) string {
	v, _ := ctx.Value(nonceKey{}).(string)
	return v
}

func contentSecurityPolicy(nonce string) string {
	script := "'self'"
	if nonce != "" {
		script += " 'nonce-" + nonce + "'"
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + script,
		"style-src 'self'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"frame-ancestors 'none'",
		// YooKassa checkout is a 303 to yoomoney.ru after POST /checkout/form.
		// Browsers apply form-action to that redirect; 'self' alone blocks it.
		"form-action 'self' https://yoomoney.ru https://*.yoomoney.ru https://yookassa.ru https://*.yookassa.ru",
	}, "; ")
}

func setCSP(w http.ResponseWriter, nonce string) {
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))
}

type editorAssets struct {
	JS, CSS string
}

var editorOnce sync.Once
var editorFound editorAssets

func loadEditorAssets() editorAssets {
	editorOnce.Do(func() {
		root := Static()
		entries, err := fs.ReadDir(root, "editor")
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasSuffix(name, ".js") {
				editorFound.JS = "/static/editor/" + name
			}
			if strings.HasSuffix(name, ".css") {
				editorFound.CSS = "/static/editor/" + name
			}
		}
	})
	return editorFound
}

// EditorLabels is the LongreadLabels object passed to the island.
func EditorLabels(t func(string) string) map[string]string {
	keys := []string{
		"toolbar", "paragraph", "heading2", "heading3", "bold", "italic", "strike",
		"quote", "bulletList", "orderedList", "codeBlock", "divider", "link",
		"linkURL", "linkText", "removeLink", "openLink", "invalidURL", "undo", "redo",
		"addBlock", "blockHelp", "commandSearch", "noCommands", "close", "cancel",
		"apply", "imageHelp", "editImage", "removeImage", "publicationHelp",
		"searchPreview", "emptyDescription", "unsupported", "openMarkdown",
		"outline", "emptyOutline", "duplicateBlock", "deleteBlock", "seoHeading",
		"description", "slug", "canonical", "ogTitle", "ogImage", "imageHeading",
		"imageUrl", "imageAlt", "imageTitle", "imageCaption", "imageCredit",
		"imageLayout", "layoutBox", "layoutContainer", "layoutFull", "insertImage",
		"updateImage", "moveBlockUp", "moveBlockDown", "dragBlock", "editorRegion",
		"placeholder",
	}
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		out[k] = t("ui.editor." + k)
	}
	return out
}

// mustJSON is a compact JSON object for tests and debug; it never fails
// on maps of strings.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func setSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-XSS-Protection", "0")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "accelerometer=(), autoplay=(), camera=(), display-capture=(), encrypted-media=(), fullscreen=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), midi=(), payment=(), picture-in-picture=(), publickey-credentials-get=(), screen-wake-lock=(), usb=(), interest-cohort=(), browsing-topics=()")
}
