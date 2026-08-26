package wafv2

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/wafv2/driver"
)

func copyRegexSet(s *driver.RegexPatternSet) driver.RegexPatternSet {
	out := *s
	out.Tags = copyTags(s.Tags)
	out.RegularExpressionList = copyBytes(s.RegularExpressionList)

	return out
}

func (m *Mock) regexSetByName(scope, name string) bool {
	for _, sd := range m.regexes.All() {
		sd.mu.RLock()
		match := sd.set.Scope == scope && sd.set.Name == name
		sd.mu.RUnlock()

		if match {
			return true
		}
	}

	return false
}

// CreateRegexPatternSet creates a regex pattern set, storing its regexes verbatim.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) CreateRegexPatternSet(
	_ context.Context, in driver.CreateRegexPatternSetInput,
) (*driver.RegexPatternSet, error) {
	if in.Name == "" || in.Scope == "" {
		return nil, invalidParameter("Name and Scope are required")
	}

	if err := validateScope(in.Scope); err != nil {
		return nil, err
	}

	m.createMu.Lock()
	defer m.createMu.Unlock()

	if m.regexSetByName(in.Scope, in.Name) {
		return nil, duplicate("regex pattern set %q already exists in scope %s", in.Name, in.Scope)
	}

	id := idgen.GenerateID("")
	set := driver.RegexPatternSet{
		ID:                    id,
		Name:                  in.Name,
		ARN:                   m.arn(in.Scope, "regexpatternset", in.Name, id),
		Scope:                 in.Scope,
		Description:           in.Description,
		LockToken:             newLockToken(),
		RegularExpressionList: in.RegularExpressionList,
		Tags:                  copyTags(in.Tags),
	}

	m.regexes.Set(key(in.Scope, id), &regexSetData{set: set})

	out := copyRegexSet(&set)

	return &out, nil
}

func (m *Mock) getRegexSetData(ref driver.Ref) (*regexSetData, error) {
	sd, ok := m.regexes.Get(key(ref.Scope, ref.ID))
	if !ok {
		return nil, nonexistent("regex pattern set %q not found in scope %s", ref.ID, ref.Scope)
	}

	return sd, nil
}

// GetRegexPatternSet returns a regex pattern set by (scope,id).
func (m *Mock) GetRegexPatternSet(_ context.Context, ref driver.Ref) (*driver.RegexPatternSet, error) {
	sd, err := m.getRegexSetData(ref)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := copyRegexSet(&sd.set)

	return &out, nil
}

// UpdateRegexPatternSet replaces a regex set's regexes/description, enforcing the lock token.
//
//nolint:gocritic // in is the public input, taken by value to match the driver API.
func (m *Mock) UpdateRegexPatternSet(_ context.Context, in driver.UpdateRegexPatternSetInput) (string, error) {
	sd, err := m.getRegexSetData(driver.Ref{Scope: in.Scope, ID: in.ID})
	if err != nil {
		return "", err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.set.LockToken != in.LockToken {
		return "", staleLock("stale lock token for regex pattern set %q", in.ID)
	}

	sd.set.Description = in.Description
	sd.set.RegularExpressionList = in.RegularExpressionList
	sd.set.LockToken = newLockToken()

	return sd.set.LockToken, nil
}

// DeleteRegexPatternSet removes a regex pattern set, enforcing the lock token.
func (m *Mock) DeleteRegexPatternSet(_ context.Context, ref driver.Ref, lockToken string) error {
	sd, err := m.getRegexSetData(ref)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if sd.set.LockToken != lockToken {
		return staleLock("stale lock token for regex pattern set %q", ref.ID)
	}

	if m.itemReferencedByWebACL(sd.set.ARN) {
		return associated("regex pattern set %q is referenced by one or more web ACLs", ref.ID)
	}

	m.regexes.Delete(key(ref.Scope, ref.ID))

	return nil
}

// ListRegexPatternSets returns all regex pattern sets in a scope.
func (m *Mock) ListRegexPatternSets(_ context.Context, scope string) ([]driver.RegexPatternSet, error) {
	all := m.regexes.All()
	out := make([]driver.RegexPatternSet, 0, len(all))

	for _, sd := range all {
		sd.mu.RLock()
		if sd.set.Scope == scope {
			out = append(out, copyRegexSet(&sd.set))
		}
		sd.mu.RUnlock()
	}

	return out, nil
}
