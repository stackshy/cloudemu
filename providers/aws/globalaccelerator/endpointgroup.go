package globalaccelerator

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// CreateEndpointGroup provisions an endpoint group under a listener. The listener
// must exist (ListenerNotFoundException). EndpointGroupArn is minted once and
// stable; the health-check settings, traffic dial and endpoint list round-trip
// verbatim, with the real-service defaults applied to omitted members.
func (m *Mock) CreateEndpointGroup(
	_ context.Context, in *driver.CreateEndpointGroupInput,
) (*driver.EndpointGroup, error) {
	listener, ok := m.listeners.Get(in.ListenerArn)
	if !ok {
		return nil, listenerNotFound(in.ListenerArn)
	}

	if in.EndpointGroupRegion == "" {
		return nil, invalidArgument("EndpointGroupRegion is required")
	}

	arn := endpointGroupARN(in.ListenerArn, idgen.UUID())

	g := driver.EndpointGroup{
		EndpointGroupArn:           arn,
		ListenerArn:                in.ListenerArn,
		EndpointGroupRegion:        in.EndpointGroupRegion,
		EndpointDescriptions:       endpointDescriptionsFromConfig(in.EndpointConfigurations),
		TrafficDialPercentage:      defaultedFloat(in.TrafficDialPercentage, defaultTrafficDial),
		HealthCheckPort:            defaultedHealthCheckPort(in.HealthCheckPort, &listener),
		HealthCheckProtocol:        defaultedString(in.HealthCheckProtocol, defaultHealthCheckProtocol),
		HealthCheckPath:            in.HealthCheckPath,
		HealthCheckIntervalSeconds: defaultedInt32(in.HealthCheckIntervalSeconds, defaultHealthCheckInterval),
		ThresholdCount:             defaultedInt32(in.ThresholdCount, defaultThresholdCount),
		PortOverrides:              copyPortOverrides(in.PortOverrides),
	}

	m.endpointGroups.Set(arn, g)

	out := copyEndpointGroup(&g)

	return &out, nil
}

// endpointDescriptionsFromConfig converts input endpoint configurations into the
// stored endpoint descriptions, applying the default weight and client-IP
// preservation, and marking each endpoint's health state INITIAL.
func endpointDescriptionsFromConfig(cfgs []driver.EndpointConfiguration) []driver.EndpointDescription {
	if cfgs == nil {
		return nil
	}

	out := make([]driver.EndpointDescription, len(cfgs))

	for i := range cfgs {
		weight := cfgs[i].Weight
		if weight == nil {
			w := defaultEndpointWeight
			weight = &w
		}

		preserve := cfgs[i].ClientIPPreservationEnabled
		if preserve == nil {
			p := true
			preserve = &p
		}

		out[i] = driver.EndpointDescription{
			EndpointID:                  cfgs[i].EndpointID,
			Weight:                      weight,
			ClientIPPreservationEnabled: preserve,
			AttachmentArn:               cfgs[i].AttachmentArn,
			HealthState:                 endpointHealthInitial,
		}
	}

	return out
}

// defaultedHealthCheckPort returns the requested health-check port, or the
// listener's first port-range FromPort when the caller omits it, matching the
// real service's default.
func defaultedHealthCheckPort(req *int32, listener *driver.Listener) *int32 {
	if req != nil {
		v := *req

		return &v
	}

	if len(listener.PortRanges) > 0 {
		v := listener.PortRanges[0].FromPort

		return &v
	}

	return nil
}

func defaultedFloat(req *float64, def float64) *float64 {
	if req != nil {
		v := *req

		return &v
	}

	return &def
}

func defaultedInt32(req *int32, def int32) *int32 {
	if req != nil {
		v := *req

		return &v
	}

	return &def
}

func defaultedString(req, def string) string {
	if req != "" {
		return req
	}

	return def
}

// DescribeEndpointGroup returns the endpoint group by ARN, or an
// EndpointGroupNotFoundException.
func (m *Mock) DescribeEndpointGroup(_ context.Context, arn string) (*driver.EndpointGroup, error) {
	g, ok := m.endpointGroups.Get(arn)
	if !ok {
		return nil, endpointGroupNotFound(arn)
	}

	out := copyEndpointGroup(&g)

	return &out, nil
}

// UpdateEndpointGroup replaces the members present in the request. A non-nil
// EndpointConfigurations or PortOverrides replaces the whole list; the arn is
// stable.
func (m *Mock) UpdateEndpointGroup(
	_ context.Context, in *driver.UpdateEndpointGroupInput,
) (*driver.EndpointGroup, error) {
	g, ok := m.endpointGroups.Get(in.EndpointGroupArn)
	if !ok {
		return nil, endpointGroupNotFound(in.EndpointGroupArn)
	}

	if in.EndpointConfigurations != nil {
		g.EndpointDescriptions = endpointDescriptionsFromConfig(*in.EndpointConfigurations)
	}

	if in.TrafficDialPercentage != nil {
		g.TrafficDialPercentage = copyFloat64Ptr(in.TrafficDialPercentage)
	}

	if in.HealthCheckPort != nil {
		g.HealthCheckPort = copyInt32Ptr(in.HealthCheckPort)
	}

	if in.HealthCheckProtocol != nil {
		g.HealthCheckProtocol = *in.HealthCheckProtocol
	}

	if in.HealthCheckPath != nil {
		g.HealthCheckPath = *in.HealthCheckPath
	}

	if in.HealthCheckIntervalSeconds != nil {
		g.HealthCheckIntervalSeconds = copyInt32Ptr(in.HealthCheckIntervalSeconds)
	}

	if in.ThresholdCount != nil {
		g.ThresholdCount = copyInt32Ptr(in.ThresholdCount)
	}

	if in.PortOverrides != nil {
		g.PortOverrides = copyPortOverrides(*in.PortOverrides)
	}

	m.endpointGroups.Set(in.EndpointGroupArn, g)

	out := copyEndpointGroup(&g)

	return &out, nil
}

// DeleteEndpointGroup removes an endpoint group.
func (m *Mock) DeleteEndpointGroup(_ context.Context, arn string) error {
	if !m.endpointGroups.Has(arn) {
		return endpointGroupNotFound(arn)
	}

	m.endpointGroups.Delete(arn)

	return nil
}

// ListEndpointGroups returns a deterministic page of the endpoint groups under a
// listener, ordered by ARN. The listener must exist.
func (m *Mock) ListEndpointGroups(
	_ context.Context, listenerArn string, page driver.Page,
) ([]*driver.EndpointGroup, string, error) {
	if !m.listeners.Has(listenerArn) {
		return nil, "", listenerNotFound(listenerArn)
	}

	matched := filterValues(m.endpointGroups.SortedValues(), func(g *driver.EndpointGroup) bool {
		return g.ListenerArn == listenerArn
	})

	out, next := paginateCopies(matched, page, copyEndpointGroup)

	return out, next, nil
}
