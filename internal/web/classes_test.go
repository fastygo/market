package web_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"regexp"
	"strings"
	"testing"

	"github.com/fastygo/market/internal/web"
)

var (
	classAttr = regexp.MustCompile(`class="([^"]*)"`)
	skinLink  = regexp.MustCompile(`href="/static/(skins/[a-z]+\.css)\?v=([0-9a-f]{12})"`)
)

// TestSemanticClassesHaveRules: every ui-* class a page renders has a rule in the skin it links (scripts/uisem prunes the
// generated kit CSS to the recipes in use, so a new recipe without `make css` fails here), and the skin URL carries the
// content hash (cache-bust when the CSS changes).
func TestSemanticClassesHaveRules(t *testing.T) {
	h := handler(t)
	seen := 0
	for _, path := range []string{"/", "/catalog", "/catalog/themes", "/products/p1", "/authors/seller", "/products/missing"} {
		body := get(h, path).Body.String()
		m := skinLink.FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s: no versioned skin link", path)
		}
		css, err := fs.ReadFile(web.Static(), m[1])
		if err != nil {
			t.Fatal(err)
		}
		if sum := sha256.Sum256(css); hex.EncodeToString(sum[:6]) != m[2] {
			t.Errorf("%s: ?v=%s is not the hash of %s", path, m[2], m[1])
		}
		for _, c := range classAttr.FindAllStringSubmatch(body, -1) {
			for _, name := range strings.Fields(c[1]) {
				if !strings.HasPrefix(name, "ui-") {
					continue
				}
				seen++
				// a root whose base and defaults draw nothing (ui-text) has no rule of its own, only its value rules
				if !regexp.MustCompile(`\.` + regexp.QuoteMeta(name) + `[^a-z0-9]`).Match(css) {
					t.Errorf("%s: class %s has no rule in %s", path, name, m[1])
				}
			}
		}
	}
	if seen == 0 {
		t.Fatal("no ui-* classes rendered: semantic class mode is off")
	}
}
