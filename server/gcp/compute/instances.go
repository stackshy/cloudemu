package compute

import (
	"context"
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/gcprest"
)

// gcpNameTag is the tag key we use to round-trip the GCP instance name
// through the driver, since the driver indexes by its own ID.
const gcpNameTag = "cloudemu:gcpName"

// statusTerminated is GCP Compute's status for stopped/terminated instances.
const statusTerminated = "TERMINATED"

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

	cfg := computedriver.InstanceConfig{
		ImageID:      bootImage(req.Disks),
		InstanceType: machineTypeShort(req.MachineType),
		SubnetID:     firstSubnet(req.NetworkInterfaces),
		Tags:         mergeTags(req.Labels, req.Name),
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

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", req.Name, "insert")

	gcprest.WriteJSON(w, http.StatusOK, op)
}

// getInstance handles GET .../instances/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	gcprest.WriteJSON(w, http.StatusOK, toInstanceResponse(inst, rp, hostFromRequest(r)))
}

// listInstances handles GET .../instances. Returns all driver instances; the
// mock isn't project/zone scoped.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	instances, err := h.compute.DescribeInstances(r.Context(), nil, nil)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	host := hostFromRequest(r)
	out := make([]instanceResponse, 0, len(instances))

	for i := range instances {
		scope := rp
		scope.ResourceName = tagOr(instances[i].Tags, gcpNameTag, instances[i].ID)
		out = append(out, toInstanceResponse(&instances[i], scope, host))
	}

	gcprest.WriteJSON(w, http.StatusOK, instanceListResponse{
		Kind:     "compute#instanceList",
		ID:       "projects/" + rp.Project + "/zones/" + rp.ScopeName + "/instances",
		Items:    out,
		SelfLink: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "instances", ""),
	})
}

// deleteInstance handles DELETE .../instances/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deleteInstance(w http.ResponseWriter, r *http.Request, rp gcprest.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := h.compute.TerminateInstances(r.Context(), []string{inst.ID}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	op := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
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
	inst, err := findByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	if err := op(r.Context(), []string{inst.ID}); err != nil {
		gcprest.WriteCErr(w, err)
		return
	}

	doneOp := gcprest.NewDoneOperation(hostFromRequest(r), rp.Project, rp.Scope, rp.ScopeName,
		"instances", rp.ResourceName, opType)

	gcprest.WriteJSON(w, http.StatusOK, doneOp)
}

// findByName looks up an instance by its GCP-tagged name.
func findByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.Instance, error) {
	instances, err := c.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	for i := range instances {
		if tagOr(instances[i].Tags, gcpNameTag, "") == name {
			return &instances[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "instance %s not found", name)
}

// Helpers that map between GCP REST shapes and the driver model.

func bootImage(disks []attachedDisk) string {
	for _, d := range disks {
		if d.Boot && d.InitializeParams != nil {
			return d.InitializeParams.SourceImage
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

func mergeTags(in map[string]string, gcpName string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[gcpNameTag] = gcpName

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

// toInstanceResponse maps a driver Instance back to GCP's REST shape.
//
//nolint:gocritic // rp is a request-scoped value passed once per response build
func toInstanceResponse(inst *computedriver.Instance, rp gcprest.ResourcePath, host string) instanceResponse {
	name := tagOr(inst.Tags, gcpNameTag, rp.ResourceName)

	return instanceResponse{
		Kind:        "compute#instance",
		ID:          inst.ID,
		Name:        name,
		MachineType: gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "machineTypes", inst.InstanceType),
		Status:      gcpStatusFor(inst.State),
		Zone:        host + "/compute/v1/projects/" + rp.Project + "/zones/" + rp.ScopeName,
		SelfLink:    gcprest.SelfLink(host, rp.Project, rp.Scope, rp.ScopeName, "instances", name),
		Labels:      stripInternalTags(inst.Tags),
	}
}

func stripInternalTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == gcpNameTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// gcpStatusFor maps driver states to GCP Compute Engine instance status.
// GCP's documented values: PROVISIONING, STAGING, RUNNING, STOPPING,
// STOPPED, SUSPENDING, SUSPENDED, REPAIRING, TERMINATED.
func gcpStatusFor(state string) string {
	switch state {
	case "running":
		return "RUNNING"
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
