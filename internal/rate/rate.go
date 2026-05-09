package rate

import (
	"sync"
	"time"
)

// Bucket is a simple token bucket. Capacity tokens, refills at refillPerSec.
type Bucket struct {
	mu     sync.Mutex
	tokens float64
	cap    float64
	refill float64 // tokens per second
	last   time.Time
}

func New(capacity int, per time.Duration) *Bucket {
	return &Bucket{
		tokens: float64(capacity),
		cap:    float64(capacity),
		refill: float64(capacity) / per.Seconds(),
		last:   time.Now(),
	}
}

func (b *Bucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * b.refill
	if b.tokens > b.cap {
		b.tokens = b.cap
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// Limiter keys -> Bucket; per-user/per-IP.
type Limiter struct {
	mu      sync.Mutex
	buckets map[string]*Bucket
	cap     int
	per     time.Duration
}

func NewLimiter(capacity int, per time.Duration) *Limiter {
	return &Limiter{buckets: map[string]*Bucket{}, cap: capacity, per: per}
}

func (l *Limiter) Allow(key string) bool {
	now := time.Now()
	l.mu.Lock()
	for k, b := range l.buckets {
		b.mu.Lock()
		idle := now.Sub(b.last)
		b.mu.Unlock()
		if idle > 2*l.per {
			delete(l.buckets, k)
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = New(l.cap, l.per)
		l.buckets[key] = b
	}
	l.mu.Unlock()
	return b.Allow()
}
