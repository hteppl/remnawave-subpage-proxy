package panel

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Fetcher interface {
	SubscriptionInfo(ctx context.Context, shortUUID, realIP string) (*Info, error)
}

type entry struct {
	info      *Info
	err       error
	expiresAt time.Time
}

type call struct {
	done chan struct{}
	info *Info
	err  error
}

// Cache memoises info lookups and collapses concurrent ones for the same short
// UUID. Clients poll on a fixed interval, so a short TTL removes most load.
type Cache struct {
	fetcher     Fetcher
	ttl         time.Duration
	negativeTTL time.Duration
	maxEntries  int

	mu       sync.Mutex
	entries  map[string]entry
	inFlight map[string]*call

	now func() time.Time
}

// NewCache wraps fetcher. A non-positive ttl still collapses duplicates.
func NewCache(fetcher Fetcher, ttl, negativeTTL time.Duration, maxEntries int) *Cache {
	return &Cache{
		fetcher:     fetcher,
		ttl:         ttl,
		negativeTTL: negativeTTL,
		maxEntries:  maxEntries,
		entries:     make(map[string]entry),
		inFlight:    make(map[string]*call),
		now:         time.Now,
	}
}

func (c *Cache) SubscriptionInfo(ctx context.Context, shortUUID, realIP string) (*Info, error) {
	if info, err, ok := c.lookup(shortUUID); ok {
		return info, err
	}

	c.mu.Lock()
	if e, ok := c.entries[shortUUID]; ok && c.now().Before(e.expiresAt) {
		c.mu.Unlock()
		return e.info, e.err
	}
	if existing, ok := c.inFlight[shortUUID]; ok {
		c.mu.Unlock()
		select {
		case <-existing.done:
			return existing.info, existing.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	pending := &call{done: make(chan struct{})}
	c.inFlight[shortUUID] = pending
	c.mu.Unlock()

	// Detached: a client hanging up must not cancel other waiters' lookup.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	pending.info, pending.err = c.fetcher.SubscriptionInfo(fetchCtx, shortUUID, realIP)
	cancel()

	c.mu.Lock()
	delete(c.inFlight, shortUUID)
	c.store(shortUUID, pending.info, pending.err)
	c.mu.Unlock()

	close(pending.done)
	return pending.info, pending.err
}

func (c *Cache) lookup(shortUUID string) (*Info, error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[shortUUID]
	if !ok || !c.now().Before(e.expiresAt) {
		return nil, nil, false
	}
	return e.info, e.err, true
}

// store caches ErrNotFound negatively; transient failures are not cached.
func (c *Cache) store(shortUUID string, info *Info, err error) {
	var ttl time.Duration
	switch {
	case err == nil:
		ttl = c.ttl
	case errors.Is(err, ErrNotFound):
		ttl = c.negativeTTL
	default:
		return
	}
	if ttl <= 0 {
		return
	}

	c.evictIfNeededLocked()
	c.entries[shortUUID] = entry{info: info, err: err, expiresAt: c.now().Add(ttl)}
}

// evictIfNeededLocked drops expired entries first, then arbitrary live ones.
func (c *Cache) evictIfNeededLocked() {
	if c.maxEntries <= 0 || len(c.entries) < c.maxEntries {
		return
	}

	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}

	for k := range c.entries {
		if len(c.entries) < c.maxEntries {
			break
		}
		delete(c.entries, k)
	}
}

func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}
