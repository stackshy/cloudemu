package driver

import "time"

// Recorder / recording-group state.
const (
	RecorderStatusPending = "Pending"
	RecorderStatusSuccess = "Success"
)

// Config-rule lifecycle states. The emulator only ever reports ACTIVE: rule
// creates complete instantly and deletes remove the rule outright, so the
// transient DELETING/EVALUATING states never surface.
const (
	RuleStateActive = "ACTIVE"
)

// Compliance types shared across rules and resources.
const (
	ComplianceCompliant        = "COMPLIANT"
	ComplianceNonCompliant     = "NON_COMPLIANT"
	ComplianceNotApplicable    = "NOT_APPLICABLE"
	ComplianceInsufficientData = "INSUFFICIENT_DATA"
)

// Conformance-pack state. The emulator completes pack creation instantly, so a
// pack is always CREATE_COMPLETE; the transient CREATE_IN_PROGRESS /
// DELETE_IN_PROGRESS states are never observable and are intentionally omitted.
const (
	PackStateCreateComplete = "CREATE_COMPLETE"
)

// RecordingGroup selects which resource types a recorder captures.
type RecordingGroup struct {
	AllSupported               bool
	IncludeGlobalResourceTypes bool
	ResourceTypes              []string
	// RecordingStrategy is one of ALL_SUPPORTED_RESOURCE_TYPES /
	// INCLUSION_BY_RESOURCE_TYPES / EXCLUSION_BY_RESOURCE_TYPES.
	RecordingStrategy    string
	ExclusionByResources []string
}

// ConfigurationRecorder mirrors the customer-managed Config recorder.
type ConfigurationRecorder struct {
	Arn            string
	Name           string
	RoleARN        string
	RecordingGroup *RecordingGroup
	Tags           map[string]string

	// Runtime status, mutated by Start/StopConfigurationRecorder.
	Recording            bool
	LastStatus           string
	LastStartTime        time.Time
	LastStopTime         time.Time
	LastStatusChangeTime time.Time
}

// ConfigSnapshotDeliveryProperties controls delivery cadence.
type ConfigSnapshotDeliveryProperties struct {
	DeliveryFrequency string
}

// DeliveryChannel routes configuration snapshots/history to S3 (+ optional SNS).
type DeliveryChannel struct {
	Arn                   string
	Name                  string
	S3BucketName          string
	S3KeyPrefix           string
	S3KmsKeyArn           string
	SnsTopicARN           string
	SnapshotDeliveryProps *ConfigSnapshotDeliveryProperties
	Tags                  map[string]string

	// Runtime status, synthesized.
	LastStatus           string
	LastStatusChangeTime time.Time
}

// RuleSource identifies how a rule is evaluated (AWS managed / custom Lambda /
// custom policy).
type RuleSource struct {
	Owner            string
	SourceIdentifier string
	// SourceDetails carries event/frequency triggers as opaque JSON handled at
	// the wire layer; the mock preserves it verbatim.
	CustomPolicyRuntime string
	PolicyText          string
}

// ConfigRule is a Config rule (managed, custom Lambda, or custom policy).
type ConfigRule struct {
	ConfigRuleArn             string
	ConfigRuleID              string
	ConfigRuleName            string
	Description               string
	Scope                     *RuleScope
	Source                    *RuleSource
	InputParameters           string
	MaximumExecutionFrequency string
	ConfigRuleState           string
	CreatedBy                 string
	Tags                      map[string]string

	// Synthesized evaluation/compliance state.
	Compliance         string
	LastSuccessfulEval time.Time
}

// RuleScope narrows which resources a rule applies to.
type RuleScope struct {
	ComplianceResourceTypes []string
	ComplianceResourceID    string
	TagKey                  string
	TagValue                string
}

// Evaluation is a single compliance evaluation reported via PutEvaluations.
type Evaluation struct {
	ComplianceResourceType string
	ComplianceResourceID   string
	ComplianceType         string
	Annotation             string
	OrderingTimestamp      time.Time
}

// ConformancePack groups rules and remediation into a deployable unit.
type ConformancePack struct {
	ConformancePackArn      string
	ConformancePackID       string
	ConformancePackName     string
	DeliveryS3Bucket        string
	DeliveryS3KeyPrefix     string
	TemplateBody            string
	TemplateS3URI           string
	InputParameters         map[string]string
	CreatedBy               string
	LastUpdateRequestedTime time.Time
	State                   string
	Tags                    map[string]string
}

// OrganizationConfigRule is an org-wide managed/custom rule.
type OrganizationConfigRule struct {
	Arn                   string
	Name                  string
	ManagedRuleIdentifier string
	Description           string
	InputParameters       string
	ExcludedAccounts      []string
	MaximumExecutionFreq  string
	LastUpdateTime        time.Time
	Tags                  map[string]string
}

// OrganizationConformancePack is an org-wide conformance pack.
type OrganizationConformancePack struct {
	Arn                 string
	Name                string
	DeliveryS3Bucket    string
	DeliveryS3KeyPrefix string
	TemplateBody        string
	TemplateS3URI       string
	ExcludedAccounts    []string
	InputParameters     map[string]string
	LastUpdateTime      time.Time
	Tags                map[string]string
}

// AccountAggregationSource selects source accounts + regions for an aggregator.
type AccountAggregationSource struct {
	AccountIDs    []string
	AllAwsRegions bool
	AwsRegions    []string
}

// OrganizationAggregationSource aggregates across the whole organization.
type OrganizationAggregationSource struct {
	RoleARN       string
	AllAwsRegions bool
	AwsRegions    []string
}

// ConfigurationAggregator aggregates Config data across accounts/regions.
type ConfigurationAggregator struct {
	Arn                string
	Name               string
	AccountSources     []AccountAggregationSource
	OrganizationSource *OrganizationAggregationSource
	CreatedBy          string
	CreationTime       time.Time
	LastUpdatedTime    time.Time
	Tags               map[string]string
}

// AggregationAuthorization authorizes another account to aggregate this one.
type AggregationAuthorization struct {
	Arn                 string
	AuthorizedAccountID string
	AuthorizedAwsRegion string
	CreationTime        time.Time
	Tags                map[string]string
}

// PendingAggregationRequest is an unaccepted authorization request.
type PendingAggregationRequest struct {
	RequesterAccountID string
	RequesterAwsRegion string
}

// RemediationConfiguration attaches remediation to a config rule.
type RemediationConfiguration struct {
	Arn                      string
	ConfigRuleName           string
	TargetType               string
	TargetID                 string
	TargetVersion            string
	ResourceType             string
	Automatic                bool
	MaximumAutomaticAttempts int32
	RetryAttemptSeconds      int64
	Parameters               map[string]string
	CreatedByService         string
}

// RemediationException suppresses remediation for a specific resource.
type RemediationException struct {
	ConfigRuleName string
	ResourceType   string
	ResourceID     string
	Message        string
	ExpirationTime time.Time
}

// StoredQuery is a saved SelectResourceConfig query.
type StoredQuery struct {
	QueryArn    string
	QueryID     string
	QueryName   string
	Description string
	Expression  string
	Tags        map[string]string
}

// RetentionConfiguration sets how long Config data is retained.
type RetentionConfiguration struct {
	Name                  string
	RetentionPeriodInDays int32
}

// ConfigurationItem is a synthesized snapshot of a discovered resource.
type ConfigurationItem struct {
	ResourceType        string
	ResourceID          string
	ResourceName        string
	Arn                 string
	AwsRegion           string
	AccountID           string
	ConfigurationState  string
	CaptureTime         time.Time
	Configuration       string
	Tags                map[string]string
	SupplementaryConfig map[string]string
}

// ResourceKey identifies a resource for BatchGetResourceConfig.
type ResourceKey struct {
	ResourceType string
	ResourceID   string
}

// ResourceCount is a per-type discovered-resource count.
type ResourceCount struct {
	ResourceType string
	Count        int64
}

// Tag is a key/value pair. Config uses lists on the wire; the driver uses maps
// internally and converts at the boundary.
type Tag struct {
	Key   string
	Value string
}

// Page carries an opaque continuation token plus a page limit for list ops.
type Page struct {
	NextToken string
	Limit     int32
}
