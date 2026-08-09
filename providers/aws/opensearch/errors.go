package opensearch

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/opensearch/driver"
)

// notFound builds a ResourceNotFoundException-tagged error.
func notFound(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceNotFound,
		Err:       errors.Newf(errors.NotFound, format, args...),
	}
}

// alreadyExists builds a ResourceAlreadyExistsException-tagged error.
func alreadyExists(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExResourceAlreadyExists,
		Err:       errors.Newf(errors.AlreadyExists, format, args...),
	}
}

// validation builds a ValidationException-tagged error.
func validation(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExValidation,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}

// limitExceeded builds a LimitExceededException-tagged error for breaches of a
// documented maximum (e.g. the per-domain tag cap).
func limitExceeded(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExLimitExceeded,
		Err:       errors.Newf(errors.ResourceExhausted, format, args...),
	}
}

// invalidToken builds an InvalidPaginationTokenException-tagged error for a
// corrupt or out-of-range NextToken, so the client learns its token was bad
// instead of silently re-reading the first page.
func invalidToken(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExInvalidPaginationToken,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
	}
}
