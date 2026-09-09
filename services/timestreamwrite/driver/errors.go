package driver

// Amazon Timestream Write exception names, as they appear in the __type member
// of a JSON 1.0 error body. These are the exact names the
// aws-sdk-go-v2/service/timestreamwrite client (and terraform-provider-aws)
// match on.
const (
	ExValidation           = "ValidationException"
	ExResourceNotFound     = "ResourceNotFoundException"
	ExConflict             = "ConflictException"
	ExThrottling           = "ThrottlingException"
	ExServiceQuotaExceeded = "ServiceQuotaExceededException"
	ExInternalServer       = "InternalServerException"
)

// APIError tags a canonical cloudemu error with the Timestream exception name it
// should surface as on the wire, so the server renders the exact __type the SDK
// expects rather than a generic code map.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
