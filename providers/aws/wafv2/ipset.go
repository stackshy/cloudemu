package wafv2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

func copyIPSet(s *driver.IPSet) driver.IPSet {
	out := *s
	out.Tags = copyTags(s.Tags)
	out.Addresses = copyStrings(s.Addresses)

	return out
}

func (m *Mock) ipSetByName(scope, name string) bool {
	for _, sd := range m.ipSets.All() {
		sd.mu.RLock()
		match := sd.set.Scope == scope && sd.set.Name == name
		sd.mu.RUnlock()

		if match {
			return true
		}
	}

	return false
}

// CreateIPSet creates an IP set.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) CreateIPSet(_ context.Context, in driver.CreateIPSetInput) (*driver.IPSet, error) {
	if in.Name == "" || in.Scope == "" {
		return nil, invalidParameter("Name and Scope are required")
	}

	if in.IPAddressVersion == "" {
		return nil, invalidParameter("IPAddressVersion is required")
	}

	if err := validateScope(in.Scope); err != nil {
		return nil, err
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()

	if m.ipSetByName(in.Scope, in.Name) {
		return nil, duplicate("IP set %q already exists in scope %s", in.Name, in.Scope)
	}

	id := idgen.GenerateID("")
	set := driver.IPSet{
		ID:               id,
		Name:             in.Name,
		ARN:              m.arn(in.Scope, "ipset", in.Name, id),
		Scope:            in.Scope,
		Description:      in.Description,
		LockToken:        newLockToken(),
		IPAddressVersion: in.IPAddressVersion,
		Addresses:        copyStrings(in.Addresses),
		Tags:             copyTags(in.Tags),
	}

	m.ipSets.Set(key(in.Scope, id), &ipSetData{set: set})

	out := copyIPSet(&set)

	return &out, nil
}

func (m *Mock) getIPSetData(ref driver.Ref) (*ipSetData, error) {
	sd, ok := m.ipSets.Get(key(ref.Scope, ref.ID))
	if !ok {
		return nil, nonexistent("IP set %q not found in scope %s", ref.ID, ref.Scope)
	}

	return sd, nil
}

// GetIPSet returns an IP set by (scope,id).
func (m *Mock) GetIPSet(_ context.Context, ref driver.Ref) (*driver.IPSet, error) {
	sd, err := m.getIPSetData(ref)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := copyIPSet(&sd.set)

	return &out, nil
}

// UpdateIPSet replaces an IP set's addresses/description, enforcing the lock token.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) UpdateIPSet(_ context.Context, in driver.UpdateIPSetInput) (string, error) {
	sd, err := m.getIPSetData(driver.Ref{Scope: in.Scope, ID: in.ID})
	if err != nil {
		return "", err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.set.LockToken != in.LockToken {
		return "", staleLock("stale lock token for IP set %q", in.ID)
	}

	sd.set.Description = in.Description
	sd.set.Addresses = copyStrings(in.Addresses)
	sd.set.LockToken = newLockToken()

	return sd.set.LockToken, nil
}

// DeleteIPSet removes an IP set, enforcing the lock token.
func (m *Mock) DeleteIPSet(_ context.Context, ref driver.Ref, lockToken string) error {
	sd, err := m.getIPSetData(ref)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.set.LockToken != lockToken {
		return staleLock("stale lock token for IP set %q", ref.ID)
	}

	if m.itemReferencedByWebACL(sd.set.ARN) {
		return associated("IP set %q is referenced by one or more web ACLs", ref.ID)
	}

	m.ipSets.Delete(key(ref.Scope, ref.ID))

	return nil
}

// ListIPSets returns all IP sets in a scope.
func (m *Mock) ListIPSets(_ context.Context, scope string) ([]driver.IPSet, error) {
	all := m.ipSets.All()
	out := make([]driver.IPSet, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		if sd.set.Scope == scope {
			out = append(out, copyIPSet(&sd.set))
		}
		sd.mu.RUnlock()
	}

	return out, nil
}
