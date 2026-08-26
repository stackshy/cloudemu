package loadbalancer

// GCP Compute Load Balancing REST shapes. Only the subset of fields the
// loadbalancer driver can populate is modeled; the compute SDK ignores unknown
// members on the wire.

// --- backend services (→ driver target groups) ---

// backend is one entry of a backend service's backends[] — the instance-group
// or NEG reference plus its balancing knobs. google_compute_backend_service
// sends this on every apply, so it must round-trip.
type backend struct {
	Group          string  `json:"group,omitempty"`
	BalancingMode  string  `json:"balancingMode,omitempty"`
	CapacityScaler float64 `json:"capacityScaler,omitempty"`
	MaxUtilization float64 `json:"maxUtilization,omitempty"`
}

// connectionDraining is the backend service's connection-draining policy.
type connectionDraining struct {
	DrainingTimeoutSec int `json:"drainingTimeoutSec,omitempty"`
}

// cdnPolicy is the subset of a backend service's Cloud CDN policy the emulator
// round-trips verbatim (the full body is preserved as an opaque JSON blob).
type cdnPolicy struct {
	CacheMode  string `json:"cacheMode,omitempty"`
	DefaultTTL int    `json:"defaultTtl,omitempty"`
	ClientTTL  int    `json:"clientTtl,omitempty"`
	MaxTTL     int    `json:"maxTtl,omitempty"`
}

type backendServiceRequest struct {
	Name                string              `json:"name"`
	Description         string              `json:"description,omitempty"`
	Protocol            string              `json:"protocol,omitempty"`
	Port                int                 `json:"port,omitempty"`
	PortName            string              `json:"portName,omitempty"`
	HealthChecks        []string            `json:"healthChecks,omitempty"`
	LoadBalancingScheme string              `json:"loadBalancingScheme,omitempty"`
	SessionAffinity     string              `json:"sessionAffinity,omitempty"`
	TimeoutSec          int                 `json:"timeoutSec,omitempty"`
	Backends            []backend           `json:"backends,omitempty"`
	ConnectionDraining  *connectionDraining `json:"connectionDraining,omitempty"`
	CdnPolicy           *cdnPolicy          `json:"cdnPolicy,omitempty"`
	EnableCDN           *bool               `json:"enableCDN,omitempty"`
}

type backendServiceResponse struct {
	Kind                string              `json:"kind"`
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	Description         string              `json:"description,omitempty"`
	Protocol            string              `json:"protocol,omitempty"`
	Port                int                 `json:"port,omitempty"`
	PortName            string              `json:"portName,omitempty"`
	HealthChecks        []string            `json:"healthChecks,omitempty"`
	LoadBalancingScheme string              `json:"loadBalancingScheme,omitempty"`
	SessionAffinity     string              `json:"sessionAffinity,omitempty"`
	TimeoutSec          int                 `json:"timeoutSec,omitempty"`
	Backends            []backend           `json:"backends,omitempty"`
	ConnectionDraining  *connectionDraining `json:"connectionDraining,omitempty"`
	CdnPolicy           *cdnPolicy          `json:"cdnPolicy,omitempty"`
	EnableCDN           *bool               `json:"enableCDN,omitempty"`
	Fingerprint         string              `json:"fingerprint,omitempty"`
	CreationTimestamp   string              `json:"creationTimestamp,omitempty"`
	SelfLink            string              `json:"selfLink"`
}

type backendServiceListResponse struct {
	Kind          string                   `json:"kind"`
	ID            string                   `json:"id"`
	Items         []backendServiceResponse `json:"items"`
	NextPageToken string                   `json:"nextPageToken,omitempty"`
	SelfLink      string                   `json:"selfLink"`
}

// --- forwarding rules (→ driver load balancers) ---

type forwardingRuleRequest struct {
	Name                string `json:"name"`
	Description         string `json:"description,omitempty"`
	IPAddress           string `json:"IPAddress,omitempty"`
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
	CreationTimestamp   string `json:"creationTimestamp,omitempty"`
	SelfLink            string `json:"selfLink"`
}

type forwardingRuleListResponse struct {
	Kind          string                   `json:"kind"`
	ID            string                   `json:"id"`
	Items         []forwardingRuleResponse `json:"items"`
	NextPageToken string                   `json:"nextPageToken,omitempty"`
	SelfLink      string                   `json:"selfLink"`
}
