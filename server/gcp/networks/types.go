package networks

// GCP Compute Engine networking REST shapes.

type networkRequest struct {
	Name                  string `json:"name"`
	Description           string `json:"description,omitempty"`
	IPv4Range             string `json:"IPv4Range,omitempty"`
	AutoCreateSubnetworks *bool  `json:"autoCreateSubnetworks,omitempty"`
	RoutingConfig         any    `json:"routingConfig,omitempty"`
}

type networkResponse struct {
	Kind                  string `json:"kind"`
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Description           string `json:"description,omitempty"`
	SelfLink              string `json:"selfLink"`
	IPv4Range             string `json:"IPv4Range,omitempty"`
	AutoCreateSubnetworks bool   `json:"autoCreateSubnetworks"`
	RoutingConfig         any    `json:"routingConfig,omitempty"`
}

type networkListResponse struct {
	Kind     string            `json:"kind"`
	ID       string            `json:"id"`
	Items    []networkResponse `json:"items"`
	SelfLink string            `json:"selfLink"`
}

type subnetworkRequest struct {
	Name        string `json:"name"`
	Network     string `json:"network,omitempty"`
	IPCIDRRange string `json:"ipCidrRange,omitempty"`
	Region      string `json:"region,omitempty"`
}

type subnetworkResponse struct {
	Kind        string `json:"kind"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Network     string `json:"network,omitempty"`
	IPCIDRRange string `json:"ipCidrRange,omitempty"`
	Region      string `json:"region,omitempty"`
	SelfLink    string `json:"selfLink"`
}

type subnetworkListResponse struct {
	Kind     string               `json:"kind"`
	ID       string               `json:"id"`
	Items    []subnetworkResponse `json:"items"`
	SelfLink string               `json:"selfLink"`
}

type firewallRequest struct {
	Name         string         `json:"name"`
	Network      string         `json:"network,omitempty"`
	Description  string         `json:"description,omitempty"`
	Priority     int            `json:"priority,omitempty"`
	Direction    string         `json:"direction,omitempty"`
	Allowed      []firewallRule `json:"allowed,omitempty"`
	Denied       []firewallRule `json:"denied,omitempty"`
	SourceRanges []string       `json:"sourceRanges,omitempty"`
	TargetTags   []string       `json:"targetTags,omitempty"`
}

type firewallRule struct {
	IPProtocol string   `json:"IPProtocol"`
	Ports      []string `json:"ports,omitempty"`
}

type firewallResponse struct {
	Kind         string         `json:"kind"`
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Network      string         `json:"network,omitempty"`
	Description  string         `json:"description,omitempty"`
	Priority     int            `json:"priority,omitempty"`
	Direction    string         `json:"direction,omitempty"`
	Allowed      []firewallRule `json:"allowed,omitempty"`
	Denied       []firewallRule `json:"denied,omitempty"`
	SourceRanges []string       `json:"sourceRanges,omitempty"`
	TargetTags   []string       `json:"targetTags,omitempty"`
	SelfLink     string         `json:"selfLink"`
}

type firewallListResponse struct {
	Kind     string             `json:"kind"`
	ID       string             `json:"id"`
	Items    []firewallResponse `json:"items"`
	SelfLink string             `json:"selfLink"`
}
