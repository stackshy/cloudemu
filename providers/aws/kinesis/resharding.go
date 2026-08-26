package kinesis

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// findOpenShard returns the open shard with the given id, or nil.
func findOpenShard(shards []*shardState, id string) *shardState {
	for _, ss := range shards {
		if ss.shard.ShardID == id && !ss.closed {
			return ss
		}
	}

	return nil
}

// SplitShard splits an open shard at newStartingHashKey into two child shards.
func (m *Mock) SplitShard(_ context.Context, name, arn, shardToSplit, newStartingHashKey string) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	parent := findOpenShard(sd.shards, shardToSplit)
	if parent == nil {
		return errNotFound("open shard %q not found", shardToSplit)
	}

	newStart, ok := new(big.Int).SetString(newStartingHashKey, base10)
	if !ok {
		return invalidArg("NewStartingHashKey %q is not a valid hash key", newStartingHashKey)
	}

	start, _ := new(big.Int).SetString(parent.shard.HashKeyRange.StartingHashKey, base10)
	end, _ := new(big.Int).SetString(parent.shard.HashKeyRange.EndingHashKey, base10)

	if newStart.Cmp(start) <= 0 || newStart.Cmp(end) > 0 {
		return invalidArg("NewStartingHashKey must fall within the parent shard's hash-key range")
	}

	now := m.now()
	endSeq := sd.nextSeq()
	parent.closed = true
	parent.closedAt = now
	parent.shard.SequenceNumberRange.EndingSequenceNumber = endSeq

	startSeq := sd.nextSeq()
	left := m.childShard(len(sd.shards), start.String(),
		new(big.Int).Sub(newStart, big.NewInt(1)).String(), startSeq, shardToSplit, "", now)
	right := m.childShard(len(sd.shards)+1, newStart.String(), end.String(), startSeq, shardToSplit, "", now)
	sd.shards = append(sd.shards, left, right)

	return nil
}

// MergeShards merges two adjacent open shards into one child shard.
func (m *Mock) MergeShards(_ context.Context, name, arn, shardToMerge, adjacentShardToMerge string) error {
	sd, err := m.resolve(name, arn)
	if err != nil {
		return err
	}

	sd.mu.Lock()
	defer sd.mu.Unlock()

	a := findOpenShard(sd.shards, shardToMerge)
	b := findOpenShard(sd.shards, adjacentShardToMerge)

	if a == nil || b == nil {
		return errNotFound("both shards must exist and be open")
	}

	aEnd, _ := new(big.Int).SetString(a.shard.HashKeyRange.EndingHashKey, base10)
	bStart, _ := new(big.Int).SetString(b.shard.HashKeyRange.StartingHashKey, base10)

	if new(big.Int).Add(aEnd, big.NewInt(1)).Cmp(bStart) != 0 {
		return invalidArg("shards %q and %q are not adjacent", shardToMerge, adjacentShardToMerge)
	}

	now := m.now()
	endSeq := sd.nextSeq()

	for _, ss := range []*shardState{a, b} {
		ss.closed = true
		ss.closedAt = now
		ss.shard.SequenceNumberRange.EndingSequenceNumber = endSeq
	}

	child := m.childShard(len(sd.shards), a.shard.HashKeyRange.StartingHashKey,
		b.shard.HashKeyRange.EndingHashKey, sd.nextSeq(), shardToMerge, adjacentShardToMerge, now)
	sd.shards = append(sd.shards, child)

	return nil
}

func (*Mock) childShard(
	index int, startKey, endKey, startSeq, parent, adjacentParent string, createdAt time.Time,
) *shardState {
	return &shardState{
		createdAt: createdAt,
		shard: driver.Shard{
			ShardID:               fmt.Sprintf(shardIDFmt, index),
			ParentShardID:         parent,
			AdjacentParentShardID: adjacentParent,
			HashKeyRange:          driver.HashKeyRange{StartingHashKey: startKey, EndingHashKey: endKey},
			SequenceNumberRange:   driver.SequenceNumberRange{StartingSequenceNumber: startSeq},
		},
	}
}
