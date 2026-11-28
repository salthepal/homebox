package ics

import (
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"
)

// Limiter is a small fixed-window, per-key request limiter held in memory. It is
// defence in depth for the unauthenticated feed route: tokens carry 256 bits, so guessing
// is already hopeless.
type Limiter struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	start time.Time
	count int
}

// NewLimiter allows limit requests per key per window. now may be nil (time.Now).
func NewLimiter(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, window: window, now: now, buckets: map[string]*bucket{}}
}

// Allow records a request for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Drop expired buckets at most once per window so the map can't grow without bound.
	if now.Sub(l.swept) >= l.window {
		for k, b := range l.buckets {
			if now.Sub(b.start) >= l.window {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	b.count++
	return b.count <= l.limit
}

// ClientIP returns the caller's address for rate limiting. Upstream deliberately doesn't
// apply a RealIP middleware, so this uses the socket peer, and only honours
// CF-Connecting-IP when that peer is loopback or private (i.e. our own tunnel connector),
// so a direct client can't pick its own bucket.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	if peer.IsLoopback() || peer.IsPrivate() {
		if cf, err := netip.ParseAddr(r.Header.Get("CF-Connecting-IP")); err == nil {
			return cf.String()
		}
	}
	return peer.String()
}
