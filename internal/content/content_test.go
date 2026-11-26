package content_test

import (
	"strings"
	"testing"

	"github.com/fastygo/market/internal/content"
)

func TestRenderCommonMarkAndStrike(t *testing.T) {
	html, err := content.Render("# Title\n\nA **bold** and *em* and ~~strike~~ and a [link](https://example.com/x).\n\n- one\n- two\n")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<h1", "Title", "<strong>bold</strong>", "<em>em</em>", "<del>strike</del>", `href="https://example.com/x"`, "<li>one</li>"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestRenderStripsFrontmatterAndKeepsBody(t *testing.T) {
	src := "---\ndescription: meta\nslug: ignored\n---\n\nHello **world**.\n"
	html, err := content.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "ignored") || strings.Contains(html, "meta") {
		t.Fatalf("frontmatter leaked: %s", html)
	}
	if !strings.Contains(html, "Hello") || !strings.Contains(html, "<strong>world</strong>") {
		t.Fatalf("body lost: %s", html)
	}
}

func TestRenderXSS(t *testing.T) {
	cases := map[string][]string{
		`<script>alert(1)</script>`:                                         {"<script", "alert"},
		`<p onclick="alert(1)">ok</p>`:                                      {"onclick", "alert"},
		`<img src=x onerror="alert(1)">`:                                    {"onerror", "alert", `src="x"`},
		`[xss](javascript:alert(1))`:                                        {"javascript:", "alert(1)"},
		`[xss](javascript&colon;alert(1))`:                                  {"javascript", "alert(1)"},
		`[xss](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)`: {"data:", "alert"},
		`<img src="https://evil.example/a.png">`:                            {`src="https://evil.example/a.png"`},
		`<iframe src="https://evil.example"></iframe>`:                      {"<iframe"},
		`<a href="https://ok.example" onclick="alert(1)">x</a>`:             {"onclick"},
	}
	for in, banned := range cases {
		html, err := content.Render(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		for _, b := range banned {
			if strings.Contains(strings.ToLower(html), strings.ToLower(b)) {
				t.Errorf("input %q: sanitized HTML still contains %q: %s", in, b, html)
			}
		}
	}
}

func TestRenderAllowsFirstPartyImageAndFigure(t *testing.T) {
	src := `<figure class="longread-figure" data-layout="box" data-caption="Cap" data-credit="Cred"><img src="/api/files/col/rec/pic.png" alt="A" title="T"><figcaption>Cap<cite>Cred</cite></figcaption></figure>`
	html, err := content.Render(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<figure`, `class="longread-figure"`, `data-layout="box"`, `src="/api/files/col/rec/pic.png"`, `alt="A"`, `<figcaption>`, `<cite>Cred</cite>`} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q in %s", want, html)
		}
	}
}

func TestCap(t *testing.T) {
	ok := strings.Repeat("a", content.MaxMarkdown)
	if _, err := content.Render(ok); err != nil {
		t.Fatalf("exact cap: %v", err)
	}
	if _, err := content.Render(ok + "x"); err != content.ErrTooLarge {
		t.Fatalf("over cap: %v", err)
	}
	front := "---\nslug: x\n---\n\n" + ok
	if _, err := content.Render(front); err != nil {
		t.Fatalf("cap is of the body, not the frontmatter: %v", err)
	}
}

func TestEmpty(t *testing.T) {
	html, err := content.Render("   \n")
	if err != nil || html != "" {
		t.Fatalf("got %q %v", html, err)
	}
}

func TestSanitizeOnReadStripsInjectedScript(t *testing.T) {
	out := content.Sanitize(`<p>ok</p><script>alert(1)</script><p onclick="x">y</p>`)
	if strings.Contains(out, "<script") || strings.Contains(out, "onclick") || strings.Contains(out, "alert") {
		t.Fatalf("%s", out)
	}
	if !strings.Contains(out, "<p>ok</p>") {
		t.Fatalf("%s", out)
	}
}

func TestSanitizeKeepsSafeHrefAndSrc(t *testing.T) {
	a := content.Sanitize(`<a href="https://example.com/x">link</a>`)
	if !strings.Contains(a, `href="https://example.com/x"`) {
		t.Fatalf("href: %s", a)
	}
	img := content.Sanitize(`<img src="/api/files/col/rec/pic.png" alt="A">`)
	if !strings.Contains(img, `src="/api/files/col/rec/pic.png"`) {
		t.Fatalf("src: %s", img)
	}
}
