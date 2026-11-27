package s3

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type captureRT struct{ req *http.Request }

func (c *captureRT) RoundTrip(r *http.Request) (*http.Response, error) {
	c.req = r
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
}

// The PUT example of the AWS SigV4 documentation ("Signature calculation:
// transfer payload in a single chunk", Example: PUT Object), with its
// published credentials, headers and signature.
func TestPutSignatureAWSExample(t *testing.T) {
	rt := &captureRT{}
	p := Presigner{
		Endpoint: "https://s3.amazonaws.com", Region: "us-east-1", Bucket: "examplebucket",
		AccessKey: "AKIAIOSFODNN7" + "EXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCY" + "EXAMPLEKEY",
		Now:  func() time.Time { return time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC) },
		HTTP: &http.Client{Transport: rt},
	}
	body := "Welcome to Amazon S3."
	res, err := p.do(context.Background(), http.MethodPut, "test$file.text", strings.NewReader(body), int64(len(body)),
		"44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072",
		map[string]string{"Date": "Fri, 24 May 2013 00:00:00 GMT", "x-amz-storage-class": "REDUCED_REDUNDANCY"})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	want := "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7" + "EXAMPLE/20130524/us-east-1/s3/aws4_request, " +
		"SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class, " +
		"Signature=98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd"
	if got := rt.req.Header.Get("Authorization"); got != want {
		t.Fatalf("authorization\n got %s\nwant %s", got, want)
	}
	if rt.req.URL.String() != "https://examplebucket.s3.amazonaws.com/test%24file.text" || rt.req.ContentLength != int64(len(body)) {
		t.Fatalf("request %s %d", rt.req.URL, rt.req.ContentLength)
	}
}

func TestPutDeleteKeys(t *testing.T) {
	rt := &captureRT{}
	p := Presigner{Endpoint: "https://s3.example.com", Region: "ru-1", Bucket: "b", AccessKey: "a", SecretKey: "s",
		PathStyle: true, Prefix: "storeee/", HTTP: &http.Client{Transport: rt}}
	if err := p.Put(context.Background(), "../x.zip", strings.NewReader("x"), 1, emptySHA, "application/zip", ""); err == nil {
		t.Fatal("key leaving the prefix accepted")
	}
	if err := p.Delete(context.Background(), "godudy/kit/kit-1.0.0.zip"); err != nil {
		t.Fatal(err)
	}
	if rt.req.Method != http.MethodDelete || rt.req.URL.Path != "/b/storeee/godudy/kit/kit-1.0.0.zip" {
		t.Fatalf("delete %s %s", rt.req.Method, rt.req.URL.Path)
	}
}
