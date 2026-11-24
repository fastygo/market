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
