// Package driver defines the interface and types for AWS Kinesis Data Streams
// implementations. It models streams, shards with hash-key ranges, records with
// per-shard monotonic sequence numbers, shard iterators, enhanced fan-out
// consumers, encryption, tags, and resource policies.
package driver

import (
	"context"
	"time"
)

// Stream statuses.
const (
	StatusCreating = "CREATING"
	StatusDeleting = "DELETING"
	StatusActive   = "ACTIVE"
	StatusUpdating = "UPDATING"
)

// Stream modes.
const (
	ModeProvisioned = "PROVISIONED"
	ModeOnDemand    = "ON_DEMAND"
)

// Encryption types.
const (
	EncryptionNone = "NONE"
	EncryptionKMS  = "KMS"
)

// Shard iterator types.
const (
	IteratorAtSequenceNumber    = "AT_SEQUENCE_NUMBER"
	IteratorAfterSequenceNumber = "AFTER_SEQUENCE_NUMBER"
	IteratorTrimHorizon         = "TRIM_HORIZON"
	IteratorLatest              = "LATEST"
	IteratorAtTimestamp         = "AT_TIMESTAMP"
)

// Consumer statuses.
const (
	ConsumerCreating = "CREATING"
	ConsumerDeleting = "DELETING"
	ConsumerActive   = "ACTIVE"
)

// ShardFilter types narrow ListShards to a subset of a stream's shards.
const (
	ShardFilterAfterShardID    = "AFTER_SHARD_ID"
	ShardFilterAtTrimHorizon   = "AT_TRIM_HORIZON"
	ShardFilterFromTrimHorizon = "FROM_TRIM_HORIZON"
	ShardFilterAtLatest        = "AT_LATEST"
	ShardFilterAtTimestamp     = "AT_TIMESTAMP"
	ShardFilterFromTimestamp   = "FROM_TIMESTAMP"
)

// HashKeyRange is the range of MD5 hash-key values a shard owns.
type HashKeyRange struct {
	StartingHashKey string
	EndingHashKey   string
}

// SequenceNumberRange is a shard's starting and (once closed) ending sequence
// number.
type SequenceNumberRange struct {
	StartingSequenceNumber string
	EndingSequenceNumber   string // empty while the shard is open
}

// Shard is one shard of a stream.
type Shard struct {
	ShardID               string
	ParentShardID         string
	AdjacentParentShardID string
	HashKeyRange          HashKeyRange
	SequenceNumberRange   SequenceNumberRange
}

// Record is a single data record stored in a shard.
type Record struct {
	SequenceNumber              string
	ApproximateArrivalTimestamp time.Time
	Data                        []byte
	PartitionKey                string
	EncryptionType              string
}

// Consumer is an enhanced fan-out consumer registered against a stream.
type Consumer struct {
	ConsumerName              string
	ConsumerARN               string
	ConsumerStatus            string
	ConsumerCreationTimestamp time.Time
	StreamARN                 string
}

// StreamDescription is the full description returned by DescribeStream.
type StreamDescription struct {
	StreamName              string
	StreamARN               string
	StreamStatus            string
	StreamModeDetails       string // stream mode
	Shards                  []Shard
	HasMoreShards           bool
	RetentionPeriodHours    int32
	StreamCreationTimestamp time.Time
	EnhancedMonitoring      []string
	EncryptionType          string
	KeyID                   string
}

// StreamSummary is the lighter description returned by DescribeStreamSummary.
type StreamSummary struct {
	StreamName              string
	StreamARN               string
	StreamStatus            string
	StreamModeDetails       string
	RetentionPeriodHours    int32
	StreamCreationTimestamp time.Time
	EnhancedMonitoring      []string
	EncryptionType          string
	KeyID                   string
	OpenShardCount          int32
	ConsumerCount           int32
}

// CreateStreamInput describes a stream to create.
type CreateStreamInput struct {
	StreamName string
	ShardCount int32
	StreamMode string
	Tags       map[string]string
}

// PutRecordInput describes a single record to put.
type PutRecordInput struct {
	StreamName                string
	StreamARN                 string
	Data                      []byte
	PartitionKey              string
	ExplicitHashKey           string
	SequenceNumberForOrdering string
}

// PutRecordResult is the result of putting one record.
type PutRecordResult struct {
	ShardID        string
	SequenceNumber string
	EncryptionType string
}

// PutRecordsRequestEntry is one entry of a PutRecords batch.
type PutRecordsRequestEntry struct {
	Data            []byte
	PartitionKey    string
	ExplicitHashKey string
}

// PutRecordsResultEntry is one result of a PutRecords batch (success or error).
type PutRecordsResultEntry struct {
	ShardID        string
	SequenceNumber string
	ErrorCode      string
	ErrorMessage   string
}

// GetShardIteratorInput describes a shard iterator to create.
type GetShardIteratorInput struct {
	StreamName             string
	StreamARN              string
	ShardID                string
	ShardIteratorType      string
	StartingSequenceNumber string
	Timestamp              time.Time
}

// GetRecordsOutput is the result of GetRecords.
type GetRecordsOutput struct {
	Records            []Record
	NextShardIterator  string
	MillisBehindLatest int64
	ChildShards        []ChildShard
}

// ChildShard describes a child shard for a closed shard, returned by GetRecords.
type ChildShard struct {
	ShardID      string
	ParentShards []string
	HashKeyRange HashKeyRange
}

// SubscribeToShardInput describes an enhanced fan-out subscription. StartingPosition
// mirrors the shard-iterator types (TRIM_HORIZON, LATEST, AT_SEQUENCE_NUMBER,
// AFTER_SEQUENCE_NUMBER, AT_TIMESTAMP).
type SubscribeToShardInput struct {
	ConsumerARN            string
	ShardID                string
	StartingPositionType   string
	StartingSequenceNumber string
	StartingTimestamp      time.Time
}

// SubscribeToShardResult carries one enhanced fan-out event's worth of records
// plus the continuation cursor used to resume the subscription.
type SubscribeToShardResult struct {
	Records                    []Record
	ContinuationSequenceNumber string
	MillisBehindLatest         int64
}

// ShardFilter narrows ListShards to a subset of shards. Type is one of the
// ShardFilter* constants; ShardID applies only to AFTER_SHARD_ID and Timestamp
// only to AT_TIMESTAMP / FROM_TIMESTAMP.
type ShardFilter struct {
	Type      string
	ShardID   string
	Timestamp time.Time
}

// ListShardsInput narrows ListShards.
type ListShardsInput struct {
	StreamName            string
	StreamARN             string
	NextToken             string
	MaxResults            int32
	ExclusiveStartShardID string
	ShardFilter           *ShardFilter
}

// ListShardsOutput is the result of ListShards.
type ListShardsOutput struct {
	Shards    []Shard
	NextToken string
}

// ListStreamsOutput is the result of ListStreams.
type ListStreamsOutput struct {
	StreamNames     []string
	StreamSummaries []StreamSummary
	HasMoreStreams  bool
	NextToken       string
}

// AccountSettings is the account-level Kinesis configuration, modeling the
// minimum-throughput billing commitment.
type AccountSettings struct {
	CommitmentStatus     string // e.g. ENABLED / DISABLED
	StartedAt            time.Time
	EndedAt              time.Time
	EarliestAllowedEndAt time.Time
}

// Limits is the result of DescribeLimits.
type Limits struct {
	ShardLimit               int32
	OpenShardCount           int32
	OnDemandStreamCount      int32
	OnDemandStreamCountLimit int32
}

// Kinesis is the interface a Kinesis backend implements. Streams are referenced
// by name or ARN; records are addressed by opaque shard iterators.
type Kinesis interface {
	// Stream lifecycle & configuration.
	CreateStream(ctx context.Context, in CreateStreamInput) error
	DeleteStream(ctx context.Context, name, arn string, enforceConsumerDeletion bool) error
	DescribeStream(ctx context.Context, name, arn string, limit int32, exclusiveStartShardID string) (*StreamDescription, error)
	DescribeStreamSummary(ctx context.Context, name, arn string) (*StreamSummary, error)
	ListStreams(ctx context.Context, nextToken, exclusiveStartStreamName string, limit int32) (*ListStreamsOutput, error)
	IncreaseStreamRetentionPeriod(ctx context.Context, name, arn string, hours int32) error
	DecreaseStreamRetentionPeriod(ctx context.Context, name, arn string, hours int32) error
	UpdateShardCount(ctx context.Context, name, arn string, targetCount int32, scalingType string) (current, target int32, err error)
	UpdateStreamMode(ctx context.Context, arn, mode string) error
	MergeShards(ctx context.Context, name, arn, shardToMerge, adjacentShardToMerge string) error
	SplitShard(ctx context.Context, name, arn, shardToSplit, newStartingHashKey string) error
	StartStreamEncryption(ctx context.Context, name, arn, encryptionType, keyID string) error
	StopStreamEncryption(ctx context.Context, name, arn, encryptionType, keyID string) error
	UpdateMaxRecordSize(ctx context.Context, name, arn string, maxRecordSizeInKiB int32) error
	UpdateStreamWarmThroughput(ctx context.Context, name, arn string, warmThroughputMiBps int32) error

	// Records.
	PutRecord(ctx context.Context, in PutRecordInput) (*PutRecordResult, error)
	PutRecords(ctx context.Context, name, arn string, entries []PutRecordsRequestEntry) ([]PutRecordsResultEntry, int32, error)
	GetShardIterator(ctx context.Context, in GetShardIteratorInput) (string, error)
	GetRecords(ctx context.Context, shardIterator string, limit int32) (*GetRecordsOutput, error)
	ListShards(ctx context.Context, in ListShardsInput) (*ListShardsOutput, error)

	// Enhanced fan-out consumers.
	RegisterStreamConsumer(ctx context.Context, streamARN, consumerName string) (*Consumer, error)
	DeregisterStreamConsumer(ctx context.Context, streamARN, consumerName, consumerARN string) error
	DescribeStreamConsumer(ctx context.Context, streamARN, consumerName, consumerARN string) (*Consumer, error)
	ListStreamConsumers(ctx context.Context, streamARN, nextToken string, maxResults int32) ([]Consumer, string, error)
	SubscribeToShard(ctx context.Context, in SubscribeToShardInput) (*SubscribeToShardResult, error)

	// Enhanced monitoring.
	EnableEnhancedMonitoring(ctx context.Context, name, arn string, metrics []string) (current, desired []string, err error)
	DisableEnhancedMonitoring(ctx context.Context, name, arn string, metrics []string) (current, desired []string, err error)

	// Tags.
	AddTagsToStream(ctx context.Context, name, arn string, tags map[string]string) error
	RemoveTagsFromStream(ctx context.Context, name, arn string, keys []string) error
	ListTagsForStream(ctx context.Context, name, arn, exclusiveStartTagKey string, limit int32) (map[string]string, bool, error)
	TagResource(ctx context.Context, resourceARN string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceARN string, keys []string) error
	ListTagsForResource(ctx context.Context, resourceARN string) (map[string]string, error)

	// Resource policy.
	PutResourcePolicy(ctx context.Context, resourceARN, policy string) error
	GetResourcePolicy(ctx context.Context, resourceARN string) (string, error)
	DeleteResourcePolicy(ctx context.Context, resourceARN string) error

	// Account & limits.
	DescribeLimits(ctx context.Context) (*Limits, error)
	DescribeAccountSettings(ctx context.Context) (*AccountSettings, error)
	UpdateAccountSettings(ctx context.Context, in AccountSettings) (*AccountSettings, error)
}
