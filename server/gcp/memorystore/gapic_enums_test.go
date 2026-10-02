package memorystore_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	gapic "cloud.google.com/go/redis/apiv1"
	"cloud.google.com/go/redis/apiv1/redispb"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

func newGAPIC(t *testing.T) *gapic.CloudRedisClient {
	t.Helper()

	ts := httptest.NewServer(gcpserver.NewFromProvider(cloudemu.NewGCP()))
	t.Cleanup(ts.Close)

	c, err := gapic.NewCloudRedisRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewCloudRedisRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = c.Close() })

	return c
}

func assertInstanceEnums(t *testing.T, step string, in *redispb.Instance) {
	t.Helper()

	checks := []struct {
		field     string
		got, want any
	}{
		{field: "tier", got: in.GetTier(), want: redispb.Instance_STANDARD_HA},
		{field: "connectMode", got: in.GetConnectMode(), want: redispb.Instance_PRIVATE_SERVICE_ACCESS},
		{field: "transitEncryptionMode", got: in.GetTransitEncryptionMode(), want: redispb.Instance_SERVER_AUTHENTICATION},
		{field: "readReplicasMode", got: in.GetReadReplicasMode(), want: redispb.Instance_READ_REPLICAS_ENABLED},
		{field: "state", got: in.GetState(), want: redispb.Instance_READY},
	}

	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s: %s=%v want %v", step, c.field, c.got, c.want)
		}
	}
}

// TestGAPICInstanceNumericEnumsLifecycle is GMEM-03: the redis/apiv1 REST
// client sends tier, connectMode, transitEncryptionMode and readReplicasMode
// as numbers. Create used to fail with "cannot unmarshal number into Go struct
// field instanceJSON.tier of type string". The update resends the fetched
// instance, so the output-only state goes back as a number too.
func TestGAPICInstanceNumericEnumsLifecycle(t *testing.T) {
	c := newGAPIC(t)
	ctx := context.Background()

	op, err := c.CreateInstance(ctx, &redispb.CreateInstanceRequest{
		Parent:     parent(),
		InstanceId: "enum-cache",
		Instance: &redispb.Instance{
			DisplayName:           "v1",
			Tier:                  redispb.Instance_STANDARD_HA,
			MemorySizeGb:          1,
			ConnectMode:           redispb.Instance_PRIVATE_SERVICE_ACCESS,
			TransitEncryptionMode: redispb.Instance_SERVER_AUTHENTICATION,
			ReadReplicasMode:      redispb.Instance_READ_REPLICAS_ENABLED,
			ReplicaCount:          1,
		},
	})
	if err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}

	created, err := op.Wait(ctx)
	if err != nil {
		t.Fatalf("CreateInstance Wait: %v", err)
	}

	assertInstanceEnums(t, "create", created)

	name := instanceName("enum-cache")

	got, err := c.GetInstance(ctx, &redispb.GetInstanceRequest{Name: name})
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	assertInstanceEnums(t, "get", got)

	assertInstanceListed(t, c, name)

	got.DisplayName = "v2"

	uop, err := c.UpdateInstance(ctx, &redispb.UpdateInstanceRequest{
		Instance:   got,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
	})
	if err != nil {
		t.Fatalf("UpdateInstance: %v", err)
	}

	updated, err := uop.Wait(ctx)
	if err != nil {
		t.Fatalf("UpdateInstance Wait: %v", err)
	}

	if updated.GetDisplayName() != "v2" {
		t.Errorf("update: displayName=%q want v2", updated.GetDisplayName())
	}

	assertInstanceEnums(t, "update", updated)

	dop, err := c.DeleteInstance(ctx, &redispb.DeleteInstanceRequest{Name: name})
	if err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}

	if err := dop.Wait(ctx); err != nil {
		t.Fatalf("DeleteInstance Wait: %v", err)
	}

	_, err = c.GetInstance(ctx, &redispb.GetInstanceRequest{Name: name})

	var apiErr *googleapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != http.StatusNotFound {
		t.Fatalf("GetInstance after delete: err=%v want 404", err)
	}
}

func assertInstanceListed(t *testing.T, c *gapic.CloudRedisClient, name string) {
	t.Helper()

	it := c.ListInstances(context.Background(), &redispb.ListInstancesRequest{Parent: parent()})

	for {
		in, err := it.Next()
		if errors.Is(err, iterator.Done) {
			t.Fatalf("ListInstances: %s not listed", name)
		}

		if err != nil {
			t.Fatalf("ListInstances: %v", err)
		}

		if in.GetName() == name {
			assertInstanceEnums(t, "list", in)
			return
		}
	}
}
