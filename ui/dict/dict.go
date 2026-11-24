// Package dict embeds the source dictionary (locale-en, ADR-0018).
package dict

import _ "embed"

// EN is the English source dictionary in the UI8Kit DictionaryV04 shape.
//
//go:embed en.json
var EN []byte
