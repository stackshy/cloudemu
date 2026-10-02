package filestore

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/gcp/sharedpath"
)

// Memorystore for Redis serves the same .../locations/{l}/instances paths.
// When both are mounted, Filestore also claims every zonal location (Redis,
// Data Fusion and Secure Source Manager are regional only) and any create
// whose tier is not a Redis one, so a bad Filestore tier gets Filestore's 400.

// SetSharedPath turns on the shared instances rules.
func (h *Handler) SetSharedPath() { h.shared = true }

// claimsShared reports the extra requests Filestore claims in a shared server.
func (h *Handler) claimsShared(r *http.Request, rt route) bool {
	if !h.shared {
		return false
	}

	if sharedpath.IsZone(rt.location) {
		return true
	}

	return rt.name == "" && r.Method == http.MethodPost && bodyHasFilestoreTier(r)
}

// bodyHasFilestoreTier reports a create whose tier is a Filestore tier, or is
// set with no Redis signal at all. The body is restored.
func bodyHasFilestoreTier(r *http.Request) bool {
	if r.Body == nil {
		return false
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxProbeBytes))
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))

	if err != nil {
		return false
	}

	var probe struct {
		Tier         string          `json:"tier"`
		MemorySizeGb json.RawMessage `json:"memorySizeGb"`
		RedisVersion string          `json:"redisVersion"`
		RedisConfigs json.RawMessage `json:"redisConfigs"`
	}

	if json.Unmarshal(raw, &probe) != nil || probe.Tier == "" {
		return false
	}

	if probe.Tier == "BASIC" || probe.Tier == "STANDARD_HA" {
		return false // the Redis tiers
	}

	return isFilestoreTier(probe.Tier) ||
		(len(probe.MemorySizeGb) == 0 && probe.RedisVersion == "" && len(probe.RedisConfigs) == 0)
}

// isFilestoreTier reports whether tier names a Filestore tier.
func isFilestoreTier(tier string) bool {
	for n, name := range tierNames {
		if n != 0 && name == tier {
			return true
		}
	}

	return false
}
