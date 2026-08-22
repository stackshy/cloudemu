package compute

import (
	"net/http"

	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
)

// machineTypeSpec is one entry in the static machine-type catalog. gcloud and
// Terraform sometimes validate a machine type exists (machineTypes.get) or
// enumerate the catalog (machineTypes.list) before creating an instance; a
// fixed common set satisfies that without a live provider lookup.
type machineTypeSpec struct {
	name        string
	guestCPUs   int64
	memoryMb    int64
	description string
}

// machineTypeCatalog is the representative machine-type set the handler reports.
// Memory follows GCP's published specs (n1/n2 standard = ~3.75 GB/vCPU; e2
// standard = 4 GB/vCPU).
var machineTypeCatalog = []machineTypeSpec{ //nolint:gochecknoglobals // static lookup table
	{"e2-micro", 2, 1024, "Efficient Instance, 2 vCPUs (shared), 1 GB RAM"},
	{"e2-small", 2, 2048, "Efficient Instance, 2 vCPUs (shared), 2 GB RAM"},
	{"e2-medium", 2, 4096, "Efficient Instance, 2 vCPUs (shared), 4 GB RAM"},
	{"e2-standard-2", 2, 8192, "Efficient Instance, 2 vCPUs, 8 GB RAM"},
	{"e2-standard-4", 4, 16384, "Efficient Instance, 4 vCPUs, 16 GB RAM"},
	{"n1-standard-1", 1, 3840, "1 vCPU, 3.75 GB RAM"},
	{"n1-standard-2", 2, 7680, "2 vCPUs, 7.5 GB RAM"},
	{"n1-standard-4", 4, 15360, "4 vCPUs, 15 GB RAM"},
	{"n2-standard-2", 2, 8192, "2 vCPUs, 8 GB RAM"},
	{"n2-standard-4", 4, 16384, "4 vCPUs, 16 GB RAM"},
	{"f1-micro", 1, 614, "1 vCPU (shared), 0.6 GB RAM"},
	{"g1-small", 1, 1740, "1 vCPU (shared), 1.7 GB RAM"},
}

type machineTypeResponse struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	GuestCpus   int64  `json:"guestCpus"`
	MemoryMb    int64  `json:"memoryMb"`
	Zone        string `json:"zone"`
	SelfLink    string `json:"selfLink"`
}

type machineTypeListResponse struct {
	Kind     string                `json:"kind"`
	ID       string                `json:"id"`
	Items    []machineTypeResponse `json:"items"`
	SelfLink string                `json:"selfLink"`
}

//nolint:gocritic // rp is a request-scoped value
func serveMachineTypesRoute(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if r.Method != http.MethodGet {
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
		return
	}

	if rp.Scope != gcprest.ScopeZones {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "machineTypes are zonal resources")
		return
	}

	if rp.ResourceName == "" {
		listMachineTypes(w, r, rp)
		return
	}

	getMachineType(w, r, rp)
}

//nolint:gocritic // rp is a request-scoped value
func getMachineType(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	for i := range machineTypeCatalog {
		if machineTypeCatalog[i].name == rp.ResourceName {
			gcprest.WriteJSON(w, http.StatusOK, toMachineTypeResponse(&machineTypeCatalog[i], rp, hostFromRequest(r)))
			return
		}
	}

	gcprest.WriteError(w, http.StatusNotFound, "notFound", "unknown machine type: "+rp.ResourceName)
}

//nolint:gocritic // rp is a request-scoped value
func listMachineTypes(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	host := hostFromRequest(r)
	items := make([]machineTypeResponse, 0, len(machineTypeCatalog))

	for i := range machineTypeCatalog {
		items = append(items, toMachineTypeResponse(&machineTypeCatalog[i], rp, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, machineTypeListResponse{
		Kind:     "compute#machineTypeList",
		ID:       "projects/" + rp.Project + "/zones/" + rp.ScopeName + "/machineTypes",
		Items:    items,
		SelfLink: gcprest.SelfLink(host, rp.Project, gcprest.ScopeZones, rp.ScopeName, "machineTypes", ""),
	})
}

//nolint:gocritic // rp is a request-scoped value
func toMachineTypeResponse(spec *machineTypeSpec, rp gcprest.ResourcePath, host string) machineTypeResponse {
	return machineTypeResponse{
		Kind:        "compute#machineType",
		ID:          numericID(rp.ScopeName + "/" + spec.name),
		Name:        spec.name,
		Description: spec.description,
		GuestCpus:   spec.guestCPUs,
		MemoryMb:    spec.memoryMb,
		Zone:        host + "/compute/v1/projects/" + rp.Project + "/zones/" + rp.ScopeName,
		SelfLink:    gcprest.SelfLink(host, rp.Project, gcprest.ScopeZones, rp.ScopeName, "machineTypes", spec.name),
	}
}
