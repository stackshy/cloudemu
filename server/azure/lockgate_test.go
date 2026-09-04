package azure

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu/v2/server/azure/locks"
)

// seedGateLock creates a lock in h by driving its own PUT handler, exactly as a
// caller would over the wire, so the gate reads the same store state.
func seedGateLock(t *testing.T, h *locks.Handler, scope, name, level string) {
	t.Helper()

	body := `{"properties":{"level":"` + level + `"}}`
	url := scope + "/providers/Microsoft.Authorization/locks/" + name
	req := httptest.NewRequest(http.MethodPut, url, strings.NewReader(body))
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("seed lock %q: status %d", name, rec.Code)
	}
}

func TestIsControlPlane(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/subscriptions/S1/resourceGroups/rg1", true},
		{"/SUBSCRIPTIONS/S1/resourceGroups/rg1", true},
		{"/mycontainer/blob.txt", false},
		{"/", false},
		{"", false},
	}

	for _, tc := range tests {
		if got := isControlPlane(tc.path); got != tc.want {
			t.Errorf("isControlPlane(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestOperationNoun(t *testing.T) {
	tests := map[string]string{
		http.MethodDelete: "delete",
		http.MethodPut:    "write",
		http.MethodPatch:  "write",
		http.MethodPost:   "action",
	}

	for method, want := range tests {
		if got := operationNoun(method); got != want {
			t.Errorf("operationNoun(%s) = %q, want %q", method, got, want)
		}
	}
}

const (
	gateRG  = "/subscriptions/S1/resourceGroups/rg1"
	gateVM  = "/subscriptions/S1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1"
	dataURL = "/mycontainer/blob.txt"
)

func TestLockGate(t *testing.T) {
	tests := []struct {
		name        string
		lockLevel   string // "" = no lock
		method      string
		path        string
		wantProceed bool
	}{
		{"no lock allows delete", "", http.MethodDelete, gateVM, true},
		{"cannotdelete blocks delete", "CanNotDelete", http.MethodDelete, gateVM, false},
		{"cannotdelete allows put", "CanNotDelete", http.MethodPut, gateVM, true},
		{"readonly blocks put", "ReadOnly", http.MethodPut, gateVM, false},
		{"readonly blocks post", "ReadOnly", http.MethodPost, gateVM, false},
		{"readonly allows get", "ReadOnly", http.MethodGet, gateVM, true},
		{"data plane exempt", "ReadOnly", http.MethodDelete, dataURL, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := locks.New()
			if tc.lockLevel != "" {
				seedGateLock(t, h, gateRG, "lk", tc.lockLevel)
			}

			gate := newLockGate(h)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, nil)

			_, proceed := gate(rec, req)
			if proceed != tc.wantProceed {
				t.Fatalf("proceed = %v, want %v", proceed, tc.wantProceed)
			}

			if !tc.wantProceed {
				if rec.Code != http.StatusConflict {
					t.Fatalf("status = %d, want 409", rec.Code)
				}

				if !strings.Contains(rec.Body.String(), "ScopeLocked") {
					t.Fatalf("body missing ScopeLocked: %s", rec.Body.String())
				}
			}
		})
	}
}

// TestLockGateSelfExemption proves the locks API is always allowed on a locked
// scope, so a caller can delete a lock to unlock — even a DELETE of a lock
// living under a ReadOnly-locked scope.
func TestLockGateSelfExemption(t *testing.T) {
	h := locks.New()
	seedGateLock(t, h, gateRG, "ro", "ReadOnly")

	gate := newLockGate(h)
	rec := httptest.NewRecorder()

	lockPath := gateRG + "/providers/Microsoft.Authorization/locks/ro"
	req := httptest.NewRequest(http.MethodDelete, lockPath, nil)

	if _, proceed := gate(rec, req); !proceed {
		t.Fatal("DELETE of a lock under a ReadOnly scope must be allowed (self-exemption)")
	}
}
