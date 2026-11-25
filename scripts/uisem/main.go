// Command uisem writes the CSS of the UI8Kit semantic class mode (ADR-0029). Build time only.
//
//	go run ./scripts/uisem           write the files below
//	go run ./scripts/uisem -check    fail when a committed file differs from a fresh build
//
// Outputs (committed):
//
//	ui/styles/generated/kit.<skin>.css  @layer components.kit: one @apply rule per ui-* name, from
//	                                    compose.SemanticRules() (ui/kit/compose/compose.json), plus the
//	                                    per-element token hooks of kit.css moved onto the ui-* rules
//	ui/styles/generated/kit-layered.css ui/styles/kit.css (verbatim upstream file) with its unlayered rules
//	                                    moved into @layer utilities; hooks keyed on data-slot skip ui-* elements
//	.ui8px/patterns.json                the ui-* names and their utilities (ui8px UI8PX005 registry)
//
// Only the recipes of the compositions the app calls are emitted (compose.<Name>( in usedIn; compositions map
// to recipes in compose.json), so the stylesheet does not carry the whole kit.
//
// Classes Tailwind cannot apply with a skin (tokens the skin does not define) are dropped from that skin's file
// and listed in its header; in the utilities mode they render no CSS either. Needs the Tailwind CLI (npm ci).
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ui8kit/codegen/generated/templ/compose"
	uiutils "github.com/ui8kit/codegen/generated/templ/utils"
)

const (
	kitCSS     = "ui/styles/kit.css"
	outKit     = "ui/styles/generated/kit.%s.css"
	outLayered = "ui/styles/generated/kit-layered.css"
	outPattern = ".ui8px/patterns.json"
)

// usedIn: the templates scanned for compose.<Name>( calls
var usedIn = []string{"internal/web/screens", "ui/kit/blocks", "ui/kit/compose"}

const composeJSON = "ui/kit/compose/compose.json"

// skins: one generated kit CSS each (ui/styles/entries/<skin>.css imports it)
var skins = []string{"default", "godudy", "storeee"}

// hook is a per-element token of kit.css (UI8KIT-2.0 §11): [data-slot=slot] plus the utility classes in when. On
// the ui-* rules of the recipes whose compositions set that data-slot, a rule that applies every class in when gets
// a companion rule right after it, <selector>:where([data-slot=slot]) { decl }: same specificity, so it wins over
// the rule it extends and loses to the later value rules, as the utility-keyed hook did against the merge (an
// unset token falls back to the utility's own value).
type hook struct {
	slot string
	when []string
	decl string
}

// The hooks of ui/styles/kit.css, keyed on the ui-* rules instead of utility classes.
var hooks = []hook{
	{"button", []string{"rounded"}, "border-radius: var(--radius-button, var(--radius));"},
	{"button", []string{"px-4", "h-10"}, "padding-inline: var(--pad-button, calc(var(--spacing) * 4)); height: var(--h-button, calc(var(--spacing) * 10));"},
	{"card", []string{"rounded"}, "border-radius: var(--radius-card, var(--radius));"},
	{"card", []string{"p-6"}, "padding: var(--pad-card, calc(var(--spacing) * 6));"},
	{"card", nil, "--tw-shadow: var(--shadow-card, 0 0 #0000);"},
	{"alert", []string{"rounded"}, "border-radius: var(--radius-alert, var(--radius));"},
	{"alert", []string{"px-2"}, "padding-inline: var(--pad-alert, calc(var(--spacing) * 2));"},
	{"alert", []string{"min-h-8"}, "min-height: var(--min-alert, calc(var(--spacing) * 8));"},
	{"alert", nil, "--tw-shadow: var(--shadow-alert, 0 0 #0000);"},
	{"badge", []string{"rounded-full"}, "border-radius: var(--radius-badge, calc(infinity * 1px));"},
	{"badge", []string{"px-3", "h-6"}, "padding-inline: var(--pad-badge, calc(var(--spacing) * 3)); height: var(--h-badge, calc(var(--spacing) * 6));"},
}

type rule struct {
	compose.SemanticRule
	hooks []string // selector:where([data-slot]) { decl } rules that follow this one
}

func main() {
	check := flag.Bool("check", false, "fail when the committed files are not up to date")
	flag.Parse()
	files, err := build()
	if err != nil {
		fmt.Fprintln(os.Stderr, "uisem:", err)
		os.Exit(2)
	}
	stale := 0
	for _, f := range sortedKeys(files) {
		if *check {
			old, _ := os.ReadFile(f)
			if !bytes.Equal(old, files[f]) {
				fmt.Fprintf(os.Stderr, "uisem: %s is not up to date (run: go run ./scripts/uisem)\n", f)
				stale++
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "uisem:", err)
			os.Exit(2)
		}
		if err := os.WriteFile(f, files[f], 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "uisem:", err)
			os.Exit(2)
		}
	}
	if stale > 0 {
		os.Exit(1)
	}
	if !*check {
		fmt.Printf("uisem: wrote %d files\n", len(files))
	}
}

func build() (map[string][]byte, error) {
	used, err := usedRecipes()
	if err != nil {
		return nil, err
	}
	var rules []rule
	for _, r := range compose.SemanticRules() {
		if !used[r.Recipe] {
			continue
		}
		// the runtime merge resolved conflicts inside one class list; @apply would keep both, so resolve them here
		r.Apply = uiutils.Cn(r.Apply)
		rules = append(rules, rule{SemanticRule: r})
	}
	slots, err := recipeSlots()
	if err != nil {
		return nil, err
	}
	for i := range rules {
		toks := map[string]bool{}
		for _, t := range strings.Fields(rules[i].Apply) {
			toks[t] = true
		}
		for _, h := range hooks {
			if !slots[rules[i].Recipe][h.slot] {
				continue
			}
			all := true
			for _, w := range h.when {
				all = all && toks[w]
			}
			// a hook without classes (a shadow token) belongs to the base rule only
			if h.when == nil {
				all = rules[i].Selector == ".ui-"+rules[i].Recipe
			}
			if all {
				rules[i].hooks = append(rules[i].hooks, fmt.Sprintf("%s:where([data-slot=%q]) { %s }", rules[i].Selector, h.slot, h.decl))
			}
		}
	}
	if !hasRoot(rules, "card") || !hasRoot(rules, "button") {
		return nil, fmt.Errorf("no ui-card / ui-button rule: is compose.json the UI8Kit recipe set?")
	}
	src, err := os.ReadFile(kitCSS)
	if err != nil {
		return nil, err
	}
	rootOf, err := roots(rules)
	if err != nil {
		return nil, err
	}
	layered, err := layerKitCSS(string(src), rootOf)
	if err != nil {
		return nil, err
	}
	pat, err := patterns(rules)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{outLayered: []byte(layered), outPattern: pat}
	for _, skin := range skins {
		rs := append([]rule(nil), rules...)
		dropped := map[string]bool{}
		for {
			bad, err := probe(skin, layered, render(rs, nil))
			if err != nil {
				return nil, err
			}
			if bad == "" {
				break
			}
			if dropped[bad] {
				return nil, fmt.Errorf("probe %s: %q still unknown after dropping it", skin, bad)
			}
			dropped[bad] = true
			for i := range rs {
				rs[i].Apply = without(rs[i].Apply, bad)
			}
		}
		kept := rs[:0:0]
		for _, r := range rs {
			if r.Apply != "" || len(r.hooks) > 0 {
				kept = append(kept, r)
			}
		}
		files[fmt.Sprintf(outKit, skin)] = []byte(render(kept, sortedKeys(dropped)))
	}
	return files, nil
}

// usedRecipes: the recipes of the compositions called in usedIn.
func usedRecipes() (map[string]bool, error) {
	d, err := compositions()
	if err != nil {
		return nil, err
	}
	call := regexp.MustCompile(`\bcompose\.([A-Z]\w*)\(`)
	used := map[string]bool{}
	for _, dir := range usedIn {
		files, err := filepath.Glob(filepath.Join(dir, "*.templ"))
		if err != nil {
			return nil, err
		}
		for _, f := range files {
			src, err := os.ReadFile(f)
			if err != nil {
				return nil, err
			}
			for _, m := range call.FindAllSubmatch(src, -1) {
				if c, ok := d[string(m[1])]; ok && c.Recipe != "" {
					used[c.Recipe] = true
				}
			}
		}
	}
	return used, nil
}

func hasRoot(rs []rule, id string) bool {
	for _, r := range rs {
		if r.Selector == ".ui-"+id {
			return true
		}
	}
	return false
}

// recipeSlots: recipe id -> the data-slot values its compositions set (compose.json fixedAttrs)
func recipeSlots() (map[string]map[string]bool, error) {
	d, err := compositions()
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, c := range d {
		if s := c.FixedAttrs["data-slot"]; s != "" && c.Recipe != "" {
			if out[c.Recipe] == nil {
				out[c.Recipe] = map[string]bool{}
			}
			out[c.Recipe][s] = true
		}
	}
	return out, nil
}

// roots: data-slot value -> the ui-* root classes of the recipes whose compositions set it
func roots(rs []rule) (map[string][]string, error) {
	slots, err := recipeSlots()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := map[string][]string{}
	for _, r := range rs {
		if seen[r.Recipe] {
			continue
		}
		seen[r.Recipe] = true
		for s := range slots[r.Recipe] {
			out[s] = append(out[s], ".ui-"+r.Recipe)
		}
	}
	for s := range out {
		sort.Strings(out[s])
	}
	return out, nil
}

type composition struct {
	Recipe     string            `json:"recipe"`
	FixedAttrs map[string]string `json:"fixedAttrs"`
}

func compositions() (map[string]composition, error) {
	b, err := os.ReadFile(composeJSON)
	if err != nil {
		return nil, err
	}
	var d struct {
		Compositions map[string]composition `json:"compositions"`
	}
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, err
	}
	return d.Compositions, nil
}

func without(list, tok string) string {
	var out []string
	for _, t := range strings.Fields(list) {
		if t != tok {
			out = append(out, t)
		}
	}
	return strings.Join(out, " ")
}

func render(rs []rule, dropped []string) string {
	var b strings.Builder
	b.WriteString("/* Code generated by scripts/uisem from ui/kit/compose/compose.json (compose.SemanticRules). DO NOT EDIT.\n")
	b.WriteString("   UI8Kit semantic class mode (ADR-0029): ui-<recipe> carries the base and the defaults, a non-default value adds\n")
	b.WriteString("   ui-<recipe>-<value> (variant) or ui-<recipe>-<axis>-<value>. Layer order: theme < base < components.kit <\n")
	b.WriteString("   components.brand < utilities (declared in ui/styles/entries/*.css). */\n")
	if len(dropped) > 0 {
		b.WriteString("/* Not applied (this skin does not define them; the utilities mode renders no CSS for them either): " + strings.Join(dropped, " ") + " */\n")
	}
	b.WriteString("@layer components.kit {\n")
	for _, r := range rs {
		if r.Apply != "" {
			b.WriteString("  " + r.Selector + " { @apply " + r.Apply + "; }\n")
		}
		for _, h := range r.hooks {
			b.WriteString("  " + h + "\n")
		}
	}
	b.WriteString("}\n")
	return b.String()
}

var unknownRE = regexp.MustCompile("Cannot apply unknown utility class `([^`]+)`")

// probe compiles a scratch entry of one skin with the candidate kit.css; it returns the first class Tailwind
// cannot apply, or "".
func probe(skin, layered, kit string) (string, error) {
	dir, err := os.MkdirTemp("ui/styles", ".uisem-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	entry := fmt.Sprintf("@layer theme, base, components, utilities;\n@layer components { @layer kit, brand; }\n@import \"../skins/%s.css\";\n@import \"./kit-layered.css\";\n@import \"./kit.css\";\n@source not \"../../..\";\n", skin)
	for name, body := range map[string]string{"entry.css": entry, "kit-layered.css": layered, "kit.css": kit} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			return "", err
		}
	}
	cmd := exec.Command("npx", "--no-install", "tailwindcss", "-i", filepath.Join(dir, "entry.css"), "-o", filepath.Join(dir, "out.css"))
	out, err := cmd.CombinedOutput()
	if m := unknownRE.FindSubmatch(out); m != nil {
		return string(m[1]), nil
	}
	if err != nil {
		return "", fmt.Errorf("tailwindcss (%s): %v\n%s", skin, err, out)
	}
	return "", nil
}

// layerKitCSS keeps @layer, @utility and other at-rule blocks of kit.css as they are and moves every top-level
// style rule into one @layer utilities block (same specificity, after Tailwind's utilities, so the outcome
// against utility classes is unchanged; unlayered CSS would beat every layer, the app brand layer included).
// A rule whose selectors start with [data-slot="x"] for a recipe x with a ui-* root skips the ui-* elements:
// their token hooks live on the generated ui-* rules, where the brand layer can override them.
func layerKitCSS(src string, rootOf map[string][]string) (string, error) {
	blocks, err := topLevel(src)
	if err != nil {
		return "", err
	}
	var keep, moved strings.Builder
	slotRE := regexp.MustCompile(`\[data-slot="([a-z-]+)"\]`)
	for _, blk := range blocks {
		s := strings.TrimSpace(blk)
		if s == "" {
			continue
		}
		if strings.HasPrefix(s, "/*") || strings.HasPrefix(s, "@") {
			keep.WriteString(s + "\n")
			continue
		}
		open := strings.Index(s, "{")
		sel, body := s[:open], s[open:]
		parts := strings.Split(sel, ",")
		for i, p := range parts {
			p = strings.TrimSpace(p)
			if m := slotRE.FindStringSubmatch(p); m != nil && strings.HasPrefix(p, m[0]) {
				if rs, ok := rootOf[m[1]]; ok {
					p = m[0] + ":not(" + strings.Join(rs, ", ") + ")" + p[len(m[0]):]
				}
			}
			parts[i] = p
		}
		moved.WriteString("  " + strings.Join(parts, ", ") + " " + body + "\n")
	}
	return "/* Code generated by scripts/uisem from ui/styles/kit.css (verbatim upstream file). DO NOT EDIT.\n" +
		"   Same rules; the unlayered style rules are moved into @layer utilities (ADR-0029). */\n" +
		keep.String() + "@layer utilities {\n" + moved.String() + "}\n", nil
}

// topLevel splits CSS into top-level statements (comments, at-rules with their block, style rules).
func topLevel(src string) ([]string, error) {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("kit.css: open comment")
			}
			if depth == 0 {
				if pre := strings.TrimSpace(src[start:i]); pre != "" {
					return nil, fmt.Errorf("kit.css: text before a comment: %q", pre)
				}
				out = append(out, src[i:i+2+end+2])
				start = i + 2 + end + 2
			}
			i += 2 + end + 1
		case c == '"' || c == '\'':
			end := strings.IndexByte(src[i+1:], c)
			if end < 0 {
				return nil, fmt.Errorf("kit.css: open string")
			}
			i += end + 1
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				out = append(out, src[start:i+1])
				start = i + 1
			}
		case c == ';' && depth == 0:
			out = append(out, src[start:i+1])
			start = i + 1
		}
	}
	if depth != 0 || strings.TrimSpace(src[start:]) != "" {
		return nil, fmt.Errorf("kit.css: unbalanced braces or trailing text")
	}
	return out, nil
}

func patterns(rs []rule) ([]byte, error) {
	p := map[string][]string{}
	for _, r := range rs {
		if r.Apply == "" {
			continue
		}
		seen := map[string]bool{}
		for _, t := range p[r.Name] {
			seen[t] = true
		}
		for _, t := range strings.Fields(r.Apply) {
			if !seen[t] {
				p[r.Name] = append(p[r.Name], t)
				seen[t] = true
			}
		}
	}
	b, err := json.MarshalIndent(map[string]any{"patterns": p}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
