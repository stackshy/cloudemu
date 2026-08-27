package vpc

import "encoding/json"

// GCP Compute Engine networking REST shapes.

// networkRoutingConfig is GCP's routingConfig block; only routingMode
// (REGIONAL|GLOBAL) is modeled.
type networkRoutingConfig struct {
	RoutingMode string `json:"routingMode,omitempty"`
}

type networkRequest struct {
	Name                  string                `json:"name"`
	Description           string                `json:"description,omitempty"`
	IPv4Range             string                `json:"IPv4Range,omitempty"`
	AutoCreateSubnetworks *bool                 `json:"autoCreateSubnetworks,omitempty"`
	RoutingConfig         *networkRoutingConfig `json:"routingConfig,omitempty"`
	Mtu                   int32                 `json:"mtu,omitempty"`
}

type networkResponse struct {
	Kind                  string                `json:"kind"`
	ID                    string                `json:"id"`
	Name                  string                `json:"name"`
	Description           string                `json:"description,omitempty"`
	SelfLink              string                `json:"selfLink"`
	IPv4Range             string                `json:"IPv4Range,omitempty"`
	AutoCreateSubnetworks bool                  `json:"autoCreateSubnetworks"`
	RoutingConfig         *networkRoutingConfig `json:"routingConfig,omitempty"`
	Mtu                   int32                 `json:"mtu,omitempty"`
	CreationTimestamp     string                `json:"creationTimestamp,omitempty"`
}

type networkListResponse struct {
	Kind          string            `json:"kind"`
	ID            string            `json:"id"`
	Items         []networkResponse `json:"items"`
	SelfLink      string            `json:"selfLink"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

// secondaryRange is one alias IP range on a subnetwork (secondaryIpRanges[]),
// the shape GKE VPC-native pods/services and Terraform secondary_ip_range use.
type secondaryRange struct {
	RangeName   string `json:"rangeName,omitempty"`
	IPCIDRRange string `json:"ipCidrRange,omitempty"`
}

type subnetworkRequest struct {
	Name                  string           `json:"name"`
	Network               string           `json:"network,omitempty"`
	IPCIDRRange           string           `json:"ipCidrRange,omitempty"`
	Region                string           `json:"region,omitempty"`
	Description           string           `json:"description,omitempty"`
	Purpose               string           `json:"purpose,omitempty"`
	StackType             string           `json:"stackType,omitempty"`
	PrivateIPGoogleAccess *bool            `json:"privateIpGoogleAccess,omitempty"`
	SecondaryIPRanges     []secondaryRange `json:"secondaryIpRanges,omitempty"`
	// Fingerprint is the concurrency token a caller echoes back on patch; GCP
	// rejects a patch whose fingerprint no longer matches the live resource.
	Fingerprint string `json:"fingerprint,omitempty"`
}

type subnetworkResponse struct {
	Kind                  string           `json:"kind"`
	ID                    string           `json:"id"`
	Name                  string           `json:"name"`
	Network               string           `json:"network,omitempty"`
	IPCIDRRange           string           `json:"ipCidrRange,omitempty"`
	Region                string           `json:"region,omitempty"`
	SelfLink              string           `json:"selfLink"`
	Description           string           `json:"description,omitempty"`
	GatewayAddress        string           `json:"gatewayAddress,omitempty"`
	Purpose               string           `json:"purpose,omitempty"`
	StackType             string           `json:"stackType,omitempty"`
	PrivateIPGoogleAccess bool             `json:"privateIpGoogleAccess"`
	SecondaryIPRanges     []secondaryRange `json:"secondaryIpRanges,omitempty"`
	Fingerprint           string           `json:"fingerprint,omitempty"`
	CreationTimestamp     string           `json:"creationTimestamp,omitempty"`
}

// expandIPCIDRRequest is the subnetworks.expandIpCidrRange body
// (SubnetworksExpandIpCidrRangeRequest): the new, broader primary range.
type expandIPCIDRRequest struct {
	IPCIDRRange string `json:"ipCidrRange"`
}

// scopedWarning is the warning GCP stamps on an empty aggregatedList scope
// bucket (code NO_RESULTS_ON_PAGE).
type scopedWarning struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// subnetworksScopedList is one scope bucket of a subnetworkAggregatedList.
type subnetworksScopedList struct {
	Subnetworks []subnetworkResponse `json:"subnetworks,omitempty"`
	Warning     *scopedWarning       `json:"warning,omitempty"`
}

type subnetworkAggregatedListResponse struct {
	Kind          string                           `json:"kind"`
	ID            string                           `json:"id"`
	Items         map[string]subnetworksScopedList `json:"items"`
	SelfLink      string                           `json:"selfLink"`
	NextPageToken string                           `json:"nextPageToken,omitempty"`
}

// addressesScopedList is one scope bucket of an addressAggregatedList. Address
// bodies are stored verbatim, so they stay raw here.
type addressesScopedList struct {
	Addresses []json.RawMessage `json:"addresses,omitempty"`
	Warning   *scopedWarning    `json:"warning,omitempty"`
}

type addressAggregatedListResponse struct {
	Kind          string                         `json:"kind"`
	ID            string                         `json:"id"`
	Items         map[string]addressesScopedList `json:"items"`
	SelfLink      string                         `json:"selfLink"`
	NextPageToken string                         `json:"nextPageToken,omitempty"`
}

type subnetworkListResponse struct {
	Kind          string               `json:"kind"`
	ID            string               `json:"id"`
	Items         []subnetworkResponse `json:"items"`
	SelfLink      string               `json:"selfLink"`
	NextPageToken string               `json:"nextPageToken,omitempty"`
}

type firewallRequest struct {
	Name                  string             `json:"name"`
	Network               string             `json:"network,omitempty"`
	Description           string             `json:"description,omitempty"`
	Priority              int                `json:"priority,omitempty"`
	Direction             string             `json:"direction,omitempty"`
	Allowed               []firewallRule     `json:"allowed,omitempty"`
	Denied                []firewallRule     `json:"denied,omitempty"`
	SourceRanges          []string           `json:"sourceRanges,omitempty"`
	DestinationRanges     []string           `json:"destinationRanges,omitempty"`
	SourceTags            []string           `json:"sourceTags,omitempty"`
	TargetTags            []string           `json:"targetTags,omitempty"`
	SourceServiceAccounts []string           `json:"sourceServiceAccounts,omitempty"`
	TargetServiceAccounts []string           `json:"targetServiceAccounts,omitempty"`
	LogConfig             *firewallLogConfig `json:"logConfig,omitempty"`
	Disabled              *bool              `json:"disabled,omitempty"`
}

type firewallRule struct {
	IPProtocol string   `json:"IPProtocol"`
	Ports      []string `json:"ports,omitempty"`
}

// firewallLogConfig mirrors GCP's firewall logConfig block.
type firewallLogConfig struct {
	Enable   bool   `json:"enable"`
	Metadata string `json:"metadata,omitempty"`
}

type firewallResponse struct {
	Kind                  string             `json:"kind"`
	ID                    string             `json:"id"`
	Name                  string             `json:"name"`
	Network               string             `json:"network,omitempty"`
	Description           string             `json:"description,omitempty"`
	Priority              int                `json:"priority,omitempty"`
	Direction             string             `json:"direction,omitempty"`
	Allowed               []firewallRule     `json:"allowed,omitempty"`
	Denied                []firewallRule     `json:"denied,omitempty"`
	SourceRanges          []string           `json:"sourceRanges,omitempty"`
	DestinationRanges     []string           `json:"destinationRanges,omitempty"`
	SourceTags            []string           `json:"sourceTags,omitempty"`
	TargetTags            []string           `json:"targetTags,omitempty"`
	SourceServiceAccounts []string           `json:"sourceServiceAccounts,omitempty"`
	TargetServiceAccounts []string           `json:"targetServiceAccounts,omitempty"`
	LogConfig             *firewallLogConfig `json:"logConfig,omitempty"`
	Disabled              *bool              `json:"disabled,omitempty"`
	SelfLink              string             `json:"selfLink"`
	CreationTimestamp     string             `json:"creationTimestamp,omitempty"`
}

type firewallListResponse struct {
	Kind          string             `json:"kind"`
	ID            string             `json:"id"`
	Items         []firewallResponse `json:"items"`
	SelfLink      string             `json:"selfLink"`
	NextPageToken string             `json:"nextPageToken,omitempty"`
}
