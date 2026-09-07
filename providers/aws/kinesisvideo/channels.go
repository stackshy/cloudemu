package kinesisvideo

import (
	"context"
	"strings"

	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

// defaultMessageTTLSeconds is the SINGLE_MASTER message TTL assigned when the
// caller supplies none, matching real Kinesis Video's default of 60 seconds.
const defaultMessageTTLSeconds = 60

// CreateSignalingChannel provisions a new signaling channel directly in the
// ACTIVE state with stable computed fields (ChannelARN embedding the creation
// Unix timestamp, Version, ChannelStatus, CreationTime).
func (m *Mock) CreateSignalingChannel(_ context.Context, in *driver.CreateChannelInput) (*driver.ChannelInfo, error) {
	if in.ChannelName == "" {
		return nil, invalid("ChannelName is required")
	}

	channelType := in.ChannelType
	if channelType == "" {
		channelType = driver.ChannelTypeSingleMaster
	}

	if channelType != driver.ChannelTypeSingleMaster && channelType != driver.ChannelTypeFullMesh {
		return nil, invalid("ChannelType %s is not valid", channelType)
	}

	if m.channels.Has(in.ChannelName) {
		return nil, inUse("signaling channel", in.ChannelName)
	}

	now := m.now()

	c := driver.ChannelInfo{
		ChannelName:       in.ChannelName,
		ChannelARN:        m.channelARN(in.ChannelName, now),
		ChannelType:       channelType,
		ChannelStatus:     driver.StatusActive,
		MessageTTLSeconds: messageTTL(in.SingleMasterConfiguration),
		Version:           newVersion(),
		CreationTime:      now,
		Tags:              tagMapFromList(in.Tags),
	}

	m.channels.Set(in.ChannelName, c)

	out := copyChannel(&c)

	return &out, nil
}

// DescribeSignalingChannel returns a copy of the channel.
func (m *Mock) DescribeSignalingChannel(_ context.Context, ref driver.ChannelRef) (*driver.ChannelInfo, error) {
	name, err := m.resolveChannelName(ref)
	if err != nil {
		return nil, err
	}

	c, ok := m.channels.Get(name)
	if !ok {
		return nil, notFound("signaling channel", name)
	}

	out := copyChannel(&c)

	return &out, nil
}

// UpdateSignalingChannel applies a new message TTL and rotates the Version
// token. The computed ChannelARN, ChannelStatus and CreationTime are preserved.
func (m *Mock) UpdateSignalingChannel(_ context.Context, in *driver.UpdateChannelInput) error {
	name, resType, ok := nameFromARN(in.ChannelARN)
	if !ok || resType != resourceChannel {
		return invalidResourceFormat(in.ChannelARN)
	}

	var verErr error

	updated := m.channels.Update(name, func(c driver.ChannelInfo) driver.ChannelInfo {
		if in.CurrentVersion != "" && in.CurrentVersion != c.Version {
			verErr = invalid("current version %s does not match channel version", in.CurrentVersion)

			return c
		}

		if in.SingleMasterConfiguration != nil && in.SingleMasterConfiguration.MessageTTLSeconds != nil {
			c.MessageTTLSeconds = *in.SingleMasterConfiguration.MessageTTLSeconds
		}

		c.Version = newVersion()

		return c
	})
	if !updated {
		return notFound("signaling channel", name)
	}

	return verErr
}

// DeleteSignalingChannel removes a channel, enforcing the optional
// current-version guard.
func (m *Mock) DeleteSignalingChannel(_ context.Context, arn, currentVersion string) error {
	name, resType, ok := nameFromARN(arn)
	if !ok || resType != resourceChannel {
		return invalidResourceFormat(arn)
	}

	c, ok := m.channels.Get(name)
	if !ok {
		return notFound("signaling channel", name)
	}

	if currentVersion != "" && currentVersion != c.Version {
		return invalid("current version %s does not match channel version", currentVersion)
	}

	m.channels.Delete(name)

	return nil
}

// ListSignalingChannels returns a deterministic page of channels ordered by
// name, applying an optional BEGINS_WITH name filter.
//
//nolint:dupl // parallel to ListStreams by design; operates on the channel store and type.
func (m *Mock) ListSignalingChannels(_ context.Context, page driver.Page) (channels []driver.ChannelInfo, nextToken string, err error) {
	stored := m.channels.SortedValues()

	filtered := stored[:0:0]

	for i := range stored {
		if page.NameBeginsWith == "" || strings.HasPrefix(stored[i].ChannelName, page.NameBeginsWith) {
			filtered = append(filtered, stored[i])
		}
	}

	start, end, next := paginate(len(filtered), page)

	out := make([]driver.ChannelInfo, 0, end-start)
	for i := start; i < end; i++ {
		out = append(out, copyChannel(&filtered[i]))
	}

	return out, next, nil
}

// resolveChannelName resolves a ChannelRef to a channel name, parsing the name
// out of a ChannelARN when the name was not supplied directly.
func (*Mock) resolveChannelName(ref driver.ChannelRef) (string, error) {
	if ref.ChannelName != "" {
		return ref.ChannelName, nil
	}

	if ref.ChannelARN == "" {
		return "", invalid("ChannelName or ChannelARN is required")
	}

	name, resType, ok := nameFromARN(ref.ChannelARN)
	if !ok || resType != resourceChannel {
		return "", invalidResourceFormat(ref.ChannelARN)
	}

	return name, nil
}

// messageTTL returns the configured message TTL or the default when unset.
func messageTTL(cfg *driver.SingleMasterConfiguration) int32 {
	if cfg != nil && cfg.MessageTTLSeconds != nil {
		return *cfg.MessageTTLSeconds
	}

	return defaultMessageTTLSeconds
}
