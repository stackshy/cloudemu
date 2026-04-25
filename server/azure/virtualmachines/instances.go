package virtualmachines

import (
	"context"
	"net/http"
	"strings"

	computedriver "github.com/stackshy/cloudemu/compute/driver"
	cerrors "github.com/stackshy/cloudemu/errors"
	"github.com/stackshy/cloudemu/server/wire/azurearm"
)

// armNameTag is the tag key we use to round-trip the ARM resource name
// through the driver, since the driver indexes by its own ID.
const armNameTag = "cloudemu:azureName"

// Driver lifecycle states we map to ARM PowerState codes.
const (
	stateRunning    = "running"
	statePending    = "pending"
	stateStopped    = "stopped"
	stateStopping   = "stopping"
	stateTerminated = "terminated"
)

// createOrUpdate handles PUT virtualMachines/{name}. Maps the ARM JSON body
// onto an InstanceConfig, calls RunInstances(count=1), and replies with an
// ARM-shaped vmResponse.
//
//nolint:gocritic // rp is a request-scoped value; pointer chain isn't worth the noise
func (h *Handler) createOrUpdate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if rp.ResourceGroup == "" {
		azurearm.WriteError(w, http.StatusBadRequest, "InvalidPath", "missing resourceGroups segment")
		return
	}

	var req vmRequest

	if !azurearm.DecodeJSON(w, r, &req) {
		return
	}

	cfg := computedriver.InstanceConfig{
		ImageID:      imageRefToID(req.Properties.StorageProfile),
		InstanceType: hardwareSize(req.Properties.HardwareProfile),
		SubnetID:     firstNicID(req.Properties.NetworkProfile),
		KeyName:      computerName(req.Properties.OSProfile),
		Tags:         mergeTags(req.Tags, rp.ResourceName),
	}

	instances, err := h.compute.RunInstances(r.Context(), cfg, 1)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if len(instances) == 0 {
		azurearm.WriteError(w, http.StatusInternalServerError, "InternalError", "driver returned zero instances")
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVMResponse(&instances[0], rp, req))
}

// get handles GET virtualMachines/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVMResponse(inst, rp, vmRequest{}))
}

// list handles GET virtualMachines (within a subscription or resource group).
// The mock isn't subscription/RG-aware, so all VMs come back.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) list(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	instances, err := h.compute.DescribeInstances(r.Context(), nil, nil)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	out := make([]vmResponse, 0, len(instances))

	for i := range instances {
		name := tagOr(instances[i].Tags, armNameTag, instances[i].ID)
		scope := rp
		scope.ResourceName = name
		out = append(out, toVMResponse(&instances[i], scope, vmRequest{}))
	}

	azurearm.WriteJSON(w, http.StatusOK, vmListResponse{Value: out})
}

// delete handles DELETE virtualMachines/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.TerminateInstances(r.Context(), []string{inst.ID}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// start handles POST virtualMachines/{name}/start.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) start(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.StartInstances)
}

// powerOff handles POST virtualMachines/{name}/powerOff (also serves deallocate).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) powerOff(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.StopInstances)
}

// restart handles POST virtualMachines/{name}/restart.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) restart(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.RebootInstances)
}

// lifecycleAction is the shared body for start/stop/restart.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) lifecycleAction(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	op func(ctx context.Context, ids []string) error,
) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := op(r.Context(), []string{inst.ID}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// findByName looks up a VM by its ARM resource name. Returns NotFound when
// no instance with that tag exists.
func findByName(ctx context.Context, c computedriver.Compute, name string) (*computedriver.Instance, error) {
	instances, err := c.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	for i := range instances {
		if tagOr(instances[i].Tags, armNameTag, "") == name {
			return &instances[i], nil
		}
	}

	return nil, cerrors.Newf(cerrors.NotFound, "virtualMachine %s not found", name)
}

// Helpers that map ARM JSON shapes to/from the driver model.

func imageRefToID(s *storageProfile) string {
	if s == nil || s.ImageReference == nil {
		return ""
	}

	if s.ImageReference.ID != "" {
		return s.ImageReference.ID
	}

	parts := []string{s.ImageReference.Publisher, s.ImageReference.Offer, s.ImageReference.SKU, s.ImageReference.Version}

	return strings.Trim(strings.Join(parts, ":"), ":")
}

func hardwareSize(p *hardwareProfile) string {
	if p == nil {
		return ""
	}

	return p.VMSize
}

func firstNicID(n *networkProfile) string {
	if n == nil || len(n.NetworkInterfaces) == 0 {
		return ""
	}

	return n.NetworkInterfaces[0].ID
}

func computerName(o *osProfile) string {
	if o == nil {
		return ""
	}

	return o.ComputerName
}

func mergeTags(in map[string]string, armName string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = armName

	return out
}

func tagOr(m map[string]string, key, fallback string) string {
	if v, ok := m[key]; ok {
		return v
	}

	return fallback
}

// toVMResponse maps a driver Instance back onto the ARM JSON shape.
//
//nolint:gocritic // rp/req are value types passed once per response build
func toVMResponse(inst *computedriver.Instance, rp azurearm.ResourcePath, req vmRequest) vmResponse {
	name := tagOr(inst.Tags, armNameTag, rp.ResourceName)

	return vmResponse{
		ID:       azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name:     name,
		Type:     providerName + "/" + resourceType,
		Location: defaultIfEmpty(req.Location, "eastus"),
		Tags:     stripInternalTags(inst.Tags),
		Properties: vmResponseProps{
			VMID:              inst.ID,
			ProvisioningState: "Succeeded",
			HardwareProfile:   &hardwareProfile{VMSize: inst.InstanceType},
			InstanceView: &instanceView{
				Statuses: []instanceViewStatus{
					{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
					{Code: "PowerState/" + powerStateFor(inst.State), Level: "Info", DisplayStatus: displayFor(inst.State)},
				},
			},
		},
	}
}

func defaultIfEmpty(v, fallback string) string {
	if v == "" {
		return fallback
	}

	return v
}

func stripInternalTags(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}

	out := make(map[string]string, len(in))

	for k, v := range in {
		if k == armNameTag {
			continue
		}

		out[k] = v
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func powerStateFor(state string) string {
	switch state {
	case stateRunning:
		return "running"
	case statePending:
		return "starting"
	case stateStopped:
		return "deallocated"
	case stateStopping:
		return "deallocating"
	case stateTerminated:
		return "deleted"
	default:
		return state
	}
}

func displayFor(state string) string {
	switch state {
	case stateRunning:
		return "VM running"
	case statePending:
		return "VM starting"
	case stateStopped:
		return "VM deallocated"
	case stateStopping:
		return "VM deallocating"
	case stateTerminated:
		return "VM deleted"
	default:
		return "VM " + state
	}
}
