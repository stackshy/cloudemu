package cloudfunctions_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
	"google.golang.org/api/cloudfunctions/v1"
	"google.golang.org/api/option"
)

func newGCPSDKService(t *testing.T) *cloudfunctions.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := cloudfunctions.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	return svc
}

func TestSDKCloudFunctionsCreateGetListDelete(t *testing.T) {
	svc := newGCPSDKService(t)
	ctx := context.Background()

	parent := "projects/demo/locations/us-central1"

	op, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:              parent + "/functions/hello",
		Runtime:           "go121",
		EntryPoint:        "Hello",
		AvailableMemoryMb: 128,
		Timeout:           "60s",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if !op.Done {
		t.Fatal("Create operation not done")
	}

	got, err := svc.Projects.Locations.Functions.Get(parent + "/functions/hello").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.Runtime != "go121" {
		t.Fatalf("Runtime = %q, want go121", got.Runtime)
	}

	if got.EntryPoint != "Hello" {
		t.Fatalf("EntryPoint = %q, want Hello", got.EntryPoint)
	}

	if got.AvailableMemoryMb != 128 {
		t.Fatalf("AvailableMemoryMb = %d, want 128", got.AvailableMemoryMb)
	}

	if !strings.HasSuffix(got.Name, "/functions/hello") {
		t.Fatalf("Name = %q, want suffix /functions/hello", got.Name)
	}

	listResp, err := svc.Projects.Locations.Functions.List(parent).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(listResp.Functions) != 1 {
		t.Fatalf("listed %d functions, want 1", len(listResp.Functions))
	}

	delOp, err := svc.Projects.Locations.Functions.Delete(parent + "/functions/hello").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if !delOp.Done {
		t.Fatal("Delete operation not done")
	}

	if _, err := svc.Projects.Locations.Functions.Get(parent + "/functions/hello").
		Context(ctx).Do(); err == nil {
		t.Fatal("post-delete Get returned nil error, want NotFound")
	}
}

func TestSDKCloudFunctionsCall(t *testing.T) {
	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudFunctions: cloud.CloudFunctions})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := cloudfunctions.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	parent := "projects/demo/locations/us-central1"

	if _, err := svc.Projects.Locations.Functions.Create(parent, &cloudfunctions.CloudFunction{
		Name:    parent + "/functions/echo",
		Runtime: "go121",
	}).Context(context.Background()).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	cloud.CloudFunctions.RegisterHandler("echo", func(_ context.Context, payload []byte) ([]byte, error) {
		return []byte("got:" + string(payload)), nil
	})

	resp, err := svc.Projects.Locations.Functions.Call(
		parent+"/functions/echo",
		&cloudfunctions.CallFunctionRequest{Data: "hello"},
	).Context(context.Background()).Do()
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	if resp.Result != "got:hello" {
		t.Fatalf("Result = %q, want got:hello", resp.Result)
	}

	if resp.Error != "" {
		t.Fatalf("Error = %q, want empty", resp.Error)
	}
}
