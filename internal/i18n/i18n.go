// Package i18n resolves dictionary keys (ADR-0018). The English source
// dictionary ships with the binary; other locales come from the database
// in phase 1 and fall back to English key by key.
package i18n

import (
	"encoding/json"
	"fmt"
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
