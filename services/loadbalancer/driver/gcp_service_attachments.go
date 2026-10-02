package driver

import "context"

// GCPServiceAttachmentCollection is the Collection a Private Service Connect
// service attachment is stored under.
const GCPServiceAttachmentCollection = "serviceAttachments"

// Private Service Connect connection statuses, as reported on a consumer
// forwarding rule's pscConnectionStatus and on the producer attachment's
// connectedEndpoints[].status.
const (
	PSCStatusAccepted = "ACCEPTED"
	PSCStatusPending  = "PENDING"
	PSCStatusRejected = "REJECTED"
	// PSCStatusClosed is reported for a consumer rule whose attachment has
	// been deleted.
	PSCStatusClosed = "CLOSED"
)

// Connection preferences a service attachment accepts.
const (
	PSCAcceptAutomatic = "ACCEPT_AUTOMATIC"
	PSCAcceptManual    = "ACCEPT_MANUAL"
)

// GCPPSCEndpoint is a consumer PSC forwarding rule connecting to a service
// attachment.
type GCPPSCEndpoint struct {
	// Endpoint is the consumer forwarding rule's URL; its projects/{p}
	// segment is the consumer project the accept/reject lists match.
	Endpoint string
	// PscConnectionID is the consumer rule's pscConnectionId.
	PscConnectionID string
	// ConsumerNetwork is the consumer rule's network.
	ConsumerNetwork string
}

// GCPServiceAttachmentStore is an OPTIONAL, type-asserted capability
// implemented only by the GCP load-balancer provider. It persists regional
// compute.serviceAttachments (the producer side of Private Service Connect) as
// GCPResource values (Collection GCPServiceAttachmentCollection, Scope = the
// region) alongside the other opaque GCP resources, so they snapshot and
// restore with them, and it owns the connection decisions for the consumer
// rules that target them. Non-GCP providers do not implement it.
//
// A connection's status follows the attachment's connectionPreference:
// ACCEPT_AUTOMATIC accepts every consumer; ACCEPT_MANUAL rejects a consumer
// whose project or network is in consumerRejectLists, accepts one matched by a
// consumerAcceptLists entry while that entry's connectionLimit has room, and
// leaves every other one PENDING. Statuses are re-evaluated, in connection
// order, whenever the attachment changes.
type GCPServiceAttachmentStore interface {
	// InsertGCPServiceAttachment validates and stores a new attachment,
	// returning AlreadyExists when (region, name) is taken.
	InsertGCPServiceAttachment(ctx context.Context, res GCPResource) error
	// GetGCPServiceAttachment returns the attachment, or NotFound.
	GetGCPServiceAttachment(ctx context.Context, region, name string) (*GCPResource, error)
	// ListGCPServiceAttachments returns every attachment in a region.
	ListGCPServiceAttachments(ctx context.Context, region string) ([]GCPResource, error)
	// UpdateGCPServiceAttachment applies mutate under the store lock, keeps the
	// attachment's connectedEndpoints, validates the result and re-evaluates
	// every connection. A mutate or validation error leaves the record
	// unchanged. Returns NotFound when absent.
	UpdateGCPServiceAttachment(ctx context.Context, region, name string, mutate func(*GCPResource) error) error
	// DeleteGCPServiceAttachment removes the attachment, or returns NotFound.
	// Its consumer rules then report PSCStatusClosed.
	DeleteGCPServiceAttachment(ctx context.Context, region, name string) error
	// ConnectGCPServiceAttachment records a consumer endpoint on the attachment
	// and returns the status it was given. Returns NotFound when the
	// attachment does not exist.
	ConnectGCPServiceAttachment(ctx context.Context, region, name string, ep GCPPSCEndpoint) (string, error)
	// DisconnectGCPServiceAttachment removes a consumer endpoint (by
	// pscConnectionId) and re-evaluates the rest; an absent attachment or
	// endpoint is not an error.
	DisconnectGCPServiceAttachment(ctx context.Context, region, name, pscConnectionID string) error
	// GCPPSCConnectionStatus returns the current status of a consumer
	// endpoint, or PSCStatusClosed when the attachment or endpoint is gone.
	GCPPSCConnectionStatus(ctx context.Context, region, name, pscConnectionID string) string
}
