package waitlist

import (
	"sync"
	"time"
)

// RateLimiter allows a fixed number of attempts per client in a sliding
// window. It is in-memory, which suits a single server.
type RateLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string][]time.Time
	now    func() time.Time
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{limit: limit, window: window, hits: map[string][]time.Time{}, now: time.Now}
}

func (r *RateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	cutoff := now.Add(-r.window)

	kept := r.hits[key][:0]
	for _, t := range r.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.limit {
		r.hits[key] = kept
		return false
	}
	r.hits[key] = append(kept, now)

	// Drop idle clients now and then so the map can't grow without bound.
	if len(r.hits) > 10_000 {
		for k, ts := range r.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(r.hits, k)
			}
		}
	}
	return true
}
