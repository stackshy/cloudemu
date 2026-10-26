package driver

// CodeArtifact exception names. These select the precise X-Amzn-Errortype at the
// wire layer.
const (
	ExNotFound     = "ResourceNotFoundException"
	ExValidation   = "ValidationException"
	ExConflict     = "ConflictException"
	ExServiceQuota = "ServiceQuotaExceededException"
	ExInternal     = "InternalServerException"
)

// Resource types a ResourceNotFound, Conflict or ServiceQuotaExceeded error names
// (the ResourceType enum of the CodeArtifact API).
const (
	ResourceTypeDomain         = "domain"
	ResourceTypeRepository     = "repository"
	ResourceTypePackage        = "package"
	ResourceTypePackageVersion = "package-version"
	ResourceTypeAsset          = "asset"
)

// ReasonFieldValidationFailed is the ValidationException reason reported for a
// request field that fails the API's constraints.
const ReasonFieldValidationFailed = "FIELD_VALIDATION_FAILED"

// APIError tags a canonical cloudemu error with the CodeArtifact exception name
// it concerns, so the server can emit the right X-Amzn-Errortype while the
// canonical code still resolves the HTTP status through Unwrap. ResourceID and
// ResourceType identify the resource of a ResourceNotFound, Conflict or
// ServiceQuotaExceeded error and Reason is the ValidationException reason; the
// server maps them to the fields of the same name in the error body.
type APIError struct {
	Exception    string
	Err          error
	ResourceID   string
	ResourceType string
	Reason       string
}

func (e *APIError) Error() string { return e.Err.Error() }
func (e *APIError) Unwrap() error { return e.Err }
