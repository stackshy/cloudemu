package kinesis

import (
	"time"

	kinesisdriver "github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// epochOrNil renders a time as Kinesis's epoch-seconds number, or nil when zero.
func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

// streamModeJSON is the {StreamMode} wire wrapper.
type streamModeJSON struct {
	StreamMode string `json:"StreamMode"`
}

type hashKeyRangeJSON struct {
	StartingHashKey string `json:"StartingHashKey"`
	EndingHashKey   string `json:"EndingHashKey"`
}

type seqRangeJSON struct {
	StartingSequenceNumber string `json:"StartingSequenceNumber"`
	EndingSequenceNumber   string `json:"EndingSequenceNumber,omitempty"`
}

type shardJSON struct {
	ShardID               string           `json:"ShardId"`
	ParentShardID         string           `json:"ParentShardId,omitempty"`
	AdjacentParentShardID string           `json:"AdjacentParentShardId,omitempty"`
	HashKeyRange          hashKeyRangeJSON `json:"HashKeyRange"`
	SequenceNumberRange   seqRangeJSON     `json:"SequenceNumberRange"`
}

func shardToWire(s *kinesisdriver.Shard) shardJSON {
	return shardJSON{
		ShardID:               s.ShardID,
		ParentShardID:         s.ParentShardID,
		AdjacentParentShardID: s.AdjacentParentShardID,
		HashKeyRange:          hashKeyRangeJSON{s.HashKeyRange.StartingHashKey, s.HashKeyRange.EndingHashKey},
		SequenceNumberRange: seqRangeJSON{
			StartingSequenceNumber: s.SequenceNumberRange.StartingSequenceNumber,
			EndingSequenceNumber:   s.SequenceNumberRange.EndingSequenceNumber,
		},
	}
}

func shardsToWire(shards []kinesisdriver.Shard) []shardJSON {
	out := make([]shardJSON, 0, len(shards))
	for i := range shards {
		out = append(out, shardToWire(&shards[i]))
	}

	return out
}

// enhancedMetricsJSON is one {ShardLevelMetrics} entry.
type enhancedMetricsJSON struct {
	ShardLevelMetrics []string `json:"ShardLevelMetrics"`
}

func monitoringToWire(metrics []string) []enhancedMetricsJSON {
	if len(metrics) == 0 {
		return []enhancedMetricsJSON{{ShardLevelMetrics: []string{}}}
	}

	return []enhancedMetricsJSON{{ShardLevelMetrics: metrics}}
}

type recordJSON struct {
	SequenceNumber              string   `json:"SequenceNumber"`
	ApproximateArrivalTimestamp *float64 `json:"ApproximateArrivalTimestamp,omitempty"`
	Data                        []byte   `json:"Data"`
	PartitionKey                string   `json:"PartitionKey"`
	EncryptionType              string   `json:"EncryptionType,omitempty"`
}

func recordsToWire(records []kinesisdriver.Record) []recordJSON {
	out := make([]recordJSON, 0, len(records))
	for i := range records {
		out = append(out, recordJSON{
			SequenceNumber:              records[i].SequenceNumber,
			ApproximateArrivalTimestamp: epochOrNil(records[i].ApproximateArrivalTimestamp),
			Data:                        records[i].Data,
			PartitionKey:                records[i].PartitionKey,
			EncryptionType:              records[i].EncryptionType,
		})
	}

	return out
}

type childShardJSON struct {
	ShardID      string           `json:"ShardId"`
	ParentShards []string         `json:"ParentShards"`
	HashKeyRange hashKeyRangeJSON `json:"HashKeyRange"`
}

func childShardsToWire(cs []kinesisdriver.ChildShard) []childShardJSON {
	if len(cs) == 0 {
		return nil
	}

	out := make([]childShardJSON, 0, len(cs))
	for i := range cs {
		out = append(out, childShardJSON{
			ShardID:      cs[i].ShardID,
			ParentShards: cs[i].ParentShards,
			HashKeyRange: hashKeyRangeJSON{cs[i].HashKeyRange.StartingHashKey, cs[i].HashKeyRange.EndingHashKey},
		})
	}

	return out
}

type consumerJSON struct {
	ConsumerName              string   `json:"ConsumerName"`
	ConsumerARN               string   `json:"ConsumerARN"`
	ConsumerStatus            string   `json:"ConsumerStatus"`
	ConsumerCreationTimestamp *float64 `json:"ConsumerCreationTimestamp,omitempty"`
	StreamARN                 string   `json:"StreamARN,omitempty"`
}

func consumerToWire(c *kinesisdriver.Consumer) consumerJSON {
	return consumerJSON{
		ConsumerName:              c.ConsumerName,
		ConsumerARN:               c.ConsumerARN,
		ConsumerStatus:            c.ConsumerStatus,
		ConsumerCreationTimestamp: epochOrNil(c.ConsumerCreationTimestamp),
		StreamARN:                 c.StreamARN,
	}
}

// tagJSON is the {Key,Value} tag shape used by ListTagsForStream/Resource.
type tagJSON struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToWire(tags map[string]string) []tagJSON {
	out := make([]tagJSON, 0, len(tags))
	for k, v := range tags {
		out = append(out, tagJSON{Key: k, Value: v})
	}

	return out
}

// kinesisAccountSettings builds a driver AccountSettings from a commitment status.
func kinesisAccountSettings(status string) kinesisdriver.AccountSettings {
	return kinesisdriver.AccountSettings{CommitmentStatus: status}
}

// accountSettingsResponse wraps a commitment status in the wire shape.
func accountSettingsResponse(status string) map[string]any {
	if status == "" {
		return map[string]any{}
	}

	return map[string]any{
		"MinimumThroughputBillingCommitment": billingCommitmentJSON{Status: status},
	}
}
