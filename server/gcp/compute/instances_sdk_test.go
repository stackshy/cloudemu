package compute_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gcpcompute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const altZone = "europe-west1-b"

// newInstancesEnv spins up a GCP wire server backed by a fresh GCE mock and a
// real cloud.google.com/go InstancesClient pointed at it.
func newInstancesEnv(t *testing.T) (*gcpcompute.InstancesClient, *httptest.Server, context.Context) {
	t.Helper()

	cloudP := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{Compute: cloudP.GCE})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	return newSDKInstancesClient(t, ts), ts, context.Background()
}

func mustInsert(t *testing.T, client *gcpcompute.InstancesClient, zone string, inst *computepb.Instance) {
	t.Helper()

	op, err := client.Insert(context.Background(), &computepb.InsertInstanceRequest{
		Project: testProject, Zone: zone, InstanceResource: inst,
	})
	if err != nil {
		t.Fatalf("Insert %s: %v", inst.GetName(), err)
	}

	if err := op.Wait(context.Background()); err != nil {
		t.Fatalf("Insert %s wait: %v", inst.GetName(), err)
	}
}

func mustGet(t *testing.T, client *gcpcompute.InstancesClient, zone, name string) *computepb.Instance {
	t.Helper()

	got, err := client.Get(context.Background(), &computepb.GetInstanceRequest{
		Project: testProject, Zone: zone, Instance: name,
	})
	if err != nil {
		t.Fatalf("Get %s: %v", name, err)
	}

	return got
}

// TestSDKGCEInstanceDisksRoundTrip covers finding #1: attached disks[] must be
// echoed with the boot disk (deviceName/source/boot/diskSizeGb/type).
func TestSDKGCEInstanceDisksRoundTrip(t *testing.T) {
	client, _, _ := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("disk-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Disks: []*computepb.AttachedDisk{{
			Boot:       ptrBool(true),
			AutoDelete: ptrBool(true),
			InitializeParams: &computepb.AttachedDiskInitializeParams{
				SourceImage: ptrStr("projects/debian-cloud/global/images/family/debian-12"),
				DiskSizeGb:  ptrInt64(50),
			},
		}},
	})

	got := mustGet(t, client, testZone, "disk-vm")

	disks := got.GetDisks()
	if len(disks) != 1 {
		t.Fatalf("disks=%d want 1", len(disks))
	}

	if !disks[0].GetBoot() {
		t.Error("boot disk not marked boot=true")
	}

	if disks[0].GetDiskSizeGb() != 50 {
		t.Errorf("diskSizeGb=%d want 50", disks[0].GetDiskSizeGb())
	}

	if !strings.HasSuffix(disks[0].GetSource(), "/disks/disk-vm") {
		t.Errorf("source=%q want .../disks/disk-vm", disks[0].GetSource())
	}

	if disks[0].GetType() != "PERSISTENT" {
		t.Errorf("type=%q want PERSISTENT", disks[0].GetType())
	}
}

// TestSDKGCEInstanceTagsAndMetadataRoundTrip covers findings #2 and #3: network
// tags and metadata (incl. startup-script) must round-trip with fingerprints.
func TestSDKGCEInstanceTagsAndMetadataRoundTrip(t *testing.T) {
	client, _, _ := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("meta-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Tags:        &computepb.Tags{Items: []string{"http-server", "https-server"}},
		Metadata: &computepb.Metadata{Items: []*computepb.Items{
			{Key: ptrStr("startup-script"), Value: ptrStr("#!/bin/bash\necho hi")},
		}},
	})

	got := mustGet(t, client, testZone, "meta-vm")

	if items := got.GetTags().GetItems(); len(items) != 2 || items[0] != "http-server" {
		t.Errorf("tags.items=%v want [http-server https-server]", items)
	}

	if got.GetTags().GetFingerprint() == "" {
		t.Error("tags.fingerprint empty")
	}

	md := got.GetMetadata().GetItems()
	if len(md) != 1 || md[0].GetKey() != "startup-script" {
		t.Fatalf("metadata.items=%v want [startup-script]", md)
	}

	if !strings.Contains(md[0].GetValue(), "echo hi") {
		t.Errorf("startup-script value=%q", md[0].GetValue())
	}

	if got.GetMetadata().GetFingerprint() == "" {
		t.Error("metadata.fingerprint empty")
	}
}

// TestSDKGCEInstanceUpdateVerbs covers finding #4: setLabels/setMetadata/
// setTags/setMachineType return a DONE Operation and mutate the instance.
func TestSDKGCEInstanceUpdateVerbs(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("upd-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	labelOp, err := client.SetLabels(ctx, &computepb.SetLabelsInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "upd-vm",
		InstancesSetLabelsRequestResource: &computepb.InstancesSetLabelsRequest{
			Labels: map[string]string{"env": "prod"},
		},
	})
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	if err := labelOp.Wait(ctx); err != nil {
		t.Fatalf("SetLabels wait: %v", err)
	}

	// Real GCP requires the instance to be stopped (TERMINATED) before its
	// machine type can be changed, so stop it first.
	stopOp, err := client.Stop(ctx, &computepb.StopInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "upd-vm",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if err := stopOp.Wait(ctx); err != nil {
		t.Fatalf("Stop wait: %v", err)
	}

	mtOp, err := client.SetMachineType(ctx, &computepb.SetMachineTypeInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "upd-vm",
		InstancesSetMachineTypeRequestResource: &computepb.InstancesSetMachineTypeRequest{
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n2-standard-4"),
		},
	})
	if err != nil {
		t.Fatalf("SetMachineType: %v", err)
	}

	if err := mtOp.Wait(ctx); err != nil {
		t.Fatalf("SetMachineType wait: %v", err)
	}

	tagsOp, err := client.SetTags(ctx, &computepb.SetTagsInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "upd-vm",
		TagsResource: &computepb.Tags{Items: []string{"web"}},
	})
	if err != nil {
		t.Fatalf("SetTags: %v", err)
	}

	if err := tagsOp.Wait(ctx); err != nil {
		t.Fatalf("SetTags wait: %v", err)
	}

	got := mustGet(t, client, testZone, "upd-vm")

	if got.GetLabels()["env"] != "prod" {
		t.Errorf("labels=%v want env=prod", got.GetLabels())
	}

	if !strings.HasSuffix(got.GetMachineType(), "/machineTypes/n2-standard-4") {
		t.Errorf("machineType=%q want n2-standard-4", got.GetMachineType())
	}

	if items := got.GetTags().GetItems(); len(items) != 1 || items[0] != "web" {
		t.Errorf("tags=%v want [web]", items)
	}
}

// TestSDKGCEInstanceSetMetadata covers the setMetadata verb from finding #4.
func TestSDKGCEInstanceSetMetadata(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("sm-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	op, err := client.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "sm-vm",
		MetadataResource: &computepb.Metadata{Items: []*computepb.Items{
			{Key: ptrStr("foo"), Value: ptrStr("bar")},
		}},
	})
	if err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("SetMetadata wait: %v", err)
	}

	got := mustGet(t, client, testZone, "sm-vm")

	md := got.GetMetadata().GetItems()
	if len(md) != 1 || md[0].GetKey() != "foo" || md[0].GetValue() != "bar" {
		t.Errorf("metadata=%v want foo=bar", md)
	}
}

// TestSDKGCEInstanceAttachDetachDisk covers finding #5.
func TestSDKGCEInstanceAttachDetachDisk(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("ad-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	attachOp, err := client.AttachDisk(ctx, &computepb.AttachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "ad-vm",
		AttachedDiskResource: &computepb.AttachedDisk{
			DeviceName: ptrStr("data"),
			Source:     ptrStr("zones/" + testZone + "/disks/data-disk"),
		},
	})
	if err != nil {
		t.Fatalf("AttachDisk: %v", err)
	}

	if err := attachOp.Wait(ctx); err != nil {
		t.Fatalf("AttachDisk wait: %v", err)
	}

	if !hasDevice(mustGet(t, client, testZone, "ad-vm").GetDisks(), "data") {
		t.Fatal("attached disk 'data' not in disks[]")
	}

	detachOp, err := client.DetachDisk(ctx, &computepb.DetachDiskInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "ad-vm", DeviceName: "data",
	})
	if err != nil {
		t.Fatalf("DetachDisk: %v", err)
	}

	if err := detachOp.Wait(ctx); err != nil {
		t.Fatalf("DetachDisk wait: %v", err)
	}

	if hasDevice(mustGet(t, client, testZone, "ad-vm").GetDisks(), "data") {
		t.Error("detached disk 'data' still present")
	}
}

func hasDevice(disks []*computepb.AttachedDisk, device string) bool {
	for _, d := range disks {
		if d.GetDeviceName() == device {
			return true
		}
	}

	return false
}

// TestSDKGCEInstanceListZoneScoped covers finding #6: List(zone) returns only
// that zone's instances.
func TestSDKGCEInstanceListZoneScoped(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name: ptrStr("za"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})
	mustInsert(t, client, altZone, &computepb.Instance{
		Name: ptrStr("zb"), MachineType: ptrStr("zones/" + altZone + "/machineTypes/n1-standard-1"),
	})

	names := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{Project: testProject, Zone: testZone}))
	if len(names) != 1 || names[0] != "za" {
		t.Errorf("list %s = %v want [za]", testZone, names)
	}
}

// TestSDKGCEInstanceDuplicateInsertConflict covers finding #7: a duplicate name
// in the same zone must fail with 409 alreadyExists.
func TestSDKGCEInstanceDuplicateInsertConflict(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	inst := &computepb.Instance{
		Name: ptrStr("dup"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	}
	mustInsert(t, client, testZone, inst)

	_, err := client.Insert(ctx, &computepb.InsertInstanceRequest{
		Project: testProject, Zone: testZone, InstanceResource: inst,
	})
	if err == nil {
		t.Fatal("duplicate insert succeeded, want 409 alreadyExists")
	}

	if !strings.Contains(err.Error(), "alreadyExists") && !strings.Contains(err.Error(), "409") {
		t.Errorf("err=%v want alreadyExists/409", err)
	}
}

// TestSDKGCEInstanceNetworkSelfLinks covers finding #8: networkInterfaces echo
// fully-qualified network + subnetwork self-links.
func TestSDKGCEInstanceNetworkSelfLinks(t *testing.T) {
	client, _, _ := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("net-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		NetworkInterfaces: []*computepb.NetworkInterface{{
			Network:    ptrStr("global/networks/default"),
			Subnetwork: ptrStr("regions/us-central1/subnetworks/my-subnet"),
		}},
	})

	nics := mustGet(t, client, testZone, "net-vm").GetNetworkInterfaces()
	if len(nics) == 0 {
		t.Fatal("no networkInterfaces")
	}

	if !strings.HasSuffix(nics[0].GetNetwork(), "/global/networks/default") {
		t.Errorf("network=%q want fully-qualified .../global/networks/default", nics[0].GetNetwork())
	}

	if !strings.HasSuffix(nics[0].GetSubnetwork(), "/regions/us-central1/subnetworks/my-subnet") {
		t.Errorf("subnetwork=%q want .../regions/us-central1/subnetworks/my-subnet", nics[0].GetSubnetwork())
	}
}

// TestSDKGCEInstanceTimestampAndFingerprints covers findings #9 and #11.
func TestSDKGCEInstanceTimestampAndFingerprints(t *testing.T) {
	client, _, _ := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("ts-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	got := mustGet(t, client, testZone, "ts-vm")

	if got.GetCreationTimestamp() == "" {
		t.Error("creationTimestamp empty")
	}

	if got.GetLabelFingerprint() == "" {
		t.Error("labelFingerprint empty")
	}

	if got.GetFingerprint() == "" {
		t.Error("fingerprint empty")
	}
}

// TestSDKGCEInstanceDefaults covers finding #13: realistic defaults.
func TestSDKGCEInstanceDefaults(t *testing.T) {
	client, _, _ := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name:        ptrStr("def-vm"),
		MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	got := mustGet(t, client, testZone, "def-vm")

	if got.GetCpuPlatform() == "" {
		t.Error("cpuPlatform empty")
	}

	if got.GetScheduling() == nil {
		t.Error("scheduling nil")
	}

	if len(got.GetServiceAccounts()) == 0 {
		t.Error("serviceAccounts empty")
	}

	if got.GetShieldedInstanceConfig() == nil {
		t.Error("shieldedInstanceConfig nil")
	}
}

// TestSDKGCEInstanceAggregatedList covers finding #12: aggregatedList groups
// instances by their zone scope.
func TestSDKGCEInstanceAggregatedList(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name: ptrStr("agg-a"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})
	mustInsert(t, client, altZone, &computepb.Instance{
		Name: ptrStr("agg-b"), MachineType: ptrStr("zones/" + altZone + "/machineTypes/n1-standard-1"),
	})

	it := client.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{Project: testProject})

	byScope := map[string][]string{}

	for {
		pair, err := it.Next()
		if err != nil {
			break
		}

		for _, inst := range pair.Value.GetInstances() {
			byScope[pair.Key] = append(byScope[pair.Key], inst.GetName())
		}
	}

	if got := byScope["zones/"+testZone]; len(got) != 1 || got[0] != "agg-a" {
		t.Errorf("scope %s = %v want [agg-a]", testZone, got)
	}

	if got := byScope["zones/"+altZone]; len(got) != 1 || got[0] != "agg-b" {
		t.Errorf("scope %s = %v want [agg-b]", altZone, got)
	}
}

// TestGCEInstanceListFilterAndPagination covers finding #10: the filter and
// pagination query params are honored (SDK for filter, raw HTTP for the
// nextPageToken/maxResults contract the SDK iterator hides).
func TestGCEInstanceListFilterAndPagination(t *testing.T) {
	client, ts, ctx := newInstancesEnv(t)

	for _, n := range []string{"f1", "f2", "f3"} {
		mustInsert(t, client, testZone, &computepb.Instance{
			Name: ptrStr(n), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		})
	}

	// Filter: a non-matching name must return zero instances (not all).
	miss := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone, Filter: ptrStr("name = nonexistent-xyz"),
	}))
	if len(miss) != 0 {
		t.Errorf("filter miss returned %v, want none", miss)
	}

	hit := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone, Filter: ptrStr("name = f2"),
	}))
	if len(hit) != 1 || hit[0] != "f2" {
		t.Errorf("filter name=f2 returned %v, want [f2]", hit)
	}

	// Pagination: maxResults=1 must return one item plus a nextPageToken.
	page := rawList(t, ts, "/instances?maxResults=1")
	if len(page.Items) != 1 {
		t.Errorf("maxResults=1 returned %d items", len(page.Items))
	}

	if page.NextPageToken == "" {
		t.Error("nextPageToken empty with more pages remaining")
	}
}

// TestGCEInstanceListFilterLabelsAndUnknownField covers BUG3: a "labels.<k>=<v>"
// filter returns only the matching instances, and a filter naming a field the
// emulator does not model matches everything (never silently excludes all).
func TestGCEInstanceListFilterLabelsAndUnknownField(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name: ptrStr("prod-vm"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Labels: map[string]string{"env": "prod"},
	})
	mustInsert(t, client, testZone, &computepb.Instance{
		Name: ptrStr("dev-vm"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
		Labels: map[string]string{"env": "dev"},
	})

	prod := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone, Filter: ptrStr("labels.env=prod"),
	}))
	if len(prod) != 1 || prod[0] != "prod-vm" {
		t.Errorf("labels.env=prod returned %v, want [prod-vm]", prod)
	}

	// An unrecognized field must match everything, not exclude all.
	all := listNames(t, client.List(ctx, &computepb.ListInstancesRequest{
		Project: testProject, Zone: testZone, Filter: ptrStr("someUnknownField=whatever"),
	}))
	if len(all) != 2 {
		t.Errorf("unknown-field filter returned %v, want both instances", all)
	}
}

// TestGCEInstanceFingerprintPrecondition covers BUG1: setLabels/setMetadata/
// setTags enforce the incoming fingerprint — a stale one is rejected 412
// conditionNotMet, the current one succeeds.
func TestGCEInstanceFingerprintPrecondition(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name: ptrStr("fp-vm"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	const stale = "c3RhbGU=" // base64("stale"), never a real fingerprint

	// setLabels: stale rejected, current accepted.
	_, err := client.SetLabels(ctx, &computepb.SetLabelsInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "fp-vm",
		InstancesSetLabelsRequestResource: &computepb.InstancesSetLabelsRequest{
			Labels: map[string]string{"env": "prod"}, LabelFingerprint: ptrStr(stale),
		},
	})
	assertConditionNotMet(t, "setLabels", err)

	labelFp := mustGet(t, client, testZone, "fp-vm").GetLabelFingerprint()
	mustWait(t, ctx, mustSetLabels(t, ctx, client, "fp-vm", map[string]string{"env": "prod"}, labelFp))

	// setMetadata: stale rejected, current accepted.
	_, err = client.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "fp-vm",
		MetadataResource: &computepb.Metadata{
			Items:       []*computepb.Items{{Key: ptrStr("k"), Value: ptrStr("v")}},
			Fingerprint: ptrStr(stale),
		},
	})
	assertConditionNotMet(t, "setMetadata", err)

	metaFp := mustGet(t, client, testZone, "fp-vm").GetMetadata().GetFingerprint()
	mdOp, err := client.SetMetadata(ctx, &computepb.SetMetadataInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "fp-vm",
		MetadataResource: &computepb.Metadata{
			Items:       []*computepb.Items{{Key: ptrStr("k"), Value: ptrStr("v")}},
			Fingerprint: ptrStr(metaFp),
		},
	})
	if err != nil {
		t.Fatalf("setMetadata current fingerprint: %v", err)
	}

	mustWait(t, ctx, mdOp)

	// setTags: stale rejected, current accepted.
	_, err = client.SetTags(ctx, &computepb.SetTagsInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "fp-vm",
		TagsResource: &computepb.Tags{Items: []string{"web"}, Fingerprint: ptrStr(stale)},
	})
	assertConditionNotMet(t, "setTags", err)

	tagsFp := mustGet(t, client, testZone, "fp-vm").GetTags().GetFingerprint()
	tagsOp, err := client.SetTags(ctx, &computepb.SetTagsInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "fp-vm",
		TagsResource: &computepb.Tags{Items: []string{"web"}, Fingerprint: ptrStr(tagsFp)},
	})
	if err != nil {
		t.Fatalf("setTags current fingerprint: %v", err)
	}

	mustWait(t, ctx, tagsOp)
}

// TestGCEInstanceSetMachineTypeRequiresStopped covers BUG2: setMachineType is
// rejected 400 on a running instance and succeeds once it is stopped.
func TestGCEInstanceSetMachineTypeRequiresStopped(t *testing.T) {
	client, _, ctx := newInstancesEnv(t)

	mustInsert(t, client, testZone, &computepb.Instance{
		Name: ptrStr("mt-vm"), MachineType: ptrStr("zones/" + testZone + "/machineTypes/n1-standard-1"),
	})

	req := &computepb.SetMachineTypeInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "mt-vm",
		InstancesSetMachineTypeRequestResource: &computepb.InstancesSetMachineTypeRequest{
			MachineType: ptrStr("zones/" + testZone + "/machineTypes/n2-standard-4"),
		},
	}

	if _, err := client.SetMachineType(ctx, req); err == nil {
		t.Fatal("setMachineType on running instance succeeded, want 400")
	} else if !strings.Contains(err.Error(), "400") && !strings.Contains(err.Error(), "must be stopped") {
		t.Errorf("err=%v want 400/must be stopped", err)
	}

	stopOp, err := client.Stop(ctx, &computepb.StopInstanceRequest{
		Project: testProject, Zone: testZone, Instance: "mt-vm",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	mustWait(t, ctx, stopOp)

	mtOp, err := client.SetMachineType(ctx, req)
	if err != nil {
		t.Fatalf("setMachineType on stopped instance: %v", err)
	}

	mustWait(t, ctx, mtOp)

	if !strings.HasSuffix(mustGet(t, client, testZone, "mt-vm").GetMachineType(), "/machineTypes/n2-standard-4") {
		t.Error("machineType not updated after stop")
	}
}

func assertConditionNotMet(t *testing.T, verb string, err error) {
	t.Helper()

	if err == nil {
		t.Fatalf("%s with stale fingerprint succeeded, want 412 conditionNotMet", verb)
	}

	if !strings.Contains(err.Error(), "412") && !strings.Contains(err.Error(), "conditionNotMet") {
		t.Errorf("%s err=%v want 412/conditionNotMet", verb, err)
	}
}

func mustSetLabels(
	t *testing.T, ctx context.Context, client *gcpcompute.InstancesClient,
	name string, labels map[string]string, fingerprint string,
) *gcpcompute.Operation {
	t.Helper()

	op, err := client.SetLabels(ctx, &computepb.SetLabelsInstanceRequest{
		Project: testProject, Zone: testZone, Instance: name,
		InstancesSetLabelsRequestResource: &computepb.InstancesSetLabelsRequest{
			Labels: labels, LabelFingerprint: ptrStr(fingerprint),
		},
	})
	if err != nil {
		t.Fatalf("setLabels current fingerprint: %v", err)
	}

	return op
}

func mustWait(t *testing.T, ctx context.Context, op *gcpcompute.Operation) {
	t.Helper()

	if err := op.Wait(ctx); err != nil {
		t.Fatalf("operation wait: %v", err)
	}
}

func listNames(t *testing.T, it *gcpcompute.InstanceIterator) []string {
	t.Helper()

	var names []string

	for {
		inst, err := it.Next()
		if err != nil {
			break
		}

		names = append(names, inst.GetName())
	}

	return names
}

type rawInstanceList struct {
	Items         []map[string]any `json:"items"`
	NextPageToken string           `json:"nextPageToken"`
}

func rawList(t *testing.T, ts *httptest.Server, suffix string) rawInstanceList {
	t.Helper()

	resp, err := ts.Client().Get(ts.URL + zonesPath(suffix))
	if err != nil {
		t.Fatalf("raw list: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("raw list status=%d", resp.StatusCode)
	}

	var out rawInstanceList
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	return out
}
