package kinesisvideo

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// TagStream merges tags into a stream's tag set.
func (m *Mock) TagStream(_ context.Context, ref driver.StreamRef, tags map[string]string) error {
	name, err := m.resolveStreamName(ref)
	if err != nil {
		return err
	}

	if !m.mergeStreamTags(name, tags) {
		return notFound("stream", name)
	}

	return nil
}

// UntagStream removes the named tag keys from a stream.
func (m *Mock) UntagStream(_ context.Context, ref driver.StreamRef, tagKeys []string) error {
	name, err := m.resolveStreamName(ref)
	if err != nil {
		return err
	}

	if !m.removeStreamTags(name, tagKeys) {
		return notFound("stream", name)
	}

	return nil
}

// ListTagsForStream returns a copy of a stream's tags. Kinesis Video paginates
// tags with a NextToken; the emulator returns the full set in one page.
func (m *Mock) ListTagsForStream(
	_ context.Context, ref driver.StreamRef, _ string,
) (tags map[string]string, next string, err error) {
	name, err := m.resolveStreamName(ref)
	if err != nil {
		return nil, "", err
	}

	s, ok := m.streams.Get(name)
	if !ok {
		return nil, "", notFound("stream", name)
	}

	return orEmptyTags(s.Tags), "", nil
}

// TagResource merges tags onto a stream or signaling channel identified by ARN.
func (m *Mock) TagResource(_ context.Context, resourceARN string, tags map[string]string) error {
	name, resType, ok := nameFromARN(resourceARN)
	if !ok {
		return invalidResourceFormat(resourceARN)
	}

	if resType == resourceStream {
		if !m.mergeStreamTags(name, tags) {
			return notFound("stream", name)
		}

		return nil
	}

	if !m.mergeChannelTags(name, tags) {
		return notFound("signaling channel", name)
	}

	return nil
}

// UntagResource removes tag keys from a stream or signaling channel by ARN.
func (m *Mock) UntagResource(_ context.Context, resourceARN string, tagKeys []string) error {
	name, resType, ok := nameFromARN(resourceARN)
	if !ok {
		return invalidResourceFormat(resourceARN)
	}

	if resType == resourceStream {
		if !m.removeStreamTags(name, tagKeys) {
			return notFound("stream", name)
		}

		return nil
	}

	if !m.removeChannelTags(name, tagKeys) {
		return notFound("signaling channel", name)
	}

	return nil
}

// ListTagsForResource returns a copy of a stream's or channel's tags.
func (m *Mock) ListTagsForResource(
	_ context.Context, resourceARN, _ string,
) (tags map[string]string, next string, err error) {
	name, resType, ok := nameFromARN(resourceARN)
	if !ok {
		return nil, "", invalidResourceFormat(resourceARN)
	}

	if resType == resourceStream {
		s, found := m.streams.Get(name)
		if !found {
			return nil, "", notFound("stream", name)
		}

		return orEmptyTags(s.Tags), "", nil
	}

	c, found := m.channels.Get(name)
	if !found {
		return nil, "", notFound("signaling channel", name)
	}

	return orEmptyTags(c.Tags), "", nil
}

func (m *Mock) mergeStreamTags(name string, tags map[string]string) bool {
	return m.streams.Update(name, func(s driver.StreamInfo) driver.StreamInfo {
		s.Tags = mergeTags(s.Tags, tags)

		return s
	})
}

func (m *Mock) removeStreamTags(name string, keys []string) bool {
	return m.streams.Update(name, func(s driver.StreamInfo) driver.StreamInfo {
		s.Tags = removeTags(s.Tags, keys)

		return s
	})
}

func (m *Mock) mergeChannelTags(name string, tags map[string]string) bool {
	return m.channels.Update(name, func(c driver.ChannelInfo) driver.ChannelInfo {
		c.Tags = mergeTags(c.Tags, tags)

		return c
	})
}

func (m *Mock) removeChannelTags(name string, keys []string) bool {
	return m.channels.Update(name, func(c driver.ChannelInfo) driver.ChannelInfo {
		c.Tags = removeTags(c.Tags, keys)

		return c
	})
}

// mergeTags returns dst with the entries of src merged in, allocating a fresh
// map so stored state is never aliased through the caller's input.
func mergeTags(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}

	out := copyTags(dst)
	if out == nil {
		out = make(map[string]string, len(src))
	}

	for k, v := range src {
		out[k] = v
	}

	return out
}

// removeTags returns dst without the named keys.
func removeTags(dst map[string]string, keys []string) map[string]string {
	if len(dst) == 0 || len(keys) == 0 {
		return dst
	}

	out := copyTags(dst)
	for _, k := range keys {
		delete(out, k)
	}

	return out
}

// orEmptyTags returns a non-nil copy so a tagless resource reports {} rather
// than null.
func orEmptyTags(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}

	return copyTags(in)
}
