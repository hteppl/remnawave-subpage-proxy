package subcache

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Status   int
	Header   http.Header
	Body     []byte
	StoredAt time.Time
}

func (e *Entry) size() int64 {
	n := int64(len(e.Body))
	for key, values := range e.Header {
		n += int64(len(key))
		for _, v := range values {
			n += int64(len(v))
		}
	}
	return n
}

// Cache is a fallback, not a read-through cache: nothing reads from it until a
// request to the upstream has failed.
type Cache struct {
	ttl      time.Duration
	maxBytes int64
	maxBody  int64

	mu      sync.Mutex
	entries map[string]*Entry
	bytes   int64

	now func() time.Time
}

func New(ttl time.Duration, maxBytes, maxBody int64) *Cache {
	return &Cache{
		ttl:      ttl,
		maxBytes: maxBytes,
		maxBody:  maxBody,
		entries:  make(map[string]*Entry),
		now:      time.Now,
	}
}

// MaxBody is the largest body worth keeping; bigger ones stream through.
func (c *Cache) MaxBody() int64 { return c.maxBody }

// Key identifies one variant of a response. Remnawave varies the payload by
// client; Accept-Encoding counts too, or a gzipped body could be replayed to a
// client that never asked for gzip.
func Key(shortUUID, clientType, userAgent, acceptEncoding string) string {
	var b strings.Builder
	b.Grow(len(shortUUID) + len(clientType) + len(userAgent) + len(acceptEncoding) + 3)
	b.WriteString(shortUUID)
	b.WriteByte(0)
	b.WriteString(clientType)
	b.WriteByte(0)
	b.WriteString(userAgent)
	b.WriteByte(0)
	b.WriteString(acceptEncoding)
	return b.String()
}

func (c *Cache) Get(key string) (*Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(entry.StoredAt) > c.ttl {
		c.removeLocked(key)
		return nil, false
	}
	return entry, true
}

func (c *Cache) Put(key string, entry *Entry) {
	if entry == nil || int64(len(entry.Body)) > c.maxBody {
		return
	}
	entry.StoredAt = c.now()

	c.mu.Lock()
	defer c.mu.Unlock()

	c.removeLocked(key)
	c.entries[key] = entry
	c.bytes += entry.size()
	c.evictLocked()
}

func (c *Cache) removeLocked(key string) {
	if existing, ok := c.entries[key]; ok {
		c.bytes -= existing.size()
		delete(c.entries, key)
	}
}

// evictLocked drops expired entries first, then the oldest, until under budget.
func (c *Cache) evictLocked() {
	if c.maxBytes <= 0 {
		return
	}

	now := c.now()
	for key, entry := range c.entries {
		if c.bytes <= c.maxBytes {
			return
		}
		if now.Sub(entry.StoredAt) > c.ttl {
			c.removeLocked(key)
		}
	}

	for c.bytes > c.maxBytes && len(c.entries) > 0 {
		var oldestKey string
		var oldest time.Time
		for key, entry := range c.entries {
			if oldestKey == "" || entry.StoredAt.Before(oldest) {
				oldestKey, oldest = key, entry.StoredAt
			}
		}
		c.removeLocked(oldestKey)
	}
}

func (c *Cache) Stats() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.bytes
}
