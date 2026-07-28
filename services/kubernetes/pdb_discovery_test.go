package kubernetes

import (
	"slices"
	"testing"
)

// Discovery is a promise: a client reads it and then only issues the verbs it
// found. Advertising one the server does not serve moves the failure into the
// client — a reflector opening a watch that never streams, or a patch that
// 405s — far from the document that promised it.
//
// This pins the two together so adding a verb to the advertisement without
// implementing it fails here rather than in whatever tool hits it first.
func TestPDBAdvertisesOnlyImplementedVerbs(t *testing.T) {
	res := policyResources()
	if len(res) != 1 {
		t.Fatalf("expected one policy resource, got %d", len(res))
	}

	pdb := res[0]

	if pdb.Name != "poddisruptionbudgets" {
		t.Fatalf("unexpected resource %q", pdb.Name)
	}

	// pdb.go implements create, read, update and delete; it has no watch
	// stream and no patch handler.
	for _, unimplemented := range []string{"watch", "patch"} {
		if slices.Contains(pdb.Verbs, unimplemented) {
			t.Errorf("verb %q is advertised but not implemented", unimplemented)
		}
	}

	for _, required := range []string{"get", "list", "create", "update", "delete"} {
		if !slices.Contains(pdb.Verbs, required) {
			t.Errorf("verb %q is implemented but not advertised", required)
		}
	}
}
