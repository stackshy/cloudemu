// Package ucstorage emulates the Databricks Unity Catalog storage/governance
// REST APIs (Metastores, External Locations, Storage Credentials, Volumes)
// served under /api/2.1/unity-catalog/... as a server.Handler. Point the real
// github.com/databricks/databricks-sdk-go WorkspaceClient at a server
// registered with this handler and w.Metastores, w.ExternalLocations,
// w.StorageCredentials, and w.Volumes work end-to-end against an in-memory
// backend.
//
// Covered endpoints:
//
//	POST   /api/2.1/unity-catalog/metastores              create
//	GET    /api/2.1/unity-catalog/metastores              list
//	GET    /api/2.1/unity-catalog/metastores/{id}         get
//	PATCH  /api/2.1/unity-catalog/metastores/{id}         update
//	DELETE /api/2.1/unity-catalog/metastores/{id}         delete
//	POST   /api/2.1/unity-catalog/external-locations          create
//	GET    /api/2.1/unity-catalog/external-locations          list
//	GET    /api/2.1/unity-catalog/external-locations/{name}   get
//	PATCH  /api/2.1/unity-catalog/external-locations/{name}   update
//	DELETE /api/2.1/unity-catalog/external-locations/{name}   delete
//	POST   /api/2.1/unity-catalog/storage-credentials         create
//	GET    /api/2.1/unity-catalog/storage-credentials         list
//	GET    /api/2.1/unity-catalog/storage-credentials/{name}  get
//	PATCH  /api/2.1/unity-catalog/storage-credentials/{name}  update
//	DELETE /api/2.1/unity-catalog/storage-credentials/{name}  delete
//	POST   /api/2.1/unity-catalog/volumes                      create
//	GET    /api/2.1/unity-catalog/volumes                      list
//	GET    /api/2.1/unity-catalog/volumes/{full_name}          get
//	PATCH  /api/2.1/unity-catalog/volumes/{full_name}          update
//	DELETE /api/2.1/unity-catalog/volumes/{full_name}          delete
package ucstorage

import (
	"net/http"
	"sync"
)

const maxBodyBytes = 5 << 20

// Unity Catalog path constants. ucRoot is the segment that precedes the
// per-resource collection segment.
const (
	ucAPI  = "api"
	ucRoot = "unity-catalog"
)

// Resource collection segments claimed by this handler.
const (
	resMetastores         = "metastores"
	resExternalLocations  = "external-locations"
	resStorageCredentials = "storage-credentials"
	resVolumes            = "volumes"
)

// Segment counts after splitPath. collectionSegs is [api, ver, unity-catalog,
// resource]; itemSegs adds the trailing {id|name|full_name} segment.
const (
	collectionSegs = 4
	itemSegs       = 5
)

// idxRoot, idxResource, and idxItem index splitPath output.
const (
	idxRoot     = 2
	idxResource = 3
	idxItem     = 4
)

// Databricks error codes.
const (
	codeNotFound      = "RESOURCE_DOES_NOT_EXIST"
	codeAlreadyExists = "RESOURCE_ALREADY_EXISTS"
	codeInvalidParam  = "INVALID_PARAMETER_VALUE"
	codeMalformed     = "MALFORMED_REQUEST"
	codeNotFoundPath  = "ENDPOINT_NOT_FOUND"
)

// stubMetastoreID is the parent metastore id reported on created resources.
const stubMetastoreID = "metastore-cloudemu"

// Handler serves the Unity Catalog storage/governance REST API from in-memory
// state. It is safe for concurrent use.
type Handler struct {
	mu          sync.RWMutex
	metastores  map[string]*metastore
	extLocs     map[string]*externalLocation
	credentials map[string]*storageCredential
	volumes     map[string]*volume
}

// New returns a ready-to-use handler with empty state.
func New() *Handler {
	return &Handler{
		metastores:  make(map[string]*metastore),
		extLocs:     make(map[string]*externalLocation),
		credentials: make(map[string]*storageCredential),
		volumes:     make(map[string]*volume),
	}
}

// Matches claims only /api/{ver}/unity-catalog/{metastores,external-locations,
// storage-credentials,volumes}[/...] paths. Catalog/schema/table paths owned by
// the sibling unitycatalog handler are deliberately left unclaimed.
func (*Handler) Matches(r *http.Request) bool {
	parts := splitPath(r.URL.Path)
	if len(parts) < collectionSegs || parts[0] != ucAPI || parts[idxRoot] != ucRoot {
		return false
	}

	switch parts[idxResource] {
	case resMetastores, resExternalLocations, resStorageCredentials, resVolumes:
		return true
	default:
		return false
	}
}

// ServeHTTP routes by resource and by the presence of an item segment.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := splitPath(r.URL.Path)
	if len(parts) < collectionSegs {
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unsupported path")

		return
	}

	item := ""
	if len(parts) >= itemSegs {
		item = parts[idxItem]
	}

	switch parts[idxResource] {
	case resMetastores:
		h.serveMetastores(w, r, item)
	case resExternalLocations:
		h.serveExternalLocations(w, r, item)
	case resStorageCredentials:
		h.serveStorageCredentials(w, r, item)
	case resVolumes:
		h.serveVolumes(w, r, item)
	default:
		writeError(w, http.StatusNotFound, codeNotFoundPath, "unknown resource: "+parts[idxResource])
	}
}
