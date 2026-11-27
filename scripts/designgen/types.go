package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// T is a TypeScript type of the spec subset, mapped onto Go.
type T struct {
	K      string // string, number, strnum, bool, named, array, object, func, any, map, lits, undef, comp
	Name   string // named type or generated struct name for object
	Elem   *T
	Fields []Field
	Lits   []string
	Ptr    bool // optional named struct prop
}

// Field of an object type.
type Field struct {
	Name string
	T    *T
	Opt  bool
}

var (
	tString = &T{K: "string"}
	tNumber = &T{K: "number"}
	tBool   = &T{K: "bool"}
	tAny    = &T{K: "any"}
	tUndef  = &T{K: "undef"}
)

// named holds types.ts declarations.
var named = map[string]*T{}

// objects are generated inline object structs by Go name.
var objects = map[string]*T{}

func splitTop(s string, sep byte) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{', '(', '[', '<':
			depth++
		case '}', ')', ']', '>':
			if !(s[i] == '>' && i > 0 && s[i-1] == '=') {
				depth--
			}
		case '"', '\'':
			q := s[i]
			for i++; i < len(s) && s[i] != q; i++ {
			}
		}
		if s[i] == sep && depth == 0 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}

// parseType parses a TS type; owner names generated object structs.
func parseType(s, owner string) *T {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "=>") && (strings.HasPrefix(s, "(") || strings.HasPrefix(s, "()")) {
		return &T{K: "func"}
	}
	if strings.HasSuffix(s, "[]") {
		inner := strings.TrimSuffix(s, "[]")
		el := parseType(inner, owner+"Item")
		return &T{K: "array", Elem: el}
	}
	if parts := splitTop(s, '|'); len(parts) > 1 {
		var lits []string
		allLit, hasNum, hasStr := true, false, false
		for _, p := range parts {
			p = strings.TrimSpace(p)
			switch {
			case strings.HasPrefix(p, "\"") || strings.HasPrefix(p, "'"):
				lits = append(lits, strings.Trim(p, "\"'"))
			case regexp.MustCompile(`^\d+$`).MatchString(p):
				lits = append(lits, p)
			case p == "number":
				hasNum, allLit = true, false
			case p == "string":
				hasStr, allLit = true, false
			case p == "boolean":
				allLit = false
			default:
				allLit = false
			}
		}
		if allLit {
			return &T{K: "lits", Lits: lits}
		}
		if hasNum && hasStr {
			return &T{K: "strnum"}
		}
		if hasStr {
			return tString
		}
		return tAny
	}
	switch s {
	case "string":
		return tString
	case "number":
		return tNumber
	case "boolean":
		return tBool
	case "any", "unknown":
		return tAny
	}
	if strings.HasPrefix(s, "Record<") {
		return &T{K: "map"}
	}
	if strings.HasPrefix(s, "{") {
		body := strings.TrimSpace(s[1 : len(s)-1])
		o := &T{K: "object", Name: owner}
		for _, f := range splitTop(body, ';') {
			f = strings.TrimSpace(f)
			if f == "" {
				continue
			}
			k, v, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			k = strings.TrimSpace(k)
			opt := strings.HasSuffix(k, "?")
			k = strings.TrimSuffix(k, "?")
			o.Fields = append(o.Fields, Field{Name: k, T: parseType(v, owner+pascal(k)), Opt: opt})
		}
		if owner != "" {
			if prev, ok := objects[owner]; ok && typeKey(prev) != typeKey(o) {
				panic("object name clash " + owner)
			}
			objects[owner] = o
		}
		return o
	}
	if regexp.MustCompile(`^[A-Z]\w*$`).MatchString(s) {
		return &T{K: "named", Name: s}
	}
	panic(fmt.Sprintf("unsupported type %q", s))
}

func typeKey(t *T) string {
	if t == nil {
		return "nil"
	}
	switch t.K {
	case "array":
		return "[]" + typeKey(t.Elem)
	case "object":
		var b []string
		for _, f := range t.Fields {
			b = append(b, f.Name+":"+typeKey(f.T))
		}
		return "{" + strings.Join(b, ";") + "}"
	case "named":
		return t.Name
	}
	return t.K
}

// resolve returns the struct type behind a named type.
func resolve(t *T) *T {
	if t != nil && t.K == "named" {
		if n, ok := named[t.Name]; ok {
			return n
		}
	}
	return t
}

func (t *T) field(name string) (*Field, bool) {
	r := resolve(t)
	if r == nil || r.K != "object" {
		return nil, false
	}
	for i := range r.Fields {
		if r.Fields[i].Name == name {
			return &r.Fields[i], true
		}
	}
	return nil, false
}

// goType is the Go spelling.
func goType(t *T) string {
	switch t.K {
	case "string", "strnum", "lits":
		return "string"
	case "number":
		return "int"
	case "bool":
		return "bool"
	case "array":
		return "[]" + goType(t.Elem)
	case "named":
		if t.Ptr {
			return "*" + t.Name
		}
		return t.Name
	case "object":
		return t.Name
	case "map":
		return "map[string]any"
	case "comp":
		return "templ.Component"
	}
	return "any"
}

func loadTypes(src string) {
	re := regexp.MustCompile(`(?m)^export type (\w+) = (.+);\s*$`)
	for _, m := range re.FindAllStringSubmatch(src, -1) {
		t := parseType(m[2], m[1])
		if t.K == "object" {
			delete(objects, m[1])
			t.Name = m[1]
			named[m[1]] = t
		} else if t.K == "map" {
			named[m[1]] = t
		}
	}
}

func pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			up = true
			continue
		}
		if up {
			b.WriteString(strings.ToUpper(string(r)))
			up = false
		} else {
			b.WriteRune(r)
		}
	}
	out := b.String()
	switch out {
	case "Id":
		return "ID"
	}
	return out
}

func emitStruct(b *strings.Builder, name string, t *T) {
	fmt.Fprintf(b, "type %s struct {\n", name)
	for _, f := range t.Fields {
		ft := *f.T
		if f.Opt && ft.K == "named" {
			ft.Ptr = true
		}
		fmt.Fprintf(b, "\t%s %s\n", pascal(f.Name), goType(&ft))
	}
	b.WriteString("}\n\n")
}

func emitTypes() string {
	var b strings.Builder
	b.WriteString("// " + header + "\n\npackage design\n\n")
	var names []string
	for n, t := range named {
		if t.K == "object" {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		emitStruct(&b, n, named[n])
	}
	for n, t := range named {
		if t.K == "map" {
			fmt.Fprintf(&b, "type %s = map[string]any\n\n", n)
		}
	}
	names = names[:0]
	for n := range objects {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		emitStruct(&b, n, objects[n])
	}
	return b.String()
}
