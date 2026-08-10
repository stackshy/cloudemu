package glue

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/stackshy/cloudemu/v2/services/glue/driver"
)

// policyHash returns a stable content hash for a resource policy document.
func policyHash(policy string) string {
	sum := sha256.Sum256([]byte(policy))

	return hex.EncodeToString(sum[:])
}

// TagResource adds or overwrites tags on a resource, enforcing Glue's per-
// resource tag cap. The cap is checked against the merged set before any
// mutation so a breach leaves existing tags unchanged.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	if resourceARN == "" {
		return invalidInput("resource ARN must not be empty")
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	existing := m.tags[resourceARN]
	merged := len(existing)

	for k := range tags {
		if _, ok := existing[k]; !ok {
			merged++
		}
	}

	if merged > maxTags {
		return resourceNumberLimit("a resource may have at most %d tags", maxTags)
	}

	if existing == nil {
		existing = map[string]string{}
		m.tags[resourceARN] = existing
	}

	for k, v := range tags {
		existing[k] = v
	}

	return nil
}

// UntagResource removes tags by key.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, keys []string) error {
	if resourceARN == "" {
		return invalidInput("resource ARN must not be empty")
	}

	m.tagsMu.Lock()
	defer m.tagsMu.Unlock()

	for _, k := range keys {
		delete(m.tags[resourceARN], k)
	}

	return nil
}

// GetTags returns a copy of a resource's tags.
func (m *Mock) GetTags(_ context.Context, resourceARN string) (map[string]string, error) {
	if resourceARN == "" {
		return nil, invalidInput("resource ARN must not be empty")
	}

	m.tagsMu.RLock()
	defer m.tagsMu.RUnlock()

	return copyTags(m.tags[resourceARN]), nil
}

// PutResourcePolicy stores a resource policy for an ARN (or the account-level
// catalog when the ARN is empty), honoring the conditional-put preconditions and
// returning the new policy's content hash (sha256).
func (m *Mock) PutResourcePolicy(_ context.Context, arn, policy string, cond driver.PolicyCondition) (string, error) {
	if policy == "" {
		return "", invalidInput("policy document must not be empty")
	}

	key := arn
	if key == "" {
		key = m.arn("catalog")
	}

	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	current, exists := m.policies[key]

	switch cond.PolicyExistsCondition {
	case driver.PolicyMustExist:
		if !exists {
			return "", conditionCheckFailure("no existing policy for %s", key)
		}
	case driver.PolicyNotExist:
		if exists {
			return "", conditionCheckFailure("a policy already exists for %s", key)
		}
	}

	// A hash condition must match the current policy's hash (VersionMismatch on
	// mismatch, matching Glue).
	if cond.PolicyHashCondition != "" {
		if !exists || cond.PolicyHashCondition != policyHash(current) {
			return "", versionMismatch("policy hash condition does not match for %s", key)
		}
	}

	m.policies[key] = policy

	return policyHash(policy), nil
}

// GetResourcePolicy returns the stored policy for an ARN.
func (m *Mock) GetResourcePolicy(_ context.Context, arn string) (string, error) {
	key := arn
	if key == "" {
		key = m.arn("catalog")
	}

	m.policyMu.RLock()
	defer m.policyMu.RUnlock()

	policy, ok := m.policies[key]
	if !ok {
		return "", entityNotFound("no resource policy for %s", key)
	}

	return policy, nil
}

// DeleteResourcePolicy removes the stored policy for an ARN.
func (m *Mock) DeleteResourcePolicy(_ context.Context, arn string) error {
	key := arn
	if key == "" {
		key = m.arn("catalog")
	}

	m.policyMu.Lock()
	defer m.policyMu.Unlock()

	if _, ok := m.policies[key]; !ok {
		return entityNotFound("no resource policy for %s", key)
	}

	delete(m.policies, key)

	return nil
}

// PutDataCatalogEncryptionSettings stores catalog encryption settings.
func (m *Mock) PutDataCatalogEncryptionSettings(_ context.Context, catalogID string, settings map[string]any) error {
	cat := m.catalogOrDefault(catalogID)

	m.encMu.Lock()
	m.encSettings[cat] = copyAnyMap(settings)
	m.encMu.Unlock()

	return nil
}

// GetDataCatalogEncryptionSettings returns the stored catalog encryption
// settings, or an empty settings map when none have been set.
func (m *Mock) GetDataCatalogEncryptionSettings(_ context.Context, catalogID string) (map[string]any, error) {
	cat := m.catalogOrDefault(catalogID)

	m.encMu.RLock()
	defer m.encMu.RUnlock()

	if s, ok := m.encSettings[cat]; ok {
		return copyAnyMap(s), nil
	}

	return map[string]any{}, nil
}
