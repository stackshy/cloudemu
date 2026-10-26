package kinesis

import (
	"context"
	"fmt"
	"math/big"
	"sort"
	"time"

	"github.com/stackshy/cloudemu/v2/internal/settle"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// buildShards partitions the full 128-bit hash-key space into count open shards
// numbered from startIndex, each seeded with the given starting sequence number
// and creation time.
func (*Mock) buildShards(count int32, startIndex int, startSeq string, createdAt time.Time) []*shardState {
	shards := make([]*shardState, 0, count)
	maxKey := maxHashKey()
	span := new(big.Int).Div(new(big.Int).Add(maxKey, big.NewInt(1)), big.NewInt(int64(count)))

	for i := int32(0); i < count; i++ {
		start := new(big.Int).Mul(span, big.NewInt(int64(i)))

		var end *big.Int
		if i == count-1 {
			end = maxKey
		} else {
			end = new(big.Int).Sub(new(big.Int).Mul(span, big.NewInt(int64(i+1))), big.NewInt(1))
		}

		shards = append(shards, &shardState{
			createdAt: createdAt,
			shard: driver.Shard{
				ShardID:             fmt.Sprintf(shardIDFmt, startIndex+int(i)),
				HashKeyRange:        driver.HashKeyRange{StartingHashKey: start.String(), EndingHashKey: end.String()},
				SequenceNumberRange: driver.SequenceNumberRange{StartingSequenceNumber: startSeq},
			},
		})
	}

	return shards
}

// Stream lifecycle settle windows. Real Kinesis CreateStream returns while the
// stream is CREATING and DeleteStream leaves it DELETING until removal. With
// config.AsyncSettle off (the default) SettleDuration returns 0, so streams are
// ACTIVE immediately and deleted immediately, the historical behavior.
const (
	settleStreamCreate = 2 * time.Second // CREATING -> ACTIVE
	settleStreamDelete = 2 * time.Second // DELETING -> gone
)

// lookup returns the named stream, first purging it if its DELETING window has
// elapsed so a finished deletion reads as ResourceNotFoundException.
func (m *Mock) lookup(name string) (*streamData, bool) {
	sd, ok := m.streams.Get(name)
	if !ok {
		return nil, false
	}

	if m.reapIfDeleted(name, sd) {
		return nil, false
	}

	return sd, true
}

// liveStreams returns every stream that still exists, purging those whose
// DELETING window has elapsed.
func (m *Mock) liveStreams() map[string]*streamData {
	all := m.streams.All()

	for name, sd := range all {
		if m.reapIfDeleted(name, sd) {
			delete(all, name)
		}
	}

	return all
}

// reapIfDeleted removes sd when its asynchronous deletion has completed and
// reports whether it did.
func (m *Mock) reapIfDeleted(name string, sd *streamData) bool {
	sd.mu.RLock()
	done := sd.deleting && sd.deleteWin.Settled(m.now())
	arn := sd.desc.StreamARN
	sd.mu.RUnlock()

	if !done {
		return false
	}

	m.removeStream(name, arn, sd)

	return true
}

// removeStream drops the stream and its ARN index entry, unless the name has
// since been taken by a different stream. Each removal is a compare-and-delete
// under the store's lock, so a concurrent CreateStream that reaps the same entry
// and installs a new stream under the name can never have that new stream (or
// its ARN entry) removed by this purge.
func (m *Mock) removeStream(name, arn string, sd *streamData) {
	m.streams.UpdateOrDelete(name, func(cur *streamData) (*streamData, bool) {
		return cur, cur != sd
	})

	// Checking the stream store while holding the ARN index lock closes the gap
	// with CreateStream, which installs the stream before (re)writing the ARN
	// entry: either the new stream is already visible here and the entry is
	// kept, or CreateStream's Set lands after this delete and restores it.
	m.arnToName.UpdateOrDelete(arn, func(cur string) (string, bool) {
		return cur, cur != name || m.streams.Has(name)
	})
}

// statusOf is the stream's observed status: DELETING while a deletion is
// settling, CREATING inside the post-create window, else the stored status.
// Caller holds sd.mu.
func (m *Mock) statusOf(sd *streamData) string {
	if sd.deleting {
		return driver.StatusDeleting
	}

	return sd.createWin.Observe(m.now(), sd.desc.StreamStatus)
}

// requireActive returns the ResourceInUseException real Kinesis raises when a
// stream-level control-plane operation targets a stream that is not ACTIVE
// (CREATING or DELETING under AsyncSettle). Without AsyncSettle every stream is
// ACTIVE, so this never fires. Caller holds sd.mu.
func (m *Mock) requireActive(sd *streamData) error {
	if status := m.statusOf(sd); status != driver.StatusActive {
		return errInUse("Stream %s under account %s is not in ACTIVE state (current state: %s)",
			sd.desc.StreamName, m.opts.AccountID, status)
	}

	return nil
}

// CreateStream creates a new stream with the requested shard count (or on-demand
// default). It reports CREATING for a short window when AsyncSettle is on and
// ACTIVE immediately otherwise.
func (m *Mock) CreateStream(ctx context.Context, in driver.CreateStreamInput) error {
	if in.StreamName == "" {
		return invalidArg("StreamName is required")
	}

	mode := in.StreamMode
	if mode == "" {
		mode = driver.ModeProvisioned
	}

	shardCount := in.ShardCount

	if mode == driver.ModeOnDemand {
		// On-demand streams manage their own shards; AWS rejects an explicit
		// ShardCount for them and starts the stream with 4 shards.
		if in.ShardCount != 0 {
			return invalidArg("ShardCount is not supported for on-demand streams")
		}

		shardCount = onDemandStartShards
	}

	if shardCount < 1 {
		return invalidArg("ShardCount must be at least 1 for provisioned streams")
	}

	arn := m.streamARN(ctx, in.StreamName)
	now := m.now()

	sd := &streamData{
		desc: driver.StreamDescription{
			StreamName:              in.StreamName,
			StreamARN:               arn,
			StreamStatus:            driver.StatusActive,
			StreamModeDetails:       mode,
			RetentionPeriodHours:    defaultRetentionHours,
			StreamCreationTimestamp: now,
			EncryptionType:          driver.EncryptionNone,
		},
		consumers: map[string]*driver.Consumer{},
		tags:      copyTags(in.Tags),
		createWin: settle.Pending(driver.StatusCreating, now, m.opts.SettleDuration(settleStreamCreate)),
	}
	sd.shards = m.buildShards(shardCount, 0, formatSeq(0), now)

	// A stream whose deletion already completed must not block re-creation.
	if old, ok := m.streams.Get(in.StreamName); ok {
		m.reapIfDeleted(in.StreamName, old)
	}

	if !m.streams.SetIfAbsent(in.StreamName, sd) {
		return errInUse("stream %q already exists", in.StreamName)
	}

	m.arnToName.Set(arn, in.StreamName)

	return nil
}

// DeleteStream removes a stream (and its consumers when enforced). Only an
// ACTIVE stream can be deleted; with AsyncSettle on the stream then reports
// DELETING (still serving reads and writes, as real Kinesis may) until the
// settle window elapses, after which it is gone.
func (m *Mock) DeleteStream(_ context.Context, name, arn string, enforceConsumerDeletion bool) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()

	streamName := sd.desc.StreamName
	streamARN := sd.desc.StreamARN

	if err := m.requireActive(sd); err != nil {
		sd.mu.Unlock()

		return err
	}

	if len(sd.consumers) > 0 && !enforceConsumerDeletion {
		sd.mu.Unlock()

		return errInUse("stream %q has registered consumers; set EnforceConsumerDeletion", streamName)
	}

	if d := m.opts.SettleDuration(settleStreamDelete); d > 0 {
		sd.deleting = true
		sd.deleteWin = settle.Pending(driver.StatusDeleting, m.now(), d)
		sd.mu.Unlock()

		return nil
	}

	sd.mu.Unlock()

	m.removeStream(streamName, streamARN, sd)

	return nil
}

// DescribeStream returns the full stream description, paginating shards.
func (m *Mock) DescribeStream(
	_ context.Context, name, arn string, limit int32, exclusiveStartShardID string,
) (*driver.StreamDescription, error) {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	out := sd.desc
	out.StreamStatus = m.statusOf(sd)
	shards, more := pageShards(sd.shards, limit, exclusiveStartShardID)
	out.Shards = shards
	out.HasMoreShards = more

	return &out, nil
}

// pageShards returns the shard metadata after exclusiveStart, up to limit, and
// whether more remain.
func pageShards(shards []*shardState, limit int32, exclusiveStart string) ([]driver.Shard, bool) {
	maxOut := len(shards)
	if limit > 0 && int(limit) < maxOut {
		maxOut = int(limit)
	}

	out := make([]driver.Shard, 0, len(shards))
	started := exclusiveStart == ""

	for _, ss := range shards {
		if !started {
			if ss.shard.ShardID == exclusiveStart {
				started = true
			}

			continue
		}

		if len(out) == maxOut {
			return out, true
		}

		out = append(out, ss.shard)
	}

	return out, false
}

// DescribeStreamSummary returns the lighter stream summary.
func (m *Mock) DescribeStreamSummary(_ context.Context, name, arn string) (*driver.StreamSummary, error) {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return nil, err
	}

	sd.mu.RLock()
	defer sd.mu.RUnlock()

	return &driver.StreamSummary{
		StreamName:              sd.desc.StreamName,
		StreamARN:               sd.desc.StreamARN,
		StreamStatus:            m.statusOf(sd),
		StreamModeDetails:       sd.desc.StreamModeDetails,
		RetentionPeriodHours:    sd.desc.RetentionPeriodHours,
		StreamCreationTimestamp: sd.desc.StreamCreationTimestamp,
		EnhancedMonitoring:      append([]string(nil), sd.desc.EnhancedMonitoring...),
		EncryptionType:          sd.desc.EncryptionType,
		KeyID:                   sd.desc.KeyID,
		OpenShardCount:          openShardCount(sd.shards),
		ConsumerCount:           int32(len(sd.consumers)), //nolint:gosec // consumer count is tiny
	}, nil
}

func openShardCount(shards []*shardState) int32 {
	var n int32

	for _, ss := range shards {
		if !ss.closed {
			n++
		}
	}

	return n
}

// ListStreams returns stream names and summaries, ordered by name and paginated
// by limit. Results resume strictly after a cursor name: the opaque NextToken
// (modern paginator) when set, otherwise the legacy ExclusiveStartStreamName.
func (m *Mock) ListStreams(
	_ context.Context, nextToken, exclusiveStartStreamName string, limit int32,
) (*driver.ListStreamsOutput, error) {
	all := m.liveStreams()

	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}

	sort.Strings(names)

	maxOut := len(names)
	if limit > 0 && int(limit) < maxOut {
		maxOut = int(limit)
	}

	// NextToken takes precedence; ExclusiveStartStreamName is the legacy cursor.
	after := nextToken
	if after == "" {
		after = exclusiveStartStreamName
	}

	out := &driver.ListStreamsOutput{}
	started := after == ""

	for _, name := range names {
		if !started {
			if name == after {
				started = true
			}

			continue
		}

		if len(out.StreamNames) == maxOut {
			out.HasMoreStreams = true
			out.NextToken = out.StreamNames[len(out.StreamNames)-1]

			break
		}

		sd := all[name]
		sd.mu.RLock()
		out.StreamNames = append(out.StreamNames, sd.desc.StreamName)
		out.StreamSummaries = append(out.StreamSummaries, driver.StreamSummary{
			StreamName:              sd.desc.StreamName,
			StreamARN:               sd.desc.StreamARN,
			StreamStatus:            m.statusOf(sd),
			StreamModeDetails:       sd.desc.StreamModeDetails,
			RetentionPeriodHours:    sd.desc.RetentionPeriodHours,
			StreamCreationTimestamp: sd.desc.StreamCreationTimestamp,
			EncryptionType:          sd.desc.EncryptionType,
			OpenShardCount:          openShardCount(sd.shards),
			ConsumerCount:           int32(len(sd.consumers)), //nolint:gosec // consumer count is tiny
		})
		sd.mu.RUnlock()
	}

	return out, nil
}

// IncreaseStreamRetentionPeriod raises the retention period.
func (m *Mock) IncreaseStreamRetentionPeriod(_ context.Context, name, arn string, hours int32) error {
	return m.setRetention(name, arn, hours, true)
}

// DecreaseStreamRetentionPeriod lowers the retention period.
func (m *Mock) DecreaseStreamRetentionPeriod(_ context.Context, name, arn string, hours int32) error {
	return m.setRetention(name, arn, hours, false)
}

func (m *Mock) setRetention(name, arn string, hours int32, increase bool) error {
	if hours < minRetentionHours || hours > maxRetentionHours {
		return invalidArg("retention must be between %d and %d hours", minRetentionHours, maxRetentionHours)
	}

	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if err := m.requireActive(sd); err != nil {
		return err
	}

	if increase && hours < sd.desc.RetentionPeriodHours {
		return invalidArg("new retention %d is below current %d", hours, sd.desc.RetentionPeriodHours)
	}

	if !increase && hours > sd.desc.RetentionPeriodHours {
		return invalidArg("new retention %d is above current %d", hours, sd.desc.RetentionPeriodHours)
	}

	sd.desc.RetentionPeriodHours = hours

	return nil
}

// UpdateShardCount reshards to the target count by closing all open shards and
// creating target new open shards partitioning the full hash-key space. The old
// shards remain readable (closed) so in-flight records aren't lost.
func (m *Mock) UpdateShardCount(
	_ context.Context, name, arn string, targetCount int32, _ string,
) (current, target int32, err error) {
	if targetCount < 1 || targetCount > maxTargetShardCount {
		return 0, 0, invalidArg("TargetShardCount must be between 1 and %d", maxTargetShardCount)
	}

	sd, err := m.resolve(name, arn)
	if err != nil {
		return 0, 0, err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if err := m.requireActive(sd); err != nil {
		return 0, 0, err
	}

	before := openShardCount(sd.shards)

	// AWS allows scaling by at most a factor of 2 (up or down) in a single call.
	if targetCount > before*shardScaleFactor || targetCount*shardScaleFactor < before {
		return 0, 0, invalidArg(
			"TargetShardCount %d must be within half to double the current shard count %d", targetCount, before)
	}

	now := m.now()
	nextIdx := len(sd.shards)

	// Close the open shards (snapshotting their ranges), build the new open shards,
	// then link each child to the parent(s) it overlaps so draining a closed shard
	// reports its children; otherwise a following consumer dead-ends.
	parents := closeOpenShards(sd.shards, now, sd.nextSeq())
	children := m.buildShards(targetCount, nextIdx, sd.nextSeq(), now)
	linkChildren(children, parents)

	sd.shards = append(sd.shards, children...)

	return before, targetCount, nil
}

// parentRange is a closed shard's id and hash-key range, used to link the child
// shards a reshard creates back to the parents whose range they overlap.
type parentRange struct {
	id         string
	start, end *big.Int
}

// closeOpenShards closes every open shard, stamping its end sequence and close
// time, and returns the hash ranges of the shards that were open.
func closeOpenShards(shards []*shardState, closedAt time.Time, endSeq string) []parentRange {
	var parents []parentRange

	for _, ss := range shards {
		if ss.closed {
			continue
		}

		s, _ := new(big.Int).SetString(ss.shard.HashKeyRange.StartingHashKey, base10)
		e, _ := new(big.Int).SetString(ss.shard.HashKeyRange.EndingHashKey, base10)
		parents = append(parents, parentRange{ss.shard.ShardID, s, e})
		ss.closed = true
		ss.closedAt = closedAt
		ss.shard.SequenceNumberRange.EndingSequenceNumber = endSeq
	}

	return parents
}

// linkChildren records, on each child shard, the parent range(s) its hash-key
// range overlaps (up to two, matching the split/merge parent contract).
func linkChildren(children []*shardState, parents []parentRange) {
	for _, ch := range children {
		cs, _ := new(big.Int).SetString(ch.shard.HashKeyRange.StartingHashKey, base10)
		ce, _ := new(big.Int).SetString(ch.shard.HashKeyRange.EndingHashKey, base10)

		var overlap []string

		for i := range parents {
			if cs.Cmp(parents[i].end) <= 0 && ce.Cmp(parents[i].start) >= 0 {
				overlap = append(overlap, parents[i].id)
			}
		}

		if len(overlap) > 0 {
			ch.shard.ParentShardID = overlap[0]
		}

		if len(overlap) > 1 {
			ch.shard.AdjacentParentShardID = overlap[1]
		}
	}
}

// UpdateStreamMode switches a stream between PROVISIONED and ON_DEMAND.
func (m *Mock) UpdateStreamMode(_ context.Context, arn, mode string) error {
	if mode != driver.ModeProvisioned && mode != driver.ModeOnDemand {
		return invalidArg("StreamMode must be PROVISIONED or ON_DEMAND")
	}

	sd, err := m.resolve("", arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if err := m.requireActive(sd); err != nil {
		return err
	}

	sd.desc.StreamModeDetails = mode

	return nil
}

// StartStreamEncryption enables server-side encryption.
func (m *Mock) StartStreamEncryption(_ context.Context, name, arn, encryptionType, keyID string) error {
	return m.setEncryption(name, arn, encryptionType, keyID, true)
}

// StopStreamEncryption disables server-side encryption.
func (m *Mock) StopStreamEncryption(_ context.Context, name, arn, encryptionType, keyID string) error {
	return m.setEncryption(name, arn, encryptionType, keyID, false)
}

func (m *Mock) setEncryption(name, arn, encryptionType, keyID string, on bool) error {
	if on && encryptionType != driver.EncryptionKMS {
		return invalidArg("EncryptionType must be KMS")
	}

	if on && keyID == "" {
		return invalidArg("KeyId is required to start encryption")
	}

	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	if err := m.requireActive(sd); err != nil {
		return err
	}

	if on {
		sd.desc.EncryptionType = driver.EncryptionKMS
		sd.desc.KeyID = keyID
	} else {
		sd.desc.EncryptionType = driver.EncryptionNone
		sd.desc.KeyID = ""
	}

	return nil
}

// UpdateMaxRecordSize sets the stream's max record size (KiB).
func (m *Mock) UpdateMaxRecordSize(_ context.Context, name, arn string, maxRecordSizeInKiB int32) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.maxRecKiB = maxRecordSizeInKiB

	return nil
}

// UpdateStreamWarmThroughput sets the stream's warm throughput (MiB/s).
func (m *Mock) UpdateStreamWarmThroughput(_ context.Context, name, arn string, warmThroughputMiBps int32) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	sd.warmMiBps = warmThroughputMiBps

	return nil
}
