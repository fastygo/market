package clientip

import (
	"net/http/httptest"
	"testing"
)

func TestResolver(t *testing.T) {
	edge, err := ParsePrefixes("10.0.0.2/32")
	if err != nil {
		t.Fatal(err)
	}
	r := Resolver{Header: "X-Forwarded-For", Trusted: edge}
	for _, tc := range []struct {
		name, remote string
		xff          []string
		want         string
	}{
		{"direct, no header", "203.0.113.7:5555", nil, "203.0.113.7"},
		{"direct peer spoofs header", "203.0.113.7:5555", []string{"185.71.76.1"}, "203.0.113.7"},
		{"edge forwards client", "10.0.0.2:4000", []string{"198.51.100.9"}, "198.51.100.9"},
		{"client prepends a fake hop", "10.0.0.2:4000", []string{"185.71.76.1, 198.51.100.9"}, "198.51.100.9"},
		{"fake hop in a second header line", "10.0.0.2:4000", []string{"185.71.76.1", "198.51.100.9"}, "198.51.100.9"},
		{"edge without header", "10.0.0.2:4000", nil, "10.0.0.2"},
		{"garbage rightmost", "10.0.0.2:4000", []string{"185.71.76.1, not-an-ip"}, "10.0.0.2"},
		{"ipv6 client", "10.0.0.2:4000", []string{"2001:db8::1"}, "2001:db8::1"},
		{"mapped ipv4 peer", "[::ffff:10.0.0.2]:4000", []string{"198.51.100.9"}, "198.51.100.9"},
	} {
		req := httptest.NewRequest("POST", "/hooks/pay/yookassa", nil)
		req.RemoteAddr = tc.remote
		for _, v := range tc.xff {
			req.Header.Add("X-Forwarded-For", v)
		}
		if got := r.IP(req); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestChainOfTrustedProxies(t *testing.T) {
	tr, _ := ParsePrefixes("10.0.0.0/24, 127.0.0.1")
	r := Resolver{Header: "X-Forwarded-For", Trusted: tr}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("X-Forwarded-For", "185.71.76.1, 198.51.100.9, 10.0.0.2")
	if got := r.IP(req); got != "198.51.100.9" {
		t.Fatal(got)
	}
}

func TestNoTrustedProxiesIgnoresHeader(t *testing.T) {
	r := Resolver{Header: "X-Forwarded-For"}
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.2:1"
	req.Header.Set("X-Forwarded-For", "185.71.76.1")
	if got := r.IP(req); got != "10.0.0.2" {
		t.Fatal(got)
	}
}

func TestParsePrefixes(t *testing.T) {
	p, err := ParsePrefixes(" 10.0.0.2/32,127.0.0.1 ::1 ")
	if err != nil || len(p) != 3 || p[1].Bits() != 32 || p[2].Bits() != 128 {
		t.Fatal(p, err)
	}
	if _, err := ParsePrefixes("10.0.0.2/33"); err == nil {
		t.Fatal("bad cidr must fail")
	}
	if p, err := ParsePrefixes(""); err != nil || len(p) != 0 {
		t.Fatal(p, err)
	}
}
