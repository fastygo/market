package web

import (
	"strings"
	"testing"
)

func TestContentSecurityPolicyAllowsYooKassaFormRedirect(t *testing.T) {
	csp := contentSecurityPolicy("n")
	want := "form-action 'self' https://yoomoney.ru https://*.yoomoney.ru https://yookassa.ru https://*.yookassa.ru"
	if !strings.Contains(csp, want) {
		t.Fatalf("form-action must allow the YooKassa confirmation redirect, got %q", csp)
	}
	if strings.Contains(csp, "form-action *") || strings.Contains(csp, "form-action 'self' *") {
		t.Fatalf("form-action must not be wildcard: %q", csp)
	}
}
