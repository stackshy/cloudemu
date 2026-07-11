package memorystore_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	redis "google.golang.org/api/redis/v1"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

const (
	testProject  = "demo"
	testLocation = "us-central1"
)

func newRedisService(t *testing.T) *redis.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{
		Memorystore: cloud.Memorystore,
		// Firestore also wired so we exercise dispatch precedence: Memorystore's
		// instances/operations guard must win over Firestore's /v1/projects/
		// prefix match.
		Firestore: cloud.Firestore,
	})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := redis.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("redis.NewService: %v", err)
	}

	return svc
}

func parent() string {
	return "projects/" + testProject + "/locations/" + testLocation
}

func instanceName(id string) string {
	return parent() + "/instances/" + id
}

func TestSDKMemorystoreLifecycle(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	op, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
		Labels:       map[string]string{"env": "test"},
	}).InstanceId("cache1").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Create: %v", err)
	}

	if !op.Done {
		t.Fatalf("Create operation not done: %+v", op)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("cache1")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Get: %v", err)
	}

	if got.Name != instanceName("cache1") {
		t.Fatalf("Get name = %q, want %q", got.Name, instanceName("cache1"))
	}

	if got.State != "READY" {
		t.Fatalf("Get state = %q, want READY", got.State)
	}

	if got.Host == "" || got.Port == 0 {
		t.Fatalf("expected host/port to be set, got host=%q port=%d", got.Host, got.Port)
	}

	if got.Labels["env"] != "test" {
		t.Fatalf("labels = %v, want env=test", got.Labels)
	}

	list, err := svc.Projects.Locations.Instances.List(parent()).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.List: %v", err)
	}

	if len(list.Instances) != 1 || list.Instances[0].Name != instanceName("cache1") {
		t.Fatalf("List = %+v, want one instance %q", list.Instances, instanceName("cache1"))
	}

	delOp, err := svc.Projects.Locations.Instances.Delete(instanceName("cache1")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Instances.Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatalf("Delete operation not done: %+v", delOp)
	}

	_, err = svc.Projects.Locations.Instances.Get(instanceName("cache1")).Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKMemorystoreNotFound(t *testing.T) {
	svc := newRedisService(t)

	_, err := svc.Projects.Locations.Instances.Get(instanceName("missing")).
		Context(context.Background()).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}
}
