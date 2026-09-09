package driver

// OpenSearch Serverless exception names, as they appear in the __type member of
// a JSON 1.0 error body and the X-Amzn-Errortype header. These are the exact
// names the aws-sdk-go-v2/service/opensearchserverless client matches on.
const (
	ExValidation           = "ValidationException"
	ExResourceNotFound     = "ResourceNotFoundException"
	ExConflict             = "ConflictException"
	ExServiceQuotaExceeded = "ServiceQuotaExceededException"
	ExInternalServer       = "InternalServerException"
)

// APIError tags a canonical cloudemu error with the OpenSearch Serverless
// exception name it should surface as on the wire, so the server renders the
// exact __type the SDK expects rather than a generic code map.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
