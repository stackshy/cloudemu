package vpc_test

import (
	"context"
	"maps"
	"net/http/httptest"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/providers/gcp"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

// serveGCPNet serves cloudP's networking + compute drivers and returns a
// compute/v1 client against it.
func serveGCPNet(t *testing.T, cloudP *gcp.Provider) *computev1.Service {
	t.Helper()

	ts := httptest.NewServer(gcpserver.New(gcpserver.Drivers{Networking: cloudP.VPC, Compute: cloudP.GCE}))
	t.Cleanup(ts.Close)

	svc, err := computev1.NewService(context.Background(),
		option.WithEndpoint(ts.URL+"/compute/v1/"), option.WithoutAuthentication(), option.WithHTTPClient(ts.Client()))
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}

	return svc
}

// TestSDKAddressLabelsSurviveSnapshotRestore: a reserved address and the
// labels / labelFingerprint setLabels gave it live in the provider, so they are
// in the provider snapshot and read back identically from a restored emulator;
// the restored fingerprint still authorizes the next setLabels, and the IP
// allocator does not hand a restored address's IP out again.
func TestSDKAddressLabelsSurviveSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	src := cloudemu.NewGCP()
	svc := serveGCPNet(t, src)

	if _, err := svc.GlobalAddresses.Insert(testProject, &computev1.Address{Name: "psa-range"}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	created, err := svc.GlobalAddresses.Get(testProject, "psa-range").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, err := svc.GlobalAddresses.SetLabels(testProject, "psa-range", &computev1.GlobalSetLabelsRequest{
		Labels: map[string]string{"env": "prod"}, LabelFingerprint: created.LabelFingerprint,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	before, err := svc.GlobalAddresses.Get(testProject, "psa-range").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after SetLabels: %v", err)
	}

	data, err := src.VPC.Snapshot(ctx, false)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	dst := cloudemu.NewGCP()
	if err := dst.VPC.Restore(ctx, data); err != nil {
		t.Fatalf("Restore: %v", err)
	}

	restored := serveGCPNet(t, dst)

	after, err := restored.GlobalAddresses.Get(testProject, "psa-range").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after restore: %v", err)
	}

	if !maps.Equal(after.Labels, map[string]string{"env": "prod"}) || after.LabelFingerprint != before.LabelFingerprint ||
		after.Address != before.Address {
		t.Fatalf("after restore: labels=%v fingerprint=%q address=%q, want %v %q %q",
			after.Labels, after.LabelFingerprint, after.Address, before.Labels, before.LabelFingerprint, before.Address)
	}

	if _, err := restored.GlobalAddresses.SetLabels(testProject, "psa-range", &computev1.GlobalSetLabelsRequest{
		Labels: map[string]string{"env": "dev"}, LabelFingerprint: after.LabelFingerprint,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("SetLabels with the restored fingerprint: %v", err)
	}

	if _, err := restored.GlobalAddresses.Insert(testProject, &computev1.Address{Name: "next-range"}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert after restore: %v", err)
	}

	next, err := restored.GlobalAddresses.Get(testProject, "next-range").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get next-range: %v", err)
	}

	if next.Address == after.Address {
		t.Errorf("new address got IP %s, already held by the restored address", next.Address)
	}

	if _, err := restored.GlobalAddresses.Delete(testProject, "next-range").Context(ctx).Do(); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, err = restored.GlobalAddresses.Get(testProject, "next-range").Context(ctx).Do()
	wantStatus(t, "Get after Delete", err, 404)

	_, err = restored.GlobalAddresses.Delete(testProject, "next-range").Context(ctx).Do()
	wantStatus(t, "Delete twice", err, 404)
}
