package driver

// Amazon MSK (Kafka) modeled exception names. These select the precise
// X-Amzn-Errortype at the wire layer so the SDK deserializes the exact typed
// error the caller expects. See the kafka SDK's types/errors.go.
const (
	ExBadRequest         = "BadRequestException"
	ExNotFound           = "NotFoundException"
	ExConflict           = "ConflictException"
	ExUnauthorized       = "UnauthorizedException"
	ExForbidden          = "ForbiddenException"
	ExInternalServer     = "InternalServerErrorException"
	ExServiceUnavailable = "ServiceUnavailableException"
	ExTooManyRequests    = "TooManyRequestsException"
)

// APIError tags a canonical cloudemu error with the MSK exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while the
// canonical-code mapping still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
