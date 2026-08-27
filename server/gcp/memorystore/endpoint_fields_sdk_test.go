package memorystore_test

import (
	"context"
	"net"
	"strings"
	"testing"

	redis "google.golang.org/api/redis/v1"
)

// TestSDKMemorystoreHostIsPrivateIP guards the AUDIT_GCP host finding: a
// Memorystore instance must expose a private IPv4 endpoint, not a fabricated
// AWS-style "{name}.redis.{region}.gcp.cloud" hostname.
func TestSDKMemorystoreHostIsPrivateIP(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
	}).InstanceId("ip-cache").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("ip-cache")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if strings.Contains(got.Host, "gcp.cloud") || strings.Contains(got.Host, ".redis.") {
		t.Fatalf("host is a fabricated hostname, want a private IP: %q", got.Host)
	}

	ip := net.ParseIP(got.Host)
	if ip == nil || ip.To4() == nil {
		t.Fatalf("host = %q, want a private IPv4 address", got.Host)
	}

	if !ip.IsPrivate() {
		t.Fatalf("host = %q, want an RFC1918 private address", got.Host)
	}

	if got.Port != 6379 {
		t.Fatalf("port = %d, want 6379", got.Port)
	}
}

// TestSDKMemorystoreNetworkFields guards the AUDIT_GCP missing-fields finding:
// authorizedNetwork must round-trip (normalized to the full network path) and
// connectMode/currentLocationId/persistenceIamIdentity must be populated.
func TestSDKMemorystoreNetworkFields(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:              "BASIC",
		MemorySizeGb:      1,
		AuthorizedNetwork: "projects/demo/global/networks/my-vpc",
		ConnectMode:       "PRIVATE_SERVICE_ACCESS",
	}).InstanceId("net-cache").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("net-cache")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.AuthorizedNetwork != "projects/demo/global/networks/my-vpc" {
		t.Errorf("authorizedNetwork = %q, want it echoed", got.AuthorizedNetwork)
	}

	if got.ConnectMode != "PRIVATE_SERVICE_ACCESS" {
		t.Errorf("connectMode = %q, want PRIVATE_SERVICE_ACCESS", got.ConnectMode)
	}

	if got.CurrentLocationId == "" {
		t.Errorf("currentLocationId is empty, want a zone")
	}

	if !strings.HasPrefix(got.CurrentLocationId, testLocation+"-") {
		t.Errorf("currentLocationId = %q, want a zone within %q", got.CurrentLocationId, testLocation)
	}

	if got.LocationId != got.CurrentLocationId {
		t.Errorf("locationId = %q, currentLocationId = %q, want them equal for BASIC", got.LocationId, got.CurrentLocationId)
	}

	if !strings.HasPrefix(got.PersistenceIamIdentity, "serviceAccount:") {
		t.Errorf("persistenceIamIdentity = %q, want a serviceAccount: identity", got.PersistenceIamIdentity)
	}
}

// TestSDKMemorystoreNetworkDefaults guards the defaults real Memorystore applies
// when connect/network fields are omitted: authorizedNetwork falls back to the
// full "default" network path and connectMode to DIRECT_PEERING.
func TestSDKMemorystoreNetworkDefaults(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
	}).InstanceId("default-net").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("default-net")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.AuthorizedNetwork != "projects/"+testProject+"/global/networks/default" {
		t.Errorf("authorizedNetwork = %q, want the default network path", got.AuthorizedNetwork)
	}

	if got.ConnectMode != "DIRECT_PEERING" {
		t.Errorf("connectMode = %q, want DIRECT_PEERING", got.ConnectMode)
	}

	// BASIC (standalone) instances have no read replica endpoint.
	if got.ReadEndpoint != "" {
		t.Errorf("readEndpoint = %q, want empty for BASIC tier", got.ReadEndpoint)
	}
}

// TestSDKMemorystoreReadEndpointStandardHA guards that instances with read
// replicas enabled expose a read endpoint distinct from the primary host.
func TestSDKMemorystoreReadEndpointStandardHA(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:             "STANDARD_HA",
		MemorySizeGb:     5,
		ReadReplicasMode: "READ_REPLICAS_ENABLED",
	}).InstanceId("ha-cache").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("ha-cache")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ReadEndpoint == "" {
		t.Fatalf("readEndpoint is empty, want a Standard-tier read endpoint")
	}

	if net.ParseIP(got.ReadEndpoint) == nil {
		t.Errorf("readEndpoint = %q, want an IP address", got.ReadEndpoint)
	}

	if got.ReadEndpoint == got.Host {
		t.Errorf("readEndpoint = %q must differ from primary host %q", got.ReadEndpoint, got.Host)
	}

	if got.ReadEndpointPort != 6379 {
		t.Errorf("readEndpointPort = %d, want 6379", got.ReadEndpointPort)
	}
}
