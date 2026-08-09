package driver

// CloudTrail exception names, used to select the precise X-Amzn-Errortype at the
// wire layer. CloudTrail models many distinct exceptions that don't map
// one-to-one to canonical codes, so errors carry the exception they concern.
const (
	ExTrailNotFound             = "TrailNotFoundException"
	ExTrailAlreadyExists        = "TrailAlreadyExistsException"
	ExInvalidTrailName          = "InvalidTrailNameException"
	ExEventDataStoreNotFound    = "EventDataStoreNotFoundException"
	ExEventDataStoreAlreadyEx   = "EventDataStoreAlreadyExistsException"
	ExEventDataStoreARNInvalid  = "EventDataStoreARNInvalidException"
	ExInvalidParameter          = "InvalidParameterException"
	ExInvalidParameterCombo     = "InvalidParameterCombinationException"
	ExResourceNotFound          = "ResourceNotFoundException"
	ExInsufficientS3BucketPolcy = "InsufficientS3BucketPolicyException"
	ExChannelNotFound           = "ChannelNotFoundException"
	ExChannelARNInvalid         = "ChannelARNInvalidException"
	ExChannelAlreadyExists      = "ChannelAlreadyExistsException"
	ExResourceARNNotValid       = "ResourceARNNotValidException"
	ExResourcePolicyNotFound    = "ResourcePolicyNotFoundException"
	ExImportNotFound            = "ImportNotFoundException"
	ExQueryIDNotFound           = "QueryIdNotFoundException"
	ExInactiveEventDataStore    = "InactiveEventDataStoreException"
	ExInvalidQueryStatement     = "InvalidQueryStatementException"
	ExEDSTerminationProtected   = "EventDataStoreTerminationProtectedException"
)

// APIError tags a canonical cloudemu error with the CloudTrail exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while GetCode
// still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
