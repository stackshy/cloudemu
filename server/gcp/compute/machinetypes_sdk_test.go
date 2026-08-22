package compute_test

import (
	"context"
	"net/http/httptest"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// newSDKMachineTypesClient builds a real google-cloud-go MachineTypesRESTClient
// pointing at the given test server. gcloud/Terraform validate a machine type
// exists (machineTypes.get) or enumerate the catalog (machineTypes.list) before
// an instance create; this exercises those wire shapes end to end.
func newSDKMachineTypesClient(t *testing.T, ts *httptest.Server) *gcpcompute.MachineTypesClient {
	t.Helper()

	client, err := gcpcompute.NewMachineTypesRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewMachineTypesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func newMachineTypesTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return ts
}

func TestSDKMachineTypesGet(t *testing.T) {
	client := newSDKMachineTypesClient(t, newMachineTypesTestServer(t))
	ctx := context.Background()

	mt, err := client.Get(ctx, &computepb.GetMachineTypeRequest{
		Project:     testProject,
		Zone:        testZone,
		MachineType: "e2-medium",
	})
	if err != nil {
		t.Fatalf("MachineTypes.Get: %v", err)
	}

	if mt.GetName() != "e2-medium" {
		t.Fatalf("Name = %q, want e2-medium", mt.GetName())
	}

	if mt.GetGuestCpus() != 2 {
		t.Fatalf("GuestCpus = %d, want 2", mt.GetGuestCpus())
	}

	if mt.GetMemoryMb() != 4096 {
		t.Fatalf("MemoryMb = %d, want 4096", mt.GetMemoryMb())
	}

	if mt.GetSelfLink() == "" {
		t.Fatal("SelfLink is empty")
	}
}

func TestSDKMachineTypesGetUnknown(t *testing.T) {
	client := newSDKMachineTypesClient(t, newMachineTypesTestServer(t))

	if _, err := client.Get(context.Background(), &computepb.GetMachineTypeRequest{
		Project:     testProject,
		Zone:        testZone,
		MachineType: "nonexistent-type",
	}); err == nil {
		t.Fatal("MachineTypes.Get for unknown type returned nil error, want NotFound")
	}
}

func TestSDKMachineTypesList(t *testing.T) {
	client := newSDKMachineTypesClient(t, newMachineTypesTestServer(t))
	ctx := context.Background()

	it := client.List(ctx, &computepb.ListMachineTypesRequest{
		Project: testProject,
		Zone:    testZone,
	})

	found := false
	count := 0

	for {
		mt, err := it.Next()
		if err == iterator.Done {
			break
		}

		if err != nil {
			t.Fatalf("MachineTypes.List iterate: %v", err)
		}

		count++

		if mt.GetName() == "n1-standard-1" {
			found = true

			if mt.GetGuestCpus() != 1 {
				t.Fatalf("n1-standard-1 GuestCpus = %d, want 1", mt.GetGuestCpus())
			}
		}
	}

	if count == 0 {
		t.Fatal("MachineTypes.List returned no items")
	}

	if !found {
		t.Fatal("MachineTypes.List did not include n1-standard-1")
	}
}
