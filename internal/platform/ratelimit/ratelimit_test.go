package ratelimit_test

import (
	"testing"
	"time"

	"github.com/fastygo/market/internal/platform/ratelimit"
)

func TestAllowWindow(t *testing.T) {
	now := time.Date(2026, 11, 26, 9, 0, 0, 0, time.UTC)
	l := &ratelimit.Limiter{Max: 3, Window: time.Minute, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("192.0.2.1"); !ok {
			t.Fatalf("shot %d rejected", i)
		}
	}
	ok, wait := l.Allow("192.0.2.1")
	if ok || wait <= 0 || wait > time.Minute {
		t.Fatalf("want limited, got ok=%v wait=%s", ok, wait)
	}
	if ok, _ := l.Allow("192.0.2.2"); !ok {
		t.Fatal("other key must not share the bucket")
	}
	now = now.Add(time.Minute + time.Second)
	if ok, _ := l.Allow("192.0.2.1"); !ok {
		t.Fatal("window should have slid")
	}
}

func TestNilLimiterAllows(t *testing.T) {
	var l *ratelimit.Limiter
	if ok, wait := l.Allow("x"); !ok || wait != 0 {
		t.Fatalf("nil limiter: %v %s", ok, wait)
	}
}
