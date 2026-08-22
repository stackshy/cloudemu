package gcp

import (
	"context"
	"fmt"
	"testing"

	"google.golang.org/api/option"
	redis "google.golang.org/api/redis/v1"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// TestGCPCacheCompat drives a Memorystore for Redis instance lifecycle through
// the real google.golang.org/api/redis/v1 client. Memorystore instances map
// onto the portable "cache" driver's cluster control plane, so operation names
// match ElastiCache's in docs/coverage/coverage.json. Only the instance
// control plane has a cloud-SDK surface; the driver's Redis data-plane methods
// (Set/Get/Incr/...) are unrouted gaps.
func TestGCPCacheCompat(t *testing.T) {
	provider := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Memorystore: provider.Memorystore})
	ctx := context.Background()

	svc, err := redis.NewService(ctx,
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	)
	if err != nil {
		t.Fatalf("redis.NewService: %v", err)
	}

	const (
		service  = "cache"
		location = "us-central1"
		instance = "compat-cache"
	)

	parent := "projects/" + compat.GCPProject + "/locations/" + location
	name := parent + "/instances/" + instance

	sess.Op(service, "CreateCache", func() error {
		op, err := svc.Projects.Locations.Instances.Create(parent, &redis.Instance{
			Tier:         "BASIC",
			MemorySizeGb: 1,
			Labels:       map[string]string{"env": "test"},
		}).InstanceId(instance).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("create operation not done: %+v", op)
		}

		return nil
	})

	sess.Op(service, "GetCache", func() error {
		got, err := svc.Projects.Locations.Instances.Get(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if got.Name != name {
			return fmt.Errorf("get name = %q, want %q", got.Name, name)
		}

		return nil
	})

	sess.Op(service, "ListCaches", func() error {
		list, err := svc.Projects.Locations.Instances.List(parent).Context(ctx).Do()
		if err != nil {
			return err
		}

		if len(list.Instances) != 1 || list.Instances[0].Name != name {
			return fmt.Errorf("list = %+v, want one instance %q", list.Instances, name)
		}

		return nil
	})

	sess.Op(service, "UpdateCache", func() error {
		_, err := svc.Projects.Locations.Instances.Patch(name, &redis.Instance{
			MemorySizeGb: 2,
		}).UpdateMask("memorySizeGb").Context(ctx).Do()

		return err
	})

	sess.Op(service, "DeleteCache", func() error {
		op, err := svc.Projects.Locations.Instances.Delete(name).Context(ctx).Do()
		if err != nil {
			return err
		}

		if !op.Done {
			return fmt.Errorf("delete operation not done: %+v", op)
		}

		return nil
	})
}
