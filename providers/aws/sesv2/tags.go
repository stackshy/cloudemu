package sesv2

import (
	"context"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
)

// taggable is the common shape for a resource whose tags can be read/modified
// under a lock. Identities, configuration sets, and templates all satisfy it.
type taggable struct {
	get func() map[string]string
	set func(map[string]string)
	rw  interface {
		Lock()
		Unlock()
	}
}

// resolveTaggable maps an SES ARN to the tag accessors for the referenced
// resource. Supported resources: identity, configuration-set, template.
func (m *Mock) resolveTaggable(arn string) (*taggable, error) {
	kind, name, ok := parseSESARN(arn)
	if !ok {
		return nil, cerrors.Newf(cerrors.InvalidArgument, "%q is not a valid SES ARN", arn)
	}

	switch kind {
	case "identity":
		d, err := m.getIdentity(name)
		if err != nil {
			return nil, err
		}

		return &taggable{
			get: func() map[string]string { return d.id.Tags },
			set: func(t map[string]string) { d.id.Tags = t },
			rw:  &d.mu,
		}, nil
	case "configuration-set":
		d, ok := m.configSets.Get(name)
		if !ok {
			return nil, errConfigSetNotFound(name)
		}

		return &taggable{
			get: func() map[string]string { return d.cs.Tags },
			set: func(t map[string]string) { d.cs.Tags = t },
			rw:  &d.mu,
		}, nil
	case "template":
		d, err := m.getTemplate(name)
		if err != nil {
			return nil, err
		}

		return &taggable{
			get: func() map[string]string { return d.tpl.Tags },
			set: func(t map[string]string) { d.tpl.Tags = t },
			rw:  &d.mu,
		}, nil
	default:
		return nil, cerrors.Newf(cerrors.InvalidArgument, "unsupported SES resource %q", kind)
	}
}

// parseSESARN extracts the resource kind and name from an SES ARN of the shape
// arn:aws:ses:<region>:<account>:<kind>/<name>.
func parseSESARN(arn string) (kind, name string, ok bool) {
	const parts = 6

	seg := strings.SplitN(arn, ":", parts)
	if len(seg) != parts || seg[0] != "arn" || seg[2] != "ses" {
		return "", "", false
	}

	res := seg[5]

	slash := strings.Index(res, "/")
	if slash < 0 {
		return "", "", false
	}

	return res[:slash], res[slash+1:], true
}

// TagResource adds tags to the resource named by arn.
func (m *Mock) TagResource(_ context.Context, arn string, tags map[string]string) error {
	t, err := m.resolveTaggable(arn)
	if err != nil {
		return err
	}

	t.rw.Lock()
	defer t.rw.Unlock()

	merged, err := mergeTags(t.get(), tags)
	if err != nil {
		return err
	}

	t.set(merged)

	return nil
}

// UntagResource removes the given tag keys from the resource named by arn.
func (m *Mock) UntagResource(_ context.Context, arn string, tagKeys []string) error {
	t, err := m.resolveTaggable(arn)
	if err != nil {
		return err
	}

	t.rw.Lock()
	defer t.rw.Unlock()

	cur := t.get()
	for _, k := range tagKeys {
		delete(cur, k)
	}

	t.set(cur)

	return nil
}

// ListTagsForResource returns the tags on the resource named by arn.
func (m *Mock) ListTagsForResource(_ context.Context, arn string) (map[string]string, error) {
	t, err := m.resolveTaggable(arn)
	if err != nil {
		return nil, err
	}

	t.rw.Lock()
	defer t.rw.Unlock()

	return copyTags(t.get()), nil
}
