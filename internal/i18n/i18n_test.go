package i18n

import "testing"

func TestTFallsBackToKey(t *testing.T) {
	d, err := Parse([]byte(`{"locale":"en","messages":{"ui.a.b":"Hi","ui.card.add":"Add"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.T("ui.a.b") != "Hi" || d.T("ui.missing") != "ui.missing" {
		t.Fatal("lookup")
	}
	if l := d.Labels("ui.card."); l["add"] != "Add" || len(l) != 1 {
		t.Fatalf("labels %v", l)
	}
	if _, err := Parse([]byte(`{"locale":"en","messages":{}}`)); err == nil {
		t.Fatal("empty dictionary accepted")
	}
}

func TestOverlay(t *testing.T) {
	d := &Dict{Locale: "en", Messages: map[string]string{"ui.a": "A", "ui.b": "B"}}
	o := d.Overlay("xx", map[string]string{"ui.a": "AA", "ui.unknown": "U", "ui.b": " "})
	if o.Locale != "xx" || o.T("ui.a") != "AA" || o.T("ui.b") != "B" || o.T("ui.unknown") != "ui.unknown" {
		t.Fatalf("overlay: %+v", o)
	}
	if d.T("ui.a") != "A" {
		t.Fatal("source changed")
	}
}
