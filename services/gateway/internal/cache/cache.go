// Package cache is a TTL + LRU byte cache for immutable RPC responses.
package cache

import (
	"container/list"
	"sync"
	"time"
)

type entry struct {
	key     string
	val     []byte
	expires time.Time
}

// Cache is safe for concurrent use.
type Cache struct {
	mu  sync.Mutex
	cap int
	ttl time.Duration
	now func() time.Time
	ll  *list.List
	m   map[string]*list.Element
}

// New builds a cache; now may be nil.
func New(capacity int, ttl time.Duration, now func() time.Time) *Cache {
	if now == nil {
		now = time.Now
	}
	return &Cache{cap: capacity, ttl: ttl, now: now, ll: list.New(), m: map[string]*list.Element{}}
}

// Get returns a live entry and marks it most-recently-used.
func (c *Cache) Get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.m[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*entry)
	if !c.now().Before(e.expires) {
		c.ll.Remove(el)
		delete(c.m, key)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return e.val, true
}

// Set stores val, evicting the least-recently-used entry when full.
func (c *Cache) Set(key string, val []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	exp := c.now().Add(c.ttl)
	if el, ok := c.m[key]; ok {
		el.Value = &entry{key, val, exp}
		c.ll.MoveToFront(el)
		return
	}
	c.m[key] = c.ll.PushFront(&entry{key, val, exp})
	if c.ll.Len() > c.cap {
		last := c.ll.Back()
		c.ll.Remove(last)
		delete(c.m, last.Value.(*entry).key)
	}
}

// Len returns the number of stored entries.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len()
}
