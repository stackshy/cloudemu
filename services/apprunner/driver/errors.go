package driver

// App Runner exception names, as they appear in the __type member of a JSON 1.0
// error body. These are the exact names the aws-sdk-go-v2/service/apprunner
// client (and terraform-provider-aws) match on.
const (
	ExInvalidRequest      = "InvalidRequestException"
	ExInvalidState        = "InvalidStateException"
	ExResourceNotFound    = "ResourceNotFoundException"
	ExServiceQuota        = "ServiceQuotaExceededException"
	ExInternalServerError = "InternalServiceErrorException"
)

// APIError tags a canonical cloudemu error with the App Runner exception name it
// should surface as on the wire, so the server renders the exact __type the SDK
// expects rather than a generic code map.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
