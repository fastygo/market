// Package modules is the minimal module registry of ADR-0017: Go modules
// are registered in cmd/market with a manifest; Resolve checks commerce
// mode, required capabilities, singular capability cardinality and load
// order (cycles fail start-up). The goja plugin runtime is a later phase.
package modules

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Kind of a module.
type Kind string

// Module kinds.
const (
	KindGo     Kind = "go"
	KindPlugin Kind = "plugin" // later phase (ADR-0017)
)

// Manifest describes one module (same fields as module.json).
type Manifest struct {
	ID       string
	Version  string
	Kind     Kind
	Provides []string // versioned capabilities, e.g. "tax.rules@1"
	Requires []string // capabilities; a trailing "?" marks an optional one
	After    []string // module ids that must load first when enabled
	Modes    []string // commerce modes the module may run in; empty = all
}

// Singular capabilities have exactly one provider per storefront.
var Singular = map[string]bool{
	"tax.rules@1": true, "taxdocs.mapping@1": true, "legal.profile@1": true,
	"checkout.fields@1": true, "storage.presign@1": true,
}

// Registry holds the modules compiled into the binary.
type Registry struct{ mods map[string]Manifest }

// New returns an empty registry.
func New() *Registry { return &Registry{mods: map[string]Manifest{}} }

// Register adds a module; ids are unique.
func (r *Registry) Register(m Manifest) error {
	if m.ID == "" || m.Version == "" {
		return errors.New("modules: id and version are required")
	}
	if _, dup := r.mods[m.ID]; dup {
		return fmt.Errorf("modules: %s registered twice", m.ID)
	}
	if m.Kind == "" {
		m.Kind = KindGo
	}
	r.mods[m.ID] = m
	return nil
}

// Resolve returns the enabled modules in load order for a commerce mode.
func (r *Registry) Resolve(enabled []string, mode string) ([]Manifest, error) {
	set := map[string]Manifest{}
	for _, id := range enabled {
		m, ok := r.mods[id]
		if !ok {
			return nil, fmt.Errorf("modules: %s is enabled but not compiled in", id)
		}
		if len(m.Modes) > 0 && !contains(m.Modes, mode) {
			return nil, fmt.Errorf("modules: %s does not run in %s mode", id, mode)
		}
		set[id] = m
	}
	providers := map[string][]string{}
	for _, m := range set {
		for _, c := range m.Provides {
			providers[c] = append(providers[c], m.ID)
		}
	}
	for c, ids := range providers {
		if Singular[c] && len(ids) > 1 {
			sort.Strings(ids)
			return nil, fmt.Errorf("modules: capability %s provided by %s", c, strings.Join(ids, ", "))
		}
	}
	for _, m := range set {
		for _, req := range m.Requires {
			if strings.HasSuffix(req, "?") {
				continue
			}
			if len(providers[req]) == 0 {
				return nil, fmt.Errorf("modules: %s requires %s, no enabled provider", m.ID, req)
			}
		}
	}
	return order(set, providers)
}

// order: Go modules before plugins, then topological by After and by the
// providers of required capabilities; ties by id for determinism.
func order(set map[string]Manifest, providers map[string][]string) ([]Manifest, error) {
	deps := map[string][]string{}
	for id, m := range set {
		for _, a := range m.After {
			if _, ok := set[a]; ok {
				deps[id] = append(deps[id], a)
			}
		}
		for _, req := range m.Requires {
			for _, p := range providers[strings.TrimSuffix(req, "?")] {
				if p != id {
					deps[id] = append(deps[id], p)
				}
			}
		}
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ki, kj := set[ids[i]].Kind == KindPlugin, set[ids[j]].Kind == KindPlugin
		if ki != kj {
			return !ki
		}
		return ids[i] < ids[j]
	})
	const (
		white = iota
		grey
		black
	)
	color := map[string]int{}
	var out []Manifest
	var visit func(id string) error
	visit = func(id string) error {
		switch color[id] {
		case grey:
			return fmt.Errorf("modules: load order cycle at %s", id)
		case black:
			return nil
		}
		color[id] = grey
		d := append([]string(nil), deps[id]...)
		sort.Strings(d)
		for _, x := range d {
			if err := visit(x); err != nil {
				return err
			}
		}
		color[id] = black
		out = append(out, set[id])
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
