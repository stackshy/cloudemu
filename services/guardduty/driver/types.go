package driver

import (
	"encoding/json"
	"time"
)

// Detector statuses reported by GetDetector.
const (
	DetectorStatusEnabled  = "ENABLED"
	DetectorStatusDisabled = "DISABLED"
)

// Set statuses (IPSet, ThreatIntelSet, ThreatEntitySet, TrustedEntitySet). The
// emulator activates and deletes deterministically, so a set is ACTIVE once
// created with Activate=true and INACTIVE otherwise.
const (
	SetStatusInactive = "INACTIVE"
	SetStatusActive   = "ACTIVE"
)

// Filter actions.
const (
	FilterActionNoop    = "NOOP"
	FilterActionArchive = "ARCHIVE"
)

// Finding-publishing frequencies.
const (
	FindingFrequencyFifteenMinutes = "FIFTEEN_MINUTES"
	FindingFrequencyOneHour        = "ONE_HOUR"
	FindingFrequencySixHours       = "SIX_HOURS"
)

// Page is a generic pagination request shared by every list operation.
type Page struct {
	NextToken  string
	MaxResults int32
}

// Detector is a GuardDuty detector: the per-region container all other
// GuardDuty resources hang off. Feature and data-source configuration the
// emulator does not interpret is carried verbatim as raw JSON so a
// round-tripped detector reflects everything the caller sent.
type Detector struct {
	ID                         string
	ServiceRole                string
	Status                     string
	FindingPublishingFrequency string
	// Features and DataSources carry the modeled-but-passed-through feature and
	// data-source blocks so GetDetector echoes what Create/Update received.
	Features    []json.RawMessage
	DataSources json.RawMessage
	Tags        map[string]string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CreateDetectorInput describes a detector to create.
type CreateDetectorInput struct {
	Enable                     bool
	FindingPublishingFrequency string
	Features                   []json.RawMessage
	DataSources                json.RawMessage
	Tags                       map[string]string
	ClientToken                string
}

// UpdateDetectorInput describes a detector config change. Only non-nil fields
// are applied so a caller can patch a single attribute.
type UpdateDetectorInput struct {
	DetectorID                 string
	Enable                     *bool
	FindingPublishingFrequency *string
	Features                   []json.RawMessage
	DataSources                json.RawMessage
}

// IPSet is a trusted-IP list attached to a detector.
type IPSet struct {
	ID                  string
	Name                string
	Format              string
	Location            string
	Status              string
	ExpectedBucketOwner string
	Tags                map[string]string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CreateIPSetInput describes an IPSet to create.
type CreateIPSetInput struct {
	DetectorID          string
	Name                string
	Format              string
	Location            string
	Activate            bool
	ExpectedBucketOwner string
	Tags                map[string]string
	ClientToken         string
}

// UpdateIPSetInput patches an IPSet's mutable fields. Nil pointers are left
// unchanged.
type UpdateIPSetInput struct {
	DetectorID          string
	IPSetID             string
	Name                *string
	Location            *string
	Activate            *bool
	ExpectedBucketOwner *string
}

// ThreatIntelSet is a threat-intelligence list attached to a detector.
type ThreatIntelSet struct {
	ID                  string
	Name                string
	Format              string
	Location            string
	Status              string
	ExpectedBucketOwner string
	Tags                map[string]string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CreateThreatIntelSetInput describes a ThreatIntelSet to create.
type CreateThreatIntelSetInput struct {
	DetectorID          string
	Name                string
	Format              string
	Location            string
	Activate            bool
	ExpectedBucketOwner string
	Tags                map[string]string
	ClientToken         string
}

// UpdateThreatIntelSetInput patches a ThreatIntelSet's mutable fields.
type UpdateThreatIntelSetInput struct {
	DetectorID          string
	ThreatIntelSetID    string
	Name                *string
	Location            *string
	Activate            *bool
	ExpectedBucketOwner *string
}

// ThreatEntitySet is a threat-entity list attached to a detector. Unlike the
// older IP/ThreatIntel sets it carries created/updated timestamps and an
// error-details field on the wire.
type ThreatEntitySet struct {
	ID                  string
	Name                string
	Format              string
	Location            string
	Status              string
	ExpectedBucketOwner string
	ErrorDetails        string
	Tags                map[string]string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CreateThreatEntitySetInput describes a ThreatEntitySet to create.
type CreateThreatEntitySetInput struct {
	DetectorID          string
	Name                string
	Format              string
	Location            string
	Activate            bool
	ExpectedBucketOwner string
	Tags                map[string]string
	ClientToken         string
}

// UpdateThreatEntitySetInput patches a ThreatEntitySet's mutable fields.
type UpdateThreatEntitySetInput struct {
	DetectorID          string
	ThreatEntitySetID   string
	Name                *string
	Location            *string
	Activate            *bool
	ExpectedBucketOwner *string
}

// TrustedEntitySet is a trusted-entity list attached to a detector.
type TrustedEntitySet struct {
	ID                  string
	Name                string
	Format              string
	Location            string
	Status              string
	ExpectedBucketOwner string
	ErrorDetails        string
	Tags                map[string]string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CreateTrustedEntitySetInput describes a TrustedEntitySet to create.
type CreateTrustedEntitySetInput struct {
	DetectorID          string
	Name                string
	Format              string
	Location            string
	Activate            bool
	ExpectedBucketOwner string
	Tags                map[string]string
	ClientToken         string
}

// UpdateTrustedEntitySetInput patches a TrustedEntitySet's mutable fields.
type UpdateTrustedEntitySetInput struct {
	DetectorID          string
	TrustedEntitySetID  string
	Name                *string
	Location            *string
	Activate            *bool
	ExpectedBucketOwner *string
}

// Filter is a saved-finding filter attached to a detector. FindingCriteria is
// carried verbatim as raw JSON because the emulator does not evaluate it.
type Filter struct {
	Name            string
	Action          string
	Description     string
	Rank            int32
	FindingCriteria json.RawMessage
	Tags            map[string]string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// CreateFilterInput describes a filter to create.
type CreateFilterInput struct {
	DetectorID      string
	Name            string
	Action          string
	Description     string
	Rank            int32
	FindingCriteria json.RawMessage
	Tags            map[string]string
	ClientToken     string
}

// UpdateFilterInput patches a filter's mutable fields. Nil pointers are left
// unchanged; FindingCriteria is replaced only when non-nil.
type UpdateFilterInput struct {
	DetectorID      string
	FilterName      string
	Action          *string
	Description     *string
	Rank            *int32
	FindingCriteria json.RawMessage
}

// Member is a GuardDuty member account and its relationship to the administrator.
type Member struct {
	AccountID          string
	DetectorID         string
	MasterID           string
	Email              string
	RelationshipStatus string
	InvitedAt          time.Time
	UpdatedAt          time.Time
}

// Finding is a GuardDuty finding. Modeled as raw JSON.
type Finding struct {
	ID  string
	Raw json.RawMessage
}

// PublishingDestination is a findings-export destination.
type PublishingDestination struct {
	DestinationID   string
	DestinationType string
	Status          string
	DestinationARN  string
	KmsKeyARN       string
}

// MalwareProtectionPlan is a malware-protection plan.
type MalwareProtectionPlan struct {
	ID     string
	Role   string
	Status string
}

// MalwareScan is a malware scan record. Modeled minimally.
type MalwareScan struct {
	ScanID     string
	DetectorID string
	Status     string
}
