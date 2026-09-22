package idempotency_test

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idempotency"
)

// fakeResources is a minimal live-record table standing in for a provider store.
type fakeResources struct {
	mu   sync.Mutex
	next int
	live map[string]string
}

func newFake() *fakeResources { return &fakeResources{live: map[string]string{}} }

var errGone = errors.New("gone")

func (f *fakeResources) replay(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, ok := f.live[id]; !ok {
		return "", errGone
	}

	return id, nil
}

func (f *fakeResources) create() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.next++
	id := "res-" + strconv.Itoa(f.next)
	f.live[id] = id

	return id, nil
}

func idOf(id string) string { return id }

func do(s *idempotency.Store, f *fakeResources, token string, now time.Time) string {
	out, _ := idempotency.Do(context.Background(), s, token, now, f.replay, f.create, idOf)

	return out
}

func TestDoReplaysSameToken(t *testing.T) {
	s, f, now := idempotency.New(time.Minute), newFake(), time.Unix(0, 0)

	first := do(s, f, "tok", now)
	if again := do(s, f, "tok", now); again != first {
		t.Fatalf("same token = %q, want replay of %q", again, first)
	}

	if other := do(s, f, "tok-2", now); other == first {
		t.Fatalf("different token replayed %q", first)
	}
}

func TestDoEmptyTokenNeverDedups(t *testing.T) {
	s, f, now := idempotency.New(time.Minute), newFake(), time.Unix(0, 0)

	if do(s, f, "", now) == do(s, f, "", now) {
		t.Fatal("empty token must never replay")
	}
}

func TestDoExpiresAfterTTL(t *testing.T) {
	s, f, now := idempotency.New(time.Minute), newFake(), time.Unix(0, 0)

	first := do(s, f, "tok", now)

	if got := do(s, f, "tok", now.Add(time.Minute)); got != first {
		t.Fatalf("within ttl = %q, want %q", got, first)
	}

	if got := do(s, f, "tok", now.Add(2*time.Minute)); got == first {
		t.Fatal("expired token must create anew")
	}
}

func TestDoNonPositiveTTLRecordsNothing(t *testing.T) {
	s, f, now := idempotency.New(0), newFake(), time.Unix(0, 0)

	if do(s, f, "tok", now) == do(s, f, "tok", now) {
		t.Fatal("zero ttl must not record the token")
	}
}

func TestDoDeletedResourceIsMiss(t *testing.T) {
	s, f, now := idempotency.New(time.Minute), newFake(), time.Unix(0, 0)

	first := do(s, f, "tok", now)
	delete(f.live, first)

	second := do(s, f, "tok", now)
	if second == first {
		t.Fatalf("replayed deleted resource %q", first)
	}

	if got := do(s, f, "tok", now); got != second {
		t.Fatalf("token must now replay the recreated %q, got %q", second, got)
	}
}

func TestDoCreateErrorRecordsNothing(t *testing.T) {
	s, f, now := idempotency.New(time.Minute), newFake(), time.Unix(0, 0)
	boom := errors.New("boom")

	_, err := idempotency.Do(context.Background(), s, "tok", now, f.replay, func() (string, error) { return "", boom }, idOf)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}

	if got := do(s, f, "tok", now); got != "res-1" {
		t.Fatalf("failed create must not bind the token, got %q", got)
	}
}

func TestDoConcurrentSameTokenCreatesOnce(t *testing.T) {
	s, now := idempotency.New(time.Minute), time.Unix(0, 0)
	f := newFake()

	var creates atomic.Int32

	create := func() (string, error) {
		creates.Add(1)
		time.Sleep(time.Millisecond) // widen the window a check-then-set race would hit

		return f.create()
	}

	const n = 32

	results := make([]string, n)

	var wg sync.WaitGroup

	for i := range n {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i], _ = idempotency.Do(context.Background(), s, "burst", now, f.replay, create, idOf)
		}()
	}

	wg.Wait()

	if creates.Load() != 1 {
		t.Fatalf("create ran %d times, want exactly 1", creates.Load())
	}

	for i := range n {
		if results[i] != results[0] {
			t.Fatalf("caller %d got %q, want %q", i, results[i], results[0])
		}
	}
}

func TestDoDistinctTokensDoNotSerialize(t *testing.T) {
	s, now := idempotency.New(time.Minute), time.Unix(0, 0)
	f := newFake()

	release := make(chan struct{})
	entered := make(chan struct{})

	go func() {
		_, _ = idempotency.Do(context.Background(), s, "slow", now, f.replay, func() (string, error) {
			close(entered)
			<-release

			return f.create()
		}, idOf)
	}()

	<-entered

	done := make(chan struct{})

	go func() {
		do(s, f, "fast", now)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a different token blocked behind an in-flight create")
	}

	close(release)
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
	s, f, now := idempotency.New(time.Minute), newFake(), time.Unix(0, 0)

	old := do(s, f, "old", now)
	do(s, f, "new", now.Add(2*time.Minute))

	// Rewinding the clock would re-hit a surviving entry; the sweep dropped it.
	if got := do(s, f, "old", now); got == old {
		t.Fatalf("expired entry %q should have been swept by the later create", old)
	}
}
