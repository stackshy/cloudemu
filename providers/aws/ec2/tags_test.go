package ec2

import (
	"context"
	"testing"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// TestResourceTagsReturnsACopy covers ResourceTags on an instance and a volume
// (the two lock paths): it reports the current tags, the returned map is a copy,
// and an unknown id is NotFound. The EC2 CreateTags/DeleteTags handler reads it
// to check a batch before writing.
func TestResourceTagsReturnsACopy(t *testing.T) {
	ctx := context.Background()
	m := newTestMock()

	insts, err := m.RunInstances(ctx, driver.InstanceConfig{ImageID: "ami-123", InstanceType: "t2.micro"}, 1)
	if err != nil {
		t.Fatalf("RunInstances: %v", err)
	}

	vol, err := m.CreateVolume(ctx, driver.VolumeConfig{Size: 8, AvailabilityZone: "us-east-1a"})
	if err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}

	for _, id := range []string{insts[0].ID, vol.ID} {
		if err := m.TagResource(ctx, id, map[string]string{"env": "prod"}); err != nil {
			t.Fatalf("TagResource(%s): %v", id, err)
		}

		got, err := m.ResourceTags(ctx, id)
		if err != nil || got["env"] != "prod" {
			t.Fatalf("ResourceTags(%s) = %v, %v; want env=prod", id, got, err)
		}

		got["env"] = "mutated"

		if again, _ := m.ResourceTags(ctx, id); again["env"] != "prod" {
			t.Fatalf("ResourceTags(%s) returned the live map: %v", id, again)
		}
	}

	for _, id := range []string{"i-missing", "vol-missing", "x-unknown"} {
		if _, err := m.ResourceTags(ctx, id); !cerrors.IsNotFound(err) {
			t.Fatalf("ResourceTags(%s) = %v, want NotFound", id, err)
		}
	}
}
