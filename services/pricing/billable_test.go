package pricing_test

import (
	"testing"

	"github.com/stackshy/cloudemu/v2/services/pricing"
)

// TestComputeInstanceBillable pins the state-aware compute gate: running/pending
// (and an unknown/empty state) bill compute; the canonical non-running VM
// lifecycle states the walker surfaces (stopping/stopped/shutting-down/
// terminated — the latter also covers GCP's stopped and Azure's stopped/
// deallocated, which settle to these values) bill $0; and non-compute resources
// are always billable regardless of any state string.
func TestComputeInstanceBillable(t *testing.T) {
	cases := []struct {
		service, resourceType, state string
		want                         bool
	}{
		{"compute", "Instance", "running", true},
		{"compute", "Instance", "pending", true},
		{"compute", "Instance", "", true},        // unknown state stays billable
		{"compute", "Instance", "Running", true}, // case-insensitive
		{"compute", "Instance", "stopped", false},
		{"compute", "Instance", "stopping", false},
		{"compute", "Instance", "shutting-down", false},
		{"compute", "Instance", "terminated", false}, // AWS terminate, GCP stop
		{"compute", "Instance", "TERMINATED", false}, // case-insensitive
		// A state the walker never surfaces (e.g. Azure's deallocated, which lives
		// in a separate PowerState field) is unknown here, so it stays billable.
		{"compute", "Instance", "deallocated", true},
		// Non-compute resources are never gated, even carrying a stopped state.
		{"compute", "Volume", "stopped", true},
		{"relationaldb", "DBInstance", "terminated", true},
		{"loadbalancer", "LoadBalancer", "stopped", true},
	}

	for _, c := range cases {
		got := pricing.ComputeInstanceBillable(c.service, c.resourceType, c.state)
		if got != c.want {
			t.Errorf("ComputeInstanceBillable(%q,%q,%q) = %v, want %v",
				c.service, c.resourceType, c.state, got, c.want)
		}
	}
}
