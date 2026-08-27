package compute

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/internal/pagination"
	"github.com/stackshy/cloudemu/v2/server/wire/gcprest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// gcpNameTag is the tag key we use to round-trip the GCP instance name
// through the driver, since the driver indexes by its own ID.
const gcpNameTag = "cloudemu:gcpName"

// statusTerminated is GCP Compute's status for stopped/terminated instances.
const statusTerminated = "TERMINATED"

// statusRunning is GCP Compute's status for a running instance.
const statusRunning = "RUNNING"

// defaultDiskSizeGb is the boot-disk size GCP reports when the caller did not
// request one.
const defaultDiskSizeGb = "10"

// insertInstance handles POST .../instances. Maps the GCP body to an
// InstanceConfig, runs RunInstances(count=1), returns a DONE Operation
// pointing at the newly-created resource.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) insertInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	if rp.Scope != gcprest.ScopeZones {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instances must be created in a zone")
		return
	}

	var req instanceRequest

	if !gcprest.DecodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "instance name required")
		return
	}

	// Real GCP rejects a duplicate name in the same zone with 409 alreadyExists.
	if existing, err := findInZone(r.Context(), h.compute, req.Name, rp.ScopeName); err == nil && existing != nil {
		gcprest.WriteError(w, http.StatusConflict, "alreadyExists",
			"The resource 'projects/"+rp.Project+"/zones/"+rp.ScopeName+"/instances/"+req.Name+"' already exists")

		return
	}

	subnet := firstSubnet(req.NetworkInterfaces)

	cfg := computedriver.InstanceConfig{
		ImageID:      bootImage(req.Disks),
		InstanceType: machineTypeShort(req.MachineType),
		SubnetID:     subnet,
		Tags:         insertTags(&req, rp.ScopeName),
		UserData:     startupScript(req.Metadata),
		PrivateIP:    h.privateIPFor(r.Context(), &req, subnet, rp.ScopeName),
	}

	instances, err := h.compute.RunInstances(r.Context(), cfg, 1)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if len(instances) == 0 {
		gcprest.WriteError(w, http.StatusInternalServerError, "internalError", "driver returned zero instances")
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// getInstance handles GET .../instances/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toInstanceResponse(inst, rp.Project, hostFromRequest(r)))
}

// listInstances handles GET .../instances. Scopes to the requested zone and
// applies the filter / maxResults / pageToken query parameters.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	instances, err := h.compute.DescribeInstances(r.Context(), nil, nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	pred := parseFilter(r.URL.Query().Get("filter"))

	out := make([]instanceResponse, 0, len(instances))

	for i := range instances {
		if !instanceInZone(&instances[i], rp.ScopeName) {
			continue
		}

		resp := toInstanceResponse(&instances[i], rp.Project, host)
		if pred(&resp) {
			out = append(out, resp)
		}
	}

	page, err := pagination.PaginateSorted(out,
		func(a, b instanceResponse) bool { return a.Name < b.Name },
		r.URL.Query().Get("pageToken"), parseMaxResults(r.URL.Query().Get("maxResults")))
	if err != nil {
		gcprest.WriteError(w, http.StatusBadRequest, "invalid", "invalid pageToken")
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, instanceListResponse{
		Kind:          "compute#instanceList",
		ID:            "projects/" + rp.Project + "/zones/" + rp.ScopeName + "/instances",
		Items:         page.Items,
		NextPageToken: page.NextPageToken,
		SelfLink:      gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "instances", ""),
	})
}

// aggregatedListInstances handles GET .../aggregated/instances: every instance
// grouped by its "zones/{zone}" scope, the shape gcloud uses when no zone is
// given.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) aggregatedListInstances(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	instances, err := h.compute.DescribeInstances(r.Context(), nil, nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	pred := parseFilter(r.URL.Query().Get("filter"))
	items := make(map[string]instancesScopedList)

	for i := range instances {
		resp := toInstanceResponse(&instances[i], rp.Project, host)
		if !pred(&resp) {
			continue
		}

		scope := "zones/" + tagOr(instances[i].Tags, keyZone, "unknown")
		bucket := items[scope]
		bucket.Instances = append(bucket.Instances, resp)
		items[scope] = bucket
	}

	gcprest.WriteJSON(w, http.StatusOK, aggregatedListResponse{
		Kind:     "compute#instanceAggregatedList",
		ID:       "projects/" + rp.Project + "/aggregated/instances",
		Items:    items,
		SelfLink: host + "/compute/v1/projects/" + rp.Project + "/aggregated/instances",
	})
}

// deleteInstance handles DELETE .../instances/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	// GCP instances.delete removes the resource (a subsequent GET is 404),
	// unlike EC2 terminate which leaves a TERMINATED tombstone. Hard-remove
	// when the driver supports it; fall back to terminate otherwise.
	if remover, ok := h.compute.(instanceRemover); ok {
		if err := remover.RemoveInstance(r.Context(), inst.ID); err != nil {
			gcprest.WriteCErr(w, err)
			return
		}
	} else if err := h.compute.TerminateInstances(r.Context(), []string{inst.ID}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", rp.ResourceName, "delete")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// startInstance handles POST .../instances/{name}/start.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) startInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	h.action(w, r, rp, "start", h.compute.StartInstances)
}

// stopInstance handles POST .../instances/{name}/stop.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) stopInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	h.action(w, r, rp, "stop", h.compute.StopInstances)
}

// resetInstance handles POST .../instances/{name}/reset.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) resetInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	h.action(w, r, rp, "reset", h.compute.RebootInstances)
}

// action is the shared body for start/stop/reset.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) action(
	w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath, opType string,
	op func(ctx context.Context, ids []string) error,
) {
	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := op(r.Context(), []string{inst.ID}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	doneOp := h.ops.RecordDone(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", rp.ResourceName, opType)

	gcprest.WriteJSON(w, http.StatusOK, doneOp)
}

// getSerialPortOutput handles GET .../instances/{name}/serialPort.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getSerialPortOutput(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	inst, err := findInZone(r.Context(), h.compute, rp.ResourceName, rp.ScopeName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	reader, ok := h.compute.(computedriver.ConsoleReader)
	if !ok {
		gcprest.WriteError(w, http.StatusNotImplemented, "notImplemented", "serial port output is not supported")
		return
	}

	out, err := reader.GetConsoleOutput(r.Context(), inst.ID)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	gcprest.WriteJSON(w, http.StatusOK, serialPortOutput{
		Kind:     "compute#serialPortOutput",
		Contents: string(out),
		Start:    "0",
		Next:     strconv.Itoa(len(out)),
		SelfLink: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "instances", rp.ResourceName) + "/serialPort",
	})
}

// instanceRemover is the GCP-local hard-delete capability (removes the
// instance rather than tombstoning it). The GCE provider Mock implements it.
type instanceRemover interface {
	RemoveInstance(ctx context.Context, instanceID string) error
}

// instanceMutator is a GCP-local capability the GCE Mock implements: it mutates
// an already-running instance (setLabels/setMetadata/setTags/setMachineType/
// attachDisk/detachDisk all apply to running VMs, unlike ModifyInstance which
// requires a stopped instance). set entries are merged into the tag map,
// remove keys are deleted, and machineType is set when non-empty.
type instanceMutator interface {
	MutateInstanceGCP(instanceID string, set map[string]string, remove []string, machineType string) error
}

// startupScript extracts the GCE boot script from instance metadata.
func startupScript(md metadataBlock) string {
	for _, item := range md.Items {
		if item.Key == "startup-script" {
			return item.Value
		}
	}

	return ""
}

// insertTags builds the driver tag map from the GCP insert body: the user
// labels, plus the internal keys that round-trip GCP-specific state (name,
// disks, network tags, metadata, network self-link, launch zone).
func insertTags(req *instanceRequest, zone string) map[string]string {
	out := make(map[string]string, len(req.Labels)+internalTagCap)

	for k, v := range req.Labels {
		out[k] = v
	}

	out[gcpNameTag] = req.Name
	out[keyZone] = zone

	if len(req.Disks) > 0 {
		out[keyDisks] = encodeJSON(req.Disks)
	}

	if len(req.Tags.Items) > 0 {
		out[keyNetTags] = encodeJSON(req.Tags.Items)
	}

	if len(req.Metadata.Items) > 0 {
		out[keyMetadata] = encodeJSON(req.Metadata.Items)
	}

	if net := firstNetwork(req.NetworkInterfaces); net != "" {
		out[keyNetwork] = net
	}

	if acs := accessConfigsFor(req.NetworkInterfaces, req.Name); len(acs) > 0 {
		out[keyAccessConfigs] = encodeJSON(acs)
	}

	if len(req.ServiceAccounts) > 0 {
		out[keyServiceAccts] = encodeJSON(req.ServiceAccounts)
	}

	return out
}

// accessConfig field defaults GCP stamps on an external-IP mapping.
const (
	accessConfigOneToOneNAT = "ONE_TO_ONE_NAT"
	accessConfigDefaultName = "External NAT"
	accessConfigNetworkTier = "PREMIUM"
)

// accessConfigsFor extracts the external-IP accessConfigs off the instance's
// network interfaces (CloudEmu models a single nic0, so the first NIC that
// carries them), filling GCP's server-side defaults. An accessConfig with no
// natIP is given a synthesized ephemeral external IP (mirroring GCP assigning
// one); an explicit natIP — a reserved google_compute_address — is preserved so
// that address reads back IN_USE while this instance holds it.
func accessConfigsFor(nics []networkInterface, instanceName string) []accessConfig {
	for i := range nics {
		if len(nics[i].AccessConfigs) == 0 {
			continue
		}

		out := make([]accessConfig, 0, len(nics[i].AccessConfigs))

		for j := range nics[i].AccessConfigs {
			ac := nics[i].AccessConfigs[j]

			if ac.Type == "" {
				ac.Type = accessConfigOneToOneNAT
			}

			if ac.Name == "" {
				ac.Name = accessConfigDefaultName
			}

			if ac.NetworkTier == "" {
				ac.NetworkTier = accessConfigNetworkTier
			}

			if ac.NatIP == "" {
				ac.NatIP = ephemeralExternalIP(instanceName, j)
			}

			out = append(out, ac)
		}

		return out
	}

	return nil
}

// findInZone looks up an instance by GCP name, scoped to zone. An instance with
// no recorded zone (created directly through the driver) matches any zone so
// non-wire callers stay visible.
func findInZone(
	ctx context.Context, c computedriver.Compute, name, zone string,
) (*computedriver.Instance, error) {
	instances, err := c.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	for i := range instances {
		if tagOr(instances[i].Tags, gcpNameTag, "") != name {
			continue
		}

		if instanceInZone(&instances[i], zone) {
			return &instances[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "instance %s not found", name)
}

// instanceInZone reports whether inst belongs to zone. An instance with no
// recorded zone matches any zone (defensive for driver-created instances).
func instanceInZone(inst *computedriver.Instance, zone string) bool {
	stored := tagOr(inst.Tags, keyZone, "")
	return zone == "" || stored == "" || stored == zone
}

// Helpers that map between GCP REST shapes and the driver model.

func bootImage(disks []attachedDisk) string {
	for i := range disks {
		if disks[i].Boot && disks[i].InitializeParams != nil {
			return disks[i].InitializeParams.SourceImage
		}
	}

	if len(disks) > 0 && disks[0].InitializeParams != nil {
		return disks[0].InitializeParams.SourceImage
	}

	return ""
}

// machineTypeShort trims the URL prefix off a machineType so we store
// "n1-standard-1" rather than the full self-link.
func machineTypeShort(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}

	return s
}

func firstSubnet(nics []networkInterface) string {
	for _, n := range nics {
		if n.Subnetwork != "" {
			return n.Subnetwork
		}
	}

	return ""
}

func firstNetwork(nics []networkInterface) string {
	for _, n := range nics {
		if n.Network != "" {
			return n.Network
		}
	}

	return ""
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

// numericID returns a stable uint64-shaped string derived from a driver
// resource ID. GCP IDs in the wire protocol are uint64; non-numeric values
// fail the SDK's protobuf unmarshalling.
func numericID(driverID string) string {
	const fnvOffset uint64 = 14695981039346656037

	const fnvPrime uint64 = 1099511628211

	h := fnvOffset
	for i := 0; i < len(driverID); i++ {
		h ^= uint64(driverID[i])
		h *= fnvPrime
	}

	return strconv.FormatUint(h, 10)
}

// gcpStatusFor maps driver states to GCP Compute Engine instance status.
func gcpStatusFor(state string) string {
	switch state {
	case "running":
		return statusRunning
	case "pending":
		return "PROVISIONING"
	case "stopping":
		return "STOPPING"
	case "stopped", "terminated":
		return statusTerminated
	default:
		return strings.ToUpper(state)
	}
}
