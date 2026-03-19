// Package memstore provides a generic thread-safe in-memory key-value store.
package memstore

import "sync"

// Store is a generic thread-safe in-memory key-value store.
type Store[V any] struct {
	mu    sync.RWMutex
	items map[string]V
}

// New creates a new empty Store.
func New[V any]() *Store[V] {
	return &Store[V]{items: make(map[string]V)}
}

// Get retrieves a value by key. Returns the value and true if found.
func (s *Store[V]) Get(key string) (V, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.items[key]

	return v, ok
}

// Set stores a value at the given key.
func (s *Store[V]) Set(key string, value V) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[key] = value
}

// Delete removes a value by key. Returns true if the key was present.
func (s *Store[V]) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, ok := s.items[key]
	if ok {
		delete(s.items, key)
	}

	return ok
}

// Has returns true if the key exists.
func (s *Store[V]) Has(key string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.items[key]

	return ok
}

// Len returns the number of items in the store.
func (s *Store[V]) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.items)
}

// Keys returns all keys in the store.
func (s *Store[V]) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0, len(s.items))

	for k := range s.items {
		keys = append(keys, k)
	}

	return keys
}

// All returns a copy of all items in the store.
func (s *Store[V]) All() map[string]V {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]V, len(s.items))

	for k, v := range s.items {
		result[k] = v
	}

	return result
}

// Update atomically reads, modifies, and writes a value. Returns false if key not found.
func (s *Store[V]) Update(key string, fn func(V) V) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.items[key]
	if !ok {
		return false
	}

	s.items[key] = fn(v)

	return true
}

// SetIfAbsent stores a value only if the key does not already exist. Returns true if set.
func (s *Store[V]) SetIfAbsent(key string, value V) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.items[key]; ok {
		return false
	}

	s.items[key] = value

	return true
}

// Clear removes all items from the store.
func (s *Store[V]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.items = make(map[string]V)
}

// Filter returns all values matching the predicate.
func (s *Store[V]) Filter(fn func(key string, value V) bool) map[string]V {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make(map[string]V)

	for k, v := range s.items {
		if fn(k, v) {
			result[k] = v
		}
	}

	return result
}
