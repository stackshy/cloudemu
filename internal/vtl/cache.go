package vtl

import (
	"container/list"
	"sync"
)

// Cache weights. A parsed template holds roughly astBytesPerSourceByte bytes
// of syntax tree per byte of source, and every entry costs entryOverhead.
const (
	astBytesPerSourceByte = 20
	entryOverhead         = 1 << 10
)

// Cache is a least-recently-used cache of parsed templates keyed by their
// source, so a template is parsed once rather than on every render. It is
// bounded by the estimated memory of what it holds, not by entry count. A
// parse failure is cached too. A parsed Template is read-only, so one cached
// template may render concurrently.
type Cache struct {
	mu       sync.Mutex
	maxBytes int
	bytes    int
	order    *list.List
	items    map[string]*list.Element
}

type cacheEntry struct {
	src    string
	tmpl   *Template
	err    error
	weight int
}

// NewCache returns a cache holding at most about maxBytes of parsed
// templates.
func NewCache(maxBytes int) *Cache {
	return &Cache{maxBytes: maxBytes, order: list.New(), items: map[string]*list.Element{}}
}

// weight estimates the memory a parsed template of src holds.
func weight(src string) int { return len(src)*astBytesPerSourceByte + entryOverhead }

// Parse returns the parsed template for src, parsing it on a miss. A template
// heavier than the whole cache is parsed but not kept.
func (c *Cache) Parse(src string) (*Template, error) {
	c.mu.Lock()

	if el, ok := c.items[src]; ok {
		c.order.MoveToFront(el)
		e, _ := el.Value.(*cacheEntry)
		c.mu.Unlock()

		return e.tmpl, e.err
	}

	c.mu.Unlock()

	tmpl, err := Parse(src)
	w := weight(src)

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.items[src]; ok || w > c.maxBytes {
		return tmpl, err
	}

	c.items[src] = c.order.PushFront(&cacheEntry{src: src, tmpl: tmpl, err: err, weight: w})
	c.bytes += w

	for c.bytes > c.maxBytes {
		oldest := c.order.Back()
		c.order.Remove(oldest)

		e, _ := oldest.Value.(*cacheEntry)
		delete(c.items, e.src)
		c.bytes -= e.weight
	}

	return tmpl, err
}

// Len returns the number of cached templates.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.order.Len()
}

// Bytes returns the estimated memory the cached templates hold.
func (c *Cache) Bytes() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.bytes
}
