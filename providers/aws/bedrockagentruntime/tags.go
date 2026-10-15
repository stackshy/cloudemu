package bedrockagentruntime

import (
	"context"
	"maps"
	"regexp"

	"github.com/stackshy/cloudemu/v2/errors"
)

// Tag constraints from the service model.
const (
	maxTagKeyLen    = 128
	maxTagValueLen  = 256
	taggablePattern = `^arn:aws(-[^:]+)?:bedrock:[a-zA-Z0-9-]+:[0-9]{12}:(session)/` + uuidPattern + `$`
)

var taggableRE = regexp.MustCompile(taggablePattern)

// TagResource adds tags to a session.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	if err := validateTagging(resourceARN, len(tags) == 0); err != nil {
		return err
	}

	if err := validateTagValues(tags); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.findSession(resourceARN); err != nil {
		return err
	}

	merged := maps.Clone(m.tagsOf(resourceARN))
	maps.Copy(merged, tags)

	if len(merged) > maxSessionTags {
		return errors.Newf(errors.ResourceExhausted, "a session can have at most %d tags", maxSessionTags)
	}

	m.sessionTags.Set(resourceARN, merged)

	return nil
}

// UntagResource removes tag keys from a session.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	if err := validateTagging(resourceARN, len(tagKeys) == 0); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.findSession(resourceARN); err != nil {
		return err
	}

	remaining := maps.Clone(m.tagsOf(resourceARN))
	for _, k := range tagKeys {
		delete(remaining, k)
	}

	m.sessionTags.Set(resourceARN, remaining)

	return nil
}

// ListTagsForResource returns a session's tags.
func (m *Mock) ListTagsForResource(_ context.Context, resourceARN string) (map[string]string, error) {
	if err := validateTagging(resourceARN, false); err != nil {
		return nil, err
	}

	if _, err := m.findSession(resourceARN); err != nil {
		return nil, err
	}

	return maps.Clone(m.tagsOf(resourceARN)), nil
}

func (m *Mock) tagsOf(resourceARN string) map[string]string {
	t, _ := m.sessionTags.Get(resourceARN)
	if t == nil {
		return map[string]string{}
	}

	return t
}

func validateTagging(resourceARN string, missing bool) error {
	var v violations

	v.required("resourceArn", resourceARN == "")
	v.pattern("resourceArn", resourceARN, taggableRE, taggablePattern)
	v.required("tags", missing)

	return v.err()
}

func validateTagValues(tags map[string]string) error {
	var v violations

	if len(tags) > maxSessionTags {
		v.addf("Value at 'tags' failed to satisfy constraint: Member must have length less than or equal to %d", maxSessionTags)
	}

	for _, k := range sortedKeys(tags) {
		if k == "" || len(k) > maxTagKeyLen {
			v.addf("Value '%s' at 'tags' failed to satisfy constraint: Map keys must have length between 1 and %d", k, maxTagKeyLen)
		}

		v.maxLen("tags."+k, tags[k], maxTagValueLen)
	}

	return v.err()
}
