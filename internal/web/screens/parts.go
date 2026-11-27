package screens

import (
	"github.com/a-h/templ"
	selectfield "github.com/ui8kit/codegen/generated/templ/ui/select"
)

// F describes one form field.
type F struct {
	ID, Name, Label, Type, Value, Placeholder, Hint, Auto string
	Required, ReadOnly                                    bool
	Rows                                                  string
	Options                                               []selectfield.SelectOptionItem
	Min, Step                                             string
}

func hintID(f F) string {
	if f.Hint == "" {
		return ""
	}
	return f.ID + "-hint"
}

func fieldAttrs(f F) templ.Attributes {
	a := templ.Attributes{}
	if f.Auto != "" {
		a["autocomplete"] = f.Auto
	}
	return a
}

// Row is one entry of a cabinet list.
type Row struct {
	Title, Href, Meta, Badge, Tone, Image string
	Lines                                 []string
}
