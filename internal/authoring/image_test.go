package authoring_test

import (
	"testing"

	"github.com/fastygo/market/internal/authoring"
)

func TestDetectImage(t *testing.T) {
	cases := []struct {
		head string
		mime string
		ok   bool
	}{
		{"\xff\xd8\xff\xe0rest", "image/jpeg", true},
		{"\x89PNG\r\n\x1a\nrest", "image/png", true},
		{"GIF89a....", "image/gif", true},
		{"RIFF....WEBP....", "image/webp", true},
		{"<svg xmlns='http://www.w3.org/2000/svg'>", "", false},
		{"<?xml version='1.0'?><svg>", "", false},
		{"<!DOCTYPE html><html>", "", false},
		{"MZ\x90\x00", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		mime, ok := authoring.DetectImage([]byte(c.head))
		if ok != c.ok || mime != c.mime {
			t.Errorf("%q: got %q %v, want %q %v", c.head[:min(20, len(c.head))], mime, ok, c.mime, c.ok)
		}
	}
}

func TestParseETag(t *testing.T) {
	if n, ok := authoring.ParseETag(`"v3"`); !ok || n != 3 {
		t.Fatalf("v3 quoted: %d %v", n, ok)
	}
	if n, ok := authoring.ParseETag("v0"); !ok || n != 0 {
		t.Fatalf("v0: %d %v", n, ok)
	}
	if n, ok := authoring.ParseETag("12"); !ok || n != 12 {
		t.Fatalf("12: %d %v", n, ok)
	}
	if _, ok := authoring.ParseETag(""); ok {
		t.Fatal("empty")
	}
	if _, ok := authoring.ParseETag("v-1"); ok {
		t.Fatal("negative")
	}
	if got := authoring.ETag(4); got != `"v4"` {
		t.Fatalf("etag %q", got)
	}
}

func TestSafeImageName(t *testing.T) {
	if g := authoring.SafeImageName(`..\..\x.png`, "image/png"); g != "x.png" {
		t.Fatalf("%s", g)
	}
	if g := authoring.SafeImageName("photo", "image/jpeg"); g != "photo.jpg" {
		t.Fatalf("%s", g)
	}
}
