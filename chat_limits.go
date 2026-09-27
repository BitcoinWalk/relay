package main

import (
	"sync"
	"time"
)

type rateWindow struct {
	end  time.Time
	used int
}
type chatLimits struct {
	mu      sync.Mutex
	windows map[string]rateWindow
}

// Bounded fixed-window limiter. Restart resets counters, not persistent bans.
func (l *chatLimits) allow(key string, max int, period time.Duration, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = map[string]rateWindow{}
	}
	w, exists := l.windows[key]
	if !exists && len(l.windows) >= 4096 {
		for k, v := range l.windows {
			if !now.Before(v.end) {
				delete(l.windows, k)
			}
		}
		if len(l.windows) >= 4096 {
			return false
		}
	}
	if !exists || !now.Before(w.end) {
		w = rateWindow{end: now.Add(period)}
	}
	if w.used >= max {
		return false
	}
	w.used++
	l.windows[key] = w
	return true
}
