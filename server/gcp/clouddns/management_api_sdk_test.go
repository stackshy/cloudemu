package clouddns_test

import (
	"context"
	"strconv"
	"strings"
	"testing"

	dns "google.golang.org/api/dns/v1"
)

// TestSDKApexRecordsSeeded reproduces the audit finding that a freshly created
// managed zone had zero record sets: real Cloud DNS auto-creates SOA + NS
// records at the apex, so rrsets.list on a new zone must return exactly those
// two.
func TestSDKApexRecordsSeeded(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "apex-zone",
		DnsName: "apex.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	rrsets, err := svc.ResourceRecordSets.List(testProject, "apex-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ResourceRecordSets.List: %v", err)
	}

	if len(rrsets.Rrsets) != 2 {
		t.Fatalf("new zone rrsets = %d, want 2 (SOA+NS): %+v", len(rrsets.Rrsets), rrsets.Rrsets)
	}

	var soa, ns *dns.ResourceRecordSet
	for _, r := range rrsets.Rrsets {
		switch r.Type {
		case "SOA":
			soa = r
		case "NS":
			ns = r
		}
	}

	if soa == nil || ns == nil {
		t.Fatalf("apex records missing SOA or NS: %+v", rrsets.Rrsets)
	}

	if soa.Name != "apex.example.com." || ns.Name != "apex.example.com." {
		t.Errorf("apex records not at dnsName: SOA=%q NS=%q", soa.Name, ns.Name)
	}

	if len(ns.Rrdatas) != 4 {
		t.Errorf("apex NS rrdatas = %v, want 4 name servers", ns.Rrdatas)
	}
}

// TestSDKZoneNameServers reproduces the finding that ManagedZone.nameServers was
// absent: real Cloud DNS assigns 4 authoritative name servers, returned on both
// create and get and stable across calls.
func TestSDKZoneNameServers(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	created, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "ns-zone",
		DnsName: "ns.example.com.",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	if len(created.NameServers) != 4 {
		t.Fatalf("create nameServers = %v, want 4", created.NameServers)
	}

	for _, ns := range created.NameServers {
		if !strings.HasSuffix(ns, ".googledomains.com.") {
			t.Errorf("nameServer %q not a googledomains delegation host", ns)
		}
	}

	got, err := svc.ManagedZones.Get(testProject, "ns-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Get: %v", err)
	}

	if len(got.NameServers) != 4 {
		t.Fatalf("get nameServers = %v, want 4", got.NameServers)
	}

	for i := range created.NameServers {
		if got.NameServers[i] != created.NameServers[i] {
			t.Fatalf("nameServers not stable: create=%v get=%v", created.NameServers, got.NameServers)
		}
	}
}

// TestSDKZoneCreationTime reproduces the finding that creationTime was never
// set: real Cloud DNS returns it on create and get.
func TestSDKZoneCreationTime(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	created, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "ct-zone",
		DnsName: "ct.example.com.",
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	if created.CreationTime == "" {
		t.Fatalf("create creationTime empty, want RFC3339 timestamp")
	}

	got, err := svc.ManagedZones.Get(testProject, "ct-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Get: %v", err)
	}

	if got.CreationTime != created.CreationTime {
		t.Fatalf("get creationTime = %q, want %q", got.CreationTime, created.CreationTime)
	}
}

// TestSDKZonePatchLabels reproduces the finding that managedZones.patch returned
// 400: real Cloud DNS supports patching labels and description.
func TestSDKZonePatchLabels(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:        "patch-zone",
		DnsName:     "patch.example.com.",
		Description: "before",
		Labels:      map[string]string{"env": "dev"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	// Patch returns an Operation, not the zone.
	if _, err := svc.ManagedZones.Patch(testProject, "patch-zone", &dns.ManagedZone{
		Description: "after",
		Labels:      map[string]string{"env": "prod", "team": "infra"},
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Patch: %v", err)
	}

	got, err := svc.ManagedZones.Get(testProject, "patch-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("ManagedZones.Get: %v", err)
	}

	if got.Labels["env"] != "prod" || got.Labels["team"] != "infra" {
		t.Errorf("labels after patch = %v, want env=prod team=infra", got.Labels)
	}

	if got.Description != "after" {
		t.Errorf("description after patch = %q, want after", got.Description)
	}

	// The reserved fields must survive the patch.
	if got.DnsName != "patch.example.com." {
		t.Errorf("dnsName lost across patch: %q", got.DnsName)
	}

	if got.CreationTime == "" {
		t.Errorf("creationTime lost across patch")
	}
}

// TestSDKChangesListAndGet reproduces the finding that changes.list/get returned
// 400 and omitted startTime: real Cloud DNS exposes the change log for polling.
func TestSDKChangesListAndGet(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "chg-zone",
		DnsName: "chg.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	created, err := svc.Changes.Create(testProject, "chg-zone", &dns.Change{
		Additions: []*dns.ResourceRecordSet{
			{Name: "www.chg.example.com.", Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.5"}},
		},
	}).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.Create: %v", err)
	}

	if created.StartTime == "" {
		t.Errorf("change startTime empty, want RFC3339 timestamp")
	}

	list, err := svc.Changes.List(testProject, "chg-zone").Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.List: %v", err)
	}

	if len(list.Changes) != 1 {
		t.Fatalf("changes.list = %d, want 1: %+v", len(list.Changes), list.Changes)
	}

	if list.Changes[0].Id != created.Id {
		t.Errorf("listed change id = %q, want %q", list.Changes[0].Id, created.Id)
	}

	got, err := svc.Changes.Get(testProject, "chg-zone", created.Id).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Changes.Get: %v", err)
	}

	if got.Id != created.Id || got.StartTime == "" || got.Status != "done" {
		t.Errorf("changes.get = %+v, want id=%s startTime set status=done", got, created.Id)
	}
}

// TestSDKRRSetsFilterAndPaging reproduces the finding that rrsets.list ignored
// ?name=/?type= filters and had no pagination.
func TestSDKRRSetsFilterAndPaging(t *testing.T) {
	svc := newDNSService(t)
	ctx := context.Background()

	if _, err := svc.ManagedZones.Create(testProject, &dns.ManagedZone{
		Name:    "filter-zone",
		DnsName: "filter.example.com.",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("ManagedZones.Create: %v", err)
	}

	for i := range 3 {
		name := "host" + strconv.Itoa(i) + ".filter.example.com."
		if _, err := svc.Changes.Create(testProject, "filter-zone", &dns.Change{
			Additions: []*dns.ResourceRecordSet{
				{Name: name, Type: "A", Ttl: 300, Rrdatas: []string{"192.0.2.1"}},
			},
		}).Context(ctx).Do(); err != nil {
			t.Fatalf("Changes.Create(%s): %v", name, err)
		}
	}

	// Filter by name: exactly the one A record.
	byName, err := svc.ResourceRecordSets.List(testProject, "filter-zone").
		Name("host1.filter.example.com.").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(name filter): %v", err)
	}

	if len(byName.Rrsets) != 1 || byName.Rrsets[0].Name != "host1.filter.example.com." {
		t.Fatalf("name filter = %+v, want single host1", byName.Rrsets)
	}

	// Filter by type NS: only the apex NS record.
	byType, err := svc.ResourceRecordSets.List(testProject, "filter-zone").
		Type("NS").Context(ctx).Do()
	if err != nil {
		t.Fatalf("List(type filter): %v", err)
	}

	if len(byType.Rrsets) != 1 || byType.Rrsets[0].Type != "NS" {
		t.Fatalf("type filter = %+v, want single NS", byType.Rrsets)
	}

	// Pagination: 5 total rrsets (SOA, NS, 3x A); a page of 2 must yield a token
	// and .Pages must walk every record exactly once.
	var pages, total int

	if err := svc.ResourceRecordSets.List(testProject, "filter-zone").MaxResults(2).
		Pages(ctx, func(page *dns.ResourceRecordSetsListResponse) error {
			pages++
			total += len(page.Rrsets)
			return nil
		}); err != nil {
		t.Fatalf("List(paged): %v", err)
	}

	if total != 5 {
		t.Errorf("paged total rrsets = %d, want 5", total)
	}

	if pages < 2 {
		t.Errorf("paged in %d page(s), want the page size to force >1", pages)
	}
}
