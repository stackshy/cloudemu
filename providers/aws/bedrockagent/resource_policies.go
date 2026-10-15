package bedrockagent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/bedrockagent/driver"
)

// knowledgeBaseARNMarker is the resource segment of a knowledge-base ARN, the
// only kind a resource policy attaches to.
const knowledgeBaseARNMarker = ":knowledge-base/"

// PutResourcePolicy attaches or replaces a knowledge base's resource policy.
// The policy must be a JSON document; a non-empty expectedRevisionID must match
// the current revision.
func (m *Mock) PutResourcePolicy(_ context.Context, resourceARN, policy, expectedRevisionID string) (*driver.ResourcePolicy, error) {
	var v violations

	v.required("resourceArn", resourceARN == "")
	v.required("policy", policy == "")

	if err := v.err(); err != nil {
		return nil, err
	}

	if !json.Valid([]byte(policy)) {
		return nil, errors.New(errors.InvalidArgument, "the policy is not a valid JSON document")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkPolicyTarget(resourceARN); err != nil {
		return nil, err
	}

	current, _ := m.policies.Get(resourceARN)
	if err := checkRevision(current, expectedRevisionID); err != nil {
		return nil, err
	}

	revision, err := idgen.BedrockAgentResourceID()
	if err != nil {
		return nil, err
	}

	stored := &driver.ResourcePolicy{ResourceARN: resourceARN, Policy: policy, RevisionID: revision}
	m.policies.Set(resourceARN, stored)

	result := *stored

	return &result, nil
}

// GetResourcePolicy returns a knowledge base's resource policy.
func (m *Mock) GetResourcePolicy(_ context.Context, resourceARN string) (*driver.ResourcePolicy, error) {
	if err := m.checkPolicyTarget(resourceARN); err != nil {
		return nil, err
	}

	stored, ok := m.policies.Get(resourceARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "no resource policy is attached to %s", resourceARN)
	}

	result := *stored

	return &result, nil
}

// DeleteResourcePolicy removes a knowledge base's resource policy and returns
// the revision it removed.
func (m *Mock) DeleteResourcePolicy(_ context.Context, resourceARN, expectedRevisionID string) (*driver.ResourcePolicy, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.checkPolicyTarget(resourceARN); err != nil {
		return nil, err
	}

	stored, ok := m.policies.Get(resourceARN)
	if !ok {
		return nil, errors.Newf(errors.NotFound, "no resource policy is attached to %s", resourceARN)
	}

	if err := checkRevision(stored, expectedRevisionID); err != nil {
		return nil, err
	}

	m.policies.Delete(resourceARN)

	result := *stored

	return &result, nil
}

// checkPolicyTarget resolves a knowledge-base ARN to a stored knowledge base.
func (m *Mock) checkPolicyTarget(resourceARN string) error {
	_, id, ok := strings.Cut(resourceARN, knowledgeBaseARNMarker)
	if !ok {
		return errors.Newf(errors.InvalidArgument,
			"Resource policies can be attached only to knowledge bases, not %s.", resourceARN)
	}

	kb, found := m.knowledge.Get(id)
	if !found || kb.ARN != resourceARN {
		return errors.Newf(errors.NotFound, "knowledge base %q not found", resourceARN)
	}

	return nil
}

// checkRevision enforces an expected revision id against the current policy.
func checkRevision(current *driver.ResourcePolicy, expected string) error {
	if expected == "" {
		return nil
	}

	if current == nil || current.RevisionID != expected {
		return errors.Newf(errors.FailedPrecondition,
			"The expected revision %s does not match the current revision of the resource policy.", expected)
	}

	return nil
}
