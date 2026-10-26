package driver

import (
	"errors"
	"time"
)

// ErrInvalidEventPattern marks an event pattern that fails validation. AWS
// EventBridge answers it as InvalidEventPatternException rather than the generic
// ValidationException.
var ErrInvalidEventPattern = errors.New("event pattern is not valid")

// ErrIllegalStatus marks an operation on a resource whose current state does
// not allow it (e.g. stopping a finished replay). AWS EventBridge answers it as
// IllegalStatusException.
var ErrIllegalStatus = errors.New("resource is not in a valid state for this operation")

// Archive describes an EventBridge archive: a store of the events put on one
// event bus that match its pattern, kept for later replay. Archives are an
// AWS-only concept, consumed through an optional interface, not the portable
// driver.
type Archive struct {
	Name             string
	ARN              string
	EventSourceARN   string
	Description      string
	EventPattern     string
	State            string
	StateReason      string
	KMSKeyIdentifier string
	// RetentionDays is how long archived events are kept; 0 keeps them forever.
	RetentionDays int
	SizeBytes     int64
	EventCount    int64
	CreatedAt     time.Time
}

// ArchiveConfig carries the parameters of an EventBridge CreateArchive call.
type ArchiveConfig struct {
	Name             string
	EventSourceARN   string
	Description      string
	EventPattern     string
	KMSKeyIdentifier string
	RetentionDays    int
}

// ArchiveUpdate carries the parameters of an EventBridge UpdateArchive call. A
// nil field was omitted from the request and leaves the archive unchanged.
type ArchiveUpdate struct {
	Name             string
	Description      *string
	EventPattern     *string
	KMSKeyIdentifier *string
	RetentionDays    *int
}

// ArchiveFilter narrows ListArchives; EventBridge accepts at most one of its
// fields per call.
type ArchiveFilter struct {
	NamePrefix     string
	EventSourceARN string
	State          string
}

// ReplayDestination is where a replay sends its events: the archive's own event
// bus, optionally limited to the rules in FilterARNs.
type ReplayDestination struct {
	ARN        string
	FilterARNs []string
}

// ReplayConfig carries the parameters of an EventBridge StartReplay call.
type ReplayConfig struct {
	Name           string
	Description    string
	EventSourceARN string // the archive ARN
	EventStartTime time.Time
	EventEndTime   time.Time
	Destination    ReplayDestination
}

// Replay describes an EventBridge replay and its progress.
type Replay struct {
	Name                  string
	ARN                   string
	Description           string
	State                 string
	StateReason           string
	EventSourceARN        string
	Destination           ReplayDestination
	EventStartTime        time.Time
	EventEndTime          time.Time
	EventLastReplayedTime time.Time
	ReplayStartTime       time.Time
	ReplayEndTime         time.Time
}

// ReplayFilter narrows ListReplays; EventBridge accepts at most one of its
// fields per call.
type ReplayFilter struct {
	NamePrefix     string
	EventSourceARN string
	State          string
}
