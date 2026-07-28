package ec2

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// Callers place subnets one per zone, so a single-zone answer silently
// collapses a multi-AZ network into one — and the resources that require two
// (managed-database subnet groups, cluster control planes) fail much later with
// an error that says nothing about zones.
func TestDescribeAvailabilityZonesReturnsSeveral(t *testing.T) {
	h := newFullHandler()

	resp := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeAvailabilityZones"}})
	if resp.Code != http.StatusOK {
		t.Fatalf("DescribeAvailabilityZones = %d: %s", resp.Code, resp.Body.String())
	}

	body := resp.Body.String()

	if n := strings.Count(body, "<zoneName>"); n < 2 {
		t.Errorf("got %d zones, want at least 2: %s", n, body)
	}

	if !strings.Contains(body, "<zoneState>available</zoneState>") {
		t.Errorf("zones should be available: %s", body)
	}
}

// The region a zone reports has to match the one the caller is working in, or
// a caller filtering by region discards every zone it was just given.
func TestAvailabilityZonesCarryTheRequestRegion(t *testing.T) {
	h := newFullHandler()

	resp := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeAvailabilityZones"}})

	body := resp.Body.String()
	region := between(body, "<regionName>", "</regionName>")

	if region == "" {
		t.Fatalf("no region reported: %s", body)
	}

	zone := between(body, "<zoneName>", "</zoneName>")
	if !strings.HasPrefix(zone, region) {
		t.Errorf("zone %q does not belong to region %q", zone, region)
	}
}

// An address is reserved, read back, then released. The release matters most:
// an allocation that outlives its teardown is a charge that keeps accruing.
func TestElasticIPLifecycle(t *testing.T) {
	h := newFullHandler()

	alloc := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"AllocateAddress"}, "Domain": {"vpc"},
	})
	if alloc.Code != http.StatusOK {
		t.Fatalf("AllocateAddress = %d: %s", alloc.Code, alloc.Body.String())
	}

	id := between(alloc.Body.String(), "<allocationId>", "</allocationId>")
	if id == "" {
		t.Fatalf("no allocation id: %s", alloc.Body.String())
	}

	if ip := between(alloc.Body.String(), "<publicIp>", "</publicIp>"); ip == "" {
		t.Error("allocation carries no public IP")
	}

	desc := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeAddresses"}})
	if !strings.Contains(desc.Body.String(), id) {
		t.Errorf("allocation %s missing from describe: %s", id, desc.Body.String())
	}

	rel := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"ReleaseAddress"}, "AllocationId": {id},
	})
	if rel.Code != http.StatusOK {
		t.Fatalf("ReleaseAddress = %d: %s", rel.Code, rel.Body.String())
	}

	after := do(t, h, http.MethodPost, "/", url.Values{"Action": {"DescribeAddresses"}})
	if strings.Contains(after.Body.String(), id) {
		t.Errorf("allocation %s survived release: %s", id, after.Body.String())
	}

	again := do(t, h, http.MethodPost, "/", url.Values{
		"Action": {"ReleaseAddress"}, "AllocationId": {id},
	})
	if again.Code == http.StatusOK {
		t.Errorf("releasing an already-released address should fail: %s", again.Body.String())
	}
}
