package virtualmachines

import (
	"context"
	"encoding/base64"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// armNameTag is the tag key we use to round-trip the ARM resource name
// through the driver, since the driver indexes by its own ID.
const armNameTag = "cloudemu:azureName"

// URL schemes for building absolute self-referential URLs (async operation
// status, boot-diagnostics serial log) against the incoming request.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// requestScheme reports the scheme the request arrived on, so self-referential
// URLs work on both the plain-HTTP and TLS listeners.
func requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return schemeHTTPS
	}

	return schemeHTTP
}

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
		ImageID:       imageRefToID(req.Properties.StorageProfile),
		InstanceType:  hardwareSize(req.Properties.HardwareProfile),
		SubnetID:      firstNicID(req.Properties.NetworkProfile),
		KeyName:       computerName(req.Properties.OSProfile),
		UserData:      decodeCustomData(customData(req.Properties.OSProfile)),
		Tags:          mergeTags(req.Tags, rp.ResourceName),
		Priority:      req.Properties.Priority,
		LicenseType:   req.Properties.LicenseType,
		OSType:        osTypeFromStorage(req.Properties.StorageProfile),
		Zones:         req.Zones,
		Region:        req.Location,
		ResourceGroup: rp.ResourceGroup,
	}

	// ARM CreateOrUpdate is idempotent: a repeated PUT to the same {rg,name}
	// updates the VM in place rather than provisioning a duplicate.
	if existing, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); err == nil {
		h.updateExisting(w, r, rp, req, existing, cfg)
		return
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

// updateExisting applies an idempotent CreateOrUpdate to an already-provisioned
// VM. When the driver supports in-place update (AzureVMController) the existing
// instance's mutable config is overwritten and its ID preserved; otherwise the
// current instance is returned unchanged so no duplicate is created.
//
//nolint:gocritic // rp/req are request-scoped values passed once per request
func (h *Handler) updateExisting(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	req vmRequest, existing *computedriver.Instance, cfg computedriver.InstanceConfig,
) {
	if ctrl, ok := h.compute.(computedriver.AzureVMController); ok {
		if err := ctrl.UpdateInstance(r.Context(), existing.ID, cfg); err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		updated, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
		if err != nil {
			azurearm.WriteCErr(w, err)
			return
		}

		existing = updated
	}

	azurearm.WriteJSON(w, http.StatusOK, toVMResponse(existing, rp, req))
}

// get handles GET virtualMachines/{name}.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) get(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toVMResponse(inst, rp, vmRequest{}))
}

// list handles GET virtualMachines. A resource-group-scoped path
// (.../resourceGroups/{rg}/...) returns only that group's VMs; a
// subscription-scoped path returns every VM.
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
		if rp.ResourceGroup != "" && instances[i].ResourceGroup != rp.ResourceGroup {
			continue
		}

		name := tagOr(instances[i].Tags, armNameTag, instances[i].ID)
		scope := rp
		scope.ResourceName = name
		out = append(out, toVMResponse(&instances[i], scope, vmRequest{}))
	}

	azurearm.WriteJSON(w, http.StatusOK, vmListResponse{Value: out})
}

// instanceView handles GET virtualMachines/{name}/instanceView. It returns the
// VirtualMachineInstanceView: the provisioning + power state statuses, the VM
// agent status, and per-disk status the Azure SDK's InstanceView call reads.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) instanceView(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, toInstanceView(inst))
}

// delete handles DELETE virtualMachines/{name}. Returns a 202 Accepted with
// the async-operation polling header so the SDK's poller terminates cleanly.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) delete(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := h.compute.TerminateInstances(r.Context(), []string{inst.ID}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "delete-"+rp.ResourceName)
}

// start handles POST virtualMachines/{name}/start.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) start(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.StartInstances)
}

// powerOff handles POST virtualMachines/{name}/powerOff. It stops the guest OS
// while keeping the VM allocated (PowerState/stopped).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) powerOff(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.powerAction(w, r, rp, func(ctrl computedriver.AzureVMController, id string) error {
		return ctrl.PowerOff(r.Context(), id)
	})
}

// deallocate handles POST virtualMachines/{name}/deallocate. It stops the guest
// and releases the allocated compute (PowerState/deallocated).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) deallocate(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.powerAction(w, r, rp, func(ctrl computedriver.AzureVMController, id string) error {
		return ctrl.Deallocate(r.Context(), id)
	})
}

// powerAction is the shared body for powerOff/deallocate. It invokes the Azure
// power controller when the driver supports it (preserving the PowerOff vs
// Deallocate distinction) and otherwise falls back to the generic StopInstances.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) powerAction(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	op func(ctrl computedriver.AzureVMController, id string) error,
) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if ctrl, ok := h.compute.(computedriver.AzureVMController); ok {
		err = op(ctrl, inst.ID)
	} else {
		err = h.compute.StopInstances(r.Context(), []string{inst.ID})
	}

	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, rp.SubResource+"-"+rp.ResourceName)
}

// restart handles POST virtualMachines/{name}/restart.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) restart(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	h.lifecycleAction(w, r, rp, h.compute.RebootInstances)
}

// retrieveBootDiagnosticsData handles POST virtualMachines/{name}/retrieveBootDiagnosticsData.
// It mirrors the real ARM action: rather than returning the serial-log bytes
// inline, it returns the URIs a client downloads the console screenshot and
// serial log from. We point the serial-log URI back at this server's
// bootDiagnostics/serialConsoleLog GET so the engine-captured boot output is
// retrievable (the closest faithful analog to the real Azure Storage SAS blob).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) retrieveBootDiagnosticsData(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	if _, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	azurearm.WriteJSON(w, http.StatusOK, bootDiagnosticsDataResult{
		SerialConsoleLogBlobURI: serialConsoleLogURI(r, rp),
	})
}

// serialConsoleLog handles GET virtualMachines/{name}/bootDiagnostics/serialConsoleLog,
// the endpoint retrieveBootDiagnosticsData points its serialConsoleLogBlobUri at.
// It returns the raw console bytes the compute engine captured for the VM's boot
// script (empty when no engine backs it).
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) serialConsoleLog(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	reader, ok := h.compute.(computedriver.ConsoleReader)
	if !ok {
		azurearm.WriteError(w, http.StatusNotImplemented, "NotImplemented", "boot diagnostics not supported")
		return
	}

	out, err := reader.GetConsoleOutput(r.Context(), inst.ID)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

// serialConsoleLogURI builds the absolute URL of this VM's serial-log download
// endpoint, resolved against the incoming request's host and scheme so it works
// on both the http and https (TLS) listeners.
//
//nolint:gocritic // rp is a request-scoped value
func serialConsoleLogURI(r *http.Request, rp azurearm.ResourcePath) string {
	return requestScheme(r) + "://" + r.Host +
		azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, rp.ResourceName) +
		"/bootDiagnostics/serialConsoleLog?api-version=2023-09-01"
}

// lifecycleAction is the shared body for start/stop/restart. Returns 202
// + async polling header so real Azure SDK pollers terminate.
//
//nolint:gocritic // rp is a request-scoped value
func (h *Handler) lifecycleAction(
	w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath,
	op func(ctx context.Context, ids []string) error,
) {
	inst, err := findByName(r.Context(), h.compute, rp.ResourceGroup, rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	if err := op(r.Context(), []string{inst.ID}); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, rp.SubResource+"-"+rp.ResourceName)
}

// writeAcceptedAsync replies 202 Accepted with the Azure-AsyncOperation and
// Location headers pointing to our operationStatuses endpoint. The Azure SDK
// poller will GET that URL and observe Succeeded immediately.
func writeAcceptedAsync(w http.ResponseWriter, r *http.Request, subscription, opID string) {
	statusURL := requestScheme(r) + "://" + r.Host +
		"/subscriptions/" + subscription +
		"/providers/" + providerName +
		"/locations/eastus/operationStatuses/" + opID +
		"?api-version=2023-09-01"

	w.Header().Set("Azure-AsyncOperation", statusURL)
	w.Header().Set("Location", statusURL)
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusAccepted)
}

// findByName looks up a VM by its ARM resource name within a resource group.
// An empty resourceGroup matches across all groups (subscription-scoped lookup).
// Returns NotFound when no matching instance exists.
func findByName(ctx context.Context, c computedriver.Compute, resourceGroup, name string) (*computedriver.Instance, error) {
	instances, err := c.DescribeInstances(ctx, nil, nil)
	if err != nil {
		return nil, err
	}

	for i := range instances {
		if tagOr(instances[i].Tags, armNameTag, "") != name {
			continue
		}

		if resourceGroup != "" && instances[i].ResourceGroup != resourceGroup {
			continue
		}

		return &instances[i], nil
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

func customData(o *osProfile) string {
	if o == nil {
		return ""
	}

	return o.CustomData
}

// decodeCustomData decodes the base64 customData Azure carries on osProfile.
// Real ARM customData is always base64-encoded; a value that does not decode is
// passed through raw so a client that sent plain text still gets its boot script.
func decodeCustomData(s string) string {
	if s == "" {
		return ""
	}

	if decoded, err := base64.StdEncoding.DecodeString(s); err == nil {
		return string(decoded)
	}

	return s
}

func osTypeFromStorage(s *storageProfile) string {
	if s == nil || s.OSDisk == nil {
		return ""
	}

	return s.OSDisk.OSType
}

func mergeTags(in map[string]string, armName string) map[string]string {
	out := make(map[string]string, len(in)+1)

	for k, v := range in {
		out[k] = v
	}

	out[armNameTag] = armName

	return out
}

// tagOr returns the tag value for key, or fallback when absent. The key
// parameter is kept for signature parity with the sibling azure compute
// handlers even though this package only reads the ARM-name tag.
//
//nolint:unparam // uniform tag-helper signature across azure compute handlers
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
		ID:   azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceType, name),
		Name: name,
		Type: providerName + "/" + resourceType,
		// Prefer the instance's recorded region so GET/LIST report where the VM
		// was actually created; the request location (create path) and the
		// "eastus" default are fallbacks for instances that carry no region.
		Location: defaultIfEmpty(inst.Region, defaultIfEmpty(req.Location, "eastus")),
		Tags:     stripInternalTags(inst.Tags),
		Zones:    inst.Zones,
		Properties: vmResponseProps{
			VMID:              inst.ID,
			ProvisioningState: "Succeeded",
			HardwareProfile:   &hardwareProfile{VMSize: inst.InstanceType},
			StorageProfile:    osDiskProfile(inst.OSType),
			Priority:          inst.Priority,
			LicenseType:       inst.LicenseType,
			InstanceView: &instanceView{
				Statuses: []instanceViewStatus{
					{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
					{Code: "PowerState/" + powerCode(inst), Level: "Info", DisplayStatus: powerDisplay(inst)},
				},
			},
		},
	}
}

// osDiskProfile echoes the guest OS family under the storageProfile.osDisk
// path the SDK reads it from. Returns nil when the OS type is unknown so the
// field is omitted rather than emitted empty.
func osDiskProfile(osType string) *storageProfile {
	if osType == "" {
		return nil
	}

	return &storageProfile{OSDisk: &osDisk{OSType: osType}}
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

// powerCode returns the ARM PowerState code suffix for an instance. It prefers
// the explicit Azure power state (which distinguishes stopped from deallocated)
// and falls back to a lifecycle-state mapping when unset.
func powerCode(inst *computedriver.Instance) string {
	if inst.PowerState != "" {
		return inst.PowerState
	}

	return powerStateFor(inst.State)
}

func powerDisplay(inst *computedriver.Instance) string {
	return "VM " + powerCode(inst)
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

// toInstanceView builds the VirtualMachineInstanceView response for an instance.
func toInstanceView(inst *computedriver.Instance) instanceViewResponse {
	return instanceViewResponse{
		ComputerName:     tagOr(inst.Tags, armNameTag, ""),
		OSName:           osNameFor(inst.OSType),
		VMAgent:          &vmAgentInstanceView{VMAgentVersion: "2.0.0.0"},
		Disks:            []diskInstanceView{{Name: "osdisk"}},
		HyperVGeneration: "V1",
		Statuses: []instanceViewStatus{
			{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
			{Code: "PowerState/" + powerCode(inst), Level: "Info", DisplayStatus: powerDisplay(inst)},
		},
	}
}

// osNameFor renders a plausible OS name from the guest OS family, for the
// instanceView's osName field. Empty OS type yields an empty name.
func osNameFor(osType string) string {
	switch strings.ToLower(osType) {
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return osType
	}
}
