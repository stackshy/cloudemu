package driver

// Operation is a google.longrunning.Operation. The emulator returns operations
// that are already complete (Done=true) so SDK pollers terminate immediately.
type Operation struct {
	Name     string
	Done     bool
	Metadata map[string]any
	Response map[string]any
	Error    *OperationError
}

// OperationError mirrors google.rpc.Status on a failed operation.
type OperationError struct {
	Code    int
	Message string
}
