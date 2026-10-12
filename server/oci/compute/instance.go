package compute

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
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

	if !h.requireCompartment(w, r, req.CompartmentID) {
		return
	}

	if req.Shape == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "shape is required")
		return
	}

	if problem := h.launchProblem(&req); problem != "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, problem)
		return
	}

	source := toSourceDetails(req.SourceDetails)

	imageID := firstNonEmpty(req.ImageID, source.ID)
	if imageID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"imageId or sourceDetails is required")

		return
	}

	instances, err := h.compute.RunInstances(r.Context(), launchConfig(&req, imageID, source), 1)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	inst := &instances[0]
	h.placeInstance(inst.ID, req.CompartmentID)

	if err := h.extras.SetInstanceDetails(inst.ID, launchDetails(&req, imageID, source)); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "LAUNCH_INSTANCE", req.CompartmentID, "instance", workrequest.ActionCreated, inst.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceResponse(inst))
}

// launchProblem names what OCI would refuse in a launch body, or returns "".
// An unknown shape is left to the provider, which answers 404.
func (h *Handler) launchProblem(req *launchInstanceRequest) string {
	switch {
	case req.AvailabilityDomain == "":
		return "availabilityDomain is required"
	case launchSubnet(req) == "":
		return "createVnicDetails.subnetId is required"
	case req.ShapeConfig == nil:
		return ""
	}

	shape, ok := h.extras.Shape(req.Shape)
	if !ok {
		return ""
	}

	return shapeConfigProblem(&shape, req.ShapeConfig)
}

// updateShapeProblem checks an update's shapeConfig against the shape the
// instance will have once updated, or returns "". An unknown shape is left to
// the provider, which answers 404.
func (h *Handler) updateShapeProblem(currentShape string, req *updateInstanceRequest) string {
	if req.ShapeConfig == nil {
		return ""
	}

	shape, ok := h.extras.Shape(firstNonEmpty(req.Shape, currentShape))
	if !ok {
		return ""
	}

	return shapeConfigProblem(&shape, req.ShapeConfig)
}

// shapeConfigProblem checks a shapeConfig against the shape's bounds. Only a
// flexible shape takes one.
func shapeConfigProblem(shape *ocicompute.Shape, cfg *shapeConfigWire) string {
	if !shape.IsFlexible {
		return "shapeConfig is only valid for a flexible shape; " + shape.Name + " is fixed"
	}

	if cfg.Ocpus != 0 && (cfg.Ocpus < shape.MinOCPUs || cfg.Ocpus > shape.MaxOCPUs) {
		return fmt.Sprintf("shapeConfig.ocpus %g is outside %g-%g for %s",
			cfg.Ocpus, shape.MinOCPUs, shape.MaxOCPUs, shape.Name)
	}

	if cfg.MemoryInGBs != 0 && (cfg.MemoryInGBs < shape.MinMemoryInGBs || cfg.MemoryInGBs > shape.MaxMemoryInGBs) {
		return fmt.Sprintf("shapeConfig.memoryInGBs %g is outside %g-%g for %s",
			cfg.MemoryInGBs, shape.MinMemoryInGBs, shape.MaxMemoryInGBs, shape.Name)
	}

	return memoryPerOCPUProblem(shape, cfg)
}

// memoryPerOCPUProblem checks memory against the OCPUs chosen, for a shape
// that caps it.
func memoryPerOCPUProblem(shape *ocicompute.Shape, cfg *shapeConfigWire) string {
	ocpus := cfg.Ocpus
	if ocpus == 0 {
		ocpus = shape.OCPUs
	}

	if shape.MaxMemoryPerOCPU != 0 && cfg.MemoryInGBs > ocpus*shape.MaxMemoryPerOCPU {
		return fmt.Sprintf("shapeConfig.memoryInGBs %g exceeds %g GB per OCPU for %g OCPUs on %s",
			cfg.MemoryInGBs, shape.MaxMemoryPerOCPU, ocpus, shape.Name)
	}

	return ""
}

// launchConfig projects OCI's launch body onto the portable launch config.
// An image launch's boot volume size rides an internal tag the provider reads.
func launchConfig(
	req *launchInstanceRequest, imageID string, source ocicompute.SourceDetails,
) computedriver.InstanceConfig {
	bootSize := ""
	if source.BootVolumeSizeInGBs != 0 && source.SourceType != sourceBootVolume {
		bootSize = strconv.Itoa(source.BootVolumeSizeInGBs)
	}

	cfg := computedriver.InstanceConfig{
		ImageID:      imageID,
		InstanceType: req.Shape,
		Tags: withInternal(req.FreeformTags, tagDisplayName, req.DisplayName,
			ocicompute.TagBootVolumeSizeInGBs, bootSize, ocicompute.TagCompartmentID, req.CompartmentID),
		SubnetID: launchSubnet(req),
		UserData: req.Metadata["user_data"],
		Priority: "Regular",
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

	name := r.URL.Query().Get("displayName")
	out := make([]instanceResponse, 0, len(infos))

	for i := range infos {
		if !h.inCompartment(infos[i].ID, compartmentID) {
			continue
		}

		resp := h.toInstanceResponse(&infos[i])
		if name != "" && resp.DisplayName != name {
			continue
		}

		out = append(out, resp)
	}

	if !sortInstances(w, r, out) {
		return
	}

	writePage(w, r, out)
}

// sortInstances applies ListInstances' sortBy and sortOrder. OCI sorts by
// TIMECREATED descending unless told otherwise, and by DISPLAYNAME ascending
// when that is the key and no order is named.
func sortInstances(w http.ResponseWriter, r *http.Request, items []instanceResponse) bool {
	sortBy := strings.ToUpper(r.URL.Query().Get("sortBy"))
	order := strings.ToUpper(r.URL.Query().Get("sortOrder"))

	var less func(a, b *instanceResponse) bool

	switch sortBy {
	case "", sortByTimeCreated:
		less = func(a, b *instanceResponse) bool {
			if a.TimeCreated != b.TimeCreated {
				return a.TimeCreated < b.TimeCreated
			}

			return a.ID < b.ID
		}

		if order == "" {
			order = sortDesc
		}
	case sortByDisplayName:
		less = func(a, b *instanceResponse) bool { return a.DisplayName < b.DisplayName }
	default:
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "sortBy must be TIMECREATED or DISPLAYNAME")
		return false
	}

	switch order {
	case "", sortAsc:
		sort.SliceStable(items, func(i, j int) bool { return less(&items[i], &items[j]) })
	case sortDesc:
		sort.SliceStable(items, func(i, j int) bool { return less(&items[j], &items[i]) })
	default:
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "sortOrder must be ASC or DESC")
		return false
	}

	return true
}

// ListInstances' sort keys and orders.
const (
	sortByTimeCreated = "TIMECREATED"
	sortByDisplayName = "DISPLAYNAME"
	sortAsc           = "ASC"
	sortDesc          = "DESC"
)

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

	if problem := h.updateShapeProblem(inst.InstanceType, &req); problem != "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, problem)
		return
	}

	err := h.compute.ModifyInstance(r.Context(), id, computedriver.ModifyInstanceInput{
		InstanceType: req.Shape,
		Tags:         updatedTags(inst.Tags, &req),
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

// updatedTags overlays an update onto an instance's tags. OCI leaves what a
// request omits untouched, so a rename keeps the freeform tags and a tag
// update keeps the name.
func updatedTags(current map[string]string, req *updateInstanceRequest) map[string]string {
	out := make(map[string]string, len(current)+len(req.FreeformTags))

	for k, v := range current {
		if req.FreeformTags == nil || strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	for k, v := range req.FreeformTags {
		if !strings.HasPrefix(k, internalTagPrefix) {
			out[k] = v
		}
	}

	if req.DisplayName != "" {
		out[tagDisplayName] = req.DisplayName
	}

	return out
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
	} else if shape, ok := h.extras.Shape(req.Shape); ok && !shape.IsFlexible {
		// A move to a fixed shape takes the shape's own OCPUs and memory.
		d.ShapeConfig = nil
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
