// Package driver defines the interface and types for AWS Config
// (configservice) implementations. It models configuration recorders, delivery
// channels, config rules and their compliance/evaluation state, conformance
// packs, organization rules and packs, configuration aggregators and
// authorizations, remediation, stored queries, retention, and the read-only
// resource-configuration query surface.
//
// Read-only, compliance, and evaluation surfaces (compliance summaries,
// discovered-resource queries, aggregate queries, SelectResourceConfig) are
// synthesized from the emulator's own recorded state or return plausible empty
// results — the emulator does not run a real Config recording pipeline. These
// synthesized behaviors are documented per method and in docs/services.md.
package driver

import "context"

// Config is the interface an AWS Config backend implements. Every method name
// matches its aws-sdk-go-v2/service/configservice Client operation so the
// coverage generator can map them one-to-one.
//
//nolint:interfacebloat // AWS Config exposes 100+ operations; full-parity emulation requires them all.
type Config interface {
	// Configuration recorders.
	PutConfigurationRecorder(ctx context.Context, rec ConfigurationRecorder) error
	DescribeConfigurationRecorders(ctx context.Context, names []string) ([]ConfigurationRecorder, error)
	DescribeConfigurationRecorderStatus(ctx context.Context, names []string) ([]ConfigurationRecorder, error)
	DeleteConfigurationRecorder(ctx context.Context, name string) error
	StartConfigurationRecorder(ctx context.Context, name string) error
	StopConfigurationRecorder(ctx context.Context, name string) error
	ListConfigurationRecorders(ctx context.Context, page Page) ([]ConfigurationRecorder, string, error)
	PutServiceLinkedConfigurationRecorder(ctx context.Context, principal string, tags map[string]string) (arn, name string, err error)
	DeleteServiceLinkedConfigurationRecorder(ctx context.Context, name string) (arn, delName string, err error)
	PutThirdPartyServiceLinkedConfigurationRecorder(
		ctx context.Context, principal string, tags map[string]string,
	) (arn, name string, err error)

	// Delivery channels.
	PutDeliveryChannel(ctx context.Context, ch DeliveryChannel) error
	DescribeDeliveryChannels(ctx context.Context, names []string) ([]DeliveryChannel, error)
	DescribeDeliveryChannelStatus(ctx context.Context, names []string) ([]DeliveryChannel, error)
	DeleteDeliveryChannel(ctx context.Context, name string) error
	DeliverConfigSnapshot(ctx context.Context, channelName string) (snapshotID string, err error)

	// Config rules.
	PutConfigRule(ctx context.Context, rule ConfigRule) error
	DescribeConfigRules(ctx context.Context, names []string, page Page) ([]ConfigRule, string, error)
	DeleteConfigRule(ctx context.Context, name string) error
	DescribeConfigRuleEvaluationStatus(ctx context.Context, names []string, page Page) ([]ConfigRule, string, error)
	StartConfigRulesEvaluation(ctx context.Context, names []string) error
	PutEvaluations(ctx context.Context, resultToken string, evals []Evaluation, testMode bool) ([]Evaluation, error)
	PutExternalEvaluation(ctx context.Context, ruleName string, eval Evaluation) error
	DeleteEvaluationResults(ctx context.Context, ruleName string) error
	GetCustomRulePolicy(ctx context.Context, ruleName string) (string, error)

	// Compliance (synthesized from recorded rules/evaluations).
	DescribeComplianceByConfigRule(ctx context.Context, names []string, page Page) ([]ConfigRule, string, error)
	DescribeComplianceByResource(ctx context.Context, resourceType, resourceID string, page Page) ([]ConfigurationItem, string, error)
	GetComplianceDetailsByConfigRule(ctx context.Context, ruleName string, page Page) ([]Evaluation, string, error)
	GetComplianceDetailsByResource(ctx context.Context, resourceType, resourceID string, page Page) ([]Evaluation, string, error)
	GetComplianceSummaryByConfigRule(ctx context.Context) (compliant, nonCompliant int32, err error)
	GetComplianceSummaryByResourceType(ctx context.Context, resourceTypes []string) (compliant, nonCompliant int32, err error)

	// Conformance packs.
	PutConformancePack(ctx context.Context, pack ConformancePack) (arn string, err error)
	DescribeConformancePacks(ctx context.Context, names []string, page Page) ([]ConformancePack, string, error)
	DescribeConformancePackStatus(ctx context.Context, names []string, page Page) ([]ConformancePack, string, error)
	DeleteConformancePack(ctx context.Context, name string) error
	GetConformancePackComplianceDetails(ctx context.Context, name string, page Page) ([]Evaluation, string, error)
	GetConformancePackComplianceSummary(ctx context.Context, names []string, page Page) ([]ConformancePack, string, error)
	DescribeConformancePackCompliance(ctx context.Context, name string, page Page) ([]ConfigRule, string, error)
	ListConformancePackComplianceScores(ctx context.Context, page Page) ([]ConformancePack, string, error)

	// Organization config rules and conformance packs.
	PutOrganizationConfigRule(ctx context.Context, rule OrganizationConfigRule) (arn string, err error)
	DescribeOrganizationConfigRules(ctx context.Context, names []string, page Page) ([]OrganizationConfigRule, string, error)
	DescribeOrganizationConfigRuleStatuses(ctx context.Context, names []string, page Page) ([]OrganizationConfigRule, string, error)
	DeleteOrganizationConfigRule(ctx context.Context, name string) error
	GetOrganizationConfigRuleDetailedStatus(ctx context.Context, name string, page Page) ([]OrganizationConfigRule, string, error)
	GetOrganizationCustomRulePolicy(ctx context.Context, name string) (string, error)
	PutOrganizationConformancePack(ctx context.Context, pack OrganizationConformancePack) (arn string, err error)
	DescribeOrganizationConformancePacks(ctx context.Context, names []string, page Page) ([]OrganizationConformancePack, string, error)
	DescribeOrganizationConformancePackStatuses(ctx context.Context, names []string, page Page) ([]OrganizationConformancePack, string, error)
	DeleteOrganizationConformancePack(ctx context.Context, name string) error
	GetOrganizationConformancePackDetailedStatus(ctx context.Context, name string, page Page) ([]OrganizationConformancePack, string, error)

	// Aggregators and aggregation authorizations.
	PutConfigurationAggregator(ctx context.Context, agg ConfigurationAggregator) (ConfigurationAggregator, error)
	DescribeConfigurationAggregators(ctx context.Context, names []string, page Page) ([]ConfigurationAggregator, string, error)
	DeleteConfigurationAggregator(ctx context.Context, name string) error
	DescribeConfigurationAggregatorSourcesStatus(ctx context.Context, name string, page Page) ([]ConfigurationAggregator, string, error)
	PutAggregationAuthorization(
		ctx context.Context, authAccountID, authRegion string, tags map[string]string,
	) (AggregationAuthorization, error)
	DescribeAggregationAuthorizations(ctx context.Context, page Page) ([]AggregationAuthorization, string, error)
	DeleteAggregationAuthorization(ctx context.Context, authAccountID, authRegion string) error
	DescribePendingAggregationRequests(ctx context.Context, page Page) ([]PendingAggregationRequest, string, error)
	DeletePendingAggregationRequest(ctx context.Context, requesterAccountID, requesterRegion string) error

	// Aggregate compliance/resource queries (synthesized).
	DescribeAggregateComplianceByConfigRules(ctx context.Context, aggregatorName string, page Page) ([]ConfigRule, string, error)
	DescribeAggregateComplianceByConformancePacks(ctx context.Context, aggregatorName string, page Page) ([]ConformancePack, string, error)
	GetAggregateComplianceDetailsByConfigRule(
		ctx context.Context, aggregatorName, ruleName, accountID, region string, page Page,
	) ([]Evaluation, string, error)
	GetAggregateConfigRuleComplianceSummary(ctx context.Context, aggregatorName string, page Page) (compliant, nonCompliant int32, err error)
	GetAggregateConformancePackComplianceSummary(
		ctx context.Context, aggregatorName string, page Page,
	) (compliant, nonCompliant int32, err error)
	GetAggregateDiscoveredResourceCounts(
		ctx context.Context, aggregatorName string, page Page,
	) (total int64, counts []ResourceCount, next string, err error)
	GetAggregateResourceConfig(ctx context.Context, aggregatorName, resourceType, resourceID string) (*ConfigurationItem, error)
	BatchGetAggregateResourceConfig(ctx context.Context, aggregatorName string, keys []ResourceKey) ([]ConfigurationItem, []ResourceKey, error)
	ListAggregateDiscoveredResources(ctx context.Context, aggregatorName, resourceType string, page Page) ([]ResourceKey, string, error)
	SelectAggregateResourceConfig(ctx context.Context, aggregatorName, expression string, page Page) ([]string, string, error)

	// Remediation.
	PutRemediationConfigurations(ctx context.Context, cfgs []RemediationConfiguration) ([]RemediationConfiguration, error)
	DescribeRemediationConfigurations(ctx context.Context, ruleNames []string) ([]RemediationConfiguration, error)
	DeleteRemediationConfiguration(ctx context.Context, ruleName, resourceType string) error
	PutRemediationExceptions(ctx context.Context, ruleName string, exceptions []RemediationException) ([]RemediationException, error)
	DescribeRemediationExceptions(ctx context.Context, ruleName string, keys []ResourceKey, page Page) ([]RemediationException, string, error)
	DeleteRemediationExceptions(ctx context.Context, ruleName string, keys []ResourceKey) ([]ResourceKey, error)
	DescribeRemediationExecutionStatus(ctx context.Context, ruleName string, keys []ResourceKey, page Page) ([]ResourceKey, string, error)
	StartRemediationExecution(ctx context.Context, ruleName string, keys []ResourceKey) ([]ResourceKey, error)

	// Stored queries.
	PutStoredQuery(ctx context.Context, query StoredQuery, tags map[string]string) (arn string, err error)
	GetStoredQuery(ctx context.Context, name string) (*StoredQuery, error)
	ListStoredQueries(ctx context.Context, page Page) ([]StoredQuery, string, error)
	DeleteStoredQuery(ctx context.Context, name string) error

	// Retention configurations.
	PutRetentionConfiguration(ctx context.Context, retentionDays int32) (RetentionConfiguration, error)
	DescribeRetentionConfigurations(ctx context.Context, names []string, page Page) ([]RetentionConfiguration, string, error)
	DeleteRetentionConfiguration(ctx context.Context, name string) error

	// Resource-configuration queries (synthesized from the emulator's own state).
	PutResourceConfig(ctx context.Context, item ConfigurationItem) error
	GetResourceConfigHistory(ctx context.Context, resourceType, resourceID string, page Page) ([]ConfigurationItem, string, error)
	DeleteResourceConfig(ctx context.Context, resourceType, resourceID string) error
	BatchGetResourceConfig(ctx context.Context, keys []ResourceKey) ([]ConfigurationItem, []ResourceKey, error)
	ListDiscoveredResources(ctx context.Context, resourceType string, ids []string, page Page) ([]ResourceKey, string, error)
	GetDiscoveredResourceCounts(
		ctx context.Context, resourceTypes []string, page Page,
	) (total int64, counts []ResourceCount, next string, err error)
	SelectResourceConfig(ctx context.Context, expression string, page Page) ([]string, string, error)

	// Resource evaluations (synthesized).
	StartResourceEvaluation(ctx context.Context, resourceType, resourceConfig string) (evaluationID string, err error)
	GetResourceEvaluationSummary(ctx context.Context, evaluationID string) (status, resourceType string, err error)
	ListResourceEvaluations(ctx context.Context, page Page) ([]string, string, error)

	// Recorder resource-type association.
	AssociateResourceTypes(ctx context.Context, recorderArn string, resourceTypes []string) (ConfigurationRecorder, error)
	DisassociateResourceTypes(ctx context.Context, recorderArn string, resourceTypes []string) (ConfigurationRecorder, error)

	// Connectors (third-party service integrations; synthesized).
	PutConnector(ctx context.Context, name, connectorAgentArn string) (arn string, err error)
	GetConnector(ctx context.Context, name string) (arn, agentArn string, err error)
	ListConnectors(ctx context.Context, page Page) ([]string, string, error)
	DeleteConnector(ctx context.Context, name string) error

	// Tags.
	TagResource(ctx context.Context, arn string, tags map[string]string) error
	UntagResource(ctx context.Context, arn string, tagKeys []string) error
	ListTagsForResource(ctx context.Context, arn string, page Page) (map[string]string, string, error)
}
