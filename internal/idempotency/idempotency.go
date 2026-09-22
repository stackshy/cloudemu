// Package idempotency provides a shared token -> resource-id store that lets a
// Create* operation detect a retried request and replay its original result
// instead of minting a duplicate resource: the standard AWS
// ClientToken/IdempotencyToken contract. A network-timeout retry (the exact
// scenario these tokens exist for) must observe the SAME resource the first,
// successful-but-unacknowledged call created, not a second one.
//
// A provider holds one Store per idempotent operation and routes the create
// through Do. The store records only the minted resource's id (never a copy of
// the resource), so a replay always re-reads the live record: an update made
// since the create is visible, and a deleted resource is a miss that creates
// afresh instead of replaying a ghost. Do also serializes concurrent callers
// that share a token, so a burst of same-token requests yields exactly one
// resource. The zero value is not usable; construct with New.
package idempotency

import (
	"context"
	"strings"
	"sync"
	"time"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// DefaultTTL is the dedup window for operations whose API reference documents
// no idempotency-token lifetime: long enough to cover any realistic SDK retry
// (seconds, occasionally minutes under backoff), short enough that a
// deliberately reused token eventually mints a new resource again.
const DefaultTTL = 5 * time.Minute

// Scoped narrows token to the natural resource key it was sent for (e.g. a
// schedule's group+name), so the same token reused on a DIFFERENT resource never
// replays the wrong one. It returns "" for an empty token, preserving the
// "no token, no dedup" contract of Do.
func Scoped(token string, scope ...string) string {
	if token == "" {
		return ""
	}

	parts := make([]string, 0, len(scope)+1)
	parts = append(parts, scope...)

	return strings.Join(append(parts, token), "\x00")
}

// entry is one stored token -> id pair with its expiry.
type entry struct {
	id       string
	expireAt time.Time
}

// tokenLock serializes the creates carrying one token; refs counts the callers
// holding or waiting on it so the lock is dropped once nobody needs it.
type tokenLock struct {
	mu   sync.Mutex
	refs int
}

// Store is a thread-safe token -> resource-id map with a per-entry TTL. The zero
// value is not usable; construct with New.
type Store struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]entry
	locks   map[string]*tokenLock
}

// New returns an empty Store whose entries live for ttl after the create that
// recorded them.
func New(ttl time.Duration) *Store {
	return &Store{ttl: ttl, entries: make(map[string]entry), locks: make(map[string]*tokenLock)}
}

// Do runs one idempotent create keyed by token and returns its result.
//
// When token already maps to a live id, replay re-reads that resource (typically
// the service's own Describe/Get method, so a replay reports exactly what a read
// would); if it
// succeeds its result is returned without creating anything. Otherwise (no
// entry, an expired entry, or a resource deleted since, i.e. replay NotFound),
// create runs, and on success idOf's id for the new resource is recorded under
// token.
// Callers sharing a token run one at a time, so a concurrent retry waits for
// the first create and then replays it rather than racing it.
//
// An empty token means the caller asked for no dedup: create runs directly and
// nothing is recorded.
func Do[R any](
	ctx context.Context, s *Store, token string, now time.Time,
	replay func(ctx context.Context, id string) (R, error), create func() (R, error), idOf func(R) string,
) (R, error) {
	if token == "" {
		return create()
	}

	unlock := s.lock(token)
	defer unlock()

	if id, ok := s.lookup(token, now); ok {
		out, err := replay(ctx, id)
		if err == nil {
			return out, nil
		}

		// Only a vanished resource means "create afresh"; any other replay
		// failure (a canceled context, an injected fault) is returned as is,
		// because creating here would mint the duplicate the token exists to
		// prevent.
		if !cerrors.IsNotFound(err) {
			return out, err
		}
	}

	out, err := create()
	if err != nil {
		return out, err
	}

	s.put(token, now, idOf(out))

	return out, nil
}

// lock acquires token's per-token lock and returns its release.
func (s *Store) lock(token string) func() {
	s.mu.Lock()

	l := s.locks[token]
	if l == nil {
		l = &tokenLock{}
		s.locks[token] = l
	}

	l.refs++
	s.mu.Unlock()

	l.mu.Lock()

	return func() {
		l.mu.Unlock()

		s.mu.Lock()
		defer s.mu.Unlock()

		l.refs--
		if l.refs == 0 {
			delete(s.locks, token)
		}
	}
}

// lookup returns the id recorded under token, if it has not expired as of now.
func (s *Store) lookup(token string, now time.Time) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.entries[token]
	if !ok {
		return "", false
	}

	if now.After(e.expireAt) {
		delete(s.entries, token)

		return "", false
	}

	return e.id, true
}

// put records id under token for the store's ttl from now. A non-positive ttl
// records nothing.
func (s *Store) put(token string, now time.Time, id string) {
	if s.ttl <= 0 {
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

	s.entries[token] = entry{id: id, expireAt: now.Add(s.ttl)}
}

// Forget drops every token recorded for id. A provider calls it when it deletes
// a resource whose id is derived from its name, so a later same-name resource
// created by a different request is never replayed to the original token.
func (s *Store) Forget(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, e := range s.entries {
		if e.id == id {
			delete(s.entries, k)
		}
	}
}
