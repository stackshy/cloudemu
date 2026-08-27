package sesv2

import "encoding/json"

// rawContent is a raw JSON message-content blob decoded lazily.
type rawContent json.RawMessage

// UnmarshalJSON captures the raw bytes of the content blob.
func (c *rawContent) UnmarshalJSON(b []byte) error {
	*c = append((*c)[:0], b...)

	return nil
}

// --- config-set event destinations ---

type kinesisFirehoseDestinationJSON struct {
	IamRoleArn        string `json:"IamRoleArn"`
	DeliveryStreamArn string `json:"DeliveryStreamArn"`
}

type snsDestinationJSON struct {
	TopicArn string `json:"TopicArn"`
}

type pinpointDestinationJSON struct {
	ApplicationArn string `json:"ApplicationArn"`
}

type cloudWatchDimensionConfigJSON struct {
	DimensionName         string `json:"DimensionName"`
	DimensionValueSource  string `json:"DimensionValueSource"`
	DefaultDimensionValue string `json:"DefaultDimensionValue"`
}

type cloudWatchDestinationJSON struct {
	DimensionConfigurations []cloudWatchDimensionConfigJSON `json:"DimensionConfigurations"`
}

type eventDestinationDefJSON struct {
	Enabled                    bool                            `json:"Enabled"`
	MatchingEventTypes         []string                        `json:"MatchingEventTypes"`
	KinesisFirehoseDestination *kinesisFirehoseDestinationJSON `json:"KinesisFirehoseDestination"`
	SnsDestination             *snsDestinationJSON             `json:"SnsDestination"`
	CloudWatchDestination      *cloudWatchDestinationJSON      `json:"CloudWatchDestination"`
	PinpointDestination        *pinpointDestinationJSON        `json:"PinpointDestination"`
}

type createEventDestinationRequest struct {
	EventDestinationName string                   `json:"EventDestinationName"`
	EventDestination     *eventDestinationDefJSON `json:"EventDestination"`
}

type updateEventDestinationRequest struct {
	EventDestination *eventDestinationDefJSON `json:"EventDestination"`
}

type eventDestinationJSON struct {
	Name                       string                          `json:"Name"`
	Enabled                    bool                            `json:"Enabled"`
	MatchingEventTypes         []string                        `json:"MatchingEventTypes"`
	KinesisFirehoseDestination *kinesisFirehoseDestinationJSON `json:"KinesisFirehoseDestination,omitempty"`
	SnsDestination             *snsDestinationJSON             `json:"SnsDestination,omitempty"`
	CloudWatchDestination      *cloudWatchDestinationJSON      `json:"CloudWatchDestination,omitempty"`
	PinpointDestination        *pinpointDestinationJSON        `json:"PinpointDestination,omitempty"`
}

type getEventDestinationsResponse struct {
	EventDestinations []eventDestinationJSON `json:"EventDestinations"`
}

// --- config-set put-options ---

type putArchivingOptionsRequest struct {
	ArchiveArn string `json:"ArchiveArn"`
}

type putDeliveryOptionsRequest struct {
	TLSPolicy       string `json:"TlsPolicy"`
	SendingPoolName string `json:"SendingPoolName"`
}

type putReputationOptionsRequest struct {
	ReputationMetricsEnabled bool `json:"ReputationMetricsEnabled"`
}

type putSendingOptionsRequest struct {
	SendingEnabled bool `json:"SendingEnabled"`
}

type putSuppressionOptionsRequest struct {
	SuppressedReasons []string `json:"SuppressedReasons"`
}

type putTrackingOptionsRequest struct {
	CustomRedirectDomain string `json:"CustomRedirectDomain"`
}

// --- contacts / contact lists ---

type topicJSON struct {
	TopicName                 string `json:"TopicName"`
	DisplayName               string `json:"DisplayName"`
	Description               string `json:"Description,omitempty"`
	DefaultSubscriptionStatus string `json:"DefaultSubscriptionStatus"`
}

type topicPreferenceJSON struct {
	TopicName          string `json:"TopicName"`
	SubscriptionStatus string `json:"SubscriptionStatus"`
}

type contactListRequest struct {
	ContactListName string      `json:"ContactListName"`
	Description     string      `json:"Description"`
	Topics          []topicJSON `json:"Topics"`
	Tags            []tag       `json:"Tags"`
}

type contactListResponse struct {
	ContactListName      string      `json:"ContactListName"`
	Description          string      `json:"Description,omitempty"`
	Topics               []topicJSON `json:"Topics"`
	CreatedTimestamp     float64     `json:"CreatedTimestamp"`
	LastUpdatedTimestamp float64     `json:"LastUpdatedTimestamp"`
	Tags                 []tag       `json:"Tags"`
}

type contactListSummaryJSON struct {
	ContactListName      string  `json:"ContactListName"`
	LastUpdatedTimestamp float64 `json:"LastUpdatedTimestamp"`
}

type listContactListsResponse struct {
	ContactLists []contactListSummaryJSON `json:"ContactLists"`
	NextToken    string                   `json:"NextToken,omitempty"`
}

type contactRequest struct {
	EmailAddress     string                `json:"EmailAddress"`
	TopicPreferences []topicPreferenceJSON `json:"TopicPreferences"`
	UnsubscribeAll   bool                  `json:"UnsubscribeAll"`
	AttributesData   string                `json:"AttributesData"`
}

type contactResponse struct {
	ContactListName      string                `json:"ContactListName"`
	EmailAddress         string                `json:"EmailAddress"`
	TopicPreferences     []topicPreferenceJSON `json:"TopicPreferences"`
	UnsubscribeAll       bool                  `json:"UnsubscribeAll"`
	AttributesData       string                `json:"AttributesData,omitempty"`
	CreatedTimestamp     float64               `json:"CreatedTimestamp"`
	LastUpdatedTimestamp float64               `json:"LastUpdatedTimestamp"`
}

type contactSummaryJSON struct {
	EmailAddress         string                `json:"EmailAddress"`
	TopicPreferences     []topicPreferenceJSON `json:"TopicPreferences"`
	UnsubscribeAll       bool                  `json:"UnsubscribeAll"`
	LastUpdatedTimestamp float64               `json:"LastUpdatedTimestamp"`
}

type listContactsResponse struct {
	Contacts  []contactSummaryJSON `json:"Contacts"`
	NextToken string               `json:"NextToken,omitempty"`
}

// --- custom verification email templates ---

type cvTemplateRequest struct {
	TemplateName          string `json:"TemplateName"`
	FromEmailAddress      string `json:"FromEmailAddress"`
	TemplateSubject       string `json:"TemplateSubject"`
	TemplateContent       string `json:"TemplateContent"`
	SuccessRedirectionURL string `json:"SuccessRedirectionURL"`
	FailureRedirectionURL string `json:"FailureRedirectionURL"`
}

type cvTemplateResponse struct {
	TemplateName          string `json:"TemplateName"`
	FromEmailAddress      string `json:"FromEmailAddress"`
	TemplateSubject       string `json:"TemplateSubject"`
	TemplateContent       string `json:"TemplateContent"`
	SuccessRedirectionURL string `json:"SuccessRedirectionURL"`
	FailureRedirectionURL string `json:"FailureRedirectionURL"`
}

type cvTemplateMetadataJSON struct {
	TemplateName          string `json:"TemplateName"`
	FromEmailAddress      string `json:"FromEmailAddress"`
	TemplateSubject       string `json:"TemplateSubject"`
	SuccessRedirectionURL string `json:"SuccessRedirectionURL"`
	FailureRedirectionURL string `json:"FailureRedirectionURL"`
}

type listCVTemplatesResponse struct {
	CustomVerificationEmailTemplates []cvTemplateMetadataJSON `json:"CustomVerificationEmailTemplates"`
	NextToken                        string                   `json:"NextToken,omitempty"`
}

type sendCVEmailRequest struct {
	TemplateName         string `json:"TemplateName"`
	EmailAddress         string `json:"EmailAddress"`
	ConfigurationSetName string `json:"ConfigurationSetName"`
}

// --- identity extras ---

type identityPolicyRequest struct {
	Policy string `json:"Policy"`
}

type getIdentityPoliciesResponse struct {
	Policies map[string]string `json:"Policies"`
}

type putDkimSigningRequest struct {
	SigningAttributesOrigin string `json:"SigningAttributesOrigin"`
}

type putDkimSigningResponse struct {
	DkimStatus string   `json:"DkimStatus"`
	DkimTokens []string `json:"DkimTokens"`
}

type putIdentityConfigSetRequest struct {
	ConfigurationSetName string `json:"ConfigurationSetName"`
}

type putIdentityFeedbackRequest struct {
	EmailForwardingEnabled bool `json:"EmailForwardingEnabled"`
}

// --- account extras ---

type putAccountDetailsRequest struct {
	MailType                string `json:"MailType"`
	WebsiteURL              string `json:"WebsiteURL"`
	ProductionAccessEnabled bool   `json:"ProductionAccessEnabled"`
}

type vdmAttributesJSON struct {
	VdmEnabled string `json:"VdmEnabled"`
}

type putAccountVdmRequest struct {
	VdmAttributes *vdmAttributesJSON `json:"VdmAttributes"`
}

type putAccountWarmupRequest struct {
	AutoWarmupEnabled bool `json:"AutoWarmupEnabled"`
}

// --- dedicated IPs ---

type createIPPoolRequest struct {
	PoolName    string `json:"PoolName"`
	ScalingMode string `json:"ScalingMode"`
	Tags        []tag  `json:"Tags"`
}

type dedicatedIPPoolJSON struct {
	PoolName    string `json:"PoolName"`
	ScalingMode string `json:"ScalingMode"`
}

type getIPPoolResponse struct {
	DedicatedIPPool dedicatedIPPoolJSON `json:"DedicatedIpPool"`
}

type listIPPoolsResponse struct {
	DedicatedIPPools []string `json:"DedicatedIpPools"`
	NextToken        string   `json:"NextToken,omitempty"`
}

type putIPPoolScalingRequest struct {
	ScalingMode string `json:"ScalingMode"`
}

type dedicatedIPJSON struct {
	IP               string `json:"Ip"`
	WarmupStatus     string `json:"WarmupStatus"`
	WarmupPercentage int32  `json:"WarmupPercentage"`
	PoolName         string `json:"PoolName,omitempty"`
}

type getDedicatedIPResponse struct {
	DedicatedIP dedicatedIPJSON `json:"DedicatedIp"`
}

type getDedicatedIPsResponse struct {
	DedicatedIPs []dedicatedIPJSON `json:"DedicatedIps"`
	NextToken    string            `json:"NextToken,omitempty"`
}

type putIPInPoolRequest struct {
	DestinationPoolName string `json:"DestinationPoolName"`
}

type putIPWarmupRequest struct {
	WarmupPercentage int32 `json:"WarmupPercentage"`
}

// --- deliverability dashboard ---

type putDashboardRequest struct {
	DashboardEnabled bool `json:"DashboardEnabled"`
}

type dashboardOptionsResponse struct {
	DashboardEnabled bool `json:"DashboardEnabled"`
}

type createTestReportRequest struct {
	ReportName       string     `json:"ReportName"`
	FromEmailAddress string     `json:"FromEmailAddress"`
	Content          rawContent `json:"Content"`
	Tags             []tag      `json:"Tags"`
}

type createTestReportResponse struct {
	ReportID                 string `json:"ReportId"`
	DeliverabilityTestStatus string `json:"DeliverabilityTestStatus"`
}

type testReportJSON struct {
	ReportID                 string  `json:"ReportId"`
	ReportName               string  `json:"ReportName,omitempty"`
	Subject                  string  `json:"Subject,omitempty"`
	FromEmailAddress         string  `json:"FromEmailAddress,omitempty"`
	CreateDate               float64 `json:"CreateDate"`
	DeliverabilityTestStatus string  `json:"DeliverabilityTestStatus"`
}

type listTestReportsResponse struct {
	DeliverabilityTestReports []testReportJSON `json:"DeliverabilityTestReports"`
	NextToken                 string           `json:"NextToken,omitempty"`
}

type getTestReportResponse struct {
	DeliverabilityTestReport testReportJSON `json:"DeliverabilityTestReport"`
	OverallPlacement         map[string]any `json:"OverallPlacement"`
	IspPlacements            []any          `json:"IspPlacements"`
}

type listCampaignsResponse struct {
	DomainDeliverabilityCampaigns []string `json:"DomainDeliverabilityCampaigns"`
	NextToken                     string   `json:"NextToken,omitempty"`
}

type getBlacklistReportsResponse struct {
	BlacklistReport map[string][]string `json:"BlacklistReport"`
}

// --- jobs ---

type createJobResponse struct {
	JobID string `json:"JobId"`
}

type getImportJobResponse struct {
	JobID            string  `json:"JobId"`
	JobStatus        string  `json:"JobStatus"`
	CreatedTimestamp float64 `json:"CreatedTimestamp"`
}

type getExportJobResponse struct {
	JobID            string  `json:"JobId"`
	JobStatus        string  `json:"JobStatus"`
	CreatedTimestamp float64 `json:"CreatedTimestamp"`
}

type jobSummaryJSON struct {
	JobID            string  `json:"JobId"`
	JobStatus        string  `json:"JobStatus"`
	CreatedTimestamp float64 `json:"CreatedTimestamp"`
}

type listImportJobsResponse struct {
	ImportJobs []jobSummaryJSON `json:"ImportJobs"`
	NextToken  string           `json:"NextToken,omitempty"`
}

type listExportJobsResponse struct {
	ExportJobs []jobSummaryJSON `json:"ExportJobs"`
	NextToken  string           `json:"NextToken,omitempty"`
}

// --- metrics / insights ---

type metricDataQueryJSON struct {
	ID string `json:"Id"`
}

type batchGetMetricDataRequest struct {
	Queries []metricDataQueryJSON `json:"Queries"`
}

type metricDataResultJSON struct {
	ID     string  `json:"Id"`
	Values []int64 `json:"Values"`
}

type batchGetMetricDataResponse struct {
	Results []metricDataResultJSON `json:"Results"`
	Errors  []any                  `json:"Errors"`
}

type getMessageInsightsResponse struct {
	MessageID        string `json:"MessageId"`
	FromEmailAddress string `json:"FromEmailAddress,omitempty"`
	Insights         []any  `json:"Insights"`
}

type addrInsightsRequest struct {
	EmailAddress string `json:"EmailAddress"`
}

type listRecommendationsResponse struct {
	Recommendations []any  `json:"Recommendations"`
	NextToken       string `json:"NextToken,omitempty"`
}

// --- multi-region endpoints ---

type endpointDetailsJSON struct {
	RoutesDetails []struct {
		Region string `json:"Region"`
	} `json:"RoutesDetails"`
}

func (d *endpointDetailsJSON) regions() []string {
	if d == nil {
		return nil
	}

	out := make([]string, 0, len(d.RoutesDetails))
	for _, rd := range d.RoutesDetails {
		out = append(out, rd.Region)
	}

	return out
}

type createEndpointRequest struct {
	EndpointName string               `json:"EndpointName"`
	Details      *endpointDetailsJSON `json:"Details"`
	Tags         []tag                `json:"Tags"`
}

type createEndpointResponse struct {
	EndpointID string `json:"EndpointId"`
	Status     string `json:"Status"`
}

type routeJSON struct {
	Region string `json:"Region"`
}

type endpointJSON struct {
	EndpointName string      `json:"EndpointName"`
	EndpointID   string      `json:"EndpointId"`
	Status       string      `json:"Status"`
	Routes       []routeJSON `json:"Routes,omitempty"`
}

type deleteEndpointResponse struct {
	Status string `json:"Status"`
}

type listEndpointsResponse struct {
	MultiRegionEndpoints []endpointJSON `json:"MultiRegionEndpoints"`
	NextToken            string         `json:"NextToken,omitempty"`
}

func regionsToRoutes(regions []string) []routeJSON {
	out := make([]routeJSON, 0, len(regions))
	for _, r := range regions {
		out = append(out, routeJSON{Region: r})
	}

	return out
}

// --- reputation entities ---

type reputationStatusJSON struct {
	Status string `json:"Status"`
}

type reputationEntityJSON struct {
	ReputationEntityReference string               `json:"ReputationEntityReference"`
	ReputationEntityType      string               `json:"ReputationEntityType"`
	CustomerManagedStatus     reputationStatusJSON `json:"CustomerManagedStatus"`
	AwsSesManagedStatus       reputationStatusJSON `json:"AwsSesManagedStatus"`
}

type getReputationEntityResponse struct {
	ReputationEntity reputationEntityJSON `json:"ReputationEntity"`
}

type listReputationEntitiesResponse struct {
	ReputationEntities []reputationEntityJSON `json:"ReputationEntities"`
	NextToken          string                 `json:"NextToken,omitempty"`
}

type updateRepStatusRequest struct {
	SendingStatus string `json:"SendingStatus"`
}

type updateRepPolicyRequest struct {
	ReputationEntityPolicy string `json:"ReputationEntityPolicy"`
}

// --- tenants ---

type createTenantRequest struct {
	TenantName string `json:"TenantName"`
	Tags       []tag  `json:"Tags"`
}

type createTenantResponse struct {
	TenantName string `json:"TenantName"`
	TenantID   string `json:"TenantId"`
	TenantArn  string `json:"TenantArn"`
}

type tenantNameRequest struct {
	TenantName string `json:"TenantName"`
}

type tenantInfoJSON struct {
	TenantName string `json:"TenantName"`
	TenantID   string `json:"TenantId,omitempty"`
	TenantArn  string `json:"TenantArn,omitempty"`
}

type getTenantResponse struct {
	Tenant tenantInfoJSON `json:"Tenant"`
}

type listTenantsResponse struct {
	Tenants   []tenantInfoJSON `json:"Tenants"`
	NextToken string           `json:"NextToken,omitempty"`
}

type tenantResourceRequest struct {
	TenantName  string `json:"TenantName"`
	ResourceArn string `json:"ResourceArn"`
}

type tenantResourceJSON struct {
	ResourceArn string `json:"ResourceArn"`
}

type listTenantResourcesResponse struct {
	TenantResources []tenantResourceJSON `json:"TenantResources"`
	NextToken       string               `json:"NextToken,omitempty"`
}

type tenantSuppressionRequest struct {
	TenantName        string   `json:"TenantName"`
	SuppressedReasons []string `json:"SuppressedReasons"`
}

type resourceTenantsRequest struct {
	ResourceArn string `json:"ResourceArn"`
}

type listResourceTenantsResponse struct {
	ResourceTenants []tenantInfoJSON `json:"ResourceTenants"`
	NextToken       string           `json:"NextToken,omitempty"`
}

// --- bulk send ---

type bulkTemplateRefJSON struct {
	TemplateName string `json:"TemplateName"`
	TemplateData string `json:"TemplateData"`
}

type bulkDefaultContentJSON struct {
	Template *bulkTemplateRefJSON `json:"Template"`
}

type bulkEmailEntryJSON struct {
	Destination *destinationJSON `json:"Destination"`
}

type sendBulkEmailRequest struct {
	FromEmailAddress     string                  `json:"FromEmailAddress"`
	ConfigurationSetName string                  `json:"ConfigurationSetName"`
	DefaultContent       *bulkDefaultContentJSON `json:"DefaultContent"`
	BulkEmailEntries     []bulkEmailEntryJSON    `json:"BulkEmailEntries"`
}

type bulkEmailResultJSON struct {
	Status    string `json:"Status"`
	MessageID string `json:"MessageId,omitempty"`
	Error     string `json:"Error,omitempty"`
}

type sendBulkEmailResponse struct {
	BulkEmailEntryResults []bulkEmailResultJSON `json:"BulkEmailEntryResults"`
}
