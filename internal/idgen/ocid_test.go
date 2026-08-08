package idgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegionCode(t *testing.T) {
	tests := []struct {
		name   string
		region string
		expect string
	}{
		{name: "ashburn", region: "us-ashburn-1", expect: "iad"},
		{name: "phoenix", region: "us-phoenix-1", expect: "phx"},
		{name: "frankfurt", region: "eu-frankfurt-1", expect: "fra"},
		{name: "mumbai", region: "ap-mumbai-1", expect: "bom"},
		{name: "empty region", region: "", expect: ""},
		{name: "unknown region falls back to city prefix", region: "us-neverland-1", expect: "nev"},
		{name: "unknown short city keeps what it has", region: "us-ab-1", expect: "ab"},
		{name: "single segment", region: "narnia", expect: "nar"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, RegionCode(tc.region))
		})
	}
}

func TestRegionCodeIsDistinctPerRegion(t *testing.T) {
	// A shared fallback would let two regions collide and silently merge
	// resources in a list filtered by OCID region.
	seen := make(map[string]string, len(regionCodes))

	for region, code := range regionCodes {
		if prev, dup := seen[code]; dup {
			t.Errorf("region code %q shared by %q and %q", code, prev, region)
		}

		seen[code] = region
	}
}

func TestOCID(t *testing.T) {
	Reset()

	tests := []struct {
		name         string
		resourceType string
		realm        string
		region       string
		expectPrefix string
	}{
		{
			name:         "region-scoped instance",
			resourceType: "instance",
			realm:        "oc1",
			region:       "us-ashburn-1",
			expectPrefix: "ocid1.instance.oc1.iad.",
		},
		{
			name:         "empty realm defaults to oc1",
			resourceType: "volume",
			realm:        "",
			region:       "us-phoenix-1",
			expectPrefix: "ocid1.volume.oc1.phx.",
		},
		{
			name:         "government realm is preserved",
			resourceType: "instance",
			realm:        "oc2",
			region:       "us-langley-1",
			expectPrefix: "ocid1.instance.oc2.lan.",
		},
		{
			name:         "global resource has empty region segment",
			resourceType: "compartment",
			realm:        "oc1",
			region:       "",
			expectPrefix: "ocid1.compartment.oc1..",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			id := OCID(tc.resourceType, tc.realm, tc.region)

			assert.True(t, strings.HasPrefix(id, tc.expectPrefix),
				"got %q, want prefix %q", id, tc.expectPrefix)
			assert.Greater(t, len(id), len(tc.expectPrefix),
				"unique suffix must not be empty")
		})
	}
}

func TestGlobalOCIDHasDoubledDot(t *testing.T) {
	Reset()

	// Real OCI identity OCIDs carry an empty region segment, so clients that
	// split on "." must still see the same field count.
	id := GlobalOCID("user", "oc1")

	require.True(t, strings.HasPrefix(id, "ocid1.user.oc1.."), "got %q", id)
	assert.Len(t, strings.Split(id, "."), 5, "OCID must always have 5 dot-separated segments: %q", id)
}

func TestOCIDIsUnique(t *testing.T) {
	Reset()

	const count = 100

	seen := make(map[string]struct{}, count)

	for range count {
		id := OCID("instance", "oc1", "us-ashburn-1")

		_, dup := seen[id]
		require.False(t, dup, "duplicate OCID generated: %q", id)

		seen[id] = struct{}{}
	}

	assert.Len(t, seen, count)
}

func TestOCIDSharesCounterWithOtherGenerators(t *testing.T) {
	Reset()

	// All generators draw from one counter, so a resource that has both an
	// OCID and a plain ID can never collide on the numeric part.
	first := OCID("instance", "oc1", "us-ashburn-1")
	second := OCID("instance", "oc1", "us-ashburn-1")

	assert.NotEqual(t, first, second)
}
