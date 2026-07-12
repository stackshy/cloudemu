package clouddns_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strconv"
	"testing"

	dns "google.golang.org/api/dns/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/stackshy/cloudemu/v2"
	gcpserver "github.com/stackshy/cloudemu/v2/server/gcp"
)

const testProject = "demo"

func newDNSService(t *testing.T) *dns.Service {
	t.Helper()

	cloud := cloudemu.NewGCP()
	srv := gcpserver.New(gcpserver.Drivers{CloudDNS: cloud.CloudDNS})

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	svc, err := dns.NewService(context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("dns.NewService: %v", err)
	}

	return svc
}

func TestSDKCloudDNSZoneLifecycle(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	created, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:        "example-zone",
		DnsName:     "example.com.",
		Description: "test zone",
		Visibility:  "public",
		Labels:      map[string]string{"env": "test"},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	if created.Name != "example-zone" {
		t.Fatalf("got zone name %q, want example-zone", created.Name)
	}

	if created.Id == 0 {
		t.Fatalf("Create returned zero zone id: %+v", created)
	}

	// Cloud DNS lets a zone be addressed by its numeric id as well as its name.
	byID, err := svc.ManagedZones.Get(testProject, strconv.FormatUint(created.Id, 10)).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Get(by numeric id): %v", err)
	}
	if byID.Name != "example-zone" {
		t.Fatalf("Get(by id) = %q, want example-zone", byID.Name)
	}

	got, err := svc.ManagedZones.Get(testProject, "example-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Get: %v", err)
	}

	if got.Labels["env"] != "test" {
		t.Fatalf("labels = %v, want env=test", got.Labels)
	}

	if got.Visibility != "public" {
		t.Fatalf("visibility = %q, want public", got.Visibility)
	}

	list, err := svc.ManagedZones.List(testProject).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.List: %v", err)
	}

	if len(list.ManagedZones) != 1 || list.ManagedZones[0].Name != "example-zone" {
		t.Fatalf("List = %+v, want one zone example-zone", list.ManagedZones)
	}

	if err := svc.ManagedZones.Delete(testProject, "example-zone").Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Delete: %v", err)
	}

	_, err = svc.ManagedZones.Get(testProject, "example-zone").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get after delete: got %v, want 404", err)
	}
}

func TestSDKCloudDNSRecordChanges(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "records-zone",
		DnsName: "records.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	rrset := &dns.ResourceRecordSet{
		Name:    "www.records.com.",
		Type:    "A",
		Ttl:     300,
		Rrdatas: []string{"192.0.2.1"},
	}

	change, err := svc.Changes.Create(testProject, "records-zone", &dns.Change{
		Additions: []*dns.ResourceRecordSet{rrset},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.Create(add): %v", err)
	}

	if change.Status != "done" {
		t.Fatalf("change status = %q, want done", change.Status)
	}

	rrsets, err := svc.ResourceRecordSets.List(testProject, "records-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ResourceRecordSets.List: %v", err)
	}

	if len(rrsets.Rrsets) != 1 {
		t.Fatalf("got %d rrsets, want 1: %+v", len(rrsets.Rrsets), rrsets.Rrsets)
	}

	got := rrsets.Rrsets[0]
	if got.Name != "www.records.com." || got.Type != "A" || got.Ttl != 300 {
		t.Fatalf("rrset = %+v, want www.records.com. A 300", got)
	}

	if len(got.Rrdatas) != 1 || got.Rrdatas[0] != "192.0.2.1" {
		t.Fatalf("rrdatas = %v, want [192.0.2.1]", got.Rrdatas)
	}

	// Delete the record via a change with a deletion.
	if _, err := svc.Changes.Create(testProject, "records-zone", &dns.Change{
		Deletions: []*dns.ResourceRecordSet{rrset},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Changes.Create(delete): %v", err)
	}

	after, err := svc.ResourceRecordSets.List(testProject, "records-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ResourceRecordSets.List after delete: %v", err)
	}

	if len(after.Rrsets) != 0 {
		t.Fatalf("got %d rrsets after delete, want 0: %+v", len(after.Rrsets), after.Rrsets)
	}
}

func TestSDKCloudDNSErrors(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	_, err := svc.ManagedZones.Get(testProject, "missing").Context(ctx).Do()

	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Get(missing): got %v, want 404", err)
	}

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "dup",
		DnsName: "dup.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Applying a change against a missing zone is a 404.
	_, err = svc.Changes.Create(testProject, "missing", &dns.Change{
		Additions: []*dns.ResourceRecordSet{{Name: "a.missing.", Type: "A", Ttl: 60, Rrdatas: []string{"1.1.1.1"}}},
	}).Context(ctx).Do()
	if !errors.As(err, &gerr) || gerr.Code != 404 {
		t.Fatalf("Changes.Create(missing zone): got %v, want 404", err)
	}
}
