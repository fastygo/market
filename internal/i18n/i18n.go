// Package i18n resolves dictionary keys (ADR-0018). The English source
// dictionary ships with the binary; other locales come from the database
// in phase 1 and fall back to English key by key.
package i18n

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Dict is the UI8Kit DictionaryV04 shape.
type Dict struct {
	Locale       string            `json:"locale"`
	SourceLocale string            `json:"source_locale,omitempty"`
	Messages     map[string]string `json:"messages"`
}

// Parse decodes a dictionary and checks it is not empty.
func Parse(b []byte) (*Dict, error) {
	var d Dict
	if err := json.Unmarshal(b, &d); err != nil {
		return nil, fmt.Errorf("i18n: %w", err)
	}
	if d.Locale == "" || len(d.Messages) == 0 {
		return nil, fmt.Errorf("i18n: empty dictionary")
	}
	return &d, nil
}

// T returns the message for key; a missing key returns the key itself so
// gaps are visible, never a blank.
func (d *Dict) T(key string) string {
	if d != nil {
		if v, ok := d.Messages[key]; ok {
			return v
		}
	}
	return key
}

// Labels returns a map view for UI8Kit blocks that read labels by name:
// short name -> message, for the given key prefix (for example "ui.card.").
func (d *Dict) Labels(prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range d.Messages {
		if strings.HasPrefix(k, prefix) {
			out[strings.TrimPrefix(k, prefix)] = v
		}
	}
	return out
}

// Overlay returns a copy of d in another locale: messages from m replace
// the source ones key by key; missing keys stay in the source language.
func (d *Dict) Overlay(locale string, m map[string]string) *Dict {
	out := &Dict{Locale: locale, SourceLocale: d.Locale, Messages: make(map[string]string, len(d.Messages))}
	for k, v := range d.Messages {
		out.Messages[k] = v
	}
	for k, v := range m {
		if _, ok := d.Messages[k]; ok && strings.TrimSpace(v) != "" {
			out.Messages[k] = v
		}
	}
	return out
}

// Report lists the gaps of a translation against the source dictionary:
// keys the translation lacks (or leaves blank), keys the source does not
// know, and keys whose {placeholders} differ from the source message.
type Report struct {
	Missing      []string
	Extra        []string
	Placeholders []string
}

// OK reports a translation that covers every source key.
func (r Report) OK() bool {
	return len(r.Missing) == 0 && len(r.Extra) == 0 && len(r.Placeholders) == 0
}

// Check compares translated messages m with the source dictionary d.
func (d *Dict) Check(m map[string]string) Report {
	var r Report
	for k, v := range d.Messages {
		t, ok := m[k]
		switch {
		case !ok || strings.TrimSpace(t) == "":
			r.Missing = append(r.Missing, k)
		case !samePlaceholders(v, t):
			r.Placeholders = append(r.Placeholders, k)
		}
	}
	for k := range m {
		if _, ok := d.Messages[k]; !ok {
			r.Extra = append(r.Extra, k)
		}
	}
	sort.Strings(r.Missing)
	sort.Strings(r.Extra)
	sort.Strings(r.Placeholders)
	return r
}

func samePlaceholders(a, b string) bool {
	pa, pb := placeholders(a), placeholders(b)
	if len(pa) != len(pb) {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return false
		}
	}
	return true
}

func placeholders(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			break
		}
		out = append(out, s[i:i+j+1])
		s = s[i+j+1:]
	}
	sort.Strings(out)
	return out
}
