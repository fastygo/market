package screens

import "testing"

func TestFormatMinorRU(t *testing.T) {
	SetMoneyLocale("ru")
	defer SetMoneyLocale("en")
	cases := map[int64]string{149000: "1\u202f490\u00a0\u20bd", 2900: "29\u00a0\u20bd", 2990: "29,90\u00a0\u20bd", 123456705: "1\u202f234\u202f567,05\u00a0\u20bd"}
	for in, want := range cases {
		if got := formatMinor(in, "RUB"); got != want {
			t.Errorf("%d: got %q want %q", in, got, want)
		}
	}
	SetMoneyLocale("en")
	if got := formatMinor(2900, "RUB"); got == "29\u00a0\u20bd" {
		t.Errorf("en uses the ru format: %q", got)
	}
}
