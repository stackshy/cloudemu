package virtualmachines

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	providervm "github.com/stackshy/cloudemu/v2/providers/azure/virtualmachines"
	"github.com/stackshy/cloudemu/v2/server/wire/azurearm"
)

// defaultVMSSLocation is the location reported for a scale set (and its VMs)
// whose stored location is unknown.
const defaultVMSSLocation = "eastus"

// serveScaleSetSubResource dispatches the sub-resource paths of a scale set:
// the per-instance VM surface (.../virtualMachines[/{instanceId}[/{action}]])
// and the scale-set-level instanceView.
//
//nolint:gocritic // rp is a request-scoped value passed once per request
func serveScaleSetSubResource(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	switch strings.ToLower(rp.SubResource) {
	case "virtualmachines":
		serveScaleSetVM(w, r, rp, store)
	case "instanceview":
		if r.Method != http.MethodGet {
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
			return
		}

		scaleSetInstanceView(w, r, rp, store)
	case actionStart, actionPowerOff, actionDeallocate, actionRestart, actionReimage:
		if r.Method != http.MethodPost {
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
			return
		}

		scaleSetPowerAction(w, r, rp, store)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

// scaleSetPowerAction handles the whole-VMSS POST power actions
// (.../virtualMachineScaleSets/{name}/start|powerOff|deallocate|restart|reimage).
// It fans the action out to every instance of the set, or to the subset named by
// the optional {"instanceIds":[...]} body, then returns 202 + async polling
// headers the SDK poller settles as Succeeded.
//
//nolint:gocritic // rp is a request-scoped value
func scaleSetPowerAction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	ids := decodeScaleSetInstanceIDs(r)

	if err := store.PowerScaleSet(r.Context(), rp.ResourceName, rp.SubResource, ids); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vmss-"+strings.ToLower(rp.SubResource)+"-"+rp.ResourceName)
}

// decodeScaleSetInstanceIDs best-effort reads the optional {"instanceIds":[...]}
// body of a whole-VMSS power action. A missing, empty, or unparseable body
// targets every instance (nil), matching the SDK's no-body whole-set call.
func decodeScaleSetInstanceIDs(r *http.Request) []string {
	if r.Body == nil {
		return nil
	}

	var body struct {
		InstanceIDs []string `json:"instanceIds"`
	}

	if err := json.NewDecoder(io.LimitReader(r.Body, azurearm.MaxBodyBytes)).Decode(&body); err != nil {
		return nil
	}

	return body.InstanceIDs
}

// serveScaleSetVM routes the VMSS VM surface: LIST (collection), GET/DELETE (a
// single instance), and the POST power actions (start/powerOff/deallocate/
// restart/reimage). rp.SubResourceName is the instanceId; rp.SubResourceAction
// is the power verb when present.
//
//nolint:gocritic // rp is a request-scoped value passed once per request
func serveScaleSetVM(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	if rp.SubResourceName == "" {
		if r.Method == http.MethodGet {
			listScaleSetVMs(w, r, rp, store)
			return
		}

		writeNotImplemented(w, r.Method+" "+r.URL.Path)

		return
	}

	if rp.SubResourceAction != "" {
		// instanceView is a GET sub-path on a single instance; the power verbs
		// (start/powerOff/deallocate/restart/reimage) are POST actions.
		if strings.EqualFold(rp.SubResourceAction, "instanceView") {
			if r.Method != http.MethodGet {
				writeNotImplemented(w, r.Method+" "+r.URL.Path)
				return
			}

			scaleSetVMInstanceView(w, r, rp, store)

			return
		}

		if r.Method != http.MethodPost {
			writeNotImplemented(w, r.Method+" "+r.URL.Path)
			return
		}

		scaleSetVMPowerAction(w, r, rp, store)

		return
	}

	switch r.Method {
	case http.MethodGet:
		getScaleSetVM(w, r, rp, store)
	case http.MethodDelete:
		deleteScaleSetVM(w, r, rp, store)
	default:
		writeNotImplemented(w, r.Method+" "+r.URL.Path)
	}
}

// listScaleSetVMs handles GET .../virtualMachineScaleSets/{vmss}/virtualMachines.
//
//nolint:gocritic // rp is a request-scoped value
func listScaleSetVMs(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	vms, err := store.ListScaleSetVMs(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := scaleSetLocation(r.Context(), store, rp.ResourceName)
	out := make([]vmssVMResponse, 0, len(vms))

	for i := range vms {
		out = append(out, toVMSSVMResponse(rp, vms[i], loc))
	}

	azurearm.WriteJSON(w, http.StatusOK, vmssVMListResponse{Value: out})
}

// getScaleSetVM handles GET .../virtualMachines/{instanceId}.
//
//nolint:gocritic // rp is a request-scoped value
func getScaleSetVM(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	vm, err := store.GetScaleSetVM(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	loc := scaleSetLocation(r.Context(), store, rp.ResourceName)

	azurearm.WriteJSON(w, http.StatusOK, toVMSSVMResponse(rp, *vm, loc))
}

// deleteScaleSetVM handles DELETE .../virtualMachines/{instanceId}. Returns 202
// + the async-operation polling headers so the SDK poller settles cleanly, and
// a follow-up list reports one fewer instance.
//
//nolint:gocritic // rp is a request-scoped value
func deleteScaleSetVM(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	if err := store.DeleteScaleSetVM(r.Context(), rp.ResourceName, rp.SubResourceName); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vmss-vm-delete-"+rp.ResourceName+"-"+rp.SubResourceName)
}

// scaleSetVMPowerAction handles the POST power actions on a single instance
// (start/powerOff/deallocate/restart/reimage). Returns 202 + async polling
// headers.
//
//nolint:gocritic // rp is a request-scoped value
func scaleSetVMPowerAction(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	if err := store.PowerScaleSetVM(r.Context(), rp.ResourceName, rp.SubResourceName, rp.SubResourceAction); err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	writeAcceptedAsync(w, r, rp.Subscription, "vmss-vm-"+rp.SubResourceAction+"-"+rp.ResourceName+"-"+rp.SubResourceName)
}

// scaleSetVMInstanceView handles GET .../virtualMachines/{instanceId}/instanceView,
// returning the single instance's VirtualMachineScaleSetVMInstanceView statuses.
//
//nolint:gocritic // rp is a request-scoped value
func scaleSetVMInstanceView(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	vm, err := store.GetScaleSetVM(r.Context(), rp.ResourceName, rp.SubResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	power := defaultIfEmpty(vm.PowerState, "running")

	azurearm.WriteJSON(w, http.StatusOK, instanceView{
		Statuses: []instanceViewStatus{
			{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
			{Code: "PowerState/" + power, Level: "Info", DisplayStatus: "VM " + power},
		},
	})
}

// scaleSetInstanceView handles GET .../virtualMachineScaleSets/{name}/instanceView.
//
//nolint:gocritic // rp is a request-scoped value
func scaleSetInstanceView(w http.ResponseWriter, r *http.Request, rp azurearm.ResourcePath, store scaleSetStore) {
	vms, err := store.ListScaleSetVMs(r.Context(), rp.ResourceName)
	if err != nil {
		azurearm.WriteCErr(w, err)
		return
	}

	counts := map[string]int{}
	for i := range vms {
		counts["ProvisioningState/"+strings.ToLower(defaultIfEmpty(vms[i].ProvisioningState, "succeeded"))]++
	}

	summary := make([]vmssStatusSummary, 0, len(counts))
	for code, n := range counts {
		summary = append(summary, vmssStatusSummary{Code: code, Count: n})
	}

	azurearm.WriteJSON(w, http.StatusOK, vmssInstanceViewResponse{
		VirtualMachine: &vmssVMStatusesSummary{StatusesSummary: summary},
		Statuses: []instanceViewStatus{
			{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
		},
	})
}

// scaleSetLocation resolves a scale set's location for rendering its VMs'
// required location field, falling back to eastus when unknown.
func scaleSetLocation(ctx context.Context, store scaleSetStore, name string) string {
	sets, err := store.ListScaleSets(ctx)
	if err != nil {
		return defaultVMSSLocation
	}

	for i := range sets {
		if strings.EqualFold(sets[i].Name, name) {
			return defaultIfEmpty(sets[i].Location, defaultVMSSLocation)
		}
	}

	return defaultVMSSLocation
}

// toVMSSVMResponse maps a stored ScaleSetVM onto the ARM wire shape. The id is
// the nested VMSS-VM resource id; the name is Azure's "{vmss}_{instanceId}".
//
//nolint:gocritic // rp is a value type passed once per response build
func toVMSSVMResponse(rp azurearm.ResourcePath, vm providervm.ScaleSetVM, location string) vmssVMResponse {
	base := azurearm.BuildResourceID(rp.Subscription, rp.ResourceGroup, providerName, resourceTypeScaleSets, rp.ResourceName)
	power := defaultIfEmpty(vm.PowerState, "running")

	return vmssVMResponse{
		ID:         base + "/virtualMachines/" + vm.InstanceID,
		Name:       rp.ResourceName + "_" + vm.InstanceID,
		Type:       providerName + "/" + resourceTypeScaleSets + "/virtualMachines",
		Location:   location,
		InstanceID: vm.InstanceID,
		Properties: vmssVMResponseProps{
			ProvisioningState: defaultIfEmpty(vm.ProvisioningState, "Succeeded"),
			InstanceView: &instanceView{
				Statuses: []instanceViewStatus{
					{Code: "ProvisioningState/succeeded", Level: "Info", DisplayStatus: "Provisioning succeeded"},
					{Code: "PowerState/" + power, Level: "Info", DisplayStatus: "VM " + power},
				},
			},
		},
	}
}
