package kinesis

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/snapshot"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

var _ snapshot.Snapshottable = (*Mock)(nil)

// kinesisSnapshot is the full serialized state of the Kinesis mock. Each stream
// carries an exported form (the stored streamData and its shards hold unexported
// fields and a mutex that json.Marshal cannot see); the arn→name index
// round-trips through the generic memstore helper so an ARN still resolves to
// its stream after a restore. The per-stream mutexes and the wired
// *config.Options are intentionally not serialized.
type kinesisSnapshot struct {
	Streams   map[string]*streamDataSnapshot `json:"streams,omitempty"`
	ArnToName json.RawMessage                `json:"arnToName,omitempty"`
	Settings  driver.AccountSettings         `json:"settings"`
}

// streamDataSnapshot mirrors streamData, promoting its unexported fields
// (including its shards and per-stream sequence counter) to exported ones.
type streamDataSnapshot struct {
	Desc      driver.StreamDescription    `json:"desc"`
	Shards    []*shardStateSnapshot       `json:"shards,omitempty"`
	Consumers map[string]*driver.Consumer `json:"consumers,omitempty"`
	Tags      map[string]string           `json:"tags,omitempty"`
	Policy    string                      `json:"policy,omitempty"`
	MaxRecKiB int32                       `json:"maxRecKiB,omitempty"`
	WarmMiBps int32                       `json:"warmMiBps,omitempty"`
	Seq       int64                       `json:"seq,omitempty"`
}

// shardStateSnapshot mirrors shardState, promoting its stored records and
// open/closed lifetime timestamps to exported fields.
type shardStateSnapshot struct {
	Shard     driver.Shard    `json:"shard"`
	Records   []driver.Record `json:"records,omitempty"`
	Closed    bool            `json:"closed,omitempty"`
	CreatedAt time.Time       `json:"createdAt,omitempty"`
	ClosedAt  time.Time       `json:"closedAt,omitempty"`
}

// Snapshot captures the mock's entire state as JSON. includeAssets is unused —
// stream records are the service's state, not bulk sidecar bodies, and are
// always captured.
func (m *Mock) Snapshot(_ context.Context, _ bool) (json.RawMessage, error) {
	snap := kinesisSnapshot{Streams: m.snapshotStreams()}

	arns, err := m.arnToName.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("kinesis: snapshot arn index: %w", err)
	}

	snap.ArnToName = arns

	m.settingsMu.RLock()
	snap.Settings = m.settings
	m.settingsMu.RUnlock()

	return json.Marshal(snap)
}

// snapshotStreams promotes each stored streamData to its exported form.
func (m *Mock) snapshotStreams() map[string]*streamDataSnapshot {
	if m.streams.Len() == 0 {
		return nil
	}

	out := make(map[string]*streamDataSnapshot, m.streams.Len())

	for name, sd := range m.streams.All() {
		sd.mu.RLock()
		out[name] = &streamDataSnapshot{
			Desc: sd.desc, Shards: snapshotShards(sd.shards), Consumers: sd.consumers,
			Tags: sd.tags, Policy: sd.policy, MaxRecKiB: sd.maxRecKiB,
			WarmMiBps: sd.warmMiBps, Seq: sd.seq,
		}
		sd.mu.RUnlock()
	}

	return out
}

func snapshotShards(shards []*shardState) []*shardStateSnapshot {
	if len(shards) == 0 {
		return nil
	}

	out := make([]*shardStateSnapshot, 0, len(shards))
	for _, s := range shards {
		out = append(out, &shardStateSnapshot{
			Shard: s.shard, Records: s.records, Closed: s.closed,
			CreatedAt: s.createdAt, ClosedAt: s.closedAt,
		})
	}

	return out
}

// Restore rebuilds the mock's state under the original identities: every stream
// (keyed by name), its shards, records, consumers, and per-stream sequence
// counter are reinstated, and the arn→name index is restored so ARN lookups
// resolve.
func (m *Mock) Restore(_ context.Context, data json.RawMessage) error {
	var snap kinesisSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("kinesis: parse snapshot: %w", err)
	}

	for name, s := range snap.Streams {
		m.streams.Set(name, restoreStream(s))
	}

	if len(snap.ArnToName) > 0 {
		if err := m.arnToName.LoadSnapshot(snap.ArnToName); err != nil {
			return fmt.Errorf("kinesis: restore arn index: %w", err)
		}
	}

	m.settingsMu.Lock()
	m.settings = snap.Settings
	m.settingsMu.Unlock()

	return nil
}

func restoreStream(s *streamDataSnapshot) *streamData {
	consumers := s.Consumers
	if consumers == nil {
		consumers = map[string]*driver.Consumer{}
	}

	tags := s.Tags
	if tags == nil {
		tags = map[string]string{}
	}

	return &streamData{
		desc: s.Desc, shards: restoreShards(s.Shards), consumers: consumers,
		tags: tags, policy: s.Policy, maxRecKiB: s.MaxRecKiB,
		warmMiBps: s.WarmMiBps, seq: s.Seq,
	}
}

func restoreShards(shards []*shardStateSnapshot) []*shardState {
	if len(shards) == 0 {
		return nil
	}

	out := make([]*shardState, 0, len(shards))
	for _, s := range shards {
		out = append(out, &shardState{
			shard: s.Shard, records: s.Records, closed: s.Closed,
			createdAt: s.CreatedAt, closedAt: s.ClosedAt,
		})
	}

	return out
}
