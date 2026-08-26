package compute

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Spot request states, as the portable driver carries them.
const (
	spotActive   = "active"
	spotCanceled = "canceled"
	// spotOneTime is the only request type OCI offers: a preemptible instance
	// is terminated on reclaim rather than re-requested.
	spotOneTime = "one-time"
)

// RequestSpotInstances launches preemptible instances, OCI's spot equivalent.
// OCI has no standing spot request resource — preemption is a property of the
// launch — so the request recorded here is CloudEmu's handle on the launch and
// is already fulfilled when it comes back.
//
//nolint:gocritic // hugeParam: the driver interface fixes the signature.
func (m *Mock) RequestSpotInstances(
	ctx context.Context, cfg driver.SpotRequestConfig,
) ([]driver.SpotInstanceRequest, error) {
	count := cfg.Count
	if count == 0 {
		count = 1
	}

	launch := cfg.InstanceConfig
	launch.Priority = prioritySpot

	instances, err := m.RunInstances(ctx, launch, count)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]driver.SpotInstanceRequest, 0, len(instances))

	for i := range instances {
		id := m.newOCID(typeInstance) + "-preemption"
		req := &driver.SpotInstanceRequest{
			ID:             id,
			InstanceConfig: launch,
			MaxPrice:       cfg.MaxPrice,
			Status:         spotActive,
			InstanceID:     instances[i].ID,
			CreatedAt:      m.now(),
			Type:           requestType(cfg.Type),
		}

		m.spot.Set(id, req)
		m.record(id)

		out = append(out, *req)
	}

	return out, nil
}

// requestType defaults to the only kind OCI offers.
func requestType(t string) string {
	if t == "" || t == spotOneTime {
		return spotOneTime
	}

	return t
}

// CancelSpotRequests terminates the preemptible instances a request launched.
func (m *Mock) CancelSpotRequests(ctx context.Context, requestIDs []string) error {
	for _, id := range requestIDs {
		instanceID, err := m.spotInstance(id)
		if err != nil {
			return err
		}

		if instanceID != "" {
			if err := m.TerminateInstance(ctx, instanceID, false); err != nil {
				return err
			}
		}

		m.closeSpotRequest(id)
	}

	return nil
}

func (m *Mock) spotInstance(id string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	req, ok := m.spot.Get(id)
	if !ok {
		return "", cerrors.Newf(cerrors.NotFound, "preemptible instance request %q not found", id)
	}

	if !m.instances.Has(req.InstanceID) {
		return "", nil
	}

	return req.InstanceID, nil
}

func (m *Mock) closeSpotRequest(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.spot.Update(id, func(r *driver.SpotInstanceRequest) *driver.SpotInstanceRequest {
		r.Status = spotCanceled

		return r
	})
}

// DescribeSpotRequests returns preemptible instance requests matching the
// given ids, or all if empty.
func (m *Mock) DescribeSpotRequests(_ context.Context, requestIDs []string) ([]driver.SpotInstanceRequest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return describeResources(m.spot, requestIDs,
		func(r *driver.SpotInstanceRequest) driver.SpotInstanceRequest { return *r }), nil
}
