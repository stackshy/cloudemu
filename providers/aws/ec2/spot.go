package ec2

import (
	"context"

	"github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/internal/idgen"
)

const (
	spotStatusOpen     = "open"
	spotStatusActive   = "active"
	spotStatusCanceled = "canceled"
	spotTypeOneTime    = "one-time"
	timeFormat         = "2006-01-02T15:04:05Z"
)

// RequestSpotInstances creates spot instance requests and immediately fulfills them.
//
//nolint:gocritic // hugeParam: interface method signature cannot be changed.
func (m *Mock) RequestSpotInstances(
	ctx context.Context, cfg driver.SpotRequestConfig,
) ([]driver.SpotInstanceRequest, error) {
	if cfg.Count <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "count must be greater than 0")
	}

	if cfg.MaxPrice <= 0 {
		return nil, cerrors.New(cerrors.InvalidArgument, "max price must be greater than 0")
	}

	instances, err := m.RunInstances(ctx, cfg.InstanceConfig, cfg.Count)
	if err != nil {
		return nil, err
	}

	results := make([]driver.SpotInstanceRequest, 0, cfg.Count)
	now := m.opts.Clock.Now().UTC().Format(timeFormat)

	for _, inst := range instances {
		reqID := idgen.GenerateID("sir-")

		req := &driver.SpotInstanceRequest{
			ID:             reqID,
			InstanceConfig: cfg.InstanceConfig,
			MaxPrice:       cfg.MaxPrice,
			Status:         spotStatusActive,
			InstanceID:     inst.ID,
			CreatedAt:      now,
			Type:           cfg.Type,
		}

		m.spotRequests.Set(reqID, req)
		results = append(results, *req)
	}

	return results, nil
}

// CancelSpotRequests cancels spot requests and terminates instances for one-time requests.
func (m *Mock) CancelSpotRequests(ctx context.Context, requestIDs []string) error {
	for _, reqID := range requestIDs {
		req, ok := m.spotRequests.Get(reqID)
		if !ok {
			return cerrors.Newf(cerrors.NotFound, "spot request %q not found", reqID)
		}

		req.Status = spotStatusCanceled

		if req.Type == spotTypeOneTime && req.InstanceID != "" {
			if err := m.TerminateInstances(ctx, []string{req.InstanceID}); err != nil {
				return err
			}
		}
	}

	return nil
}

// DescribeSpotRequests returns spot requests matching the given IDs.
func (m *Mock) DescribeSpotRequests(
	_ context.Context, requestIDs []string,
) ([]driver.SpotInstanceRequest, error) {
	if len(requestIDs) == 0 {
		return m.allSpotRequests(), nil
	}

	results := make([]driver.SpotInstanceRequest, 0, len(requestIDs))

	for _, reqID := range requestIDs {
		req, ok := m.spotRequests.Get(reqID)
		if !ok {
			return nil, cerrors.Newf(cerrors.NotFound, "spot request %q not found", reqID)
		}

		results = append(results, *req)
	}

	return results, nil
}

func (m *Mock) allSpotRequests() []driver.SpotInstanceRequest {
	all := m.spotRequests.All()
	results := make([]driver.SpotInstanceRequest, 0, len(all))

	for _, req := range all {
		results = append(results, *req)
	}

	return results
}
