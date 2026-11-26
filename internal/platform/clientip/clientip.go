// Package clientip resolves the client address of a request behind
// trusted reverse proxies. A forwarded header is honoured only when the
// direct peer is a trusted proxy, and then the rightmost address that is
// not a trusted proxy wins: entries left of it were written by the client
// and can be spoofed.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver returns the client IP of a request.
type Resolver struct {
	// Header is the forwarded header (e.g. X-Forwarded-For); empty means
	// the direct peer only.
	Header string
	// Trusted are the proxies allowed to set Header (e.g. a documentation
	// example edge 10.0.0.2/32). With no trusted proxies Header is ignored.
	Trusted []netip.Prefix
}

// ParsePrefixes parses a comma or space separated list of CIDRs or single
// addresses ("10.0.0.2/32, 127.0.0.1").
func ParsePrefixes(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if strings.Contains(f, "/") {
			p, err := netip.ParsePrefix(f)
			if err != nil {
				return nil, fmt.Errorf("clientip: bad trusted proxy %q: %w", f, err)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(f)
		if err != nil {
			return nil, fmt.Errorf("clientip: bad trusted proxy %q: %w", f, err)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// IP returns the client address as a string (the raw RemoteAddr when it
// cannot be parsed).
func (c Resolver) IP(r *http.Request) string {
	peer, ok := parse(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if c.Header == "" || !c.trusted(peer) {
		return peer.String()
	}
	vals := r.Header.Values(c.Header)
	var hops []string
	for _, v := range vals {
		hops = append(hops, strings.Split(v, ",")...)
	}
	// walk from the right: every trusted hop is skipped, the first
	// untrusted one is the client; garbage stops the walk (fall back to
	// the last trusted hop seen, never to a client-written value).
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parse(strings.TrimSpace(hops[i]))
		if !ok {
			break
		}
		client = a
		if !c.trusted(a) {
			break
		}
	}
	return client.String()
}

func (c Resolver) trusted(a netip.Addr) bool {
	for _, p := range c.Trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parse accepts "ip", "ip:port" and "[ipv6]:port".
func parse(s string) (netip.Addr, bool) {
	if s == "" {
		return netip.Addr{}, false
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		if a, err := netip.ParseAddr(host); err == nil {
			return a.Unmap(), true
		}
	}
	return netip.Addr{}, false
}
