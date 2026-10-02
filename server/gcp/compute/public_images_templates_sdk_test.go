package compute_test

import (
	"context"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"
)

// TestSDKPublicImages proves the public image projects resolve: images.get by
// name and images.getFromFamily (newest non-deprecated), which the Terraform
// provider calls to expand source_image = "debian-cloud/debian-12".
func TestSDKPublicImages(t *testing.T) {
	ts := newGCPTestServer(t)
	client := newImagesSDKClient(t, ts)
	ctx := context.Background()

	families := []struct {
		project, family, want string
	}{
		{"debian-cloud", "debian-12", "debian-12-bookworm-v20250415"},
		{"ubuntu-os-cloud", "ubuntu-2204-lts", "ubuntu-2204-jammy-v20250415"},
		{"cos-cloud", "cos-stable", "cos-stable-117-18613-164-38"},
		{"rocky-linux-cloud", "rocky-linux-9", "rocky-linux-9-v20250415"},
		{"centos-cloud", "centos-stream-9", "centos-stream-9-v20250415"},
		{"windows-cloud", "windows-2022", "windows-server-2022-dc-v20250415"},
	}

	for _, tc := range families {
		t.Run(tc.family, func(t *testing.T) {
			img, err := client.GetFromFamily(ctx, &computepb.GetFromFamilyImageRequest{Project: tc.project, Family: tc.family})
			if err != nil {
				t.Fatalf("GetFromFamily: %v", err)
			}

			if img.GetName() != tc.want || img.GetStatus() != "READY" || img.GetFamily() != tc.family {
				t.Fatalf("got name=%q status=%q family=%q, want %q READY %q",
					img.GetName(), img.GetStatus(), img.GetFamily(), tc.want, tc.family)
			}

			byName, err := client.Get(ctx, &computepb.GetImageRequest{Project: tc.project, Image: tc.want})
			if err != nil || byName.GetSelfLink() != img.GetSelfLink() {
				t.Fatalf("Get by name: %v, selfLink %q vs %q", err, byName.GetSelfLink(), img.GetSelfLink())
			}
		})
	}

	old, err := client.Get(ctx, &computepb.GetImageRequest{Project: "debian-cloud", Image: "debian-12-bookworm-v20240910"})
	if err != nil || old.GetDeprecated().GetState() != "DEPRECATED" {
		t.Fatalf("old image: err=%v deprecated=%v", err, old.GetDeprecated())
	}

	if _, err := client.GetFromFamily(ctx, &computepb.GetFromFamilyImageRequest{Project: "debian-cloud", Family: "nope"}); err == nil {
		t.Fatal("unknown family: want 404")
	}

	if _, err := client.Insert(ctx, &computepb.InsertImageRequest{
		Project: "debian-cloud", ImageResource: &computepb.Image{Name: ptrStr("mine")},
	}); err == nil {
		t.Fatal("insert into a public project: want 403")
	}
}

// TestSDKUserImageFromFamily proves getFromFamily resolves a project's own
// images created with a family, returning the newest.
func TestSDKUserImageFromFamily(t *testing.T) {
	ts := newGCPTestServer(t)
	client := newImagesSDKClient(t, ts)
	ctx := context.Background()

	for _, name := range []string{"app-v1", "app-v2"} {
		op, err := client.Insert(ctx, &computepb.InsertImageRequest{
			Project: testProject, ImageResource: &computepb.Image{Name: ptrStr(name), Family: ptrStr("app")},
		})
		if err != nil {
			t.Fatalf("Insert %s: %v", name, err)
		}

		if err := op.Wait(ctx); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}

	img, err := client.GetFromFamily(ctx, &computepb.GetFromFamilyImageRequest{Project: testProject, Family: "app"})
	if err != nil {
		t.Fatalf("GetFromFamily: %v", err)
	}

	if img.GetName() != "app-v2" || img.GetFamily() != "app" {
		t.Fatalf("got %q family %q, want app-v2 app", img.GetName(), img.GetFamily())
	}
}

// TestSDKInstanceTemplateAndRegionalMIG proves global instance templates and
// regional managed instance groups round-trip, and that a template in use by a
// group cannot be deleted.
func TestSDKInstanceTemplateAndRegionalMIG(t *testing.T) {
	ts := newGCPTestServer(t)
	ctx := context.Background()
	opts := []option.ClientOption{option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client())}

	tmpl, err := gcpcompute.NewInstanceTemplatesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewInstanceTemplatesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = tmpl.Close() })

	migs, err := gcpcompute.NewRegionInstanceGroupManagersRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewRegionInstanceGroupManagersRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = migs.Close() })

	op, err := tmpl.Insert(ctx, &computepb.InsertInstanceTemplateRequest{
		Project: testProject,
		InstanceTemplateResource: &computepb.InstanceTemplate{
			Name: ptrStr("it1"),
			Properties: &computepb.InstanceProperties{
				MachineType: ptrStr("e2-medium"),
				Disks: []*computepb.AttachedDisk{{
					Boot: ptrBool(true),
					InitializeParams: &computepb.AttachedDiskInitializeParams{
						SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
					},
				}},
			},
		},
	})
	if err != nil {
		t.Fatalf("template Insert: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	got, err := tmpl.Get(ctx, &computepb.GetInstanceTemplateRequest{Project: testProject, InstanceTemplate: "it1"})
	if err != nil {
		t.Fatalf("template Get: %v", err)
	}

	if got.GetProperties().GetMachineType() != "e2-medium" ||
		got.GetProperties().GetDisks()[0].GetInitializeParams().GetSourceImage() != "projects/debian-cloud/global/images/family/debian-12" {
		t.Fatalf("properties not echoed: %+v", got.GetProperties())
	}

	if _, err := tmpl.Insert(ctx, &computepb.InsertInstanceTemplateRequest{
		Project: testProject, InstanceTemplateResource: &computepb.InstanceTemplate{Name: ptrStr("it1"), Properties: &computepb.InstanceProperties{}},
	}); err == nil {
		t.Fatal("duplicate template: want 409")
	}

	const region = "us-central1"

	mop, err := migs.Insert(ctx, &computepb.InsertRegionInstanceGroupManagerRequest{
		Project: testProject, Region: region,
		InstanceGroupManagerResource: &computepb.InstanceGroupManager{
			Name: ptrStr("rmig"), BaseInstanceName: ptrStr("web"), TargetSize: ptrInt32(2),
			Versions: []*computepb.InstanceGroupManagerVersion{{Name: ptrStr("primary"), InstanceTemplate: got.SelfLink}},
		},
	})
	if err != nil {
		t.Fatalf("regional MIG Insert: %v", err)
	}

	if err := mop.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	mig, err := migs.Get(ctx, &computepb.GetRegionInstanceGroupManagerRequest{Project: testProject, Region: region, InstanceGroupManager: "rmig"})
	if err != nil {
		t.Fatalf("regional MIG Get: %v", err)
	}

	if mig.GetTargetSize() != 2 || len(mig.GetVersions()) != 1 || mig.GetVersions()[0].GetInstanceTemplate() != got.GetSelfLink() ||
		mig.GetRegion() == "" || mig.GetZone() != "" {
		t.Fatalf("regional MIG read: size=%d versions=%v region=%q zone=%q",
			mig.GetTargetSize(), mig.GetVersions(), mig.GetRegion(), mig.GetZone())
	}

	if _, err := tmpl.Delete(ctx, &computepb.DeleteInstanceTemplateRequest{Project: testProject, InstanceTemplate: "it1"}); err == nil {
		t.Fatal("delete template in use: want 400")
	}

	dop, err := migs.Delete(ctx, &computepb.DeleteRegionInstanceGroupManagerRequest{Project: testProject, Region: region, InstanceGroupManager: "rmig"})
	if err != nil {
		t.Fatalf("regional MIG Delete: %v", err)
	}

	if err := dop.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if _, err := tmpl.Delete(ctx, &computepb.DeleteInstanceTemplateRequest{Project: testProject, InstanceTemplate: "it1"}); err != nil {
		t.Fatalf("template Delete: %v", err)
	}

	if _, err := tmpl.Get(ctx, &computepb.GetInstanceTemplateRequest{Project: testProject, InstanceTemplate: "it1"}); err == nil {
		t.Fatal("template Get after delete: want 404")
	}
}
