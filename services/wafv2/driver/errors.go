package driver

// WAFv2 exception names, used to select the precise X-Amzn-Errortype at the wire
// layer. WAFv2 models several distinct exceptions that don't map one-to-one to
// canonical codes, so errors carry the exception they concern.
const (
	ExNonexistentItem   = "WAFNonexistentItemException"
	ExDuplicateItem     = "WAFDuplicateItemException"
	ExOptimisticLock    = "WAFOptimisticLockException"
	ExInvalidParameter  = "WAFInvalidParameterException"
	ExUnavailableEntity = "WAFUnavailableEntityException"
	ExAssociatedItem    = "WAFAssociatedItemException"
)

// APIError tags a canonical cloudemu error with the WAFv2 exception name it
// concerns, so the server can emit the right X-Amzn-Errortype while GetCode
// still resolves the HTTP status through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
