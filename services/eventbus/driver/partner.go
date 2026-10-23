package driver

import "time"

// PartnerEventSource is a partner event source as the SaaS partner that created
// it sees it. Partner event sources are an AWS-only concept, consumed through an
// optional interface, not the portable driver.
type PartnerEventSource struct {
	Name string
	ARN  string
}

// PartnerEventSourceAccount is one customer account a partner event source was
// created for, with the source's state in that account.
type PartnerEventSourceAccount struct {
	Account string
	// State is PENDING, ACTIVE or DELETED.
	State     string
	CreatedAt time.Time
	// ExpiresAt is set only while the source is PENDING.
	ExpiresAt time.Time
}

// EventSource is a partner event source as the customer account it was shared
// with sees it.
type EventSource struct {
	Name string
	ARN  string
	// CreatedBy is the name of the partner that created the source.
	CreatedBy string
	// State is PENDING, ACTIVE or DELETED.
	State     string
	CreatedAt time.Time
	// ExpiresAt is set only while the source is PENDING.
	ExpiresAt time.Time
}

// EventSourceFilter narrows and pages ListEventSources and
// ListPartnerEventSources. Limit 0 means the default page size.
type EventSourceFilter struct {
	NamePrefix string
	Limit      int
	NextToken  string
}
