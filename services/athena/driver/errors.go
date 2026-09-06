package driver

// Athena exception names, used to select the precise __type at the wire layer.
// Athena models a small set of exceptions that don't all map one-to-one to
// canonical cloudemu codes (a missing workgroup and a missing database surface
// as different exceptions), so errors carry the exception they concern and the
// server emits it verbatim, matching the typed exceptions the
// aws-sdk-go-v2/service/athena client models so callers can errors.As them.
const (
	ExInvalidRequest   = "InvalidRequestException"
	ExResourceNotFound = "ResourceNotFoundException"
	ExTooManyRequests  = "TooManyRequestsException"
	ExMetadata         = "MetadataException"
	ExInternalServer   = "InternalServerException"
)

// APIError tags a canonical cloudemu error with the Athena exception name it
// concerns, so the server can emit the right __type while GetCode still resolves
// the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
