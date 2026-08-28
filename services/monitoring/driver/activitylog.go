package driver

import "time"

// ActivityLogEvent is one Azure Activity Log entry — a management-plane
// operation recorded automatically as ARM traffic flows through the wire
// server. It mirrors the fields the Activity Log API surfaces per event.
type ActivityLogEvent struct {
	EventID          string
	OperationName    string // e.g. Microsoft.Compute/virtualMachines/write
	ResourceID       string // the target resource path
	ResourceProvider string // e.g. Microsoft.Compute
	ResourceGroup    string
	Caller           string
	Status           string // Succeeded / Failed
	Level            string // Informational / Error
	SubscriptionID   string
	CorrelationID    string
	EventTimestamp   time.Time
}

// ActivityLogQuery filters ListActivityLogEvents. A zero value returns every
// recorded event, newest first.
type ActivityLogQuery struct {
	StartTime     time.Time
	EndTime       time.Time
	ResourceGroup string
	ResourceID    string
	MaxRecords    int
}

// ActivityLogRecorder is an OPTIONAL capability, discovered by type assertion
// on a Monitoring backend. The Azure wire server calls RecordActivityLogEvent
// as ARM operations flow through it, so the Activity Log API reflects real API
// activity rather than staying empty. Recording is Azure-specific, so it is
// kept off the mandatory Monitoring interface.
type ActivityLogRecorder interface {
	RecordActivityLogEvent(e *ActivityLogEvent)
	ListActivityLogEvents(q ActivityLogQuery) []ActivityLogEvent
}
