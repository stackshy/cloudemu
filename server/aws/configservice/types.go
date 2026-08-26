package configservice

import (
	"time"

	cfgdriver "github.com/stackshy/cloudemu/v2/services/configservice/driver"
)

// AWS Config uses a mixed JSON casing convention: nested document members are
// sometimes lowerCamel (ConfigurationRecorder, DeliveryChannel,
// ConfigurationItem) and sometimes PascalCase (ConfigRule, ConformancePack,
// Aggregator, StoredQuery, Retention). Struct tags below use the exact key the
// SDK deserializer expects for each shape (confirmed against the smithy model).
// Request decoding is case-insensitive under encoding/json, so PascalCase
// operation-input keys still bind.

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

func epochOrNil(t time.Time) *float64 {
	if t.IsZero() {
		return nil
	}

	secs := float64(t.Unix())

	return &secs
}

func timeFromEpoch(secs *float64) time.Time {
	if secs == nil {
		return time.Time{}
	}

	return time.Unix(int64(*secs), 0).UTC()
}

// --- Recording group / recorder (lowerCamel document members) ---

type recordingGroupJSON struct {
	AllSupported               bool     `json:"allSupported"`
	IncludeGlobalResourceTypes bool     `json:"includeGlobalResourceTypes"`
	ResourceTypes              []string `json:"resourceTypes,omitempty"`
	RecordingStrategy          *struct {
		UseOnly string `json:"useOnly,omitempty"`
	} `json:"recordingStrategy,omitempty"`
	ExclusionByResourceTypes *struct {
		ResourceTypes []string `json:"resourceTypes,omitempty"`
	} `json:"exclusionByResourceTypes,omitempty"`
}

func (g *recordingGroupJSON) toDriver() *cfgdriver.RecordingGroup {
	if g == nil {
		return nil
	}

	out := &cfgdriver.RecordingGroup{
		AllSupported:               g.AllSupported,
		IncludeGlobalResourceTypes: g.IncludeGlobalResourceTypes,
		ResourceTypes:              g.ResourceTypes,
	}

	if g.RecordingStrategy != nil {
		out.RecordingStrategy = g.RecordingStrategy.UseOnly
	}

	if g.ExclusionByResourceTypes != nil {
		out.ExclusionByResources = g.ExclusionByResourceTypes.ResourceTypes
	}

	return out
}

func recordingGroupToWire(g *cfgdriver.RecordingGroup) *recordingGroupJSON {
	if g == nil {
		return nil
	}

	out := &recordingGroupJSON{
		AllSupported:               g.AllSupported,
		IncludeGlobalResourceTypes: g.IncludeGlobalResourceTypes,
		ResourceTypes:              g.ResourceTypes,
	}

	if g.RecordingStrategy != "" {
		out.RecordingStrategy = &struct {
			UseOnly string `json:"useOnly,omitempty"`
		}{UseOnly: g.RecordingStrategy}
	}

	// Re-emit the exclusion list when the group uses the exclusion strategy;
	// otherwise describe would silently drop it (Terraform phantom drift).
	if g.RecordingStrategy == "EXCLUSION_BY_RESOURCE_TYPES" && len(g.ExclusionByResources) > 0 {
		out.ExclusionByResourceTypes = &struct {
			ResourceTypes []string `json:"resourceTypes,omitempty"`
		}{ResourceTypes: g.ExclusionByResources}
	}

	return out
}

type recordingModeOverrideJSON struct {
	Description        string   `json:"description,omitempty"`
	ResourceTypes      []string `json:"resourceTypes,omitempty"`
	RecordingFrequency string   `json:"recordingFrequency,omitempty"`
}

type recordingModeJSON struct {
	RecordingFrequency     string                      `json:"recordingFrequency,omitempty"`
	RecordingModeOverrides []recordingModeOverrideJSON `json:"recordingModeOverrides,omitempty"`
}

func (m *recordingModeJSON) toDriver() *cfgdriver.RecordingMode {
	if m == nil {
		return nil
	}

	out := &cfgdriver.RecordingMode{RecordingFrequency: m.RecordingFrequency}

	for i := range m.RecordingModeOverrides {
		o := m.RecordingModeOverrides[i]
		out.RecordingModeOverrides = append(out.RecordingModeOverrides, cfgdriver.RecordingModeOverride{
			Description:        o.Description,
			ResourceTypes:      o.ResourceTypes,
			RecordingFrequency: o.RecordingFrequency,
		})
	}

	return out
}

func recordingModeToWire(m *cfgdriver.RecordingMode) *recordingModeJSON {
	if m == nil {
		return nil
	}

	out := &recordingModeJSON{RecordingFrequency: m.RecordingFrequency}

	for i := range m.RecordingModeOverrides {
		o := m.RecordingModeOverrides[i]
		out.RecordingModeOverrides = append(out.RecordingModeOverrides, recordingModeOverrideJSON{
			Description:        o.Description,
			ResourceTypes:      o.ResourceTypes,
			RecordingFrequency: o.RecordingFrequency,
		})
	}

	return out
}

type configurationRecorderJSON struct {
	Arn            string              `json:"arn,omitempty"`
	Name           string              `json:"name,omitempty"`
	RoleARN        string              `json:"roleARN,omitempty"`
	RecordingGroup *recordingGroupJSON `json:"recordingGroup,omitempty"`
	RecordingMode  *recordingModeJSON  `json:"recordingMode,omitempty"`
}

func (c *configurationRecorderJSON) toDriver() cfgdriver.ConfigurationRecorder {
	if c == nil {
		return cfgdriver.ConfigurationRecorder{}
	}

	return cfgdriver.ConfigurationRecorder{
		Name:           c.Name,
		RoleARN:        c.RoleARN,
		RecordingGroup: c.RecordingGroup.toDriver(),
		RecordingMode:  c.RecordingMode.toDriver(),
	}
}

func recorderToWire(r *cfgdriver.ConfigurationRecorder) configurationRecorderJSON {
	return configurationRecorderJSON{
		Arn:            r.Arn,
		Name:           r.Name,
		RoleARN:        r.RoleARN,
		RecordingGroup: recordingGroupToWire(r.RecordingGroup),
		RecordingMode:  recordingModeToWire(r.RecordingMode),
	}
}

type recorderStatusJSON struct {
	Arn                  string   `json:"arn,omitempty"`
	Name                 string   `json:"name,omitempty"`
	LastStatus           string   `json:"lastStatus,omitempty"`
	Recording            bool     `json:"recording"`
	LastStartTime        *float64 `json:"lastStartTime,omitempty"`
	LastStopTime         *float64 `json:"lastStopTime,omitempty"`
	LastStatusChangeTime *float64 `json:"lastStatusChangeTime,omitempty"`
}

func recorderStatusToWire(r *cfgdriver.ConfigurationRecorder) recorderStatusJSON {
	return recorderStatusJSON{
		Arn:                  r.Arn,
		Name:                 r.Name,
		LastStatus:           r.LastStatus,
		Recording:            r.Recording,
		LastStartTime:        epochOrNil(r.LastStartTime),
		LastStopTime:         epochOrNil(r.LastStopTime),
		LastStatusChangeTime: epochOrNil(r.LastStatusChangeTime),
	}
}

// --- Delivery channel (lowerCamel) ---

type snapshotDeliveryJSON struct {
	DeliveryFrequency string `json:"deliveryFrequency,omitempty"`
}

type deliveryChannelJSON struct {
	Name                             string                `json:"name,omitempty"`
	S3BucketName                     string                `json:"s3BucketName,omitempty"`
	S3KeyPrefix                      string                `json:"s3KeyPrefix,omitempty"`
	S3KmsKeyArn                      string                `json:"s3KmsKeyArn,omitempty"`
	SnsTopicARN                      string                `json:"snsTopicARN,omitempty"`
	ConfigSnapshotDeliveryProperties *snapshotDeliveryJSON `json:"configSnapshotDeliveryProperties,omitempty"`
}

func (c *deliveryChannelJSON) toDriver() cfgdriver.DeliveryChannel {
	out := cfgdriver.DeliveryChannel{
		Name:         c.Name,
		S3BucketName: c.S3BucketName,
		S3KeyPrefix:  c.S3KeyPrefix,
		S3KmsKeyArn:  c.S3KmsKeyArn,
		SnsTopicARN:  c.SnsTopicARN,
	}

	if c.ConfigSnapshotDeliveryProperties != nil {
		out.SnapshotDeliveryProps = &cfgdriver.ConfigSnapshotDeliveryProperties{
			DeliveryFrequency: c.ConfigSnapshotDeliveryProperties.DeliveryFrequency,
		}
	}

	return out
}

func channelToWire(c *cfgdriver.DeliveryChannel) deliveryChannelJSON {
	out := deliveryChannelJSON{
		Name:         c.Name,
		S3BucketName: c.S3BucketName,
		S3KeyPrefix:  c.S3KeyPrefix,
		S3KmsKeyArn:  c.S3KmsKeyArn,
		SnsTopicARN:  c.SnsTopicARN,
	}

	if c.SnapshotDeliveryProps != nil {
		out.ConfigSnapshotDeliveryProperties = &snapshotDeliveryJSON{
			DeliveryFrequency: c.SnapshotDeliveryProps.DeliveryFrequency,
		}
	}

	return out
}

type channelStatusJSON struct {
	Name string `json:"name,omitempty"`
}

// --- Config rule (PascalCase document members) ---

type scopeJSON struct {
	ComplianceResourceTypes []string `json:"ComplianceResourceTypes,omitempty"`
	ComplianceResourceID    string   `json:"ComplianceResourceId,omitempty"`
	TagKey                  string   `json:"TagKey,omitempty"`
	TagValue                string   `json:"TagValue,omitempty"`
}

type sourceJSON struct {
	Owner            string `json:"Owner,omitempty"`
	SourceIdentifier string `json:"SourceIdentifier,omitempty"`
}

type configRuleJSON struct {
	ConfigRuleArn             string      `json:"ConfigRuleArn,omitempty"`
	ConfigRuleID              string      `json:"ConfigRuleId,omitempty"`
	ConfigRuleName            string      `json:"ConfigRuleName,omitempty"`
	Description               string      `json:"Description,omitempty"`
	Scope                     *scopeJSON  `json:"Scope,omitempty"`
	Source                    *sourceJSON `json:"Source,omitempty"`
	InputParameters           string      `json:"InputParameters,omitempty"`
	MaximumExecutionFrequency string      `json:"MaximumExecutionFrequency,omitempty"`
	ConfigRuleState           string      `json:"ConfigRuleState,omitempty"`
	CreatedBy                 string      `json:"CreatedBy,omitempty"`
}

func (c *configRuleJSON) toDriver() cfgdriver.ConfigRule {
	out := cfgdriver.ConfigRule{
		ConfigRuleName:            c.ConfigRuleName,
		Description:               c.Description,
		InputParameters:           c.InputParameters,
		MaximumExecutionFrequency: c.MaximumExecutionFrequency,
	}

	if c.Scope != nil {
		out.Scope = &cfgdriver.RuleScope{
			ComplianceResourceTypes: c.Scope.ComplianceResourceTypes,
			ComplianceResourceID:    c.Scope.ComplianceResourceID,
			TagKey:                  c.Scope.TagKey,
			TagValue:                c.Scope.TagValue,
		}
	}

	if c.Source != nil {
		out.Source = &cfgdriver.RuleSource{
			Owner:            c.Source.Owner,
			SourceIdentifier: c.Source.SourceIdentifier,
		}
	}

	return out
}

func ruleToWire(r *cfgdriver.ConfigRule) configRuleJSON {
	out := configRuleJSON{
		ConfigRuleArn:             r.ConfigRuleArn,
		ConfigRuleID:              r.ConfigRuleID,
		ConfigRuleName:            r.ConfigRuleName,
		Description:               r.Description,
		InputParameters:           r.InputParameters,
		MaximumExecutionFrequency: r.MaximumExecutionFrequency,
		ConfigRuleState:           r.ConfigRuleState,
		CreatedBy:                 r.CreatedBy,
	}

	if r.Scope != nil {
		out.Scope = &scopeJSON{
			ComplianceResourceTypes: r.Scope.ComplianceResourceTypes,
			ComplianceResourceID:    r.Scope.ComplianceResourceID,
			TagKey:                  r.Scope.TagKey,
			TagValue:                r.Scope.TagValue,
		}
	}

	if r.Source != nil {
		out.Source = &sourceJSON{Owner: r.Source.Owner, SourceIdentifier: r.Source.SourceIdentifier}
	}

	return out
}

type complianceJSON struct {
	ComplianceType string `json:"ComplianceType,omitempty"`
}

type complianceByRuleJSON struct {
	ConfigRuleName string          `json:"ConfigRuleName,omitempty"`
	Compliance     *complianceJSON `json:"Compliance,omitempty"`
}

// --- Evaluation (PascalCase) ---

type evaluationJSON struct {
	ComplianceResourceType string   `json:"ComplianceResourceType,omitempty"`
	ComplianceResourceID   string   `json:"ComplianceResourceId,omitempty"`
	ComplianceType         string   `json:"ComplianceType,omitempty"`
	Annotation             string   `json:"Annotation,omitempty"`
	OrderingTimestamp      *float64 `json:"OrderingTimestamp,omitempty"`
}

func (e *evaluationJSON) toDriver() cfgdriver.Evaluation {
	return cfgdriver.Evaluation{
		ComplianceResourceType: e.ComplianceResourceType,
		ComplianceResourceID:   e.ComplianceResourceID,
		ComplianceType:         e.ComplianceType,
		Annotation:             e.Annotation,
		OrderingTimestamp:      timeFromEpoch(e.OrderingTimestamp),
	}
}

func toDriverEvals(in []evaluationJSON) []cfgdriver.Evaluation {
	out := make([]cfgdriver.Evaluation, 0, len(in))
	for i := range in {
		out = append(out, in[i].toDriver())
	}

	return out
}

func evalToWire(e *cfgdriver.Evaluation) evaluationJSON {
	return evaluationJSON{
		ComplianceResourceType: e.ComplianceResourceType,
		ComplianceResourceID:   e.ComplianceResourceID,
		ComplianceType:         e.ComplianceType,
		Annotation:             e.Annotation,
		OrderingTimestamp:      epochOrNil(e.OrderingTimestamp),
	}
}

// --- Conformance pack (PascalCase) ---

type packInputParamJSON struct {
	ParameterName  string `json:"ParameterName"`
	ParameterValue string `json:"ParameterValue"`
}

type conformancePackJSON struct {
	ConformancePackArn      string   `json:"ConformancePackArn,omitempty"`
	ConformancePackID       string   `json:"ConformancePackId,omitempty"`
	ConformancePackName     string   `json:"ConformancePackName,omitempty"`
	DeliveryS3Bucket        string   `json:"DeliveryS3Bucket,omitempty"`
	DeliveryS3KeyPrefix     string   `json:"DeliveryS3KeyPrefix,omitempty"`
	LastUpdateRequestedTime *float64 `json:"LastUpdateRequestedTime,omitempty"`
	CreatedBy               string   `json:"CreatedBy,omitempty"`
}

func packToWire(p *cfgdriver.ConformancePack) conformancePackJSON {
	return conformancePackJSON{
		ConformancePackArn:      p.ConformancePackArn,
		ConformancePackID:       p.ConformancePackID,
		ConformancePackName:     p.ConformancePackName,
		DeliveryS3Bucket:        p.DeliveryS3Bucket,
		DeliveryS3KeyPrefix:     p.DeliveryS3KeyPrefix,
		LastUpdateRequestedTime: epochOrNil(p.LastUpdateRequestedTime),
		CreatedBy:               p.CreatedBy,
	}
}

type packStatusJSON struct {
	ConformancePackArn      string   `json:"ConformancePackArn,omitempty"`
	ConformancePackID       string   `json:"ConformancePackId,omitempty"`
	ConformancePackName     string   `json:"ConformancePackName,omitempty"`
	ConformancePackState    string   `json:"ConformancePackState,omitempty"`
	LastUpdateRequestedTime *float64 `json:"LastUpdateRequestedTime,omitempty"`
}

// --- Aggregators (PascalCase) ---

type accountSourceJSON struct {
	AccountIDs    []string `json:"AccountIds,omitempty"`
	AllAwsRegions bool     `json:"AllAwsRegions"`
	AwsRegions    []string `json:"AwsRegions,omitempty"`
}

type orgSourceJSON struct {
	RoleArn       string   `json:"RoleArn,omitempty"`
	AllAwsRegions bool     `json:"AllAwsRegions"`
	AwsRegions    []string `json:"AwsRegions,omitempty"`
}

type aggregatorJSON struct {
	ConfigurationAggregatorArn    string              `json:"ConfigurationAggregatorArn,omitempty"`
	ConfigurationAggregatorName   string              `json:"ConfigurationAggregatorName,omitempty"`
	AccountAggregationSources     []accountSourceJSON `json:"AccountAggregationSources,omitempty"`
	OrganizationAggregationSource *orgSourceJSON      `json:"OrganizationAggregationSource,omitempty"`
	CreationTime                  *float64            `json:"CreationTime,omitempty"`
	LastUpdatedTime               *float64            `json:"LastUpdatedTime,omitempty"`
	CreatedBy                     string              `json:"CreatedBy,omitempty"`
}

func aggToWire(a *cfgdriver.ConfigurationAggregator) aggregatorJSON {
	out := aggregatorJSON{
		ConfigurationAggregatorArn:  a.Arn,
		ConfigurationAggregatorName: a.Name,
		CreationTime:                epochOrNil(a.CreationTime),
		LastUpdatedTime:             epochOrNil(a.LastUpdatedTime),
		CreatedBy:                   a.CreatedBy,
	}

	for i := range a.AccountSources {
		s := a.AccountSources[i]
		out.AccountAggregationSources = append(out.AccountAggregationSources, accountSourceJSON{
			AccountIDs: s.AccountIDs, AllAwsRegions: s.AllAwsRegions, AwsRegions: s.AwsRegions,
		})
	}

	if a.OrganizationSource != nil {
		out.OrganizationAggregationSource = &orgSourceJSON{
			RoleArn:       a.OrganizationSource.RoleARN,
			AllAwsRegions: a.OrganizationSource.AllAwsRegions,
			AwsRegions:    a.OrganizationSource.AwsRegions,
		}
	}

	return out
}

func accountSourcesToDriver(in []accountSourceJSON) []cfgdriver.AccountAggregationSource {
	out := make([]cfgdriver.AccountAggregationSource, 0, len(in))
	for _, s := range in {
		out = append(out, cfgdriver.AccountAggregationSource{
			AccountIDs: s.AccountIDs, AllAwsRegions: s.AllAwsRegions, AwsRegions: s.AwsRegions,
		})
	}

	return out
}

type aggregationAuthJSON struct {
	AggregationAuthorizationArn string   `json:"AggregationAuthorizationArn,omitempty"`
	AuthorizedAccountID         string   `json:"AuthorizedAccountId,omitempty"`
	AuthorizedAwsRegion         string   `json:"AuthorizedAwsRegion,omitempty"`
	CreationTime                *float64 `json:"CreationTime,omitempty"`
}

func authToWire(a *cfgdriver.AggregationAuthorization) aggregationAuthJSON {
	return aggregationAuthJSON{
		AggregationAuthorizationArn: a.Arn,
		AuthorizedAccountID:         a.AuthorizedAccountID,
		AuthorizedAwsRegion:         a.AuthorizedAwsRegion,
		CreationTime:                epochOrNil(a.CreationTime),
	}
}

// --- Stored query / retention / remediation / resource item ---

type storedQueryJSON struct {
	QueryArn    string `json:"QueryArn,omitempty"`
	QueryID     string `json:"QueryId,omitempty"`
	QueryName   string `json:"QueryName,omitempty"`
	Description string `json:"Description,omitempty"`
	Expression  string `json:"Expression,omitempty"`
}

type storedQueryMetaJSON struct {
	QueryArn    string `json:"QueryArn,omitempty"`
	QueryID     string `json:"QueryId,omitempty"`
	QueryName   string `json:"QueryName,omitempty"`
	Description string `json:"Description,omitempty"`
}

type retentionJSON struct {
	Name                  string `json:"Name,omitempty"`
	RetentionPeriodInDays int32  `json:"RetentionPeriodInDays"`
}

type remediationConfigJSON struct {
	Arn                      string            `json:"Arn,omitempty"`
	ConfigRuleName           string            `json:"ConfigRuleName,omitempty"`
	TargetType               string            `json:"TargetType,omitempty"`
	TargetID                 string            `json:"TargetId,omitempty"`
	TargetVersion            string            `json:"TargetVersion,omitempty"`
	ResourceType             string            `json:"ResourceType,omitempty"`
	Automatic                bool              `json:"Automatic"`
	MaximumAutomaticAttempts int32             `json:"MaximumAutomaticAttempts,omitempty"`
	RetryAttemptSeconds      int64             `json:"RetryAttemptSeconds,omitempty"`
	Parameters               map[string]string `json:"-"`
}

func remediationToWire(c *cfgdriver.RemediationConfiguration) remediationConfigJSON {
	return remediationConfigJSON{
		Arn:                      c.Arn,
		ConfigRuleName:           c.ConfigRuleName,
		TargetType:               c.TargetType,
		TargetID:                 c.TargetID,
		TargetVersion:            c.TargetVersion,
		ResourceType:             c.ResourceType,
		Automatic:                c.Automatic,
		MaximumAutomaticAttempts: c.MaximumAutomaticAttempts,
		RetryAttemptSeconds:      c.RetryAttemptSeconds,
		Parameters:               c.Parameters,
	}
}

func (c *remediationConfigJSON) toDriver() cfgdriver.RemediationConfiguration {
	return cfgdriver.RemediationConfiguration{
		ConfigRuleName:           c.ConfigRuleName,
		TargetType:               c.TargetType,
		TargetID:                 c.TargetID,
		TargetVersion:            c.TargetVersion,
		ResourceType:             c.ResourceType,
		Automatic:                c.Automatic,
		MaximumAutomaticAttempts: c.MaximumAutomaticAttempts,
		RetryAttemptSeconds:      c.RetryAttemptSeconds,
	}
}

type remediationExceptionJSON struct {
	ConfigRuleName string   `json:"ConfigRuleName,omitempty"`
	ResourceType   string   `json:"ResourceType,omitempty"`
	ResourceID     string   `json:"ResourceId,omitempty"`
	Message        string   `json:"Message,omitempty"`
	ExpirationTime *float64 `json:"ExpirationTime,omitempty"`
}

func remExceptionToWire(e *cfgdriver.RemediationException) remediationExceptionJSON {
	return remediationExceptionJSON{
		ConfigRuleName: e.ConfigRuleName,
		ResourceType:   e.ResourceType,
		ResourceID:     e.ResourceID,
		Message:        e.Message,
		ExpirationTime: epochOrNil(e.ExpirationTime),
	}
}

type resourceKeyJSON struct {
	ResourceType string `json:"resourceType,omitempty"`
	ResourceID   string `json:"resourceId,omitempty"`
}

func (k resourceKeyJSON) toDriver() cfgdriver.ResourceKey {
	return cfgdriver.ResourceKey{ResourceType: k.ResourceType, ResourceID: k.ResourceID}
}

func resourceKeyToWire(k cfgdriver.ResourceKey) resourceKeyJSON {
	return resourceKeyJSON{ResourceType: k.ResourceType, ResourceID: k.ResourceID}
}

type resourceIdentifierJSON struct {
	ResourceType string `json:"resourceType,omitempty"`
	ResourceID   string `json:"resourceId,omitempty"`
	ResourceName string `json:"resourceName,omitempty"`
}

type resourceCountJSON struct {
	ResourceType string `json:"resourceType,omitempty"`
	Count        int64  `json:"count"`
}

type configurationItemJSON struct {
	ResourceType                 string            `json:"resourceType,omitempty"`
	ResourceID                   string            `json:"resourceId,omitempty"`
	ResourceName                 string            `json:"resourceName,omitempty"`
	Arn                          string            `json:"arn,omitempty"`
	AwsRegion                    string            `json:"awsRegion,omitempty"`
	AccountID                    string            `json:"accountId,omitempty"`
	ConfigurationItemStatus      string            `json:"configurationItemStatus,omitempty"`
	ConfigurationItemCaptureTime *float64          `json:"configurationItemCaptureTime,omitempty"`
	Configuration                string            `json:"configuration,omitempty"`
	Tags                         map[string]string `json:"tags,omitempty"`
}

func itemToWire(i *cfgdriver.ConfigurationItem) configurationItemJSON {
	return configurationItemJSON{
		ResourceType:                 i.ResourceType,
		ResourceID:                   i.ResourceID,
		ResourceName:                 i.ResourceName,
		Arn:                          i.Arn,
		AwsRegion:                    i.AwsRegion,
		AccountID:                    i.AccountID,
		ConfigurationItemStatus:      i.ConfigurationState,
		ConfigurationItemCaptureTime: epochOrNil(i.CaptureTime),
		Configuration:                i.Configuration,
		Tags:                         i.Tags,
	}
}

// pageFrom builds a driver.Page from a request's NextToken/Limit.
func pageFrom(nextToken string, limit int32) cfgdriver.Page {
	return cfgdriver.Page{NextToken: nextToken, Limit: limit}
}

// ruleNamePageReq is the shared shape for per-rule, paginated compliance/detail
// requests.
type ruleNamePageReq struct {
	ConfigRuleName string `json:"ConfigRuleName"`
	NextToken      string `json:"NextToken"`
	Limit          int32  `json:"Limit"`
}
