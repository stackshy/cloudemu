package configservice

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// PutDeliveryChannel creates or updates the account's delivery channel. Config
// allows one channel per account; a Put naming a different channel while one
// exists is a MaxNumberOfDeliveryChannelsExceededException. A Put naming the
// existing channel is an idempotent upsert.
//
//nolint:gocritic // ch is the driver DeliveryChannel input, taken by value to match the driver API.
func (m *Mock) PutDeliveryChannel(_ context.Context, ch driver.DeliveryChannel) error {
	if ch.Name == "" {
		ch.Name = defaultName
	}

	if ch.S3BucketName == "" {
		return tagged(driver.ExNoSuchBucket, invalidArgCode, "S3BucketName is required")
	}

	// Real Config requires a configuration recorder to exist before a delivery
	// channel can be created.
	if m.recorders.Len() == 0 {
		return noAvailableConfigurationRecorder()
	}

	now := m.now()

	// Hold createMu across scan+insert so the single-channel cap holds under
	// concurrent creates with DIFFERENT names. See Mock.createMu.
	m.createMu.Lock()
	defer m.createMu.Unlock()

	for _, k := range m.channels.Keys() {
		if k != ch.Name {
			return maxDeliveryChannelsExceeded()
		}
	}

	if existing, ok := m.channels.Get(ch.Name); ok {
		existing.mu.Lock()
		existing.ch.S3BucketName = ch.S3BucketName
		existing.ch.S3KeyPrefix = ch.S3KeyPrefix
		existing.ch.S3KmsKeyArn = ch.S3KmsKeyArn
		existing.ch.SnsTopicARN = ch.SnsTopicARN
		existing.ch.SnapshotDeliveryProps = ch.SnapshotDeliveryProps
		existing.ch.LastStatusChangeTime = now
		existing.mu.Unlock()

		return nil
	}

	ch.Arn = m.arn("delivery-channel/" + ch.Name)
	ch.LastStatus = "SUCCESS"
	ch.LastStatusChangeTime = now
	m.channels.Set(ch.Name, &channelData{ch: ch})

	return nil
}

func copyChannel(c *driver.DeliveryChannel) driver.DeliveryChannel {
	out := *c
	out.Tags = copyTags(c.Tags)

	if c.SnapshotDeliveryProps != nil {
		p := *c.SnapshotDeliveryProps
		out.SnapshotDeliveryProps = &p
	}

	return out
}

func (m *Mock) allChannels() []driver.DeliveryChannel {
	keys := sortedKeys(m.channels.Keys())
	out := make([]driver.DeliveryChannel, 0, len(keys))

	for _, k := range keys {
		cd, ok := m.channels.Get(k)
		if !ok {
			continue
		}

		cd.mu.RLock()
		out = append(out, copyChannel(&cd.ch))
		cd.mu.RUnlock()
	}

	return out
}

// DescribeDeliveryChannels returns the named channels (all if empty). A
// named-but-absent channel is a NoSuchDeliveryChannelException.
func (m *Mock) DescribeDeliveryChannels(_ context.Context, names []string) ([]driver.DeliveryChannel, error) {
	for _, n := range names {
		if !m.channels.Has(n) {
			return nil, noSuchDeliveryChannel(n)
		}
	}

	all := m.allChannels()

	return filterByNames(all, func(c driver.DeliveryChannel) string { return c.Name }, names), nil
}

// DescribeDeliveryChannelStatus returns the runtime status of the named channels.
func (m *Mock) DescribeDeliveryChannelStatus(_ context.Context, names []string) ([]driver.DeliveryChannel, error) {
	for _, n := range names {
		if !m.channels.Has(n) {
			return nil, noSuchDeliveryChannel(n)
		}
	}

	all := m.allChannels()

	return filterByNames(all, func(c driver.DeliveryChannel) string { return c.Name }, names), nil
}

// DeleteDeliveryChannel removes a channel. A channel in use by a running
// recorder cannot be deleted (LastDeliveryChannelDeleteFailedException).
func (m *Mock) DeleteDeliveryChannel(_ context.Context, name string) error {
	if !m.channels.Has(name) {
		return noSuchDeliveryChannel(name)
	}

	for _, k := range m.recorders.Keys() {
		rd, ok := m.recorders.Get(k)
		if !ok {
			continue
		}

		rd.mu.RLock()
		recording := rd.rec.Recording
		rd.mu.RUnlock()

		if recording {
			return tagged(driver.ExLastDeliveryChannelDeleteFailed, failedPreconditionCode,
				"delivery channel %q is in use by a running configuration recorder", name)
		}
	}

	m.channels.Delete(name)

	return nil
}

// DeliverConfigSnapshot triggers an on-demand snapshot delivery, returning a
// synthesized snapshot ID. Requires an existing channel.
func (m *Mock) DeliverConfigSnapshot(_ context.Context, channelName string) (string, error) {
	if !m.channels.Has(channelName) {
		return "", noSuchDeliveryChannel(channelName)
	}

	return "snapshot-" + m.now().UTC().Format("20060102T150405Z"), nil
}
