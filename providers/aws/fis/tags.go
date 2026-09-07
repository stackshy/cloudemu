package fis

import (
	"context"
)

// resolveResource returns the store and id for a FIS resource ARN, or an error
// if the ARN does not name a known template or experiment.
func (m *Mock) resolveResource(arn string) (kind, id string, err error) {
	kind, id = resourceRefFromARN(arn)

	switch kind {
	case kindTemplate:
		if !m.templates.Has(id) {
			return "", "", notFound("experiment template %s not found", id)
		}
	case kindExperiment:
		if !m.experiments.Has(id) {
			return "", "", notFound("experiment %s not found", id)
		}
	default:
		return "", "", validation("invalid FIS resource ARN: %s", arn)
	}

	return kind, id, nil
}

// TagResource merges tags onto a template or experiment.
func (m *Mock) TagResource(_ context.Context, resourceArn string, tags map[string]string) error {
	kind, id, err := m.resolveResource(resourceArn)
	if err != nil {
		return err
	}

	m.mutateTags(kind, id, func(existing map[string]string) map[string]string {
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

// UntagResource removes the given tag keys from a template or experiment.
func (m *Mock) UntagResource(_ context.Context, resourceArn string, tagKeys []string) error {
	kind, id, err := m.resolveResource(resourceArn)
	if err != nil {
		return err
	}

	m.mutateTags(kind, id, func(existing map[string]string) map[string]string {
		for _, k := range tagKeys {
			delete(existing, k)
		}

		return existing
	})

	return nil
}

// ListTagsForResource returns the tags of a template or experiment.
func (m *Mock) ListTagsForResource(_ context.Context, resourceArn string) (map[string]string, error) {
	kind, id, err := m.resolveResource(resourceArn)
	if err != nil {
		return nil, err
	}

	if kind == kindTemplate {
		t, _ := m.templates.Get(id)

		return copyTags(t.Tags), nil
	}

	e, _ := m.experiments.Get(id)

	return copyTags(e.Tags), nil
}

// mutateTags applies fn to the tag map of the identified resource and stores the
// result.
func (m *Mock) mutateTags(kind, id string, fn func(map[string]string) map[string]string) {
	if kind == kindTemplate {
		t, _ := m.templates.Get(id)
		t.Tags = fn(copyTags(t.Tags))
		m.templates.Set(id, t)

		return
	}

	e, _ := m.experiments.Get(id)
	e.Tags = fn(copyTags(e.Tags))
	m.experiments.Set(id, e)
}
