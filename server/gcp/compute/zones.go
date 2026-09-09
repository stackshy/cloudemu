package compute

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// zoneResponse mirrors the subset of compute#zone clients read. Terraform's
// google_compute_instance calls zones.get to resolve (and validate) the zone
// before creating an instance, so the emulator must answer it with a live
// (status UP) zone rather than 501.
type zoneResponse struct {
	Kind                  string   `json:"kind"`
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Description           string   `json:"description,omitempty"`
	Status                string   `json:"status"`
	Region                string   `json:"region"`
	SelfLink              string   `json:"selfLink"`
	AvailableCPUPlatforms []string `json:"availableCpuPlatforms,omitempty"`
	SupportsPzs           bool     `json:"supportsPzs"`
}

// regionResponse mirrors the subset of compute#region clients read.
type regionResponse struct {
	Kind        string   `json:"kind"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status"`
	SelfLink    string   `json:"selfLink"`
	Zones       []string `json:"zones,omitempty"`
	SupportsPzs bool     `json:"supportsPzs"`
}

// statusUp is GCP's compute#zone / compute#region "operational" status.
const statusUp = "UP"

// regionZoneSuffixes are the zone letters GCP publishes for a typical region;
// used to synthesize a region's zones[] list.
var regionZoneSuffixes = []string{"a", "b", "c"} //nolint:gochecknoglobals // static lookup table

// serveScopeResource answers zones.get / regions.get for a bare zone/region
// path. CloudEmu is project-and-zone agnostic, so any well-formed zone or
// region name is reported UP (matching an emulator's "any region works"
// posture); only GET is supported.
//
//nolint:gocritic // rp is a request-scoped value
func serveScopeResource(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if r.Method != http.MethodGet {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	host := hostFromRequest(r)

	if rp.Scope == gcprest.ScopeRegions {
		gcprest.WriteJSON(w, http.StatusOK, toRegionResponse(rp, host))
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toZoneResponse(rp, host))
}

//nolint:gocritic // rp is a request-scoped value
func toZoneResponse(rp gcprest.ResourcePath, host string) zoneResponse {
	region := regionFromZone(rp.ScopeName)

	return zoneResponse{
		Kind:                  "compute#zone",
		ID:                    numericID("zone/" + rp.ScopeName),
		Name:                  rp.ScopeName,
		Description:           rp.ScopeName,
		Status:                statusUp,
		Region:                host + "/compute/v1/projects/" + rp.Project + "/regions/" + region,
		SelfLink:              host + "/compute/v1/projects/" + rp.Project + "/zones/" + rp.ScopeName,
		AvailableCPUPlatforms: []string{defaultCPUPlatform},
	}
}

//nolint:gocritic // rp is a request-scoped value
func toRegionResponse(rp gcprest.ResourcePath, host string) regionResponse {
	base := host + "/compute/v1/projects/" + rp.Project

	zones := make([]string, 0, len(regionZoneSuffixes))
	for _, s := range regionZoneSuffixes {
		zones = append(zones, base+"/zones/"+rp.ScopeName+"-"+s)
	}

	return regionResponse{
		Kind:        "compute#region",
		ID:          numericID("region/" + rp.ScopeName),
		Name:        rp.ScopeName,
		Description: rp.ScopeName,
		Status:      statusUp,
		SelfLink:    base + "/regions/" + rp.ScopeName,
		Zones:       zones,
	}
}
