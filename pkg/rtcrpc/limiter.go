package rtcrpc

import (
	"bytes"
	"io"
	"sync"
	"time"
)

// Limiter is a token bucket: rate tokens per second, up to burst.
type Limiter struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// NewLimiter returns a full bucket.
func NewLimiter(rate, burst float64) *Limiter {
	return &Limiter{rate: rate, burst: burst, tokens: burst, now: time.Now}
}

// Allow takes one token if there is one.
func (l *Limiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.last.IsZero() {
		l.tokens += now.Sub(l.last).Seconds() * l.rate
		if l.tokens > l.burst {
			l.tokens = l.burst
		}
	}
	l.last = now
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }
