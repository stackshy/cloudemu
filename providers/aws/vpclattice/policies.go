package vpclattice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/vpclattice/driver"
)

const (
	authPolicyStateActive = "Active"
)

func (m *Mock) PutAuthPolicy(_ context.Context, resourceID, policy string) (*driver.AuthPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := idFromIdentifier(resourceID)

	p := &driver.AuthPolicy{
		Policy:        policy,
		State:         authPolicyStateActive,
		CreatedAt:     m.now(),
		LastUpdatedAt: m.now(),
	}
	if existing, ok := m.authPolicies.Get(key); ok {
		p.CreatedAt = existing.CreatedAt
	}

	m.authPolicies.Set(key, p)

	out := *p

	return &out, nil
}

func (m *Mock) GetAuthPolicy(_ context.Context, resourceID string) (*driver.AuthPolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	p, ok := m.authPolicies.Get(idFromIdentifier(resourceID))
	if !ok {
		return nil, errors.Newf(errors.NotFound, "auth policy for %q not found", resourceID)
	}

	out := *p

	return &out, nil
}

func (m *Mock) DeleteAuthPolicy(_ context.Context, resourceID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.authPolicies.Delete(idFromIdentifier(resourceID))

	return nil
}

func (m *Mock) PutResourcePolicy(_ context.Context, resourceARN, policy string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.resourcePolics.Set(resourceARN, policy)

	return nil
}

func (m *Mock) GetResourcePolicy(_ context.Context, resourceARN string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	policy, ok := m.resourcePolics.Get(resourceARN)
	if !ok {
		return "", errors.Newf(errors.NotFound, "resource policy for %q not found", resourceARN)
	}

	return policy, nil
}

func (m *Mock) DeleteResourcePolicy(_ context.Context, resourceARN string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.resourcePolics.Delete(resourceARN)

	return nil
}
