package kinesis

import (
	"context"

	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// RegisterStreamConsumer registers an enhanced fan-out consumer on a stream.
func (m *Mock) RegisterStreamConsumer(_ context.Context, streamARN, consumerName string) (*driver.Consumer, error) {
	if consumerName == "" {
		return nil, invalidArg("ConsumerName is required")
	}

	sd, err := m.resolve("", streamARN)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if _, exists := sd.consumers[consumerName]; exists {
		return nil, errInUse("consumer %q already exists on the stream", consumerName)
	}

	c := &driver.Consumer{
		ConsumerName:              consumerName,
		ConsumerARN:               m.consumerARN(sd.desc.StreamName, consumerName),
		ConsumerStatus:            driver.ConsumerActive,
		ConsumerCreationTimestamp: m.now(),
		StreamARN:                 sd.desc.StreamARN,
	}
	sd.consumers[consumerName] = c

	out := *c

	return &out, nil
}

// DeregisterStreamConsumer removes a consumer by name or ARN.
func (m *Mock) DeregisterStreamConsumer(_ context.Context, streamARN, consumerName, consumerARN string) error {
	sd, name, err := m.findConsumerStream(streamARN, consumerName, consumerARN)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if _, ok := sd.consumers[name]; !ok {
		return errNotFound("consumer %q not found", name)
	}

	delete(sd.consumers, name)

	return nil
}

// DescribeStreamConsumer returns a consumer by name or ARN.
func (m *Mock) DescribeStreamConsumer(
	_ context.Context, streamARN, consumerName, consumerARN string,
) (*driver.Consumer, error) {
	sd, name, err := m.findConsumerStream(streamARN, consumerName, consumerARN)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	c, ok := sd.consumers[name]
	if !ok {
		return nil, errNotFound("consumer %q not found", name)
	}

	out := *c

	return &out, nil
}

// ListStreamConsumers lists a stream's consumers.
func (m *Mock) ListStreamConsumers(
	_ context.Context, streamARN, _ string, _ int32,
) ([]driver.Consumer, string, error) {
	sd, err := m.resolve("", streamARN)
	if err != nil {
		return nil, "", err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := make([]driver.Consumer, 0, len(sd.consumers))
	for _, c := range sd.consumers {
		out = append(out, *c)
	}

	return out, "", nil
}

// SubscribeToShard resolves the consumer's stream and shard, reads the shard's
// records from the requested starting position, and returns them with a
// continuation sequence number for resuming the subscription. It reuses the same
// position resolution as GetShardIterator so all StartingPosition types behave
// consistently with the polling path.
//
//nolint:gocritic // in is the public SubscribeToShard input, taken by value to match the driver API
func (m *Mock) SubscribeToShard(
	_ context.Context, in driver.SubscribeToShardInput,
) (*driver.SubscribeToShardResult, error) {
	if in.ConsumerARN == "" {
		return nil, invalidArg("ConsumerARN is required")
	}

	if in.ShardID == "" {
		return nil, invalidArg("ShardId is required")
	}

	sd, _, err := m.findConsumerStream("", "", in.ConsumerARN)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	shard := findShardByID(sd.shards, in.ShardID)
	if shard == nil {
		return nil, errNotFound("shard %q not found", in.ShardID)
	}

	start, err := positionFor(shard, &driver.GetShardIteratorInput{
		ShardIteratorType:      in.StartingPositionType,
		StartingSequenceNumber: in.StartingSequenceNumber,
		Timestamp:              in.StartingTimestamp,
	})
	if err != nil {
		return nil, err
	}

	start = min(start, len(shard.records))
	recs := make([]driver.Record, len(shard.records)-start)
	copy(recs, shard.records[start:])

	return &driver.SubscribeToShardResult{
		Records:                    recs,
		ContinuationSequenceNumber: continuationSeq(shard, recs),
		MillisBehindLatest:         0,
	}, nil
}

// continuationSeq is the cursor a client uses to resume a subscription: the last
// delivered record's sequence number, or the shard's starting sequence number
// when no records were delivered.
func continuationSeq(shard *shardState, recs []driver.Record) string {
	if n := len(recs); n > 0 {
		return recs[n-1].SequenceNumber
	}

	return shard.shard.SequenceNumberRange.StartingSequenceNumber
}

// findConsumerStream resolves the stream + consumer name from any combination of
// stream ARN, consumer name, and consumer ARN.
func (m *Mock) findConsumerStream(streamARN, consumerName, consumerARN string) (*streamData, string, error) {
	if streamARN != "" {
		sd, err := m.resolve("", streamARN)
		if err != nil {
			return nil, "", err
		}

		if consumerName != "" {
			return sd, consumerName, nil
		}
	}

	// Fall back to scanning by consumer ARN.
	if consumerARN != "" {
		for _, sd := range m.streams.All() {
			sd.mu.RLock()
			for name, c := range sd.consumers {
				if c.ConsumerARN == consumerARN {
					sd.mu.RUnlock()
					return sd, name, nil
				}
			}
			sd.mu.RUnlock()
		}

		return nil, "", errNotFound("consumer %q not found", consumerARN)
	}

	if streamARN != "" && consumerName != "" {
		sd, err := m.resolve("", streamARN)

		return sd, consumerName, err
	}

	return nil, "", invalidArg("ConsumerName or ConsumerARN is required")
}
