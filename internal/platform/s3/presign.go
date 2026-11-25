// Package s3 presigns GET URLs for S3-compatible storage with AWS
// Signature Version 4 (query string auth). It needs no SDK and makes no
// network call: the buyer's browser fetches the object (ADR-0009).
package s3

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Presigner signs GET URLs for one bucket.
type Presigner struct {
	Endpoint  string // e.g. https://s3.example.com
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	// PathStyle puts the bucket in the path (endpoint/bucket/key); otherwise
	// the bucket is a subdomain of the endpoint host.
	PathStyle bool
	// Prefix is a folder inside the bucket (e.g. "storeee/") prepended to
	// every storage key, so one bucket can hold several stores or the
	// backups of the host. Storage keys in the database stay relative.
	Prefix string
	Now    func() time.Time
}

// ErrConfig is returned for incomplete configuration.
var ErrConfig = errors.New("s3: endpoint, region, bucket and keys are required")

// ErrKey is returned for an empty storage key or one that leaves the
// prefix ("." or ".." segments).
var ErrKey = errors.New("s3: invalid storage key")

// Validate checks the configuration without a network call (start-up
// fail-fast).
func (p Presigner) Validate() error {
	if p.Endpoint == "" || p.Region == "" || p.Bucket == "" || p.AccessKey == "" || p.SecretKey == "" {
		return ErrConfig
	}
	if ep, err := url.Parse(p.Endpoint); err != nil || ep.Host == "" || (ep.Scheme != "https" && ep.Scheme != "http") {
		return ErrConfig
	}
	if _, err := ObjectKey(p.Prefix, "x"); err != nil {
		return fmt.Errorf("s3: invalid prefix %q", p.Prefix)
	}
	return nil
}

// ObjectKey joins the bucket prefix and a storage key. Leading and
// trailing slashes are ignored; "." and ".." segments are rejected so a
// key cannot leave the prefix.
func ObjectKey(prefix, key string) (string, error) {
	key = strings.Trim(key, "/")
	if key == "" {
		return "", ErrKey
	}
	prefix = strings.Trim(prefix, "/")
	for _, s := range []string{prefix, key} {
		if s == "" {
			continue
		}
		for _, seg := range strings.Split(s, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return "", ErrKey
			}
		}
	}
	if prefix == "" {
		return key, nil
	}
	return prefix + "/" + key, nil
}

// PresignGet returns a URL valid for ttl (1 s to 7 days) for the object
// Prefix + key.
func (p Presigner) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	if p.Endpoint == "" || p.Region == "" || p.Bucket == "" || p.AccessKey == "" || p.SecretKey == "" {
		return "", ErrConfig
	}
	key, err := ObjectKey(p.Prefix, key)
	if err != nil {
		return "", err
	}
	if ttl < time.Second || ttl > 7*24*time.Hour {
		return "", fmt.Errorf("s3: ttl %s out of range", ttl)
	}
	ep, err := url.Parse(p.Endpoint)
	if err != nil || ep.Host == "" {
		return "", ErrConfig
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	t := now().UTC()
	amzDate, day := t.Format("20060102T150405Z"), t.Format("20060102")
	host, path := ep.Host, "/"+uriEncode(key, false)
	if p.PathStyle {
		path = "/" + uriEncode(p.Bucket, false) + path
	} else {
		host = p.Bucket + "." + ep.Host
	}
	scope := day + "/" + p.Region + "/s3/aws4_request"
	q := map[string]string{
		"X-Amz-Algorithm":     "AWS4-HMAC-SHA256",
		"X-Amz-Credential":    p.AccessKey + "/" + scope,
		"X-Amz-Date":          amzDate,
		"X-Amz-Expires":       fmt.Sprint(int(ttl.Seconds())),
		"X-Amz-SignedHeaders": "host",
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = uriEncode(k, true) + "=" + uriEncode(q[k], true)
	}
	query := strings.Join(parts, "&")
	canonical := strings.Join([]string{"GET", path, query, "host:" + host, "", "host", "UNSIGNED-PAYLOAD"}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := mac([]byte("AWS4"+p.SecretKey), day)
	k = mac(k, p.Region)
	k = mac(k, "s3")
	k = mac(k, "aws4_request")
	sig := hex.EncodeToString(mac(k, toSign))
	return ep.Scheme + "://" + host + path + "?" + query + "&X-Amz-Signature=" + sig, nil
}

func mac(key []byte, s string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(s))
	return h.Sum(nil)
}

// uriEncode is the SigV4 URI encoding: unreserved characters stay, the
// rest is %XX; '/' stays in paths and is encoded in query values.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
