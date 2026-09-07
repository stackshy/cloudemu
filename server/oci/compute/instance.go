package compute

import (
	"context"
	"net/http"
	"strings"

	cerrors "github.com/stackshy/cloudemu/v2/errors"
	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
	computedriver "github.com/stackshy/cloudemu/v2/services/compute/driver"
)

// Instance actions OCI accepts on POST /instances/{id}?action=<action>.
const (
	actionStart     = "START"
	actionStop      = "STOP"
	actionSoftStop  = "SOFTSTOP"
	actionReset     = "RESET"
	actionSoftReset = "SOFTRESET"
)

// preemptionTerminate is the only preemption action OCI defines.
const preemptionTerminate = "TERMINATE"

// serveInstance routes the instances collection. OCI puts the lifecycle action
// on the resource itself as POST /instances/{id}?action=…, not under /actions.
func (h *Handler) serveInstance(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.Sub != "" {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "unknown sub-collection "+rt.Sub)
		return
	}

	if rt.ID != "" && r.Method == http.MethodPost {
		h.instanceAction(w, r, rt.ID)
		return
	}

	serveCRUD(w, r, rt, crud{
		create: h.launchInstance,
		list:   h.listInstances,
		get:    h.getInstance,
		update: h.updateInstance,
		remove: h.terminateInstance,
	})
}

func (h *Handler) launchInstance(w http.ResponseWriter, r *http.Request) {
	var req launchInstanceRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	if req.Shape == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "shape is required")
		return
	}

	if _, ok := h.extras.Shape(req.Shape); !ok {
		ocirest.WriteError(w, r, http.StatusNotFound, codeNotFound, "shape "+req.Shape+" not found")
		return
	}

	source := toSourceDetails(req.SourceDetails)

	imageID := firstNonEmpty(req.ImageID, source.ID)
	if imageID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"imageId or sourceDetails is required")

		return
	}

	instances, err := h.compute.RunInstances(r.Context(), launchConfig(&req, imageID), 1)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	inst := &instances[0]
	h.place(inst.ID, req.CompartmentID)

	if err := h.extras.SetInstanceDetails(inst.ID, launchDetails(&req, imageID, source)); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "LAUNCH_INSTANCE", req.CompartmentID, "instance", workrequest.ActionCreated, inst.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceResponse(inst))
}

// launchConfig projects OCI's launch body onto the portable launch config.
func launchConfig(req *launchInstanceRequest, imageID string) computedriver.InstanceConfig {
	cfg := computedriver.InstanceConfig{
		ImageID:      imageID,
		InstanceType: req.Shape,
		Tags:         withInternal(req.FreeformTags, tagDisplayName, req.DisplayName),
		SubnetID:     launchSubnet(req),
		UserData:     req.Metadata["user_data"],
		Priority:     "Regular",
	}

	if req.AvailabilityDomain != "" {
		cfg.Zones = []string{req.AvailabilityDomain}
	}

	if req.CreateVnicDetails != nil {
		cfg.SecurityGroups = req.CreateVnicDetails.NsgIDs
	}

	if req.PreemptibleInstanceConfig != nil {
		cfg.Priority = "Spot"
	}

	return cfg
}

// launchSubnet reads the subnet from createVnicDetails, falling back to the
// deprecated top-level subnetId OCI still accepts.
func launchSubnet(req *launchInstanceRequest) string {
	if req.CreateVnicDetails != nil && req.CreateVnicDetails.SubnetID != "" {
		return req.CreateVnicDetails.SubnetID
	}

	return req.SubnetID
}

// launchDetails collects the OCI-only attributes of a launch.
func launchDetails(
	req *launchInstanceRequest, imageID string, source ocicompute.SourceDetails,
) ocicompute.InstanceDetails {
	if source.SourceType == "" {
		source = ocicompute.SourceDetails{SourceType: "image", ID: imageID}
	}

	d := ocicompute.InstanceDetails{
		DisplayName:        req.DisplayName,
		AvailabilityDomain: req.AvailabilityDomain,
		FaultDomain:        req.FaultDomain,
		Metadata:           req.Metadata,
		ExtendedMetadata:   req.ExtendedMetadata,
		ShapeConfig:        toShapeConfig(req.ShapeConfig),
		SourceDetails:      source,
		AgentConfig:        toAgentConfig(req.AgentConfig),
		DedicatedVMHostID:  req.DedicatedVMHostID,
	}

	if req.CreateVnicDetails != nil {
		d.HostnameLabel = req.CreateVnicDetails.HostnameLabel
	}

	if req.PreemptibleInstanceConfig != nil {
		d.IsPreemptible = true
		d.PreserveBootVolume = req.PreemptibleInstanceConfig.PreemptionAction.PreserveBootVolume
	}

	return d
}

func (h *Handler) listInstances(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	filters := instanceFilters(r)

	infos, err := h.compute.DescribeInstances(r.Context(), nil, filters)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	scopedList(h, w, r, compartmentID, infos,
		func(v *computedriver.Instance) string { return v.ID },
		h.toInstanceResponse)
}

// instanceFilters turns ListInstances' query parameters into driver filters.
func instanceFilters(r *http.Request) []computedriver.DescribeFilter {
	var out []computedriver.DescribeFilter

	for param, name := range map[string]string{
		"availabilityDomain": "availability-domain",
		"lifecycleState":     "lifecycle-state",
	} {
		if v := r.URL.Query().Get(param); v != "" {
			value := v
			if param == "lifecycleState" {
				value = portableInstanceState(v)
			}

			out = append(out, computedriver.DescribeFilter{Name: name, Values: []string{value}})
		}
	}

	return out
}

// portableInstanceState is instanceLifecycle's inverse, so a caller filtering
// on OCI's RUNNING reaches the driver's "running".
func portableInstanceState(state string) string {
	for _, portable := range []string{
		"pending", "running", "stopping", "stopped", "shutting-down", "terminated", "restarting",
	} {
		if instanceLifecycle(portable) == strings.ToUpper(state) {
			return portable
		}
	}

	return state
}

func (h *Handler) getInstance(w http.ResponseWriter, r *http.Request, id string) {
	inst, err := h.findInstance(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceResponse(inst))
}

func (h *Handler) updateInstance(w http.ResponseWriter, r *http.Request, id string) {
	inst, findErr := h.findInstance(r.Context(), id)
	if findErr != nil {
		ocirest.WriteDriverError(w, r, findErr)
		return
	}

	var req updateInstanceRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	tags := withInternal(req.FreeformTags, tagDisplayName, req.DisplayName)
	if req.FreeformTags == nil && req.DisplayName == "" {
		tags = inst.Tags
	}

	err := h.compute.ModifyInstance(r.Context(), id, computedriver.ModifyInstanceInput{
		InstanceType: req.Shape,
		Tags:         tags,
	})
	if err == nil {
		err = h.applyInstanceDetails(id, &req)
	}

	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	updated, err := h.findInstance(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "UPDATE_INSTANCE", h.compartmentOf(id), "instance", workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceResponse(updated))
}

// applyInstanceDetails folds an update's OCI-only fields into the stored
// details, leaving the ones it did not name alone.
func (h *Handler) applyInstanceDetails(id string, req *updateInstanceRequest) error {
	d, ok := h.extras.InstanceDetails(id)
	if !ok {
		return cerrors.Newf(cerrors.NotFound, "instance %s not found", id)
	}

	if req.DisplayName != "" {
		d.DisplayName = req.DisplayName
	}

	if req.FaultDomain != "" {
		d.FaultDomain = req.FaultDomain
	}

	if req.Metadata != nil {
		d.Metadata = req.Metadata
	}

	if req.ExtendedMetadata != nil {
		d.ExtendedMetadata = req.ExtendedMetadata
	}

	if req.ShapeConfig != nil {
		d.ShapeConfig = toShapeConfig(req.ShapeConfig)
	}

	if req.AgentConfig != nil {
		d.AgentConfig = toAgentConfig(req.AgentConfig)
	}

	return h.extras.SetInstanceDetails(id, d)
}

// instanceAction runs OCI's lifecycle action, which real OCI performs
// asynchronously and CloudEmu completes before the response is written.
func (h *Handler) instanceAction(w http.ResponseWriter, r *http.Request, id string) {
	action := strings.ToUpper(r.URL.Query().Get("action"))
	if action == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "action is required")
		return
	}

	run, ok := h.actionFor(action)
	if !ok {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"instance action "+action+" is not emulated; use START, STOP, SOFTSTOP, RESET or SOFTRESET")

		return
	}

	if err := run(r.Context(), []string{id}); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	inst, err := h.findInstance(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "INSTANCE_ACTION_"+action, h.compartmentOf(id), "instance", workrequest.ActionUpdated, id)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceResponse(inst))
}

// actionFor maps an OCI instance action onto the driver call that performs it.
// SOFTSTOP and SOFTRESET differ from STOP and RESET only in asking the guest
// first, which a mock with no guest cannot distinguish.
func (h *Handler) actionFor(action string) (func(context.Context, []string) error, bool) {
	switch action {
	case actionStart:
		return h.compute.StartInstances, true
	case actionStop, actionSoftStop:
		return h.compute.StopInstances, true
	case actionReset, actionSoftReset:
		return h.compute.RebootInstances, true
	default:
		return nil, false
	}
}

func (h *Handler) terminateInstance(w http.ResponseWriter, r *http.Request, id string) {
	preserve := r.URL.Query().Get("preserveBootVolume") == "true"
	compartmentID := h.compartmentOf(id)

	if err := h.extras.TerminateInstance(r.Context(), id, preserve); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "TERMINATE_INSTANCE", compartmentID, "instance", workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// findInstance reads one instance, reporting OCI's not-found for an unknown
// OCID.
func (h *Handler) findInstance(ctx context.Context, id string) (*computedriver.Instance, error) {
	infos, err := h.compute.DescribeInstances(ctx, []string{id}, nil)
	if err != nil {
		return nil, err
	}

	if len(infos) == 0 {
		return nil, cerrors.Newf(cerrors.NotFound, "instance %s not found", id)
	}

	return &infos[0], nil
}

func (h *Handler) toInstanceResponse(inst *computedriver.Instance) instanceResponse {
	d, _ := h.extras.InstanceDetails(inst.ID)

	out := instanceResponse{
		ID:                 inst.ID,
		CompartmentID:      h.compartmentOf(inst.ID),
		AvailabilityDomain: firstNonEmpty(d.AvailabilityDomain, firstZone(inst.Zones)),
		FaultDomain:        d.FaultDomain,
		DisplayName:        firstNonEmpty(d.DisplayName, tagOr(inst.Tags, tagDisplayName, "")),
		Region:             ocidRegion(inst.ID),
		Shape:              inst.InstanceType,
		ShapeConfig:        toShapeConfigWire(d.ShapeConfig),
		ImageID:            inst.ImageID,
		SourceDetails:      toSourceDetailsWire(d.SourceDetails),
		Metadata:           orEmptyMap(d.Metadata),
		ExtendedMetadata:   d.ExtendedMetadata,
		AgentConfig:        toAgentConfigWire(d.AgentConfig),
		DedicatedVMHostID:  d.DedicatedVMHostID,
		LaunchMode:         launchModeParavirtualized,
		LifecycleState:     instanceLifecycle(inst.State),
		TimeCreated:        h.extras.Created(inst.ID),
		FreeformTags:       freeformOf(inst.Tags),
		DefinedTags:        definedTags{},
	}

	if d.IsPreemptible {
		out.PreemptibleInstanceConfig = &preemptibleInstanceConfig{
			PreemptionAction: preemptionAction{
				Type:               preemptionTerminate,
				PreserveBootVolume: d.PreserveBootVolume,
			},
		}
	}

	return out
}

// ocidRegion is the region segment of an OCID, which is the region an
// instance reports.
func ocidRegion(id string) string {
	parts := strings.Split(id, ".")
	if len(parts) < 4 { //nolint:mnd // ocid1.<type>.<realm>.<region>.<unique>
		return ""
	}

	return parts[3]
}

func firstZone(zones []string) string {
	if len(zones) == 0 {
		return ""
	}

	return zones[0]
}

func orEmptyMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}

	return m
}
