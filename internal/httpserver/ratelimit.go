package httpserver

import (
	"sync"
	"time"
)

type visitor struct {
	since time.Time
	count int
}
type rateLimiter struct {
	mu       sync.Mutex
	limit    int
	visitors map[string]visitor
}

func newRateLimiter(limit int) *rateLimiter {
	return &rateLimiter{limit: limit, visitors: make(map[string]visitor)}
}

func (r *rateLimiter) allow(key string) bool {
	if r.limit == 0 {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	v := r.visitors[key]
	if now.Sub(v.since) >= time.Minute {
		v = visitor{since: now}
	}
	if v.since.IsZero() {
		v.since = now
	}
	if v.count >= r.limit {
		r.visitors[key] = v
		return false
	}
	v.count++
	r.visitors[key] = v
	if len(r.visitors) > 10_000 {
		for k, old := range r.visitors {
			if now.Sub(old.since) > 2*time.Minute {
				delete(r.visitors, k)
			}
		}
	}
	return true
}
