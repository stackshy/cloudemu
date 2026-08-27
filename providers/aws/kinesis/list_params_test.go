package kinesis_test

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/kinesis"
	"github.com/stackshy/cloudemu/v2/services/kinesis/driver"
)

func newClockMock(t *testing.T, clk config.Clock) *kinesis.Mock {
	t.Helper()

	return kinesis.New(config.NewOptions(config.WithClock(clk)))
}

func TestListStreamsExclusiveStartStreamName(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for _, n := range []string{"s-a", "s-b", "s-c", "s-d"} {
		if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: n, ShardCount: 1}); err != nil {
			t.Fatalf("CreateStream(%s): %v", n, err)
		}
	}

	out, err := m.ListStreams(ctx, "", "s-b", 0)
	if err != nil {
		t.Fatalf("ListStreams: %v", err)
	}

	if got := out.StreamNames; len(got) != 2 || got[0] != "s-c" || got[1] != "s-d" {
		t.Fatalf("ExclusiveStartStreamName=s-b returned %v, want [s-c s-d]", got)
	}
}

func TestListStreamsNextTokenStillPaginates(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	for _, n := range []string{"s-a", "s-b", "s-c", "s-d"} {
		if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: n, ShardCount: 1}); err != nil {
			t.Fatalf("CreateStream(%s): %v", n, err)
		}
	}

	seen := map[string]int{}
	token := ""

	for i := 0; i < 10; i++ {
		out, err := m.ListStreams(ctx, token, "", 2)
		if err != nil {
			t.Fatalf("ListStreams: %v", err)
		}

		for _, n := range out.StreamNames {
			seen[n]++
		}

		if !out.HasMoreStreams {
			break
		}

		token = out.NextToken
	}

	if len(seen) != 4 {
		t.Fatalf("distinct streams walked = %d, want 4", len(seen))
	}

	for n, c := range seen {
		if c != 1 {
			t.Fatalf("stream %s returned %d times, want 1 (duplicate)", n, c)
		}
	}
}

// mergedStream creates a 2-shard stream and merges its two shards, leaving 2
// closed parents and 1 open child. The clock is advanced around each phase so
// the shards carry distinct creation/close timestamps.
func mergedStream(t *testing.T, m *kinesis.Mock, clk *config.FakeClock) {
	t.Helper()

	ctx := context.Background()
	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 2}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	clk.Advance(time.Minute)

	if err := m.MergeShards(ctx, "s", "", "shardId-000000000000", "shardId-000000000001"); err != nil {
		t.Fatalf("MergeShards: %v", err)
	}

	clk.Advance(time.Minute)
}

func listShards(t *testing.T, m *kinesis.Mock, f *driver.ShardFilter) []driver.Shard {
	t.Helper()

	out, err := m.ListShards(context.Background(), driver.ListShardsInput{StreamName: "s", ShardFilter: f})
	if err != nil {
		t.Fatalf("ListShards: %v", err)
	}

	return out.Shards
}

func openShards(shards []driver.Shard) []driver.Shard {
	var open []driver.Shard

	for _, s := range shards {
		if s.SequenceNumberRange.EndingSequenceNumber == "" {
			open = append(open, s)
		}
	}

	return open
}

func TestListShardsShardFilterAtLatest(t *testing.T) {
	clk := config.NewFakeClock(time.Unix(1_000_000, 0).UTC())
	m := newClockMock(t, clk)
	mergedStream(t, m, clk)

	if all := listShards(t, m, nil); len(all) != 3 {
		t.Fatalf("no filter returned %d shards, want 3", len(all))
	}

	got := listShards(t, m, &driver.ShardFilter{Type: driver.ShardFilterAtLatest})
	if len(got) != 1 {
		t.Fatalf("AT_LATEST returned %d shards, want 1 (only the open child)", len(got))
	}

	if got[0].SequenceNumberRange.EndingSequenceNumber != "" {
		t.Fatalf("AT_LATEST returned a closed shard %s", got[0].ShardID)
	}
}

func TestListShardsShardFilterTypes(t *testing.T) {
	base := time.Unix(1_000_000, 0).UTC()
	clk := config.NewFakeClock(base)
	m := newClockMock(t, clk)
	mergedStream(t, m, clk) // create @base, merge @base+1m, now @base+2m

	// AT_TRIM_HORIZON: only shards open at the trim horizon (stream creation) —
	// the two originals, not the later child.
	trim := listShards(t, m, &driver.ShardFilter{Type: driver.ShardFilterAtTrimHorizon})
	if len(trim) != 2 {
		t.Fatalf("AT_TRIM_HORIZON returned %d shards, want 2", len(trim))
	}

	// FROM_TRIM_HORIZON (default): the whole set.
	if all := listShards(t, m, &driver.ShardFilter{Type: driver.ShardFilterFromTrimHorizon}); len(all) != 3 {
		t.Fatalf("FROM_TRIM_HORIZON returned %d shards, want 3", len(all))
	}

	// AFTER_SHARD_ID: shards strictly after the given id.
	after := listShards(t, m, &driver.ShardFilter{
		Type: driver.ShardFilterAfterShardID, ShardID: "shardId-000000000000",
	})
	if len(after) != 2 {
		t.Fatalf("AFTER_SHARD_ID returned %d shards, want 2", len(after))
	}

	for _, s := range after {
		if s.ShardID == "shardId-000000000000" {
			t.Fatal("AFTER_SHARD_ID included the exclusive-start shard")
		}
	}

	// AT_TIMESTAMP after the merge: only the open child (parents already closed).
	atTS := listShards(t, m, &driver.ShardFilter{
		Type: driver.ShardFilterAtTimestamp, Timestamp: base.Add(2 * time.Minute),
	})
	if len(atTS) != 1 || len(openShards(atTS)) != 1 {
		t.Fatalf("AT_TIMESTAMP(now) returned %d shards, want 1 open child", len(atTS))
	}

	// FROM_TIMESTAMP after the merge: open shards plus closed shards ending at/after
	// ts; the parents closed before ts, so only the child qualifies.
	fromTS := listShards(t, m, &driver.ShardFilter{
		Type: driver.ShardFilterFromTimestamp, Timestamp: base.Add(2 * time.Minute),
	})
	if len(fromTS) != 1 {
		t.Fatalf("FROM_TIMESTAMP(now) returned %d shards, want 1", len(fromTS))
	}

	// FROM_TIMESTAMP clamped below the trim horizon returns the whole set.
	early := listShards(t, m, &driver.ShardFilter{
		Type: driver.ShardFilterFromTimestamp, Timestamp: base.Add(-time.Hour),
	})
	if len(early) != 3 {
		t.Fatalf("FROM_TIMESTAMP(pre-horizon) returned %d shards, want 3", len(early))
	}
}

func TestListShardsShardFilterTimestampRequired(t *testing.T) {
	clk := config.NewFakeClock(time.Unix(1_000_000, 0).UTC())
	m := newClockMock(t, clk)

	if err := m.CreateStream(context.Background(), driver.CreateStreamInput{StreamName: "s", ShardCount: 1}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if _, err := m.ListShards(context.Background(), driver.ListShardsInput{
		StreamName: "s", ShardFilter: &driver.ShardFilter{Type: driver.ShardFilterAtTimestamp},
	}); err == nil {
		t.Fatal("AT_TIMESTAMP without a Timestamp should fail")
	}
}

func TestCreateStreamOnDemandRejectsShardCount(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	err := m.CreateStream(ctx, driver.CreateStreamInput{
		StreamName: "od-bad", ShardCount: 2, StreamMode: driver.ModeOnDemand,
	})
	if err == nil {
		t.Fatal("ON_DEMAND with an explicit ShardCount should fail")
	}

	if err := m.CreateStream(ctx, driver.CreateStreamInput{
		StreamName: "od-ok", StreamMode: driver.ModeOnDemand,
	}); err != nil {
		t.Fatalf("ON_DEMAND without ShardCount: %v", err)
	}

	desc, err := m.DescribeStream(ctx, "od-ok", "", 0, "")
	if err != nil {
		t.Fatalf("DescribeStream: %v", err)
	}

	if len(desc.Shards) != 4 {
		t.Fatalf("on-demand stream started with %d shards, want 4", len(desc.Shards))
	}
}

func TestUpdateShardCountBounds(t *testing.T) {
	ctx := context.Background()
	m := newMock(t)

	if err := m.CreateStream(ctx, driver.CreateStreamInput{StreamName: "s", ShardCount: 2}); err != nil {
		t.Fatalf("CreateStream: %v", err)
	}

	if _, _, err := m.UpdateShardCount(ctx, "s", "", 0, ""); err == nil {
		t.Fatal("TargetShardCount 0 should fail")
	}

	if _, _, err := m.UpdateShardCount(ctx, "s", "", 5, ""); err == nil {
		t.Fatal("scaling 2 -> 5 (more than 2x) should fail")
	}

	if _, _, err := m.UpdateShardCount(ctx, "s", "", 4, ""); err != nil {
		t.Fatalf("scaling 2 -> 4 (exactly 2x): %v", err)
	}

	// Current open count is now 4; halving to 1 (below half) must fail, to 2 is ok.
	if _, _, err := m.UpdateShardCount(ctx, "s", "", 1, ""); err == nil {
		t.Fatal("scaling 4 -> 1 (below half) should fail")
	}

	if _, _, err := m.UpdateShardCount(ctx, "s", "", 2, ""); err != nil {
		t.Fatalf("scaling 4 -> 2 (exactly half): %v", err)
	}
}
