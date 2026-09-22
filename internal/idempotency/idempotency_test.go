package idempotency_test

import (
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idempotency"
)

func TestLookupMissEmptyToken(t *testing.T) {
	s := idempotency.New[string]()
	now := time.Unix(0, 0)

	s.Put("", now, time.Minute, "value")

	if _, ok := s.Lookup("", now); ok {
		t.Fatalf("empty token should never match")
	}
}

func TestPutThenLookupHit(t *testing.T) {
	s := idempotency.New[string]()
	now := time.Unix(0, 0)

	s.Put("tok-1", now, time.Minute, "res-1")

	got, ok := s.Lookup("tok-1", now)
	if !ok || got != "res-1" {
		t.Fatalf("expected hit res-1, got %q ok=%v", got, ok)
	}
}

func TestLookupExpiresAfterTTL(t *testing.T) {
	s := idempotency.New[string]()
	now := time.Unix(0, 0)

	s.Put("tok-1", now, time.Minute, "res-1")

	if _, ok := s.Lookup("tok-1", now.Add(2*time.Minute)); ok {
		t.Fatalf("expected expired entry to miss")
	}
}

func TestLookupUnknownToken(t *testing.T) {
	s := idempotency.New[string]()

	if _, ok := s.Lookup("nope", time.Unix(0, 0)); ok {
		t.Fatalf("expected miss for unknown token")
	}
}

func TestPutNonPositiveTTLIsNoop(t *testing.T) {
	s := idempotency.New[string]()
	now := time.Unix(0, 0)

	s.Put("tok-1", now, 0, "res-1")

	if _, ok := s.Lookup("tok-1", now); ok {
		t.Fatalf("non-positive TTL should not store an entry")
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	s := idempotency.New[string]()
	now := time.Unix(0, 0)

	s.Put("tok-1", now, time.Minute, "res-1")
	s.Delete("tok-1")

	if _, ok := s.Lookup("tok-1", now); ok {
		t.Fatalf("expected entry to be gone after Delete")
	}
}

func TestDeleteEmptyTokenIsNoop(t *testing.T) {
	s := idempotency.New[string]()
	// Must not panic on an empty token with no entries.
	s.Delete("")
}

func TestConcurrentPutLookup(t *testing.T) {
	s := idempotency.New[int]()
	now := time.Unix(0, 0)

	done := make(chan struct{})

	go func() {
		for i := 0; i < 1000; i++ {
			s.Put("tok", now, time.Minute, i)
		}

		close(done)
	}()

	for i := 0; i < 1000; i++ {
		s.Lookup("tok", now)
	}

	<-done
}

func TestScopedEmptyTokenStaysEmpty(t *testing.T) {
	if got := idempotency.Scoped("", "group", "name"); got != "" {
		t.Fatalf("Scoped with empty token = %q, want empty", got)
	}
}

func TestScopedSeparatesResources(t *testing.T) {
	a := idempotency.Scoped("tok", "group", "a")
	b := idempotency.Scoped("tok", "group", "b")

	if a == b || a == "" {
		t.Fatalf("scoped keys must differ per resource: %q vs %q", a, b)
	}
}

func TestPutSweepsExpiredEntries(t *testing.T) {
	s := idempotency.New[string]()
	now := time.Unix(0, 0)

	s.Put("old", now, time.Minute, "v1")
	s.Put("new", now.Add(2*time.Minute), time.Minute, "v2")

	if _, ok := s.Lookup("old", now); ok {
		t.Fatalf("expired entry should have been swept by the later Put")
	}
}
