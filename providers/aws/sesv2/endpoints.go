package sesv2

import (
	"context"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// CreateMultiRegionEndpoint registers a multi-region endpoint (ready at once).
func (m *Mock) CreateMultiRegionEndpoint(
	_ context.Context, in driver.MultiRegionEndpointInput,
) (*driver.MultiRegionEndpoint, error) {
	if in.EndpointName == "" {
		return nil, cerrors.New(cerrors.InvalidArgument, "EndpointName is required")
	}

	ep := &driver.MultiRegionEndpoint{
		EndpointName: in.EndpointName,
		EndpointID:   idgen.GenerateID(""),
		Status:       driver.EndpointStatusReady,
		Regions:      append([]string(nil), in.Regions...),
		CreatedAt:    m.now(),
	}

	if !m.endpoints.SetIfAbsent(in.EndpointName, ep) {
		return nil, cerrors.Newf(cerrors.AlreadyExists, "multi-region endpoint %q already exists", in.EndpointName)
	}

	out := *ep

	return &out, nil
}

// GetMultiRegionEndpoint returns an endpoint by name.
func (m *Mock) GetMultiRegionEndpoint(_ context.Context, name string) (*driver.MultiRegionEndpoint, error) {
	ep, ok := m.endpoints.Get(name)
	if !ok {
		return nil, errEndpointNotFound(name)
	}

	out := *ep
	out.Regions = append([]string(nil), ep.Regions...)

	return &out, nil
}

// DeleteMultiRegionEndpoint removes an endpoint and returns its final status.
func (m *Mock) DeleteMultiRegionEndpoint(_ context.Context, name string) (string, error) {
	if !m.endpoints.Delete(name) {
		return "", errEndpointNotFound(name)
	}

	return "DELETED", nil
}

// ListMultiRegionEndpoints returns all endpoints ordered by name.
func (m *Mock) ListMultiRegionEndpoints(_ context.Context) ([]driver.MultiRegionEndpoint, error) {
	all := m.endpoints.SortedValues()
	out := make([]driver.MultiRegionEndpoint, 0, len(all))

	for _, ep := range all {
		e := *ep
		e.Regions = append([]string(nil), ep.Regions...)
		out = append(out, e)
	}

	return out, nil
}

func errEndpointNotFound(name string) error {
	return cerrors.Newf(cerrors.NotFound, "multi-region endpoint %q does not exist", name)
}
