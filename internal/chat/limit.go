package chat

import (
	"sync"
	"time"
)

// bucket is a token bucket: rate tokens a second, at most burst saved up.
type bucket struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func newBucket(rate, burst float64) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: time.Now()}
}

// allow takes one token if there is one.
func (b *bucket) allow(now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// limiter keeps one bucket per key (a username), shared by all of that
// person's devices: twenty messages a second is a person's budget, not a
// socket's, or opening a second tab would double it.
type limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*bucket
}

func newLimiter(rate, burst float64) *limiter {
	return &limiter{rate: rate, burst: burst, buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	b := l.buckets[key]
	if b == nil {
		b = newBucket(l.rate, l.burst)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.allow(time.Now())
}
