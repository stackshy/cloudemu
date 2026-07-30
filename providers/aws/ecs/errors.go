package ecs

import cerrors "github.com/stackshy/cloudemu/v2/errors"

// ECS exception names surfaced on the wire. The AWS JSON 1.1 deserializer keys
// off these (via the "__type" body) to select a typed SDK error, so they must
// match the aws-sdk-go-v2/service/ecs exception shape names exactly.
const (
	excClusterNotFound          = "ClusterNotFoundException"
	excServiceNotFound          = "ServiceNotFoundException"
	excClient                   = "ClientException"
	excInvalidParameter         = "InvalidParameterException"
	excClusterContainsServices  = "ClusterContainsServicesException"
	excClusterContainsTasks     = "ClusterContainsTasksException"
	excClusterContainsInstances = "ClusterContainsContainerInstancesException"
	excServer                   = "ServerException"
)

// apiError pairs a canonical cloudemu error with the precise ECS exception name
// the server should surface. It unwraps to the underlying *cerrors.Error so the
// cerrors.IsX predicates keep classifying it, while exposing ECSException() for
// the server's error mapper to pick the operation-appropriate typed exception
// (a NotFound in a cluster context becomes ClusterNotFoundException, in a
// service context ServiceNotFoundException, and so on).
type apiError struct {
	err       *cerrors.Error
	exception string
}

// Error implements the error interface.
func (e *apiError) Error() string { return e.err.Error() }

// ECSException returns the AWS exception name for this error.
func (e *apiError) ECSException() string { return e.exception }

// Unwrap exposes the canonical error so errors.As(&*cerrors.Error) matches and
// cerrors.GetCode reads the right code.
func (e *apiError) Unwrap() error { return e.err }

// apiErrf builds an apiError with a formatted message.
func apiErrf(code cerrors.Code, exception, format string, args ...any) error {
	return &apiError{err: cerrors.Newf(code, format, args...), exception: exception}
}
