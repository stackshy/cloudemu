package driver

// Glue exception names, used to select the precise X-Amzn-Errortype at the wire
// layer. Glue models several distinct exceptions that don't all map one-to-one
// to canonical cloudemu codes, so errors carry the exception they concern and
// the server emits it verbatim (matching the typed exceptions the
// aws-sdk-go-v2/service/glue client models, so callers can errors.As them).
const (
	ExEntityNotFound             = "EntityNotFoundException"
	ExAlreadyExists              = "AlreadyExistsException"
	ExInvalidInput               = "InvalidInputException"
	ExConcurrentModification     = "ConcurrentModificationException"
	ExResourceNumberLimit        = "ResourceNumberLimitExceededException"
	ExCrawlerNotRunning          = "CrawlerNotRunningException"
	ExInternalService            = "InternalServiceException"
	ExVersionMismatch            = "VersionMismatchException"
	ExConditionCheckFailure      = "ConditionCheckFailureException"
	ExSchemaVersionParseNotFound = "SchemaVersionNotFoundException"
)

// APIError tags a canonical cloudemu error with the Glue exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while GetCode
// still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
