package kafka

import (
	"context"
	"encoding/json"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
)

// putClusterPolicyRequest is the modeled PutClusterPolicy body.
type putClusterPolicyRequest struct {
	Policy         string `json:"policy"`
	CurrentVersion string `json:"currentVersion"`
}

// PutClusterPolicy stores (or replaces) a cluster's resource policy. When the
// request supplies CurrentVersion it must match the stored version (optimistic
// concurrency) or the call is a BadRequestException. The version is bumped on
// success and returned.
func (m *Mock) PutClusterPolicy(_ context.Context, clusterARN string, body json.RawMessage) (string, error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return "", err
	}

	var req putClusterPolicyRequest
	if len(body) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return "", badRequest("invalid PutClusterPolicy body: %v", err)
		}
	}

	if req.Policy == "" {
		return "", badRequest("policy is required")
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if req.CurrentVersion != "" && req.CurrentVersion != cd.policyVersion {
		return "", badRequest(
			"currentVersion %q does not match policy version %q", req.CurrentVersion, cd.policyVersion)
	}

	cd.policy = req.Policy
	cd.policyVersion = "KTP" + idgen.GenerateID("")

	return cd.policyVersion, nil
}

// GetClusterPolicy returns a cluster's stored policy and version. When no policy
// is set it returns an empty policy and version rather than an error, matching
// real MSK's empty-policy behavior.
func (m *Mock) GetClusterPolicy(
	_ context.Context, clusterARN string,
) (currentVersion, policy string, err error) {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return "", "", err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	return cd.policyVersion, cd.policy, nil
}

// DeleteClusterPolicy clears a cluster's resource policy.
func (m *Mock) DeleteClusterPolicy(_ context.Context, clusterARN string) error {
	cd, err := m.getCluster(clusterARN)
	if err != nil {
		return err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	cd.policy = ""
	cd.policyVersion = ""

	return nil
}
