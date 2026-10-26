package driver

import (
	stderrors "errors"

	"github.com/stackshy/cloudemu/v2/errors"
)

// KMS exception names that don't map one-to-one onto a canonical cloudemu
// error code. Errors carrying one of these (via APIError) reach the client as
// that exact exception, so an SDK caller can errors.As the typed exception.
const (
	ExTag                             = "TagException"
	ExInvalidArn                      = "InvalidArnException"
	ExInvalidMarker                   = "InvalidMarkerException"
	ExCustomKeyStoreNotFound          = "CustomKeyStoreNotFoundException"
	ExCustomKeyStoreNameInUse         = "CustomKeyStoreNameInUseException"
	ExCustomKeyStoreInvalidState      = "CustomKeyStoreInvalidStateException"
	ExCustomKeyStoreHasCMKs           = "CustomKeyStoreHasCMKsException"
	ExCloudHsmClusterInUse            = "CloudHsmClusterInUseException"
	ExXksProxyURIInUse                = "XksProxyUriInUseException"
	ExXksProxyURIEndpointInUse        = "XksProxyUriEndpointInUseException"
	ExXksProxyVpcEndpointServiceInUse = "XksProxyVpcEndpointServiceInUseException"
	ExXksKeyAlreadyInUse              = "XksKeyAlreadyInUseException"
	ExUnsupportedOperation            = "UnsupportedOperationException"
	ExKMSInvalidState                 = "KMSInvalidStateException"
)

// APIError tags a canonical cloudemu error with the KMS exception name it
// concerns, so the wire layer can emit the exact __type while GetCode/Message
// still resolve through Unwrap.
type APIError struct {
	Exception string
	Err       error
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }

// NewAPIError builds an APIError for exception with a canonical code and a
// formatted message.
func NewAPIError(exception string, code errors.Code, format string, args ...any) error {
	return &APIError{Exception: exception, Err: errors.Newf(code, format, args...)}
}

// sentinelMessageError is a sentinel error with a request-specific message.
type sentinelMessageError struct {
	err      *errors.Error
	sentinel error
}

func (e *sentinelMessageError) Error() string        { return e.err.Error() }
func (e *sentinelMessageError) Unwrap() error        { return e.err }
func (e *sentinelMessageError) Is(target error) bool { return stderrors.Is(target, e.sentinel) }

// WithMessage returns an error that matches errors.Is(err, sentinel) and keeps
// the sentinel's code, but carries its own message, so the client sees the
// exact reason (for example "<key ARN> is unavailable") under the sentinel's
// exception.
func WithMessage(sentinel *errors.Error, format string, args ...any) error {
	return &sentinelMessageError{err: errors.Newf(sentinel.Code, format, args...), sentinel: sentinel}
}
