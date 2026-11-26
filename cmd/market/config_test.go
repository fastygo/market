package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func prodEnv(t *testing.T) map[string]string {
	return map[string]string{
		"MARKET_SESSION_SECRET":  strings.Repeat("s", 44),
		"MARKET_DOWNLOAD_SECRET": strings.Repeat("d", 44),
		"MARKET_PUBLIC_URL":      "https://market.example.com/",
		"MARKET_TRUSTED_PROXIES": "10.0.0.2/32",
		"MARKET_FILES_DIR":       filepath.Join(t.TempDir(), "files"),
	}
}

func TestProductionLocalFiles(t *testing.T) {
	e := prodEnv(t)
	c, err := loadConfig(envOf(e), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if c.Dev || c.PublicURL != "https://market.example.com" || c.S3 != nil || c.FilesDir != e["MARKET_FILES_DIR"] || len(c.Warnings) != 0 {
		t.Fatalf("%+v", c)
	}
	if st, err := os.Stat(c.FilesDir); err != nil || !st.IsDir() {
		t.Fatal("files dir not created", err)
	}
	if c.ClientIP.Header != "X-Forwarded-For" || len(c.ClientIP.Trusted) != 1 {
		t.Fatalf("%+v", c.ClientIP)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr, r.Header["X-Forwarded-For"] = "10.0.0.2:1", []string{"185.71.76.1, 198.51.100.9"}
	if ip := c.ClientIP.IP(r); ip != "198.51.100.9" {
		t.Fatal(ip)
	}
}

func TestDefaultFilesDirUnderData(t *testing.T) {
	e := prodEnv(t)
	delete(e, "MARKET_FILES_DIR")
	data := t.TempDir()
	c, err := loadConfig(envOf(e), data)
	if err != nil || c.FilesDir != filepath.Join(data, "files") {
		t.Fatal(c.FilesDir, err)
	}
}

func TestProductionS3WithPrefix(t *testing.T) {
	e := prodEnv(t)
	for k, v := range map[string]string{"MARKET_S3_ENDPOINT": "https://s3.example.com", "MARKET_S3_REGION": "ru-1",
		"MARKET_S3_BUCKET": "bucket", "MARKET_S3_ACCESS_KEY": "a", "MARKET_S3_SECRET_KEY": "b", "MARKET_S3_PREFIX": "storeee/"} {
		e[k] = v
	}
	c, err := loadConfig(envOf(e), t.TempDir())
	if err != nil || c.S3 == nil || c.S3.Prefix != "storeee/" || !c.S3.PathStyle || c.FilesDir != "" {
		t.Fatal(c, err)
	}
	delete(e, "MARKET_S3_SECRET_KEY")
	if _, err := loadConfig(envOf(e), t.TempDir()); err == nil || !strings.Contains(err.Error(), "S3") {
		t.Fatal("incomplete S3 must fail", err)
	}
}

func TestProductionRefusesToStart(t *testing.T) {
	for _, tc := range []struct {
		name, key, val, want string
	}{
		{"no session secret", "MARKET_SESSION_SECRET", "", "MARKET_SESSION_SECRET is required"},
		{"short download secret", "MARKET_DOWNLOAD_SECRET", "short", "MARKET_DOWNLOAD_SECRET is too short"},
		{"same secrets", "MARKET_DOWNLOAD_SECRET", strings.Repeat("s", 44), "must differ"},
		{"no public url", "MARKET_PUBLIC_URL", "", "MARKET_PUBLIC_URL is required"},
		{"http public url", "MARKET_PUBLIC_URL", "http://market.example.com", "must be https"},
		{"public url with path", "MARKET_PUBLIC_URL", "https://market.example.com/shop", "without a path"},
		{"bad cidr", "MARKET_TRUSTED_PROXIES", "10.0.0.2/40", "MARKET_TRUSTED_PROXIES"},
		{"header without proxies", "MARKET_TRUSTED_PROXIES", "", "needs MARKET_TRUSTED_PROXIES"},
		{"bad ttl", "MARKET_S3_URL_TTL", "five", "MARKET_S3_URL_TTL"},
		{"partial s3", "MARKET_S3_ENDPOINT", "https://s3.example.com", "S3 storage"},
	} {
		e := prodEnv(t)
		e["MARKET_CLIENT_IP_HEADER"] = "X-Forwarded-For"
		e[tc.key] = tc.val
		_, err := loadConfig(envOf(e), t.TempDir())
		if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "refusing to start") {
			t.Errorf("%s: err %v, want %q", tc.name, err, tc.want)
		}
		if err != nil && (strings.Contains(err.Error(), strings.Repeat("s", 44)) || strings.Contains(err.Error(), strings.Repeat("d", 44))) {
			t.Errorf("%s: error leaks a secret value", tc.name)
		}
	}
}

func TestProductionDoesNotRequireMailOrS3(t *testing.T) {
	// mail lives in the PocketBase admin settings (ADR-0026); files are local
	e := prodEnv(t)
	for k := range e {
		if strings.HasPrefix(k, "MARKET_MAIL") || strings.HasPrefix(k, "MARKET_S3") {
			t.Fatal(k)
		}
	}
	if _, err := loadConfig(envOf(e), t.TempDir()); err != nil {
		t.Fatal(err)
	}
}

func TestDevelopmentDefaults(t *testing.T) {
	for _, flag := range []string{"MARKET_FAKE_GATEWAY", "MARKET_DEV"} {
		data := t.TempDir()
		c, err := loadConfig(envOf(map[string]string{flag: "1", "MARKET_CLIENT_IP_HEADER": "X-Forwarded-For"}), data)
		if err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if !c.Dev || len(c.SessionKey) != 32 || len(c.DownloadKey) != 32 || c.FilesDir != filepath.Join(data, "files") {
			t.Fatalf("%s: %+v", flag, c)
		}
		all := strings.Join(c.Warnings, "\n")
		for _, w := range []string{"MARKET_SESSION_SECRET", "MARKET_DOWNLOAD_SECRET", "MARKET_PUBLIC_URL", "MARKET_TRUSTED_PROXIES"} {
			if !strings.Contains(all, w) {
				t.Errorf("%s: no warning about %s:\n%s", flag, w, all)
			}
		}
	}
}

func TestProductionErrorHasNoDevWarnings(t *testing.T) {
	c, err := loadConfig(envOf(map[string]string{}), t.TempDir())
	if err == nil || len(c.Warnings) != 0 {
		t.Fatal(err, c.Warnings)
	}
}

func TestBaseURLAlias(t *testing.T) {
	e := prodEnv(t)
	delete(e, "MARKET_PUBLIC_URL")
	e["MARKET_BASE_URL"] = "https://market.example.com/"
	c, err := loadConfig(envOf(e), t.TempDir())
	if err != nil || c.PublicURL != "https://market.example.com" {
		t.Fatal(c.PublicURL, err)
	}
	e["MARKET_PUBLIC_URL"] = "https://market.example.com"
	if c, err := loadConfig(envOf(e), t.TempDir()); err != nil || c.PublicURL != "https://market.example.com" {
		t.Fatal("same value in both must pass", err)
	}
	e["MARKET_PUBLIC_URL"] = "https://other.example.com"
	if _, err := loadConfig(envOf(e), t.TempDir()); err == nil || !strings.Contains(err.Error(), "differ") {
		t.Fatal("different values must fail", err)
	}
}

func TestPaymentRulesAndProviders(t *testing.T) {
	e := prodEnv(t)
	e["MARKET_PAYMENT_PROVIDERS"] = "yookassa, fake"
	e["MARKET_PAYMENT_ENTITY"] = "IT_Garage"
	e["MARKET_PAYMENT_RULES"] = "currency=RUB,country=RU,entity=it_garage:yookassa"
	c, err := loadConfig(envOf(e), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.PaymentProviders) != 2 || c.PaymentProviders[0] != "yookassa" || c.PaymentEntity != "it_garage" {
		t.Fatalf("%+v", c)
	}
	if len(c.PaymentRules) != 1 || c.PaymentRules[0].Provider != "yookassa" || c.PaymentRules[0].Currency != "RUB" {
		t.Fatalf("%+v", c.PaymentRules)
	}
	e["MARKET_PAYMENT_RULES"] = "not-a-rule"
	if _, err := loadConfig(envOf(e), t.TempDir()); err == nil {
		t.Fatal("bad rules must refuse to start")
	}
}
