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

// TestSDKPatchMIG proves instanceGroupManagers.patch (the Terraform update
// path) updates targetSize and the version's template on zonal and regional
// groups, and the template in-use check follows the new template.
func TestSDKPatchMIG(t *testing.T) {
	ts := newGCPTestServer(t)
	ctx := context.Background()
	opts := []option.ClientOption{option.WithEndpoint(ts.URL), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client())}

	tmpl, err := gcpcompute.NewInstanceTemplatesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewInstanceTemplatesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = tmpl.Close() })

	links := map[string]string{}

	for _, name := range []string{"old", "new"} {
		op, err := tmpl.Insert(ctx, &computepb.InsertInstanceTemplateRequest{
			Project: testProject,
			InstanceTemplateResource: &computepb.InstanceTemplate{
				Name: ptrStr(name), Properties: &computepb.InstanceProperties{MachineType: ptrStr("e2-small")},
			},
		})
		if err != nil {
			t.Fatalf("template Insert: %v", err)
		}

		if err := op.Wait(ctx); err != nil {
			t.Fatalf("Wait: %v", err)
		}

		got, err := tmpl.Get(ctx, &computepb.GetInstanceTemplateRequest{Project: testProject, InstanceTemplate: name})
		if err != nil {
			t.Fatalf("template Get: %v", err)
		}

		links[name] = got.GetSelfLink()
	}

	regional, err := gcpcompute.NewRegionInstanceGroupManagersRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewRegionInstanceGroupManagersRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = regional.Close() })

	zonal, err := gcpcompute.NewInstanceGroupManagersRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewInstanceGroupManagersRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = zonal.Close() })

	mig := func(size int32, tmplLink string) *computepb.InstanceGroupManager {
		return &computepb.InstanceGroupManager{
			Name: ptrStr("m"), BaseInstanceName: ptrStr("web"), TargetSize: ptrInt32(size),
			Versions: []*computepb.InstanceGroupManagerVersion{{Name: ptrStr("primary"), InstanceTemplate: ptrStr(tmplLink)}},
		}
	}

	type ops struct {
		insert func() error
		patch  func(*computepb.InstanceGroupManager) error
		get    func() (*computepb.InstanceGroupManager, error)
	}

	wait := func(op *gcpcompute.Operation, err error) error {
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	}

	cases := map[string]ops{
		"regional": {
			insert: func() error {
				op, err := regional.Insert(ctx, &computepb.InsertRegionInstanceGroupManagerRequest{
					Project: testProject, Region: "us-central1", InstanceGroupManagerResource: mig(2, links["old"]),
				})
				return wait(op, err)
			},
			patch: func(body *computepb.InstanceGroupManager) error {
				op, err := regional.Patch(ctx, &computepb.PatchRegionInstanceGroupManagerRequest{
					Project: testProject, Region: "us-central1", InstanceGroupManager: "m", InstanceGroupManagerResource: body,
				})
				return wait(op, err)
			},
			get: func() (*computepb.InstanceGroupManager, error) {
				return regional.Get(ctx, &computepb.GetRegionInstanceGroupManagerRequest{
					Project: testProject, Region: "us-central1", InstanceGroupManager: "m",
				})
			},
		},
		"zonal": {
			insert: func() error {
				op, err := zonal.Insert(ctx, &computepb.InsertInstanceGroupManagerRequest{
					Project: testProject, Zone: testZone, InstanceGroupManagerResource: mig(2, links["old"]),
				})
				return wait(op, err)
			},
			patch: func(body *computepb.InstanceGroupManager) error {
				op, err := zonal.Patch(ctx, &computepb.PatchInstanceGroupManagerRequest{
					Project: testProject, Zone: testZone, InstanceGroupManager: "m", InstanceGroupManagerResource: body,
				})
				return wait(op, err)
			},
			get: func() (*computepb.InstanceGroupManager, error) {
				return zonal.Get(ctx, &computepb.GetInstanceGroupManagerRequest{
					Project: testProject, Zone: testZone, InstanceGroupManager: "m",
				})
			},
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			if err := c.insert(); err != nil {
				t.Fatalf("insert: %v", err)
			}

			if err := c.patch(&computepb.InstanceGroupManager{TargetSize: ptrInt32(3)}); err != nil {
				t.Fatalf("patch targetSize: %v", err)
			}

			got, err := c.get()
			if err != nil || got.GetTargetSize() != 3 || got.GetVersions()[0].GetInstanceTemplate() != links["old"] {
				t.Fatalf("after size patch: err=%v size=%d versions=%v", err, got.GetTargetSize(), got.GetVersions())
			}

			body := mig(3, links["new"])
			body.TargetSize = nil

			if err := c.patch(body); err != nil {
				t.Fatalf("patch template: %v", err)
			}

			got, err = c.get()
			if err != nil || got.GetTargetSize() != 3 || got.GetVersions()[0].GetInstanceTemplate() != links["new"] ||
				got.GetBaseInstanceName() != "web" {
				t.Fatalf("after template patch: err=%v size=%d base=%q versions=%v",
					err, got.GetTargetSize(), got.GetBaseInstanceName(), got.GetVersions())
			}
		})
	}

	// Both groups now run "new", so "old" is free and "new" is in use.
	if _, err := tmpl.Delete(ctx, &computepb.DeleteInstanceTemplateRequest{Project: testProject, InstanceTemplate: "old"}); err != nil {
		t.Fatalf("delete old template: %v", err)
	}

	if _, err := tmpl.Delete(ctx, &computepb.DeleteInstanceTemplateRequest{Project: testProject, InstanceTemplate: "new"}); err == nil {
		t.Fatal("delete template in use after patch: want 400")
	}
}
