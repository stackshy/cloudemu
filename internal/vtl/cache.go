package vtl

import (
	"container/list"
	"sync"
)

// Cache is a bounded, least-recently-used cache of parsed templates keyed by
// their source, so a template is parsed once rather than on every render. A
// parse failure is cached too. A parsed Template is read-only, so one cached
// template may render concurrently.
type Cache struct {
	mu    sync.Mutex
	max   int
	order *list.List
	items map[string]*list.Element
}

type cacheEntry struct {
	src  string
	tmpl *Template
	err  error
}

// NewCache returns a cache holding at most maxEntries templates.
func NewCache(maxEntries int) *Cache {
	return &Cache{max: maxEntries, order: list.New(), items: map[string]*list.Element{}}
}

// Parse returns the parsed template for src, parsing it on a miss.
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

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.items[src]; !ok {
		c.items[src] = c.order.PushFront(&cacheEntry{src: src, tmpl: tmpl, err: err})

		for c.order.Len() > c.max {
			oldest := c.order.Back()
			c.order.Remove(oldest)

			e, _ := oldest.Value.(*cacheEntry)
			delete(c.items, e.src)
		}
	}

	return tmpl, err
}

// Len returns the number of cached templates.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.order.Len()
}
