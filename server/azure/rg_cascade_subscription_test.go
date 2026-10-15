package azure_test

import (
	"net/http"
	"testing"
)

// TestResourceGroupDeleteStaysInSubscription is the AZRM-N9 regression:
// deleting resource group "shared" in one subscription must not purge the
// compute, AKS or ACR resources of a same-named group in another subscription.
func TestResourceGroupDeleteStaysInSubscription(t *testing.T) {
	c := newCasClient(t)

	const rg = "shared"

	group := func(sub string) string { return "/subscriptions/" + sub + "/resourceGroups/" + rg }
	res := func(sub, typ, name string) string { return group(sub) + "/providers/" + typ + "/" + name }

	seed := func(sub, suffix string) []string {
		c.mustPut(group(sub), `{"location":"eastus"}`)

		disk := res(sub, "Microsoft.Compute/disks", "d"+suffix)
		c.mustPut(disk, `{"location":"eastus","properties":{"creationData":{"createOption":"Empty"},"diskSizeGB":4}}`)

		vm := res(sub, "Microsoft.Compute/virtualMachines", "vm"+suffix)
		c.mustPut(vm, `{"location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_B1s"}}}`)

		key := res(sub, "Microsoft.Compute/sshPublicKeys", "k"+suffix)
		c.mustPut(key, `{"location":"eastus","properties":{"publicKey":"`+casSSHPK+`"}}`)

		acr := res(sub, "Microsoft.ContainerRegistry/registries", "acr"+suffix)
		c.mustPut(acr, `{"location":"eastus","sku":{"name":"Standard"}}`)

		aks := res(sub, "Microsoft.ContainerService/managedClusters", "aks"+suffix)
		c.mustPut(aks, `{"location":"eastus","properties":{"dnsPrefix":"n9","agentPoolProfiles":[`+
			`{"name":"sys","count":1,"vmSize":"Standard_D2s_v3","mode":"System"}]}}`)

		return []string{"d" + suffix, "vm" + suffix, "k" + suffix, "acr" + suffix, "aks" + suffix}
	}

	types := []string{
		"Microsoft.Compute/disks", "Microsoft.Compute/virtualMachines", "Microsoft.Compute/sshPublicKeys",
		"Microsoft.ContainerRegistry/registries", "Microsoft.ContainerService/managedClusters",
	}

	gone := seed("sub-a", "a")
	kept := seed("sub-b", "b")

	c.wantStatus(http.MethodDelete, group("sub-a"), http.StatusAccepted)

	// Lookups are not subscription-scoped, so reading through sub-b's group
	// shows whether each resource still exists anywhere.
	for i, typ := range types {
		c.wantStatus(http.MethodGet, res("sub-b", typ, gone[i]), http.StatusNotFound)
		c.wantStatus(http.MethodGet, res("sub-b", typ, kept[i]), http.StatusOK)
	}
}
