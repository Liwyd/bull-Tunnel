package auth

import (
	"sync"
	"time"
)

// Limiter is the rate limiter of the login.
//
// The panel is one password away from everything it manages, so a wrong
// password must not be free to try. The limiter counts the failures of an
// address in a moving window and blocks it for a while after too many.
type Limiter struct {
	limit  int
	window float64
	block  float64

	mu    sync.Mutex
	fails map[string][]float64
	until map[string]float64
	seen  int
}

// NewLimiter returns a limiter.
func NewLimiter(limit int, window int, block int) *Limiter {
	return &Limiter{
		limit:  limit,
		window: float64(window),
		block:  float64(block),
		fails:  map[string][]float64{},
		until:  map[string]float64{},
	}
}

// prune forgets the failures of an address that are older than the window.
func (l *Limiter) prune(who string, now float64) {
	queue := l.fails[who]
	kept := []float64{}
	for _, when := range queue {
		if now-when <= l.window {
			kept = append(kept, when)
		}
	}
	l.fails[who] = kept
}

// pruneAll forgets the addresses that have not failed for a while.
//
// The map of the failures grows with every address that is ever seen, and
// an attacker can make it grow without end. Every so often the addresses
// whose failures have all aged out are dropped.
func (l *Limiter) pruneAll(now float64) {
	l.seen++
	if l.seen%64 != 0 {
		return
	}
	for who := range l.fails {
		l.prune(who, now)
		if len(l.fails[who]) == 0 {
			delete(l.fails, who)
		}
	}
	for who, until := range l.until {
		if until <= now {
			delete(l.until, who)
		}
	}
}

// Allowed tells whether an address may try, and how long it must wait.
func (l *Limiter) Allowed(who string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := float64(time.Now().Unix())
	if until := l.until[who]; until > now {
		return false, int(until - now)
	}
	l.pruneAll(now)
	l.prune(who, now)
	return true, 0
}

// Fail counts a failure of an address.
func (l *Limiter) Fail(who string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := float64(time.Now().Unix())
	l.prune(who, now)
	l.fails[who] = append(l.fails[who], now)
	if len(l.fails[who]) >= l.limit {
		l.until[who] = now + l.block
		l.fails[who] = []float64{}
	}
}

// Success forgets the failures of an address: it got the password right.
func (l *Limiter) Success(who string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, who)
	delete(l.until, who)
}

// Forget drops everything about an address.
func (l *Limiter) Forget(who string) {
	l.Success(who)
}
