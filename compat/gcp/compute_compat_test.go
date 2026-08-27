package gcp

import (
	"context"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/internal/compat"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const (
	computeService = "compute"
	testZone       = "us-central1-a"
	diskSizeGb     = 64
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }
func i64Ptr(v int64) *int64   { return &v }

// TestGCEComputeCompat drives real cloud.google.com/go Compute REST clients
// (instances, disks, snapshots, images) against CloudEmu's in-process GCP wire
// server and records one compat result per portable compute op.
func TestGCEComputeCompat(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Compute: cloudP.GCE})

	ctx := context.Background()
	opts := []option.ClientOption{
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	}

	instances, err := gcpcompute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewInstancesRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = instances.Close() })

	disks, err := gcpcompute.NewDisksRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewDisksRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = disks.Close() })

	snapshots, err := gcpcompute.NewSnapshotsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewSnapshotsRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = snapshots.Close() })

	images, err := gcpcompute.NewImagesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewImagesRESTClient: %v", err)
	}
	t.Cleanup(func() { _ = images.Close() })

	project := compat.GCPProject

	// Instance lifecycle: RunInstances -> DescribeInstances -> Start/Stop/Reboot -> Terminate.
	sess.Op(computeService, "RunInstances", func() error {
		op, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
			Project: project,
			Zone:    testZone,
			InstanceResource: &computepb.Instance{
				Name:        strPtr("compat-vm"),
				MachineType: strPtr("zones/" + testZone + "/machineTypes/n1-standard-1"),
				Disks: []*computepb.AttachedDisk{{
					Boot:       boolPtr(true),
					AutoDelete: boolPtr(true),
					InitializeParams: &computepb.AttachedDiskInitializeParams{
						SourceImage: strPtr("projects/debian-cloud/global/images/family/debian-12"),
					},
				}},
				NetworkInterfaces: []*computepb.NetworkInterface{
					{Network: strPtr("global/networks/default")},
				},
			},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "DescribeInstances", func() error {
		got, err := instances.Get(ctx, &computepb.GetInstanceRequest{
			Project: project, Zone: testZone, Instance: "compat-vm",
		})
		if err != nil {
			return err
		}

		it := instances.List(ctx, &computepb.ListInstancesRequest{Project: project, Zone: testZone})
		for {
			inst, err := it.Next()
			if err != nil {
				break
			}

			if inst.GetName() == got.GetName() {
				return nil
			}
		}

		return errNotFound("instance compat-vm not in list")
	})

	sess.Op(computeService, "StopInstances", func() error {
		op, err := instances.Stop(ctx, &computepb.StopInstanceRequest{
			Project: project, Zone: testZone, Instance: "compat-vm",
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "StartInstances", func() error {
		op, err := instances.Start(ctx, &computepb.StartInstanceRequest{
			Project: project, Zone: testZone, Instance: "compat-vm",
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "RebootInstances", func() error {
		op, err := instances.Reset(ctx, &computepb.ResetInstanceRequest{
			Project: project, Zone: testZone, Instance: "compat-vm",
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "TerminateInstances", func() error {
		op, err := instances.Delete(ctx, &computepb.DeleteInstanceRequest{
			Project: project, Zone: testZone, Instance: "compat-vm",
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	// Volume (disk) lifecycle: CreateVolume -> DescribeVolumes -> DeleteVolume.
	sess.Op(computeService, "CreateVolume", func() error {
		op, err := disks.Insert(ctx, &computepb.InsertDiskRequest{
			Project: project, Zone: testZone,
			DiskResource: &computepb.Disk{
				Name:   strPtr("compat-disk"),
				SizeGb: i64Ptr(diskSizeGb),
				Type:   strPtr("zones/" + testZone + "/diskTypes/pd-standard"),
			},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "DescribeVolumes", func() error {
		got, err := disks.Get(ctx, &computepb.GetDiskRequest{
			Project: project, Zone: testZone, Disk: "compat-disk",
		})
		if err != nil {
			return err
		}

		if got.GetSizeGb() != diskSizeGb {
			return errNotFound("disk size mismatch")
		}

		it := disks.List(ctx, &computepb.ListDisksRequest{Project: project, Zone: testZone})
		for {
			d, err := it.Next()
			if err != nil {
				break
			}

			if d.GetName() == "compat-disk" {
				return nil
			}
		}

		return errNotFound("disk compat-disk not in list")
	})

	// Snapshot lifecycle: CreateSnapshot -> DescribeSnapshots -> DeleteSnapshot.
	sess.Op(computeService, "CreateSnapshot", func() error {
		op, err := snapshots.Insert(ctx, &computepb.InsertSnapshotRequest{
			Project: project,
			SnapshotResource: &computepb.Snapshot{
				Name:       strPtr("compat-snap"),
				SourceDisk: strPtr("projects/" + project + "/zones/" + testZone + "/disks/compat-disk"),
			},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "DescribeSnapshots", func() error {
		got, err := snapshots.Get(ctx, &computepb.GetSnapshotRequest{
			Project: project, Snapshot: "compat-snap",
		})
		if err != nil {
			return err
		}

		it := snapshots.List(ctx, &computepb.ListSnapshotsRequest{Project: project})
		for {
			s, err := it.Next()
			if err != nil {
				break
			}

			if s.GetName() == got.GetName() {
				return nil
			}
		}

		return errNotFound("snapshot compat-snap not in list")
	})

	sess.Op(computeService, "DeleteSnapshot", func() error {
		op, err := snapshots.Delete(ctx, &computepb.DeleteSnapshotRequest{
			Project: project, Snapshot: "compat-snap",
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	// Image lifecycle: CreateImage -> DescribeImages -> DeregisterImage.
	sess.Op(computeService, "CreateImage", func() error {
		op, err := images.Insert(ctx, &computepb.InsertImageRequest{
			Project: project,
			ImageResource: &computepb.Image{
				Name:       strPtr("compat-img"),
				SourceDisk: strPtr("zones/" + testZone + "/disks/compat-disk"),
			},
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	sess.Op(computeService, "DescribeImages", func() error {
		got, err := images.Get(ctx, &computepb.GetImageRequest{Project: project, Image: "compat-img"})
		if err != nil {
			return err
		}

		it := images.List(ctx, &computepb.ListImagesRequest{Project: project})
		for {
			img, err := it.Next()
			if err != nil {
				break
			}

			if img.GetName() == got.GetName() {
				return nil
			}
		}

		return errNotFound("image compat-img not in list")
	})

	sess.Op(computeService, "DeregisterImage", func() error {
		op, err := images.Delete(ctx, &computepb.DeleteImageRequest{Project: project, Image: "compat-img"})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})

	// DeleteVolume last, after snapshot/image sources no longer need it.
	sess.Op(computeService, "DeleteVolume", func() error {
		op, err := disks.Delete(ctx, &computepb.DeleteDiskRequest{
			Project: project, Zone: testZone, Disk: "compat-disk",
		})
		if err != nil {
			return err
		}

		return op.Wait(ctx)
	})
}

type notFoundError string

func (e notFoundError) Error() string { return string(e) }

func errNotFound(msg string) error {
	return notFoundError(strings.TrimSpace(msg))
}

// TestComputeOperationsGetUnknown404 proves a GET for an operation name that was
// never issued returns 404 NOT_FOUND, rather than a fabricated DONE. A genuine
// operation (minted by an instance delete) still resolves, so op.Wait succeeds.
func TestComputeOperationsGetUnknown404(t *testing.T) {
	cloudP := cloudemu.NewGCP()
	sess := compat.BootGCP(t, gcpserver.Drivers{Compute: cloudP.GCE})
	ctx := context.Background()

	opts := []option.ClientOption{
		option.WithEndpoint(sess.Endpoint()),
		option.WithoutAuthentication(),
		option.WithHTTPClient(sess.Transport()),
	}

	zoneOps, err := gcpcompute.NewZoneOperationsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewZoneOperationsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = zoneOps.Close() })

	globalOps, err := gcpcompute.NewGlobalOperationsRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewGlobalOperationsRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = globalOps.Close() })

	if _, err := zoneOps.Get(ctx, &computepb.GetZoneOperationRequest{
		Project: compat.GCPProject, Zone: testZone, Operation: "operation-does-not-exist",
	}); err == nil {
		t.Fatal("zoneOperations.get of a bogus operation returned success, want 404")
	}

	if _, err := globalOps.Get(ctx, &computepb.GetGlobalOperationRequest{
		Project: compat.GCPProject, Operation: "operation-bogus-global",
	}); err == nil {
		t.Fatal("globalOperations.get of a bogus operation returned success, want 404")
	}

	// A genuine operation (from an instance insert) must still resolve: op.Wait
	// polls zoneOperations.get and succeeds.
	instances, err := gcpcompute.NewInstancesRESTClient(ctx, opts...)
	if err != nil {
		t.Fatalf("NewInstancesRESTClient: %v", err)
	}

	t.Cleanup(func() { _ = instances.Close() })

	op, err := instances.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: compat.GCPProject,
		Zone:    testZone,
		InstanceResource: &computepb.Instance{
			Name:        strPtr("op-vm"),
			MachineType: strPtr("zones/" + testZone + "/machineTypes/n1-standard-1"),
			Disks: []*computepb.AttachedDisk{{
				Boot:       boolPtr(true),
				AutoDelete: boolPtr(true),
				InitializeParams: &computepb.AttachedDiskInitializeParams{
					SourceImage: strPtr("projects/debian-cloud/global/images/family/debian-12"),
				},
			}},
			NetworkInterfaces: []*computepb.NetworkInterface{{Network: strPtr("global/networks/default")}},
		},
	})
	if err != nil {
		t.Fatalf("insert instance: %v", err)
	}

	if werr := op.Wait(ctx); werr != nil {
		t.Fatalf("wait on genuine operation: %v", werr)
	}
}
