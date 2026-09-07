package compute

import (
	"net/http"
	"strings"

	ocicompute "github.com/stackshy/cloudemu/v2/providers/oci/compute"
	"github.com/stackshy/cloudemu/v2/server/oci/workrequest"
	"github.com/stackshy/cloudemu/v2/server/wire/ocirest"
)

// instanceTypeCompute is the only instance configuration source OCI defines.
const instanceTypeCompute = "compute"

// serveInstanceConfig routes the instanceConfigurations collection and its
// launch action.
func (h *Handler) serveInstanceConfig(w http.ResponseWriter, r *http.Request, rt route) {
	if rt.Sub == subActions {
		if !strings.EqualFold(rt.Action, actionLaunch) {
			ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
				"instance configuration action "+rt.Action+" is not emulated; use launch")

			return
		}

		h.launchFromConfig(w, r, rt.ID)

		return
	}

	serveCRUD(w, r, rt, crud{
		create: h.createInstanceConfig,
		list:   h.listInstanceConfigs,
		get:    h.getInstanceConfig,
		update: h.updateInstanceConfig,
		remove: h.deleteInstanceConfig,
	})
}

func (h *Handler) createInstanceConfig(w http.ResponseWriter, r *http.Request) {
	var req instanceConfigurationRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.CompartmentID == "" {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter, "compartmentId is required")
		return
	}

	launch, ok := configLaunch(w, r, req.InstanceDetails)
	if !ok {
		return
	}

	cfg, err := h.extras.CreateInstanceConfiguration(r.Context(), req.DisplayName, launch,
		freeformOf(req.FreeformTags))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.place(cfg.ID, req.CompartmentID)
	h.accept(w, "CREATE_INSTANCE_CONFIGURATION", req.CompartmentID, "instanceconfiguration",
		workrequest.ActionCreated, cfg.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceConfigResponse(cfg))
}

// configLaunch reads the launch details an instance configuration saves,
// rejecting the instance types CloudEmu does not model.
func configLaunch(
	w http.ResponseWriter, r *http.Request, details *instanceConfigurationDetails,
) (ocicompute.LaunchSpec, bool) {
	if details == nil || details.LaunchDetails == nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"instanceDetails.launchDetails is required")

		return ocicompute.LaunchSpec{}, false
	}

	if details.InstanceType != "" && details.InstanceType != instanceTypeCompute {
		ocirest.WriteError(w, r, http.StatusNotImplemented, codeNotImplemented,
			"instance configuration type "+details.InstanceType+" is not emulated; use compute")

		return ocicompute.LaunchSpec{}, false
	}

	return toLaunchSpec(details.LaunchDetails), true
}

// toLaunchSpec projects OCI's saved launch details onto the provider's.
func toLaunchSpec(l *instanceConfigurationLaunch) ocicompute.LaunchSpec {
	spec := ocicompute.LaunchSpec{
		AvailabilityDomain: l.AvailabilityDomain,
		FaultDomain:        l.FaultDomain,
		DisplayName:        l.DisplayName,
		Shape:              l.Shape,
		ShapeConfig:        toShapeConfig(l.ShapeConfig),
		Metadata:           l.Metadata,
		IsPreemptible:      l.PreemptibleInstanceConfig != nil,
		Tags:               freeformOf(l.FreeformTags),
	}

	if source := toSourceDetails(l.SourceDetails); source.ID != "" {
		spec.ImageID = source.ID
	}

	if l.CreateVnicDetails != nil {
		spec.SubnetID = l.CreateVnicDetails.SubnetID
		spec.NSGIDs = l.CreateVnicDetails.NsgIDs
	}

	return spec
}

func (h *Handler) listInstanceConfigs(w http.ResponseWriter, r *http.Request) {
	compartmentID, given := ocirest.RequireCompartmentID(w, r)
	if !given {
		return
	}

	configs, err := h.extras.ListInstanceConfigurations(r.Context(), compartmentID)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	renderPage(w, r, configs, h.toInstanceConfigResponse)
}

func (h *Handler) getInstanceConfig(w http.ResponseWriter, r *http.Request, id string) {
	cfg, err := h.extras.GetInstanceConfiguration(r.Context(), id)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceConfigResponse(cfg))
}

func (h *Handler) updateInstanceConfig(w http.ResponseWriter, r *http.Request, id string) {
	var req instanceConfigurationRequest

	if !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	if req.InstanceDetails != nil {
		ocirest.WriteError(w, r, http.StatusBadRequest, codeInvalidParameter,
			"instanceDetails cannot be updated; create a new instance configuration")

		return
	}

	cfg, err := h.extras.UpdateInstanceConfiguration(r.Context(), id,
		displayNameUpdate(req.DisplayName, req.FreeformTags))
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceConfigResponse(cfg))
}

func (h *Handler) deleteInstanceConfig(w http.ResponseWriter, r *http.Request, id string) {
	compartmentID := h.compartmentOf(id)

	if err := h.extras.DeleteInstanceConfiguration(r.Context(), id); err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	h.accept(w, "DELETE_INSTANCE_CONFIGURATION", compartmentID, "instanceconfiguration",
		workrequest.ActionDeleted, id)
	ocirest.WriteJSON(w, r, http.StatusNoContent, nil)
}

// launchFromConfig launches one instance from a saved configuration.
func (h *Handler) launchFromConfig(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, r)
		return
	}

	var req launchConfigurationRequest

	// The override body is optional, so a decode failure on an empty body is
	// not an error; only a malformed non-empty body is.
	if r.ContentLength > 0 && !ocirest.DecodeJSON(w, r, &req) {
		return
	}

	var overrides *ocicompute.LaunchSpec

	if req.LaunchDetails != nil {
		spec := toLaunchSpec(req.LaunchDetails)
		overrides = &spec
	}

	inst, err := h.extras.LaunchFromInstanceConfiguration(r.Context(), id, overrides)
	if err != nil {
		ocirest.WriteDriverError(w, r, err)
		return
	}

	compartmentID := h.compartmentOf(id)
	h.place(inst.ID, compartmentID)
	h.accept(w, "LAUNCH_INSTANCE", compartmentID, "instance", workrequest.ActionCreated, inst.ID)
	ocirest.WriteJSON(w, r, http.StatusOK, h.toInstanceResponse(inst))
}

func (h *Handler) toInstanceConfigResponse(c *ocicompute.InstanceConfiguration) instanceConfigurationResponse {
	launch := &instanceConfigurationLaunch{
		AvailabilityDomain: c.Launch.AvailabilityDomain,
		FaultDomain:        c.Launch.FaultDomain,
		DisplayName:        c.Launch.DisplayName,
		Shape:              c.Launch.Shape,
		ShapeConfig:        toShapeConfigWire(c.Launch.ShapeConfig),
		Metadata:           c.Launch.Metadata,
		FreeformTags:       freeformOf(c.Launch.Tags),
	}

	if c.Launch.ImageID != "" {
		launch.SourceDetails = &sourceDetailsWire{SourceType: "image", ImageID: c.Launch.ImageID, ID: c.Launch.ImageID}
	}

	if c.Launch.SubnetID != "" {
		launch.CreateVnicDetails = &createVnicDetails{SubnetID: c.Launch.SubnetID, NsgIDs: c.Launch.NSGIDs}
	}

	if c.Launch.IsPreemptible {
		launch.PreemptibleInstanceConfig = &preemptibleInstanceConfig{
			PreemptionAction: preemptionAction{Type: preemptionTerminate},
		}
	}

	return instanceConfigurationResponse{
		ID:            c.ID,
		CompartmentID: h.compartmentOf(c.ID),
		DisplayName:   c.DisplayName,
		InstanceType:  instanceTypeCompute,
		InstanceDetails: &instanceConfigurationDetails{
			InstanceType:  instanceTypeCompute,
			LaunchDetails: launch,
		},
		TimeCreated:  c.TimeCreated,
		FreeformTags: freeformOf(c.Tags),
		DefinedTags:  definedTags{},
	}
}
