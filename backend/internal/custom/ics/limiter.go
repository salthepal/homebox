package ics

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// MaxLimiterKeys bounds the limiter's memory: at most this many distinct clients are
// tracked per window. When the map is full of unexpired buckets, requests from new keys are
// rejected (429) rather than evicting a tracked client.
const MaxLimiterKeys = 10000

// Limiter is a small fixed-window, per-key request limiter held in memory. It is
// defence in depth for the unauthenticated feed route: tokens carry 256 bits, so guessing
// is already hopeless.
type Limiter struct {
	limit   int
	window  time.Duration
	now     func() time.Time
	maxKeys int

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
	return &Limiter{limit: limit, window: window, now: now, maxKeys: MaxLimiterKeys, buckets: map[string]*bucket{}}
}

// SetMaxKeys overrides the key cap (tests). Values below 1 are ignored.
func (l *Limiter) SetMaxKeys(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n >= 1 {
		l.maxKeys = n
	}
}

// Len reports how many keys are currently tracked (tests).
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

func (l *Limiter) sweepLocked(now time.Time) {
	for k, b := range l.buckets {
		if now.Sub(b.start) >= l.window {
			delete(l.buckets, k)
		}
	}
	l.swept = now
}

// Allow records a request for key and reports whether it is within the limit.
func (l *Limiter) Allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Drop expired buckets at most once per window.
	if now.Sub(l.swept) >= l.window {
		l.sweepLocked(now)
	}

	b, ok := l.buckets[key]
	if !ok || now.Sub(b.start) >= l.window {
		if !ok && len(l.buckets) >= l.maxKeys {
			// Full: reclaim expired buckets first, otherwise refuse the new key so the map
			// can't grow past the cap however many addresses a caller cycles through.
			l.sweepLocked(now)
			if len(l.buckets) >= l.maxKeys {
				return false
			}
		}
		b = &bucket{start: now}
		l.buckets[key] = b
	}
	b.count++
	return b.count <= l.limit
}

// ClientIP returns the caller's address for rate limiting, normalised so a client can't
// get fresh buckets by varying the spelling of its address: IPv4-mapped IPv6 addresses are
// unmapped to IPv4, and IPv6 addresses are keyed by their /64 (the smallest block a
// subscriber normally controls).
//
// Trust assumption: upstream deliberately doesn't apply a RealIP middleware, so the socket
// peer is used, and CF-Connecting-IP is honoured only when that peer is loopback or private.
// That matches the deployment (cloudflared on the Docker/LAN network in front of Homebox). A
// public peer can't choose its own bucket via that header; if the tunnel connector ever
// connects from a public address, every caller shares the connector's bucket.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	peer = peer.Unmap()
	if peer.IsLoopback() || peer.IsPrivate() {
		if cf, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); err == nil {
			return normaliseIP(cf)
		}
	}
	return normaliseIP(peer)
}

func normaliseIP(a netip.Addr) string {
	a = a.WithZone("").Unmap()
	if a.Is6() {
		if p, err := a.Prefix(64); err == nil {
			return p.Masked().String()
		}
	}
	return a.String()
}
