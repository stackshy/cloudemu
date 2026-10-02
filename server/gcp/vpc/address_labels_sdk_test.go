package vpc_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	computev1 "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const labelsRegion = "us-central1"

func newComputeV1(t *testing.T) *computev1.Service {
	t.Helper()

	ts := newGCPNetServer(t)

	svc, err := computev1.NewService(context.Background(),
		option.WithEndpoint(ts.URL+"/compute/v1/"),
		option.WithoutAuthentication(),
		option.WithHTTPClient(ts.Client()),
	)
	if err != nil {
		t.Fatalf("compute.NewService: %v", err)
	}

	return svc
}

// wantStatus asserts err is a googleapi error with the given HTTP status.
func wantStatus(t *testing.T, what string, err error, code int) {
	t.Helper()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != code {
		t.Fatalf("%s: err=%v, want HTTP %d", what, err, code)
	}
}

// TestSDKRegionalAddressSetLabels drives AddressesService.SetLabels through the
// real compute/v1 client: labels replace the set under the current
// labelFingerprint, a stale or missing fingerprint is 412, the returned
// operation resolves DONE through regionOperations, Get shows the new labels
// under a new fingerprint, and a labels.<k>=<v> list filter narrows the list.
func TestSDKRegionalAddressSetLabels(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	for _, name := range []string{"addr-a", "addr-b"} {
		if _, err := svc.Addresses.Insert(testProject, labelsRegion, &computev1.Address{
			Name: name, Labels: map[string]string{"team": "net"},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Insert %s: %v", name, err)
		}
	}

	before, err := svc.Addresses.Get(testProject, labelsRegion, "addr-a").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if before.LabelFingerprint == "" {
		t.Fatal("labelFingerprint empty on a freshly inserted address")
	}

	_, err = svc.Addresses.SetLabels(testProject, labelsRegion, "addr-a", &computev1.RegionSetLabelsRequest{
		Labels: map[string]string{"env": "prod"}, LabelFingerprint: "c3RhbGU=",
	}).Context(ctx).Do()
	wantStatus(t, "SetLabels(stale fingerprint)", err, http.StatusPreconditionFailed)

	_, err = svc.Addresses.SetLabels(testProject, labelsRegion, "addr-a", &computev1.RegionSetLabelsRequest{
		Labels: map[string]string{"env": "prod"},
	}).Context(ctx).Do()
	wantStatus(t, "SetLabels(no fingerprint)", err, http.StatusPreconditionFailed)

	op, err := svc.Addresses.SetLabels(testProject, labelsRegion, "addr-a", &computev1.RegionSetLabelsRequest{
		Labels: map[string]string{"env": "prod", "tier": "edge"}, LabelFingerprint: before.LabelFingerprint,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	if op.Status != "DONE" || op.OperationType != "setLabels" {
		t.Fatalf("op status=%q type=%q, want DONE/setLabels", op.Status, op.OperationType)
	}

	polled, err := svc.RegionOperations.Get(testProject, labelsRegion, op.Name).Context(ctx).Do()
	if err != nil || polled.Status != "DONE" {
		t.Fatalf("RegionOperations.Get: op=%+v err=%v", polled, err)
	}

	after, err := svc.Addresses.Get(testProject, labelsRegion, "addr-a").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get after: %v", err)
	}

	if len(after.Labels) != 2 || after.Labels["env"] != "prod" || after.Labels["tier"] != "edge" {
		t.Fatalf("labels=%v, want exactly env=prod tier=edge (team replaced away)", after.Labels)
	}

	if after.LabelFingerprint == "" || after.LabelFingerprint == before.LabelFingerprint {
		t.Fatalf("labelFingerprint %q did not change from %q", after.LabelFingerprint, before.LabelFingerprint)
	}

	// The old fingerprint is now stale.
	_, err = svc.Addresses.SetLabels(testProject, labelsRegion, "addr-a", &computev1.RegionSetLabelsRequest{
		Labels: map[string]string{}, LabelFingerprint: before.LabelFingerprint,
	}).Context(ctx).Do()
	wantStatus(t, "SetLabels(superseded fingerprint)", err, http.StatusPreconditionFailed)

	_, err = svc.Addresses.SetLabels(testProject, labelsRegion, "missing", &computev1.RegionSetLabelsRequest{
		LabelFingerprint: before.LabelFingerprint,
	}).Context(ctx).Do()
	wantStatus(t, "SetLabels(missing address)", err, http.StatusNotFound)

	assertFilter(ctx, t, svc, "labels.env=prod", []string{"addr-a"})
	assertFilter(ctx, t, svc, "labels.team=net", []string{"addr-b"})
	assertFilter(ctx, t, svc, "labels.env!=prod", []string{"addr-b"})
	assertFilter(ctx, t, svc, "name=addr-b", []string{"addr-b"})
}

func assertFilter(ctx context.Context, t *testing.T, svc *computev1.Service, filter string, want []string) {
	t.Helper()

	list, err := svc.Addresses.List(testProject, labelsRegion).Filter(filter).Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(%q): %v", filter, err)
	}

	got := make([]string, 0, len(list.Items))
	for _, a := range list.Items {
		got = append(got, a.Name)
	}

	if len(got) != len(want) {
		t.Fatalf("List(%q) = %v, want %v", filter, got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List(%q) = %v, want %v", filter, got, want)
		}
	}
}

// TestSDKGlobalAddressSetLabels covers GlobalAddressesService.SetLabels: the
// same replace + fingerprint semantics on a global address, with the operation
// resolving through globalOperations and an empty label set clearing labels.
func TestSDKGlobalAddressSetLabels(t *testing.T) {
	ctx := context.Background()
	svc := newComputeV1(t)

	if _, err := svc.GlobalAddresses.Insert(testProject, &computev1.Address{
		Name: "g-addr", Purpose: "VPC_PEERING", AddressType: "INTERNAL", PrefixLength: 16,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	before, err := svc.GlobalAddresses.Get(testProject, "g-addr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	_, err = svc.GlobalAddresses.SetLabels(testProject, "g-addr", &computev1.GlobalSetLabelsRequest{
		Labels: map[string]string{"env": "dev"},
	}).Context(ctx).Do()
	wantStatus(t, "SetLabels(no fingerprint)", err, http.StatusPreconditionFailed)

	op, err := svc.GlobalAddresses.SetLabels(testProject, "g-addr", &computev1.GlobalSetLabelsRequest{
		Labels: map[string]string{"env": "dev"}, LabelFingerprint: before.LabelFingerprint,
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("SetLabels: %v", err)
	}

	polled, err := svc.GlobalOperations.Get(testProject, op.Name).Context(ctx).Do()
	if err != nil || polled.Status != "DONE" {
		t.Fatalf("GlobalOperations.Get: op=%+v err=%v", polled, err)
	}

	mid, err := svc.GlobalAddresses.Get(testProject, "g-addr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get mid: %v", err)
	}

	if mid.Labels["env"] != "dev" || mid.LabelFingerprint == before.LabelFingerprint {
		t.Fatalf("labels=%v fp=%q (before %q), want env=dev under a new fingerprint",
			mid.Labels, mid.LabelFingerprint, before.LabelFingerprint)
	}

	if mid.Purpose != "VPC_PEERING" || mid.PrefixLength != 16 {
		t.Fatalf("setLabels clobbered other fields: purpose=%q prefixLength=%d", mid.Purpose, mid.PrefixLength)
	}

	if _, err := svc.GlobalAddresses.SetLabels(testProject, "g-addr", &computev1.GlobalSetLabelsRequest{
		LabelFingerprint: mid.LabelFingerprint,
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("SetLabels(clear): %v", err)
	}

	cleared, err := svc.GlobalAddresses.Get(testProject, "g-addr").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get cleared: %v", err)
	}

	if len(cleared.Labels) != 0 || cleared.LabelFingerprint != before.LabelFingerprint {
		t.Fatalf("labels=%v fp=%q, want no labels and the empty-set fingerprint %q",
			cleared.Labels, cleared.LabelFingerprint, before.LabelFingerprint)
	}
}
