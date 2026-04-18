package topology

import (
	computedriver "github.com/stackshy/cloudemu/compute/driver"
	dnsdriver "github.com/stackshy/cloudemu/dns/driver"
	netdriver "github.com/stackshy/cloudemu/networking/driver"
)

// Engine evaluates network topology and connectivity between cloud resources.
type Engine struct {
	compute    computedriver.Compute
	networking netdriver.Networking
	dns        dnsdriver.DNS
}

// New creates a new topology Engine that reads state from the provided
// compute, networking, and DNS services.
func New(
	compute computedriver.Compute,
	networking netdriver.Networking,
	dns dnsdriver.DNS,
) *Engine {
	return &Engine{
		compute:    compute,
		networking: networking,
		dns:        dns,
	}
}
