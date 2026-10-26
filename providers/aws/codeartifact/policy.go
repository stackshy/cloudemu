package codeartifact

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

const policyRevisionBytes = 32

// newPolicyRevision mints an opaque revision token.
func newPolicyRevision() (string, error) {
	raw := make([]byte, policyRevisionBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("codeartifact: generate policy revision: %w", err)
	}

	return base64.StdEncoding.EncodeToString(raw), nil
}

// validatePolicyDocument checks that a policy document is within the size limit
// and is an IAM policy JSON document (an object with a Statement).
func validatePolicyDocument(doc string) error {
	if doc == "" || len(doc) > maxPolicyBytes {
		return validation("policyDocument must be 1-%d bytes", maxPolicyBytes)
	}

	var parsed struct {
		Statement json.RawMessage `json:"Statement"`
	}

	if err := json.Unmarshal([]byte(doc), &parsed); err != nil || len(parsed.Statement) == 0 {
		return validation("policyDocument must be a JSON policy document with a Statement")
	}

	return nil
}

// putPolicyLocked stores a policy in store under key. A supplied revision must
// equal the current one; the stored revision changes on every put. The caller
// holds mu.
func putPolicyLocked(store *memstore.Store[driver.ResourcePolicy], key, arn, resourceType, resourceID,
	revision, document string) (*driver.ResourcePolicy, error) {
	if revision != "" {
		cur, ok := store.Get(key)
		if !ok || cur.Revision != revision {
			return nil, conflict(resourceType, resourceID, "The policy revision %s is not the current revision", revision)
		}
	}

	next, err := newPolicyRevision()
	if err != nil {
		return nil, err
	}

	p := driver.ResourcePolicy{ResourceArn: arn, Revision: next, Document: document}
	store.Set(key, p)

	return &p, nil
}

// getPolicyLocked returns the policy under key, or a ResourceNotFound. The
// caller holds mu.
func getPolicyLocked(store *memstore.Store[driver.ResourcePolicy], key, resourceType, resourceID string) (*driver.ResourcePolicy, error) {
	p, ok := store.Get(key)
	if !ok {
		return nil, notFound(resourceType, resourceID, "No resource policy is set on %s", resourceID)
	}

	return &p, nil
}

// deletePolicyLocked removes the policy under key. A supplied revision must equal
// the current one. The caller holds mu.
func deletePolicyLocked(store *memstore.Store[driver.ResourcePolicy], key, resourceType, resourceID,
	revision string) (*driver.ResourcePolicy, error) {
	p, err := getPolicyLocked(store, key, resourceType, resourceID)
	if err != nil {
		return nil, err
	}

	if revision != "" && revision != p.Revision {
		return nil, conflict(resourceType, resourceID, "The policy revision %s is not the current revision", revision)
	}

	store.Delete(key)

	return p, nil
}

// PutDomainPermissionsPolicy sets the resource policy on a domain.
func (m *Mock) PutDomainPermissionsPolicy(_ context.Context, ref driver.DomainRef,
	revision, document string) (*driver.ResourcePolicy, error) {
	if err := validatePolicyRevision(revision); err != nil {
		return nil, err
	}

	if err := validatePolicyDocument(document); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	d, err := m.domainLocked(ref)
	if err != nil {
		return nil, err
	}

	return putPolicyLocked(m.domainPolicies, d.Name, d.Arn, driver.ResourceTypeDomain, d.Name, revision, document)
}

// GetDomainPermissionsPolicy returns the resource policy on a domain.
func (m *Mock) GetDomainPermissionsPolicy(_ context.Context, ref driver.DomainRef) (*driver.ResourcePolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	d, err := m.domainLocked(ref)
	if err != nil {
		return nil, err
	}

	return getPolicyLocked(m.domainPolicies, d.Name, driver.ResourceTypeDomain, d.Name)
}

// DeleteDomainPermissionsPolicy removes the resource policy from a domain.
func (m *Mock) DeleteDomainPermissionsPolicy(_ context.Context, ref driver.DomainRef, revision string) (*driver.ResourcePolicy, error) {
	if err := validatePolicyRevision(revision); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	d, err := m.domainLocked(ref)
	if err != nil {
		return nil, err
	}

	return deletePolicyLocked(m.domainPolicies, d.Name, driver.ResourceTypeDomain, d.Name, revision)
}

// PutRepositoryPermissionsPolicy sets the resource policy on a repository.
func (m *Mock) PutRepositoryPermissionsPolicy(_ context.Context, ref driver.RepositoryRef,
	revision, document string) (*driver.ResourcePolicy, error) {
	if err := validatePolicyRevision(revision); err != nil {
		return nil, err
	}

	if err := validatePolicyDocument(document); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, r, err := m.repositoryLocked(ref)
	if err != nil {
		return nil, err
	}

	return putPolicyLocked(m.repoPolicies, repoKey(r.DomainName, r.Name), r.Arn, driver.ResourceTypeRepository, r.Name, revision, document)
}

// GetRepositoryPermissionsPolicy returns the resource policy on a repository.
func (m *Mock) GetRepositoryPermissionsPolicy(_ context.Context, ref driver.RepositoryRef) (*driver.ResourcePolicy, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, r, err := m.repositoryLocked(ref)
	if err != nil {
		return nil, err
	}

	return getPolicyLocked(m.repoPolicies, repoKey(r.DomainName, r.Name), driver.ResourceTypeRepository, r.Name)
}

// DeleteRepositoryPermissionsPolicy removes the resource policy from a repository.
func (m *Mock) DeleteRepositoryPermissionsPolicy(_ context.Context, ref driver.RepositoryRef,
	revision string) (*driver.ResourcePolicy, error) {
	if err := validatePolicyRevision(revision); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	_, r, err := m.repositoryLocked(ref)
	if err != nil {
		return nil, err
	}

	return deletePolicyLocked(m.repoPolicies, repoKey(r.DomainName, r.Name), driver.ResourceTypeRepository, r.Name, revision)
}
