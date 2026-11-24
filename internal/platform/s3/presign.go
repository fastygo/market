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
	Now       func() time.Time
}

// ErrConfig is returned for incomplete configuration.
var ErrConfig = errors.New("s3: endpoint, region, bucket and keys are required")

// PresignGet returns a URL valid for ttl (1 s to 7 days).
func (p Presigner) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	if p.Endpoint == "" || p.Region == "" || p.Bucket == "" || p.AccessKey == "" || p.SecretKey == "" {
		return "", ErrConfig
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
