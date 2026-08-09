package sesv2

import (
	"time"

	"github.com/stackshy/cloudemu/v2/services/sesv2/driver"
)

// nanosPerSecond converts nanoseconds to fractional epoch seconds.
const nanosPerSecond = 1e9

// epochSeconds renders a time as fractional epoch-seconds (the restJson1 wire
// form SES uses for timestamps).
func epochSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / nanosPerSecond
}

// tag is the SES v2 wire tag shape ({Key, Value}).
type tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

func tagsToMap(tags []tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}

	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}

	return out
}

func mapToTags(m map[string]string) []tag {
	out := make([]tag, 0, len(m))
	for k, v := range m {
		out = append(out, tag{Key: k, Value: v})
	}

	return out
}

// --- identities ---

type dkimAttributesJSON struct {
	SigningEnabled          bool     `json:"SigningEnabled"`
	Status                  string   `json:"Status,omitempty"`
	Tokens                  []string `json:"Tokens,omitempty"`
	SigningAttributesOrigin string   `json:"SigningAttributesOrigin,omitempty"`
	SigningHostedZone       string   `json:"SigningHostedZone,omitempty"`
}

type mailFromAttributesJSON struct {
	MailFromDomain       string `json:"MailFromDomain,omitempty"`
	MailFromDomainStatus string `json:"MailFromDomainStatus,omitempty"`
	BehaviorOnMxFailure  string `json:"BehaviorOnMxFailure,omitempty"`
}

type createEmailIdentityRequest struct {
	EmailIdentity        string `json:"EmailIdentity"`
	ConfigurationSetName string `json:"ConfigurationSetName"`
	Tags                 []tag  `json:"Tags"`
}

type createEmailIdentityResponse struct {
	IdentityType             string             `json:"IdentityType"`
	VerifiedForSendingStatus bool               `json:"VerifiedForSendingStatus"`
	DkimAttributes           dkimAttributesJSON `json:"DkimAttributes"`
}

type getEmailIdentityResponse struct {
	IdentityType             string                  `json:"IdentityType"`
	FeedbackForwardingStatus bool                    `json:"FeedbackForwardingStatus"`
	VerifiedForSendingStatus bool                    `json:"VerifiedForSendingStatus"`
	VerificationStatus       string                  `json:"VerificationStatus"`
	ConfigurationSetName     string                  `json:"ConfigurationSetName,omitempty"`
	DkimAttributes           dkimAttributesJSON      `json:"DkimAttributes"`
	MailFromAttributes       *mailFromAttributesJSON `json:"MailFromAttributes,omitempty"`
	Tags                     []tag                   `json:"Tags"`
}

type identityInfoJSON struct {
	IdentityName       string `json:"IdentityName"`
	IdentityType       string `json:"IdentityType"`
	SendingEnabled     bool   `json:"SendingEnabled"`
	VerificationStatus string `json:"VerificationStatus"`
}

type listEmailIdentitiesResponse struct {
	EmailIdentities []identityInfoJSON `json:"EmailIdentities"`
	NextToken       string             `json:"NextToken,omitempty"`
}

type putDkimAttributesRequest struct {
	SigningEnabled bool `json:"SigningEnabled"`
}

type putMailFromAttributesRequest struct {
	MailFromDomain      string `json:"MailFromDomain"`
	BehaviorOnMxFailure string `json:"BehaviorOnMxFailure"`
}

func identityToDkimJSON(id *driver.Identity) dkimAttributesJSON {
	return dkimAttributesJSON{
		SigningEnabled:          id.DkimSigningEnabled,
		Status:                  id.DkimStatus,
		Tokens:                  id.DkimTokens,
		SigningAttributesOrigin: id.DkimSigningOrigin,
		SigningHostedZone:       id.DkimSigningHostedZn,
	}
}

// --- configuration sets ---

type sendingOptionsJSON struct {
	SendingEnabled bool `json:"SendingEnabled"`
}

type reputationOptionsJSON struct {
	ReputationMetricsEnabled bool `json:"ReputationMetricsEnabled"`
}

type deliveryOptionsJSON struct {
	TLSPolicy       string `json:"TlsPolicy,omitempty"`
	SendingPoolName string `json:"SendingPoolName,omitempty"`
}

type createConfigurationSetRequest struct {
	ConfigurationSetName string                 `json:"ConfigurationSetName"`
	SendingOptions       *sendingOptionsJSON    `json:"SendingOptions"`
	ReputationOptions    *reputationOptionsJSON `json:"ReputationOptions"`
	DeliveryOptions      *deliveryOptionsJSON   `json:"DeliveryOptions"`
	Tags                 []tag                  `json:"Tags"`
}

type getConfigurationSetResponse struct {
	ConfigurationSetName string                `json:"ConfigurationSetName"`
	SendingOptions       sendingOptionsJSON    `json:"SendingOptions"`
	ReputationOptions    reputationOptionsJSON `json:"ReputationOptions"`
	DeliveryOptions      deliveryOptionsJSON   `json:"DeliveryOptions"`
	Tags                 []tag                 `json:"Tags"`
}

type listConfigurationSetsResponse struct {
	ConfigurationSets []string `json:"ConfigurationSets"`
	NextToken         string   `json:"NextToken,omitempty"`
}

// --- templates ---

type emailTemplateContentJSON struct {
	Subject string `json:"Subject,omitempty"`
	HTML    string `json:"Html,omitempty"`
	Text    string `json:"Text,omitempty"`
}

type createEmailTemplateRequest struct {
	TemplateName    string                   `json:"TemplateName"`
	TemplateContent emailTemplateContentJSON `json:"TemplateContent"`
}

type updateEmailTemplateRequest struct {
	TemplateContent emailTemplateContentJSON `json:"TemplateContent"`
}

type getEmailTemplateResponse struct {
	TemplateName    string                   `json:"TemplateName"`
	TemplateContent emailTemplateContentJSON `json:"TemplateContent"`
}

type templateMetadataJSON struct {
	TemplateName string  `json:"TemplateName"`
	CreatedTime  float64 `json:"CreatedTimestamp"`
}

type listEmailTemplatesResponse struct {
	TemplatesMetadata []templateMetadataJSON `json:"TemplatesMetadata"`
	NextToken         string                 `json:"NextToken,omitempty"`
}

type testRenderRequest struct {
	TemplateData string `json:"TemplateData"`
}

type testRenderResponse struct {
	RenderedTemplate string `json:"RenderedTemplate"`
}

func contentToDriver(c emailTemplateContentJSON) driver.TemplateContent {
	return driver.TemplateContent{Subject: c.Subject, HTML: c.HTML, Text: c.Text}
}

func contentToWire(c driver.TemplateContent) emailTemplateContentJSON {
	return emailTemplateContentJSON{Subject: c.Subject, HTML: c.HTML, Text: c.Text}
}

// --- sending ---

type contentJSON struct {
	Data    string `json:"Data"`
	Charset string `json:"Charset,omitempty"`
}

type bodyJSON struct {
	HTML *contentJSON `json:"Html"`
	Text *contentJSON `json:"Text"`
}

type messageJSON struct {
	Subject *contentJSON `json:"Subject"`
	Body    *bodyJSON    `json:"Body"`
}

type templateRefJSON struct {
	TemplateName    string                    `json:"TemplateName"`
	TemplateArn     string                    `json:"TemplateArn"`
	TemplateData    string                    `json:"TemplateData"`
	TemplateContent *emailTemplateContentJSON `json:"TemplateContent"`
}

type emailContentJSON struct {
	Simple   *messageJSON     `json:"Simple"`
	Template *templateRefJSON `json:"Template"`
}

type destinationJSON struct {
	ToAddresses  []string `json:"ToAddresses"`
	CcAddresses  []string `json:"CcAddresses"`
	BccAddresses []string `json:"BccAddresses"`
}

type sendEmailRequest struct {
	FromEmailAddress     string            `json:"FromEmailAddress"`
	Destination          *destinationJSON  `json:"Destination"`
	Content              *emailContentJSON `json:"Content"`
	ConfigurationSetName string            `json:"ConfigurationSetName"`
}

type sendEmailResponse struct {
	MessageID string `json:"MessageId"`
}

// --- suppression ---

type suppressedDestinationJSON struct {
	EmailAddress   string  `json:"EmailAddress"`
	Reason         string  `json:"Reason"`
	LastUpdateTime float64 `json:"LastUpdateTime"`
}

type getSuppressedDestinationResponse struct {
	SuppressedDestination suppressedDestinationJSON `json:"SuppressedDestination"`
}

type suppressedSummaryJSON struct {
	EmailAddress   string  `json:"EmailAddress"`
	Reason         string  `json:"Reason"`
	LastUpdateTime float64 `json:"LastUpdateTime"`
}

type listSuppressedDestinationsResponse struct {
	SuppressedDestinationSummaries []suppressedSummaryJSON `json:"SuppressedDestinationSummaries"`
	NextToken                      string                  `json:"NextToken,omitempty"`
}

type putSuppressedDestinationRequest struct {
	EmailAddress string `json:"EmailAddress"`
	Reason       string `json:"Reason"`
}

// --- account ---

type sendQuotaJSON struct {
	Max24HourSend   float64 `json:"Max24HourSend"`
	MaxSendRate     float64 `json:"MaxSendRate"`
	SentLast24Hours float64 `json:"SentLast24Hours"`
}

type suppressionAttributesJSON struct {
	SuppressedReasons []string `json:"SuppressedReasons"`
}

type getAccountResponse struct {
	SendingEnabled          bool                      `json:"SendingEnabled"`
	ProductionAccessEnabled bool                      `json:"ProductionAccessEnabled"`
	EnforcementStatus       string                    `json:"EnforcementStatus,omitempty"`
	SendQuota               sendQuotaJSON             `json:"SendQuota"`
	SuppressionAttributes   suppressionAttributesJSON `json:"SuppressionAttributes"`
}

type putAccountSendingRequest struct {
	SendingEnabled bool `json:"SendingEnabled"`
}

type putAccountSuppressionRequest struct {
	SuppressedReasons []string `json:"SuppressedReasons"`
}

// --- tags ---

type tagResourceRequest struct {
	ResourceArn string `json:"ResourceArn"`
	Tags        []tag  `json:"Tags"`
}

type listTagsForResourceResponse struct {
	Tags []tag `json:"Tags"`
}
