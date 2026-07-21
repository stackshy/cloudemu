package lambda

import (
	"context"
	"testing"
	"time"
)

// TestTimestampsUseConfiguredClock locks LastModified to the configured
// clock: with a fake clock pinned to 2025-01-01, function timestamps must
// not leak wall-clock time.
func TestTimestampsUseConfiguredClock(t *testing.T) {
	m := newTestMock()

	fn, err := m.CreateFunction(context.Background(), defaultFuncConfig())
	if err != nil {
		t.Fatal(err)
	}

	got, err := time.Parse(time.RFC3339, fn.LastModified)
	if err != nil {
		t.Fatalf("LastModified %q not RFC3339: %v", fn.LastModified, err)
	}

	want := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("LastModified = %v, want fake-clock time %v (wall clock leaked)", got, want)
	}
}
