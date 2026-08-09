package driver

import "time"

// Subscription statuses for contact topic preferences.
const (
	SubscriptionStatusOptIn  = "OPT_IN"
	SubscriptionStatusOptOut = "OPT_OUT"
)

// Job statuses for import/export jobs.
const (
	JobStatusCreated    = "CREATED"
	JobStatusProcessing = "PROCESSING"
	JobStatusCompleted  = "COMPLETED"
	JobStatusFailed     = "FAILED"
	// JobStatusCancelled uses AWS's British spelling of the wire value.
	JobStatusCancelled = "CANCELLED" //nolint:misspell // matches the AWS SES v2 JobStatus enum value.
)

// Dedicated IP warmup statuses.
const (
	WarmupStatusInProgress = "IN_PROGRESS"
	WarmupStatusDone       = "DONE"
)

// Dedicated IP pool scaling modes.
const (
	ScalingModeStandard = "STANDARD"
	ScalingModeManaged  = "MANAGED"
)

// Tenant / endpoint statuses.
const (
	SendingStatusEnabled   = "ENABLED"
	EndpointStatusReady    = "READY"
	EndpointStatusCreating = "CREATING"
)

// Reputation-entity types and statuses.
const (
	ReputationEntityTypeResource = "RESOURCE"
	ReputationStatusHealthy      = "HEALTHY"
)

// DeliverabilityStatusCompleted is the terminal status of a deliverability test.
const DeliverabilityStatusCompleted = "COMPLETED"

// Contact / contact-list types.

// TopicPreference is a contact's opt-in/opt-out choice for a single topic.
type TopicPreference struct {
	TopicName          string
	SubscriptionStatus string
}

// Topic describes a subscribable topic on a contact list.
type Topic struct {
	TopicName                 string
	DisplayName               string
	Description               string
	DefaultSubscriptionStatus string
}

// ContactList groups contacts and the topics they can subscribe to.
type ContactList struct {
	Name        string
	Description string
	Topics      []Topic
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Tags        map[string]string
}

// Contact is a single recipient on a contact list.
type Contact struct {
	EmailAddress            string
	TopicPreferences        []TopicPreference
	UnsubscribeAll          bool
	AttributesData          string
	TopicDefaultPreferences []TopicPreference
	CreatedAt               time.Time
	UpdatedAt               time.Time
}

// ContactListInput describes a contact list to create or update.
type ContactListInput struct {
	Name        string
	Description string
	Topics      []Topic
	Tags        map[string]string
}

// ContactInput describes a contact to create or update.
type ContactInput struct {
	ContactListName  string
	EmailAddress     string
	TopicPreferences []TopicPreference
	UnsubscribeAll   bool
	AttributesData   string
}

// Custom verification email template types.

// CustomVerificationEmailTemplate is a template used to verify email addresses.
type CustomVerificationEmailTemplate struct {
	TemplateName       string
	FromEmailAddress   string
	TemplateSubject    string
	TemplateContent    string
	SuccessRedirectURL string
	FailureRedirectURL string
	CreatedAt          time.Time
}

// CustomVerificationEmailTemplateInput describes a template to create/update.
type CustomVerificationEmailTemplateInput struct {
	TemplateName       string
	FromEmailAddress   string
	TemplateSubject    string
	TemplateContent    string
	SuccessRedirectURL string
	FailureRedirectURL string
}

// Config-set event destination types.

// EventDestination is a destination configuration-set events are published to.
type EventDestination struct {
	Name                string
	Enabled             bool
	MatchingEventTypes  []string
	KinesisFirehoseARN  string
	SNSTopicARN         string
	CloudWatchNamespace string
}

// EventDestinationInput describes an event destination to create/update.
type EventDestinationInput struct {
	ConfigurationSetName string
	EventDestinationName string
	Enabled              bool
	MatchingEventTypes   []string
	KinesisFirehoseARN   string
	SNSTopicARN          string
	CloudWatchNamespace  string
}

// Dedicated IP types.

// DedicatedIPPool is a pool of dedicated sending IP addresses.
type DedicatedIPPool struct {
	Name        string
	ScalingMode string
	CreatedAt   time.Time
	Tags        map[string]string
}

// DedicatedIP is a single dedicated sending IP address.
type DedicatedIP struct {
	IP           string
	WarmupStatus string
	WarmupPct    int32
	PoolName     string
}

// Deliverability dashboard types.

// DeliverabilityTestReport is a synthesized inbox-placement test report.
type DeliverabilityTestReport struct {
	ReportID             string
	ReportName           string
	Subject              string
	FromEmailAddress     string
	CreatedAt            time.Time
	DeliverabilityStatus string
}

// DeliverabilityTestReportInput describes a test report to create.
type DeliverabilityTestReportInput struct {
	ReportName       string
	FromEmailAddress string
	Subject          string
	Tags             map[string]string
}

// Email identity policy types.

// Import / export job types.

// Job is an import or export job.
type Job struct {
	JobID          string
	JobType        string
	Status         string
	CreatedAt      time.Time
	CompletedAt    time.Time
	FailedCount    int64
	ProcessedCount int64
}

// Tenant types.

// Tenant is a sending tenant (multi-tenant isolation unit).
type Tenant struct {
	Name          string
	ID            string
	ARN           string
	CreatedAt     time.Time
	SendingStatus string
	Tags          map[string]string
}

// TenantResource associates a resource with a tenant.
type TenantResource struct {
	TenantName   string
	ResourceType string
	ResourceARN  string
}

// Reputation entity types.

// ReputationEntity holds reputation state for an identity or config set.
type ReputationEntity struct {
	Reference                  string
	EntityType                 string
	ReputationManagementPolicy string
	CustomerManagedStatus      string
	AWSManagedStatus           string
}

// Multi-region endpoint types.

// MultiRegionEndpoint is a multi-region sending endpoint.
type MultiRegionEndpoint struct {
	EndpointName string
	EndpointID   string
	Status       string
	Regions      []string
	CreatedAt    time.Time
}

// MultiRegionEndpointInput describes an endpoint to create.
type MultiRegionEndpointInput struct {
	EndpointName string
	Regions      []string
	Tags         map[string]string
}

// Bulk send types.

// BulkEmailEntry is one recipient/replacement entry in a bulk send.
type BulkEmailEntry struct {
	ToAddresses     []string
	CcAddresses     []string
	BccAddresses    []string
	ReplacementData string
}

// SendBulkEmailInput describes a templated bulk send.
type SendBulkEmailInput struct {
	FromAddress          string
	TemplateName         string
	DefaultTemplateData  string
	ConfigurationSetName string
	Entries              []BulkEmailEntry
}

// BulkEmailResult is the per-entry outcome of a bulk send.
type BulkEmailResult struct {
	Status    string
	Error     string
	MessageID string
}
