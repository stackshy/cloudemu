package driver

// AWS Config exception names, used to select the precise X-Amzn-Errortype at the
// wire layer. Config models many distinct exceptions that don't map one-to-one
// to canonical cloudemu codes, so errors carry the exception they concern.
const (
	ExNoSuchConfigRule                            = "NoSuchConfigRuleException"
	ExNoSuchConfigurationRecorder                 = "NoSuchConfigurationRecorderException"
	ExNoSuchDeliveryChannel                       = "NoSuchDeliveryChannelException"
	ExNoSuchBucket                                = "NoSuchBucketException"
	ExNoSuchConfigurationAggregator               = "NoSuchConfigurationAggregatorException"
	ExNoSuchConformancePack                       = "NoSuchConformancePackException"
	ExNoSuchOrganizationConfigRule                = "NoSuchOrganizationConfigRuleException"
	ExNoSuchOrganizationConformancePack           = "NoSuchOrganizationConformancePackException"
	ExNoSuchRemediationConfiguration              = "NoSuchRemediationConfigurationException"
	ExNoSuchRemediationException                  = "NoSuchRemediationExceptionException"
	ExNoSuchRetentionConfiguration                = "NoSuchRetentionConfigurationException"
	ExInvalidParameterValue                       = "InvalidParameterValueException"
	ExValidation                                  = "ValidationException"
	ExResourceInUse                               = "ResourceInUseException"
	ExResourceNotFound                            = "ResourceNotFoundException"
	ExResourceNotDiscovered                       = "ResourceNotDiscoveredException"
	ExMaxNumberOfConfigurationRecordersExceeded   = "MaxNumberOfConfigurationRecordersExceededException"
	ExMaxNumberOfDeliveryChannelsExceeded         = "MaxNumberOfDeliveryChannelsExceededException"
	ExMaxNumberOfConfigRulesExceeded              = "MaxNumberOfConfigRulesExceededException"
	ExMaxNumberOfConformancePacksExceeded         = "MaxNumberOfConformancePacksExceededException"
	ExMaxNumberOfConfigurationAggregatorsExceeded = "MaxNumberOfConfigurationAggregatorsExceededException"
	ExMaxNumberOfRetentionConfigurationsExceeded  = "MaxNumberOfRetentionConfigurationsExceededException"
	ExInvalidNextToken                            = "InvalidNextTokenException"
	ExInvalidLimit                                = "InvalidLimitException"
	ExInvalidConfigurationRecorderName            = "InvalidConfigurationRecorderNameException"
	ExInvalidDeliveryChannelName                  = "InvalidDeliveryChannelNameException"
	ExInvalidS3KeyPrefix                          = "InvalidS3KeyPrefixException"
	ExInvalidSNSTopicARN                          = "InvalidSNSTopicARNException"
	ExInvalidRoleARN                              = "InvalidRoleException"
	ExNoAvailableConfigurationRecorder            = "NoAvailableConfigurationRecorderException"
	ExNoAvailableDeliveryChannel                  = "NoAvailableDeliveryChannelException"
	ExLastDeliveryChannelDeleteFailed             = "LastDeliveryChannelDeleteFailedException"
	ExTooManyTags                                 = "TooManyTagsException"
	ExResourceConcurrentModification              = "ResourceConcurrentModificationException"
	ExConformancePackTemplateValidation           = "ConformancePackTemplateValidationException"
	ExOrganizationAccessDenied                    = "OrganizationAccessDeniedException"
	ExNoSuchConfigRuleInConformancePack           = "NoSuchConfigRuleInConformancePackException"
)

// APIError tags a canonical cloudemu error with the Config exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while the code
// mapping still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
