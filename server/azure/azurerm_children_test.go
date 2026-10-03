package azure_test

import (
	"net/http"
	"testing"
)

const vmBody = `{"location":"eastus","properties":{"hardwareProfile":{"vmSize":"Standard_B1s"}}}`

// TestVMExtensionLifecycle covers azurerm_virtual_machine_extension: CRUD under
// the VM, protectedSettings never read back, and removal with the VM and with
// the resource group.
func TestVMExtensionLifecycle(t *testing.T) {
	c := newCasClient(t)
	c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)

	vm := resPath("rg1", "Microsoft.Compute/virtualMachines", "vm1")
	c.mustPut(vm, vmBody)

	ext := vm + "/extensions/cse"
	c.mustPut(ext, `{"location":"eastus","tags":{"v":"1"},"properties":{"publisher":"Microsoft.Azure.Extensions",`+
		`"type":"CustomScript","typeHandlerVersion":"2.1","settings":{"commandToExecute":"echo hi"},`+
		`"protectedSettings":{"secret":"s3cret"}}}`)

	code, out := c.do(http.MethodGet, ext, "")
	if code != http.StatusOK {
		t.Fatalf("GET extension = %d %v", code, out)
	}

	props, _ := out["properties"].(map[string]any)
	if _, leaked := props["protectedSettings"]; leaked {
		t.Error("protectedSettings returned on GET")
	}

	settings, _ := props["settings"].(map[string]any)
	if settings["commandToExecute"] != "echo hi" || props["provisioningState"] != "Succeeded" {
		t.Errorf("extension properties = %v", props)
	}

	if n := len(listValue(c, vm+"/extensions")); n != 1 {
		t.Errorf("extension list has %d items, want 1", n)
	}

	c.wantStatus(http.MethodDelete, ext, http.StatusOK)
	c.wantStatus(http.MethodGet, ext, http.StatusNotFound)

	// An extension does not outlive its VM, even when a VM of the same name
	// comes back.
	c.mustPut(ext, `{"location":"eastus","properties":{"publisher":"p","type":"t","typeHandlerVersion":"1.0"}}`)
	c.wantStatus(http.MethodDelete, vm, http.StatusAccepted)
	c.mustPut(vm, vmBody)
	c.wantStatus(http.MethodGet, ext, http.StatusNotFound)

	// The resource group cascade takes the VM and its extensions.
	c.mustPut(ext, `{"location":"eastus","properties":{"publisher":"p","type":"t","typeHandlerVersion":"1.0"}}`)
	c.wantStatus(http.MethodDelete, rgPath("rg1"), http.StatusAccepted)
	c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)
	c.mustPut(vm, vmBody)
	c.wantStatus(http.MethodGet, ext, http.StatusNotFound)
}

// TestLoadBalancerAlwaysReturnsChildArrays: azurerm_lb_probe, azurerm_lb_rule
// and azurerm_lb_outbound_rule read the load balancer and dereference its child
// arrays before a whole-LB PUT, so a GET must carry every array, empty or not,
// as real ARM does.
func TestLoadBalancerAlwaysReturnsChildArrays(t *testing.T) {
	c := newCasClient(t)
	c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)

	lb := resPath("rg1", "Microsoft.Network/loadBalancers", "lb1")
	c.mustPut(lb, `{"location":"eastus","sku":{"name":"Standard"},"properties":{"frontendIPConfigurations":`+
		`[{"name":"fe","properties":{"privateIPAllocationMethod":"Dynamic"}}]}}`)

	_, out := c.do(http.MethodGet, lb, "")
	props, _ := out["properties"].(map[string]any)

	for _, k := range []string{
		"backendAddressPools", "loadBalancingRules", "probes", "inboundNatRules", "inboundNatPools", "outboundRules",
	} {
		if arr, ok := props[k].([]any); !ok || len(arr) != 0 {
			t.Errorf("properties.%s = %v, want []", k, props[k])
		}
	}

	// A rule pointing at a probe the body does not carry is rejected.
	code, _ := c.do(http.MethodPut, lb, `{"location":"eastus","sku":{"name":"Standard"},"properties":{`+
		`"frontendIPConfigurations":[{"name":"fe","properties":{}}],"loadBalancingRules":[{"name":"r",`+
		`"properties":{"protocol":"Tcp","frontendPort":80,"backendPort":80,"frontendIPConfiguration":{"id":"`+
		lb+`/frontendIPConfigurations/fe"},"probe":{"id":"`+lb+`/probes/missing"}}}]}}`)
	if code != http.StatusBadRequest {
		t.Errorf("rule with a missing probe = %d, want 400", code)
	}
}

// TestFirewallPolicyRuleCollectionGroups covers
// azurerm_firewall_policy_rule_collection_group: CRUD, priority validation and
// removal with the policy.
func TestFirewallPolicyRuleCollectionGroups(t *testing.T) {
	c := newCasClient(t)
	c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)

	pol := resPath("rg1", "Microsoft.Network/firewallPolicies", "fp1")
	c.mustPut(pol, `{"location":"eastus"}`)

	group := pol + "/ruleCollectionGroups/g1"
	coll := `[{"name":"net","priority":400,"ruleCollectionType":"FirewallPolicyFilterRuleCollection",` +
		`"action":{"type":"Allow"},"rules":[]}]`

	for _, bad := range []string{
		`{"properties":{"priority":50,"ruleCollections":[]}}`,
		`{"properties":{"priority":500,"ruleCollections":[{"name":"x","priority":70000}]}}`,
	} {
		if code, _ := c.do(http.MethodPut, group, bad); code != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", bad, code)
		}
	}

	if code, _ := c.do(http.MethodPut, pol+"/ruleCollectionGroups/g0", `{"properties":{"priority":500}}`); code != http.StatusCreated {
		t.Fatalf("PUT g0 = %d, want 201", code)
	}

	c.mustPut(group, `{"properties":{"priority":500,"ruleCollections":`+coll+`}}`)

	code, out := c.do(http.MethodGet, group, "")
	props, _ := out["properties"].(map[string]any)

	if cols, _ := props["ruleCollections"].([]any); code != http.StatusOK || props["priority"] != float64(500) || len(cols) != 1 {
		t.Fatalf("GET group = %d %v", code, out)
	}

	if n := len(listValue(c, pol+"/ruleCollectionGroups")); n != 2 {
		t.Errorf("group list has %d items, want 2", n)
	}

	c.wantStatus(http.MethodDelete, pol+"/ruleCollectionGroups/g0", http.StatusOK)

	// Groups go with their policy: a recreated policy starts empty.
	c.wantStatus(http.MethodDelete, pol, http.StatusOK)
	c.mustPut(pol, `{"location":"eastus"}`)
	c.wantStatus(http.MethodGet, group, http.StatusNotFound)
}
