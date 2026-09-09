package cloudfront

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/services/cloudfront/driver"
)

// invalidationIDLen is the number of characters in an invalidation id
// ("I" + 13 uppercase alphanumerics).
const invalidationIDLen = 13

func newInvalidationID() string { return "I" + randToken(idAlphabet, invalidationIDLen) }

// CreateInvalidation records a synchronous invalidation against a distribution.
// The emulator has no edge cache, so the invalidation is Completed immediately.
func (m *Mock) CreateInvalidation(
	_ context.Context, distributionID string, in *driver.CreateInvalidationInput,
) (*driver.Invalidation, error) {
	if !m.dists.Has(distributionID) {
		return nil, driver.ErrNoSuchDistribution
	}

	inv := driver.Invalidation{
		ID:              newInvalidationID(),
		Status:          driver.InvalidationCompleted,
		CreateTime:      m.now(),
		CallerReference: in.CallerReference,
		Paths:           append([]string(nil), in.Paths...),
	}

	m.invMu.Lock()
	if m.invalidations[distributionID] == nil {
		m.invalidations[distributionID] = map[string]driver.Invalidation{}
	}

	m.invalidations[distributionID][inv.ID] = inv
	m.invMu.Unlock()

	return cloneInvalidation(&inv), nil
}

// GetInvalidation returns a distribution's invalidation by id.
func (m *Mock) GetInvalidation(_ context.Context, distributionID, invalidationID string) (*driver.Invalidation, error) {
	if !m.dists.Has(distributionID) {
		return nil, driver.ErrNoSuchDistribution
	}

	m.invMu.Lock()
	defer m.invMu.Unlock()

	inv, ok := m.invalidations[distributionID][invalidationID]
	if !ok {
		return nil, driver.ErrNoSuchInvalidation
	}

	return cloneInvalidation(&inv), nil
}

// ListInvalidations returns a distribution's invalidations, newest first.
func (m *Mock) ListInvalidations(_ context.Context, distributionID string) ([]driver.Invalidation, error) {
	if !m.dists.Has(distributionID) {
		return nil, driver.ErrNoSuchDistribution
	}

	m.invMu.Lock()
	defer m.invMu.Unlock()

	byID := m.invalidations[distributionID]
	out := make([]driver.Invalidation, 0, len(byID))

	for k := range byID {
		inv := byID[k]
		out = append(out, *cloneInvalidation(&inv))
	}

	// Deterministic order: most recent first, ties broken by id.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreateTime.Equal(out[j].CreateTime) {
			return out[i].CreateTime.After(out[j].CreateTime)
		}

		return out[i].ID < out[j].ID
	})

	return out, nil
}

func cloneInvalidation(inv *driver.Invalidation) *driver.Invalidation {
	cp := *inv
	cp.Paths = append([]string(nil), inv.Paths...)

	return &cp
}
