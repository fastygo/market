package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fastygo/market/internal/platform/clientip"
	"github.com/fastygo/market/internal/platform/s3"
)

// minSecretLen is the minimum length of MARKET_SESSION_SECRET and
// MARKET_DOWNLOAD_SECRET in production (32 bytes of entropy as base64 is
// 44 characters; 32 is the floor).
const minSecretLen = 32

// config is the start-up configuration read from env. Production
// (MARKET_FAKE_GATEWAY and MARKET_DEV unset) refuses to start when a
// required value is missing; development falls back to defaults and
// warns. Mail settings are not checked here: the default mailer reads
// SMTP from the PocketBase admin settings (ADR-0026).
type config struct {
	Dev         bool
	PublicURL   string // no trailing slash
	SessionKey  []byte
	DownloadKey []byte
	ClientIP    clientip.Resolver
	// Storage: S3 when MARKET_S3_ENDPOINT is set, else local files in
	// FilesDir (default <pb_data>/files) served by the app on /files.
	S3             *s3.Presigner
	FilesDir       string
	URLTTL         time.Duration
	PaymentTimeout time.Duration
	Warnings       []string
}

// loadConfig reads and checks env. All problems are reported at once.
func loadConfig(getenv func(string) string, dataDir string) (config, error) {
	c := config{Dev: getenv("MARKET_FAKE_GATEWAY") == "1" || getenv("MARKET_DEV") == "1"}
	var errs []error
	fail := func(format string, a ...any) {
		if c.Dev {
			c.Warnings = append(c.Warnings, fmt.Sprintf(format, a...)+" (allowed in development only)")
			return
		}
		errs = append(errs, fmt.Errorf(format, a...))
	}
	hard := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	secret := func(k string) []byte {
		v := getenv(k)
		if v == "" {
			if c.Dev {
				c.Warnings = append(c.Warnings, k+" not set: using a random per-process key, links break on restart (development only)")
			} else {
				errs = append(errs, fmt.Errorf("%s is required (%d+ random characters, e.g. openssl rand -base64 48)", k, minSecretLen))
			}
			b := make([]byte, 32)
			_, _ = rand.Read(b)
			return b
		}
		if len(v) < minSecretLen {
			fail("%s is too short (%d characters, need %d+)", k, len(v), minSecretLen)
		}
		return []byte(v)
	}
	c.SessionKey = secret("MARKET_SESSION_SECRET")
	c.DownloadKey = secret("MARKET_DOWNLOAD_SECRET")
	if getenv("MARKET_SESSION_SECRET") != "" && getenv("MARKET_SESSION_SECRET") == getenv("MARKET_DOWNLOAD_SECRET") {
		fail("MARKET_SESSION_SECRET and MARKET_DOWNLOAD_SECRET must differ")
	}

	c.PublicURL = strings.TrimRight(getenv("MARKET_PUBLIC_URL"), "/")
	if c.PublicURL == "" {
		fail("MARKET_PUBLIC_URL is required (https origin, e.g. https://market.example.com)")
	} else if u, err := url.Parse(c.PublicURL); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		hard("MARKET_PUBLIC_URL %q is not an absolute URL", c.PublicURL)
	} else {
		if u.Scheme != "https" {
			fail("MARKET_PUBLIC_URL must be https (got %q)", c.PublicURL)
		}
		if u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			hard("MARKET_PUBLIC_URL must be an origin without a path (got %q)", c.PublicURL)
		}
	}

	trusted, err := clientip.ParsePrefixes(getenv("MARKET_TRUSTED_PROXIES"))
	if err != nil {
		hard("MARKET_TRUSTED_PROXIES: %v", err)
	}
	header := getenv("MARKET_CLIENT_IP_HEADER")
	if len(trusted) > 0 && header == "" {
		header = "X-Forwarded-For"
	}
	if header != "" && len(trusted) == 0 {
		fail("MARKET_CLIENT_IP_HEADER=%s needs MARKET_TRUSTED_PROXIES (the reverse proxy CIDRs); without it the header is ignored", header)
	}
	c.ClientIP = clientip.Resolver{Header: header, Trusted: trusted}

	if ep := getenv("MARKET_S3_ENDPOINT"); ep != "" {
		p := s3.Presigner{
			Endpoint: ep, Region: getenv("MARKET_S3_REGION"), Bucket: getenv("MARKET_S3_BUCKET"),
			AccessKey: getenv("MARKET_S3_ACCESS_KEY"), SecretKey: getenv("MARKET_S3_SECRET_KEY"),
			PathStyle: getenv("MARKET_S3_PATH_STYLE") != "0", Prefix: getenv("MARKET_S3_PREFIX"),
		}
		if err := p.Validate(); err != nil {
			hard("S3 storage (MARKET_S3_ENDPOINT is set): %v; set MARKET_S3_REGION, MARKET_S3_BUCKET, MARKET_S3_ACCESS_KEY, MARKET_S3_SECRET_KEY or unset MARKET_S3_ENDPOINT for local files", err)
		}
		c.S3 = &p
	} else {
		dir := getenv("MARKET_FILES_DIR")
		if dir == "" {
			dir = filepath.Join(dataDir, "files")
		}
		if err := os.MkdirAll(dir, 0o750); err != nil {
			hard("MARKET_FILES_DIR %q: %v", dir, err)
		} else if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			hard("MARKET_FILES_DIR %q is not a directory", dir)
		}
		c.FilesDir = dir
	}

	dur := func(k, def string) time.Duration {
		v := getenv(k)
		if v == "" {
			v = def
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			hard("%s %q is not a positive duration", k, v)
		}
		return d
	}
	c.URLTTL = dur("MARKET_S3_URL_TTL", "5m")
	c.PaymentTimeout = dur("MARKET_PAYMENT_TIMEOUT", "30m")

	if err := errors.Join(errs...); err != nil {
		return c, fmt.Errorf("refusing to start (set MARKET_DEV=1 for development defaults):\n%w", err)
	}
	return c, nil
}
