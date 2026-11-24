package modules

import (
	"strings"
	"testing"
)

func reg(t *testing.T, ms ...Manifest) *Registry {
	t.Helper()
	r := New()
	for _, m := range ms {
		if err := r.Register(m); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func ids(ms []Manifest) string {
	var s []string
	for _, m := range ms {
		s = append(s, m.ID)
	}
	return strings.Join(s, ",")
}

func TestResolve(t *testing.T) {
	r := reg(t,
		Manifest{ID: "region-ru", Version: "1.0.0", Provides: []string{"tax.rules@1"}, Requires: []string{"locale.format@1?"}},
		Manifest{ID: "pay-fake", Version: "1.0.0", Provides: []string{"payment.gateway@1"}},
		Manifest{ID: "ordering", Version: "1.0.0", Requires: []string{"tax.rules@1", "payment.gateway@1"}},
		Manifest{ID: "marketplace", Version: "1.0.0", Modes: []string{"marketplace"}},
		Manifest{ID: "region-x", Version: "1.0.0", Provides: []string{"tax.rules@1"}},
	)
	got, err := r.Resolve([]string{"ordering", "region-ru", "pay-fake"}, "single_store")
	if err != nil {
		t.Fatal(err)
	}
	if ids(got) != "pay-fake,region-ru,ordering" {
		t.Fatalf("order = %s", ids(got))
	}
	cases := map[string][]string{
		"not compiled in":      {"nope"},
		"does not run":         {"marketplace"},
		"no enabled provider":  {"ordering", "pay-fake"},
		"provided by region-r": {"region-ru", "region-x"},
	}
	for want, en := range cases {
		if _, err := r.Resolve(en, "single_store"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v: want %q, got %v", en, want, err)
		}
	}
	if err := r.Register(Manifest{ID: "pay-fake", Version: "1"}); err == nil {
		t.Error("duplicate id must fail")
	}
}

func TestCycle(t *testing.T) {
	r := reg(t, Manifest{ID: "a", Version: "1", After: []string{"b"}}, Manifest{ID: "b", Version: "1", After: []string{"a"}})
	if _, err := r.Resolve([]string{"a", "b"}, ""); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle, got %v", err)
	}
}
