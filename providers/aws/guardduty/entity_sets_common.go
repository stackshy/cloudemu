package guardduty

import (
	"time"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// setInput carries the shared fields every GuardDuty list-set create accepts
// (IP sets, threat-intel/entity sets, trusted-entity sets). It lets one generic
// create path serve every set resource without duplicating per-type bodies.
type setInput struct {
	detectorID          string
	name                string
	format              string
	location            string
	activate            bool
	expectedBucketOwner string
	tags                map[string]string
}

// setPatch carries the mutable fields a GuardDuty list-set update may change.
// Nil pointers are left unchanged, matching the driver's Update*Input contract.
type setPatch struct {
	name                *string
	location            *string
	activate            *bool
	expectedBucketOwner *string
}

// setStore abstracts the per-resource storage and copy semantics so the generic
// CRUD helpers below can operate on any list-set type (T) uniformly.
type setStore[T any] struct {
	// notFoundMsg is the ResourceNotFound message format (one %s for the ID).
	notFoundMsg string
	// storeOf returns the detector's backing map for this resource type.
	storeOf func(dd *detectorData) map[string]T
	// build mints a new stored value from create input and the assigned ID/time.
	build func(id string, in setInput, now time.Time) T
	// apply mutates a stored value with the patch and stamps updatedAt, returning
	// the updated value to write back.
	apply func(cur T, patch setPatch, now time.Time) T
	// copy returns a deep copy so readers cannot alias stored maps.
	copy func(T) T
}

// applySetPatch applies a setPatch to a stored set's shared mutable fields.
// Activate is mapped to the resource's Status string via setStatus. Nil pointers
// leave the corresponding field unchanged.
func applySetPatch(name, location, status, expectedBucketOwner *string, patch setPatch) {
	if patch.name != nil {
		*name = *patch.name
	}

	if patch.location != nil {
		*location = *patch.location
	}

	if patch.activate != nil {
		*status = setStatus(*patch.activate)
	}

	if patch.expectedBucketOwner != nil {
		*expectedBucketOwner = *patch.expectedBucketOwner
	}
}

// createSet validates the shared required fields, then inserts a new set under
// the detector lock so a concurrent DeleteDetector cannot orphan it.
//
//nolint:gocritic // hugeParam: shared create input passed by value for a clean per-resource call site.
func createSet[T any](m *Mock, s setStore[T], in setInput) (string, error) {
	if verr := validateSetInput(in.name, in.format, in.location); verr != nil {
		return "", verr
	}

	dd, err := m.getDetector(in.detectorID)
	if err != nil {
		return "", err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	setID := idgen.GenerateID("")
	s.storeOf(dd)[setID] = s.build(setID, in, m.now())

	return setID, nil
}

// getSet returns a deep copy of a stored set, or ResourceNotFound.
func getSet[T any](m *Mock, s setStore[T], detectorID, setID string) (*T, error) {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return nil, err
	}

	dd.mu.RLock()
	defer dd.mu.RUnlock()

	cur, ok := s.storeOf(dd)[setID]
	if !ok {
		return nil, notFound(s.notFoundMsg, setID)
	}

	out := s.copy(cur)

	return &out, nil
}

// updateSet patches a stored set's mutable fields under the detector lock.
func updateSet[T any](m *Mock, s setStore[T], detectorID, setID string, patch setPatch) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	store := s.storeOf(dd)

	cur, ok := store[setID]
	if !ok {
		return notFound(s.notFoundMsg, setID)
	}

	store[setID] = s.apply(cur, patch, m.now())

	return nil
}

// deleteSet removes a stored set under the detector lock, or ResourceNotFound.
func deleteSet[T any](m *Mock, s setStore[T], detectorID, setID string) error {
	dd, err := m.getDetector(detectorID)
	if err != nil {
		return err
	}

	dd.mu.Lock()
	defer dd.mu.Unlock()

	store := s.storeOf(dd)
	if _, ok := store[setID]; !ok {
		return notFound(s.notFoundMsg, setID)
	}

	delete(store, setID)

	return nil
}
