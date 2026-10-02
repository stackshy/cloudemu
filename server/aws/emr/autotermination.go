package emr

import (
	"context"
	"net/http"
)

// IdleTimeout limits in seconds, from the AutoTerminationPolicy API reference.
const (
	minIdleTimeout = 60
	maxIdleTimeout = 604800
)

// autoTerminationPolicy mirrors the SDK AutoTerminationPolicy.
type autoTerminationPolicy struct {
	IdleTimeout *int64 `json:"IdleTimeout,omitempty"`
}

type clusterIDInput struct {
	ClusterID *string `json:"ClusterId"`
}

type putAutoTerminationPolicyInput struct {
	ClusterID             *string                `json:"ClusterId"`
	AutoTerminationPolicy *autoTerminationPolicy `json:"AutoTerminationPolicy"`
}

type getAutoTerminationPolicyOutput struct {
	AutoTerminationPolicy *autoTerminationPolicy `json:"AutoTerminationPolicy,omitempty"`
}

// validateAutoTermination checks the idle timeout range. A nil policy or a
// nil timeout is allowed.
func validateAutoTermination(p *autoTerminationPolicy) error {
	if p == nil || p.IdleTimeout == nil {
		return nil
	}

	if t := *p.IdleTimeout; t < minIdleTimeout || t > maxIdleTimeout {
		return validationErrorf("IdleTimeout must be between %d and %d seconds, got %d.",
			minIdleTimeout, maxIdleTimeout, t)
	}

	return nil
}

// setAutoTermination stores (or with nil, removes) a cluster's policy.
func (s *store) setAutoTermination(clusterID string, p *autoTerminationPolicy) error {
	if err := validateAutoTermination(p); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return notFound(clusterID)
	}

	c.idleTimeout = nil

	if p != nil && p.IdleTimeout != nil {
		t := *p.IdleTimeout
		c.idleTimeout = &t
	}

	return nil
}

// getAutoTermination returns a cluster's policy. IdleTimeout is nil when the
// cluster has none.
func (s *store) getAutoTermination(clusterID string) (autoTerminationPolicy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.clusters[clusterID]
	if !ok {
		return autoTerminationPolicy{}, notFound(clusterID)
	}

	if c.idleTimeout == nil {
		return autoTerminationPolicy{}, nil
	}

	t := *c.idleTimeout

	return autoTerminationPolicy{IdleTimeout: &t}, nil
}

func (h *Handler) getAutoTerminationPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *clusterIDInput) (any, error) {
		p, err := h.store.getAutoTermination(deref(in.ClusterID))
		if err != nil {
			return nil, err
		}

		if p.IdleTimeout == nil {
			return getAutoTerminationPolicyOutput{}, nil
		}

		return getAutoTerminationPolicyOutput{AutoTerminationPolicy: &p}, nil
	})
}

func (h *Handler) putAutoTerminationPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *putAutoTerminationPolicyInput) (any, error) {
		if in.AutoTerminationPolicy == nil {
			return nil, validationErrorf("1 validation error detected: Value null at 'autoTerminationPolicy' " +
				"failed to satisfy constraint: Member must not be null")
		}

		return struct{}{}, h.store.setAutoTermination(deref(in.ClusterID), in.AutoTerminationPolicy)
	})
}

func (h *Handler) removeAutoTerminationPolicy(w http.ResponseWriter, r *http.Request) {
	dispatch(h, w, r, func(h *Handler, _ context.Context, in *clusterIDInput) (any, error) {
		return struct{}{}, h.store.setAutoTermination(deref(in.ClusterID), nil)
	})
}
