package web

import "testing"

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
