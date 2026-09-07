package kinesisvideo

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/kinesisvideo/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kinesisVideoSnapshot is the full serialized state of the Kinesis Video mock.
// The stores hold exported driver types, so they serialize directly, keyed by
// stream/channel name. The wired opts are not serialized.
type kinesisVideoSnapshot struct {
	Streams  map[string]driver.StreamInfo  `json:"streams,omitempty"`
	Channels map[string]driver.ChannelInfo `json:"channels,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// Kinesis Video is control-plane only and holds no bulk object bodies.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := kinesisVideoSnapshot{}

	if m.streams.Len() > 0 {
		snap.Streams = m.streams.All()
	}

	if m.channels.Len() > 0 {
		snap.Channels = m.channels.All()
	}

	b, err := json.Marshal(snap)
	if err != nil {
		return nil, fmt.Errorf("kinesisvideo: snapshot: %w", err)
	}

	return b, nil
}

// Restore rebuilds the mock's state under the original identities: every stream
// and channel name (and the ARN, version and creation time stored with it) is
// preserved, so a restore is transparent to clients.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kinesisVideoSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kinesisvideo: parse snapshot: %w", err)
	}

	for name := range snap.Streams {
		m.streams.Set(name, snap.Streams[name])
	}

	for name := range snap.Channels {
		m.channels.Set(name, snap.Channels[name])
	}

	return nil
}
