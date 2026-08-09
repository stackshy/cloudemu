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
}
