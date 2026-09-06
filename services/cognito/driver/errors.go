package driver

// Cognito exception names, used to select the precise __type at the wire layer.
// These match the typed exceptions the aws-sdk-go-v2/service/cognitoidentity
// provider client models, so callers can errors.As them and the Terraform AWS
// provider recognizes a missing pool/client on its delete and refresh paths.
const (
	ExResourceNotFound = "ResourceNotFoundException"
	ExInvalidParameter = "InvalidParameterException"
	ExInternalError    = "InternalErrorException"
	ExTooManyRequests  = "TooManyRequestsException"
	ExLimitExceeded    = "LimitExceededException"
)

// APIError tags a canonical cloudemu error with the Cognito exception name it
// concerns, so the server can emit the right __type while GetCode still resolves
// the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
