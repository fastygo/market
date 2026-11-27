package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	uiutils "github.com/ui8kit/codegen/generated/templ/utils"
)

// Spec is one market-design JSON spec.
type Spec struct {
	ID     string              `json:"id"`
	Name   string              `json:"name"`
	Kind   string              `json:"kind"`
	Docs   string              `json:"docs"`
	Props  map[string]PropDecl `json:"props"`
	Recipe *Recipe             `json:"recipe"`
	Rest   bool                `json:"rest"`
	Slots  []string            `json:"slots"`
	Derive map[string]string   `json:"derive"`
	Root   []any               `json:"root"`
	file   string
}

// PropDecl declares a prop.
type PropDecl struct {
	Type     string `json:"type"`
	Optional bool   `json:"optional"`
}

// Recipe is a cva-style class recipe.
type Recipe struct {
	Base     string                       `json:"base"`
	ByKey    map[string]map[string]string `json:"byKey"`
	Defaults map[string]string            `json:"defaults"`
	Keys     []string                     `json:"keys"`
}

func (r *Recipe) classes(sel map[string]string) string {
	if r == nil {
		return ""
	}
	parts := []string{r.Base}
	for _, k := range r.Keys {
		v := sel[k]
		if v == "" {
			v = r.Defaults[k]
		}
		parts = append(parts, r.ByKey[k][v])
	}
	return strings.Join(parts, " ")
}

// prim describes a UI8Kit templ primitive.
type prim struct {
	pkg, fn, props string
	fields         map[string][2]string // js key -> Go field, kind (s b ps pb)
	void           bool
}

var commonAria = map[string][2]string{}

var prims = map[string]prim{
	"Box":        {pkg: "box", fn: "Box", props: "BoxProps", fields: map[string][2]string{"tag": {"Tag", "s"}}},
	"Text":       {pkg: "text", fn: "Text", props: "TextProps", fields: map[string][2]string{"tag": {"Tag", "s"}}},
	"Button":     {pkg: "button", fn: "Button", props: "ButtonProps", fields: map[string][2]string{"type": {"Type", "s"}, "form": {"Form", "s"}, "href": {"Href", "s"}, "disabled": {"Disabled", "b"}, "pressed": {"Pressed", "pb"}, "expanded": {"Expanded", "pb"}, "aria-label": {"AriaLabel", "s"}, "aria-controls": {"AriaControls", "s"}}},
	"Link":       {pkg: "link", fn: "Link", props: "LinkProps", fields: map[string][2]string{"href": {"Href", "s"}, "target": {"Target", "s"}, "rel": {"Rel", "s"}, "download": {"Download", "s"}, "aria-current": {"AriaCurrent", "s"}, "aria-label": {"AriaLabel", "s"}}},
	"Image":      {pkg: "image", fn: "Image", props: "ImageProps", void: true, fields: map[string][2]string{"src": {"Src", "s"}, "alt": {"Alt", "s"}, "srcset": {"SrcSet", "s"}, "sizes": {"Sizes", "s"}, "width": {"Width", "s"}, "height": {"Height", "s"}, "loading": {"Loading", "s"}, "decoding": {"Decoding", "s"}}},
	"Icon":       {pkg: "icon", fn: "Icon", props: "IconProps", fields: map[string][2]string{"size": {"Size", "s"}, "label": {"Label", "s"}}},
	"Input":      {pkg: "input", fn: "Input", props: "InputProps", void: true, fields: map[string][2]string{"type": {"Type", "s"}, "id": {"ID", "s"}, "name": {"Name", "s"}, "value": {"Value", "ps"}, "placeholder": {"Placeholder", "s"}, "min": {"Min", "s"}, "max": {"Max", "s"}, "step": {"Step", "s"}, "required": {"Required", "b"}, "disabled": {"Disabled", "b"}, "readonly": {"ReadOnly", "b"}, "aria-invalid": {"AriaInvalid", "s"}, "aria-describedby": {"AriaDescribedBy", "s"}}},
	"Checkbox":   {pkg: "checkbox", fn: "Checkbox", props: "CheckboxProps", void: true, fields: map[string][2]string{"id": {"ID", "s"}, "name": {"Name", "s"}, "value": {"Value", "ps"}, "checked": {"Checked", "b"}, "required": {"Required", "b"}, "disabled": {"Disabled", "b"}}},
	"Radio":      {pkg: "radio", fn: "Radio", props: "RadioProps", void: true, fields: map[string][2]string{"id": {"ID", "s"}, "name": {"Name", "s"}, "value": {"Value", "ps"}, "checked": {"Checked", "b"}, "required": {"Required", "b"}, "disabled": {"Disabled", "b"}}},
	"Select":     {pkg: "selectfield", fn: "Select", props: "SelectProps", fields: map[string][2]string{"options": {"Options", "opts"}, "id": {"ID", "s"}, "name": {"Name", "s"}, "value": {"Value", "s"}, "required": {"Required", "b"}, "disabled": {"Disabled", "b"}}},
	"Textarea":   {pkg: "textarea", fn: "Textarea", props: "TextareaProps", void: true, fields: map[string][2]string{"id": {"ID", "s"}, "name": {"Name", "s"}, "value": {"Value", "s"}, "placeholder": {"Placeholder", "s"}, "rows": {"Rows", "s"}, "required": {"Required", "b"}}},
	"Label":      {pkg: "label", fn: "Label", props: "LabelProps", fields: map[string][2]string{"for": {"For", "s"}}},
	"Form":       {pkg: "form", fn: "Form", props: "FormProps", fields: map[string][2]string{"id": {"ID", "s"}, "action": {"Action", "s"}, "method": {"Method", "s"}, "enctype": {"Enctype", "s"}, "novalidate": {"NoValidate", "b"}, "aria-label": {"AriaLabel", "s"}, "aria-labelledby": {"AriaLabelledBy", "s"}}},
	"Dialog":     {pkg: "dialog", fn: "Dialog", props: "DialogProps", fields: map[string][2]string{"id": {"ID", "s"}, "open": {"Open", "b"}, "aria-label": {"AriaLabel", "s"}, "aria-labelledby": {"AriaLabelledBy", "s"}}},
	"Disclosure": {pkg: "disclosure", fn: "Disclosure", props: "DisclosureProps", fields: map[string][2]string{"open": {"Open", "b"}, "name": {"Name", "s"}}},
	"Summary":    {pkg: "disclosure", fn: "Summary", props: "SummaryProps", fields: map[string][2]string{}},
	"Separator":  {pkg: "separator", fn: "Separator", props: "SeparatorProps", void: true, fields: map[string][2]string{"orientation": {"Orientation", "s"}, "decorative": {"Decorative", "b"}}},
}

// gen is the generator state.
type gen struct {
	specs   map[string]*Spec // by Name
	css     []cssRule
	cssSeen map[string]string
	icons   map[string]bool
	patches map[string]map[string]map[string]any
}

type cssRule struct{ name, apply, layer string }

func (g *gen) rule(name, apply string) {
	layer := "blocks"
	switch {
	case strings.HasPrefix(name, "mk-"):
		layer = "kit"
	case regexp.MustCompile(`-is\d+$`).MatchString(name):
		layer = "state"
	}
	apply = strings.Join(strings.Fields(apply), " ")
	if apply == "" {
		return
	}
	if prev, ok := g.cssSeen[name]; ok {
		if prev != apply {
			panic("class clash " + name + ": " + prev + " vs " + apply)
		}
		return
	}
	g.cssSeen[name] = apply
	g.css = append(g.css, cssRule{name: name, apply: apply, layer: layer})
}

// markers cannot be applied; they stay in the markup.
var markerRe = regexp.MustCompile(`^(group|peer)(/[\w-]+)?$`)

func splitMarkers(cls string) (apply string, markers []string) {
	var keep []string
	for _, c := range strings.Fields(cls) {
		if markerRe.MatchString(c) {
			markers = append(markers, c)
		} else {
			keep = append(keep, c)
		}
	}
	return strings.Join(keep, " "), markers
}

// blk is the per-spec emit context.
type blk struct {
	g       *gen
	spec    *Spec
	kebab   string
	imports map[string]bool
	b       strings.Builder
	ind     int
	props   map[string]*T
	order   []string
}

func (k *blk) line(f string, a ...any) {
	k.b.WriteString(strings.Repeat("\t", k.ind))
	fmt.Fprintf(&k.b, f, a...)
	k.b.WriteByte('\n')
}

func isMap(v any) (map[string]any, bool) { m, ok := v.(map[string]any); return m, ok }

func exprOf(v any) (string, bool) {
	if m, ok := isMap(v); ok {
		if s, ok := m["$"].(string); ok {
			return s, true
		}
	}
	return "", false
}

var goKeywords = map[string]bool{"props": true, "range": true, "type": true, "func": true, "select": true, "default": true, "map": true, "string": true, "len": true, "var": true, "go": true, "case": true, "chan": true, "const": true, "defer": true, "fallthrough": true, "goto": true, "import": true, "interface": true, "package": true, "struct": true, "switch": true, "true": true, "false": true, "nil": true, "templ": true}

func goVar(n string) string {
	if goKeywords[n] {
		return n + "_"
	}
	return n
}

func hrefLit(s string) string {
	if strings.HasPrefix(s, "#/") {
		return "/" + strings.TrimPrefix(s, "#/")
	}
	return s
}

// lit renders a JSON literal as Go of the field kind.
func litGo(v any, kind string) string {
	switch x := v.(type) {
	case string:
		switch kind {
		case "b":
			return strconv.FormatBool(x != "" && x != "false")
		case "ps":
			return "strPtr(" + goStr(x) + ")"
		}
		return goStr(hrefLit(x))
	case float64:
		s := strconv.FormatFloat(x, 'f', -1, 64)
		switch kind {
		case "b":
			return strconv.FormatBool(x != 0)
		case "ps":
			return "strPtr(" + goStr(s) + ")"
		case "n":
			return s
		}
		return goStr(s)
	case bool:
		switch kind {
		case "pb":
			return "boolPtr(" + strconv.FormatBool(x) + ")"
		case "s":
			return goStr(strconv.FormatBool(x))
		}
		return strconv.FormatBool(x)
	}
	panic(fmt.Sprintf("literal %v", v))
}

func (k *blk) exprGo(v any, kind string, sc *Scope) string {
	e, ok := exprOf(v)
	if !ok {
		return litGo(v, kind)
	}
	g := tr(parseExpr(e), sc)
	switch kind {
	case "b":
		return conv(g, tBool)
	case "pb":
		return "boolPtr(" + conv(g, tBool) + ")"
	case "ps":
		return "strPtr(" + conv(g, tString) + ")"
	case "s":
		return conv(g, tString)
	case "opts":
		return "mapSlice(" + g.S + ", func(x SelectItem) selectfield.SelectOptionItem { return selectfield.SelectOptionItem{Value: x.Value, Label: x.Label} })"
	}
	return g.S
}

// handler turns an fn body into data-action attributes.
var callRe = regexp.MustCompile(`^\s*(\w+)\s*(?:\?\.)?\((.*)\)\s*;?\s*$`)

func (k *blk) handler(event, body string, sc *Scope, attrs *[]string) {
	m := callRe.FindStringSubmatch(body)
	name := "run"
	arg := ""
	if m != nil {
		name, arg = m[1], strings.TrimSpace(m[2])
	}
	name = strings.TrimPrefix(name, "on")
	name = strings.ToLower(name[:1]) + name[1:]
	*attrs = append(*attrs, goStr("data-on"+event), goStr(k.kebab+"."+name))
	if arg != "" && !strings.Contains(arg, "$event") {
		func() {
			defer func() { _ = recover() }()
			g := tr(parseExpr(arg), sc)
			*attrs = append(*attrs, goStr("data-value"), conv(g, tString))
		}()
	}
}

// classOf computes the class expression of an element node and records
// the CSS rules. extra is the composition recipe; axes are dynamic axes.
func (k *blk) classOf(n map[string]any, recipe *Recipe, static map[string]string, dyn []dynAxis, sc *Scope) string {
	name := k.kebab
	if nn, _ := n["n"].(string); nn != "" && nn != "root" {
		name = k.kebab + "-" + nn
	}
	p, _ := isMap(n["p"])
	nodeCls := ""
	var conds [][2]string
	var dynCls []string
	if p != nil {
		switch c := p["class"].(type) {
		case string:
			nodeCls = c
		case map[string]any:
			if list, ok := c["cls"].([]any); ok {
				for _, it := range list {
					switch x := it.(type) {
					case string:
						nodeCls += " " + x
					case []any:
						conds = append(conds, [2]string{x[0].(string), x[1].(string)})
					case map[string]any:
						dynCls = append(dynCls, x["$"].(string))
					}
				}
			}
		}
	}
	var parts []string // Go string expressions
	var markers []string
	combos := combine(dyn)
	if len(combos) == 0 {
		merged := uiutils.Cn(strings.TrimSpace(recipe.classes(static) + " " + nodeCls))
		apply, mk := splitMarkers(merged)
		markers = append(markers, mk...)
		if apply != "" {
			k.g.rule(name, apply)
			parts = append(parts, goStr(name))
		}
	} else {
		for _, combo := range combos {
			sel := map[string]string{}
			for kk, vv := range static {
				sel[kk] = vv
			}
			suffix := ""
			for i, d := range dyn {
				sel[d.axis] = combo[i]
				suffix += "-" + combo[i]
			}
			merged := uiutils.Cn(strings.TrimSpace(recipe.classes(sel) + " " + nodeCls))
			apply, mk := splitMarkers(merged)
			markers = mk
			k.g.rule(name+suffix, apply)
		}
		expr := goStr(name)
		for _, d := range dyn {
			expr += " + \"-\" + " + d.goExpr
		}
		parts = append(parts, expr)
	}
	for _, m := range uniq(markers) {
		parts = append([]string{goStr(m)}, parts...)
	}
	cx := []string{}
	for j, c := range conds {
		cn := fmt.Sprintf("%s-is%d", name, j+1)
		apply, mk := splitMarkers(uiutils.Cn(c[1]))
		k.g.rule(cn, apply)
		cond := truthyExpr(c[0], sc)
		cls := []string{}
		for _, m := range mk {
			cls = append(cls, m)
		}
		if apply != "" {
			cls = append(cls, cn)
		}
		cx = append(cx, cond, goStr(strings.Join(cls, " ")))
	}
	for _, d := range dynCls {
		parts = append(parts, conv(tr(parseExpr(d), sc), tString))
	}
	if len(parts) == 0 && len(cx) == 0 {
		return ""
	}
	if len(cx) == 0 && len(parts) == 1 {
		return parts[0]
	}
	if len(cx) == 0 {
		return "join(" + strings.Join(parts, ", ") + ")"
	}
	return "cx(" + firstOr(parts) + ", " + strings.Join(cx, ", ") + ")"
}

func firstOr(parts []string) string {
	if len(parts) == 0 {
		return `""`
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "join(" + strings.Join(parts, ", ") + ")"
}

func truthyExpr(e string, sc *Scope) string { return truthy(tr(parseExpr(e), sc)) }

type dynAxis struct {
	axis   string
	values []string
	goExpr string
}

func combine(d []dynAxis) [][]string {
	if len(d) == 0 {
		return nil
	}
	out := [][]string{{}}
	for _, a := range d {
		var next [][]string
		for _, o := range out {
			for _, v := range a.values {
				next = append(next, append(append([]string{}, o...), v))
			}
		}
		out = next
	}
	return out
}

// attrsGo builds the Attrs expression from key/value Go pairs.
func attrsGo(pairs []string) string {
	if len(pairs) == 0 {
		return ""
	}
	return "attrs(" + strings.Join(pairs, ", ") + ")"
}

func (k *blk) emitNodes(nodes []any, sc *Scope) {
	for _, n := range nodes {
		k.emitNode(n.(map[string]any), sc)
	}
}

func (k *blk) emitNode(n map[string]any, sc *Scope) {
	switch {
	case n["t"] != nil:
		k.element(n, sc)
	case n["text"] != nil:
		if s, ok := n["text"].(string); ok {
			k.line("{ %s }", goStr(s))
			return
		}
		e, _ := exprOf(n["text"])
		g := tr(parseExpr(e), sc)
		k.line("{ %s }", conv(g, tString))
	case n["each"] != nil:
		g := tr(parseExpr(n["each"].(string)), sc)
		el := resolve(g.T).Elem
		if el == nil {
			el = tAny
		}
		as := n["as"].(string)
		ix, _ := n["i"].(string)
		child := sc.child()
		av := goVar(as)
		child.vars[as] = binding{Go: av, T: el}
		iv := "_"
		if ix != "" {
			iv = goVar(ix)
			child.vars[ix] = binding{Go: iv, T: tNumber}
		}
		// render body first to see which loop variables are used
		sub := &blk{g: k.g, spec: k.spec, kebab: k.kebab, imports: k.imports, ind: k.ind + 1, props: k.props}
		sub.emitNodes(n["c"].([]any), child)
		body := sub.b.String()
		used := func(v string) bool { return regexp.MustCompile(`\b` + regexp.QuoteMeta(v) + `\b`).MatchString(body) }
		if !used(av) {
			av = "_"
		}
		if iv != "_" && !used(iv) {
			iv = "_"
		}
		if av == "_" && iv == "_" {
			k.line("for range %s {", g.S)
		} else if av == "_" {
			k.line("for %s := range %s {", iv, g.S)
		} else {
			k.line("for %s, %s := range %s {", iv, av, g.S)
		}
		k.b.WriteString(body)
		k.line("}")
	case n["if"] != nil:
		k.line("if %s {", truthyExpr(n["if"].(string), sc))
		k.ind++
		k.emitNodes(n["c"].([]any), sc)
		k.ind--
		if els, ok := n["else"].([]any); ok && len(els) > 0 {
			k.line("} else {")
			k.ind++
			k.emitNodes(els, sc)
			k.ind--
		}
		k.line("}")
	case n["slot"] != nil:
		s := n["slot"].(string)
		if s == "children" {
			k.line("{ children... }")
		} else {
			k.line("if props.%s != nil {", pascal(s))
			k.line("\t@props.%s", pascal(s))
			k.line("}")
		}
	default:
		panic(fmt.Sprintf("%s: unknown node %v", k.spec.ID, n))
	}
}

func (k *blk) patch(n map[string]any) map[string]any {
	p, _ := isMap(n["p"])
	nn, _ := n["n"].(string)
	if nn == "" {
		nn = "root"
	}
	if ps, ok := k.g.patches[k.spec.ID][nn]; ok {
		out := map[string]any{}
		for kk, v := range p {
			out[kk] = v
		}
		for kk, v := range ps {
			if v == nil {
				delete(out, kk)
			} else {
				out[kk] = v
			}
		}
		return out
	}
	return p
}

func sortedKeys[M ~map[string]V, V any](m M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (k *blk) element(n map[string]any, sc *Scope) {
	t := n["t"].(string)
	p := k.patch(n)
	if p == nil {
		p = map[string]any{}
	}
	n = map[string]any{"t": t, "p": p, "n": n["n"], "c": n["c"]}
	kids, _ := n["c"].([]any)
	if t == "Glyph" {
		k.icon(p["name"], sc)
		return
	}
	if strings.HasPrefix(t, "kit.") {
		k.primitive(strings.TrimPrefix(t, "kit."), n, p, nil, nil, nil, kids, sc, "")
		return
	}
	s, ok := k.g.specs[t]
	if !ok {
		panic(k.spec.ID + ": unknown component " + t)
	}
	if s.Kind == "composition" {
		k.composition(s, n, p, kids, sc)
		return
	}
	k.child(s, n, p, kids, sc)
}

// composition inlines a composition onto its primitive.
func (k *blk) composition(c *Spec, n, p map[string]any, kids []any, sc *Scope) {
	root := c.Root[0].(map[string]any)
	primName := strings.TrimPrefix(root["t"].(string), "kit.")
	rp, _ := isMap(root["p"])
	static := map[string]string{}
	var dyn []dynAxis
	isKey := map[string]bool{}
	if c.Recipe != nil {
		for _, key := range c.Recipe.Keys {
			isKey[key] = true
		}
	}
	params := map[string]any{} // composition declared props
	rest := map[string]any{}
	for _, key := range sortedKeys(p) {
		v := p[key]
		switch {
		case key == "class":
		case isKey[key]:
			if s, ok := v.(string); ok {
				static[key] = s
			} else if e, ok := exprOf(v); ok {
				ex := parseExpr(e)
				vals := literals(ex, sc)
				if vals == nil {
					for vv := range c.Recipe.ByKey[key] {
						vals = append(vals, vv)
					}
					sort.Strings(vals)
				}
				g := tr(ex, sc)
				gs := conv(g, tString)
				if def := c.Recipe.Defaults[key]; def != "" {
					gs = "or(" + gs + ", " + goStr(def) + ")"
				}
				dyn = append(dyn, dynAxis{axis: key, values: vals, goExpr: gs})
			}
			if rp != nil {
				if e, ok := exprOf(rp[key]); ok && e == key {
					params[key] = v
				}
			}
		case c.Props[key].Type != "":
			params[key] = v
		default:
			rest[key] = v
		}
	}
	// kit props from the composition root
	kp := map[string]any{}
	for kk, v := range rp {
		if e, ok := exprOf(v); ok {
			switch e {
			case "cls", "rest":
				continue
			case "level":
				if as, ok := params["as"]; ok {
					if f, ok := as.(float64); ok {
						kp["tag"] = "h" + strconv.Itoa(int(f))
					} else if s, ok := as.(string); ok {
						kp["tag"] = "h" + s
					} else if e2, ok := exprOf(as); ok {
						kp["tag"] = map[string]any{"$": "'h' + " + e2}
					}
				}
				continue
			}
			if pv, ok := params[e]; ok {
				kp[kk] = pv
			}
			continue
		}
		kp[kk] = v
	}
	for kk, v := range rest {
		kp[kk] = v
	}
	if c.Name == "Icon" {
		kp["name"] = params["name"]
	}
	k.primitive(primName, n, kp, c.Recipe, static, dyn, kids, sc, "")
}

func (k *blk) icon(name any, sc *Scope) {
	if s, ok := name.(string); ok {
		k.g.icons[s] = true
		k.line("@Glyph(%s)", goStr(s))
		return
	}
	e, _ := exprOf(name)
	ex := parseExpr(e)
	if vals := literals(ex, sc); vals != nil {
		for _, v := range vals {
			k.g.icons[v] = true
		}
	}
	k.line("@Glyph(%s)", conv(tr(ex, sc), tString))
}

// primitive writes a primitive call.
func (k *blk) primitive(name string, n, p map[string]any, recipe *Recipe, static map[string]string, dyn []dynAxis, kids []any, sc *Scope, _ string) {
	pr, ok := prims[name]
	if !ok {
		panic(k.spec.ID + ": no primitive " + name)
	}
	k.imports[primImport(pr.pkg)] = true
	var fields []string
	var attrs []string
	if cls := k.classOf(n, recipe, static, dyn, sc); cls != "" {
		fields = append(fields, "Class: "+cls)
	}
	var glyph any
	for _, key := range sortedKeys(p) {
		v := p[key]
		if key == "class" || key == "..." {
			continue
		}
		if name == "Icon" && key == "name" {
			glyph = v
			continue
		}
		if m, ok := isMap(v); ok {
			if body, ok := m["fn"].(string); ok {
				k.handler(strings.TrimPrefix(key, "on"), body, sc, &attrs)
				continue
			}
		}
		if f, ok := pr.fields[key]; ok {
			fields = append(fields, f[0]+": "+k.exprGo(v, f[1], sc))
			continue
		}
		// everything else is an attribute
		if s, ok := v.(string); ok {
			attrs = append(attrs, goStr(key), goStr(hrefLit(s)))
		} else if f, ok := v.(float64); ok {
			attrs = append(attrs, goStr(key), goStr(strconv.FormatFloat(f, 'f', -1, 64)))
		} else if b, ok := v.(bool); ok {
			attrs = append(attrs, goStr(key), strconv.FormatBool(b))
		} else if e, ok := exprOf(v); ok {
			g := tr(parseExpr(e), sc)
			if g.T.K == "bool" {
				attrs = append(attrs, goStr(key), g.S)
			} else {
				attrs = append(attrs, goStr(key), conv(g, tString))
			}
		}
	}
	if a := attrsGo(attrs); a != "" {
		fields = append(fields, "Attrs: "+a)
	}
	call := fmt.Sprintf("@%s.%s(%s.%s{%s})", pr.pkg, pr.fn, pr.pkg, pr.props, strings.Join(fields, ", "))
	if pr.void || (len(kids) == 0 && glyph == nil) {
		k.line("%s", call)
		return
	}
	k.line("%s {", call)
	k.ind++
	if glyph != nil {
		k.icon(glyph, sc)
	}
	k.emitNodes(kids, sc)
	k.ind--
	k.line("}")
}

// child renders a nested block or part.
func (k *blk) child(s *Spec, n, p map[string]any, kids []any, sc *Scope) {
	var fields []string
	cprops := propTypes(s)
	for _, key := range sortedKeys(p) {
		v := p[key]
		ct, ok := cprops[key]
		if !ok || ct.K == "func" {
			continue
		}
		if m, ok := isMap(v); ok && m["fn"] != nil {
			continue
		}
		fields = append(fields, pascal(key)+": "+k.valueAs(v, ct, sc))
	}
	call := fmt.Sprintf("@%s(%sProps{%s})", s.Name, s.Name, strings.Join(fields, ", "))
	if len(kids) == 0 {
		k.line("%s", call)
		return
	}
	k.line("%s {", call)
	k.ind++
	k.emitNodes(kids, sc)
	k.ind--
	k.line("}")
}

// valueAs converts a prop value to the child prop type.
func (k *blk) valueAs(v any, ct *T, sc *Scope) string {
	e, ok := exprOf(v)
	if !ok {
		switch ct.K {
		case "number":
			return litGo(v, "n")
		case "bool":
			return litGo(v, "b")
		case "array":
			if l, ok := v.([]any); ok && len(l) == 0 {
				return "nil"
			}
		}
		return litGo(v, "s")
	}
	g := tr(parseExpr(e), sc)
	return convTo(g, ct)
}

func convTo(g G, ct *T) string {
	switch ct.K {
	case "string", "lits", "strnum":
		return conv(g, tString)
	case "bool":
		return conv(g, tBool)
	case "number":
		return g.S
	case "object":
		src := resolve(g.T)
		if src.K == "object" && src.Name != ct.Name {
			var fs []string
			for _, f := range ct.Fields {
				if sf, ok := g.T.field(f.Name); ok {
					fs = append(fs, pascal(f.Name)+": "+convTo(G{g.S + "." + pascal(f.Name), sf.T}, f.T))
				}
			}
			return ct.Name + "{" + strings.Join(fs, ", ") + "}"
		}
	case "named":
		if ct.Ptr && !g.T.Ptr {
			return "&" + g.S
		}
		if !ct.Ptr && g.T.Ptr {
			return "deref(" + g.S + ")"
		}
	case "array":
		se, de := resolve(g.T).Elem, ct.Elem
		if se != nil && de != nil && resolve(se).K == "object" && resolve(de).K == "object" && resolve(se).Name != resolve(de).Name {
			var fs []string
			for _, f := range resolve(de).Fields {
				if sf, ok := se.field(f.Name); ok {
					fs = append(fs, pascal(f.Name)+": "+convTo(G{"x." + pascal(f.Name), sf.T}, f.T))
				}
			}
			return fmt.Sprintf("mapSlice(%s, func(x %s) %s { return %s{%s} })", g.S, goType(se), goType(de), goType(de), strings.Join(fs, ", "))
		}
	}
	return g.S
}

var propCache = map[string]map[string]*T{}

func propTypes(s *Spec) map[string]*T {
	if m, ok := propCache[s.ID]; ok {
		return m
	}
	m := map[string]*T{}
	for _, name := range sortedKeys(s.Props) {
		d := s.Props[name]
		t := parseType(d.Type, s.Name+pascal(name))
		if d.Optional && t.K == "named" {
			c := *t
			c.Ptr = true
			t = &c
		}
		m[name] = t
	}
	propCache[s.ID] = m
	return m
}

// emitBlock renders one block or part.
func (g *gen) emitBlock(s *Spec) (out string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s: %v", s.ID, r)
		}
	}()
	k := &blk{g: g, spec: s, kebab: strings.TrimPrefix(strings.TrimPrefix(s.ID, "block."), "part."), imports: map[string]bool{}}
	pt := propTypes(s)
	sc := &Scope{vars: map[string]binding{}}
	var fields []string
	for _, name := range sortedKeys(pt) {
		t := pt[name]
		if t.K == "func" {
			continue
		}
		sc.vars[name] = binding{Go: "props." + pascal(name), T: t}
		fields = append(fields, fmt.Sprintf("\t%s %s", pascal(name), goType(t)))
	}
	for _, sl := range s.Slots {
		fields = append(fields, fmt.Sprintf("\t%s templ.Component", pascal(sl)))
	}
	for _, dn := range sortedKeys(s.Derive) {
		gx := tr(parseExpr(s.Derive[dn]), sc)
		sc.vars[dn] = binding{Go: gx.S, T: gx.T}
	}
	k.ind = 1
	k.emitNodes(s.Root, sc)
	body := k.b.String()
	var b strings.Builder
	b.WriteString("// " + header + "\n\npackage design\n\n")
	imps := sortedKeys(k.imports)
	for _, std := range []string{"strings", "strconv", "slices"} {
		if strings.Contains(body, std+".") {
			imps = append([]string{std}, imps...)
		}
	}
	if len(imps) > 0 {
		b.WriteString("import (\n")
		for _, i := range imps {
			fmt.Fprintf(&b, "\t%q\n", i)
		}
		b.WriteString(")\n\n")
	}
	fmt.Fprintf(&b, "// %sProps configures %s.\ntype %sProps struct {\n%s\n}\n\n", s.Name, s.Name, s.Name, strings.Join(fields, "\n"))
	fmt.Fprintf(&b, "// %s: %s\ntempl %s(props %sProps) {\n%s}\n", s.Name, oneLine(s.Docs), s.Name, s.Name, body)
	return b.String(), nil
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// compositions for screens: runtime recipe classes mk-<name>-...
func (g *gen) emitCompositions(list []*Spec) string {
	var b strings.Builder
	b.WriteString("// " + header + "\n\npackage design\n\n")
	imps := map[string]bool{}
	var body strings.Builder
	for _, c := range list {
		root := c.Root[0].(map[string]any)
		pn := strings.TrimPrefix(root["t"].(string), "kit.")
		pr := prims[pn]
		imps[primImport(pr.pkg)] = true
		kb := "mk-" + kebabName(c.Name)
		kbMarkers := ""
		var fs []string
		var sel []string
		if c.Recipe != nil {
			apply, mk := splitMarkers(uiutils.Cn(c.Recipe.Base))
			g.rule(kb, apply)
			kbMarkers = strings.Join(mk, " ")
			for _, key := range c.Recipe.Keys {
				fs = append(fs, fmt.Sprintf("\t%s string", pascal(key)))
				for _, v := range sortedKeys(c.Recipe.ByKey[key]) {
					cn := kb + "-" + key + "-" + v
					if key == "variant" {
						cn = kb + "-" + v
					}
					apply, _ := splitMarkers(uiutils.Cn(c.Recipe.ByKey[key][v]))
					g.rule(cn, apply)
				}
				pfx := `"` + kb + "-" + key + `-"`
				if key == "variant" {
					pfx = `"` + kb + `-"`
				}
				sel = append(sel, fmt.Sprintf("%s + or(p.%s, %q)", pfx, pascal(key), c.Recipe.Defaults[key]))
			}
		}
		rp, _ := isMap(root["p"])
		inner := []string{}
		for _, key := range sortedKeys(rp) {
			v := rp[key]
			e, isE := exprOf(v)
			if !isE {
				if f, ok := pr.fields[key]; ok {
					inner = append(inner, fmt.Sprintf("in.%s = %s", f[0], litGo(v, f[1])))
				} else {
					inner = append(inner, fmt.Sprintf("in.Attrs = withAttr(in.Attrs, %q, %s)", key, litGo(v, "s")))
				}
				continue
			}
			switch e {
			case "cls", "rest":
				continue
			case "level":
				fs = append(fs, "\tAs int")
				inner = append(inner, `in.Tag = "h" + strconv.Itoa(or(p.As, 2))`)
				continue
			case "name":
				fs = append(fs, "\tName string")
				continue
			}
			f, ok := pr.fields[key]
			if !ok {
				continue
			}
			isRecipe := c.Recipe != nil && c.Recipe.ByKey[e] != nil
			if !isRecipe {
				fs = append(fs, fmt.Sprintf("\t%s %s", pascal(e), map[string]string{"s": "string", "b": "bool"}[f[1]]))
			}
			inner = append(inner, fmt.Sprintf("in.%s = p.%s", f[0], pascal(e)))
		}
		if c.Name == "Icon" {
			fs = append(fs, "\tName string")
		}
		fs = append(fs, "\tClass string", fmt.Sprintf("\t%s.%s", pr.pkg, pr.props))
		fmt.Fprintf(&body, "// %sProps configures %s: %s\ntype %sProps struct {\n%s\n}\n\n", c.Name, c.Name, oneLine(c.Docs), c.Name, strings.Join(fs, "\n"))
		clsExpr := `p.Class`
		if c.Recipe != nil {
			clsExpr = fmt.Sprintf("join(%s)", strings.Join(append(append([]string{fmt.Sprintf("%q", strings.TrimSpace(kb+" "+kbMarkers))}, sel...), "p.Class"), ", "))
		}
		fmt.Fprintf(&body, "func %sInner(p %sProps) %s.%s {\n\tin := p.%s\n\tin.Class = %s\n", lower(c.Name), c.Name, pr.pkg, pr.props, pr.props, clsExpr)
		for _, l := range inner {
			fmt.Fprintf(&body, "\t%s\n", l)
		}
		body.WriteString("\treturn in\n}\n\n")
		fmt.Fprintf(&body, "// %s renders the composition.\ntempl %s(p %sProps) {\n", c.Name, c.Name, c.Name)
		switch {
		case c.Name == "Icon":
			fmt.Fprintf(&body, "\t@%s.%s(%sInner(p)) {\n\t\t@Glyph(p.Name)\n\t}\n", pr.pkg, pr.fn, lower(c.Name))
		case pr.void:
			fmt.Fprintf(&body, "\t@%s.%s(%sInner(p))\n", pr.pkg, pr.fn, lower(c.Name))
		default:
			fmt.Fprintf(&body, "\t@%s.%s(%sInner(p)) {\n\t\t{ children... }\n\t}\n", pr.pkg, pr.fn, lower(c.Name))
		}
		body.WriteString("}\n\n")
	}
	b.WriteString("import (\n\t\"strconv\"\n")
	for _, i := range sortedKeys(imps) {
		fmt.Fprintf(&b, "\t%q\n", i)
	}
	b.WriteString(")\n\nvar _ = strconv.Itoa\n\n")
	b.WriteString(body.String())
	return b.String()
}

func lower(s string) string { return strings.ToLower(s[:1]) + s[1:] }

func kebabName(s string) string {
	return strings.ToLower(regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(s, "$1-$2"))
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func primImport(pkg string) string {
	if pkg == "selectfield" {
		pkg = "select"
	}
	return "github.com/ui8kit/codegen/generated/templ/ui/" + pkg
}
