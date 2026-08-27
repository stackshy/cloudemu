package memorystore_test

import (
	"context"
	"fmt"
	"testing"

	redis "google.golang.org/api/redis/v1"
)

// TestSDKMemorystoreRedisConfigsRoundTrip (B1) guards that redis_configs (e.g.
// maxmemory-policy) round-trips on create and get.
func TestSDKMemorystoreRedisConfigsRoundTrip(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
		RedisConfigs: map[string]string{"maxmemory-policy": "allkeys-lru"},
	}).InstanceId("cfg").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("cfg")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.RedisConfigs["maxmemory-policy"] != "allkeys-lru" {
		t.Fatalf("redisConfigs did not round-trip: %v", got.RedisConfigs)
	}
}

// TestSDKMemorystorePatchMaskHonored (B2) guards that PATCH applies only the
// fields named in updateMask: a body memorySizeGb outside the mask must not
// resize the instance.
func TestSDKMemorystorePatchMaskHonored(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 3,
		DisplayName:  "before",
	}).InstanceId("masked").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// updateMask names only displayName, yet the body also sets memorySizeGb=9.
	if _, err := svc.Projects.Locations.Instances.Patch(instanceName("masked"), &redis.Instance{
		DisplayName:  "after",
		MemorySizeGb: 9,
	}).UpdateMask("displayName").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("masked")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.DisplayName != "after" {
		t.Errorf("displayName = %q, want it updated to %q", got.DisplayName, "after")
	}

	if got.MemorySizeGb != 3 {
		t.Errorf("memorySizeGb = %d, want it UNCHANGED at 3 (outside updateMask)", got.MemorySizeGb)
	}
}

// TestSDKMemorystorePatchLabelsClearable (B3) guards that updateMask=labels
// whole-replaces the label set, so a removed label disappears (not merge-only).
func TestSDKMemorystorePatchLabelsClearable(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
		Labels:       map[string]string{"env": "prod", "team": "core"},
	}).InstanceId("lbl").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Replace the label set with just env=prod; team must be dropped.
	if _, err := svc.Projects.Locations.Instances.Patch(instanceName("lbl"), &redis.Instance{
		Labels: map[string]string{"env": "prod"},
	}).UpdateMask("labels").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("lbl")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if _, ok := got.Labels["team"]; ok {
		t.Errorf("label team still present after whole-replace patch: %v", got.Labels)
	}

	if got.Labels["env"] != "prod" {
		t.Errorf("label env = %q, want prod", got.Labels["env"])
	}
}

// TestSDKMemorystorePatchNoMaskPreservesMapFields guards the regression where a
// PATCH omitting updateMask entirely silently wiped labels/redisConfigs: with no
// mask the map fields must MERGE FORWARD (unspecified entries survive).
func TestSDKMemorystorePatchNoMaskPreservesMapFields(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "BASIC",
		MemorySizeGb: 1,
		Labels:       map[string]string{"env": "prod"},
		RedisConfigs: map[string]string{"maxmemory-policy": "allkeys-lru"},
	}).InstanceId("nomask").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// PATCH with NO updateMask, changing only displayName. The body carries no
	// labels/redisConfigs, so they must be preserved (not whole-replaced away).
	if _, err := svc.Projects.Locations.Instances.Patch(instanceName("nomask"), &redis.Instance{
		DisplayName: "renamed",
	}).Context(ctx).Do(); err != nil {
		t.Fatalf("Patch (no updateMask): %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("nomask")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.DisplayName != "renamed" {
		t.Errorf("displayName = %q, want renamed", got.DisplayName)
	}

	if got.Labels["env"] != "prod" {
		t.Errorf("labels = %v, want env=prod preserved after no-mask patch", got.Labels)
	}

	if got.RedisConfigs["maxmemory-policy"] != "allkeys-lru" {
		t.Errorf("redisConfigs = %v, want maxmemory-policy preserved after no-mask patch", got.RedisConfigs)
	}
}

// TestSDKMemorystorePatchReplicaCountZero guards that replicaCount=0 under an
// explicit updateMask=replicaCount is applied (0 is a valid "no replicas"
// setting), not swallowed as if unset.
func TestSDKMemorystorePatchReplicaCountZero(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:         "STANDARD_HA",
		MemorySizeGb: 5,
		ReplicaCount: 2,
	}).InstanceId("rc").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Projects.Locations.Instances.Patch(instanceName("rc"), &redis.Instance{
		ReplicaCount: 0,
	}).UpdateMask("replicaCount").Context(ctx).Do(); err != nil {
		t.Fatalf("Patch: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("rc")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ReplicaCount != 0 {
		t.Errorf("replicaCount = %d, want 0 applied under explicit updateMask", got.ReplicaCount)
	}
}

// TestSDKMemorystoreSecurityFieldsRoundTrip (B4) guards that authEnabled,
// transitEncryptionMode and replicaCount round-trip on create and get.
func TestSDKMemorystoreSecurityFieldsRoundTrip(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:                  "STANDARD_HA",
		MemorySizeGb:          5,
		AuthEnabled:           true,
		TransitEncryptionMode: "SERVER_AUTHENTICATION",
		ReplicaCount:          2,
	}).InstanceId("sec").Context(ctx).Do(); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := svc.Projects.Locations.Instances.Get(instanceName("sec")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if !got.AuthEnabled {
		t.Errorf("authEnabled = false, want true")
	}

	if got.TransitEncryptionMode != "SERVER_AUTHENTICATION" {
		t.Errorf("transitEncryptionMode = %q, want SERVER_AUTHENTICATION", got.TransitEncryptionMode)
	}

	if got.ReplicaCount != 2 {
		t.Errorf("replicaCount = %d, want 2", got.ReplicaCount)
	}
}

// TestSDKMemorystoreReadEndpointGating (B5) guards that the read endpoint is
// gated on readReplicasMode, not tier: a STANDARD_HA instance with replicas
// disabled has no read endpoint, while one with replicas enabled does.
func TestSDKMemorystoreReadEndpointGating(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:             "STANDARD_HA",
		MemorySizeGb:     5,
		ReadReplicasMode: "READ_REPLICAS_DISABLED",
	}).InstanceId("no-rr").Context(ctx).Do(); err != nil {
		t.Fatalf("Create no-rr: %v", err)
	}

	noRR, err := svc.Projects.Locations.Instances.Get(instanceName("no-rr")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get no-rr: %v", err)
	}

	if noRR.ReadEndpoint != "" {
		t.Errorf("readEndpoint = %q, want empty when read replicas disabled", noRR.ReadEndpoint)
	}

	if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
		Tier:             "STANDARD_HA",
		MemorySizeGb:     5,
		ReadReplicasMode: "READ_REPLICAS_ENABLED",
	}).InstanceId("rr").Context(ctx).Do(); err != nil {
		t.Fatalf("Create rr: %v", err)
	}

	rr, err := svc.Projects.Locations.Instances.Get(instanceName("rr")).Context(ctx).Do()
	if err != nil {
		t.Fatalf("Get rr: %v", err)
	}

	if rr.ReadEndpoint == "" || rr.ReadEndpointPort == 0 {
		t.Errorf("read endpoint missing with replicas enabled: endpoint=%q port=%d",
			rr.ReadEndpoint, rr.ReadEndpointPort)
	}
}

// TestSDKMemorystoreListPagination (B6) guards that List honors pageSize and
// pageToken: paging through with a small page size visits every instance exactly
// once and terminates with an empty nextPageToken.
func TestSDKMemorystoreListPagination(t *testing.T) {
	svc := newRedisService(t)
	ctx := context.Background()

	const total = 7

	for i := range total {
		id := fmt.Sprintf("inst-%02d", i)
		if _, err := svc.Projects.Locations.Instances.Create(parent(), &redis.Instance{
			Tier:         "BASIC",
			MemorySizeGb: 1,
		}).InstanceId(id).Context(ctx).Do(); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	seen := make(map[string]int)
	pages := 0
	token := ""

	for {
		call := svc.Projects.Locations.Instances.List(parent()).PageSize(3).Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}

		resp, err := call.Do()
		if err != nil {
			t.Fatalf("List page %d: %v", pages, err)
		}

		for _, inst := range resp.Instances {
			seen[inst.Name]++
		}

		pages++
		token = resp.NextPageToken

		if token == "" {
			break
		}

		if pages > total+1 {
			t.Fatalf("pagination did not terminate after %d pages", pages)
		}
	}

	if len(seen) != total {
		t.Fatalf("saw %d distinct instances, want %d", len(seen), total)
	}

	for name, count := range seen {
		if count != 1 {
			t.Errorf("instance %s returned %d times, want exactly once", name, count)
		}
	}

	if pages < 2 {
		t.Errorf("expected multiple pages for %d instances at pageSize 3, got %d", total, pages)
	}
}
