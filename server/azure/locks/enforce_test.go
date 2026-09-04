package locks

import (
	"net/http"
	"testing"
)

// seedLock stores a lock directly in the handler's store, bypassing the wire
// layer, so Enforce can be unit-tested without a server.
func seedLock(h *Handler, scope, name, level string) {
	h.store.put(scope, name, level, "")
}

const (
	sub   = "/subscriptions/S1"
	rg    = "/subscriptions/S1/resourceGroups/rg1"
	vm    = "/subscriptions/S1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm1"
	vm2   = "/subscriptions/S1/resourceGroups/rg1/providers/Microsoft.Compute/virtualMachines/vm2"
	vmB   = "/subscriptions/S1/resourceGroups/rg2/providers/Microsoft.Compute/virtualMachines/vmb"
	sub10 = "/subscriptions/S10/resourceGroups/rg1"
)

func TestEnforceLevelsAndMethods(t *testing.T) {
	tests := []struct {
		name        string
		lockScope   string
		lockLevel   string
		reqPath     string
		method      string
		wantBlocked bool
	}{
		// CanNotDelete: blocks DELETE only.
		{"cannotdelete blocks delete", rg, levelCanNotDelete, vm, http.MethodDelete, true},
		{"cannotdelete allows put", rg, levelCanNotDelete, vm, http.MethodPut, false},
		{"cannotdelete allows patch", rg, levelCanNotDelete, vm, http.MethodPatch, false},
		{"cannotdelete allows post", rg, levelCanNotDelete, vm, http.MethodPost, false},
		{"cannotdelete allows get", rg, levelCanNotDelete, vm, http.MethodGet, false},

		// ReadOnly: blocks DELETE + PUT + PATCH + POST.
		{"readonly blocks delete", rg, levelReadOnly, vm, http.MethodDelete, true},
		{"readonly blocks put", rg, levelReadOnly, vm, http.MethodPut, true},
		{"readonly blocks patch", rg, levelReadOnly, vm, http.MethodPatch, true},
		{"readonly blocks post", rg, levelReadOnly, vm, http.MethodPost, true},
		{"readonly allows get", rg, levelReadOnly, vm, http.MethodGet, false},
		{"readonly allows head", rg, levelReadOnly, vm, http.MethodHead, false},

		// Case-insensitive level matching.
		{"lowercase level blocks", rg, "readonly", vm, http.MethodPut, true},

		// Inheritance: subscription lock covers a resource beneath it.
		{"subscription lock covers resource delete", sub, levelCanNotDelete, vm, http.MethodDelete, true},
		{"subscription readonly covers resource write", sub, levelReadOnly, vm, http.MethodPut, true},

		// Lock at resource scope covers the resource itself and its extensions.
		{"resource lock blocks own delete", vm, levelCanNotDelete, vm, http.MethodDelete, true},
		{"resource lock blocks extension write", vm, levelReadOnly,
			vm + "/providers/microsoft.insights/diagnosticSettings/ds1", http.MethodPut, true},

		// Sibling isolation: a lock on vm1 does not affect vm2.
		{"sibling resource unaffected", vm, levelReadOnly, vm2, http.MethodPut, false},
		{"sibling rg unaffected", rg, levelReadOnly, vmB, http.MethodDelete, false},

		// Segment-boundary: /subscriptions/S1 must NOT match /subscriptions/S10.
		{"S1 lock does not match S10", sub, levelCanNotDelete, sub10, http.MethodDelete, false},

		// No lock at all.
		{"no lock allows delete", "", "", vm, http.MethodDelete, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := New()
			if tc.lockScope != "" {
				seedLock(h, tc.lockScope, "lk", tc.lockLevel)
			}

			_, _, blocked := h.Enforce(tc.reqPath, tc.method)
			if blocked != tc.wantBlocked {
				t.Fatalf("Enforce(%q, %s) blocked = %v, want %v", tc.reqPath, tc.method, blocked, tc.wantBlocked)
			}
		})
	}
}

// TestEnforceDeleteBlockedByDescendantLock proves that deleting a container
// (resource group) is blocked when a lock sits on a resource inside it, even
// though no lock covers the RG scope itself — the RG-delete-with-locked-child
// asymmetry.
func TestEnforceDeleteBlockedByDescendantLock(t *testing.T) {
	h := New()
	seedLock(h, vm, "child-lock", levelCanNotDelete)

	lockedScope, _, blocked := h.Enforce(rg, http.MethodDelete)
	if !blocked {
		t.Fatal("RG delete with a locked child: want blocked, got allowed")
	}

	if lockedScope != vm {
		t.Fatalf("blocking scope = %q, want the child %q", lockedScope, vm)
	}

	// A write to the RG is NOT blocked by a CanNotDelete child lock.
	if _, _, b := h.Enforce(rg, http.MethodPut); b {
		t.Fatal("RG write with a CanNotDelete child lock: want allowed, got blocked")
	}
}

// TestEnforceMostRestrictiveWins proves that when both a CanNotDelete and a
// ReadOnly lock cover a path, a write still finds the ReadOnly and blocks.
func TestEnforceMostRestrictiveWins(t *testing.T) {
	h := New()
	seedLock(h, rg, "nodel", levelCanNotDelete)
	seedLock(h, sub, "ro", levelReadOnly)

	if _, _, blocked := h.Enforce(vm, http.MethodPut); !blocked {
		t.Fatal("write covered by CanNotDelete + ReadOnly: want blocked (ReadOnly wins), got allowed")
	}
}

// TestEnforceMessageNamesMostSpecificLock proves the tightest (longest-scope)
// covering lock is reported for the error message.
func TestEnforceMessageNamesMostSpecificLock(t *testing.T) {
	h := New()
	seedLock(h, sub, "sub-lock", levelReadOnly)
	seedLock(h, rg, "rg-lock", levelReadOnly)

	lockedScope, _, blocked := h.Enforce(vm, http.MethodPatch)
	if !blocked {
		t.Fatal("want blocked")
	}

	if lockedScope != rg {
		t.Fatalf("reported scope = %q, want the more specific %q", lockedScope, rg)
	}
}
