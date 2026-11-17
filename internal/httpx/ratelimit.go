package httpx

import (
	"sync"
	"time"
)

type rateBucket struct {
	count   int
	resetAt time.Time
}

const maxRateLimiterBuckets = 16384

// PublicRateLimiter bounds per-IP request volume over fixed windows with bounded memory and O(1) amortized cleanup.
type PublicRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]rateBucket
	limit     int
	window    time.Duration
	lastSweep time.Time
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

	b, ok := l.buckets[key]
	if ok && now.Before(b.resetAt) {
		if b.count >= l.limit {
			return false
		}
		b.count++
		l.buckets[key] = b
		return true
	}

	sweepInterval := l.window / 4
	if sweepInterval < 5*time.Second {
		sweepInterval = 5 * time.Second
	}
	if len(l.buckets) >= maxRateLimiterBuckets || (len(l.buckets) > 4096 && now.Sub(l.lastSweep) >= sweepInterval) {
		for k, entry := range l.buckets {
			if !now.Before(entry.resetAt) {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
		// Hard memory cap if >maxRateLimiterBuckets distinct active keys hit within one window.
		for k := range l.buckets {
			if len(l.buckets) < maxRateLimiterBuckets {
				break
			}
			delete(l.buckets, k)
		}
	}

	l.buckets[key] = rateBucket{count: 1, resetAt: now.Add(l.window)}
	return true
}
