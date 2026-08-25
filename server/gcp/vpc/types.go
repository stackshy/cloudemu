package vpc

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
	CreationTimestamp     string `json:"creationTimestamp,omitempty"`
}

type networkListResponse struct {
	Kind          string            `json:"kind"`
	ID            string            `json:"id"`
	Items         []networkResponse `json:"items"`
	SelfLink      string            `json:"selfLink"`
	NextPageToken string            `json:"nextPageToken,omitempty"`
}

type subnetworkRequest struct {
	Name                  string `json:"name"`
	Network               string `json:"network,omitempty"`
	IPCIDRRange           string `json:"ipCidrRange,omitempty"`
	Region                string `json:"region,omitempty"`
	Purpose               string `json:"purpose,omitempty"`
	StackType             string `json:"stackType,omitempty"`
	PrivateIPGoogleAccess *bool  `json:"privateIpGoogleAccess,omitempty"`
}

type subnetworkResponse struct {
	Kind                  string `json:"kind"`
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Network               string `json:"network,omitempty"`
	IPCIDRRange           string `json:"ipCidrRange,omitempty"`
	Region                string `json:"region,omitempty"`
	SelfLink              string `json:"selfLink"`
	GatewayAddress        string `json:"gatewayAddress,omitempty"`
	Purpose               string `json:"purpose,omitempty"`
	StackType             string `json:"stackType,omitempty"`
	PrivateIPGoogleAccess bool   `json:"privateIpGoogleAccess"`
	Fingerprint           string `json:"fingerprint,omitempty"`
	CreationTimestamp     string `json:"creationTimestamp,omitempty"`
}

type subnetworkListResponse struct {
	Kind          string               `json:"kind"`
	ID            string               `json:"id"`
	Items         []subnetworkResponse `json:"items"`
	SelfLink      string               `json:"selfLink"`
	NextPageToken string               `json:"nextPageToken,omitempty"`
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
	Kind              string         `json:"kind"`
	ID                string         `json:"id"`
	Name              string         `json:"name"`
	Network           string         `json:"network,omitempty"`
	Description       string         `json:"description,omitempty"`
	Priority          int            `json:"priority,omitempty"`
	Direction         string         `json:"direction,omitempty"`
	Allowed           []firewallRule `json:"allowed,omitempty"`
	Denied            []firewallRule `json:"denied,omitempty"`
	SourceRanges      []string       `json:"sourceRanges,omitempty"`
	TargetTags        []string       `json:"targetTags,omitempty"`
	SelfLink          string         `json:"selfLink"`
	CreationTimestamp string         `json:"creationTimestamp,omitempty"`
}

type firewallListResponse struct {
	Kind          string             `json:"kind"`
	ID            string             `json:"id"`
	Items         []firewallResponse `json:"items"`
	SelfLink      string             `json:"selfLink"`
	NextPageToken string             `json:"nextPageToken,omitempty"`
}
