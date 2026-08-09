package kinesis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

const defaultGetRecordsLimit = 10000

// iteratorToken is the opaque state a shard iterator carries. Records are never
// trimmed in the emulator, so a stable slice index is a sufficient cursor.
type iteratorToken struct {
	StreamName string `json:"s"`
	ShardID    string `json:"h"`
	NextIndex  int    `json:"i"`
}

func encodeIterator(t iteratorToken) string {
	b, _ := json.Marshal(t)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeIterator(s string) (iteratorToken, error) {
	var t iteratorToken

	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return t, expiredIterator("shard iterator is malformed")
	}

	if err := json.Unmarshal(raw, &t); err != nil {
		return t, expiredIterator("shard iterator is malformed")
	}

	return t, nil
}

// shardForHashKey returns the open shard whose hash-key range covers key.
func shardForHashKey(shards []*shardState, key *big.Int) *shardState {
	for _, ss := range shards {
		if ss.closed {
			continue
		}

		start, _ := new(big.Int).SetString(ss.shard.HashKeyRange.StartingHashKey, base10)
		end, _ := new(big.Int).SetString(ss.shard.HashKeyRange.EndingHashKey, base10)

		if key.Cmp(start) >= 0 && key.Cmp(end) <= 0 {
			return ss
		}
	}

	return nil
}

// PutRecord routes a record to the open shard covering its hash key and appends
// it, returning the assigned shard id and sequence number.
//
//nolint:gocritic // in is the public PutRecord input, taken by value to match the driver API
func (m *Mock) PutRecord(_ context.Context, in driver.PutRecordInput) (*driver.PutRecordResult, error) {
	if in.PartitionKey == "" {
		return nil, invalidArg("PartitionKey is required")
	}

	sd, err := m.resolve(in.StreamName, in.StreamARN)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	shard, seq, err := m.appendRecord(sd, in.PartitionKey, in.ExplicitHashKey, in.Data)
	if err != nil {
		return nil, err
	}

	return &driver.PutRecordResult{
		ShardID:        shard,
		SequenceNumber: seq,
		EncryptionType: sd.desc.EncryptionType,
	}, nil
}

// PutRecords appends a batch of records, returning a per-entry result (records
// never fail individually in the emulator) and the failed count.
func (m *Mock) PutRecords(
	_ context.Context, name, arn string, entries []driver.PutRecordsRequestEntry,
) ([]driver.PutRecordsResultEntry, int32, error) {
	if len(entries) == 0 {
		return nil, 0, invalidArg("Records must contain at least one entry")
	}

	sd, err := m.resolve(name, arn)
	if err != nil {
		return nil, 0, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	out := make([]driver.PutRecordsResultEntry, 0, len(entries))

	for i := range entries {
		if entries[i].PartitionKey == "" {
			out = append(out, driver.PutRecordsResultEntry{
				ErrorCode: "ValidationException", ErrorMessage: "PartitionKey is required",
			})

			continue
		}

		shard, seq, aerr := m.appendRecord(sd, entries[i].PartitionKey, entries[i].ExplicitHashKey, entries[i].Data)
		if aerr != nil {
			out = append(out, driver.PutRecordsResultEntry{
				ErrorCode: "InternalFailure", ErrorMessage: aerr.Error(),
			})

			continue
		}

		out = append(out, driver.PutRecordsResultEntry{ShardID: shard, SequenceNumber: seq})
	}

	var failed int32

	for i := range out {
		if out[i].ErrorCode != "" {
			failed++
		}
	}

	return out, failed, nil
}

// appendRecord routes and stores one record; caller holds sd.mu.
func (m *Mock) appendRecord(
	sd *streamData, partitionKey, explicitHashKey string, data []byte,
) (shardID, sequenceNumber string, err error) {
	keyStr := explicitHashKey
	if keyStr == "" {
		keyStr = hashKeyOf(partitionKey)
	}

	key, ok := new(big.Int).SetString(keyStr, base10)
	if !ok {
		return "", "", invalidArg("ExplicitHashKey %q is not a valid hash key", explicitHashKey)
	}

	shard := shardForHashKey(sd.shards, key)
	if shard == nil {
		return "", "", errInUse("no open shard covers the record's hash key")
	}

	seq := sd.nextSeq()
	shard.records = append(shard.records, driver.Record{
		SequenceNumber:              seq,
		ApproximateArrivalTimestamp: m.now(),
		Data:                        append([]byte(nil), data...),
		PartitionKey:                partitionKey,
		EncryptionType:              sd.desc.EncryptionType,
	})

	return shard.shard.ShardID, seq, nil
}

// GetShardIterator returns an opaque iterator positioned per the requested type.
//
//nolint:gocritic // in is the public GetShardIterator input, taken by value to match the driver API
func (m *Mock) GetShardIterator(_ context.Context, in driver.GetShardIteratorInput) (string, error) {
	sd, err := m.resolve(in.StreamName, in.StreamARN)
	if err != nil {
		return "", err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	shard := findShardByID(sd.shards, in.ShardID)
	if shard == nil {
		return "", errNotFound("shard %q not found", in.ShardID)
	}

	idx, err := positionFor(shard, &in)
	if err != nil {
		return "", err
	}

	return encodeIterator(iteratorToken{StreamName: sd.desc.StreamName, ShardID: in.ShardID, NextIndex: idx}), nil
}

// findShardByID returns the shard with the given id, or nil.
func findShardByID(shards []*shardState, id string) *shardState {
	for _, ss := range shards {
		if ss.shard.ShardID == id {
			return ss
		}
	}

	return nil
}

// positionFor resolves a shard-iterator type to a starting record index.
func positionFor(shard *shardState, in *driver.GetShardIteratorInput) (int, error) {
	switch in.ShardIteratorType {
	case driver.IteratorTrimHorizon:
		return 0, nil
	case driver.IteratorLatest:
		return len(shard.records), nil
	case driver.IteratorAtTimestamp:
		return indexByTimestamp(shard.records, in.Timestamp), nil
	case driver.IteratorAtSequenceNumber:
		return indexBySeq(shard.records, in.StartingSequenceNumber, false)
	case driver.IteratorAfterSequenceNumber:
		return indexBySeq(shard.records, in.StartingSequenceNumber, true)
	default:
		return 0, invalidArg("unsupported ShardIteratorType %q", in.ShardIteratorType)
	}
}

func indexByTimestamp(records []driver.Record, ts time.Time) int {
	for i := range records {
		if !records[i].ApproximateArrivalTimestamp.Before(ts) {
			return i
		}
	}

	return len(records)
}

func indexBySeq(records []driver.Record, seq string, after bool) (int, error) {
	for i := range records {
		if records[i].SequenceNumber == seq {
			if after {
				return i + 1, nil
			}

			return i, nil
		}
	}

	return 0, invalidArg("sequence number %q not found in shard", seq)
}

// GetRecords returns records from the iterator position and advances it.
func (m *Mock) GetRecords(_ context.Context, shardIterator string, limit int32) (*driver.GetRecordsOutput, error) {
	tok, err := decodeIterator(shardIterator)
	if err != nil {
		return nil, err
	}

	sd, ok := m.streams.Get(tok.StreamName)
	if !ok {
		return nil, errNotFoundName(tok.StreamName)
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	shard := findShardByID(sd.shards, tok.ShardID)
	if shard == nil {
		return nil, expiredIterator("shard %q no longer exists", tok.ShardID)
	}

	if limit <= 0 || limit > defaultGetRecordsLimit {
		limit = defaultGetRecordsLimit
	}

	start := min(tok.NextIndex, len(shard.records))
	end := min(start+int(limit), len(shard.records))

	recs := make([]driver.Record, end-start)
	copy(recs, shard.records[start:end])

	out := &driver.GetRecordsOutput{
		Records:            recs,
		NextShardIterator:  encodeIterator(iteratorToken{StreamName: tok.StreamName, ShardID: tok.ShardID, NextIndex: end}),
		MillisBehindLatest: 0,
	}

	// A drained, closed shard reports its children and a nil iterator (end of shard).
	if shard.closed && end == len(shard.records) {
		out.NextShardIterator = ""
		out.ChildShards = childShardsOf(sd.shards, shard.shard.ShardID)
	}

	return out, nil
}

func childShardsOf(shards []*shardState, parentID string) []driver.ChildShard {
	var out []driver.ChildShard

	for _, ss := range shards {
		if ss.shard.ParentShardID == parentID || ss.shard.AdjacentParentShardID == parentID {
			parents := []string{ss.shard.ParentShardID}
			if ss.shard.AdjacentParentShardID != "" {
				parents = append(parents, ss.shard.AdjacentParentShardID)
			}

			out = append(out, driver.ChildShard{
				ShardID:      ss.shard.ShardID,
				ParentShards: parents,
				HashKeyRange: ss.shard.HashKeyRange,
			})
		}
	}

	return out
}

// ListShards lists a stream's shards.
func (m *Mock) ListShards(_ context.Context, in driver.ListShardsInput) (*driver.ListShardsOutput, error) {
	sd, err := m.resolve(in.StreamName, in.StreamARN)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	shards, _ := pageShards(sd.shards, in.MaxResults, in.ExclusiveStartShardID)

	return &driver.ListShardsOutput{Shards: shards}, nil
}
