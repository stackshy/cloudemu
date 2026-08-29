package kinesis

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

const (
	defaultGetRecordsLimit = 10000
	// maxRecordBytes is Kinesis's per-record data cap (1 MiB) when the stream has
	// no explicit MaxRecordSize configured.
	maxRecordBytes = 1 << 20
	bytesPerKiB    = 1024
	// maxBatchRecords / maxBatchBytes are the PutRecords request limits.
	maxBatchRecords = 500
	maxBatchBytes   = 5 << 20
	// shardIteratorTTL is how long a shard iterator stays valid after it is
	// returned. Real Kinesis expires iterators 5 minutes after creation; a
	// GetRecords call refreshes the NextShardIterator it hands back.
	shardIteratorTTL = 5 * time.Minute
)

// recordSizeLimit returns the per-record byte cap for a stream, honoring a
// configured MaxRecordSize (KiB) or falling back to the 1 MiB default.
func recordSizeLimit(sd *streamData) int {
	if sd.maxRecKiB > 0 {
		return int(sd.maxRecKiB) * bytesPerKiB
	}

	return maxRecordBytes
}

// validateBatchSize enforces the PutRecords per-record and aggregate byte caps.
// The aggregate counts each record's data plus partition key, matching Kinesis.
func validateBatchSize(sd *streamData, entries []driver.PutRecordsRequestEntry) error {
	limit := recordSizeLimit(sd)
	total := 0

	for i := range entries {
		if len(entries[i].Data) > limit {
			return validationErr("record %d data of %d bytes exceeds the %d-byte limit",
				i, len(entries[i].Data), limit)
		}

		total += len(entries[i].Data) + len(entries[i].PartitionKey)
	}

	if total > maxBatchBytes {
		return validationErr("PutRecords batch of %d bytes exceeds the %d-byte limit", total, maxBatchBytes)
	}

	return nil
}

// iteratorToken is the opaque state a shard iterator carries. Records are never
// trimmed in the emulator, so a stable slice index is a sufficient cursor.
// CreatedAtMillis stamps when the iterator was minted (Unix milliseconds) so
// GetRecords can reject iterators older than shardIteratorTTL without any extra
// server-side state.
type iteratorToken struct {
	StreamName      string `json:"s"`
	ShardID         string `json:"h"`
	NextIndex       int    `json:"i"`
	CreatedAtMillis int64  `json:"c"`
}

func encodeIterator(t iteratorToken) string {
	b, _ := json.Marshal(t)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeIterator(s string) (iteratorToken, error) {
	var t iteratorToken

	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return t, invalidArg("shard iterator is malformed")
	}

	if err := json.Unmarshal(raw, &t); err != nil {
		return t, invalidArg("shard iterator is malformed")
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
func (m *Mock) PutRecord(ctx context.Context, in driver.PutRecordInput) (*driver.PutRecordResult, error) {
	if in.PartitionKey == "" {
		return nil, invalidArg("PartitionKey is required")
	}

	sd, err := m.resolve(in.StreamName, in.StreamARN)
	if err != nil {
		return nil, err
	}

	sd.mu.Lock()

	if limit := recordSizeLimit(sd); len(in.Data) > limit {
		sd.mu.Unlock()
		return nil, validationErr("record data of %d bytes exceeds the %d-byte limit", len(in.Data), limit)
	}

	shard, seq, err := m.appendRecord(sd, in.PartitionKey, in.ExplicitHashKey, in.Data)
	if err != nil {
		sd.mu.Unlock()
		return nil, err
	}

	res := &driver.PutRecordResult{
		ShardID:        shard,
		SequenceNumber: seq,
		EncryptionType: sd.desc.EncryptionType,
	}
	esm := driver.LambdaEventRecord{
		ShardID: shard, SequenceNumber: seq, PartitionKey: in.PartitionKey,
		Data: in.Data, ArrivalTime: m.now(),
	}
	streamARN := m.streamARN(ctx, sd.desc.StreamName)
	sd.mu.Unlock()

	m.deliverToLambda(ctx, streamARN, []driver.LambdaEventRecord{esm})

	return res, nil
}

// PutRecords appends a batch of records, returning a per-entry result (records
// never fail individually in the emulator) and the failed count.
func (m *Mock) PutRecords(
	ctx context.Context, name, arn string, entries []driver.PutRecordsRequestEntry,
) ([]driver.PutRecordsResultEntry, int32, error) {
	if len(entries) == 0 {
		return nil, 0, invalidArg("Records must contain at least one entry")
	}

	if len(entries) > maxBatchRecords {
		return nil, 0, validationErr("a PutRecords request may contain at most %d records, got %d",
			maxBatchRecords, len(entries))
	}

	sd, err := m.resolve(name, arn)
	if err != nil {
		return nil, 0, err
	}

	sd.mu.Lock()

	if err := validateBatchSize(sd, entries); err != nil {
		sd.mu.Unlock()
		return nil, 0, err
	}

	out := make([]driver.PutRecordsResultEntry, 0, len(entries))
	esm := make([]driver.LambdaEventRecord, 0, len(entries))

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
		esm = append(esm, driver.LambdaEventRecord{
			ShardID: shard, SequenceNumber: seq, PartitionKey: entries[i].PartitionKey,
			Data: entries[i].Data, ArrivalTime: m.now(),
		})
	}

	var failed int32

	for i := range out {
		if out[i].ErrorCode != "" {
			failed++
		}
	}

	streamARN := m.streamARN(ctx, sd.desc.StreamName)
	sd.mu.Unlock()

	m.deliverToLambda(ctx, streamARN, esm)

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

	// An explicit hash key must fall within the 128-bit key space. Real Kinesis
	// returns InvalidArgumentException for out-of-range values.
	if key.Sign() < 0 || key.Cmp(maxHashKey()) > 0 {
		return "", "", invalidArg("ExplicitHashKey %q is out of range [0, 2^128)", explicitHashKey)
	}

	shard := shardForHashKey(sd.shards, key)
	if shard == nil {
		// Unreachable for MD5-routed keys (they tile the full range) and for
		// bounds-checked explicit keys — a genuine internal invariant.
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

	return m.encodeFreshIterator(sd.desc.StreamName, in.ShardID, idx), nil
}

// encodeFreshIterator mints an iterator stamped with the current clock time, so
// its shardIteratorTTL window starts now.
func (m *Mock) encodeFreshIterator(streamName, shardID string, nextIndex int) string {
	return encodeIterator(iteratorToken{
		StreamName:      streamName,
		ShardID:         shardID,
		NextIndex:       nextIndex,
		CreatedAtMillis: m.now().UnixMilli(),
	})
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
		if in.Timestamp.IsZero() {
			return 0, invalidArg("AT_TIMESTAMP shard iterator type requires a Timestamp value")
		}

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

	createdAt := time.UnixMilli(tok.CreatedAtMillis).UTC()
	if m.now().Sub(createdAt) > shardIteratorTTL {
		return nil, expiredIterator(
			"Iterator expired. The iterator was created at time %s which is now expired.",
			createdAt.Format(time.RFC1123))
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
		NextShardIterator:  m.encodeFreshIterator(tok.StreamName, tok.ShardID, end),
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

// listShardsToken is the opaque NextToken ListShards hands back: real Kinesis
// forbids passing StreamName or ShardFilter alongside NextToken, so the token
// must carry the stream identity, the cursor to resume after, and any active
// filter so later pages stay consistent with the first.
type listShardsToken struct {
	StreamName string `json:"s"`
	AfterShard string `json:"a"`
	FilterType string `json:"f,omitempty"`
	FilterTS   int64  `json:"t,omitempty"` // Unix seconds; 0 when no timestamp
}

// filter reconstructs the ShardFilter a paginating token must keep applying.
// Full-set and AFTER_SHARD_ID filters aren't persisted — the AfterShard cursor
// already captures them — so only the state-narrowing types come back here.
func (t listShardsToken) filter() *driver.ShardFilter {
	if t.FilterType == "" {
		return nil
	}

	f := &driver.ShardFilter{Type: t.FilterType}
	if t.FilterTS != 0 {
		f.Timestamp = time.Unix(t.FilterTS, 0).UTC()
	}

	return f
}

// newListShardsToken builds the next-page token, persisting only filters that
// still constrain subsequent pages.
func newListShardsToken(streamName, afterShard string, f *driver.ShardFilter) listShardsToken {
	t := listShardsToken{StreamName: streamName, AfterShard: afterShard}

	if f != nil {
		switch f.Type {
		case driver.ShardFilterAtLatest, driver.ShardFilterAtTrimHorizon,
			driver.ShardFilterAtTimestamp, driver.ShardFilterFromTimestamp:
			t.FilterType = f.Type
			if !f.Timestamp.IsZero() {
				t.FilterTS = f.Timestamp.Unix()
			}
		}
	}

	return t
}

func encodeListShardsToken(t listShardsToken) string {
	b, _ := json.Marshal(t)
	return base64.StdEncoding.EncodeToString(b)
}

func decodeListShardsToken(s string) (listShardsToken, error) {
	var t listShardsToken

	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return t, invalidArg("NextToken is malformed")
	}

	if err := json.Unmarshal(raw, &t); err != nil {
		return t, invalidArg("NextToken is malformed")
	}

	return t, nil
}

// ListShards lists a stream's shards, optionally narrowed by a ShardFilter.
//
//nolint:gocritic // in is the public ListShards input, taken by value to match the driver API
func (m *Mock) ListShards(_ context.Context, in driver.ListShardsInput) (*driver.ListShardsOutput, error) {
	streamName, streamARN := in.StreamName, in.StreamARN

	// A NextToken from a prior page carries the stream identity + resume cursor
	// (+ any active filter). Otherwise AFTER_SHARD_ID sets the initial cursor.
	start := in.ExclusiveStartShardID
	filter := in.ShardFilter

	switch {
	case in.NextToken != "":
		tok, err := decodeListShardsToken(in.NextToken)
		if err != nil {
			return nil, err
		}

		streamName, streamARN, start, filter = tok.StreamName, "", tok.AfterShard, tok.filter()
	case filter != nil && filter.Type == driver.ShardFilterAfterShardID:
		start = filter.ShardID
	}

	sd, err := m.resolve(streamName, streamARN)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	filtered, err := filterShards(sd.shards, filter, sd.desc.StreamCreationTimestamp)
	if err != nil {
		return nil, err
	}

	shards, more := pageShards(filtered, in.MaxResults, start)

	out := &driver.ListShardsOutput{Shards: shards}
	if more && len(shards) > 0 {
		out.NextToken = encodeListShardsToken(
			newListShardsToken(sd.desc.StreamName, shards[len(shards)-1].ShardID, filter))
	}

	return out, nil
}

// filterShards returns the subset of shards a ShardFilter selects. Records are
// never trimmed in the emulator, so the trim horizon is the stream's creation
// time and the retention window spans creation→now.
func filterShards(shards []*shardState, f *driver.ShardFilter, trimHorizon time.Time) ([]*shardState, error) {
	if f == nil || f.Type == "" {
		return shards, nil
	}

	switch f.Type {
	case driver.ShardFilterFromTrimHorizon, driver.ShardFilterAfterShardID:
		// Whole shard set; AFTER_SHARD_ID additionally advances the start cursor.
		return shards, nil
	case driver.ShardFilterAtLatest:
		return selectShards(shards, func(ss *shardState) bool { return !ss.closed }), nil
	case driver.ShardFilterAtTrimHorizon:
		return selectShards(shards, func(ss *shardState) bool { return openAt(ss, trimHorizon) }), nil
	case driver.ShardFilterAtTimestamp, driver.ShardFilterFromTimestamp:
		return filterByTimestamp(shards, f, trimHorizon)
	default:
		return nil, invalidArg("unsupported ShardFilter type %q", f.Type)
	}
}

// filterByTimestamp applies the AT_TIMESTAMP / FROM_TIMESTAMP filters, both of
// which require a Timestamp. AT_TIMESTAMP returns shards open at that instant;
// FROM_TIMESTAMP returns open shards plus closed shards ending at/after it
// (clamped up to the trim horizon).
func filterByTimestamp(shards []*shardState, f *driver.ShardFilter, trimHorizon time.Time) ([]*shardState, error) {
	if f.Timestamp.IsZero() {
		return nil, invalidArg("ShardFilter %s requires a Timestamp", f.Type)
	}

	if f.Type == driver.ShardFilterAtTimestamp {
		return selectShards(shards, func(ss *shardState) bool { return openAt(ss, f.Timestamp) }), nil
	}

	ts := f.Timestamp
	if ts.Before(trimHorizon) {
		ts = trimHorizon
	}

	return selectShards(shards, func(ss *shardState) bool {
		return !ss.closed || !ss.closedAt.Before(ts)
	}), nil
}

// openAt reports whether a shard was open at ts: created at or before ts and not
// yet closed at ts (start <= ts <= end, or still open).
func openAt(ss *shardState, ts time.Time) bool {
	if ss.createdAt.After(ts) {
		return false
	}

	return ss.closedAt.IsZero() || !ss.closedAt.Before(ts)
}

func selectShards(shards []*shardState, keep func(*shardState) bool) []*shardState {
	out := make([]*shardState, 0, len(shards))

	for _, ss := range shards {
		if keep(ss) {
			out = append(out, ss)
		}
	}

	return out
}
