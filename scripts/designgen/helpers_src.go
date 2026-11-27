package main

// helpers is written verbatim to ui/design/helpers.go.
const helpers = `// ` + header + `

package design

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/a-h/templ"
)

func or[T comparable](v, d T) T {
	var zero T
	if v == zero {
		return d
	}
	return v
}

func orAny(v, d any) any {
	if v == nil || v == "" {
		return d
	}
	return v
}

func pick[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}

func at[T any](s []T, i int) T {
	var zero T
	if i < 0 || i >= len(s) {
		return zero
	}
	return s[i]
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func concat[T any](parts ...[]T) []T {
	var out []T
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func mapSlice[A, B any](s []A, f func(A) B) []B {
	out := make([]B, 0, len(s))
	for _, v := range s {
		out = append(out, f(v))
	}
	return out
}

func mget(m any, k string) any {
	if mm, ok := m.(map[string]any); ok {
		return mm[k]
	}
	return nil
}

func toStr(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	}
	return true
}

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

// join joins non-empty class names.
func join(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

// cx is the base class plus condition/class pairs.
func cx(base string, pairs ...any) string {
	parts := []string{base}
	for i := 0; i+1 < len(pairs); i += 2 {
		if c, _ := pairs[i].(bool); c {
			parts = append(parts, pairs[i+1].(string))
		}
	}
	return join(parts...)
}

// attrs builds attributes from key/value pairs, dropping empty strings and
// false booleans.
func attrs(kv ...any) templ.Attributes {
	out := templ.Attributes{}
	for i := 0; i+1 < len(kv); i += 2 {
		k := kv[i].(string)
		switch v := kv[i+1].(type) {
		case string:
			if v != "" {
				out[k] = v
			}
		case bool:
			if v {
				out[k] = true
			}
		default:
			out[k] = v
		}
	}
	return out
}

func withAttr(a templ.Attributes, k string, v any) templ.Attributes {
	out := templ.Attributes{}
	for kk, vv := range a {
		out[kk] = vv
	}
	if _, ok := out[k]; !ok {
		out[k] = v
	}
	return out
}

// Glyph renders a Lucide glyph as an inline svg (decorative).
func Glyph(name string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		body, ok := glyphs[name]
		if !ok {
			body = glyphs["circle"]
		}
		_, err := io.WriteString(w, ` + "`" + `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="mk-glyph" aria-hidden="true" focusable="false">` + "`" + `+body+"</svg>")
		return err
	})
}

// HasGlyph reports whether the generated icon set has name.
func HasGlyph(name string) bool { _, ok := glyphs[name]; return ok }
`
