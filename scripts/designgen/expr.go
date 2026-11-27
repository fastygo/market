package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// A small JS expression subset of the specs, parsed and translated to Go
// with types (props, loop variables).

type tok struct {
	k string // id, str, num, op, eof
	v string
}

func lex(s string) []tok {
	var out []tok
	ops := []string{"===", "!==", "?.", "??", "&&", "||", "<=", ">=", "...", "=>", "==", "!=", "(", ")", "[", "]", "{", "}", ",", ".", "?", ":", "!", "+", "-", "<", ">", "*", "/", "%"}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n':
			i++
		case c == '\'' || c == '"' || c == '`':
			j := i + 1
			var b strings.Builder
			for j < len(s) && s[j] != c {
				if s[j] == '\\' && j+1 < len(s) {
					j++
				}
				b.WriteByte(s[j])
				j++
			}
			out = append(out, tok{"str", b.String()})
			i = j + 1
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == '.') {
				j++
			}
			out = append(out, tok{"num", s[i:j]})
			i = j
		case c == '_' || c == '$' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(s) && (s[j] == '_' || s[j] == '$' || s[j] >= 'a' && s[j] <= 'z' || s[j] >= 'A' && s[j] <= 'Z' || s[j] >= '0' && s[j] <= '9') {
				j++
			}
			out = append(out, tok{"id", s[i:j]})
			i = j
		default:
			found := false
			for _, o := range ops {
				if strings.HasPrefix(s[i:], o) {
					// ?. followed by a digit is a ternary
					if o == "?." && i+2 < len(s) && s[i+2] >= '0' && s[i+2] <= '9' {
						continue
					}
					out = append(out, tok{"op", o})
					i += len(o)
					found = true
					break
				}
			}
			if !found {
				panic(fmt.Sprintf("lex: bad char %q in %q", c, s))
			}
		}
	}
	return append(out, tok{"eof", ""})
}

// E is the AST.
type E struct {
	K    string // id, str, num, bool, undef, member, index, call, unary, bin, cond, array, spread
	V    string
	A, B *E
	C    *E
	Args []*E
	Opt  bool
}

type parser struct {
	t []tok
	i int
	s string
}

func (p *parser) peek() tok { return p.t[p.i] }
func (p *parser) next() tok { t := p.t[p.i]; p.i++; return t }
func (p *parser) is(v string) bool {
	t := p.peek()
	return t.k == "op" && t.v == v
}
func (p *parser) expect(v string) {
	if !p.is(v) {
		panic(fmt.Sprintf("parse %q: want %q got %q", p.s, v, p.peek().v))
	}
	p.i++
}

var asAny = regexp.MustCompile(`\s+as\s+any\b`)

func parseExpr(s string) *E {
	s = asAny.ReplaceAllString(s, "")
	p := &parser{t: lex(s), s: s}
	e := p.cond()
	if p.peek().k != "eof" {
		panic(fmt.Sprintf("parse %q: trailing %q", s, p.peek().v))
	}
	return e
}

func (p *parser) cond() *E {
	c := p.bin(0)
	if p.is("?") {
		p.next()
		a := p.cond()
		p.expect(":")
		b := p.cond()
		return &E{K: "cond", C: c, A: a, B: b}
	}
	return c
}

var prec = [][]string{{"??"}, {"||"}, {"&&"}, {"===", "!==", "==", "!="}, {"<", ">", "<=", ">="}, {"+", "-"}, {"*", "/", "%"}}

func (p *parser) bin(level int) *E {
	if level == len(prec) {
		return p.unary()
	}
	l := p.bin(level + 1)
	for {
		t := p.peek()
		hit := false
		for _, o := range prec[level] {
			if t.k == "op" && t.v == o {
				hit = true
			}
		}
		if !hit {
			return l
		}
		p.next()
		r := p.bin(level + 1)
		l = &E{K: "bin", V: t.v, A: l, B: r}
	}
}

func (p *parser) unary() *E {
	if p.is("!") {
		p.next()
		return &E{K: "unary", V: "!", A: p.unary()}
	}
	if p.is("-") {
		p.next()
		return &E{K: "unary", V: "-", A: p.unary()}
	}
	return p.postfix(p.primary())
}

func (p *parser) postfix(e *E) *E {
	for {
		switch {
		case p.is("."):
			p.next()
			e = &E{K: "member", A: e, V: p.next().v}
		case p.is("?."):
			p.next()
			if p.is("(") {
				p.next()
				e = &E{K: "call", A: e, Args: p.args(), Opt: true}
				continue
			}
			if p.is("[") {
				p.next()
				ix := p.cond()
				p.expect("]")
				e = &E{K: "index", A: e, B: ix, Opt: true}
				continue
			}
			e = &E{K: "member", A: e, V: p.next().v, Opt: true}
		case p.is("["):
			p.next()
			ix := p.cond()
			p.expect("]")
			e = &E{K: "index", A: e, B: ix}
		case p.is("("):
			p.next()
			e = &E{K: "call", A: e, Args: p.args()}
		default:
			return e
		}
	}
}

func (p *parser) args() []*E {
	var out []*E
	for !p.is(")") {
		out = append(out, p.cond())
		if p.is(",") {
			p.next()
		}
	}
	p.expect(")")
	return out
}

func (p *parser) primary() *E {
	t := p.next()
	switch t.k {
	case "str":
		return &E{K: "str", V: t.v}
	case "num":
		return &E{K: "num", V: t.v}
	case "id":
		switch t.v {
		case "true", "false":
			return &E{K: "bool", V: t.v}
		case "undefined", "null":
			return &E{K: "undef"}
		}
		return &E{K: "id", V: t.v}
	case "op":
		switch t.v {
		case "(":
			e := p.cond()
			p.expect(")")
			return e
		case "[":
			a := &E{K: "array"}
			for !p.is("]") {
				if p.is("...") {
					p.next()
					a.Args = append(a.Args, &E{K: "spread", A: p.cond()})
				} else {
					a.Args = append(a.Args, p.cond())
				}
				if p.is(",") {
					p.next()
				}
			}
			p.expect("]")
			return a
		}
	}
	panic(fmt.Sprintf("parse %q: unexpected %q", p.s, t.v))
}

// Scope binds JS names to Go expressions.
type Scope struct {
	vars   map[string]binding
	parent *Scope
}

type binding struct {
	Go string
	T  *T
}

func (s *Scope) get(n string) (binding, bool) {
	for c := s; c != nil; c = c.parent {
		if b, ok := c.vars[n]; ok {
			return b, true
		}
	}
	return binding{}, false
}

func (s *Scope) child() *Scope { return &Scope{vars: map[string]binding{}, parent: s} }

// G is a translated expression.
type G struct {
	S string
	T *T
}

func goStr(v string) string { return strconv.Quote(v) }

// tr translates e in scope.
func tr(e *E, sc *Scope) G {
	switch e.K {
	case "str":
		return G{goStr(e.V), &T{K: "lits", Lits: []string{e.V}}}
	case "num":
		return G{e.V, tNumber}
	case "bool":
		return G{e.V, tBool}
	case "undef":
		return G{"", tUndef}
	case "id":
		b, ok := sc.get(e.V)
		if !ok {
			panic(fmt.Sprintf("unknown name %q", e.V))
		}
		return G{b.Go, b.T}
	case "member":
		o := tr(e.A, sc)
		if _, isField := o.T.field("length"); e.V == "length" && !isField {
			return G{"len(" + o.S + ")", tNumber}
		}
		r := resolve(o.T)
		if r.K == "map" || r.K == "any" {
			return G{"mget(" + o.S + ", " + goStr(e.V) + ")", tAny}
		}
		f, ok := o.T.field(e.V)
		if !ok {
			panic(fmt.Sprintf("no field %q on %s", e.V, typeKey(o.T)))
		}
		ft := *f.T
		if f.Opt && ft.K == "named" {
			ft.Ptr = true
		}
		if o.T.Ptr && e.Opt {
			return G{fmt.Sprintf("func() %s { if %s == nil { return %s }; return %s.%s }()", goType(&ft), o.S, zero(&ft), o.S, pascal(e.V)), &ft}
		}
		return G{o.S + "." + pascal(e.V), &ft}
	case "index":
		o := tr(e.A, sc)
		ix := tr(e.B, sc)
		r := resolve(o.T)
		switch r.K {
		case "array":
			return G{"at(" + o.S + ", " + ix.S + ")", r.Elem}
		case "map", "any":
			return G{"mget(" + o.S + ", " + ix.S + ")", tAny}
		case "object":
			return G{"fieldOf(" + o.S + ", " + ix.S + ")", tString}
		}
		panic("index on " + typeKey(o.T))
	case "unary":
		a := tr(e.A, sc)
		if e.V == "!" {
			return G{"!(" + truthy(a) + ")", tBool}
		}
		return G{"-" + a.S, tNumber}
	case "cond":
		c := tr(e.C, sc)
		a, b := tr(e.A, sc), tr(e.B, sc)
		t := unify(a.T, b.T)
		as, bs := conv(a, t), conv(b, t)
		return G{fmt.Sprintf("pick(%s, %s, %s)", truthy(c), as, bs), t}
	case "bin":
		a, b := tr(e.A, sc), tr(e.B, sc)
		switch e.V {
		case "??":
			t := unify(a.T, b.T)
			if resolve(a.T).K == "array" {
				return a
			}
			if t.K == "any" || a.T.K == "any" {
				return G{"orAny(" + a.S + ", " + b.S + ")", tAny}
			}
			return G{"or(" + conv(a, t) + ", " + conv(b, t) + ")", t}
		case "&&", "||":
			return G{"(" + truthy(a) + " " + e.V + " " + truthy(b) + ")", tBool}
		case "===", "==", "!==", "!=":
			op := "=="
			if e.V[0] == '!' {
				op = "!="
			}
			if b.T.K == "undef" {
				return G{"(" + a.S + " " + op + " " + zero(a.T) + ")", tBool}
			}
			t := unify(a.T, b.T)
			return G{"(" + conv(a, t) + " " + op + " " + conv(b, t) + ")", tBool}
		case "<", ">", "<=", ">=", "-", "*", "/", "%":
			t := tNumber
			if e.V == "-" || e.V == "*" || e.V == "/" || e.V == "%" {
				return G{"(" + a.S + " " + e.V + " " + b.S + ")", t}
			}
			return G{"(" + a.S + " " + e.V + " " + b.S + ")", tBool}
		case "+":
			if isStr(a.T) || isStr(b.T) {
				return G{"(" + conv(a, tString) + " + " + conv(b, tString) + ")", tString}
			}
			return G{"(" + a.S + " + " + b.S + ")", tNumber}
		}
	case "call":
		// String(x), x.replace(a, b), x.includes(y)
		if e.A.K == "id" && e.A.V == "String" {
			a := tr(e.Args[0], sc)
			return G{conv(a, tString), tString}
		}
		if e.A.K == "member" {
			o := tr(e.A.A, sc)
			switch e.A.V {
			case "replace":
				return G{fmt.Sprintf("strings.Replace(%s, %s, %s, 1)", o.S, conv(tr(e.Args[0], sc), tString), conv(tr(e.Args[1], sc), tString)), tString}
			case "includes":
				return G{fmt.Sprintf("slices.Contains(%s, %s)", o.S, tr(e.Args[0], sc).S), tBool}
			}
		}
		panic(fmt.Sprintf("unsupported call %v", e.A.V))
	case "array":
		var parts []string
		var el *T
		for _, a := range e.Args {
			if a.K == "spread" {
				g := tr(a.A, sc)
				parts = append(parts, g.S)
				el = resolve(g.T).Elem
			} else {
				g := tr(a, sc)
				parts = append(parts, "[]"+goType(g.T)+"{"+g.S+"}")
				el = g.T
			}
		}
		if el == nil {
			return G{"nil", &T{K: "array", Elem: tAny}}
		}
		return G{"concat(" + strings.Join(parts, ", ") + ")", &T{K: "array", Elem: el}}
	}
	panic("tr: " + e.K)
}

func isStr(t *T) bool { return t.K == "string" || t.K == "lits" || t.K == "strnum" }

func unify(a, b *T) *T {
	if a.K == "undef" {
		return b
	}
	if b.K == "undef" {
		return a
	}
	if isStr(a) && isStr(b) {
		if a.K == "lits" && b.K == "lits" {
			return &T{K: "lits", Lits: append(append([]string{}, a.Lits...), b.Lits...)}
		}
		return tString
	}
	if a.K == "any" || b.K == "any" {
		return tAny
	}
	return a
}

// conv converts g to t's Go type.
func conv(g G, t *T) string {
	if g.T.K == "undef" {
		return zero(t)
	}
	if isStr(t) {
		switch g.T.K {
		case "number":
			return "strconv.Itoa(" + g.S + ")"
		case "any", "map":
			return "toStr(" + g.S + ")"
		case "bool":
			return "strconv.FormatBool(" + g.S + ")"
		}
	}
	if t.K == "bool" && g.T.K != "bool" {
		return truthy(g)
	}
	return g.S
}

func zero(t *T) string {
	switch t.K {
	case "string", "strnum", "lits":
		return `""`
	case "number":
		return "0"
	case "bool":
		return "false"
	case "named":
		if t.Ptr {
			return "nil"
		}
		return t.Name + "{}"
	case "object":
		return t.Name + "{}"
	}
	return "nil"
}

func truthy(g G) string {
	switch resolve(g.T).K {
	case "bool":
		return g.S
	case "string", "strnum", "lits":
		return g.S + ` != ""`
	case "number":
		return g.S + " != 0"
	case "array", "map":
		return "len(" + g.S + ") > 0"
	case "any":
		return "truthy(" + g.S + ")"
	case "object":
		if g.T.Ptr {
			return g.S + " != nil"
		}
		return "true"
	}
	return "true"
}

// literals returns the possible literal values of an expression (for
// recipe axes), or nil when unknown.
func literals(e *E, sc *Scope) []string {
	switch e.K {
	case "str":
		return []string{e.V}
	case "cond":
		a, b := literals(e.A, sc), literals(e.B, sc)
		if a == nil || b == nil {
			return nil
		}
		return uniq(append(a, b...))
	case "bin":
		if e.V == "??" {
			a, b := literals(e.A, sc), literals(e.B, sc)
			if a == nil || b == nil {
				return nil
			}
			return uniq(append(a, b...))
		}
	}
	g := tr(e, sc)
	if g.T.K == "lits" {
		return g.T.Lits
	}
	return nil
}

func uniq(v []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range v {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
