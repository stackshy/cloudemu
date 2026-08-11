package driver

// GuardDuty modeled exception names. GuardDuty's REST-JSON API returns a small
// set of exceptions; these select the precise X-Amzn-Errortype at the wire
// layer for exceptions that don't map one-to-one to canonical cloudemu codes.
const (
	ExBadRequest       = "BadRequestException"
	ExConflict         = "ConflictException"
	ExResourceNotFound = "ResourceNotFoundException"
	ExAccessDenied     = "AccessDeniedException"
	ExInternal         = "InternalServerErrorException"
)

// APIError tags a canonical cloudemu error with the GuardDuty exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while the
// canonical-code fallback still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
