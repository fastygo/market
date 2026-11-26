// Command fontver appends a content hash to /static/fonts/* URLs in the
// compiled skin stylesheets so those files can be served with a long
// immutable cache. Run after `make css` (or as part of it). -check exits
// 1 when a stylesheet is stale.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

var fontURL = regexp.MustCompile(`url\((['"]?)(/static/fonts/[^)'"?]+)(\?v=[0-9a-f]{12})?(['"]?)\)`)

func main() {
	check := flag.Bool("check", false, "exit 1 if compiled skins are missing current font hashes")
	flag.Parse()
	root, err := os.Getwd()
	if err != nil {
		fatal(err)
	}
	skins, err := filepath.Glob(filepath.Join(root, "internal/web/static/skins/*.css"))
	if err != nil {
		fatal(err)
	}
	if len(skins) == 0 {
		fatal(fmt.Errorf("no compiled skins in internal/web/static/skins"))
	}
	var stale []string
	for _, p := range skins {
		b, err := os.ReadFile(p)
		if err != nil {
			fatal(err)
		}
		out, changed, err := rewrite(root, b)
		if err != nil {
			fatal(err)
		}
		if !changed {
			continue
		}
		if *check {
			stale = append(stale, p)
			continue
		}
		if err := os.WriteFile(p, out, 0o644); err != nil {
			fatal(err)
		}
	}
	if *check && len(stale) > 0 {
		fmt.Fprintln(os.Stderr, "fontver: compiled skins missing current font hashes (run make css):")
		for _, p := range stale {
			fmt.Fprintln(os.Stderr, "  ", p)
		}
		os.Exit(1)
	}
}

func rewrite(root string, css []byte) ([]byte, bool, error) {
	var err error
	out := fontURL.ReplaceAllFunc(css, func(m []byte) []byte {
		if err != nil {
			return m
		}
		sub := fontURL.FindSubmatch(m)
		quote, path := sub[1], string(sub[2])
		endq := sub[4]
		name := filepath.Base(path)
		b, readErr := os.ReadFile(filepath.Join(root, "internal/web/static/fonts", name))
		if readErr != nil {
			err = fmt.Errorf("%s: %w", path, readErr)
			return m
		}
		sum := sha256.Sum256(b)
		v := hex.EncodeToString(sum[:6])
		return []byte("url(" + string(quote) + path + "?v=" + v + string(endq) + ")")
	})
	if err != nil {
		return nil, false, err
	}
	return out, string(out) != string(css), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "fontver:", err)
	os.Exit(1)
}
