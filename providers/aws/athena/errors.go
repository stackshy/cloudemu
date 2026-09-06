package athena

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/athena/driver"
)

// invalidRequest builds an InvalidRequestException-tagged error (bad input or a
// duplicate create — Athena reports both as InvalidRequestException).
func invalidRequest(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidRequest, Err: errors.Newf(errors.InvalidArgument, format, args...)}
}

// notFoundRequest builds an InvalidRequestException-tagged error for a missing
// workgroup / named query / query execution, which real Athena surfaces as
// InvalidRequestException (not ResourceNotFoundException) while keeping a
// NotFound canonical code so status mapping stays correct.
func notFoundRequest(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExInvalidRequest, Err: errors.Newf(errors.NotFound, format, args...)}
}

// resourceNotFound builds a ResourceNotFoundException-tagged error, used by the
// Data Catalog read path (GetDatabase / GetDataCatalog) when a resource is
// absent.
func resourceNotFound(format string, args ...any) error {
	return &driver.APIError{Exception: driver.ExResourceNotFound, Err: errors.Newf(errors.NotFound, format, args...)}
}
