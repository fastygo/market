package web_test

import (
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/", "/catalog", "/products/p1"} {
		rec := get(h, path)
		if rec.Header().Get("X-XSS-Protection") != "0" {
			t.Errorf("%s: X-XSS-Protection %q", path, rec.Header().Get("X-XSS-Protection"))
		}
		if rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("%s: CORP %q", path, rec.Header().Get("Cross-Origin-Resource-Policy"))
		}
		if !strings.Contains(rec.Header().Get("Permissions-Policy"), "geolocation=()") {
			t.Errorf("%s: Permissions-Policy %q", path, rec.Header().Get("Permissions-Policy"))
		}
		if rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("%s: unexpected ACAO %q", path, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestStaticHasNoDirectoryListing(t *testing.T) {
	h := handler(t)
	for _, path := range []string{"/static/", "/static/fonts/", "/static/skins/", "/static/editor/"} {
		rec := get(h, path)
		if rec.Code != 404 {
			t.Errorf("%s: want 404, got %d body %q", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "<a href=") {
			t.Errorf("%s: listing leaked", path)
		}
	}
	// a real file still works
	if rec := get(h, "/static/logo.svg"); rec.Code != 200 {
		t.Fatalf("logo.svg: %d", rec.Code)
	}
}
