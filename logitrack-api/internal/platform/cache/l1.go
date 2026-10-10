package cache

import (
	"sync"
	"time"
)

// l1 is the optional in-process copy in front of Redis. An entry derives from one or more Redis keys
// (its deps); deleting any of them, here or on another replica through rt:cache, drops the entry.
// Entries live at most ttl, which bounds staleness when an invalidation message is lost.
type l1 struct {
	mu      sync.Mutex
	max     int
	ttl     time.Duration
	now     func() time.Time
	entries map[string]l1Entry
	byDep   map[string]map[string]struct{}
}

type l1Entry struct {
	v    any
	exp  time.Time
	deps []string
}

func newL1(maxEntries int, ttl time.Duration) *l1 {
	return &l1{
		max: maxEntries, ttl: ttl, now: time.Now,
		entries: map[string]l1Entry{}, byDep: map[string]map[string]struct{}{},
	}
}

func (s *l1) get(key string) (any, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	if !s.now().Before(e.exp) {
		s.removeLocked(key)
		return nil, false
	}
	return e.v, true
}

// put stores v under key; deps are the Redis keys it was read from (key itself when empty).
func (s *l1) put(key string, v any, deps ...string) {
	if s == nil {
		return
	}
	if len(deps) == 0 {
		deps = []string{key}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(key)
	if len(s.entries) >= s.max {
		now := s.now()
		for k, e := range s.entries {
			if !now.Before(e.exp) {
				s.removeLocked(k)
			}
		}
		for k := range s.entries { // still full: drop an arbitrary entry
			if len(s.entries) < s.max {
				break
			}
			s.removeLocked(k)
		}
	}
	s.entries[key] = l1Entry{v: v, exp: s.now().Add(s.ttl), deps: deps}
	for _, d := range deps {
		if s.byDep[d] == nil {
			s.byDep[d] = map[string]struct{}{}
		}
		s.byDep[d][key] = struct{}{}
	}
}

// drop removes every entry stored under, or derived from, one of the keys.
func (s *l1) drop(keys ...string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		s.removeLocked(k)
		for owner := range s.byDep[k] {
			s.removeLocked(owner)
		}
	}
}

func (s *l1) flush() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = map[string]l1Entry{}
	s.byDep = map[string]map[string]struct{}{}
}

func (s *l1) len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *l1) removeLocked(key string) {
	e, ok := s.entries[key]
	if !ok {
		return
	}
	delete(s.entries, key)
	for _, d := range e.deps {
		if set := s.byDep[d]; set != nil {
			delete(set, key)
			if len(set) == 0 {
				delete(s.byDep, d)
			}
		}
	}
}
