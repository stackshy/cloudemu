package kafka_test

import (
	"context"
	"testing"

	"github.com/stackshy/cloudemu/v2/services/kafka/driver"
)

// TestSnapshotRoundTripKafka proves a snapshot/restore round-trip preserves a
// cluster and — critically — rebuilds the operations index so a
// DescribeClusterOperation still resolves to the restored cluster's operation.
func TestSnapshotRoundTripKafka(t *testing.T) {
	ctx := context.Background()
	src := newMock(t)

	out, err := src.CreateCluster(ctx, driver.CreateClusterInput{
		ClusterName:         "my-cluster",
		KafkaVersion:        "3.6.0",
		NumberOfBrokerNodes: 2,
		BrokerNodeGroupInfo: &driver.BrokerNodeGroupInfo{
			ClientSubnets: []string{"subnet-1", "subnet-2"},
			InstanceType:  "kafka.m5.large",
		},
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	op, err := src.UpdateBrokerCount(ctx, out.ClusterARN, out.CurrentVersion, 4)
	if err != nil {
		t.Fatalf("UpdateBrokerCount: %v", err)
	}

	raw, err := src.Snapshot(ctx, true)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}

	dst := newMock(t)
	if err := dst.Restore(ctx, raw); err != nil {
		t.Fatalf("restore: %v", err)
	}

	got, err := dst.DescribeCluster(ctx, out.ClusterARN)
	if err != nil {
		t.Fatalf("DescribeCluster: %v", err)
	}

	if got.ClusterName != "my-cluster" || got.Tags["env"] != "prod" {
		t.Fatalf("restored cluster = %+v", got)
	}

	// The operations index shares pointers with the clusters store; confirm it
	// was rebuilt so the operation ARN still resolves.
	gotOp, err := dst.DescribeClusterOperation(ctx, op.OperationARN)
	if err != nil {
		t.Fatalf("DescribeClusterOperation: %v", err)
	}

	if gotOp.OperationARN != op.OperationARN || gotOp.ClusterARN != out.ClusterARN {
		t.Fatalf("restored operation = %+v", gotOp)
	}
}
