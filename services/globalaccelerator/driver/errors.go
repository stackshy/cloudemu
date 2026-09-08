package driver

// AWS Global Accelerator exception names, as they appear in the __type member of
// a JSON 1.1 error body. These are the exact names the
// aws-sdk-go-v2/service/globalaccelerator client (and terraform-provider-aws)
// match on.
const (
	ExAcceleratorNotFound        = "AcceleratorNotFoundException"
	ExAcceleratorNotDisabled     = "AcceleratorNotDisabledException"
	ExListenerNotFound           = "ListenerNotFoundException"
	ExEndpointGroupNotFound      = "EndpointGroupNotFoundException"
	ExAssociatedListenerFound    = "AssociatedListenerFoundException"
	ExAssociatedEndpointGroup    = "AssociatedEndpointGroupFoundException"
	ExEndpointGroupAlreadyExists = "EndpointGroupAlreadyExistsException"
	ExInvalidArgument            = "InvalidArgumentException"
	ExLimitExceeded              = "LimitExceededException"
	ExInternalService            = "InternalServiceErrorException"
)

// APIError tags a canonical cloudemu error with the Global Accelerator exception
// name it should surface as on the wire, so the server renders the exact __type
// the SDK expects rather than a generic code map. The canonical code still
// resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
