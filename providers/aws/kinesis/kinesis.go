// Package kinesis provides an in-memory mock implementation of AWS Kinesis Data
// Streams: streams partitioned into shards by MD5 hash-key range, records with
// per-stream monotonic sequence numbers, opaque shard iterators, enhanced
// fan-out consumers, encryption, tags, and resource policies.
package kinesis

import (
	"crypto/md5" //nolint:gosec // MD5 is the Kinesis partition-key hash, not used for security
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/internal/idgen"
	"github.com/stackshy/cloudemu/v2/internal/memstore"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

// Compile-time check that Mock implements driver.Kinesis.
var _ driver.Kinesis = (*Mock)(nil)

const (
	defaultRetentionHours = 24
	minRetentionHours     = 24
	maxRetentionHours     = 8760
	// seqPrefix pads sequence numbers to a fixed 56-char width so string and
	// numeric ordering agree; the trailing counter is zero-padded to seqWidth
	// digits, wide enough to hold any int64 counter without collision.
	seqPrefix   = "495900000000000000000000000000000000"
	seqWidth    = 20
	shardIDFmt  = "shardId-%012d"
	hashKeyBits = 128
	base10      = 10
	// onDemandStartShards is the shard count an on-demand stream starts with.
	onDemandStartShards = 4
	// maxTargetShardCount / shardScaleFactor bound a single UpdateShardCount call:
	// AWS caps the target at 10000 and permits scaling by at most a factor of two.
	maxTargetShardCount = 10000
	shardScaleFactor    = 2
)

// shardState is a shard plus its stored records and open/closed flag. createdAt
// and closedAt (zero while open) time-stamp the shard's lifetime so ListShards
// can honor the timestamp-based ShardFilter types.
type shardState struct {
	shard     driver.Shard
	records   []driver.Record
	closed    bool
	createdAt time.Time
	closedAt  time.Time
}

// streamData is the full server-side state of a stream.
type streamData struct {
	desc      driver.StreamDescription
	shards    []*shardState
	consumers map[string]*driver.Consumer
	tags      map[string]string
	policy    string
	maxRecKiB int32
	warmMiBps int32
	seq       int64 // per-stream monotonic sequence counter
	mu        sync.RWMutex
}

// Mock is an in-memory implementation of AWS Kinesis Data Streams.
type Mock struct {
	// streams is keyed by stream name; arnToName resolves an ARN to its name.
	streams   *memstore.Store[*streamData]
	arnToName *memstore.Store[string]

	settingsMu sync.RWMutex
	settings   driver.AccountSettings

	opts *config.Options
}

// New creates a new Kinesis mock with the given configuration options.
func New(opts *config.Options) *Mock {
	return &Mock{
		streams:   memstore.New[*streamData](),
		arnToName: memstore.New[string](),
		settings:  driver.AccountSettings{CommitmentStatus: "DISABLED"},
		opts:      opts,
	}
}

func (m *Mock) streamARN(name string) string {
	return idgen.AWSARN("kinesis", m.opts.Region, m.opts.AccountID, "stream/"+name)
}

func (m *Mock) consumerARN(streamName, consumerName string) string {
	return idgen.AWSARN("kinesis", m.opts.Region, m.opts.AccountID,
		"stream/"+streamName+"/consumer/"+consumerName)
}

func (m *Mock) now() time.Time {
	return m.opts.Clock.Now().UTC()
}

// resolve finds a stream by name or ARN (name takes precedence when both set).
func (m *Mock) resolve(name, arn string) (*streamData, error) {
	if name == "" && arn != "" {
		n, ok := m.arnToName.Get(arn)
		if !ok {
			return nil, errNotFoundName(arn)
		}

		name = n
	}

	sd, ok := m.streams.Get(name)
	if !ok {
		return nil, errNotFoundName(name)
	}

	return sd, nil
}

// nextSeq returns the next monotonic sequence number string for a stream.
func (sd *streamData) nextSeq() string {
	n := atomic.AddInt64(&sd.seq, 1)

	return formatSeq(n)
}

func formatSeq(n int64) string {
	return seqPrefix + fmt.Sprintf("%0*d", seqWidth, n)
}

// hashKeyOf returns the 128-bit MD5 hash of a partition key as a decimal string.
func hashKeyOf(partitionKey string) string {
	sum := md5.Sum([]byte(partitionKey)) //nolint:gosec // partition-key hashing, not security
	return new(big.Int).SetBytes(sum[:]).String()
}

// maxHashKey returns 2^128 - 1 as a big.Int.
func maxHashKey() *big.Int {
	return new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), hashKeyBits), big.NewInt(1))
}

func copyTags(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}

	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}

	return out
}
