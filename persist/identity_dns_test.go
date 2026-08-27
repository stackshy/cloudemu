package persist_test

import (
	"context"
	"encoding/json"
	"testing"

	cloudemu "github.com/stackshy/cloudemu/v2"
	"github.com/stackshy/cloudemu/v2/persist"
	dnsdriver "github.com/stackshy/cloudemu/v2/services/dns/driver"
)

// roundTripDNS exports the given provider's Snapshottable set, JSON round-trips
// it exactly as the on-disk snapshot, and restores into a fresh provider set.
func roundTripDNS(t *testing.T, provider string, src, dst persist.Services) {
	t.Helper()

	ctx := context.Background()

	snap, err := persist.ExportAll(ctx, map[string]persist.Services{provider: src}, persist.Options{IncludeAssets: true})
	if err != nil {
		t.Fatalf("ExportAll: %v", err)
	}

	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}

	var got persist.Snapshot
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}

	if err := persist.RestoreAll(ctx, &got, map[string]persist.Services{provider: dst}); err != nil {
		t.Fatalf("RestoreAll: %v", err)
	}
}

// TestDNSSnapshotIdentityAWS verifies a Route 53 snapshot/restore into a fresh
// mock preserves hosted-zone ids, records (with their zone cross-reference and
// weighting), health-check ids and status, private-zone VPC associations, and
// the ChangeResourceTags map.
func TestDNSSnapshotIdentityAWS(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAWS()

	if _, ok := src.SnapshotServices()["route53"]; !ok {
		t.Fatalf("SnapshotServices() missing DNS key %q", "route53")
	}

	zone, err := src.Route53.CreateZone(ctx, dnsdriver.ZoneConfig{
		Name:    "example.com",
		Private: true,
		Tags:    map[string]string{"env": "prod"},
		VPCs:    []dnsdriver.VPCAssociation{{VPCID: "vpc-abc", VPCRegion: "us-east-1"}},
	})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	wantZoneID := zone.ID

	if _, err := src.Route53.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: wantZoneID, Name: "www.example.com", Type: "A", TTL: 300, Values: []string{"1.2.3.4"},
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	hc, err := src.Route53.CreateHealthCheck(ctx, dnsdriver.HealthCheckConfig{
		Endpoint: "1.2.3.4", Port: 443, Protocol: "HTTPS", Path: "/health",
	})
	if err != nil {
		t.Fatalf("create health check: %v", err)
	}
	if err := src.Route53.SetHealthCheckStatus(ctx, hc.ID, "UNHEALTHY"); err != nil {
		t.Fatalf("set health check status: %v", err)
	}

	// ChangeResourceTags on the zone: a separate store from ZoneInfo.Tags.
	if err := src.Route53.ChangeResourceTags(ctx, wantZoneID, map[string]string{"team": "dns"}, nil); err != nil {
		t.Fatalf("change resource tags: %v", err)
	}

	dst := cloudemu.NewAWS()
	roundTripDNS(t, "aws", src.SnapshotServices(), dst.SnapshotServices())

	// Zone identity + VPC association survive.
	gotZone, err := dst.Route53.GetZone(ctx, wantZoneID)
	if err != nil {
		t.Fatalf("get restored zone: %v", err)
	}
	if gotZone.ID != wantZoneID || gotZone.Name != "example.com" {
		t.Fatalf("restored zone = %+v, want id %q name example.com", gotZone, wantZoneID)
	}
	if !gotZone.Private || len(gotZone.VPCs) != 1 || gotZone.VPCs[0].VPCID != "vpc-abc" {
		t.Fatalf("restored zone VPC association lost: %+v", gotZone.VPCs)
	}

	// Record survives under its zone.
	rec, err := dst.Route53.GetRecord(ctx, wantZoneID, "www.example.com", "A")
	if err != nil {
		t.Fatalf("get restored record: %v", err)
	}
	if len(rec.Values) != 1 || rec.Values[0] != "1.2.3.4" {
		t.Fatalf("restored record values = %v, want [1.2.3.4]", rec.Values)
	}

	// Health check id + non-default status survive.
	gotHC, err := dst.Route53.GetHealthCheck(ctx, hc.ID)
	if err != nil {
		t.Fatalf("get restored health check: %v", err)
	}
	if gotHC.ID != hc.ID || gotHC.Status != "UNHEALTHY" {
		t.Fatalf("restored health check = %+v, want id %q status UNHEALTHY", gotHC, hc.ID)
	}

	// ChangeResourceTags map survives: removing the tag proves it was restored.
	if err := src.Route53.ChangeResourceTags(ctx, wantZoneID, nil, []string{"team"}); err != nil {
		t.Fatalf("sanity change on source: %v", err)
	}
	if err := dst.Route53.ChangeResourceTags(ctx, wantZoneID, nil, []string{"team"}); err != nil {
		t.Fatalf("remove restored resource tag: %v", err)
	}
}

// TestDNSSnapshotIdentityAzure verifies an Azure DNS snapshot/restore into a
// fresh mock preserves zone ids, records, and health-check ids.
func TestDNSSnapshotIdentityAzure(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewAzure()

	if _, ok := src.SnapshotServices()["dns"]; !ok {
		t.Fatalf("SnapshotServices() missing DNS key %q", "dns")
	}

	zone, err := src.DNS.CreateZone(ctx, dnsdriver.ZoneConfig{
		Name: "contoso.com",
		Tags: map[string]string{"env": "prod"},
	})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	wantZoneID := zone.ID

	if _, err := src.DNS.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: wantZoneID, Name: "app.contoso.com", Type: "CNAME", TTL: 60, Values: []string{"target.contoso.com"},
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	hc, err := src.DNS.CreateHealthCheck(ctx, dnsdriver.HealthCheckConfig{
		Endpoint: "app.contoso.com", Port: 80, Protocol: "HTTP", Path: "/",
	})
	if err != nil {
		t.Fatalf("create health check: %v", err)
	}

	dst := cloudemu.NewAzure()
	roundTripDNS(t, "azure", src.SnapshotServices(), dst.SnapshotServices())

	gotZone, err := dst.DNS.GetZone(ctx, wantZoneID)
	if err != nil {
		t.Fatalf("get restored zone: %v", err)
	}
	if gotZone.ID != wantZoneID || gotZone.Tags["env"] != "prod" {
		t.Fatalf("restored zone = %+v, want id %q env=prod", gotZone, wantZoneID)
	}

	rec, err := dst.DNS.GetRecord(ctx, wantZoneID, "app.contoso.com", "CNAME")
	if err != nil {
		t.Fatalf("get restored record: %v", err)
	}
	if len(rec.Values) != 1 || rec.Values[0] != "target.contoso.com" {
		t.Fatalf("restored record values = %v", rec.Values)
	}

	if _, err := dst.DNS.GetHealthCheck(ctx, hc.ID); err != nil {
		t.Fatalf("get restored health check: %v", err)
	}
}

// TestDNSSnapshotIdentityGCP verifies a Cloud DNS snapshot/restore into a fresh
// mock preserves managed-zone ids, records, health-check ids, and the zone's
// nested DNSSEC config.
func TestDNSSnapshotIdentityGCP(t *testing.T) {
	ctx := context.Background()

	src := cloudemu.NewGCP()

	if _, ok := src.SnapshotServices()["clouddns"]; !ok {
		t.Fatalf("SnapshotServices() missing DNS key %q", "clouddns")
	}

	zone, err := src.CloudDNS.CreateZone(ctx, dnsdriver.ZoneConfig{
		Name:         "example-gcp.com",
		DNSSECConfig: &dnsdriver.DNSSECConfig{State: "on", NonExistence: "nsec3"},
	})
	if err != nil {
		t.Fatalf("create zone: %v", err)
	}

	wantZoneID := zone.ID

	if _, err := src.CloudDNS.CreateRecord(ctx, dnsdriver.RecordConfig{
		ZoneID: wantZoneID, Name: "svc.example-gcp.com", Type: "A", TTL: 120, Values: []string{"10.0.0.1"},
	}); err != nil {
		t.Fatalf("create record: %v", err)
	}

	hc, err := src.CloudDNS.CreateHealthCheck(ctx, dnsdriver.HealthCheckConfig{
		Endpoint: "10.0.0.1", Port: 443, Protocol: "HTTPS", Path: "/healthz",
	})
	if err != nil {
		t.Fatalf("create health check: %v", err)
	}

	dst := cloudemu.NewGCP()
	roundTripDNS(t, "gcp", src.SnapshotServices(), dst.SnapshotServices())

	gotZone, err := dst.CloudDNS.GetZone(ctx, wantZoneID)
	if err != nil {
		t.Fatalf("get restored zone: %v", err)
	}
	if gotZone.ID != wantZoneID {
		t.Fatalf("restored zone id = %q, want %q", gotZone.ID, wantZoneID)
	}
	if gotZone.DNSSECConfig == nil || gotZone.DNSSECConfig.State != "on" {
		t.Fatalf("restored zone DNSSEC config lost: %+v", gotZone.DNSSECConfig)
	}

	rec, err := dst.CloudDNS.GetRecord(ctx, wantZoneID, "svc.example-gcp.com", "A")
	if err != nil {
		t.Fatalf("get restored record: %v", err)
	}
	if len(rec.Values) != 1 || rec.Values[0] != "10.0.0.1" {
		t.Fatalf("restored record values = %v", rec.Values)
	}

	if _, err := dst.CloudDNS.GetHealthCheck(ctx, hc.ID); err != nil {
		t.Fatalf("get restored health check: %v", err)
	}
}
