package s3

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Put uploads body (size bytes, hex SHA-256 sum) to Prefix + key with one
// signed PUT (AWS SigV4, header auth). The payload hash is signed, so the
// storage rejects a body that does not match the checksum. Objects up to
// 5 GiB fit one PUT; release archives are capped far below that.
func (p Presigner) Put(ctx context.Context, key string, body io.Reader, size int64, sum, contentType, disposition string) error {
	h := map[string]string{"content-type": contentType}
	if disposition != "" {
		h["content-disposition"] = disposition
	}
	res, err := p.do(ctx, http.MethodPut, key, body, size, sum, h)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode/100 != 2 {
		return fmt.Errorf("s3: put: status %d", res.StatusCode)
	}
	return nil
}

// Delete removes Prefix + key. A missing object is not an error.
func (p Presigner) Delete(ctx context.Context, key string) error {
	res, err := p.do(ctx, http.MethodDelete, key, nil, 0, emptySHA, nil)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode/100 != 2 && res.StatusCode != http.StatusNotFound {
		return fmt.Errorf("s3: delete: status %d", res.StatusCode)
	}
	return nil
}

var emptySHA = hex.EncodeToString(func() []byte { s := sha256.Sum256(nil); return s[:] }())

func (p Presigner) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return http.DefaultClient
}

// do sends one request signed in the Authorization header.
func (p Presigner) do(ctx context.Context, method, key string, body io.Reader, size int64, sum string, extra map[string]string) (*http.Response, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	obj, err := ObjectKey(p.Prefix, key)
	if err != nil {
		return nil, err
	}
	ep, _ := url.Parse(p.Endpoint)
	host, path := ep.Host, "/"+uriEncode(obj, false)
	if p.PathStyle {
		path = "/" + uriEncode(p.Bucket, false) + path
	} else {
		host = p.Bucket + "." + ep.Host
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	t := now().UTC()
	amzDate, day := t.Format("20060102T150405Z"), t.Format("20060102")
	hdr := map[string]string{"host": host, "x-amz-content-sha256": sum, "x-amz-date": amzDate}
	for k, v := range extra {
		hdr[strings.ToLower(k)] = v
	}
	names := make([]string, 0, len(hdr))
	for k := range hdr {
		names = append(names, k)
	}
	sort.Strings(names)
	var canonHdr strings.Builder
	for _, k := range names {
		canonHdr.WriteString(k + ":" + strings.TrimSpace(hdr[k]) + "\n")
	}
	signed := strings.Join(names, ";")
	canonical := strings.Join([]string{method, path, "", canonHdr.String(), signed, sum}, "\n")
	cs := sha256.Sum256([]byte(canonical))
	scope := day + "/" + p.Region + "/s3/aws4_request"
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(cs[:])
	k := mac([]byte("AWS4"+p.SecretKey), day)
	k = mac(k, p.Region)
	k = mac(k, "s3")
	k = mac(k, "aws4_request")
	sig := hex.EncodeToString(mac(k, toSign))

	req, err := http.NewRequestWithContext(ctx, method, ep.Scheme+"://"+host+path, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	if body == nil {
		req.Body = http.NoBody
	}
	req.Host = host
	for _, n := range names {
		if n != "host" {
			req.Header.Set(n, hdr[n])
		}
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+p.AccessKey+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	res, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("s3: %s: %w", strings.ToLower(method), err)
	}
	return res, nil
}
