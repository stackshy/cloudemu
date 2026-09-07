// Package compute implements OCI's Core Compute and Block Volume REST API
// against a CloudEmu compute driver. Real github.com/oracle/oci-go-sdk core
// clients hit this handler the same way they hit
// iaas.<region>.oraclecloud.com.
//
// The /20160918 prefix is the Core Services API version, shared with
// Networking, so Matches claims only the compute and block storage
// collections and leaves server/oci/vcn's to it:
//
//	POST/GET             /20160918/instances                    — launch, list
//	GET/PUT              /20160918/instances/{id}               — get, update
//	POST                 /20160918/instances/{id}?action=…      — START/STOP/SOFTSTOP/RESET/SOFTRESET
//	DELETE               /20160918/instances/{id}               — terminate
//	GET                  /20160918/shapes                       — list
//	POST/GET             /20160918/images[/{id}]                — create, list, get, update, delete
//	POST/GET/PUT/DELETE  /20160918/volumes[/{id}]
//	POST/GET/DELETE      /20160918/volumeAttachments[/{id}]
//	POST/GET/PUT/DELETE  /20160918/bootVolumes[/{id}]
//	POST/GET/DELETE      /20160918/bootVolumeAttachments[/{id}]
//	POST/GET/PUT/DELETE  /20160918/volumeBackups[/{id}]
//	POST/GET/PUT/DELETE  /20160918/bootVolumeBackups[/{id}]
//	POST/GET/PUT/DELETE  /20160918/volumeGroups[/{id}]
//	POST/GET/DELETE      /20160918/vnicAttachments[/{id}]
//	POST/GET/PUT/DELETE  /20160918/instanceConfigurations[/{id}]
//	POST                 /20160918/instanceConfigurations/{id}/actions/launch
//	POST/GET/PUT/DELETE  /20160918/instancePools[/{id}]
//	POST                 /20160918/instancePools/{id}/actions/{start,stop,reset,softreset}
//	GET                  /20160918/instancePools/{id}/instances
//	POST                 /20160918/{collection}/{id}/actions/changeCompartment
//
// Not emulated: /volumeBackupPolicies, /computeCapacityReservations,
// /dedicatedVmHosts and /instanceConsoleConnections, which the compute driver
// has no shape for — the handler claims them anyway so a caller gets a 501
// naming the gap rather than a bare 404. There is no key pair collection: OCI
// models no key pair resource, and an SSH public key travels as the
// ssh_authorized_keys instance metadata entry.
//
// Autoscaling policies live in OCI's separate Autoscaling API (/20181001),
// which this handler does not claim; the compute driver stores them so the
// portable API round-trips them.
package compute

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
	"github.com/stackshy/cloudemu/v2/services/scope"
)

// apiVersion is the Core Services API version every compute path carries.
const apiVersion = "20160918"

// Collections this handler claims.
const (
	segInstances             = "instances"
	segShapes                = "shapes"
	segImages                = "images"
	segVolumes               = "volumes"
	segVolumeAttachments     = "volumeAttachments"
	segBootVolumes           = "bootVolumes"
	segBootVolumeAttachments = "bootVolumeAttachments"
	segVolumeBackups         = "volumeBackups"
	segBootVolumeBackups     = "bootVolumeBackups"
	segVolumeGroups          = "volumeGroups"
	segVNICAttachments       = "vnicAttachments"
	segInstanceConfigs       = "instanceConfigurations"
	segInstancePools         = "instancePools"
)

// Collections this handler claims in order to report them as unemulated, so a
// caller reaching for one is told why rather than left with a bare 404.
const (
	segBackupPolicies       = "volumeBackupPolicies"
	segCapacityReservations = "computeCapacityReservations"
	segDedicatedVMHosts     = "dedicatedVmHosts"
	segConsoleConnections   = "instanceConsoleConnections"
)

// Sub-collections and actions.
const (
	subActions   = "actions"
	subInstances = "instances"

	actionChangeCompartment = "changeCompartment"
	actionLaunch            = "launch"
)

// Error codes the handler raises itself.
const (
	codeInvalidParameter = "InvalidParameter"
	codeMethodNotAllowed = "MethodNotAllowed"
	codeNotImplemented   = "NotImplemented"
	codeNotFound         = "NotAuthorizedOrNotFound"
)

// maxPathSegments is /{version}/{collection}/{id}/{sub}/{action}.
const maxPathSegments = 5

// Extras is the OCI-only surface the portable compute driver cannot express:
// compartments, creation times, shapes, boot volumes, attachments as
// first-class resources, volume groups, and the OCID-addressed instance
// configurations and pools. *providers/oci/compute.Mock satisfies it; any
// driver that does not is served 501 for every path this handler claims.
type Extras interface {
	Scope(id string) scope.Scope
	SetScope(id string, s scope.Scope)
	Created(id string) string
	SetTags(id string, tags map[string]string) error

	InstanceDetails(id string) (ocicompute.InstanceDetails, bool)
	SetInstanceDetails(id string, d ocicompute.InstanceDetails) error
	TerminateInstance(ctx context.Context, id string, preserveBootVolume bool) error

	ListShapes(ctx context.Context, imageID string) ([]ocicompute.Shape, error)
	Shape(name string) (ocicompute.Shape, bool)

	GetImage(ctx context.Context, id string) (*ocicompute.Image, error)
	ListImages(ctx context.Context, compartmentID, operatingSystem, osVersion string) ([]ocicompute.Image, error)
	UpdateImage(ctx context.Context, id string, upd ocicompute.Update) (*ocicompute.Image, error)

	CreateVolumeFrom(
		ctx context.Context, cfg computedriver.VolumeConfig, source ocicompute.SourceDetails,
	) (*computedriver.VolumeInfo, error)
	UpdateVolume(
		ctx context.Context, id string, upd ocicompute.Update, sizeInGBs, vpusPerGB int,
	) (*computedriver.VolumeInfo, error)
	VolumeSource(id string) (ocicompute.SourceDetails, int)

	AttachVolumeToInstance(
		ctx context.Context, spec ocicompute.VolumeAttachment,
	) (*ocicompute.VolumeAttachment, error)
	GetVolumeAttachment(ctx context.Context, id string) (*ocicompute.VolumeAttachment, error)
	ListVolumeAttachments(
		ctx context.Context, compartmentID, instanceID, volumeID string,
	) ([]ocicompute.VolumeAttachment, error)
	DetachVolumeAttachment(ctx context.Context, id string) error

	CreateBootVolume(ctx context.Context, spec ocicompute.BootVolume) (*ocicompute.BootVolume, error)
	GetBootVolume(ctx context.Context, id string) (*ocicompute.BootVolume, error)
	ListBootVolumes(ctx context.Context, compartmentID string) ([]ocicompute.BootVolume, error)
	UpdateBootVolume(
		ctx context.Context, id string, upd ocicompute.Update, sizeInGBs, vpusPerGB int,
	) (*ocicompute.BootVolume, error)
	DeleteBootVolume(ctx context.Context, id string) error
	ListBootVolumeAttachments(
		ctx context.Context, compartmentID, instanceID, bootVolumeID string,
	) ([]ocicompute.BootVolumeAttachment, error)
	GetBootVolumeAttachment(ctx context.Context, id string) (*ocicompute.BootVolumeAttachment, error)
	AttachBootVolume(
		ctx context.Context, instanceID, bootVolumeID, displayName string,
	) (*ocicompute.BootVolumeAttachment, error)
	DetachBootVolume(ctx context.Context, id string) error

	CreateVolumeBackup(
		ctx context.Context, volumeID, displayName, backupType string, boot bool, tags map[string]string,
	) (*computedriver.SnapshotInfo, error)
	UpdateVolumeBackup(ctx context.Context, id string, upd ocicompute.Update) (*computedriver.SnapshotInfo, error)
	VolumeBackupDetails(id string) (backupType string, boot, ok bool)

	CreateVolumeGroup(ctx context.Context, spec ocicompute.VolumeGroup) (*ocicompute.VolumeGroup, error)
	GetVolumeGroup(ctx context.Context, id string) (*ocicompute.VolumeGroup, error)
	ListVolumeGroups(ctx context.Context, compartmentID string) ([]ocicompute.VolumeGroup, error)
	UpdateVolumeGroup(
		ctx context.Context, id string, upd ocicompute.Update, volumeIDs []string,
	) (*ocicompute.VolumeGroup, error)
	DeleteVolumeGroup(ctx context.Context, id string) error

	ListVNICAttachments(
		ctx context.Context, compartmentID, instanceID, vnicID string,
	) ([]ocicompute.VNICAttachment, error)
	GetVNICAttachment(ctx context.Context, id string) (*ocicompute.VNICAttachment, error)
	AttachVNIC(
		ctx context.Context, instanceID, subnetID, displayName, hostname string, nsgIDs []string,
	) (*ocicompute.VNICAttachment, error)
	DetachVNIC(ctx context.Context, attachmentID string) error

	CreateInstanceConfiguration(
		ctx context.Context, displayName string, launch ocicompute.LaunchSpec, tags map[string]string,
	) (*ocicompute.InstanceConfiguration, error)
	GetInstanceConfiguration(ctx context.Context, id string) (*ocicompute.InstanceConfiguration, error)
	ListInstanceConfigurations(ctx context.Context, compartmentID string) ([]ocicompute.InstanceConfiguration, error)
	UpdateInstanceConfiguration(
		ctx context.Context, id string, upd ocicompute.Update,
	) (*ocicompute.InstanceConfiguration, error)
	DeleteInstanceConfiguration(ctx context.Context, id string) error
	LaunchFromInstanceConfiguration(
		ctx context.Context, id string, overrides *ocicompute.LaunchSpec,
	) (*computedriver.Instance, error)

	CreateInstancePool(
		ctx context.Context, displayName, configurationID string, size int,
		placements []ocicompute.PoolPlacement, tags map[string]string,
	) (*ocicompute.InstancePool, error)
	GetInstancePool(ctx context.Context, id string) (*ocicompute.InstancePool, error)
	ListInstancePools(ctx context.Context, compartmentID string) ([]ocicompute.InstancePool, error)
	UpdateInstancePool(
		ctx context.Context, id string, upd ocicompute.Update, size int,
	) (*ocicompute.InstancePool, error)
	TerminateInstancePool(ctx context.Context, id string) error
	InstancePoolAction(ctx context.Context, id, action string) (*ocicompute.InstancePool, error)
	ListInstancePoolInstances(ctx context.Context, id string) ([]ocicompute.PoolInstance, error)
}

// Handler serves OCI Core Compute and Block Volume against a compute driver.
type Handler struct {
	compute computedriver.Compute
	extras  Extras
	work    *workrequest.Store
}

// New returns a compute handler. work records the asynchronous mutations; a
// nil store leaves the opc-work-request-id header off.
func New(c computedriver.Compute, work *workrequest.Store) *Handler {
	extras, _ := c.(Extras)

	return &Handler{compute: c, extras: extras, work: work}
}

// route is a parsed Core Compute path.
type route struct {
	Collection string
	ID         string
	Sub        string
	Action     string
}

// Matches claims the compute and block storage collections under /20160918,
// and nothing else sharing that prefix — server/oci/vcn owns the networking
// collections and server/oci/workrequest owns /workRequests.
func (*Handler) Matches(r *http.Request) bool {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		return false
	}

	switch rt.Collection {
	case segInstances, segShapes, segImages, segVolumes, segVolumeAttachments,
		segBootVolumes, segBootVolumeAttachments, segVolumeBackups, segBootVolumeBackups,
		segVolumeGroups, segVNICAttachments, segInstanceConfigs, segInstancePools,
		segBackupPolicies, segCapacityReservations, segDedicatedVMHosts, segConsoleConnections:
		return true
	}

	return false
}

// ServeHTTP routes on collection, then on path shape and method.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt, ok := parsePath(r.URL.Path)
	if !ok {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "malformed compute path")
		return
	}

	if h.extras == nil {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"the wired compute driver does not implement OCI compartments")

		return
	}

	if rt.Sub == subActions && rt.Action == actionChangeCompartment {
		h.changeCompartment(w, r, rt.ID)
		return
	}

	h.serveCollection(w, r, rt)
}

// serveCollection dispatches to the handler family for rt's collection.
func (h *Handler) serveCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Collection {
	case segInstances:
		h.serveInstance(w, r, rt)
	case segShapes:
		h.serveShapes(w, r, rt)
	case segInstanceConfigs:
		h.serveInstanceConfig(w, r, rt)
	case segInstancePools:
		h.serveInstancePool(w, r, rt)
	default:
		h.serveStorageCollection(w, r, rt)
	}
}

// serveStorageCollection dispatches the collections whose paths are plain
// CRUD: images and everything Block Volume publishes.
func (h *Handler) serveStorageCollection(w http.ResponseWriter, r *http.Request, rt route) {
	switch rt.Collection {
	case segImages:
		serveCRUD(w, r, rt, h.imageOps())
	case segVolumes:
		serveCRUD(w, r, rt, h.volumeOps())
	case segVolumeAttachments:
		serveCRUD(w, r, rt, h.volumeAttachmentOps())
	case segBootVolumes:
		serveCRUD(w, r, rt, h.bootVolumeOps())
	case segBootVolumeAttachments:
		serveCRUD(w, r, rt, h.bootVolumeAttachmentOps())
	case segVolumeBackups:
		serveCRUD(w, r, rt, h.backupOps(false))
	case segBootVolumeBackups:
		serveCRUD(w, r, rt, h.backupOps(true))
	case segVolumeGroups:
		serveCRUD(w, r, rt, h.volumeGroupOps())
	case segVNICAttachments:
		serveCRUD(w, r, rt, h.vnicAttachmentOps())
	default:
		unemulated(w, r, rt.Collection)
	}
}

// unemulated reports a collection the handler claims but cannot serve. The
// compute driver models no backup schedule, capacity reservation, dedicated
// host or serial console, so each would be a shape with nothing behind it.
func unemulated(w http.ResponseWriter, r *http.Request, collection string) {
	ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
		collection+" is not emulated by CloudEmu's OCI Compute service")
}

// crud is one collection's five operations.
type crud struct {
	create func(w http.ResponseWriter, r *http.Request)
	list   func(w http.ResponseWriter, r *http.Request)
	get    func(w http.ResponseWriter, r *http.Request, id string)
	update func(w http.ResponseWriter, r *http.Request, id string)
	remove func(w http.ResponseWriter, r *http.Request, id string)
}

// serveCRUD maps method and path shape onto a collection's operations.
func serveCRUD(w http.ResponseWriter, r *http.Request, rt route, c crud) {
	if rt.Sub != "" {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown sub-collection "+rt.Sub)
		return
	}

	if rt.ID == "" {
		switch r.Method {
		case http.MethodPost:
			dispatch(w, r, c.create)
		case http.MethodGet:
			dispatch(w, r, c.list)
		default:
			methodNotAllowed(w, r)
		}

		return
	}

	switch r.Method {
	case http.MethodGet:
		dispatchID(w, r, rt.ID, c.get)
	case http.MethodPut:
		dispatchID(w, r, rt.ID, c.update)
	case http.MethodDelete:
		dispatchID(w, r, rt.ID, c.remove)
	default:
		methodNotAllowed(w, r)
	}
}

// dispatch calls op, or reports the operation as unsupported for the collection.
func dispatch(w http.ResponseWriter, r *http.Request, op func(http.ResponseWriter, *http.Request)) {
	if op == nil {
		methodNotAllowed(w, r)
		return
	}

	op(w, r)
}

// dispatchID is dispatch for the operations addressing a single resource.
func dispatchID(
	w http.ResponseWriter, r *http.Request, id string, op func(http.ResponseWriter, *http.Request, string),
) {
	if op == nil {
		methodNotAllowed(w, r)
		return
	}

	op(w, r, id)
}

// changeCompartment moves a resource between compartments, which real OCI runs
// asynchronously.
func (h *Handler) changeCompartment(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req changeCompartmentRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if h.extras.Created(id) == "" {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "resource "+id+" not found")
		return
	}

	h.extras.SetScope(id, scope.Scope{Compartment: req.CompartmentID})
	h.accept(w, "CHANGE_"+strings.ToUpper(ocidType(id))+"_COMPARTMENT", req.CompartmentID,
		ocidType(id), workrequest.ActionUpdated, id)

	ocirest.WriteJSON(w, r, http.StatusAccepted, nil)
}

// accept records a work request and stamps its OCID, which SDK waiters poll.
func (h *Handler) accept(w http.ResponseWriter, operation, compartmentID, entity, action, id string) {
	if h.work == nil {
		return
	}

	wrID := h.work.Accept(operation, compartmentID, workrequest.Resource{
		EntityType: entity,
		ActionType: action,
		Identifier: id,
	})

	ocirest.SetWorkRequestID(w, wrID)
}

// place records the compartment a create call named.
func (h *Handler) place(id, compartmentID string) {
	h.extras.SetScope(id, scope.Scope{Compartment: compartmentID})
}

// inCompartment reports whether a resource is visible under a compartment filter.
func (h *Handler) inCompartment(id, compartmentID string) bool {
	return h.extras.Scope(id).Matches(scope.Scope{Compartment: compartmentID})
}

// compartmentOf returns the compartment a resource lives in.
func (h *Handler) compartmentOf(id string) string {
	return h.extras.Scope(id).Compartment
}

// parsePath splits /{version}/{collection}[/{id}[/{sub}[/{action}]]].
func parsePath(urlPath string) (route, bool) {
	parts := strings.Split(strings.Trim(urlPath, "/"), "/")
	if len(parts) < 2 || len(parts) > maxPathSegments || parts[0] != apiVersion {
		return route{}, false
	}

	rt := route{Collection: parts[1]}

	if len(parts) > 2 { //nolint:mnd // the id follows the collection
		rt.ID = parts[2]
	}

	if len(parts) > 3 { //nolint:mnd // then the sub-collection
		rt.Sub = parts[3]
	}

	if len(parts) > 4 { //nolint:mnd // then the action on it
		rt.Action = parts[4]
	}

	return rt, true
}

// paginate applies OCI's limit and opaque page cursor, stamping the cursor for
// the next page. The cursor is the offset the next page starts at.
func paginate[T any](w http.ResponseWriter, r *http.Request, items []T) []T {
	start := 0

	if token := ocirest.Page(r); token != "" {
		if n, err := strconv.Atoi(token); err == nil && n > 0 {
			start = n
		}
	}

	// items[:0] rather than nil: an empty page is [] on the wire, not null.
	if start >= len(items) {
		return items[:0]
	}

	end := min(start+ocirest.Limit(r), len(items))
	if end < len(items) {
		ocirest.SetNextPage(w, strconv.Itoa(end))
	}

	return items[start:end]
}

// renderPage projects a driver listing onto its wire shape and writes it as
// one page.
func renderPage[T, R any](w http.ResponseWriter, r *http.Request, items []T, render func(*T) R) {
	out := make([]R, 0, len(items))
	for i := range items {
		out = append(out, render(&items[i]))
	}

	writePage(w, r, out)
}

// writePage writes a listing as one page.
func writePage[T any](w http.ResponseWriter, r *http.Request, items []T) {
	ocirest.WriteJSON(w, r, http.StatusOK, paginate(w, r, items))
}

// methodNotAllowed is the response for a verb a collection does not serve.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	ocirest.WriteError(w, r, http.StatusMethodNotAllowed, codeMethodNotAllowed, "method not allowed")
}

// scopedList filters a driver listing to the caller's compartment and writes
// the page. key returns a resource's own OCID.
func scopedList[T, R any](
	h *Handler, w http.ResponseWriter, r *http.Request, compartmentID string,
	items []T, key func(*T) string, render func(*T) R,
) {
	out := make([]R, 0, len(items))

	for i := range items {
		if !h.inCompartment(key(&items[i]), compartmentID) {
			continue
		}

		out = append(out, render(&items[i]))
	}

	writePage(w, r, out)
}
