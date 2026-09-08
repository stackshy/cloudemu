package globalaccelerator

import (
	"context"
)

// resolveKind classifies a Global Accelerator resource ARN and confirms the
// resource exists, returning the segment count that identifies its store.
func (m *Mock) resolveKind(arn string) (segs int, err error) {
	switch classifyARN(arn) {
	case segsAccelerator:
		if !m.accelerators.Has(arn) {
			return 0, acceleratorNotFound(arn)
		}

		return segsAccelerator, nil
	case segsListener:
		if !m.listeners.Has(arn) {
			return 0, listenerNotFound(arn)
		}

		return segsListener, nil
	case segsEndpointGroup:
		if !m.endpointGroups.Has(arn) {
			return 0, endpointGroupNotFound(arn)
		}

		return segsEndpointGroup, nil
	default:
		return 0, invalidArgument("invalid Global Accelerator resource ARN: %s", arn)
	}
}

// TagResource merges tags onto an accelerator, listener or endpoint group.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	kind, err := m.resolveKind(resourceArn)
	if err != nil {
		return err
	}

	m.mutateTags(kind, resourceArn, func(existing map[string]string) map[string]string {
		if existing == nil {
			existing = map[string]string{}
		}

		for k, v := range tags {
			existing[k] = v
		}

		return existing
	})

	return nil
}

// UntagResource removes the given tag keys from a resource.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	kind, err := m.resolveKind(resourceArn)
	if err != nil {
		return err
	}

	m.mutateTags(kind, resourceArn, func(existing map[string]string) map[string]string {
		for _, k := range tagKeys {
			delete(existing, k)
		}

		return existing
	})

	return nil
}

// ListTagsForResource returns the tags of a resource.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	kind, err := m.resolveKind(resourceArn)
	if err != nil {
		return nil, err
	}

	switch kind {
	case segsAccelerator:
		a, _ := m.accelerators.Get(resourceArn)

		return copyTags(a.Tags), nil
	case segsListener:
		l, _ := m.listeners.Get(resourceArn)

		return copyTags(l.Tags), nil
	default:
		g, _ := m.endpointGroups.Get(resourceArn)

		return copyTags(g.Tags), nil
	}
}

// mutateTags applies fn to the tag map of the identified resource and stores the
// result.
func (m *Mock) mutateTags(kind int, arn string, fn func(map[string]string) map[string]string) {
	switch kind {
	case segsAccelerator:
		a, _ := m.accelerators.Get(arn)
		a.Tags = fn(copyTags(a.Tags))
		m.accelerators.Set(arn, a)
	case segsListener:
		l, _ := m.listeners.Get(arn)
		l.Tags = fn(copyTags(l.Tags))
		m.listeners.Set(arn, l)
	default:
		g, _ := m.endpointGroups.Get(arn)
		g.Tags = fn(copyTags(g.Tags))
		m.endpointGroups.Set(arn, g)
	}
}
