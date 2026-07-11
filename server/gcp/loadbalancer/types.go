package loadbalancer

// GCP Compute Load Balancing REST shapes. Only the subset of fields the
// loadbalancer driver can populate is modeled; the compute SDK ignores unknown
// members on the wire.

// --- backend services (→ driver target groups) ---

type backendServiceRequest struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Protocol     string   `json:"protocol,omitempty"`
	Port         int      `json:"port,omitempty"`
	PortName     string   `json:"portName,omitempty"`
	HealthChecks []string `json:"healthChecks,omitempty"`
}

type backendServiceResponse struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	Port        int    `json:"port,omitempty"`
	SelfLink    string `json:"selfLink"`
}

type backendServiceListResponse struct {
	Kind     string                   `json:"kind"`
	ID       string                   `json:"id"`
	Items    []backendServiceResponse `json:"items"`
	SelfLink string                   `json:"selfLink"`
}

// --- forwarding rules (→ driver load balancers) ---

type forwardingRuleRequest struct {
	Name                string `json:"name"`
	Description         string `json:"description,omitempty"`
	IPProtocol          string `json:"IPProtocol,omitempty"`
	PortRange           string `json:"portRange,omitempty"`
	Target              string `json:"target,omitempty"`
	BackendService      string `json:"backendService,omitempty"`
	LoadBalancingScheme string `json:"loadBalancingScheme,omitempty"`
}

type forwardingRuleResponse struct {
	Kind                string `json:"kind"`
	ID                  string `json:"id"`
	Name                string `json:"name"`
	Description         string `json:"description,omitempty"`
	IPAddress           string `json:"IPAddress,omitempty"`
	IPProtocol          string `json:"IPProtocol,omitempty"`
	PortRange           string `json:"portRange,omitempty"`
	Target              string `json:"target,omitempty"`
	BackendService      string `json:"backendService,omitempty"`
	LoadBalancingScheme string `json:"loadBalancingScheme,omitempty"`
	SelfLink            string `json:"selfLink"`
}

type forwardingRuleListResponse struct {
	Kind     string                   `json:"kind"`
	ID       string                   `json:"id"`
	Items    []forwardingRuleResponse `json:"items"`
	SelfLink string                   `json:"selfLink"`
}
