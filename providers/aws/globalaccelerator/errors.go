package globalaccelerator

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/globalaccelerator/driver"
)

// tagged builds an APIError pairing a canonical code with the exact Global
// Accelerator exception name to surface on the wire.
func tagged(exception string, code errors.Code, format string, args ...any) error {
	return &driver.APIError{
		Exception: exception,
		Err:       errors.Newf(code, format, args...),
	}
}

func acceleratorNotFound(arn string) error {
	return tagged(driver.ExAcceleratorNotFound, errors.NotFound, "accelerator %s not found", arn)
}

func listenerNotFound(arn string) error {
	return tagged(driver.ExListenerNotFound, errors.NotFound, "listener %s not found", arn)
}

func endpointGroupNotFound(arn string) error {
	return tagged(driver.ExEndpointGroupNotFound, errors.NotFound, "endpoint group %s not found", arn)
}

func acceleratorNotDisabled(arn string) error {
	return tagged(driver.ExAcceleratorNotDisabled, errors.FailedPrecondition,
		"accelerator %s must be disabled before it can be deleted", arn)
}

func associatedListenerFound(arn string) error {
	return tagged(driver.ExAssociatedListenerFound, errors.FailedPrecondition,
		"accelerator %s still has associated listeners", arn)
}

func associatedEndpointGroupFound(arn string) error {
	return tagged(driver.ExAssociatedEndpointGroup, errors.FailedPrecondition,
		"listener %s still has associated endpoint groups", arn)
}

func invalidArgument(format string, args ...any) error {
	return tagged(driver.ExInvalidArgument, errors.InvalidArgument, format, args...)
}
