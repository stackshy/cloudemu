// Internal tests for the Phase 2 watch resume + BOOKMARK behavior of
// streamWatch (unexported, so this is package-internal).

package kubernetes

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamWatch_ResumeSkipsInitialSnapshot(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("")

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// resume=true → the two seed items must NOT be replayed as ADDED.
	streamWatch[string](ctx, rec, sub, []string{"seed-1", "seed-2"}, nil, watchOpts{resume: true})

	if body := rec.Body.String(); strings.Contains(body, "ADDED") {
		t.Fatalf("resume watch replayed the snapshot: %s", body)
	}
}

func TestStreamWatch_EmitsBookmarkAfterSync(t *testing.T) {
	b := newBroadcaster()
	sub := b.subscribe("")

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	streamWatch[string](ctx, rec, sub, []string{"seed"}, nil, watchOpts{
		bookmarks:   true,
		bookmarkObj: map[string]any{"metadata": map[string]any{"resourceVersion": "42"}},
	})

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"BOOKMARK"`) {
		t.Fatalf("expected a BOOKMARK event, got: %s", body)
	}

	if !strings.Contains(body, `"resourceVersion":"42"`) {
		t.Fatalf("BOOKMARK missing resourceVersion: %s", body)
	}
}

func TestWatchResumeAndBookmarkParsing(t *testing.T) {
	cases := []struct {
		query      string
		wantResume bool
		wantBM     bool
	}{
		{"", false, false},
		{"resourceVersion=0", false, false},
		{"resourceVersion=15", true, false},
		{"allowWatchBookmarks=true", false, true},
		{"resourceVersion=9&allowWatchBookmarks=true", true, true},
	}

	for _, tc := range cases {
		r := httptest.NewRequest("GET", "/x?"+tc.query, nil)
		if got := watchResume(r); got != tc.wantResume {
			t.Errorf("watchResume(%q) = %v, want %v", tc.query, got, tc.wantResume)
		}

		if got := watchBookmarksEnabled(r); got != tc.wantBM {
			t.Errorf("watchBookmarksEnabled(%q) = %v, want %v", tc.query, got, tc.wantBM)
		}
	}
}
