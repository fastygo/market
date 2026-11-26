// Package ratelimit is a per-key sliding window used for sign-in routes.
package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// DefaultAuth is 5 requests per minute, shared by the cabinet and the
// PocketBase users auth/create endpoints for one client address.
const (
	DefaultAuthMax    = 5
	DefaultAuthWindow = time.Minute
	// KeyPrefix is prepended to a client IP so all sign-in routes share one budget.
	KeyPrefix = "auth:"
)

// Limiter counts events per key over a sliding window.
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time
}

// NewAuth returns a limiter with DefaultAuthMax / DefaultAuthWindow.
func NewAuth() *Limiter {
	return &Limiter{Max: DefaultAuthMax, Window: DefaultAuthWindow}
}

func (l *Limiter) now() time.Time {
	if l != nil && l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Limiter) max() int {
	if l == nil || l.Max <= 0 {
		return DefaultAuthMax
	}
	return l.Max
}

func (l *Limiter) window() time.Duration {
	if l == nil || l.Window <= 0 {
		return DefaultAuthWindow
	}
	return l.Window
}

// Allow reports whether key may proceed. retryAfter is zero on success.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	if l == nil {
		return true, 0
	}
	if key == "" {
		key = "unknown"
	}
	now := l.now()
	cut := now.Add(-l.window())
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.hits == nil {
		l.hits = map[string][]time.Time{}
	}
	q := l.hits[key]
	i := 0
	for i < len(q) && !q[i].After(cut) {
		i++
	}
	q = q[i:]
	if len(q) >= l.max() {
		l.hits[key] = q
		return false, q[0].Add(l.window()).Sub(now)
	}
	l.hits[key] = append(q, now)
	return true, 0
}

// SetRetryAfter writes Retry-After in whole seconds, at least 1.
func SetRetryAfter(w http.ResponseWriter, wait time.Duration) {
	sec := int(wait.Seconds())
	if sec < 1 {
		sec = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(sec))
}
