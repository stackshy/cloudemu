package driver

// Step Functions exception names, used to select the precise X-Amzn-Errortype /
// __type at the wire layer. SFN models several distinct exceptions that don't
// map one-to-one to canonical codes, so errors carry the exception they concern.
const (
	ExStateMachineDoesNotExist  = "StateMachineDoesNotExist"
	ExStateMachineAlreadyExists = "StateMachineAlreadyExists"
	ExExecutionDoesNotExist     = "ExecutionDoesNotExist"
	ExExecutionAlreadyExists    = "ExecutionAlreadyExists"
	ExActivityDoesNotExist      = "ActivityDoesNotExist"
	ExActivityAlreadyExists     = "ActivityAlreadyExists"
	ExResourceNotFound          = "ResourceNotFound"
	ExInvalidArn                = "InvalidArn"
	ExInvalidName               = "InvalidName"
	ExInvalidToken              = "InvalidToken"
	ExTaskDoesNotExist          = "TaskDoesNotExist"
	ExTooManyTags               = "TooManyTags"
)

// APIError tags a canonical cloudemu error with the SFN exception name it
// concerns, so the server can emit the right __type while GetCode still
// resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
