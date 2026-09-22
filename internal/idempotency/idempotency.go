// Package idempotency provides a shared, generic token->result store that lets
// a Create* operation detect a retried request and replay its original result
// instead of minting a duplicate resource — the standard AWS
// ClientToken/IdempotencyToken/CallerReference contract. A network-timeout
// retry (the exact scenario these tokens exist for) must observe the SAME
// resource the first, successful-but-unacknowledged call created, not a second
// one.
//
// A provider holds one Store[V] per idempotent operation (V is whatever the
// operation needs to replay on a repeat token — a bare id/ARN string, or a full
// result struct) and consults it before doing any work: Lookup first, and Put
// once the real create succeeds. The zero value is not usable; construct with
// New.
package idempotency

import (
	"strings"
	"sync"
	"time"
)

// DefaultTTL is the dedup window used by services with no documented
// idempotency-token lifetime of their own: long enough to cover any realistic
// SDK retry (seconds, occasionally minutes under backoff), short enough that a
// deliberately reused token eventually mints a new resource again.
const DefaultTTL = 5 * time.Minute

// Scoped narrows token to the natural resource key it was sent for (e.g. a
// schedule's group+name), so the same token reused on a DIFFERENT resource never
// replays the wrong one. It returns "" for an empty token, preserving the
// "no token, no dedup" contract of Lookup and Put.
func Scoped(token string, scope ...string) string {
	if token == "" {
		return ""
	}

	parts := make([]string, 0, len(scope)+1)
	parts = append(parts, scope...)

	return strings.Join(append(parts, token), "\x00")
}

// entry is one stored token -> value pair with its expiry.
type entry[V any] struct {
	value    V
	expireAt time.Time
}

// Store is a thread-safe token -> V map with a per-entry TTL. The zero value is
// not usable; construct with New.
type Store[V any] struct {
	mu      sync.Mutex
	entries map[string]entry[V]
}

// New returns an empty, ready-to-use Store.
func New[V any]() *Store[V] {
	return &Store[V]{entries: make(map[string]entry[V])}
}

// Lookup returns the value previously Put under token, if token is non-empty
// and the entry has not expired as of now. An empty token never matches — AWS
// treats an absent idempotency token as "no dedup requested", so a caller that
// never sends one can legitimately create duplicates.
func (s *Store[V]) Lookup(token string, now time.Time) (V, bool) {
	var zero V

	if token == "" {
		return zero, false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[token]
	if !ok {
		return zero, false
	}

	if now.After(e.expireAt) {
		delete(s.entries, token)

		return zero, false
	}

	return e.value, true
}

// Put records value under token, valid for ttl from now. An empty token or a
// non-positive ttl is a no-op, since there is nothing to dedup.
func (s *Store[V]) Put(token string, now time.Time, ttl time.Duration, value V) {
	if token == "" || ttl <= 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Sweep expired entries so a long-lived server's token map stays bounded by
	// the tokens seen within one TTL, not every token ever sent.
	for k, e := range s.entries {
		if now.After(e.expireAt) {
			delete(s.entries, k)
		}
	}

	s.entries[token] = entry[V]{value: value, expireAt: now.Add(ttl)}
}

// Delete drops token's entry, if any. Idempotent. Used when the resource a
// token minted is deleted, freeing the token for reuse without waiting out its
// TTL.
func (s *Store[V]) Delete(token string) {
	if token == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.entries, token)
}
