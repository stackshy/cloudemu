package globalaccelerator

import (
	"context"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// CreateListener provisions a listener under an accelerator. The accelerator must
// exist (AcceleratorNotFoundException). ListenerArn is minted once and stable;
// PortRanges, Protocol and ClientAffinity round-trip verbatim.
func (m *Mock) CreateListener(_ context.Context, in *driver.CreateListenerInput) (*driver.Listener, error) {
	if !m.accelerators.Has(in.AcceleratorArn) {
		return nil, acceleratorNotFound(in.AcceleratorArn)
	}

	if len(in.PortRanges) == 0 {
		return nil, invalidArgument("at least one port range is required")
	}

	if in.Protocol == "" {
		return nil, invalidArgument("Protocol is required")
	}

	affinity := in.ClientAffinity
	if affinity == "" {
		affinity = defaultClientAffinity
	}

	arn := listenerARN(in.AcceleratorArn, idgen.UUID())

	l := driver.Listener{
		ListenerArn:    arn,
		AcceleratorArn: in.AcceleratorArn,
		PortRanges:     copyPortRanges(in.PortRanges),
		Protocol:       in.Protocol,
		ClientAffinity: affinity,
	}

	m.listeners.Set(arn, l)

	out := copyListener(&l)

	return &out, nil
}

// DescribeListener returns the listener by ARN, or a ListenerNotFoundException.
func (m *Mock) DescribeListener(_ context.Context, arn string) (*driver.Listener, error) {
	l, ok := m.listeners.Get(arn)
	if !ok {
		return nil, listenerNotFound(arn)
	}

	out := copyListener(&l)

	return &out, nil
}

// UpdateListener replaces the members present in the request. The arn is stable.
func (m *Mock) UpdateListener(_ context.Context, in *driver.UpdateListenerInput) (*driver.Listener, error) {
	l, ok := m.listeners.Get(in.ListenerArn)
	if !ok {
		return nil, listenerNotFound(in.ListenerArn)
	}

	if in.PortRanges != nil {
		l.PortRanges = copyPortRanges(in.PortRanges)
	}

	if in.Protocol != nil {
		l.Protocol = *in.Protocol
	}

	if in.ClientAffinity != nil {
		l.ClientAffinity = *in.ClientAffinity
	}

	m.listeners.Set(in.ListenerArn, l)

	out := copyListener(&l)

	return &out, nil
}

// DeleteListener removes a listener. It is rejected while the listener still owns
// endpoint groups (AssociatedEndpointGroupFoundException).
func (m *Mock) DeleteListener(_ context.Context, arn string) error {
	if !m.listeners.Has(arn) {
		return listenerNotFound(arn)
	}

	if m.listenerHasEndpointGroups(arn) {
		return associatedEndpointGroupFound(arn)
	}

	m.listeners.Delete(arn)

	return nil
}

// listenerHasEndpointGroups reports whether any endpoint group belongs to the
// listener.
func (m *Mock) listenerHasEndpointGroups(listenerArn string) bool {
	return anyValue(m.endpointGroups.SortedValues(), func(g *driver.EndpointGroup) bool {
		return g.ListenerArn == listenerArn
	})
}

// ListListeners returns a deterministic page of the listeners under an
// accelerator, ordered by ARN. The accelerator must exist.
func (m *Mock) ListListeners(
	_ context.Context, acceleratorArn string, page driver.Page,
) ([]*driver.Listener, string, error) {
	if !m.accelerators.Has(acceleratorArn) {
		return nil, "", acceleratorNotFound(acceleratorArn)
	}

	matched := filterValues(m.listeners.SortedValues(), func(l *driver.Listener) bool {
		return l.AcceleratorArn == acceleratorArn
	})

	out, next := paginateCopies(matched, page, copyListener)

	return out, next, nil
}
