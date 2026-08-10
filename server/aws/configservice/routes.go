package configservice

import "net/http"

// buildRoutes wires every Config operation to its handler. Split out of New so
// the map literal doesn't trip funlen on the constructor.
func (h *Handler) buildRoutes() map[string]http.HandlerFunc {
	routes := map[string]http.HandlerFunc{}

	h.addRecorderRoutes(routes)
	h.addChannelRoutes(routes)
	h.addRuleRoutes(routes)
	h.addComplianceRoutes(routes)
	h.addPackRoutes(routes)
	h.addOrgRoutes(routes)
	h.addAggregatorRoutes(routes)
	h.addAggregateQueryRoutes(routes)
	h.addRemediationRoutes(routes)
	h.addResourceRoutes(routes)
	h.addMiscRoutes(routes)

	return routes
}

func (h *Handler) addRecorderRoutes(r map[string]http.HandlerFunc) {
	r["PutConfigurationRecorder"] = h.putConfigurationRecorder
	r["DescribeConfigurationRecorders"] = h.describeConfigurationRecorders
	r["DescribeConfigurationRecorderStatus"] = h.describeConfigurationRecorderStatus
	r["DeleteConfigurationRecorder"] = h.deleteConfigurationRecorder
	r["StartConfigurationRecorder"] = h.startConfigurationRecorder
	r["StopConfigurationRecorder"] = h.stopConfigurationRecorder
	r["ListConfigurationRecorders"] = h.listConfigurationRecorders
	r["PutServiceLinkedConfigurationRecorder"] = h.putServiceLinkedConfigurationRecorder
	r["DeleteServiceLinkedConfigurationRecorder"] = h.deleteServiceLinkedConfigurationRecorder
	r["PutThirdPartyServiceLinkedConfigurationRecorder"] = h.putThirdPartyServiceLinkedConfigurationRecorder
	r["AssociateResourceTypes"] = h.associateResourceTypes
	r["DisassociateResourceTypes"] = h.disassociateResourceTypes
}

func (h *Handler) addChannelRoutes(r map[string]http.HandlerFunc) {
	r["PutDeliveryChannel"] = h.putDeliveryChannel
	r["DescribeDeliveryChannels"] = h.describeDeliveryChannels
	r["DescribeDeliveryChannelStatus"] = h.describeDeliveryChannelStatus
	r["DeleteDeliveryChannel"] = h.deleteDeliveryChannel
	r["DeliverConfigSnapshot"] = h.deliverConfigSnapshot
}

func (h *Handler) addRuleRoutes(r map[string]http.HandlerFunc) {
	r["PutConfigRule"] = h.putConfigRule
	r["DescribeConfigRules"] = h.describeConfigRules
	r["DeleteConfigRule"] = h.deleteConfigRule
	r["DescribeConfigRuleEvaluationStatus"] = h.describeConfigRuleEvaluationStatus
	r["StartConfigRulesEvaluation"] = h.startConfigRulesEvaluation
	r["PutEvaluations"] = h.putEvaluations
	r["PutExternalEvaluation"] = h.putExternalEvaluation
	r["DeleteEvaluationResults"] = h.deleteEvaluationResults
	r["GetCustomRulePolicy"] = h.getCustomRulePolicy
}

func (h *Handler) addComplianceRoutes(r map[string]http.HandlerFunc) {
	r["DescribeComplianceByConfigRule"] = h.describeComplianceByConfigRule
	r["DescribeComplianceByResource"] = h.describeComplianceByResource
	r["GetComplianceDetailsByConfigRule"] = h.getComplianceDetailsByConfigRule
	r["GetComplianceDetailsByResource"] = h.getComplianceDetailsByResource
	r["GetComplianceSummaryByConfigRule"] = h.getComplianceSummaryByConfigRule
	r["GetComplianceSummaryByResourceType"] = h.getComplianceSummaryByResourceType
}

func (h *Handler) addPackRoutes(r map[string]http.HandlerFunc) {
	r["PutConformancePack"] = h.putConformancePack
	r["DescribeConformancePacks"] = h.describeConformancePacks
	r["DescribeConformancePackStatus"] = h.describeConformancePackStatus
	r["DeleteConformancePack"] = h.deleteConformancePack
	r["GetConformancePackComplianceDetails"] = h.getConformancePackComplianceDetails
	r["GetConformancePackComplianceSummary"] = h.getConformancePackComplianceSummary
	r["DescribeConformancePackCompliance"] = h.describeConformancePackCompliance
	r["ListConformancePackComplianceScores"] = h.listConformancePackComplianceScores
}

func (h *Handler) addOrgRoutes(r map[string]http.HandlerFunc) {
	r["PutOrganizationConfigRule"] = h.putOrganizationConfigRule
	r["DescribeOrganizationConfigRules"] = h.describeOrganizationConfigRules
	r["DescribeOrganizationConfigRuleStatuses"] = h.describeOrganizationConfigRuleStatuses
	r["DeleteOrganizationConfigRule"] = h.deleteOrganizationConfigRule
	r["GetOrganizationConfigRuleDetailedStatus"] = h.getOrganizationConfigRuleDetailedStatus
	r["GetOrganizationCustomRulePolicy"] = h.getOrganizationCustomRulePolicy
	r["PutOrganizationConformancePack"] = h.putOrganizationConformancePack
	r["DescribeOrganizationConformancePacks"] = h.describeOrganizationConformancePacks
	r["DescribeOrganizationConformancePackStatuses"] = h.describeOrganizationConformancePackStatuses
	r["DeleteOrganizationConformancePack"] = h.deleteOrganizationConformancePack
	r["GetOrganizationConformancePackDetailedStatus"] = h.getOrganizationConformancePackDetailedStatus
}

func (h *Handler) addAggregatorRoutes(r map[string]http.HandlerFunc) {
	r["PutConfigurationAggregator"] = h.putConfigurationAggregator
	r["DescribeConfigurationAggregators"] = h.describeConfigurationAggregators
	r["DeleteConfigurationAggregator"] = h.deleteConfigurationAggregator
	r["DescribeConfigurationAggregatorSourcesStatus"] = h.describeConfigurationAggregatorSourcesStatus
	r["PutAggregationAuthorization"] = h.putAggregationAuthorization
	r["DescribeAggregationAuthorizations"] = h.describeAggregationAuthorizations
	r["DeleteAggregationAuthorization"] = h.deleteAggregationAuthorization
	r["DescribePendingAggregationRequests"] = h.describePendingAggregationRequests
	r["DeletePendingAggregationRequest"] = h.deletePendingAggregationRequest
}

func (h *Handler) addAggregateQueryRoutes(r map[string]http.HandlerFunc) {
	r["DescribeAggregateComplianceByConfigRules"] = h.describeAggregateComplianceByConfigRules
	r["DescribeAggregateComplianceByConformancePacks"] = h.describeAggregateComplianceByConformancePacks
	r["GetAggregateComplianceDetailsByConfigRule"] = h.getAggregateComplianceDetailsByConfigRule
	r["GetAggregateConfigRuleComplianceSummary"] = h.getAggregateConfigRuleComplianceSummary
	r["GetAggregateConformancePackComplianceSummary"] = h.getAggregateConformancePackComplianceSummary
	r["GetAggregateDiscoveredResourceCounts"] = h.getAggregateDiscoveredResourceCounts
	r["GetAggregateResourceConfig"] = h.getAggregateResourceConfig
	r["BatchGetAggregateResourceConfig"] = h.batchGetAggregateResourceConfig
	r["ListAggregateDiscoveredResources"] = h.listAggregateDiscoveredResources
	r["SelectAggregateResourceConfig"] = h.selectAggregateResourceConfig
}

func (h *Handler) addRemediationRoutes(r map[string]http.HandlerFunc) {
	r["PutRemediationConfigurations"] = h.putRemediationConfigurations
	r["DescribeRemediationConfigurations"] = h.describeRemediationConfigurations
	r["DeleteRemediationConfiguration"] = h.deleteRemediationConfiguration
	r["PutRemediationExceptions"] = h.putRemediationExceptions
	r["DescribeRemediationExceptions"] = h.describeRemediationExceptions
	r["DeleteRemediationExceptions"] = h.deleteRemediationExceptions
	r["DescribeRemediationExecutionStatus"] = h.describeRemediationExecutionStatus
	r["StartRemediationExecution"] = h.startRemediationExecution
}

func (h *Handler) addResourceRoutes(r map[string]http.HandlerFunc) {
	r["PutResourceConfig"] = h.putResourceConfig
	r["GetResourceConfigHistory"] = h.getResourceConfigHistory
	r["DeleteResourceConfig"] = h.deleteResourceConfig
	r["BatchGetResourceConfig"] = h.batchGetResourceConfig
	r["ListDiscoveredResources"] = h.listDiscoveredResources
	r["GetDiscoveredResourceCounts"] = h.getDiscoveredResourceCounts
	r["SelectResourceConfig"] = h.selectResourceConfig
	r["StartResourceEvaluation"] = h.startResourceEvaluation
	r["GetResourceEvaluationSummary"] = h.getResourceEvaluationSummary
	r["ListResourceEvaluations"] = h.listResourceEvaluations
}

func (h *Handler) addMiscRoutes(r map[string]http.HandlerFunc) {
	r["PutStoredQuery"] = h.putStoredQuery
	r["GetStoredQuery"] = h.getStoredQuery
	r["ListStoredQueries"] = h.listStoredQueries
	r["DeleteStoredQuery"] = h.deleteStoredQuery
	r["PutRetentionConfiguration"] = h.putRetentionConfiguration
	r["DescribeRetentionConfigurations"] = h.describeRetentionConfigurations
	r["DeleteRetentionConfiguration"] = h.deleteRetentionConfiguration
	r["PutConnector"] = h.putConnector
	r["GetConnector"] = h.getConnector
	r["ListConnectors"] = h.listConnectors
	r["DeleteConnector"] = h.deleteConnector
	r["TagResource"] = h.tagResource
	r["UntagResource"] = h.untagResource
	r["ListTagsForResource"] = h.listTagsForResource
}
