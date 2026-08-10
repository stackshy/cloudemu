package driver

// AWS Config exception names, used to select the precise X-Amzn-Errortype at the
// wire layer. Config models many distinct exceptions that don't map one-to-one
// to canonical cloudemu codes, so errors carry the exception they concern.
const (
	ExNoSuchConfigRule                          = "NoSuchConfigRuleException"
	ExNoSuchConfigurationRecorder               = "NoSuchConfigurationRecorderException"
	ExNoSuchDeliveryChannel                     = "NoSuchDeliveryChannelException"
	ExNoSuchBucket                              = "NoSuchBucketException"
	ExNoSuchConfigurationAggregator             = "NoSuchConfigurationAggregatorException"
	ExNoSuchConformancePack                     = "NoSuchConformancePackException"
	ExNoSuchOrganizationConfigRule              = "NoSuchOrganizationConfigRuleException"
	ExNoSuchOrganizationConformancePack         = "NoSuchOrganizationConformancePackException"
	ExNoSuchRemediationConfiguration            = "NoSuchRemediationConfigurationException"
	ExNoSuchRetentionConfiguration              = "NoSuchRetentionConfigurationException"
	ExInvalidParameterValue                     = "InvalidParameterValueException"
	ExInvalidResultToken                        = "InvalidResultTokenException"
	ExInvalidExpression                         = "InvalidExpressionException"
	ExValidation                                = "ValidationException"
	ExResourceInUse                             = "ResourceInUseException"
	ExResourceNotFound                          = "ResourceNotFoundException"
	ExResourceNotDiscovered                     = "ResourceNotDiscoveredException"
	ExMaxNumberOfConfigurationRecordersExceeded = "MaxNumberOfConfigurationRecordersExceededException"
	ExMaxNumberOfDeliveryChannelsExceeded       = "MaxNumberOfDeliveryChannelsExceededException"
	ExMaxNumberOfConfigRulesExceeded            = "MaxNumberOfConfigRulesExceededException"
	ExMaxNumberOfConformancePacksExceeded       = "MaxNumberOfConformancePacksExceededException"
	ExInvalidNextToken                          = "InvalidNextTokenException"
	ExNoAvailableConfigurationRecorder          = "NoAvailableConfigurationRecorderException"
	ExNoAvailableDeliveryChannel                = "NoAvailableDeliveryChannelException"
	ExLastDeliveryChannelDeleteFailed           = "LastDeliveryChannelDeleteFailedException"
	ExTooManyTags                               = "TooManyTagsException"
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
