// Package driver defines the interface and types for the AWS Global Accelerator
// control-plane API: accelerators, their listeners and the endpoint groups under
// each listener, plus per-accelerator attributes (flow logs) and resource tags.
//
// Global Accelerator is a GLOBAL service — its ARNs carry an empty region field
// (arn:aws:globalaccelerator::<acct>:accelerator/<id>) and its control plane is
// reached in us-west-2. The emulator is control-plane only: it does NOT route
// any real traffic and runs no health checks. An accelerator is created
// synchronously with stable computed fields — AcceleratorArn, DnsName,
// DualStackDnsName, two deterministic static IPv4 addresses (IpSets), Status and
// CreatedTime — minted once at create and stored, so repeated DescribeAccelerator
// and ListAccelerators reads never drift. Status settles to DEPLOYED at once (the
// real service takes minutes) so an IaC waiter completes without hanging.
//
// Referential integrity mirrors the real service: an accelerator cannot be
// deleted while enabled (AcceleratorNotDisabledException) or while it still owns
// listeners (AssociatedListenerFoundException); a listener cannot be deleted
// while it still owns endpoint groups (AssociatedEndpointGroupFoundException);
// and operating on an unknown resource yields the matching NotFound exception.
package driver

import (
	"context"
	"time"
)

// Page is the pagination cursor shared by the list operations.
type Page struct {
	NextToken  string
	MaxResults int32
}

// PortRange is a contiguous listener port range. Both bounds round-trip verbatim.
type PortRange struct {
	FromPort int32 `json:"fromPort"`
	ToPort   int32 `json:"toPort"`
}

// IPSet is a set of static IP addresses of one family assigned to an
// accelerator. IpFamily is the legacy member; IpAddressFamily is its modern
// spelling — both are emitted so old and new SDKs read the value.
type IPSet struct {
	IPFamily        string   `json:"ipFamily,omitempty"`
	IPAddresses     []string `json:"ipAddresses,omitempty"`
	IPAddressFamily string   `json:"ipAddressFamily,omitempty"`
}

// Accelerator is a Global Accelerator. AcceleratorArn, IpSets, DnsName,
// DualStackDnsName, Status and CreatedTime are minted once at create and stable
// across reads; LastModifiedTime advances on update.
type Accelerator struct {
	AcceleratorArn   string
	Name             string
	IPAddressType    string
	Enabled          bool
	IPSets           []IPSet
	DNSName          string
	DualStackDNSName string
	Status           string
	CreatedTime      time.Time
	LastModifiedTime time.Time
	Tags             map[string]string
}

// AcceleratorAttributes are the per-accelerator flow-log settings, managed
// separately from the accelerator by the *AcceleratorAttributes operations.
type AcceleratorAttributes struct {
	FlowLogsEnabled  bool
	FlowLogsS3Bucket string
	FlowLogsS3Prefix string
}

// Listener is a listener under an accelerator. ListenerArn is minted once and
// stable; PortRanges, Protocol and ClientAffinity round-trip verbatim.
type Listener struct {
	ListenerArn    string
	AcceleratorArn string
	PortRanges     []PortRange
	Protocol       string
	ClientAffinity string
	Tags           map[string]string
}

// EndpointConfiguration is an endpoint supplied on create/update of an endpoint
// group. Weight and ClientIPPreservationEnabled are optional pointers so an
// explicit zero/false round-trips distinctly from an absent member.
type EndpointConfiguration struct {
	EndpointID                  string
	Weight                      *int32
	ClientIPPreservationEnabled *bool
	AttachmentArn               string
}

// EndpointDescription is an endpoint as reported on read: the configured fields
// plus the (control-plane-only) health state.
type EndpointDescription struct {
	EndpointID                  string
	Weight                      *int32
	ClientIPPreservationEnabled *bool
	AttachmentArn               string
	HealthState                 string
	HealthReason                string
}

// PortOverride maps a listener port to a different port on the endpoints.
type PortOverride struct {
	ListenerPort int32 `json:"listenerPort"`
	EndpointPort int32 `json:"endpointPort"`
}

// EndpointGroup is an endpoint group under a listener. EndpointGroupArn is minted
// once and stable; the health-check settings, traffic dial and endpoint list
// round-trip verbatim.
type EndpointGroup struct {
	EndpointGroupArn           string
	ListenerArn                string
	EndpointGroupRegion        string
	EndpointDescriptions       []EndpointDescription
	TrafficDialPercentage      *float64
	HealthCheckPort            *int32
	HealthCheckProtocol        string
	HealthCheckPath            string
	HealthCheckIntervalSeconds *int32
	ThresholdCount             *int32
	PortOverrides              []PortOverride
	Tags                       map[string]string
}

// CreateAcceleratorInput is the input to CreateAccelerator.
type CreateAcceleratorInput struct {
	Name             string
	IPAddressType    string
	IPAddresses      []string
	Enabled          *bool
	IdempotencyToken string
	Tags             map[string]string
}

// UpdateAcceleratorInput is the input to UpdateAccelerator. A nil pointer field
// means the member was absent and is left unchanged; a non-nil pointer replaces
// the value.
type UpdateAcceleratorInput struct {
	AcceleratorArn string
	Name           *string
	IPAddressType  *string
	IPAddresses    []string
	Enabled        *bool
}

// UpdateAcceleratorAttributesInput is the input to UpdateAcceleratorAttributes. A
// nil pointer means the member was absent and is left unchanged.
type UpdateAcceleratorAttributesInput struct {
	AcceleratorArn   string
	FlowLogsEnabled  *bool
	FlowLogsS3Bucket *string
	FlowLogsS3Prefix *string
}

// CreateListenerInput is the input to CreateListener.
type CreateListenerInput struct {
	AcceleratorArn   string
	PortRanges       []PortRange
	Protocol         string
	ClientAffinity   string
	IdempotencyToken string
}

// UpdateListenerInput is the input to UpdateListener. A nil pointer field means
// the member was absent and is left unchanged.
type UpdateListenerInput struct {
	ListenerArn    string
	PortRanges     []PortRange
	Protocol       *string
	ClientAffinity *string
}

// CreateEndpointGroupInput is the input to CreateEndpointGroup.
type CreateEndpointGroupInput struct {
	ListenerArn                string
	EndpointGroupRegion        string
	EndpointConfigurations     []EndpointConfiguration
	TrafficDialPercentage      *float64
	HealthCheckPort            *int32
	HealthCheckProtocol        string
	HealthCheckPath            string
	HealthCheckIntervalSeconds *int32
	ThresholdCount             *int32
	PortOverrides              []PortOverride
	IdempotencyToken           string
}

// UpdateEndpointGroupInput is the input to UpdateEndpointGroup. A nil pointer
// field means the member was absent and is left unchanged; EndpointConfigurations
// and PortOverrides, when non-nil, replace the stored lists.
type UpdateEndpointGroupInput struct {
	EndpointGroupArn           string
	EndpointConfigurations     *[]EndpointConfiguration
	TrafficDialPercentage      *float64
	HealthCheckPort            *int32
	HealthCheckProtocol        *string
	HealthCheckPath            *string
	HealthCheckIntervalSeconds *int32
	ThresholdCount             *int32
	PortOverrides              *[]PortOverride
}

// GlobalAccelerator is the Global Accelerator control-plane surface: accelerators
// and their attributes, listeners, endpoint groups and resource tags.
type GlobalAccelerator interface {
	CreateAccelerator(ctx context.Context, in *CreateAcceleratorInput) (*Accelerator, error)
	DescribeAccelerator(ctx context.Context, arn string) (*Accelerator, error)
	UpdateAccelerator(ctx context.Context, in *UpdateAcceleratorInput) (*Accelerator, error)
	DeleteAccelerator(ctx context.Context, arn string) error
	ListAccelerators(ctx context.Context, page Page) (accelerators []*Accelerator, nextToken string, err error)

	DescribeAcceleratorAttributes(ctx context.Context, arn string) (*AcceleratorAttributes, error)
	UpdateAcceleratorAttributes(ctx context.Context, in *UpdateAcceleratorAttributesInput) (*AcceleratorAttributes, error)

	CreateListener(ctx context.Context, in *CreateListenerInput) (*Listener, error)
	DescribeListener(ctx context.Context, arn string) (*Listener, error)
	UpdateListener(ctx context.Context, in *UpdateListenerInput) (*Listener, error)
	DeleteListener(ctx context.Context, arn string) error
	ListListeners(ctx context.Context, acceleratorArn string, page Page) (listeners []*Listener, nextToken string, err error)

	CreateEndpointGroup(ctx context.Context, in *CreateEndpointGroupInput) (*EndpointGroup, error)
	DescribeEndpointGroup(ctx context.Context, arn string) (*EndpointGroup, error)
	UpdateEndpointGroup(ctx context.Context, in *UpdateEndpointGroupInput) (*EndpointGroup, error)
	DeleteEndpointGroup(ctx context.Context, arn string) error
	ListEndpointGroups(ctx context.Context, listenerArn string, page Page) (groups []*EndpointGroup, nextToken string, err error)

	TagResource(ctx context.Context, resourceArn string, tags map[string]string) error
	UntagResource(ctx context.Context, resourceArn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, resourceArn string) (map[string]string, error)
}
