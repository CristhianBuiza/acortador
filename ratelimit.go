package main

import (
	"sync"
	"time"
)

// limiter es una ventana fija por clave (IP), suficiente para un único proceso.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*bucket
	lastGC time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string]*bucket{}, lastGC: time.Now()}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if now.Sub(l.lastGC) > l.window {
		for k, b := range l.hits {
			if now.After(b.reset) {
				delete(l.hits, k)
			}
		}
		l.lastGC = now
	}

	b, ok := l.hits[key]
	if !ok || now.After(b.reset) {
		l.hits[key] = &bucket{count: 1, reset: now.Add(l.window)}
		return true
	}
	if b.count >= l.max {
		return false
	}
	b.count++
	return true
}
