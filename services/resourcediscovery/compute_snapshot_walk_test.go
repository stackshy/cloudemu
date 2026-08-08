package resourcediscovery

import (
	"context"
	"testing"
	"time"

	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/providers/aws/ec2"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

func TestWalkComputeSnapshot(t *testing.T) {
	ctx := context.Background()
	opts := config.NewOptions(config.WithClock(config.NewFakeClock(time.Unix(0, 0))))
	c := ec2.New(opts)

	vol, err := c.CreateVolume(ctx, computedriver.VolumeConfig{Size: 10, AvailabilityZone: "us-east-1a"})
	if err != nil {
		t.Fatalf("create volume: %v", err)
	}

	if _, err := c.CreateSnapshot(ctx, computedriver.SnapshotConfig{VolumeID: vol.ID}); err != nil {
		t.Fatalf("create snapshot: %v", err)
	}

	eng := New(ProviderAWS, "123456789012", "us-east-1", &Drivers{Compute: c})

	res, err := eng.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}

	var found int
	for i := range res {
		if res[i].Service == ServiceCompute && res[i].Type == TypeSnapshot {
			found++
		}
	}

	if found != 1 {
		t.Fatalf("expected 1 discovered snapshot, got %d (of %d resources)", found, len(res))
	}
}
