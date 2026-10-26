package driver

import "time"

// EndpointRouting is an EventBridge global endpoint's failover configuration:
// the Route 53 health check that decides when to fail over, and the secondary
// Region events go to when it does.
type EndpointRouting struct {
	HealthCheck    string
	SecondaryRoute string
}

// Endpoint describes an EventBridge global endpoint: one event bus name present
// in two Regions, with failover between them. Global endpoints are an AWS-only
// concept, consumed through an optional interface, not the portable driver.
type Endpoint struct {
	Name        string
	Description string
	ARN         string
	Routing     EndpointRouting
	// ReplicationState is ENABLED or DISABLED.
	ReplicationState string
	// EventBusARNs holds the two event bus ARNs, one per Region.
	EventBusARNs []string
	RoleARN      string
	// EndpointID is the URL subdomain, such as "abcde.veo".
	EndpointID  string
	EndpointURL string
	// HomeRegion is the primary Region: the one the endpoint was created in.
	HomeRegion     string
	State          string
	StateReason    string
	CreatedAt      time.Time
	LastModifiedAt time.Time
}

// EndpointConfig carries the parameters of an EventBridge CreateEndpoint call.
// An empty ReplicationState means the request left it out (ENABLED).
type EndpointConfig struct {
	Name             string
	Description      string
	Routing          EndpointRouting
	ReplicationState string
	EventBusARNs     []string
	RoleARN          string
}

// EndpointUpdate carries the parameters of an EventBridge UpdateEndpoint call. A
// nil field was omitted from the request and leaves the endpoint unchanged.
type EndpointUpdate struct {
	Name             string
	Description      *string
	Routing          *EndpointRouting
	ReplicationState *string
	EventBusARNs     []string
	RoleARN          *string
}

// EndpointFilter narrows and pages ListEndpoints. MaxResults 0 means the default
// page size.
type EndpointFilter struct {
	NamePrefix string
	HomeRegion string
	MaxResults int
	NextToken  string
}
