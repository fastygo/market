package web

import (
	"strings"
	"testing"

	"github.com/fastygo/market/internal/authoring"
)

func TestReleaseName(t *testing.T) {
	cases := map[string]struct {
		out string
		ok  bool
	}{
		"theme-1.0.zip":            {"theme-1.0.zip", true},
		`C:\Users\me\My Theme.ZIP`: {"My-Theme.ZIP", true},
		"../../etc/passwd":         {"passwd", false},
		"run.sh":                   {"run.sh", false},
		".zip":                     {"zip", false},
	}
	for in, want := range cases {
		out, ok := releaseName(in)
		if out != want.out || ok != want.ok {
			t.Errorf("%q: got %q %v want %q %v", in, out, ok, want.out, want.ok)
		}
	}
}

func TestReleaseKey(t *testing.T) {
	cases := []struct {
		t       authoring.ReleaseTarget
		id, ver string
		want    string
	}{
		{authoring.ReleaseTarget{SellerSlug: "godudy", ItemSlug: "ui8kit"}, "p1", "1.0.0", "godudy/ui8kit/ui8kit-1.0.0.zip"},
		{authoring.ReleaseTarget{SellerSlug: "../..", SellerID: "s1", ItemSlug: ""}, "p1", "2.0+build", "s1/p1/p1-2.0-build.zip"},
		{authoring.ReleaseTarget{SellerSlug: "Big Store", ItemSlug: "kit/../x"}, "p1", ".1", "big-store/kit-..-x/kit-..-x-1.zip"},
	}
	for _, c := range cases {
		got := releaseKey(c.t, c.id, c.ver)
		if got != c.want {
			t.Errorf("releaseKey(%+v, %q) = %q, want %q", c.t, c.ver, got, c.want)
		}
		for _, seg := range strings.Split(got, "/") {
			if seg == "" || seg == "." || seg == ".." {
				t.Errorf("unsafe segment in %q", got)
			}
		}
	}
}
