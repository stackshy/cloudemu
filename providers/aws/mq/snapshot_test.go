package mq_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/mq/driver"
)

// TestSnapshotRoundTripMQ proves a snapshot/restore round-trip preserves brokers
// and configurations under their original identities, so the computed brokerArn,
// created, instances and configuration revisions survive a restart transparently.
func TestSnapshotRoundTripMQ(t *testing.T) {
	ctx := context.Background()
	src := newMock()

	broker := createBroker(t, src)

	cfg, err := src.CreateConfiguration(ctx, &driver.CreateConfigurationInput{
		Name: "snap-cfg", EngineType: "ActiveMQ", EngineVersion: "5.17.6",
	})
	requireNoError(t, err)

	raw, err := src.Snapshot(ctx, true)
	requireNoError(t, err)

	dst := newMock()
	requireNoError(t, dst.Restore(ctx, raw))

	restored, err := dst.DescribeBroker(ctx, broker.BrokerID)
	requireNoError(t, err)

	if restored.BrokerArn != broker.BrokerArn || !restored.Created.Equal(broker.Created) {
		t.Fatal("broker identity drifted after restore")
	}

	if len(restored.Instances) != 1 || restored.Instances[0].ConsoleURL != broker.Instances[0].ConsoleURL {
		t.Fatal("broker instances not preserved after restore")
	}

	if len(restored.Users) != 1 || restored.Users[0].Username != "admin" {
		t.Fatalf("broker users not preserved: %+v", restored.Users)
	}

	rc, err := dst.DescribeConfiguration(ctx, cfg.ID)
	requireNoError(t, err)

	if rc.Arn != cfg.Arn || rc.LatestRevision().Revision != 1 {
		t.Fatal("configuration identity drifted after restore")
	}
}
