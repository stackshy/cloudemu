package driver

// Amazon Kendra exception names, as they appear in the __type member of a JSON
// 1.1 error body. These are the exact names the aws-sdk-go-v2/service/kendra
// client (and terraform-provider-aws) match on.
const (
	ExValidation           = "ValidationException"
	ExResourceNotFound     = "ResourceNotFoundException"
	ExConflict             = "ConflictException"
	ExResourceAlreadyExist = "ResourceAlreadyExistException"
	ExInternalServer       = "InternalServerException"
)

// APIError tags a canonical cloudemu error with the Kendra exception name it
// should surface as on the wire, so the server renders the exact __type the SDK
// expects rather than a generic code map.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
