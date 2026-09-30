package httpserver

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type loginAttempt struct {
	first   time.Time
	count   int
	blocked time.Time
}

type loginLimiter struct {
	mu          sync.Mutex
	attempts    map[string]loginAttempt
	maxAttempts int
	window      time.Duration
	block       time.Duration
	lastSweep   time.Time
	maxEntries  int
}

func newLoginLimiter(maxAttempts int, window, block time.Duration) *loginLimiter {
	return &loginLimiter{attempts: map[string]loginAttempt{}, maxAttempts: maxAttempts, window: window, block: block, maxEntries: 10000}
}

func (l *loginLimiter) key(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.ToLower(strings.TrimSpace(username)) + "|" + host
}

func (l *loginLimiter) sweep(now time.Time) {
	if !l.lastSweep.IsZero() && now.Sub(l.lastSweep) < time.Minute && len(l.attempts) < l.maxEntries {
		return
	}
	for key, attempt := range l.attempts {
		if now.After(attempt.blocked) && now.Sub(attempt.first) > l.window {
			delete(l.attempts, key)
		}
	}
	l.lastSweep = now
}

func (l *loginLimiter) RetryAfter(r *http.Request, username string) time.Duration {
	if l == nil {
		return 0
	}
	key := l.key(r, username)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	a, ok := l.attempts[key]
	if !ok {
		return 0
	}
	if !a.blocked.IsZero() && now.Before(a.blocked) {
		return a.blocked.Sub(now)
	}
	if now.Sub(a.first) > l.window {
		delete(l.attempts, key)
	}
	return 0
}

func (l *loginLimiter) Allowed(r *http.Request, username string) bool {
	return l.RetryAfter(r, username) <= 0
}

func (l *loginLimiter) Failure(r *http.Request, username string) {
	if l == nil {
		return
	}
	key := l.key(r, username)
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.sweep(now)
	if _, exists := l.attempts[key]; !exists && len(l.attempts) >= l.maxEntries {
		var oldestKey string
		var oldest time.Time
		for candidate, attempt := range l.attempts {
			if oldestKey == "" || attempt.first.Before(oldest) {
				oldestKey = candidate
				oldest = attempt.first
			}
		}
		delete(l.attempts, oldestKey)
	}
	a := l.attempts[key]
	if a.first.IsZero() || now.Sub(a.first) > l.window {
		a = loginAttempt{first: now}
	}
	a.count++
	if a.count >= l.maxAttempts {
		a.blocked = now.Add(l.block)
	}
	l.attempts[key] = a
}

func (l *loginLimiter) Success(r *http.Request, username string) {
	if l == nil {
		return
	}
	l.mu.Lock()
	delete(l.attempts, l.key(r, username))
	l.mu.Unlock()
}
