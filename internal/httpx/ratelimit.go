package httpx

import (
	"sync"
	"time"
)

type rateBucket struct {
	count   int
	resetAt time.Time
}

// PublicRateLimiter bounds per-IP request volume over fixed windows with periodic cleanup.
type PublicRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]rateBucket
	limit   int
	window  time.Duration
}

func NewPublicRateLimiter(limit int, window time.Duration) *PublicRateLimiter {
	if limit <= 0 {
		limit = 120
	}
	if window <= 0 {
		window = time.Minute
	}
	return &PublicRateLimiter{
		buckets: make(map[string]rateBucket),
		limit:   limit,
		window:  window,
	}
}

func (l *PublicRateLimiter) Allow(key string, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) > 4096 {
		for k, b := range l.buckets {
			if !now.Before(b.resetAt) {
				delete(l.buckets, k)
			}
		}
	}

	b, ok := l.buckets[key]
	if !ok || !now.Before(b.resetAt) {
		l.buckets[key] = rateBucket{count: 1, resetAt: now.Add(l.window)}
		return true
	}
	if b.count >= l.limit {
		return false
	}
	b.count++
	l.buckets[key] = b
	return true
}
