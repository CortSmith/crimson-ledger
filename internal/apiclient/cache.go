package apiclient

import (
	"container/list"
	"sync"
)

const etagCacheSize = 32

type etagEntry struct {
	key, etag string
	body      []byte
}

// etagCache is a small LRU of (etag, body) per canonical URL.
type etagCache struct {
	mu    sync.Mutex
	max   int
	order *list.List // front = most recent
	items map[string]*list.Element
}

func newETagCache(limit int) *etagCache {
	return &etagCache{max: limit, order: list.New(), items: map[string]*list.Element{}}
}

func (c *etagCache) get(key string) (etag string, body []byte, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return "", nil, false
	}
	c.order.MoveToFront(el)
	e := el.Value.(*etagEntry)
	return e.etag, e.body, true
}

func (c *etagCache) put(key, etag string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		*el.Value.(*etagEntry) = etagEntry{key, etag, body}
		c.order.MoveToFront(el)
		return
	}
	c.items[key] = c.order.PushFront(&etagEntry{key, etag, body})
	if c.order.Len() > c.max {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.items, old.Value.(*etagEntry).key)
	}
}

func (c *etagCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
