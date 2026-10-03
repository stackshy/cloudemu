package azure_test

import (
	"net/http"
	"testing"
)

// TestAvailabilitySetRules covers the real Azure constraints on availability
// sets and the VMs placed in them.
func TestAvailabilitySetRules(t *testing.T) {
	c := newCasClient(t)
	c.mustPut(rgPath("rg1"), `{"location":"eastus"}`)
	c.mustPut(rgPath("rg2"), `{"location":"eastus"}`)

	set := func(rg, name, loc string) string {
		p := resPath(rg, "Microsoft.Compute/availabilitySets", name)
		c.mustPut(p, `{"location":"`+loc+`","properties":{"platformFaultDomainCount":2,"platformUpdateDomainCount":5}}`)

		return p
	}
	vmIn := func(as string) string {
		return `{"location":"eastus","properties":{"availabilitySet":{"id":"` + as + `"}}}`
	}

	as1 := set("rg1", "as1", "eastus")
	as2 := set("rg1", "as2", "eastus")
	other := set("rg2", "asx", "eastus")
	west := set("rg1", "asw", "westeurope")

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
		code   string
	}{
		{
			"create without location", http.MethodPut, resPath("rg1", "Microsoft.Compute/availabilitySets", "noloc"),
			`{"properties":{"platformFaultDomainCount":2,"platformUpdateDomainCount":5}}`,
			http.StatusBadRequest, "LocationRequired",
		},
		{
			"PATCH fault domains", http.MethodPatch, as1, `{"properties":{"platformFaultDomainCount":3}}`,
			http.StatusConflict, "PropertyChangeNotAllowed",
		},
		{
			"PUT update domains", http.MethodPut, as1,
			`{"location":"eastus","properties":{"platformFaultDomainCount":2,"platformUpdateDomainCount":10}}`,
			http.StatusConflict, "PropertyChangeNotAllowed",
		},
		{
			"VM in another group's set", http.MethodPut, resPath("rg1", "Microsoft.Compute/virtualMachines", "vmrg"),
			vmIn(other), http.StatusBadRequest, "InvalidParameter",
		},
		{
			"VM in another region's set", http.MethodPut, resPath("rg1", "Microsoft.Compute/virtualMachines", "vmloc"),
			vmIn(west), http.StatusBadRequest, "InvalidParameter",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out := c.do(tc.method, tc.path, tc.body)
			if code != tc.want || errorCode(out) != tc.code {
				t.Errorf("= %d %q, want %d %q (%v)", code, errorCode(out), tc.want, tc.code, out)
			}
		})
	}

	// A tag-only PATCH still works.
	if code, out := c.do(http.MethodPatch, as1, `{"tags":{"k":"v"}}`); code != http.StatusOK {
		t.Fatalf("tag PATCH = %d %v", code, out)
	}

	vm := resPath("rg1", "Microsoft.Compute/virtualMachines", "vm1")
	c.mustPut(vm, vmIn(as1))

	// Moving the VM to another set is not allowed; a re-PUT in the same set is.
	if code, out := c.do(http.MethodPut, vm, vmIn(as2)); code != http.StatusConflict ||
		errorCode(out) != "PropertyChangeNotAllowed" {
		t.Errorf("move VM to as2 = %d %v, want 409", code, out)
	}

	c.mustPut(vm, vmIn(as1))

	// A set with VMs in it cannot be deleted and stays.
	if code, out := c.do(http.MethodDelete, as1, ""); code != http.StatusConflict ||
		errorCode(out) != "OperationNotAllowed" {
		t.Errorf("delete set with a VM = %d %v, want 409", code, out)
	}

	_, out := c.do(http.MethodGet, as1, "")
	props, _ := out["properties"].(map[string]any)

	if props["platformFaultDomainCount"] != float64(2) || props["platformUpdateDomainCount"] != float64(5) {
		t.Errorf("set after rejected changes = %v", props)
	}

	c.wantStatus(http.MethodDelete, vm, http.StatusAccepted)
	c.wantStatus(http.MethodDelete, as1, http.StatusOK)
}
