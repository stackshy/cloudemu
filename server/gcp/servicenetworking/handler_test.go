package servicenetworking

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const connPath = "/v1/services/servicenetworking.googleapis.com/connections"

func newTestHandler(t *testing.T) *Handler {
	t.Helper()

	h := New()

	for _, network := range []string{"projects/p/global/networks/a", "projects/p/global/networks/b"} {
		req := httptest.NewRequest(http.MethodPatch, connPath+"?network="+network,
			strings.NewReader(`{"reservedPeeringRanges":["r1"]}`))
		rec := httptest.NewRecorder()

		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("seed %s: got %d, want 200", network, rec.Code)
		}
	}

	return h
}

func connectionCount(t *testing.T, h *Handler) int {
	t.Helper()

	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.connections)
}

// TestRemove_RequiresNetwork pins the data-loss fix: a DELETE with no network
// parameter used to fall through to the "-" catch-all and wipe every stored
// connection, including networks the caller had nothing to do with.
func TestRemove_RequiresNetwork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		query string
	}{
		{name: "no query string at all", query: ""},
		{name: "empty network parameter", query: "?network="},
		{name: "the wildcard placeholder", query: "?network=-"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := newTestHandler(t)

			req := httptest.NewRequest(http.MethodDelete, connPath+tt.query, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("got status %d, want 400", rec.Code)
			}

			if got := connectionCount(t, h); got != 2 {
				t.Errorf("got %d connections left, want 2 — the delete destroyed "+
					"connections it did not name", got)
			}
		})
	}
}

// TestRemove_DeletesOnlyTheNamedNetwork is the other half: a properly targeted
// delete must still work, and must leave its neighbours alone.
func TestRemove_DeletesOnlyTheNamedNetwork(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodDelete,
		connPath+"?network=projects/p/global/networks/a", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", rec.Code)
	}

	if got := connectionCount(t, h); got != 1 {
		t.Fatalf("got %d connections left, want 1", got)
	}

	h.mu.RLock()
	_, survived := h.connections["projects/p/global/networks/b"]
	h.mu.RUnlock()

	if !survived {
		t.Error("the delete removed the wrong network's connection")
	}
}

// TestUpsert_BodyIsCapped checks the oversized body is rejected rather than
// read into memory unbounded.
func TestUpsert_BodyIsCapped(t *testing.T) {
	t.Parallel()

	h := New()

	huge := `{"x":"` + strings.Repeat("a", 2<<20) + `"}`
	req := httptest.NewRequest(http.MethodPatch, connPath+"?network=projects/p/global/networks/a",
		strings.NewReader(huge))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	// The decode is deliberately tolerant of an unreadable body, so the
	// request still completes — what matters is that the stored body is the
	// empty fallback rather than the multi-megabyte payload.
	h.mu.RLock()
	stored := h.connections["projects/p/global/networks/a"]
	h.mu.RUnlock()

	if len(stored) > int(1<<20) {
		t.Errorf("stored %d bytes — the body cap did not apply", len(stored))
	}
}
