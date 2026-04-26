package compute_test

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu"
	gcpserver "github.com/stackshy/cloudemu/server/gcp"
)

func newImagesSDKClient(t *testing.T, ts *httptest.Server) *gcpcompute.ImagesClient {
	t.Helper()

	ctx := context.Background()

	client, err := gcpcompute.NewImagesRESTClient(ctx,
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("NewImagesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	return client
}

func TestSDKImageRoundTripGCP(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	ctx := context.Background()

	// Image creation in our mock requires an existing instance, so create
	// one via the InstancesRESTClient first.
	instClient := newSDKInstancesClient(t, ts)

	instOp, err := instClient.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone,
		InstanceResource: &computepb.Instance{
			Name:        ptrStr("src-vm"),
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			Disks: []*computepb.AttachedDisk{{
				Boot:       ptrBool(true),
				AutoDelete: ptrBool(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("instance Insert: %v", err)
	}

	if err := instOp.Wait(ctx); err != nil {
		t.Fatalf("instance wait: %v", err)
	}

	imgClient := newImagesSDKClient(t, ts)

	insertOp, err := imgClient.Insert(ctx, &computepb.InsertImageRequest{
		Project: testProject,
		ImageResource: &computepb.Image{
			Name: ptrStr("img-1"),
		},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := insertOp.Wait(ctx); err != nil {
		t.Fatalf("Insert wait: %v", err)
	}

	got, err := imgClient.Get(ctx, &computepb.GetImageRequest{
		Project: testProject, Image: "img-1",
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.GetName() != "img-1" {
		t.Errorf("name=%s want img-1", got.GetName())
	}

	if !strings.HasSuffix(got.GetSelfLink(), "/global/images/img-1") {
		t.Errorf("selfLink=%s", got.GetSelfLink())
	}

	it := imgClient.List(ctx, &computepb.ListImagesRequest{Project: testProject})

	found := false
	for {
		im, err := it.Next()
		if err != nil {
			break
		}

		if im.GetName() == "img-1" {
			found = true
		}
	}

	if !found {
		t.Error("List did not return img-1")
	}

	delOp, err := imgClient.Delete(ctx, &computepb.DeleteImageRequest{
		Project: testProject, Image: "img-1",
	})
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if err := delOp.Wait(ctx); err != nil {
		t.Errorf("Delete wait: %v", err)
	}
}
