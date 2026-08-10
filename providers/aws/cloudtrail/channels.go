package cloudtrail

import (
	"context"
	"sort"

	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/services/cloudtrail/driver"
)

// CreateChannel stores a channel and returns it. The name is claimed atomically.
func (m *Mock) CreateChannel(
	_ context.Context, name, source string, dests []driver.Destination, tags map[string]string,
) (*driver.Channel, error) {
	if name == "" {
		return nil, errInvalidParameter("Name is required")
	}

	if source == "" {
		return nil, errInvalidParameter("Source is required")
	}

	ch := driver.Channel{
		Name:         name,
		ARN:          m.channelARN(idgen.GenerateID("")),
		Source:       source,
		Destinations: copyDestinations(dests),
		CreatedAt:    m.now(),
		Tags:         copyTags(tags),
	}

	cd := &channelData{channel: ch, maxEventSize: maxEventSizeStd}

	// Claim the name atomically first (ARNs are unique, so name is the contended
	// key for a duplicate create).
	if !m.chanNameIdx.SetIfAbsent(name, ch.ARN) {
		return nil, errChannelExists(name)
	}

	m.channels.Set(ch.ARN, cd)
	m.storeResourceTags(ch.ARN, tags)

	out := cd.channel
	out.Destinations = copyDestinations(cd.channel.Destinations)

	return &out, nil
}

// GetChannel returns a channel by ARN.
func (m *Mock) GetChannel(_ context.Context, arn string) (*driver.Channel, error) {
	cd, err := m.resolveChannel(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.RLock()
	defer cd.mu.RUnlock()

	out := cd.channel
	out.Destinations = copyDestinations(cd.channel.Destinations)

	return &out, nil
}

// UpdateChannel applies a new name and/or destinations.
func (m *Mock) UpdateChannel(
	_ context.Context, arn, name string, dests []driver.Destination,
) (*driver.Channel, error) {
	cd, err := m.resolveChannel(arn)
	if err != nil {
		return nil, err
	}

	cd.mu.Lock()
	defer cd.mu.Unlock()

	if name != "" && name != cd.channel.Name {
		if !m.chanNameIdx.SetIfAbsent(name, arn) {
			return nil, errChannelExists(name)
		}

		m.chanNameIdx.Delete(cd.channel.Name)
		cd.channel.Name = name
	}

	if dests != nil {
		cd.channel.Destinations = copyDestinations(dests)
	}

	out := cd.channel
	out.Destinations = copyDestinations(cd.channel.Destinations)

	return &out, nil
}

// DeleteChannel removes a channel, its name-index entry, and its tags.
func (m *Mock) DeleteChannel(_ context.Context, arn string) error {
	cd, err := m.resolveChannel(arn)
	if err != nil {
		return err
	}

	cd.mu.RLock()
	name := cd.channel.Name
	cd.mu.RUnlock()

	m.channels.Delete(arn)
	m.chanNameIdx.Delete(name)
	m.deleteResourceTags(arn)

	return nil
}

// ListChannels returns all channels ordered by ARN, paginated.
func (m *Mock) ListChannels(
	_ context.Context, nextToken string, maxResults int32,
) ([]driver.Channel, string, error) {
	all := m.channels.All()

	arns := make([]string, 0, len(all))
	for arn := range all {
		arns = append(arns, arn)
	}

	sort.Strings(arns)

	limit := int(maxResults)
	if limit <= 0 {
		limit = defaultMaxResults
	}

	out := make([]driver.Channel, 0, len(arns))
	started := nextToken == ""

	for _, arn := range arns {
		if !started {
			if arn == nextToken {
				started = true
			}

			continue
		}

		if len(out) == limit {
			return out, out[len(out)-1].ARN, nil
		}

		cd := all[arn]
		cd.mu.RLock()
		ch := cd.channel
		ch.Destinations = copyDestinations(cd.channel.Destinations)
		cd.mu.RUnlock()

		out = append(out, ch)
	}

	return out, "", nil
}

// GetEventConfiguration returns a resource's max event size (channel or EDS).
func (m *Mock) GetEventConfiguration(
	_ context.Context, resourceARN string,
) (outARN, maxEventSize string, err error) {
	cd, chErr := m.resolveChannelIfChannel(resourceARN)
	if chErr != nil {
		return "", "", chErr
	}

	if cd != nil {
		cd.mu.RLock()
		defer cd.mu.RUnlock()

		return resourceARN, cd.maxEventSize, nil
	}

	ed, edErr := m.resolveEDS(resourceARN)
	if edErr != nil {
		return "", "", edErr
	}

	ed.mu.RLock()
	defer ed.mu.RUnlock()

	return resourceARN, ed.maxEventSize, nil
}

// PutEventConfiguration sets a resource's max event size (channel or EDS).
func (m *Mock) PutEventConfiguration(
	_ context.Context, resourceARN, maxEventSize string,
) (outARN, outMaxEventSize string, err error) {
	cd, chErr := m.resolveChannelIfChannel(resourceARN)
	if chErr != nil {
		return "", "", chErr
	}

	if cd != nil {
		cd.mu.Lock()
		defer cd.mu.Unlock()
		cd.maxEventSize = maxEventSize

		return resourceARN, cd.maxEventSize, nil
	}

	ed, edErr := m.resolveEDS(resourceARN)
	if edErr != nil {
		return "", "", edErr
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()
	ed.maxEventSize = maxEventSize

	return resourceARN, ed.maxEventSize, nil
}

// resolveChannelIfChannel returns the channel for arn when arn is a channel ARN,
// (nil, nil) when arn is not channel-shaped (so the caller can try EDS), or
// (nil, err) when arn is channel-shaped but unknown.
func (m *Mock) resolveChannelIfChannel(arn string) (*channelData, error) {
	if !validChannelARN(arn) {
		return nil, nil
	}

	cd, ok := m.channels.Get(arn)
	if !ok {
		return nil, errChannelNotFound(arn)
	}

	return cd, nil
}
