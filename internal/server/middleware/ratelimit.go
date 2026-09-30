package middleware

import (
	"net"
	"net/http"
	"sync"

	"github.com/ethandilley/rankings/internal/server/auth"
	"golang.org/x/time/rate"
)

// RateLimiter is an in-memory per-identity token bucket applied to mutating
// requests. Each session (by cookie value, falling back to remote IP when
// unauthenticated) gets its own bucket, so a buggy frontend retry-loop or a
// league member's script cannot hammer the database. A single server
// instance needs no distributed limiter (see ai/07-security-hardening.md).
type RateLimiter struct {
	limit rate.Limit
	burst int

	mu      sync.Mutex
	buckets map[string]*rate.Limiter
}

func NewRateLimiter(limit rate.Limit, burst int) *RateLimiter {
	return &RateLimiter{limit: limit, burst: burst, buckets: map[string]*rate.Limiter{}}
}

func (l *RateLimiter) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if !l.bucketFor(r).Allow() {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (l *RateLimiter) bucketFor(r *http.Request) *rate.Limiter {
	key := identityKey(r)
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = rate.NewLimiter(l.limit, l.burst)
		l.buckets[key] = b
	}
	return b
}

// identityKey prefers the session token so one user's buckets are not
// drained by other users sharing an IP (e.g. the office/VPN NAT); unauthenticated
// requests fall back to the remote IP.
func identityKey(r *http.Request) string {
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		return "session:" + c.Value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}
