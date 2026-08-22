package compute_test

import (
	"context"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/config"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/compute"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dockerx"
	"github.com/stackshy/cloudemu/v2/contrib/dockerengine/internal/dtest"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	gceProject = "test-project"
	gceZone    = "us-central1-a"
	gceVMName  = "gce-boot-vm"
	// vmContainerPrefix namespaces the containers the compute engine creates; the
	// leak check counts containers with this prefix before and after the flow.
	vmContainerPrefix = "cloudemu-vm-"
)

// TestComputeGCESerialPortOutputE2E runs the exact flow a real user runs against
// GCP: create a Compute Engine instance (instances.insert) with a startup-script
// that echoes a marker, then read instances.getSerialPortOutput and assert the
// marker appears in the serial console contents — proving a real container
// actually ran the boot script — all against CloudEmu backed by a real Docker
// container (no cloud account), driven by the real cloud.google.com/go compute
// SDK. Then delete the instance and assert the backing container is torn down
// (no leak).
func TestComputeGCESerialPortOutputE2E(t *testing.T) {
	if !dtest.DockerUp() {
		t.Skip("docker daemon not available")
	}

	eng := compute.New()
	t.Cleanup(func() { _ = eng.Close() })

	cloud := cloudemu.NewGCP(config.WithComputeEngine(eng))
	ts := httptest.NewServer(gcpserver.New(gcpserver.DriversFrom(cloud)))
	t.Cleanup(ts.Close)

	ctx := context.Background()
	client := newInstancesClient(t, ts)

	before := countVMContainers(t)

	const marker = "cloudemu-gce-marker-42"

	script := "#!/bin/sh\necho " + marker

	// 1. Create the instance — exactly like `gcloud compute instances create`
	//    with --metadata startup-script=... (GCE carries the boot script as a
	//    metadata item keyed "startup-script").
	insertOp, err := client.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: gceProject,
		Zone:    gceZone,
		InstanceResource: &computepb.Instance{
			Name:        ptr(gceVMName),
			MachineType: ptr("zones/" + gceZone + "/machineTypes/n1-standard-1"),
			Disks: []*computepb.AttachedDisk{{
				Boot: ptr(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: ptr("projects/debian-cloud/global/images/family/debian-12"),
				},
			}},
			Metadata: &computepb.Metadata{
				Items: []*computepb.Items{{Key: ptr("startup-script"), Value: ptr(script)}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	// 2. Read the serial port output — the real container's boot-script output.
	serial, err := client.GetSerialPortOutput(ctx, &computepb.GetSerialPortOutputInstanceRequest{
		Project:  gceProject,
		Zone:     gceZone,
		Instance: gceVMName,
		Port:     ptr(int32(1)),
	})
	if err != nil {
		t.Fatalf("GetSerialPortOutput: %v", err)
	}

	if !strings.Contains(serial.GetContents(), marker) {
		t.Fatalf("serial port output missing marker %q: got %q", marker, serial.GetContents())
	}

	// 3. Delete the instance — the real container is torn down.
	delOp, err := client.Delete(ctx, &computepb.DeleteInstanceRequest{
		Project: gceProject, Zone: gceZone, Instance: gceVMName,
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Fatalf("Delete wait: %v", err)
	}

	// 4. The backing container count returns to baseline (no leaked container).
	if after := countVMContainers(t); after != before {
		t.Fatalf("container leak: before=%d after=%d", before, after)
	}
}

// newInstancesClient builds a real cloud.google.com/go InstancesRESTClient
// pointing at the plain-HTTP test server. Authentication is disabled — the
// handler ignores credential headers — and the endpoint/HTTP client are the
// test server's, so the SDK's REST transport talks to CloudEmu.
func newInstancesClient(t *testing.T, ts *httptest.Server) *gcpcompute.InstancesClient {
	t.Helper()

	client, err := gcpcompute.NewInstancesRESTClient(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewInstancesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

// countVMContainers reports how many engine-created containers exist, so the
// test can assert the flow leaks none.
func countVMContainers(t *testing.T) int {
	t.Helper()

	//nolint:gosec // first-party argv, never a shell string
	out, err := exec.Command(dockerx.Binary, "ps", "-aq", "--filter", "name="+vmContainerPrefix).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}

	return len(strings.Fields(string(out)))
}

func ptr[T any](v T) *T { return &v }
