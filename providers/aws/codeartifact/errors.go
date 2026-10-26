package codeartifact

import (
	"github.com/stackshy/cloudemu/v2/errors"
	"github.com/stackshy/cloudemu/v2/services/codeartifact/driver"
)

// notFound builds a ResourceNotFoundException naming the missing resource.
func notFound(resourceType, resourceID, format string, args ...any) error {
	return &driver.APIError{
		Exception:    driver.ExNotFound,
		Err:          errors.Newf(errors.NotFound, format, args...),
		ResourceID:   resourceID,
		ResourceType: resourceType,
	}
}

// validation builds a ValidationException for a request field that breaks the
// API's constraints.
func validation(format string, args ...any) error {
	return &driver.APIError{
		Exception: driver.ExValidation,
		Err:       errors.Newf(errors.InvalidArgument, format, args...),
		Reason:    driver.ReasonFieldValidationFailed,
	}
}

// conflict builds a ConflictException for a duplicate resource or an unmet
// precondition, naming the resource it concerns.
func conflict(resourceType, resourceID, format string, args ...any) error {
	return &driver.APIError{
		Exception:    driver.ExConflict,
		Err:          errors.Newf(errors.AlreadyExists, format, args...),
		ResourceID:   resourceID,
		ResourceType: resourceType,
	}
}

// quotaExceeded builds a ServiceQuotaExceededException for a service limit.
func quotaExceeded(resourceType, resourceID, format string, args ...any) error {
	return &driver.APIError{
		Exception:    driver.ExServiceQuota,
		Err:          errors.Newf(errors.ResourceExhausted, format, args...),
		ResourceID:   resourceID,
		ResourceType: resourceType,
	}
}
