package ai

import (
	"sync"
	"time"
)

const maxCacheEntries = 1000

// cache remembers successful LLM answers for a limited time, so typing
// the same text again does not call the LLM again. It is safe for
// concurrent use: every HTTP request runs in its own goroutine.
type cache struct {
	mu      sync.Mutex
	ttl     time.Duration
	entries map[string]cacheEntry
	now     func() time.Time // replaced in tests to simulate time passing
}

type cacheEntry struct {
	value   any
	expires time.Time
}

func newCache(ttl time.Duration) *cache {
	return &cache{ttl: ttl, entries: make(map[string]cacheEntry), now: time.Now}
}

// get returns the cached value for key if it exists and has not expired.
func (c *cache) get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.now().After(e.expires) {
		delete(c.entries, key)
		return nil, false
	}
	return e.value, true
}

// set stores value under key until the TTL passes.
func (c *cache) set(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= maxCacheEntries {
		c.evict()
	}
	c.entries[key] = cacheEntry{value: value, expires: c.now().Add(c.ttl)}
}

// evict removes expired entries; if the cache is still full, it is
// cleared, so it can never grow without limit. The caller holds c.mu.
func (c *cache) evict() {
	now := c.now()
	for k, e := range c.entries {
		if now.After(e.expires) {
			delete(c.entries, k)
		}
	}
	if len(c.entries) >= maxCacheEntries {
		c.entries = make(map[string]cacheEntry)
	}
}
