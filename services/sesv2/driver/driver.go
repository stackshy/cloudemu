// Package driver defines the interface and types for AWS SES v2 (Simple Email
// Service v2) implementations. It models email identities and their DKIM /
// verification state, configuration sets, email templates, the account-level
// suppression list, sent messages, and per-resource tags.
package driver

import (
	"context"
	"time"
)

// Identity types.
const (
	IdentityTypeEmailAddress = "EMAIL_ADDRESS"
	IdentityTypeDomain       = "DOMAIN"
)

// Verification / DKIM statuses.
const (
	StatusPending          = "PENDING"
	StatusSuccess          = "SUCCESS"
	StatusFailed           = "FAILED"
	StatusTemporaryFailure = "TEMPORARY_FAILURE"
	StatusNotStarted       = "NOT_STARTED"
)

// DKIM signing attribute origins.
const (
	DkimOriginAWSSES   = "AWS_SES"
	DkimOriginExternal = "EXTERNAL"
)

// Suppression list reasons.
const (
	SuppressionReasonBounce    = "BOUNCE"
	SuppressionReasonComplaint = "COMPLAINT"
)

// Behavior-on-MX-failure values for a custom MAIL FROM domain.
const (
	BehaviorUseDefault = "USE_DEFAULT_VALUE"
	BehaviorReject     = "REJECT_MESSAGE"
)

// Identity is a verified (or pending) email identity: an address or a domain.
type Identity struct {
	Name                     string
	Type                     string
	VerificationStatus       string
	VerifiedForSendingStatus bool
	FeedbackForwardingStatus bool
	ConfigurationSetName     string

	// DKIM attributes.
	DkimStatus          string
	DkimSigningEnabled  bool
	DkimSigningOrigin   string
	DkimTokens          []string
	DkimSigningHostedZn string

	// Custom MAIL FROM attributes.
	MailFromDomain           string
	MailFromBehaviorOnMxFail string
	MailFromDomainStatus     string

	CreatedAt time.Time
	Tags      map[string]string

	// Identity resource policies keyed by policy name.
	Policies map[string]string
}

// ConfigurationSet groups sending options that can be referenced by name.
type ConfigurationSet struct {
	Name           string
	SendingEnabled bool
	ReputationOn   bool
	TLSPolicy      string
	SendingPoolN   string
	CreatedAt      time.Time
	Tags           map[string]string

	// Additional put-option state.
	ArchiveARN        string
	SuppressedReasons []string
	CustomRedirectDom string
	VdmEnabled        bool
	EventDestinations []EventDestination
}

// TemplateContent is the subject/HTML/text of an email template.
type TemplateContent struct {
	Subject string
	HTML    string
	Text    string
}

// Template is a reusable email template.
type Template struct {
	Name      string
	Content   TemplateContent
	CreatedAt time.Time
	Tags      map[string]string
}

// SuppressedDestination is an address on the account suppression list.
type SuppressedDestination struct {
	EmailAddress   string
	Reason         string
	LastUpdateTime time.Time
}

// SentMessage records an accepted outbound email so tests can assert on it.
type SentMessage struct {
	MessageID            string
	FromAddress          string
	ToAddresses          []string
	CcAddresses          []string
	BccAddresses         []string
	Subject              string
	ConfigurationSetName string
	TemplateName         string
	SentAt               time.Time
}

// Account holds the account-level SES v2 attributes.
type Account struct {
	SendingEnabled          bool
	ProductionAccessEnabled bool
	Max24HourSend           float64
	MaxSendRate             float64
	SentLast24Hours         float64
	SuppressedReasons       []string
	EnforcementStatus       string
}

// CreateIdentityInput describes an identity to create.
type CreateIdentityInput struct {
	EmailIdentity        string
	ConfigurationSetName string
	Tags                 map[string]string
}

// CreateConfigurationSetInput describes a configuration set to create.
type CreateConfigurationSetInput struct {
	Name           string
	SendingEnabled bool
	ReputationOn   bool
	TLSPolicy      string
	SendingPoolN   string
	Tags           map[string]string
}

// TemplateInput describes a template to create or update.
type TemplateInput struct {
	Name    string
	Content TemplateContent
	Tags    map[string]string
}

// SendEmailInput describes a message to send.
type SendEmailInput struct {
	FromAddress          string
	ToAddresses          []string
	CcAddresses          []string
	BccAddresses         []string
	Subject              string
	TextBody             string
	HTMLBody             string
	TemplateName         string
	TemplateData         string
	ConfigurationSetName string
}

// PutSuppressedInput adds an address to the suppression list.
type PutSuppressedInput struct {
	EmailAddress string
	Reason       string
}

// SESV2 is the interface an SES v2 backend implements.
type SESV2 interface {
	// Email identities.
	CreateEmailIdentity(ctx context.Context, in CreateIdentityInput) (*Identity, error)
	GetEmailIdentity(ctx context.Context, name string) (*Identity, error)
	DeleteEmailIdentity(ctx context.Context, name string) error
	ListEmailIdentities(ctx context.Context) ([]Identity, error)
	PutEmailIdentityDkimAttributes(ctx context.Context, name string, signingEnabled bool) error
	PutEmailIdentityMailFromAttributes(ctx context.Context, name, mailFromDomain, behaviorOnMxFail string) error

	// Configuration sets.
	CreateConfigurationSet(ctx context.Context, in CreateConfigurationSetInput) error
	GetConfigurationSet(ctx context.Context, name string) (*ConfigurationSet, error)
	DeleteConfigurationSet(ctx context.Context, name string) error
	ListConfigurationSets(ctx context.Context) ([]string, error)

	// Email templates.
	CreateEmailTemplate(ctx context.Context, in TemplateInput) error
	GetEmailTemplate(ctx context.Context, name string) (*Template, error)
	UpdateEmailTemplate(ctx context.Context, in TemplateInput) error
	DeleteEmailTemplate(ctx context.Context, name string) error
	ListEmailTemplates(ctx context.Context) ([]Template, error)
	TestRenderEmailTemplate(ctx context.Context, name, templateData string) (string, error)

	// Sending.
	SendEmail(ctx context.Context, in SendEmailInput) (string, error)
	ListSentMessages(ctx context.Context) []SentMessage

	// Suppression list.
	PutSuppressedDestination(ctx context.Context, in PutSuppressedInput) error
	GetSuppressedDestination(ctx context.Context, addr string) (*SuppressedDestination, error)
	DeleteSuppressedDestination(ctx context.Context, addr string) error
	ListSuppressedDestinations(ctx context.Context) ([]SuppressedDestination, error)

	// Account.
	GetAccount(ctx context.Context) (*Account, error)
	PutAccountSendingAttributes(ctx context.Context, sendingEnabled bool) error
	PutAccountSuppressionAttributes(ctx context.Context, suppressedReasons []string) error

	// Tags.
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, arn string) (map[string]string, error)

	// Contact lists.
	CreateContactList(ctx context.Context, in ContactListInput) error
	GetContactList(ctx context.Context, name string) (*ContactList, error)
	UpdateContactList(ctx context.Context, in ContactListInput) error
	DeleteContactList(ctx context.Context, name string) error
	ListContactLists(ctx context.Context) ([]ContactList, error)

	// Contacts.
	CreateContact(ctx context.Context, in ContactInput) error
	GetContact(ctx context.Context, listName, addr string) (*Contact, error)
	UpdateContact(ctx context.Context, in ContactInput) error
	DeleteContact(ctx context.Context, listName, addr string) error
	ListContacts(ctx context.Context, listName string) ([]Contact, error)

	// Custom verification email templates.
	CreateCustomVerificationEmailTemplate(ctx context.Context, in CustomVerificationEmailTemplateInput) error
	GetCustomVerificationEmailTemplate(ctx context.Context, name string) (*CustomVerificationEmailTemplate, error)
	UpdateCustomVerificationEmailTemplate(ctx context.Context, in CustomVerificationEmailTemplateInput) error
	DeleteCustomVerificationEmailTemplate(ctx context.Context, name string) error
	ListCustomVerificationEmailTemplates(ctx context.Context) ([]CustomVerificationEmailTemplate, error)
	SendCustomVerificationEmail(ctx context.Context, templateName, emailAddress, configSet string) (string, error)

	// Configuration-set event destinations.
	CreateConfigurationSetEventDestination(ctx context.Context, in EventDestinationInput) error
	UpdateConfigurationSetEventDestination(ctx context.Context, in EventDestinationInput) error
	DeleteConfigurationSetEventDestination(ctx context.Context, configSet, name string) error
	GetConfigurationSetEventDestinations(ctx context.Context, configSet string) ([]EventDestination, error)

	// Configuration-set put-options.
	PutConfigurationSetArchivingOptions(ctx context.Context, configSet, archiveARN string) error
	PutConfigurationSetDeliveryOptions(ctx context.Context, configSet, tlsPolicy, sendingPool string) error
	PutConfigurationSetReputationOptions(ctx context.Context, configSet string, reputationEnabled bool) error
	PutConfigurationSetSendingOptions(ctx context.Context, configSet string, sendingEnabled bool) error
	PutConfigurationSetSuppressionOptions(ctx context.Context, configSet string, suppressedReasons []string) error
	PutConfigurationSetTrackingOptions(ctx context.Context, configSet, customRedirectDomain string) error
	PutConfigurationSetVdmOptions(ctx context.Context, configSet string) error

	// Dedicated IP pools and IPs.
	CreateDedicatedIPPool(ctx context.Context, name, scalingMode string, tags map[string]string) error
	DeleteDedicatedIPPool(ctx context.Context, name string) error
	GetDedicatedIPPool(ctx context.Context, name string) (*DedicatedIPPool, error)
	ListDedicatedIPPools(ctx context.Context) ([]string, error)
	GetDedicatedIP(ctx context.Context, ip string) (*DedicatedIP, error)
	GetDedicatedIPs(ctx context.Context, poolName string) ([]DedicatedIP, error)
	PutDedicatedIPInPool(ctx context.Context, ip, destinationPool string) error
	PutDedicatedIPPoolScalingAttributes(ctx context.Context, poolName, scalingMode string) error
	PutDedicatedIPWarmupAttributes(ctx context.Context, ip string, warmupPct int32) error
	PutAccountDedicatedIPWarmupAttributes(ctx context.Context, autoWarmupEnabled bool) error

	// Deliverability dashboard.
	PutDeliverabilityDashboardOption(ctx context.Context, enabled bool) error
	GetDeliverabilityDashboardOptions(ctx context.Context) (bool, error)
	CreateDeliverabilityTestReport(ctx context.Context, in DeliverabilityTestReportInput) (*DeliverabilityTestReport, error)
	GetDeliverabilityTestReport(ctx context.Context, reportID string) (*DeliverabilityTestReport, error)
	ListDeliverabilityTestReports(ctx context.Context) ([]DeliverabilityTestReport, error)
	GetDomainDeliverabilityCampaign(ctx context.Context, campaignID string) (string, error)
	ListDomainDeliverabilityCampaigns(ctx context.Context, domain string) ([]string, error)
	GetDomainStatisticsReport(ctx context.Context, domain string) (string, error)
	GetBlacklistReports(ctx context.Context, ips []string) (map[string][]string, error)

	// Email identity policies and attributes.
	CreateEmailIdentityPolicy(ctx context.Context, identity, policyName, policy string) error
	GetEmailIdentityPolicies(ctx context.Context, identity string) (map[string]string, error)
	UpdateEmailIdentityPolicy(ctx context.Context, identity, policyName, policy string) error
	DeleteEmailIdentityPolicy(ctx context.Context, identity, policyName string) error
	PutEmailIdentityConfigurationSetAttributes(ctx context.Context, identity, configSet string) error
	PutEmailIdentityDkimSigningAttributes(ctx context.Context, identity, origin string) ([]string, error)
	PutEmailIdentityFeedbackAttributes(ctx context.Context, identity string, forwardingEnabled bool) error

	// Import / export jobs.
	CreateImportJob(ctx context.Context) (string, error)
	GetImportJob(ctx context.Context, jobID string) (*Job, error)
	ListImportJobs(ctx context.Context) ([]Job, error)
	CreateExportJob(ctx context.Context) (string, error)
	GetExportJob(ctx context.Context, jobID string) (*Job, error)
	ListExportJobs(ctx context.Context) ([]Job, error)
	CancelExportJob(ctx context.Context, jobID string) error

	// Insights, metrics and recommendations.
	BatchGetMetricData(ctx context.Context, queryIDs []string) (map[string][]int64, error)
	GetMessageInsights(ctx context.Context, messageID string) (*SentMessage, error)
	GetEmailAddressInsights(ctx context.Context, emailAddress string) (string, error)
	ListRecommendations(ctx context.Context) ([]string, error)

	// Account extras.
	PutAccountDetails(ctx context.Context, mailType, website string, productionAccess bool) error
	PutAccountVdmAttributes(ctx context.Context, enabled bool) error
	PutAccountPricingAttributes(ctx context.Context) error

	// Tenants.
	CreateTenant(ctx context.Context, name string, tags map[string]string) (*Tenant, error)
	GetTenant(ctx context.Context, name string) (*Tenant, error)
	DeleteTenant(ctx context.Context, name string) error
	ListTenants(ctx context.Context) ([]Tenant, error)
	CreateTenantResourceAssociation(ctx context.Context, tenantName, resourceARN string) error
	DeleteTenantResourceAssociation(ctx context.Context, tenantName, resourceARN string) error
	ListTenantResources(ctx context.Context, tenantName string) ([]TenantResource, error)
	ListResourceTenants(ctx context.Context, resourceARN string) ([]Tenant, error)
	PutTenantSuppressionAttributes(ctx context.Context, tenantName string, suppressedReasons []string) error

	// Reputation entities.
	GetReputationEntity(ctx context.Context, entityType, reference string) (*ReputationEntity, error)
	ListReputationEntities(ctx context.Context) ([]ReputationEntity, error)
	UpdateReputationEntityCustomerManagedStatus(ctx context.Context, entityType, reference, status string) error
	UpdateReputationEntityPolicy(ctx context.Context, entityType, reference, policy string) error

	// Multi-region endpoints.
	CreateMultiRegionEndpoint(ctx context.Context, in MultiRegionEndpointInput) (*MultiRegionEndpoint, error)
	GetMultiRegionEndpoint(ctx context.Context, name string) (*MultiRegionEndpoint, error)
	DeleteMultiRegionEndpoint(ctx context.Context, name string) (string, error)
	ListMultiRegionEndpoints(ctx context.Context) ([]MultiRegionEndpoint, error)

	// Bulk send.
	SendBulkEmail(ctx context.Context, in SendBulkEmailInput) ([]BulkEmailResult, error)
}
